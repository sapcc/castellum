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
	"regexp"
	"strings"
	"time"

	"github.com/gophercloud/gophercloud"
	"github.com/gophercloud/gophercloud/openstack"
	"github.com/gophercloud/gophercloud/openstack/sharedfilesystems/v2/shares"
	prom_api "github.com/prometheus/client_golang/api"
	prom_v1 "github.com/prometheus/client_golang/api/prometheus/v1"
	"github.com/prometheus/common/model"
	"github.com/sapcc/go-bits/logg"
	"github.com/sapcc/go-bits/osext"

	"github.com/sapcc/castellum/internal/core"
	"github.com/sapcc/castellum/internal/db"
)

var (
	nfsGroupRx = regexp.MustCompile(`^[A-Za-z0-9-]+$`)
)

type assetTypeNFS struct {
	AllShares bool
	GroupName string
}

func (m *assetManagerNFS) parseAssetType(assetType db.AssetType) *assetTypeNFS {
	if assetType == "nfs-shares" {
		return &assetTypeNFS{AllShares: true}
	}
	if strings.HasPrefix(string(assetType), "nfs-shares-group:") {
		groupName := strings.TrimPrefix(string(assetType), "nfs-shares-group:")
		if nfsGroupRx.MatchString(groupName) {
			return &assetTypeNFS{AllShares: false, GroupName: groupName}
		}
	}
	return nil
}

type assetManagerNFS struct {
	Manila     *gophercloud.ServiceClient
	Prometheus prom_v1.API
}

func init() {
	core.RegisterAssetManagerFactory("nfs-shares", func(provider core.ProviderClient) (core.AssetManager, error) {
		manila, err := provider.CloudAdminClient(openstack.NewSharedFileSystemV2)
		if err != nil {
			return nil, err
		}
		manila.Microversion = "2.64" //for "force" field on .Extend(), requires Manila at least on Xena

		prometheusURL := osext.MustGetenv("CASTELLUM_NFS_PROMETHEUS_URL")
		promClient, err := prom_api.NewClient(prom_api.Config{Address: prometheusURL})
		if err != nil {
			return nil, fmt.Errorf("cannot connect to Prometheus at %s: %s",
				prometheusURL, err.Error())
		}

		return &assetManagerNFS{manila, prom_v1.NewAPI(promClient)}, nil
	})
}

//InfoForAssetType implements the core.AssetManager interface.
func (m *assetManagerNFS) InfoForAssetType(assetType db.AssetType) *core.AssetTypeInfo {
	if m.parseAssetType(assetType) != nil {
		return &core.AssetTypeInfo{
			AssetType:    assetType,
			UsageMetrics: []db.UsageMetric{db.SingularUsageMetric},
		}
	}
	return nil
}

//CheckResourceAllowed implements the core.AssetManager interface.
func (m *assetManagerNFS) CheckResourceAllowed(assetType db.AssetType, scopeUUID string, configJSON string, existingResources []db.AssetType) error {
	if configJSON != "" {
		return core.ErrNoConfigurationAllowed
	}

	parsed := m.parseAssetType(assetType)
	for _, otherAssetType := range existingResources {
		parsedOther := m.parseAssetType(otherAssetType)
		if parsedOther != nil && (parsed.AllShares != parsedOther.AllShares) {
			return fmt.Errorf("cannot create a %q resource because of possible contradiction with existing %q resource",
				string(assetType), string(otherAssetType))
		}
	}

	return nil
}

//ListAssets implements the core.AssetManager interface.
func (m *assetManagerNFS) ListAssets(res db.Resource) ([]string, error) {
	assetType := m.parseAssetType(res.AssetType)

	page := 0
	pageSize := 1000
	var shareIDs []string

	//NOTE: Since Manila uses a shitty pagination strategy (limit/offset instead
	//of marker), we could miss existing shares or observe them doubly if the
	//pages shift around (due to creation or deletion of shares) between
	//requests. Therefore we increment the offset by `pageSize-10` instead of
	//`pageSize` between pages, so that pages overlap by 10 items. This decreases
	//the chance of us missing items.
	wasSeen := make(map[string]bool)

	for {
		opts := shares.ListOpts{
			ProjectID:  res.ScopeUUID,
			AllTenants: true,
			Limit:      pageSize,
			Offset:     page * (pageSize - 10),
		}
		if !assetType.AllShares {
			opts.Metadata = map[string]string{"autoscaling_group": assetType.GroupName}
		}

		p, err := shares.ListDetail(m.Manila, opts).AllPages()
		if err != nil {
			return nil, err
		}

		s, err := shares.ExtractShares(p)
		if err != nil {
			return nil, err
		}

		if len(s) > 0 {
			for _, share := range s {
				if m.ignoreShare(share) {
					continue
				}
				if !wasSeen[share.ID] {
					shareIDs = append(shareIDs, share.ID)
					wasSeen[share.ID] = true
				}
			}
			page++
		} else {
			//last page reached
			return shareIDs, nil
		}
	}
}

func (m *assetManagerNFS) ignoreShare(share shares.Share) bool {
	//ignore shares in status "error" (we won't be able to resize them anyway)
	if share.Status == "error" {
		return true
	}

	//ignore "shares" that are actually snapmirror targets (sapcc-specific
	//extension); old-style check: check for the "snapmirror" metadata key
	//
	//NOTE: Just because it's the "old-style check" doesn't mean we can remove
	//this without careful thought. As of Dec 2020, some snapmirrors are only
	//detected with the old-style check. And vice versa as well: We built the new
	//check because some snapmirrors are only detected by it, not by the old one.
	if snapmirrorStr, ok := share.Metadata["snapmirror"]; ok {
		if snapmirrorStr == "1" {
			return true
		}
	}

	//ignore "shares" that are actually snapmirror targets (sapcc-specific
	//extension); new-style check: check for volume_type!="dp" label on share
	//metrics (it's possible that we have both metrics with volume_type="dp" and
	//other volume_type values, in which case we will only use the non-dp
	//metrics)
	query := fmt.Sprintf(`netapp_volume_total_bytes{project_id=%q,share_id=%q}`, share.ProjectID, share.ID)
	resultVector, err := prometheusGetVector(m.Prometheus, query)
	if err != nil {
		logg.Error("cannot check volume_type for share %q: %s", share.ID, err.Error())
	}
	hasDPMetrics := false
	hasNonDPMetrics := false
	for _, sample := range resultVector {
		if sample.Metric["volume_type"] == "dp" {
			hasDPMetrics = true
		} else {
			hasNonDPMetrics = true
		}
	}
	if hasDPMetrics && !hasNonDPMetrics {
		return true
	}
	//NOTE: Not having any useful metrics at all is not a valid reason for
	//ignoring the share. If we lack metrics about a share, we want to be alerted
	//by the failing scrape.

	return false
}

var (
	sizeInconsistencyErrorRx = regexp.MustCompile(`New size for (?:extend must be greater|shrink must be less) than current size.*\(current: ([0-9]+), (?:new|extended): ([0-9]+)\)`)
	quotaErrorRx             = regexp.MustCompile(`Requested share exceeds allowed project/user or share type \S+ quota.`)
	shareStatusErrorRx       = regexp.MustCompile(`Invalid share: .* current status is: error.`)
)

//SetAssetSize implements the core.AssetManager interface.
func (m *assetManagerNFS) SetAssetSize(res db.Resource, assetUUID string, oldSize, newSize uint64) (db.OperationOutcome, error) {
	err := m.resize(assetUUID, oldSize, newSize /* useReverseOperation = */, false)
	if err != nil {
		match := sizeInconsistencyErrorRx.FindStringSubmatch(err.Error())
		if match != nil {
			//ignore idiotic complaints about the share already having the size we
			//want to resize to
			if match[1] == match[2] {
				return db.OperationOutcomeSucceeded, nil
			}

			//We only rely on sizes reported by NetApp. But bugs in the Manila API may
			//cause it to have a different expectation of how big the share is, therefore
			//rejecting shrink/extend requests because it thinks they go in the wrong
			//direction. In this case, we try the opposite direction to see if it helps.
			err2 := m.resize(assetUUID, oldSize, newSize /* useReverseOperation = */, true)
			if err2 == nil {
				return db.OperationOutcomeSucceeded, nil
			}
			//If not successful, still return the original error (to avoid confusion).
		}

		//If the resize fails because of missing quota or because the share is in
		//status "error", it's the user's fault, not ours.
		if quotaErrorRx.MatchString(err.Error()) || shareStatusErrorRx.MatchString(err.Error()) {
			return db.OperationOutcomeFailed, err
		}
		return db.OperationOutcomeErrored, err
	}
	return db.OperationOutcomeSucceeded, nil
}

func (m *assetManagerNFS) resize(assetUUID string, oldSize, newSize uint64, useReverseOperation bool) error {
	if newSize > math.MaxInt { // we need to convert `newSize` to int to satisfy the Gophercloud API
		return fmt.Errorf("newSize out of bounds: %d", newSize)
	}
	if (oldSize < newSize && !useReverseOperation) || (oldSize >= newSize && useReverseOperation) {
		return shares.Extend(m.Manila, assetUUID, shareExtendOpts{NewSize: int(newSize), Force: true}).ExtractErr()
	}
	return shares.Shrink(m.Manila, assetUUID, shares.ShrinkOpts{NewSize: int(newSize)}).ExtractErr()
}

//GetAssetStatus implements the core.AssetManager interface.
func (m *assetManagerNFS) GetAssetStatus(res db.Resource, assetUUID string, previousStatus *core.AssetStatus) (core.AssetStatus, error) {
	//check status in Prometheus
	var bytesReservedBySnapshots, bytesUsed, bytesUsedBySnapshots, snapshotReservePercent float64
	bytesTotal, err := m.getMetricForShare("netapp_volume_total_bytes", res.ScopeUUID, assetUUID)
	if err == nil {
		bytesReservedBySnapshots, err = m.getMetricForShare("netapp_volume_snapshot_reserved_bytes", res.ScopeUUID, assetUUID)
		if err == nil {
			bytesUsed, err = m.getMetricForShare("netapp_volume_used_bytes", res.ScopeUUID, assetUUID)
			if err == nil {
				bytesUsedBySnapshots, err = m.getMetricForShare("netapp_volume_snapshot_used_bytes", res.ScopeUUID, assetUUID)
				if err == nil {
					snapshotReservePercent, err = m.getMetricForShare("netapp_volume_percentage_snapshot_reserve", res.ScopeUUID, assetUUID)
				}
			}
		}
	}
	if err != nil {
		if _, ok := err.(emptyPrometheusResultErr); ok {
			//check if the share still exists in the backend
			_, getErr := shares.Get(m.Manila, assetUUID).Extract()
			if _, ok := getErr.(gophercloud.ErrDefault404); ok {
				return core.AssetStatus{}, core.AssetNotFoundErr{InnerError: fmt.Errorf("share not found in Manila: %s", getErr.Error())}
			}
		}
		return core.AssetStatus{}, err
	}

	//compute asset status from Prometheus metrics
	//
	//Option 1: For old shares, we have 5% snapshot reserve that gets allocated
	//*AS PART OF* the target share size, so we need to count the snapshot
	//reserve into the size and the snapshot usage into the usage, i.e.
	//
	//    size  = total + reserved_by_snapshots
	//    usage = used  + max(reserved_by_snapshots, used_by_snapshots)
	//
	//Option 2: For newer shares, we have a much larger snapshot reserve (usually
	//50%) that gets allocated *IN ADDITION TO* the target share size, and
	//therefore snapshot usage usually does not eat into the main share size, i.e.
	//
	//    size  = total
	//    usage = used
	//
	//TODO Remove option 1 once all shares have migrated to the new layout.
	var status core.AssetStatus
	if snapshotReservePercent == 5.0 {
		//option 1
		if bytesUsedBySnapshots < bytesReservedBySnapshots {
			bytesUsedBySnapshots = bytesReservedBySnapshots
		}
		sizeBytes := bytesTotal + bytesReservedBySnapshots
		usageBytes := bytesUsed + bytesUsedBySnapshots
		usageGiB := usageBytes / 1024 / 1024 / 1024
		if usageBytes <= 0 {
			usageGiB = 0
		}
		status = core.AssetStatus{
			Size:  uint64(math.Round(sizeBytes / 1024 / 1024 / 1024)),
			Usage: db.UsageValues{db.SingularUsageMetric: usageGiB},
		}
	} else {
		//option 2
		usageGiB := bytesUsed / 1024 / 1024 / 1024
		if bytesUsed <= 0 {
			usageGiB = 0
		}
		status = core.AssetStatus{
			Size:  uint64(math.Round(bytesTotal / 1024 / 1024 / 1024)),
			Usage: db.UsageValues{db.SingularUsageMetric: usageGiB},
		}
	}

	//when size has changed compared to last time, double-check with the Manila
	//API (this call is expensive, so we only do it when really necessary)
	if previousStatus == nil || previousStatus.Size != status.Size {
		share, err := shares.Get(m.Manila, assetUUID).Extract()
		if err != nil {
			return core.AssetStatus{}, fmt.Errorf(
				"cannot get status of share %s from Manila API: %s",
				assetUUID, err.Error())
		}
		if uint64(share.Size) != status.Size {
			return core.AssetStatus{}, fmt.Errorf(
				"inconsistent size reports for share %s: Prometheus says %d GiB, Manila says %d GiB",
				assetUUID, status.Size, share.Size)
		}
	}

	return status, nil
}

type emptyPrometheusResultErr struct {
	Query string
}

func (e emptyPrometheusResultErr) Error() string {
	return fmt.Sprintf("Prometheus query returned empty result: %s", e.Query)
}

func (m *assetManagerNFS) getMetricForShare(metric, projectUUID, shareUUID string) (float64, error) {
	//NOTE: The `max by (share_id)` is necessary for when a share is being
	//migrated to another shareserver and thus appears in the metrics twice.
	query := fmt.Sprintf(`max by (share_id) (%s{project_id=%q,share_id=%q,volume_type!="dp"})`,
		metric, projectUUID, shareUUID)
	return prometheusGetSingleValue(m.Prometheus, query)
}

func prometheusGetVector(api prom_v1.API, queryStr string) (model.Vector, error) {
	value, warnings, err := api.Query(context.Background(), queryStr, time.Now())
	for _, warning := range warnings {
		logg.Info("Prometheus query produced warning: %s", warning)
	}
	if err != nil {
		return nil, fmt.Errorf("could not execute Prometheus query: %s: %s", queryStr, err.Error())
	}
	resultVector, ok := value.(model.Vector)
	if !ok {
		return nil, fmt.Errorf("could not execute Prometheus query: %s: unexpected type %T", queryStr, value)
	}
	return resultVector, nil
}

func prometheusGetSingleValue(api prom_v1.API, queryStr string) (float64, error) {
	resultVector, err := prometheusGetVector(api, queryStr)
	if err != nil {
		return 0, err
	}

	switch resultVector.Len() {
	case 0:
		return 0, emptyPrometheusResultErr{Query: queryStr}
	case 1:
		return float64(resultVector[0].Value), nil
	default:
		//suppress the log message when all values are the same (this can happen
		//when an adventurous Prometheus configuration causes the NetApp exporter
		//to be scraped twice)
		firstValue := resultVector[0].Value
		allTheSame := true
		for _, entry := range resultVector {
			if firstValue != entry.Value {
				allTheSame = false
				break
			}
		}
		if !allTheSame {
			logg.Info("Prometheus query returned more than one result: %s (only the first value will be used)", queryStr)
		}
		return float64(resultVector[0].Value), nil
	}
}

//shareExtendOpts is like shares.ExtendOpts, but supports the new "force" option.
//TODO: merge into upstream
type shareExtendOpts struct {
	NewSize int  `json:"new_size"`
	Force   bool `json:"force"`
}

// ToShareExtendMap implements the shares.ExtendOptsBuilder interface.
func (opts shareExtendOpts) ToShareExtendMap() (map[string]interface{}, error) {
	return gophercloud.BuildRequestBody(opts, "extend")
}
