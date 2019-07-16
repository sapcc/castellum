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
	"fmt"

	"github.com/gophercloud/gophercloud"
	"github.com/gophercloud/gophercloud/openstack/identity/v3/tokens"
	"github.com/sapcc/castellum/internal/core"
	"github.com/sapcc/castellum/internal/db"
	"github.com/sapcc/go-bits/logg"
	"github.com/sapcc/gophercloud-limes/resources"
	"github.com/sapcc/gophercloud-limes/resources/v1/projects"
	"github.com/sapcc/limes"
)

//This asset manager has one asset type for each resource (i.e. each type of
//quota).  For a given type of quota, there is only one quota per project, so
//for each project resource, exactly one asset is reported, the project itself
//(i.e. `asset.UUID == resource.ScopeUUID`).
type assetManagerProjectQuota struct {
	Provider       *core.ProviderClient
	Limes          *gophercloud.ServiceClient
	KnownResources []limesResourceInfo
}

type limesResourceInfo struct {
	ServiceType  string
	ResourceName string
	Unit         limes.Unit
}

func (info limesResourceInfo) AssetType() db.AssetType {
	return db.AssetType(fmt.Sprintf("project-quota:%s:%s", info.ServiceType, info.ResourceName))
}

func init() {
	core.RegisterAssetManagerFactory("project-quota", func(provider *core.ProviderClient, eo gophercloud.EndpointOpts) (core.AssetManager, error) {
		limes, err := resources.NewLimesV1(provider.ProviderClient, eo)
		if err != nil {
			return nil, err
		}

		//get project ID where we're authenticated (see below for why)
		var (
			currentProjectID       string
			currentProjectDomainID string
		)
		if result, ok := provider.GetAuthResult().(tokens.CreateResult); ok {
			project, err := result.ExtractProject()
			if err == nil {
				currentProjectID = project.ID
				currentProjectDomainID = project.Domain.ID
			} else {
				return nil, err
			}
		} else {
			return nil, fmt.Errorf("cannot extract project ID from %t", provider.GetAuthResult())
		}

		//list all resources that exist, by looking at the current project
		report, err := projects.Get(limes, currentProjectDomainID, currentProjectID, nil).Extract()
		var knownResources []limesResourceInfo
		for _, srv := range report.Services {
			for _, res := range srv.Resources {
				knownResources = append(knownResources, limesResourceInfo{
					ServiceType:  srv.Type,
					ResourceName: res.Name,
					Unit:         res.Unit,
				})
			}
		}

		return &assetManagerProjectQuota{provider, limes, knownResources}, nil
	})
}

//AssetTypes implements the core.AssetManager interface.
func (m *assetManagerProjectQuota) AssetTypes() []core.AssetTypeInfo {
	result := make([]core.AssetTypeInfo, len(m.KnownResources))
	for idx, info := range m.KnownResources {
		result[idx] = core.AssetTypeInfo{
			AssetType:            info.AssetType(),
			ReportsAbsoluteUsage: true,
		}
	}
	return result
}

//ListAssets implements the core.AssetManager interface.
func (m *assetManagerProjectQuota) ListAssets(res db.Resource) ([]string, error) {
	//see notes on type declaration above
	return []string{res.ScopeUUID}, nil
}

//SetAssetSize implements the core.AssetManager interface.
func (m *assetManagerProjectQuota) SetAssetSize(res db.Resource, projectID string, oldSize, newSize uint64) error {
	info, err := m.parseAssetType(res)
	if err != nil {
		return err
	}
	project, err := m.Provider.GetProject(projectID)
	if err != nil {
		return err
	}

	srvQuotaReq := limes.ServiceQuotaRequest{}
	srvQuotaReq[info.ResourceName] = limes.ValueWithUnit{Value: newSize, Unit: info.Unit}
	quotaReq := limes.QuotaRequest{}
	quotaReq[info.ServiceType] = srvQuotaReq

	respBytes, err := projects.Update(m.Limes, project.DomainID, projectID, projects.UpdateOpts{Services: quotaReq})
	if len(respBytes) > 0 {
		logg.Info("encountered non-critical error while setting %s/%s quota on project %s: %q",
			info.ServiceType, info.ResourceName, projectID, string(respBytes))
	}
	return err
}

//GetAssetStatus implements the core.AssetManager interface.
func (m *assetManagerProjectQuota) GetAssetStatus(res db.Resource, projectID string, previousStatus *core.AssetStatus) (core.AssetStatus, error) {
	info, err := m.parseAssetType(res)
	if err != nil {
		return core.AssetStatus{}, err
	}
	project, err := m.Provider.GetProject(projectID)
	if err != nil {
		return core.AssetStatus{}, err
	}
	if project == nil {
		return core.AssetStatus{}, fmt.Errorf("project not found in Keystone: %s", projectID)
	}

	opts := projects.GetOpts{Service: info.ServiceType, Resource: info.ResourceName}
	report, err := projects.Get(m.Limes, project.DomainID, projectID, opts).Extract()
	if err != nil {
		return core.AssetStatus{}, err
	}
	for _, srv := range report.Services {
		for _, res := range srv.Resources {
			return core.AssetStatus{
				Size:          res.Quota,
				AbsoluteUsage: p2u64(res.Usage),
				UsagePercent:  uint32(100 * res.Usage / res.Quota),
			}, nil
		}
	}
	return core.AssetStatus{}, fmt.Errorf("Limes does not report %s/%s quota for project %s",
		info.ServiceType, info.ResourceName, projectID)
}

func (m *assetManagerProjectQuota) parseAssetType(res db.Resource) (limesResourceInfo, error) {
	for _, info := range m.KnownResources {
		if info.AssetType() == res.AssetType {
			return info, nil
		}
	}
	return limesResourceInfo{}, fmt.Errorf("unknown asset type: %s", res.AssetType)
}
