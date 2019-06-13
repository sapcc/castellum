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
	"strings"
	"testing"

	"github.com/sapcc/castellum/internal/db"
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
		"size_constraints": assert.JSONObject{
			"maximum": 20000,
		},
		"size_steps": assert.JSONObject{
			"percent": 10,
		},
	}
)

func TestGetProject(baseT *testing.T) {
	t := test.T{T: baseT}
	_, hh, validator, _, _ := setupTest(t)

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
	_, hh, validator, _, _ := setupTest(t)

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

func TestPutResource(baseT *testing.T) {
	t := test.T{T: baseT}
	h, hh, validator, allResources, _ := setupTest(t)

	//mostly like `initialFooResourceJSON`, but with some delays changed
	newFooResourceJSON1 := assert.JSONObject{
		"low_threshold": assert.JSONObject{
			"usage_percent": 20,
			"delay_seconds": 1800,
		},
		"high_threshold": assert.JSONObject{
			"usage_percent": 80,
			"delay_seconds": 900,
		},
		"size_steps": assert.JSONObject{
			"percent": 20,
		},
	}

	//endpoint requires a token with project access
	validator.Forbid("project:access")
	assert.HTTPRequest{
		Method:       "PUT",
		Path:         "/v1/projects/project1/resources/foo",
		Body:         newFooResourceJSON1,
		ExpectStatus: http.StatusForbidden,
	}.Check(t.T, hh)
	validator.Allow("project:access")

	//expect error for unknown resource
	assert.HTTPRequest{
		Method:       "PUT",
		Path:         "/v1/projects/project1/resources/doesnotexist",
		Body:         newFooResourceJSON1,
		ExpectStatus: http.StatusNotFound,
	}.Check(t.T, hh)

	//the "unknown" resource exists, but it should be 404 regardless because we
	//don't have an asset manager for it
	assert.HTTPRequest{
		Method:       "PUT",
		Path:         "/v1/projects/project1/resources/unknown",
		Body:         newFooResourceJSON1,
		ExpectStatus: http.StatusNotFound,
	}.Check(t.T, hh)

	//expect error for inaccessible resource
	validator.Forbid("project:show:foo")
	assert.HTTPRequest{
		Method:       "PUT",
		Path:         "/v1/projects/project1/resources/foo",
		Body:         newFooResourceJSON1,
		ExpectStatus: http.StatusForbidden,
	}.Check(t.T, hh)
	validator.Allow("project:show:foo")

	validator.Forbid("project:edit:foo")
	assert.HTTPRequest{
		Method:       "PUT",
		Path:         "/v1/projects/project1/resources/foo",
		Body:         newFooResourceJSON1,
		ExpectStatus: http.StatusForbidden,
	}.Check(t.T, hh)
	validator.Allow("project:edit:foo")

	//since all tests above were error cases, expect the DB to be unchanged
	t.ExpectResources(h.DB, allResources...)

	//happy path
	assert.HTTPRequest{
		Method:       "PUT",
		Path:         "/v1/projects/project1/resources/foo",
		Body:         newFooResourceJSON1,
		ExpectStatus: http.StatusAccepted,
	}.Check(t.T, hh)

	//expect the resource to have been updated
	var newResources1 []db.Resource
	for _, res := range allResources {
		cloned := res
		if res.ScopeUUID == "project1" && res.AssetType == "foo" {
			cloned.LowDelaySeconds = 1800
			cloned.HighDelaySeconds = 900
		}
		newResources1 = append(newResources1, cloned)
	}
	t.ExpectResources(h.DB, newResources1...)

	//test disabling low and high thresholds, and enabling critical threshold
	newFooResourceJSON2 := assert.JSONObject{
		"critical_threshold": assert.JSONObject{
			"usage_percent": 98,
		},
		"size_steps": assert.JSONObject{
			"percent": 15,
		},
	}
	assert.HTTPRequest{
		Method:       "PUT",
		Path:         "/v1/projects/project1/resources/foo",
		Body:         newFooResourceJSON2,
		ExpectStatus: http.StatusAccepted,
	}.Check(t.T, hh)

	//expect the resource to have been updated
	var newResources2 []db.Resource
	for _, res := range allResources {
		if res.ScopeUUID == "project1" && res.AssetType == "foo" {
			newResources2 = append(newResources2, db.Resource{
				ID:                       res.ID,
				ScopeUUID:                "project1",
				AssetType:                "foo",
				ScrapedAt:                res.ScrapedAt,
				CriticalThresholdPercent: 98,
				SizeStepPercent:          15,
			})
		} else {
			newResources2 = append(newResources2, res)
		}
	}
	t.ExpectResources(h.DB, newResources2...)

	//test enabling low and high thresholds, and disabling critical threshold
	assert.HTTPRequest{
		Method:       "PUT",
		Path:         "/v1/projects/project1/resources/foo",
		Body:         newFooResourceJSON1,
		ExpectStatus: http.StatusAccepted,
	}.Check(t.T, hh)
	t.ExpectResources(h.DB, newResources1...)

	//test creating a new resource from scratch (rather than updating an existing one)
	assert.HTTPRequest{
		Method:       "PUT",
		Path:         "/v1/projects/project3/resources/foo",
		Body:         newFooResourceJSON2,
		ExpectStatus: http.StatusAccepted,
	}.Check(t.T, hh)
	allResources = append(newResources1, db.Resource{
		ID:                       5,
		ScopeUUID:                "project3",
		AssetType:                "foo",
		CriticalThresholdPercent: 98,
		SizeStepPercent:          15,
	})
	t.ExpectResources(h.DB, allResources...)

	//test setting constraints
	newFooResourceJSON3 := assert.JSONObject{
		"critical_threshold": assert.JSONObject{
			"usage_percent": 98,
		},
		"size_steps": assert.JSONObject{
			"percent": 15,
		},
		"size_constraints": assert.JSONObject{
			"minimum": 0,
			"maximum": 42000,
		},
	}
	assert.HTTPRequest{
		Method:       "PUT",
		Path:         "/v1/projects/project1/resources/foo",
		Body:         newFooResourceJSON3,
		ExpectStatus: http.StatusAccepted,
	}.Check(t.T, hh)

	//expect the resource to have been updated
	var newResources3 []db.Resource
	for _, res := range allResources {
		if res.ScopeUUID == "project1" && res.AssetType == "foo" {
			newResources3 = append(newResources3, db.Resource{
				ID:                       res.ID,
				ScopeUUID:                "project1",
				AssetType:                "foo",
				ScrapedAt:                res.ScrapedAt,
				CriticalThresholdPercent: 98,
				SizeStepPercent:          15,
				MinimumSize:              nil, //0 gets normalized to NULL
				MaximumSize:              p2uint64(42000),
			})
		} else {
			newResources3 = append(newResources3, res)
		}
	}
	t.ExpectResources(h.DB, newResources3...)
}

func TestPutResourceValidationErrors(baseT *testing.T) {
	t := test.T{T: baseT}
	h, hh, _, allResources, _ := setupTest(t)

	expectErrors := func(body assert.JSONObject, errors ...string) {
		assert.HTTPRequest{
			Method:       "PUT",
			Path:         "/v1/projects/project1/resources/foo",
			Body:         body,
			ExpectStatus: http.StatusUnprocessableEntity,
			ExpectBody:   assert.StringData(strings.Join(errors, "\n") + "\n"),
		}.Check(t.T, hh)
	}

	expectErrors(
		assert.JSONObject{
			"critical_dingsbums": assert.JSONObject{"usage_percent": 95},
		},
		`request body is not valid JSON: json: unknown field "critical_dingsbums"`,
	)

	expectErrors(
		assert.JSONObject{},
		"at least one threshold must be configured",
		"size step must be greater than 0%",
	)

	expectErrors(
		assert.JSONObject{
			"scraped_at":       12345,
			"low_threshold":    assert.JSONObject{"usage_percent": 20},
			"high_threshold":   assert.JSONObject{"usage_percent": 80},
			"size_steps":       assert.JSONObject{"percent": 10},
			"size_constraints": assert.JSONObject{"minimum": 30, "maximum": 20},
		},
		"resource.scraped_at cannot be set via the API",
		"delay for low threshold is missing",
		"delay for high threshold is missing",
		"maximum size must be greater than minimum size",
	)

	expectErrors(
		assert.JSONObject{
			"critical_threshold": assert.JSONObject{"usage_percent": 95, "delay_seconds": 60},
			"size_steps":         assert.JSONObject{"percent": 10},
			"size_constraints":   assert.JSONObject{"minimum": 20, "maximum": 30},
		},
		"critical threshold may not have a delay",
	)

	expectErrors(
		assert.JSONObject{
			"low_threshold":  assert.JSONObject{"usage_percent": 120, "delay_seconds": 60},
			"high_threshold": assert.JSONObject{"usage_percent": 80, "delay_seconds": 60},
			"size_steps":     assert.JSONObject{"percent": 10},
		},
		"low threshold must be between 0% and 100% of usage",
		"low threshold must be below high threshold",
	)

	expectErrors(
		assert.JSONObject{
			"high_threshold":     assert.JSONObject{"usage_percent": 120, "delay_seconds": 60},
			"critical_threshold": assert.JSONObject{"usage_percent": 105},
			"size_steps":         assert.JSONObject{"percent": 10},
		},
		"high threshold must be between 0% and 100% of usage",
		"critical threshold must be between 0% and 100% of usage",
		"high threshold must be below critical threshold",
	)

	expectErrors(
		assert.JSONObject{
			"low_threshold":      assert.JSONObject{"usage_percent": 20, "delay_seconds": 60},
			"critical_threshold": assert.JSONObject{"usage_percent": 15},
			"size_steps":         assert.JSONObject{"percent": 10},
		},
		"low threshold must be below critical threshold",
	)

	//none of this should have touched the DB
	t.ExpectResources(h.DB, allResources...)
}

func TestDeleteResource(baseT *testing.T) {
	t := test.T{T: baseT}
	h, hh, validator, allResources, allAssets := setupTest(t)

	//endpoint requires a token with project access
	validator.Forbid("project:access")
	assert.HTTPRequest{
		Method:       "DELETE",
		Path:         "/v1/projects/project1/resources/foo",
		ExpectStatus: http.StatusForbidden,
	}.Check(t.T, hh)
	validator.Allow("project:access")

	//expect error for unknown project or resource
	assert.HTTPRequest{
		Method:       "DELETE",
		Path:         "/v1/projects/project2/resources/foo",
		ExpectStatus: http.StatusNotFound,
	}.Check(t.T, hh)
	assert.HTTPRequest{
		Method:       "DELETE",
		Path:         "/v1/projects/project1/resources/doesnotexist",
		ExpectStatus: http.StatusNotFound,
	}.Check(t.T, hh)

	//the "unknown" resource exists, but it should be 404 regardless because we
	//don't have an asset manager for it
	assert.HTTPRequest{
		Method:       "DELETE",
		Path:         "/v1/projects/project1/resources/unknown",
		ExpectStatus: http.StatusNotFound,
	}.Check(t.T, hh)

	//expect error for inaccessible resource
	validator.Forbid("project:show:foo")
	assert.HTTPRequest{
		Method:       "DELETE",
		Path:         "/v1/projects/project1/resources/foo",
		ExpectStatus: http.StatusForbidden,
	}.Check(t.T, hh)
	validator.Allow("project:show:foo")

	validator.Forbid("project:edit:foo")
	assert.HTTPRequest{
		Method:       "DELETE",
		Path:         "/v1/projects/project1/resources/foo",
		ExpectStatus: http.StatusForbidden,
	}.Check(t.T, hh)
	validator.Allow("project:edit:foo")

	//since all tests above were error cases, expect all resources to still be there
	t.ExpectResources(h.DB, allResources...)

	//happy path
	assert.HTTPRequest{
		Method:       "DELETE",
		Path:         "/v1/projects/project1/resources/foo",
		ExpectStatus: http.StatusNoContent,
	}.Check(t.T, hh)

	//expect this resource and its assets to be gone
	var remainingResources []db.Resource
	isRemainingResource := make(map[int64]bool)
	for _, res := range allResources {
		if res.ScopeUUID != "project1" || res.AssetType != "foo" {
			remainingResources = append(remainingResources, res)
			isRemainingResource[res.ID] = true
		}
	}
	t.ExpectResources(h.DB, remainingResources...)

	var remainingAssets []db.Asset
	for _, asset := range allAssets {
		if isRemainingResource[asset.ResourceID] {
			remainingAssets = append(remainingAssets, asset)
		}
	}
	t.ExpectAssets(h.DB, remainingAssets...)
}
