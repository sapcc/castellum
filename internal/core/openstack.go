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
	"fmt"
	"net/http"
	"sync"

	"github.com/gophercloud/gophercloud"
	"github.com/gophercloud/gophercloud/openstack"
	"github.com/gophercloud/gophercloud/openstack/identity/v3/domains"
	"github.com/gophercloud/gophercloud/openstack/identity/v3/projects"
	"github.com/gophercloud/gophercloud/openstack/identity/v3/roles"
	"github.com/gophercloud/gophercloud/openstack/identity/v3/tokens"
)

//ProviderClient is an interface for an internal type that wraps
//gophercloud.ProviderClient to provide caching and rescoping. It is only
//provided as an interface to enable substitution of a mock for tests.
type ProviderClient interface {
	//CloudAdminClient returns a service client in the provider client's default scope.
	//The argument is a function like `openstack.NewIdentityV3`.
	CloudAdminClient(factory ServiceClientFactory) (*gophercloud.ServiceClient, error)
	//ProjectScopedClient authenticates into the specified project scope.
	ProjectScopedClient(scope ProjectScope) (*gophercloud.ProviderClient, gophercloud.EndpointOpts, error)

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
	pc            *gophercloud.ProviderClient
	ao            gophercloud.AuthOptions
	eo            gophercloud.EndpointOpts
	roleIDForName map[string]string
	projectCache  map[string]*CachedProject //key = UUID, nil value = project does not exist
	domainCache   map[string]*CachedDomain  //key = UUID, nil value = domain does not exist
	cacheMutex    *sync.RWMutex
}

//ServiceClientFactory is a typedef that appears in type ProviderClient.
type ServiceClientFactory func(*gophercloud.ProviderClient, gophercloud.EndpointOpts) (*gophercloud.ServiceClient, error)

//ProjectScope defines the scope into which ProviderClient.ProjectScopedClient() will authenticate.
type ProjectScope struct {
	//The ID of the project to scope into.
	ID string
	//Before scoping into the project, assign these roles to ourselves.
	RoleNames []string
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

//NewProviderClient constructs a new ProviderClient instance.
func NewProviderClient(ao gophercloud.AuthOptions, eo gophercloud.EndpointOpts) (ProviderClient, error) {
	pc, err := createProviderClient(ao)
	if err != nil {
		return nil, err
	}
	pc.UserAgent.Prepend("castellum")

	//list all roles and remember the name -> ID mapping
	identityV3, err := openstack.NewIdentityV3(pc, eo)
	if err != nil {
		return nil, err
	}
	page, err := roles.List(identityV3, roles.ListOpts{}).AllPages()
	if err != nil {
		return nil, err
	}
	allRoles, err := roles.ExtractRoles(page)
	if err != nil {
		return nil, err
	}
	roleIDForName := make(map[string]string, len(allRoles))
	for _, role := range allRoles {
		roleIDForName[role.Name] = role.ID
	}

	return &providerClientImpl{
		pc:            pc,
		ao:            ao,
		eo:            eo,
		roleIDForName: roleIDForName,
		projectCache:  make(map[string]*CachedProject),
		domainCache:   make(map[string]*CachedDomain),
		cacheMutex:    new(sync.RWMutex),
	}, nil
}

//CloudAdminClient implements the ProviderClient interface.
func (p *providerClientImpl) CloudAdminClient(factory ServiceClientFactory) (*gophercloud.ServiceClient, error) {
	return factory(p.pc, p.eo)
}

//ProjectScopedClient implements the ProviderClient interface.
func (p *providerClientImpl) ProjectScopedClient(scope ProjectScope) (*gophercloud.ProviderClient, gophercloud.EndpointOpts, error) {
	return p.projectScopedClientImpl(scope, true)
}

func (p *providerClientImpl) projectScopedClientImpl(scope ProjectScope, firstPass bool) (*gophercloud.ProviderClient, gophercloud.EndpointOpts, error) {
	//auth into the target project
	ao := p.ao
	ao.Scope = &gophercloud.AuthScope{ProjectID: scope.ID}
	pc, err := createProviderClient(ao)
	if err != nil {
		//NOTE: If we don't have any roles assigned in the project yet, we will get
		//a 401, even if the provided credentials are correct. This is not a fatal
		//error, we just need to carry on and assign roles.
		if _, is401 := err.(gophercloud.ErrDefault401); is401 {
			pc = nil
		} else {
			return nil, p.eo, fmt.Errorf("cannot authenticate into project %s: %w", scope.ID, err)
		}
	}
	if pc != nil {
		pc.UserAgent.Prepend("castellum")
	}

	//get currently assigned roles
	var (
		result        tokens.CreateResult
		ok            bool
		assignedRoles []tokens.Role
	)
	if pc == nil {
		//auth failed with 401, so we have no roles assigned in the target project...
		assignedRoles = nil
		//...but we need to get our user ID from somewhere, so we're going to use
		//our cloud-admin-scope AuthResult for that
		result, ok = p.pc.GetAuthResult().(tokens.CreateResult)
		if !ok {
			return nil, p.eo, fmt.Errorf("unknown type for AuthResult: %T", p.pc.GetAuthResult())
		}
	} else {
		if len(scope.RoleNames) == 0 {
			//no checks to perform
			return pc, p.eo, nil
		}
		result, ok = pc.GetAuthResult().(tokens.CreateResult)
		if !ok {
			return nil, p.eo, fmt.Errorf("unknown type for AuthResult: %T", p.pc.GetAuthResult())
		}
		assignedRoles, err = result.ExtractRoles()
		if err != nil {
			return nil, p.eo, fmt.Errorf("cannot get role assignments for project scope: %w", err)
		}
	}

	//which roles are we still missing?
	isRequestedRole := make(map[string]bool)
	for _, roleName := range scope.RoleNames {
		isRequestedRole[roleName] = true
	}
	for _, role := range assignedRoles {
		delete(isRequestedRole, role.Name)
	}
	if len(isRequestedRole) == 0 {
		//all required roles are assigned
		return pc, p.eo, nil
	}

	//not all roles present -> try at most once to assign missing roles
	//(this check prevents an infinite loop in case of unforeseen problems)
	if !firstPass {
		return nil, p.eo, fmt.Errorf("some roles in project %s are still missing despite successful assignment: %v",
			scope.ID, isRequestedRole)
	}
	user, err := result.ExtractUser()
	if err != nil {
		return nil, p.eo, fmt.Errorf("cannot get own user ID: %w", err)
	}
	identityV3, err := p.CloudAdminClient(openstack.NewIdentityV3)
	if err != nil {
		return nil, p.eo, err
	}
	for roleName := range isRequestedRole {
		roleID := p.roleIDForName[roleName]
		if roleID == "" {
			return nil, p.eo, fmt.Errorf("no such role: %s", roleName)
		}
		opts := roles.AssignOpts{
			UserID:    user.ID,
			ProjectID: scope.ID,
		}
		err := roles.Assign(identityV3, roleID, opts).ExtractErr()
		if err != nil {
			return nil, p.eo, fmt.Errorf("could not assign role %s in project %s: %w", roleName, scope.ID, err)
		}
	}

	//restart call to reauthenticate and obtain the new role assignments
	return p.projectScopedClientImpl(scope, false)
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

func createProviderClient(ao gophercloud.AuthOptions) (*gophercloud.ProviderClient, error) {
	provider, err := openstack.NewClient(ao.IdentityEndpoint)
	if err == nil {
		//use http.DefaultClient, esp. to pick up the CASTELLUM_INSECURE flag
		provider.HTTPClient = *http.DefaultClient
		err = openstack.Authenticate(provider, ao)
	}
	return provider, err
}
