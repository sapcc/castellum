/*******************************************************************************
*
* Copyright 2023 SAP SE
*
* Licensed under the Apache License, Version 2.0 (the "License");
* you may not use this file except in compliance with the License.
* You should have received a copy of the License along with this
* program. If not, you may obtain a copy of the License at
*
*     http://www.apache.org/licenses/LICENSE-2.0
*
* Unless required by applicable law or agreed to in writing, software
* distributed under the License is distributed on an "AS IS" BASIS,
* WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
* See the License for the specific language governing permissions and
* limitations under the License.
*
*******************************************************************************/

package test

import (
	"github.com/gophercloud/gophercloud"

	"github.com/sapcc/castellum/internal/core"
)

// MockProviderClient implements the core.ProviderClientInterface.
type MockProviderClient struct {
	Domains  map[string]core.CachedDomain
	Projects map[string]core.CachedProject
}

func (c MockProviderClient) CloudAdminClient(factory core.ServiceClientFactory) (*gophercloud.ServiceClient, error) {
	panic("CloudAdminClient is not implemented in MockProviderClient")
}

func (c MockProviderClient) ProjectScopedClient(scope core.ProjectScope) (*gophercloud.ProviderClient, gophercloud.EndpointOpts, error) {
	panic("ProjectScopedClient is not implemented in MockProviderClient")
}

func (c MockProviderClient) GetAuthResult() gophercloud.AuthResult {
	panic("GetAuthResult is not implemented in MockProviderClient")
}

func (c MockProviderClient) GetProject(projectID string) (*core.CachedProject, error) {
	result, exists := c.Projects[projectID]
	if exists {
		return &result, nil
	}
	return nil, nil
}

func (c MockProviderClient) GetDomain(domainID string) (*core.CachedDomain, error) {
	result, exists := c.Domains[domainID]
	if exists {
		return &result, nil
	}
	return nil, nil
}

func (c MockProviderClient) FindProjectID(projectName, projectDomainName string) (string, error) {
	domainID := c.findDomainID(projectDomainName)
	if domainID == "" {
		return "", nil //no such project
	}
	for projectID, project := range c.Projects {
		if project.Name == projectName && project.DomainID == domainID {
			return projectID, nil
		}
	}
	return "", nil //no such project
}

func (c MockProviderClient) findDomainID(domainName string) string {
	for domainID, domain := range c.Domains {
		if domain.Name == domainName {
			return domainID
		}
	}
	return "" //no such domain
}
