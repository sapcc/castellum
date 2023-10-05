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
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"regexp"
	"strings"

	"github.com/gophercloud/gophercloud"
	"github.com/gophercloud/gophercloud/openstack"
	"github.com/gophercloud/gophercloud/openstack/sharedfilesystems/v2/shares"
	"github.com/sapcc/go-api-declarations/castellum"
	"github.com/sapcc/go-bits/errext"
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
	Manila       *gophercloud.ServiceClient
	ScoutBaseURL string
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
	m.Manila.Microversion = "2.64" //for "force" field on .Extend(), requires Manila at least on Xena

	m.ScoutBaseURL, err = osext.NeedGetenv("CASTELLUM_NFS_NETAPP_SCOUT_URL")
	return err
}

// InfoForAssetType implements the core.AssetManager interface.
func (m *assetManagerNFS) InfoForAssetType(assetType db.AssetType) *core.AssetTypeInfo {
	if m.parseAssetType(assetType) != nil {
		return &core.AssetTypeInfo{
			AssetType:    assetType,
			UsageMetrics: []castellum.UsageMetric{castellum.SingularUsageMetric},
		}
	}
	return nil
}

// CheckResourceAllowed implements the core.AssetManager interface.
func (m *assetManagerNFS) CheckResourceAllowed(assetType db.AssetType, scopeUUID, configJSON string, existingResources map[db.AssetType]struct{}) error {
	if configJSON != "" {
		return core.ErrNoConfigurationAllowed
	}

	parsed := m.parseAssetType(assetType)
	for otherAssetType := range existingResources {
		parsedOther := m.parseAssetType(otherAssetType)
		if parsedOther != nil && (parsed.AllShares != parsedOther.AllShares) {
			return fmt.Errorf("cannot create a %q resource because of possible contradiction with existing %q resource",
				string(assetType), string(otherAssetType))
		}
	}

	return nil
}

// ListAssets implements the core.AssetManager interface.
func (m *assetManagerNFS) ListAssets(ctx context.Context, res db.Resource) ([]string, error) {
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
				isIgnored, err := m.ignoreShare(ctx, share)
				if err != nil {
					return nil, err
				}
				if isIgnored {
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

func (m *assetManagerNFS) ignoreShare(ctx context.Context, share shares.Share) (bool, error) {
	//ignore shares in status "error" (we won't be able to resize them anyway)
	if share.Status == "error" {
		logg.Debug("ignoring share %s because of status = error", share.ID)
		return true, nil
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
			logg.Debug("ignoring share %s because of snapmirror = 1", share.ID)
			return true, nil
		}
	}

	//There are further exclusion rules that are based on Prometheus metrics and
	//thus evaluated in the netapp-scout.
	path := fmt.Sprintf("v1/projects/%s/shares/%s/exclusion-reasons", share.ProjectID, share.ID)
	var exclusionReasons map[string]bool
	err := m.queryScout(ctx, path, &exclusionReasons, nil)
	if err != nil {
		return false, err
	}
	for reason, isExcluded := range exclusionReasons {
		if isExcluded {
			logg.Debug("ignoring share %s because of %s", share.ID, reason)
			return true, nil
		}
	}

	return false, nil
}

func (m *assetManagerNFS) queryScout(ctx context.Context, path string, data any, actionOn404 func() error) error {
	url := strings.TrimSuffix(m.ScoutBaseURL, "/") + "/" + strings.TrimPrefix(path, "/")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		return fmt.Errorf("could not GET %s: %w", url, err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("could not GET %s: %w", url, err)
	}
	defer resp.Body.Close()

	buf, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("could not GET %s: %w", url, err)
	}

	if resp.StatusCode == http.StatusNotFound && actionOn404 != nil {
		//when netapp-scout reports a 404, we can sometimes return a more specific
		//error than a generic HTTP request error
		err := actionOn404()
		if err != nil {
			return err
		}
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("could not GET %s: expected 200 OK, but got %d and response: %q", url, resp.StatusCode, strings.TrimSpace(string(buf)))
	}

	err = json.Unmarshal(buf, data)
	if err != nil {
		return fmt.Errorf("could not GET %s: %w", url, err)
	}
	return nil
}

var (
	sizeInconsistencyErrorRx = regexp.MustCompile(`New size for (?:extend must be greater|shrink must be less) than current size.*\(current: ([0-9]+), (?:new|extended): ([0-9]+)\)`)
	quotaErrorRx             = regexp.MustCompile(`Requested share exceeds allowed project/user or share type \S+ quota.|\bShareReplicaSizeExceedsAvailableQuota\b`)
	shareStatusErrorRx       = regexp.MustCompile(`Invalid share: .* current status is: error.`)
)

// SetAssetSize implements the core.AssetManager interface.
func (m *assetManagerNFS) SetAssetSize(res db.Resource, assetUUID string, oldSize, newSize uint64) (castellum.OperationOutcome, error) {
	err := m.resize(assetUUID, oldSize, newSize /* useReverseOperation = */, false)
	if err != nil {
		match := sizeInconsistencyErrorRx.FindStringSubmatch(err.Error())
		if match != nil {
			//ignore idiotic complaints about the share already having the size we
			//want to resize to
			if match[1] == match[2] {
				return castellum.OperationOutcomeSucceeded, nil
			}

			//We only rely on sizes reported by NetApp. But bugs in the Manila API may
			//cause it to have a different expectation of how big the share is, therefore
			//rejecting shrink/extend requests because it thinks they go in the wrong
			//direction. In this case, we try the opposite direction to see if it helps.
			err2 := m.resize(assetUUID, oldSize, newSize /* useReverseOperation = */, true)
			if err2 == nil {
				return castellum.OperationOutcomeSucceeded, nil
			}
			//If not successful, still return the original error (to avoid confusion).
		}

		//If the resize fails because of missing quota or because the share is in
		//status "error", it's the user's fault, not ours.
		if quotaErrorRx.MatchString(err.Error()) || shareStatusErrorRx.MatchString(err.Error()) {
			return castellum.OperationOutcomeFailed, err
		}
		return castellum.OperationOutcomeErrored, err
	}
	return castellum.OperationOutcomeSucceeded, nil
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

// GetAssetStatus implements the core.AssetManager interface.
func (m *assetManagerNFS) GetAssetStatus(ctx context.Context, res db.Resource, assetUUID string, previousStatus *core.AssetStatus) (core.AssetStatus, error) {
	//when netapp-scout reports a 404 for this share, we can check Manila to see if the share was deleted in the meantime
	actionOn404 := func() error {
		_, getErr := shares.Get(m.Manila, assetUUID).Extract()
		if errext.IsOfType[gophercloud.ErrDefault404](getErr) {
			return core.AssetNotFoundErr{InnerError: fmt.Errorf("share not found in Manila: %s", getErr.Error())}
		}
		return nil //use the default error returned by m.queryScout()
	}

	//query Prometheus metrics (via netapp-scout) for size and usage
	var data struct {
		SizeGiB        uint64  `json:"size_gib"`
		MinimumSizeGiB uint64  `json:"min_size_gib"`
		UsageGiB       float64 `json:"usage_gib"`
	}
	path := fmt.Sprintf("v1/projects/%s/shares/%s", res.ScopeUUID, assetUUID)
	err := m.queryScout(ctx, path, &data, actionOn404)
	if err != nil {
		return core.AssetStatus{}, err
	}
	status := core.AssetStatus{
		Size:        data.SizeGiB,
		MinimumSize: &data.MinimumSizeGiB,
		Usage:       castellum.UsageValues{castellum.SingularUsageMetric: data.UsageGiB},
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

// shareExtendOpts is like shares.ExtendOpts, but supports the new "force" option.
// TODO: merge into upstream
type shareExtendOpts struct {
	NewSize int  `json:"new_size"`
	Force   bool `json:"force"`
}

// ToShareExtendMap implements the shares.ExtendOptsBuilder interface.
func (opts shareExtendOpts) ToShareExtendMap() (map[string]interface{}, error) {
	return gophercloud.BuildRequestBody(opts, "extend")
}
