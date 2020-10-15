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

//ProviderClientInterface is implemented by ProviderClient. Use this type to
//allow for test doubles.
type ProviderClientInterface interface {
	GetProject(projectID string) (*CachedProject, error)
	GetDomain(domainID string) (*CachedDomain, error)
}

//ProviderClient extends gophercloud.ProviderClient with some caching.
type ProviderClient struct {
	*gophercloud.ProviderClient
	KeystoneV3   *gophercloud.ServiceClient
	projectCache map[string]*CachedProject //key = UUID, nil value = project does not exist
	domainCache  map[string]*CachedDomain  //key = UUID, nil value = domain does not exist
	cacheMutex   *sync.RWMutex
}

//CachedProject contains cached information about a Keystone project.
type CachedProject struct {
	Name     string
	DomainID string
}

//CachedDomain contains cached information about a Keystone domain.
type CachedDomain struct {
	Name string
}

//WrapProviderClient constructs a new ProviderClient instance.
func WrapProviderClient(provider *gophercloud.ProviderClient, eo gophercloud.EndpointOpts) (*ProviderClient, error) {
	keystoneV3, err := openstack.NewIdentityV3(provider, eo)
	if err != nil {
		return nil, err
	}
	return &ProviderClient{
		ProviderClient: provider,
		KeystoneV3:     keystoneV3,
		projectCache:   make(map[string]*CachedProject),
		domainCache:    make(map[string]*CachedDomain),
		cacheMutex:     new(sync.RWMutex),
	}, nil
}

//GetProject queries the given project ID in Keystone, unless it is already cached.
//When the project does not exist, nil is returned instead of an error.
func (p *ProviderClient) GetProject(projectID string) (*CachedProject, error) {
	p.cacheMutex.RLock()
	result, ok := p.projectCache[projectID]
	p.cacheMutex.RUnlock()
	if ok {
		return result, nil
	}

	project, err := projects.Get(p.KeystoneV3, projectID).Extract()
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

//GetDomain queries the given domain ID in Keystone, unless it is already cached.
//When the project does not exist, nil is returned instead of an error.
func (p *ProviderClient) GetDomain(domainID string) (*CachedDomain, error) {
	p.cacheMutex.RLock()
	result, ok := p.domainCache[domainID]
	p.cacheMutex.RUnlock()
	if ok {
		return result, nil
	}

	domain, err := domains.Get(p.KeystoneV3, domainID).Extract()
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
