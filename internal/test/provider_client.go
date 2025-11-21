// SPDX-FileCopyrightText: 2023 SAP SE or an SAP affiliate company
// SPDX-License-Identifier: Apache-2.0

package test

import (
	"context"

	"github.com/gophercloud/gophercloud/v2"

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

func (c MockProviderClient) ProjectScopedClient(_ context.Context, scope core.ProjectScope) (*gophercloud.ProviderClient, gophercloud.EndpointOpts, error) {
	panic("ProjectScopedClient is not implemented in MockProviderClient")
}

func (c MockProviderClient) GetAuthResult() gophercloud.AuthResult {
	panic("GetAuthResult is not implemented in MockProviderClient")
}

func (c MockProviderClient) GetProject(_ context.Context, projectID string) (*core.CachedProject, error) {
	result, exists := c.Projects[projectID]
	if exists {
		return &result, nil
	}
	return nil, nil
}

func (c MockProviderClient) GetDomain(_ context.Context, domainID string) (*core.CachedDomain, error) {
	result, exists := c.Domains[domainID]
	if exists {
		return &result, nil
	}
	return nil, nil
}

func (c MockProviderClient) FindProjectID(_ context.Context, projectName, projectDomainName string) (string, error) {
	domainID := c.findDomainID(projectDomainName)
	if domainID == "" {
		return "", nil // no such project
	}
	for projectID, project := range c.Projects {
		if project.Name == projectName && project.DomainID == domainID {
			return projectID, nil
		}
	}
	return "", nil // no such project
}

func (c MockProviderClient) findDomainID(domainName string) string {
	for domainID, domain := range c.Domains {
		if domain.Name == domainName {
			return domainID
		}
	}
	return "" // no such domain
}
