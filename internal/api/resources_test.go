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

package api

import (
	"net/http"
	"testing"

	"github.com/sapcc/castellum/internal/test"
	"github.com/sapcc/go-bits/assert"
)

//JSON serializations of the records in internal/api/fixtures/start-data.sql
var (
	initialFooResourceJSON = assert.JSONObject{
		"scraped_at": 1,
		"low_threshold": assert.JSONObject{
			"usage_percent": 20,
			"delay_seconds": 3600,
		},
		"high_threshold": assert.JSONObject{
			"usage_percent": 80,
			"delay_seconds": 1800,
		},
		"size_steps": assert.JSONObject{
			"percent": 20,
		},
	}
	initialBarResourceJSON = assert.JSONObject{
		"scraped_at": 2,
		"critical_threshold": assert.JSONObject{
			"usage_percent": 95,
		},
		"size_steps": assert.JSONObject{
			"percent": 10,
		},
	}
)

func TestGetProject(baseT *testing.T) {
	t := test.T{T: baseT}
	_, hh, validator := setupTest(t)

	//endpoint requires a token with project access
	validator.Forbid("project:access")
	assert.HTTPRequest{
		Method:       "GET",
		Path:         "/v1/projects/project1",
		ExpectStatus: http.StatusForbidden,
	}.Check(t.T, hh)
	validator.Allow("project:access")

	//expect empty result for project with no resources
	assert.HTTPRequest{
		Method:       "GET",
		Path:         "/v1/projects/project2",
		ExpectStatus: http.StatusOK,
		ExpectBody: assert.JSONObject{
			"resources": assert.JSONObject{},
		},
	}.Check(t.T, hh)

	//expect non-empty result for project with resources
	assert.HTTPRequest{
		Method:       "GET",
		Path:         "/v1/projects/project1",
		ExpectStatus: http.StatusOK,
		ExpectBody: assert.JSONObject{
			"resources": assert.JSONObject{
				"foo": initialFooResourceJSON,
				"bar": initialBarResourceJSON,
			},
		},
	}.Check(t.T, hh)

	//expect partial result when user is not allowed to view certain resources
	validator.Forbid("project:show:bar")
	validator.Forbid("project:edit:foo") //this should not be an issue
	assert.HTTPRequest{
		Method:       "GET",
		Path:         "/v1/projects/project1",
		ExpectStatus: http.StatusOK,
		ExpectBody: assert.JSONObject{
			"resources": assert.JSONObject{
				"foo": initialFooResourceJSON,
			},
		},
	}.Check(t.T, hh)
}

func TestGetResource(baseT *testing.T) {
	t := test.T{T: baseT}
	_, hh, validator := setupTest(t)

	//endpoint requires a token with project access
	validator.Forbid("project:access")
	assert.HTTPRequest{
		Method:       "GET",
		Path:         "/v1/projects/project1/resources/foo",
		ExpectStatus: http.StatusForbidden,
	}.Check(t.T, hh)
	validator.Allow("project:access")

	//expect error for unknown project or resource
	assert.HTTPRequest{
		Method:       "GET",
		Path:         "/v1/projects/project2/resources/foo",
		ExpectStatus: http.StatusNotFound,
	}.Check(t.T, hh)
	assert.HTTPRequest{
		Method:       "GET",
		Path:         "/v1/projects/project1/resources/doesnotexist",
		ExpectStatus: http.StatusNotFound,
	}.Check(t.T, hh)

	//the "unknown" resource exists, but it should be 404 regardless because we
	//don't have an asset manager for it
	assert.HTTPRequest{
		Method:       "GET",
		Path:         "/v1/projects/project1/resources/unknown",
		ExpectStatus: http.StatusNotFound,
	}.Check(t.T, hh)

	//expect error for inaccessible resource
	validator.Forbid("project:show:foo")
	assert.HTTPRequest{
		Method:       "GET",
		Path:         "/v1/projects/project1/resources/foo",
		ExpectStatus: http.StatusForbidden,
	}.Check(t.T, hh)
	validator.Allow("project:show:foo")

	//happy path
	validator.Forbid("project:edit:foo") //this should not be an issue
	assert.HTTPRequest{
		Method:       "GET",
		Path:         "/v1/projects/project1/resources/foo",
		ExpectStatus: http.StatusOK,
		ExpectBody:   initialFooResourceJSON,
	}.Check(t.T, hh)
	assert.HTTPRequest{
		Method:       "GET",
		Path:         "/v1/projects/project1/resources/bar",
		ExpectStatus: http.StatusOK,
		ExpectBody:   initialBarResourceJSON,
	}.Check(t.T, hh)
}
