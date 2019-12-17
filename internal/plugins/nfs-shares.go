/******************************************************************************
*
*  Copyright 2019 SAP SE
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
	"errors"
	"fmt"
	"math"
	"os"
	"regexp"
	"time"

	"github.com/gophercloud/gophercloud"
	"github.com/gophercloud/gophercloud/openstack"
	"github.com/gophercloud/gophercloud/openstack/sharedfilesystems/v2/shares"
	prom_api "github.com/prometheus/client_golang/api"
	prom_v1 "github.com/prometheus/client_golang/api/prometheus/v1"
	"github.com/prometheus/common/model"
	"github.com/sapcc/castellum/internal/core"
	"github.com/sapcc/castellum/internal/db"
	"github.com/sapcc/go-bits/logg"
)

type assetManagerNFS struct {
	Manila     *gophercloud.ServiceClient
	Prometheus prom_v1.API
}

func init() {
	core.RegisterAssetManagerFactory("nfs-shares", func(provider *core.ProviderClient, eo gophercloud.EndpointOpts) (core.AssetManager, error) {
		manila, err := openstack.NewSharedFileSystemV2(provider.ProviderClient, eo)
		if err != nil {
			return nil, err
		}
		manila.Microversion = "2.23"

		prometheusURL := os.Getenv("CASTELLUM_NFS_PROMETHEUS_URL")
		if prometheusURL == "" {
			return nil, errors.New("missing required environment variable: CASTELLUM_NFS_PROMETHEUS_URL")
		}
		promClient, err := prom_api.NewClient(prom_api.Config{Address: prometheusURL})
		if err != nil {
			return nil, fmt.Errorf("cannot connect to Prometheus at %s: %s",
				prometheusURL, err.Error())
		}

		return &assetManagerNFS{manila, prom_v1.NewAPI(promClient)}, nil
	})
}

//AssetTypes implements the core.AssetManager interface.
func (m *assetManagerNFS) AssetTypes() []core.AssetTypeInfo {
	return []core.AssetTypeInfo{{
		AssetType:            "nfs-shares",
		ReportsAbsoluteUsage: true,
	}}
}

//CheckResourceAllowed implements the core.AssetManager interface.
func (m *assetManagerNFS) CheckResourceAllowed(assetType db.AssetType, scopeUUID string) error {
	return nil
}

//ListAssets implements the core.AssetManager interface.
func (m *assetManagerNFS) ListAssets(res db.Resource) ([]string, error) {
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

	//TODO: simplify this by adding a shares.List() function to
	//package github.com/gophercloud/gophercloud/openstack/sharedfilesystems/v2/shares
	for {
		url := m.Manila.ServiceURL("shares") + fmt.Sprintf("?project_id=%s&all_tenants=1&limit=%d&offset=%d", res.ScopeUUID, pageSize, page*(pageSize-10))
		var r gophercloud.Result
		_, err := m.Manila.Get(url, &r.Body, nil)
		if err != nil {
			return nil, err
		}

		var data struct {
			Shares []struct {
				ID string `json:"id"`
			} `json:"shares"`
		}
		err = r.ExtractInto(&data)
		if err != nil {
			return nil, err
		}

		if len(data.Shares) > 0 {
			for _, share := range data.Shares {
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

var sizeInconsistencyErrorRx = regexp.MustCompile(`New size for (?:extend must be greater|shrink must be less) than current size`)
var quotaErrorRx = regexp.MustCompile(`Requested share exceeds allowed project/user or share type \S+ quota.`)

//SetAssetSize implements the core.AssetManager interface.
func (m *assetManagerNFS) SetAssetSize(res db.Resource, assetUUID string, oldSize, newSize uint64) (db.OperationOutcome, error) {
	err := m.resize(assetUUID, oldSize, newSize /* useReverseOperation = */, false)
	if err != nil && sizeInconsistencyErrorRx.MatchString(err.Error()) {
		//We only rely on sizes reported by NetApp. But bugs in the Manila API may
		//cause it to have a different expection how big the share is, therefore
		//rejecting shrink/extend requests because it thinks they go in the wrong
		//direction. In this case, we try the opposite direction to see if it helps.
		err2 := m.resize(assetUUID, oldSize, newSize /* useReverseOperation = */, true)
		if err2 == nil {
			return db.OperationOutcomeSucceeded, nil
		}
		//If not successful, still return the original error (to avoid confusion).
	}
	if err != nil {
		if quotaErrorRx.MatchString(err.Error()) {
			return db.OperationOutcomeFailed, err
		}
		return db.OperationOutcomeErrored, err
	}
	return db.OperationOutcomeSucceeded, nil
}

func (m *assetManagerNFS) resize(assetUUID string, oldSize, newSize uint64, useReverseOperation bool) error {
	if (oldSize < newSize && !useReverseOperation) || (oldSize >= newSize && useReverseOperation) {
		return shares.Extend(m.Manila, assetUUID, shares.ExtendOpts{NewSize: int(newSize)}).ExtractErr()
	}
	return shares.Shrink(m.Manila, assetUUID, shares.ShrinkOpts{NewSize: int(newSize)}).ExtractErr()
}

//GetAssetStatus implements the core.AssetManager interface.
func (m *assetManagerNFS) GetAssetStatus(res db.Resource, assetUUID string, previousStatus *core.AssetStatus) (core.AssetStatus, error) {
	//check status in Prometheus
	bytesTotal, err := m.getMetricForShare(res.ScopeUUID, assetUUID, "size_total")
	if err != nil {
		return core.AssetStatus{}, err
	}
	bytesReservedBySnapshots, err := m.getMetricForShare(res.ScopeUUID, assetUUID, "size_reserved_by_snapshots")
	if err != nil {
		return core.AssetStatus{}, err
	}
	bytesUsed, err := m.getMetricForShare(res.ScopeUUID, assetUUID, "size_used")
	if err != nil {
		return core.AssetStatus{}, err
	}
	bytesUsedBySnapshots, err := m.getMetricForShare(res.ScopeUUID, assetUUID, "size_used_by_snapshots")
	if err != nil {
		return core.AssetStatus{}, err
	}

	//compute asset status from Prometheus metrics
	//
	//    size  = total + reserved_by_snapshots
	//    usage = used  + max(reserved_by_snapshots, used_by_snapshots)
	//
	if bytesUsedBySnapshots < bytesReservedBySnapshots {
		bytesUsedBySnapshots = bytesReservedBySnapshots
	}
	sizeBytes := bytesTotal + bytesReservedBySnapshots
	usageBytes := bytesUsed + bytesUsedBySnapshots
	status := core.AssetStatus{
		Size:          uint64(math.Round(sizeBytes / 1024 / 1024 / 1024)),
		AbsoluteUsage: p2u64(uint64(math.Round(usageBytes / 1024 / 1024 / 1024))),
		UsagePercent:  core.GetUsagePercent(uint64(sizeBytes), uint64(usageBytes)),
	}
	if usageBytes <= 0 {
		status.AbsoluteUsage = p2u64(0)
		status.UsagePercent = 0
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

func (m *assetManagerNFS) getMetricForShare(projectUUID, shareUUID, metric string) (float64, error) {
	query := fmt.Sprintf(`netapp_capacity_svm{project_id=%q,share_id=%q,metric=%q}`,
		projectUUID, shareUUID, metric)
	return prometheusGetSingleValue(m.Prometheus, query)
}

func prometheusGetSingleValue(api prom_v1.API, queryStr string) (float64, error) {
	value, warnings, err := api.Query(context.Background(), queryStr, time.Now())
	for _, warning := range warnings {
		logg.Info("Prometheus query produced warning: %s", warning)
	}
	if err != nil {
		return 0, fmt.Errorf("Prometheus query failed: %s: %s", queryStr, err.Error())
	}
	resultVector, ok := value.(model.Vector)
	if !ok {
		return 0, fmt.Errorf("Prometheus query failed: %s: unexpected type %T", queryStr, value)
	}

	switch resultVector.Len() {
	case 0:
		return 0, fmt.Errorf("Prometheus query returned empty result: %s", queryStr)
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
		fallthrough
	case 1:
		return float64(resultVector[0].Value), nil
	}
}

func p2u64(val uint64) *uint64 {
	return &val
}
