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
	"errors"
	"fmt"
	"os"

	"github.com/gophercloud/gophercloud"
	"github.com/gophercloud/gophercloud/openstack"
	"github.com/gophercloud/gophercloud/openstack/sharedfilesystems/v2/shares"
	prom_api "github.com/prometheus/client_golang/api"
	"github.com/sapcc/castellum/internal/core"
	"github.com/sapcc/castellum/internal/db"
)

type assetManagerNFS struct {
	Manila     *gophercloud.ServiceClient
	Prometheus prom_api.Client
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

		return &assetManagerNFS{manila, promClient}, nil
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
	return core.AssetStatus{}, errors.New("TODO implement")
}
