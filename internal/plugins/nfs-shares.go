/******************************************************************************
*
*  Copyright 2019-2020 SAP SE
*
*  Licensed under the Apache License, Version 2.0 (the "License");
*  you may not use this file except in compliance with the License.
*  You may obtain a copy of the License at
*
*      http://www.apache.org/licenses/LICENSE-2.0
*
*  Unless required by applicable law or agreed to in writing, software
*  distributed under the License is distributed on an "AS IS" BASIS,
*  WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
*  See the License for the specific language governing permissions and
*  limitations under the License.
*
******************************************************************************/

package plugins

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"regexp"
	"time"

	"github.com/gophercloud/gophercloud/v2"
	"github.com/gophercloud/gophercloud/v2/openstack"
	"github.com/gophercloud/gophercloud/v2/openstack/sharedfilesystems/v2/shares"
	"github.com/prometheus/common/model"
	"github.com/sapcc/go-api-declarations/castellum"
	"github.com/sapcc/go-bits/logg"
	"github.com/sapcc/go-bits/promquery"

	"github.com/sapcc/castellum/internal/core"
	"github.com/sapcc/castellum/internal/db"
)

type assetManagerNFS struct {
	Manila       *gophercloud.ServiceClient
	Discovery    promquery.Client
	ShareMetrics *promquery.BulkQueryCache[manilaShareMetricsKey, manilaShareMetrics]
}

func init() {
	core.AssetManagerRegistry.Add(func() core.AssetManager { return &assetManagerNFS{} })
}

// PluginTypeID implements the core.AssetManager interface.
func (m *assetManagerNFS) PluginTypeID() string { return "nfs-shares" }

// Init implements the core.AssetManager interface.
func (m *assetManagerNFS) Init(provider core.ProviderClient) (err error) {
	m.Manila, err = provider.CloudAdminClient(openstack.NewSharedFileSystemV2)
	if err != nil {
		return err
	}
	m.Manila.Microversion = "2.64" // for "force" field on .Extend(), requires Manila at least on Xena

	promClient, err := promquery.ConfigFromEnv("CASTELLUM_NFS_PROMETHEUS").Connect()
	if err != nil {
		return err
	}
	m.ShareMetrics = promquery.NewBulkQueryCache(manilaShareQueries, 30*time.Second, promClient)

	m.Discovery, err = promquery.ConfigFromEnv("CASTELLUM_NFS_DISCOVERY_PROMETHEUS").Connect()
	return err
}

// InfoForAssetType implements the core.AssetManager interface.
func (m *assetManagerNFS) InfoForAssetType(assetType db.AssetType) *core.AssetTypeInfo {
	if assetType == "nfs-shares" {
		return &core.AssetTypeInfo{
			AssetType:    assetType,
			UsageMetrics: []castellum.UsageMetric{castellum.SingularUsageMetric},
		}
	}
	return nil
}

// CheckResourceAllowed implements the core.AssetManager interface.
func (m *assetManagerNFS) CheckResourceAllowed(ctx context.Context, assetType db.AssetType, scopeUUID, configJSON string, existingResources map[db.AssetType]struct{}) error {
	if configJSON != "" {
		return core.ErrNoConfigurationAllowed
	}

	return nil
}

// ListAssets implements the core.AssetManager interface.
func (m *assetManagerNFS) ListAssets(ctx context.Context, res db.Resource) ([]string, error) {
	// shares are discovered via Prometheus metrics since that is way faster than
	// going through the Manila API
	vector, err := m.Discovery.GetVector(ctx, fmt.Sprintf(
		`count by (id) (openstack_manila_shares_size_gauge{project_id="%s",status!="error"})`,
		res.ScopeUUID,
	))
	if err != nil {
		return nil, fmt.Errorf("while discovering shares for project %s in Prometheus: %w", res.ScopeUUID, err)
	}

	var allShareIDs []string
	for _, sample := range vector {
		shareID := string(sample.Metric["id"])

		// evaluate exclusion rules based on Prometheus metrics
		metrics, err := m.ShareMetrics.Get(ctx, manilaShareMetricsKey{
			ProjectUUID: res.ScopeUUID,
			ShareUUID:   shareID,
		})
		if err != nil {
			return nil, err
		}
		if metrics.ExclusionReason == "" {
			allShareIDs = append(allShareIDs, shareID)
		} else {
			logg.Debug("ignoring share %s because of %s", shareID, metrics.ExclusionReason)
		}
	}

	return allShareIDs, nil
}

var (
	sizeInconsistencyErrorRx = regexp.MustCompile(`New size for (?:extend must be greater|shrink must be less) than current size.*\(current: ([0-9]+), (?:new|extended): ([0-9]+)\)`)
	quotaErrorRx             = regexp.MustCompile(`Requested share exceeds allowed project/user or share type \S+ quota.|\bShareReplicaSizeExceedsAvailableQuota\b`)
	shareStatusErrorRx       = regexp.MustCompile(`Invalid share: .* current status is: error.`)
)

// SetAssetSize implements the core.AssetManager interface.
func (m *assetManagerNFS) SetAssetSize(ctx context.Context, res db.Resource, assetUUID string, oldSize, newSize uint64) (castellum.OperationOutcome, error) {
	err := m.resize(ctx, assetUUID, oldSize, newSize /* useReverseOperation = */, false)
	if err != nil {
		match := sizeInconsistencyErrorRx.FindStringSubmatch(err.Error())
		if match != nil {
			// ignore idiotic complaints about the share already having the size we
			// want to resize to
			if match[1] == match[2] {
				return castellum.OperationOutcomeSucceeded, nil
			}

			// We only rely on sizes reported by NetApp. But bugs in the Manila API may
			// cause it to have a different expectation of how big the share is, therefore
			// rejecting shrink/extend requests because it thinks they go in the wrong
			// direction. In this case, we try the opposite direction to see if it helps.
			err2 := m.resize(ctx, assetUUID, oldSize, newSize /* useReverseOperation = */, true)
			if err2 == nil {
				return castellum.OperationOutcomeSucceeded, nil
			}
			// If not successful, still return the original error (to avoid confusion).
		}

		// If the resize fails because of missing quota or because the share is in
		// status "error", it's the user's fault, not ours.
		if quotaErrorRx.MatchString(err.Error()) || shareStatusErrorRx.MatchString(err.Error()) {
			return castellum.OperationOutcomeFailed, err
		}
		return castellum.OperationOutcomeErrored, err
	}
	return castellum.OperationOutcomeSucceeded, nil
}

func (m *assetManagerNFS) resize(ctx context.Context, assetUUID string, oldSize, newSize uint64, useReverseOperation bool) error {
	if newSize > math.MaxInt { // we need to convert `newSize` to int to satisfy the Gophercloud API
		return fmt.Errorf("newSize out of bounds: %d", newSize)
	}
	if (oldSize < newSize && !useReverseOperation) || (oldSize >= newSize && useReverseOperation) {
		return shares.Extend(ctx, m.Manila, assetUUID, shareExtendOpts{NewSize: int(newSize), Force: true}).ExtractErr()
	}
	return shares.Shrink(ctx, m.Manila, assetUUID, shares.ShrinkOpts{NewSize: int(newSize)}).ExtractErr()
}

// GetAssetStatus implements the core.AssetManager interface.
func (m *assetManagerNFS) GetAssetStatus(ctx context.Context, res db.Resource, assetUUID string, previousStatus *core.AssetStatus) (core.AssetStatus, error) {
	// query Prometheus metrics for size and usage
	metrics, err := m.ShareMetrics.Get(ctx, manilaShareMetricsKey{
		ProjectUUID: res.ScopeUUID,
		ShareUUID:   assetUUID,
	})
	if err != nil {
		return core.AssetStatus{}, err
	}
	if metrics.ExclusionReason != "" {
		// defense in depth: this share should already have been ignored during ListAssets
		return core.AssetStatus{}, core.AssetNotFoundError{InnerError: fmt.Errorf("ignoring because of %s", metrics.ExclusionReason)}
	}

	// if there are no metrics for this share, we can check Manila to see if the share was deleted in the meantime
	if metrics.SizeGiB == nil || metrics.UsedGiB == nil {
		_, err := shares.Get(ctx, m.Manila, assetUUID).Extract()
		if gophercloud.ResponseCodeIs(err, http.StatusNotFound) {
			return core.AssetStatus{}, core.AssetNotFoundError{InnerError: fmt.Errorf("share not found in Manila: %w", err)}
		} else {
			return core.AssetStatus{}, fmt.Errorf("incomplete metrics for share %q: %#v", assetUUID, metrics)
		}
	}

	status := core.AssetStatus{
		Size:              *metrics.SizeGiB,
		StrictMinimumSize: &metrics.MinSizeGiB,
		Usage:             castellum.UsageValues{castellum.SingularUsageMetric: *metrics.UsedGiB},
	}

	// when size has changed compared to last time, double-check with the Manila
	// API (this call is expensive, so we only do it when really necessary)
	if previousStatus == nil || previousStatus.Size != status.Size {
		share, err := shares.Get(ctx, m.Manila, assetUUID).Extract()
		if err != nil {
			return core.AssetStatus{}, fmt.Errorf("cannot get status of share %s from Manila API: %w", assetUUID, err)
		}
		if share.Size < 0 || uint64(share.Size) != status.Size { //nolint:gosec // we cannot store exabytes and negative check is done
			return core.AssetStatus{}, fmt.Errorf(
				"inconsistent size reports for share %s: Prometheus says %d GiB, Manila says %d GiB",
				assetUUID, status.Size, share.Size)
		}
	}

	return status, nil
}

// shareExtendOpts is like shares.ExtendOpts, but supports the new "force" option.
// TODO: merge into upstream
type shareExtendOpts struct {
	NewSize int  `json:"new_size"`
	Force   bool `json:"force"`
}

// ToShareExtendMap implements the shares.ExtendOptsBuilder interface.
func (opts shareExtendOpts) ToShareExtendMap() (map[string]any, error) {
	return gophercloud.BuildRequestBody(opts, "extend")
}

////////////////////////////////////////////////////////////////////////////////
// type declarations and configuration for promquery.BulkQueryCache

const (
	manilaExclusionReasonsQuery = `max by (project_id, share_id, reason) (manila_share_exclusion_reasons_for_castellum{reason!=""} == 1)`

	manilaSizeBytesQuery    = `max by (project_id, share_id) (manila_share_size_bytes_for_castellum        {volume_type!="dp",volume_state!="offline"})`
	manilaUsedBytesQuery    = `max by (project_id, share_id) (manila_share_used_bytes_for_castellum        {volume_type!="dp",volume_state!="offline"})`
	manilaMinSizeBytesQuery = `max by (project_id, share_id) (manila_share_minimal_size_bytes_for_castellum{volume_type!="dp",volume_state!="offline"})`
)

type manilaShareMetricsKey struct {
	ProjectUUID string
	ShareUUID   string
}

func manilaShareMetricsKeyer(sample *model.Sample) manilaShareMetricsKey {
	return manilaShareMetricsKey{
		ProjectUUID: string(sample.Metric["project_id"]),
		ShareUUID:   string(sample.Metric["share_id"]),
	}
}

type manilaShareMetrics struct {
	ExclusionReason string
	SizeGiB         *uint64
	UsedGiB         *float64
	MinSizeGiB      uint64
}

var (
	manilaShareQueries = []promquery.BulkQuery[manilaShareMetricsKey, manilaShareMetrics]{
		{
			Query:       manilaExclusionReasonsQuery,
			Description: "Manila share exclusion reasons",
			Keyer:       manilaShareMetricsKeyer,
			Filler: func(entry *manilaShareMetrics, sample *model.Sample) {
				entry.ExclusionReason = string(sample.Metric["reason"])
			},
			ZeroResultsIsNotAnError: true, // the specific setups that warrant exclusion may not exist everywhere
		},
		{
			Query:       manilaSizeBytesQuery,
			Description: "Manila share size bytes",
			Keyer:       manilaShareMetricsKeyer,
			Filler: func(entry *manilaShareMetrics, sample *model.Sample) {
				entry.SizeGiB = pointerTo(uint64(math.Round(asGigabytes(sample.Value))))
			},
		},
		{
			Query:       manilaMinSizeBytesQuery,
			Description: "Manila share minimum size bytes",
			Keyer:       manilaShareMetricsKeyer,
			Filler: func(entry *manilaShareMetrics, sample *model.Sample) {
				entry.MinSizeGiB = uint64(math.Ceil(asGigabytes(sample.Value)))
			},
		},
		{
			Query:       manilaUsedBytesQuery,
			Description: "Manila share used bytes",
			Keyer:       manilaShareMetricsKeyer,
			Filler: func(entry *manilaShareMetrics, sample *model.Sample) {
				entry.UsedGiB = pointerTo(asGigabytes(sample.Value))
			},
		},
	}
)

func asGigabytes(bytes model.SampleValue) float64 {
	return float64(bytes) / (1 << 30)
}

func pointerTo[T any](value T) *T {
	return &value
}
