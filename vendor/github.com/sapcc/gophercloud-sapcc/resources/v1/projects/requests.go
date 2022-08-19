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
	"io"
	"net/http"

	"github.com/gophercloud/gophercloud"
	"github.com/sapcc/go-api-declarations/limes"
)

// RatesDisplay determines the presence of rate limits in a project's Get/List response.
type RatesDisplay string

const (
	// WithoutRates is the default value, it is only here for documentation purposes.
	WithoutRates RatesDisplay = ""
	WithRates    RatesDisplay = "true"
	OnlyRates    RatesDisplay = "only"
)

// ListOptsBuilder allows extensions to add additional parameters to the List request.
type ListOptsBuilder interface {
	ToProjectListParams() (map[string]string, string, error)
}

// ListOpts contains parameters for filtering a List request.
type ListOpts struct {
	Detail    bool         `q:"detail"`
	Areas     []string     `q:"area"`
	Services  []string     `q:"service"`
	Resources []string     `q:"resource"`
	Rates     RatesDisplay `q:"rates"`
}

// ToProjectListParams formats a ListOpts into a map of headers and a query string.
func (opts ListOpts) ToProjectListParams() (headers map[string]string, queryString string, err error) {
	h, err := gophercloud.BuildHeaders(opts)
	if err != nil {
		return nil, "", err
	}

	q, err := gophercloud.BuildQueryString(opts)
	if err != nil {
		return nil, "", err
	}

	return h, q.String(), nil
}

// List enumerates the projects in a specific domain.
func List(c *gophercloud.ServiceClient, domainID string, opts ListOptsBuilder) (r CommonResult) {
	url := listURL(c, domainID)
	headers := make(map[string]string)
	if opts != nil {
		h, q, err := opts.ToProjectListParams()
		if err != nil {
			r.Err = err
			return
		}
		headers = h
		url += q
	}

	resp, err := c.Get(url, &r.Body, &gophercloud.RequestOpts{ //nolint:bodyclose // already closed by gophercloud
		MoreHeaders: headers,
	})
	_, r.Header, r.Err = gophercloud.ParseResponse(resp, err)
	return
}

// GetOptsBuilder allows extensions to add additional parameters to the Get request.
type GetOptsBuilder interface {
	ToProjectGetParams() (map[string]string, string, error)
}

// GetOpts contains parameters for filtering a Get request.
type GetOpts struct {
	Detail    bool         `q:"detail"`
	Areas     []string     `q:"area"`
	Services  []string     `q:"service"`
	Resources []string     `q:"resource"`
	Rates     RatesDisplay `q:"rates"`
}

// ToProjectGetParams formats a GetOpts into a map of headers and a query string.
func (opts GetOpts) ToProjectGetParams() (headers map[string]string, queryString string, err error) {
	h, err := gophercloud.BuildHeaders(opts)
	if err != nil {
		return nil, "", err
	}

	q, err := gophercloud.BuildQueryString(opts)
	if err != nil {
		return nil, "", err
	}

	return h, q.String(), nil
}

// Get retrieves details on a single project, by ID.
func Get(c *gophercloud.ServiceClient, domainID, projectID string, opts GetOptsBuilder) (r CommonResult) {
	url := getURL(c, domainID, projectID)
	headers := make(map[string]string)
	if opts != nil {
		h, q, err := opts.ToProjectGetParams()
		if err != nil {
			r.Err = err
			return
		}
		headers = h
		url += q
	}

	resp, err := c.Get(url, &r.Body, &gophercloud.RequestOpts{ //nolint:bodyclose // already closed by gophercloud
		MoreHeaders: headers,
	})
	_, r.Header, r.Err = gophercloud.ParseResponse(resp, err)
	return
}

// UpdateOptsBuilder allows extensions to add additional parameters to the Update request.
type UpdateOptsBuilder interface {
	ToProjectUpdateMap() (map[string]string, map[string]interface{}, error)
}

// UpdateOpts contains parameters to update a project.
type UpdateOpts struct {
	Services limes.QuotaRequest `json:"services"`
}

// ToProjectUpdateMap formats a UpdateOpts into a map of headers and a request body.
func (opts UpdateOpts) ToProjectUpdateMap() (headers map[string]string, requestBody map[string]interface{}, err error) {
	h, err := gophercloud.BuildHeaders(opts)
	if err != nil {
		return nil, nil, err
	}

	b, err := gophercloud.BuildRequestBody(opts, "project")
	if err != nil {
		return nil, nil, err
	}

	return h, b, nil
}

// Update modifies the attributes of a project and returns the response body which contains non-fatal error messages.
func Update(c *gophercloud.ServiceClient, domainID, projectID string, opts UpdateOptsBuilder) (r UpdateResult) {
	url := updateURL(c, domainID, projectID)
	h, b, err := opts.ToProjectUpdateMap()
	if err != nil {
		r.Err = err
		return
	}
	resp, err := c.Put(url, b, nil, &gophercloud.RequestOpts{
		OkCodes:          []int{http.StatusAccepted},
		MoreHeaders:      h,
		KeepResponseBody: true,
	})
	_, r.Header, r.Err = gophercloud.ParseResponse(resp, err)
	if r.Err != nil {
		return
	}
	defer resp.Body.Close()
	r.Body, r.Err = io.ReadAll(resp.Body)
	return
}

// Sync schedules a sync task that pulls a project's data from the backing services
// into Limes' local database.
func Sync(c *gophercloud.ServiceClient, domainID, projectID string) (r SyncResult) {
	url := syncURL(c, domainID, projectID)
	resp, err := c.Post(url, nil, nil, &gophercloud.RequestOpts{ //nolint:bodyclose // already closed by gophercloud
		OkCodes: []int{http.StatusAccepted},
	})
	_, r.Header, r.Err = gophercloud.ParseResponse(resp, err)
	return
}
