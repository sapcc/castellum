package projects

import (
	"github.com/gophercloud/gophercloud"
	"github.com/sapcc/go-api-declarations/limes"
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
func (r CommonResult) ExtractProjects() ([]limes.ProjectReport, error) {
	var s struct {
		Projects []limes.ProjectReport `json:"projects"`
	}

	err := r.ExtractInto(&s)
	return s.Projects, err
}

// Extract interprets a CommonResult as a Project.
func (r CommonResult) Extract() (*limes.ProjectReport, error) {
	var s struct {
		Project *limes.ProjectReport `json:"project"`
	}
	err := r.ExtractInto(&s)
	return s.Project, err
}
