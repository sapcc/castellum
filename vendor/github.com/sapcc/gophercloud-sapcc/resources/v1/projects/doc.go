// Copyright 2020 SAP SE
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package projects provides interaction with Limes at the project hierarchical
// level.
//
// Here is an example on how you would list all the projects in the current
// domain:
//
//	import (
//	  "fmt"
//	  "log"
//
//	  "github.com/gophercloud/gophercloud"
//	  "github.com/gophercloud/gophercloud/openstack/identity/v3/tokens"
//	  "github.com/gophercloud/utils/openstack/clientconfig"
//
//	  "github.com/sapcc/gophercloud-sapcc/clients"
//	  "github.com/sapcc/gophercloud-sapcc/resources/v1/projects"
//	)
//
//	func main() {
//	  provider, err := clientconfig.AuthenticatedClient(nil)
//	  if err != nil {
//	    log.Fatalf("could not initialize openstack client: %v", err)
//	  }
//
//	  limesClient, err := clients.NewLimesV1(provider, gophercloud.EndpointOpts{})
//	  if err != nil {
//	    log.Fatalf("could not initialize Limes client: %v", err)
//	  }
//
//	  project, err := provider.GetAuthResult().(tokens.CreateResult).ExtractProject()
//	  if err != nil {
//	    log.Fatalf("could not get project from token: %v", err)
//	  }
//
//	  result := projects.List(limesClient, project.Domain.ID, projects.ListOpts{Detail: true})
//	  if result.Err != nil {
//	    log.Fatalf("could not get projects: %v", result.Err)
//	  }
//
//	  projectList, err := result.ExtractProjects()
//	  if err != nil {
//	    log.Fatalf("could not get projects: %v", err)
//	  }
//	  for _, project := range projectList {
//	    fmt.Printf("%+v\n", project.Services)
//	  }
//	}
package projects
