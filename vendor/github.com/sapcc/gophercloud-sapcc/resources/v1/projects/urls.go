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

package projects

import "github.com/gophercloud/gophercloud"

func listURL(client *gophercloud.ServiceClient, domainID string) string {
	return client.ServiceURL("domains", domainID, "projects")
}

func getURL(client *gophercloud.ServiceClient, domainID, projectID string) string {
	return client.ServiceURL("domains", domainID, "projects", projectID)
}

func updateURL(client *gophercloud.ServiceClient, domainID, projectID string) string {
	return client.ServiceURL("domains", domainID, "projects", projectID)
}

func syncURL(client *gophercloud.ServiceClient, domainID, projectID string) string {
	return client.ServiceURL("domains", domainID, "projects", projectID, "sync")
}
