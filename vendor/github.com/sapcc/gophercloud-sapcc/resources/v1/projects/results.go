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

import (
	"github.com/gophercloud/gophercloud"
	limesresources "github.com/sapcc/go-api-declarations/limes/resources"
)

// CommonResult is the result of a Get/List operation. Call its appropriate
// Extract method to interpret it as a Project or a slice of Projects.
type CommonResult struct {
	gophercloud.Result
}

// UpdateResult is the result of an Update operation. Call its appropriate
// Extract method to extract the error and the warning body from the result.
type UpdateResult struct {
	gophercloud.Result
	Body []byte
}

// Extract interprets a UpdateResult as an update warning body and error
func (r UpdateResult) Extract() ([]byte, error) {
	return r.Body, r.Err
}

// SyncResult is the result of an Sync operation. Call its appropriate
// ExtractErr method to extract the error from the result.
type SyncResult struct {
	gophercloud.ErrResult
}

// ExtractProjects interprets a CommonResult as a slice of Projects.
func (r CommonResult) ExtractProjects() ([]limesresources.ProjectReport, error) {
	var s struct {
		Projects []limesresources.ProjectReport `json:"projects"`
	}

	err := r.ExtractInto(&s)
	return s.Projects, err
}

// Extract interprets a CommonResult as a Project.
func (r CommonResult) Extract() (*limesresources.ProjectReport, error) {
	var s struct {
		Project *limesresources.ProjectReport `json:"project"`
	}
	err := r.ExtractInto(&s)
	return s.Project, err
}
