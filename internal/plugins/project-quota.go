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
	"strings"

	"github.com/gophercloud/gophercloud"
	"github.com/gophercloud/gophercloud/openstack/identity/v3/tokens"
	"github.com/sapcc/go-api-declarations/limes"
	"github.com/sapcc/go-bits/logg"
	"github.com/sapcc/gophercloud-sapcc/clients"
	"github.com/sapcc/gophercloud-sapcc/resources/v1/projects"

	"github.com/sapcc/castellum/internal/core"
	"github.com/sapcc/castellum/internal/db"
)

// This asset manager has one asset type for each resource (i.e. each type of
// quota).  For a given type of quota, there is only one quota per project, so
// for each project resource, exactly one asset is reported, the project itself
// (i.e. `asset.UUID == resource.ScopeUUID`).
type assetManagerProjectQuota struct {
	Provider       core.ProviderClient
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
	core.RegisterAssetManagerFactory("project-quota", func(provider core.ProviderClient) (core.AssetManager, error) {
		limes, err := provider.CloudAdminClient(clients.NewLimesV1)
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
		if err != nil {
			return nil, fmt.Errorf("could not get project report for %s", currentProjectID)
		}
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

// InfoForAssetType implements the core.AssetManager interface.
func (m *assetManagerProjectQuota) InfoForAssetType(assetType db.AssetType) *core.AssetTypeInfo {
	for _, info := range m.KnownResources {
		thisAssetType := info.AssetType()
		if thisAssetType == assetType {
			return &core.AssetTypeInfo{
				AssetType:    thisAssetType,
				UsageMetrics: []db.UsageMetric{db.SingularUsageMetric},
			}
		}
	}
	return nil
}

var errNotAllowedForThisProject = errors.New("autoscaling is not permitted for this resource because of cluster-level policies")

// CheckResourceAllowed implements the core.AssetManager interface.
func (m *assetManagerProjectQuota) CheckResourceAllowed(assetType db.AssetType, projectID string, configJSON string, existingResources []db.AssetType) error {
	if configJSON != "" {
		return core.ErrNoConfigurationAllowed
	}

	resource, err := m.getQuotaStatus(assetType, projectID)
	if err != nil {
		return err
	}
	switch val := resource.Annotations["can_autoscale"].(type) {
	case string:
		if val == "true" {
			return nil
		}
	case bool:
		if val {
			return nil
		}
	}
	return errNotAllowedForThisProject
}

// ListAssets implements the core.AssetManager interface.
func (m *assetManagerProjectQuota) ListAssets(res db.Resource) ([]string, error) {
	//see notes on type declaration above
	return []string{res.ScopeUUID}, nil
}

// SetAssetSize implements the core.AssetManager interface.
func (m *assetManagerProjectQuota) SetAssetSize(res db.Resource, projectID string, oldSize, newSize uint64) (db.OperationOutcome, error) {
	info, err := m.parseAssetType(res.AssetType)
	if err != nil {
		return db.OperationOutcomeErrored, err
	}
	project, err := m.Provider.GetProject(projectID)
	if err != nil {
		return db.OperationOutcomeErrored, err
	}

	srvQuotaReq := limes.ServiceQuotaRequest{
		Resources: make(limes.ResourceQuotaRequest),
	}
	srvQuotaReq.Resources[info.ResourceName] = limes.ValueWithUnit{Value: newSize, Unit: info.Unit}
	quotaReq := limes.QuotaRequest{}
	quotaReq[info.ServiceType] = srvQuotaReq

	respBytes, err := projects.Update(m.Limes, project.DomainID, projectID, projects.UpdateOpts{Services: quotaReq}).Extract()
	if len(respBytes) > 0 {
		logg.Info("encountered non-critical error while setting %s/%s quota on project %s: %q",
			info.ServiceType, info.ResourceName, projectID, string(respBytes))
	}
	if err != nil {
		if isUserError(err) {
			return db.OperationOutcomeFailed, err
		}
		return db.OperationOutcomeErrored, err
	}
	return db.OperationOutcomeSucceeded, nil
}

func isUserError(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "got 409 instead") && (strings.Contains(msg, "domain quota exceeded") || strings.Contains(msg, "quota may not be lower than current usage"))
}

// GetAssetStatus implements the core.AssetManager interface.
func (m *assetManagerProjectQuota) GetAssetStatus(res db.Resource, projectID string, previousStatus *core.AssetStatus) (core.AssetStatus, error) {
	resource, err := m.getQuotaStatus(res.AssetType, projectID)
	if err != nil {
		return core.AssetStatus{}, err
	}
	if resource.Quota == nil {
		return core.AssetStatus{}, errors.New("resource does not track quota")
	}
	return core.AssetStatus{
		Size:  *resource.Quota,
		Usage: db.UsageValues{db.SingularUsageMetric: float64(resource.Usage)},
	}, nil
}

func (m *assetManagerProjectQuota) getQuotaStatus(assetType db.AssetType, projectID string) (*limes.ProjectResourceReport, error) {
	info, err := m.parseAssetType(assetType)
	if err != nil {
		return nil, err
	}
	project, err := m.Provider.GetProject(projectID)
	if err != nil {
		return nil, err
	}
	if project == nil {
		return nil, fmt.Errorf("project not found in Keystone: %s", projectID)
	}

	opts := projects.GetOpts{Services: []string{info.ServiceType}, Resources: []string{info.ResourceName}}
	report, err := projects.Get(m.Limes, project.DomainID, projectID, opts).Extract()
	if err != nil {
		return nil, err
	}
	for _, srv := range report.Services {
		for _, res := range srv.Resources {
			return res, nil
		}
	}
	return nil, fmt.Errorf("%s/%s quota for project %s is not reported by Limes",
		info.ServiceType, info.ResourceName, projectID)
}

func (m *assetManagerProjectQuota) parseAssetType(assetType db.AssetType) (limesResourceInfo, error) {
	for _, info := range m.KnownResources {
		if info.AssetType() == assetType {
			return info, nil
		}
	}
	return limesResourceInfo{}, fmt.Errorf("unknown asset type: %s", assetType)
}
