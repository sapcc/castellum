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
	core.RegisterAssetManagerFactory("nfs-shares", func(provider *gophercloud.ProviderClient, eo gophercloud.EndpointOpts) (core.AssetManager, error) {
		manila, err := openstack.NewSharedFileSystemV2(provider, eo)
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
func (m *assetManagerNFS) AssetTypes() []db.AssetType {
	return []db.AssetType{"nfs-shares"}
}

//ListAssets implements the core.AssetManager interface.
func (m *assetManagerNFS) ListAssets(res db.Resource) ([]string, error) {
	page := 0
	pageSize := 250
	var shareIDs []string

	//TODO: simplify this by adding a shares.List() function to
	//package github.com/gophercloud/gophercloud/openstack/sharedfilesystems/v2/shares
	for {
		url := m.Manila.ServiceURL("shares") + fmt.Sprintf("?project_id=%s&all_tenants=1&limit=%d&offset=%d", res.ScopeUUID, pageSize, page*pageSize)
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
				shareIDs = append(shareIDs, share.ID)
			}
			page++
		} else {
			//last page reached
			return shareIDs, nil
		}
	}
}

//SetAssetSize implements the core.AssetManager interface.
func (m *assetManagerNFS) SetAssetSize(res db.Resource, assetUUID string, oldSize, newSize uint64) error {
	if oldSize < newSize {
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

	//compute asset status from Prometheus metrics
	sizeBytes := bytesTotal + bytesReservedBySnapshots
	usageBytes := bytesUsed + bytesReservedBySnapshots
	status := core.AssetStatus{
		Size:         uint64(math.Round(sizeBytes / 1024 / 1024 / 1024)),
		UsagePercent: uint32(math.Round(100 * usageBytes / sizeBytes)),
	}
	if usageBytes <= 0 {
		status.UsagePercent = 0
	}
	if usageBytes > sizeBytes {
		status.UsagePercent = 100
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
	value, err := api.Query(context.Background(), queryStr, time.Now())
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
		logg.Info("Prometheus query returned more than one result: %s (only the first value will be used)", queryStr)
		fallthrough
	case 1:
		return float64(resultVector[0].Value), nil
	}
}