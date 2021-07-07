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

package core

import (
	"sync"

	"github.com/gophercloud/gophercloud"
	"github.com/gophercloud/gophercloud/openstack"
	"github.com/gophercloud/gophercloud/openstack/identity/v3/domains"
	"github.com/gophercloud/gophercloud/openstack/identity/v3/projects"
)

//ProviderClient is an interface for an internal type that wraps
//gophercloud.ProviderClient to provide caching and rescoping. It is only
//provided as an interface to enable substitution of a mock for tests.
type ProviderClient interface {
	//CloudAdminClient returns a service client in the provider client's default scope.
	//The argument is a function like `openstack.NewIdentityV3`.
	CloudAdminClient(factory ServiceClientFactory) (*gophercloud.ServiceClient, error)

	//GetAuthResult has the same semantics as gophercloud.ProviderClient.GetAuthResult.
	GetAuthResult() gophercloud.AuthResult
	//GetProject queries the given project ID in Keystone, unless it is already cached.
	//When the project does not exist, nil is returned instead of an error.
	GetProject(projectID string) (*CachedProject, error)
	//GetDomain queries the given domain ID in Keystone, unless it is already cached.
	//When the project does not exist, nil is returned instead of an error.
	GetDomain(domainID string) (*CachedDomain, error)
}

//providerClientImpl is the implementation for the ProviderClient interface.
type providerClientImpl struct {
	pc           *gophercloud.ProviderClient
	ao           gophercloud.AuthOptions
	eo           gophercloud.EndpointOpts
	projectCache map[string]*CachedProject //key = UUID, nil value = project does not exist
	domainCache  map[string]*CachedDomain  //key = UUID, nil value = domain does not exist
	cacheMutex   *sync.RWMutex
}

//ServiceClientFactory is a typedef that appears in type ProviderClient.
type ServiceClientFactory func(*gophercloud.ProviderClient, gophercloud.EndpointOpts) (*gophercloud.ServiceClient, error)

//CachedProject contains cached information about a Keystone project.
type CachedProject struct {
	Name     string
	DomainID string
}

//CachedDomain contains cached information about a Keystone domain.
type CachedDomain struct {
	Name string
}

//NewProviderClient constructs a new ProviderClient instance.
func NewProviderClient(ao gophercloud.AuthOptions, eo gophercloud.EndpointOpts) (ProviderClient, error) {
	pc, err := openstack.AuthenticatedClient(ao)
	if err != nil {
		return nil, err
	}
	pc.UserAgent.Prepend("castellum")

	return &providerClientImpl{
		pc:           pc,
		ao:           ao,
		eo:           eo,
		projectCache: make(map[string]*CachedProject),
		domainCache:  make(map[string]*CachedDomain),
		cacheMutex:   new(sync.RWMutex),
	}, nil
}

//GetProject implements the ProviderClient interface.
func (p *providerClientImpl) CloudAdminClient(factory ServiceClientFactory) (*gophercloud.ServiceClient, error) {
	return factory(p.pc, p.eo)
}

//GetAuthResult implements the ProviderClient interface.
func (p *providerClientImpl) GetAuthResult() gophercloud.AuthResult {
	return p.pc.GetAuthResult()
}

//GetProject implements the ProviderClient interface.
func (p *providerClientImpl) GetProject(projectID string) (*CachedProject, error) {
	p.cacheMutex.RLock()
	result, ok := p.projectCache[projectID]
	p.cacheMutex.RUnlock()
	if ok {
		return result, nil
	}

	identityV3, err := p.CloudAdminClient(openstack.NewIdentityV3)
	if err != nil {
		return nil, err
	}
	project, err := projects.Get(identityV3, projectID).Extract()
	if err != nil {
		if _, ok := err.(gophercloud.ErrDefault404); ok {
			p.cacheMutex.Lock()
			p.projectCache[projectID] = nil
			p.cacheMutex.Unlock()
			return nil, nil
		}
		return nil, err
	}

	result = &CachedProject{Name: project.Name, DomainID: project.DomainID}
	p.cacheMutex.Lock()
	p.projectCache[projectID] = result
	p.cacheMutex.Unlock()
	return result, nil
}

//GetDomain implements the ProviderClient interface.
func (p *providerClientImpl) GetDomain(domainID string) (*CachedDomain, error) {
	p.cacheMutex.RLock()
	result, ok := p.domainCache[domainID]
	p.cacheMutex.RUnlock()
	if ok {
		return result, nil
	}

	identityV3, err := p.CloudAdminClient(openstack.NewIdentityV3)
	if err != nil {
		return nil, err
	}
	domain, err := domains.Get(identityV3, domainID).Extract()
	if err != nil {
		if _, ok := err.(gophercloud.ErrDefault404); ok {
			p.cacheMutex.Lock()
			p.domainCache[domainID] = nil
			p.cacheMutex.Unlock()
			return nil, nil
		}
		return nil, err
	}

	result = &CachedDomain{Name: domain.Name}
	p.cacheMutex.Lock()
	p.domainCache[domainID] = result
	p.cacheMutex.Unlock()
	return result, nil
}
