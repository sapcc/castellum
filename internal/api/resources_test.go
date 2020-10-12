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

	"github.com/sapcc/castellum/internal/core"
	"github.com/sapcc/castellum/internal/db"
	"github.com/sapcc/castellum/internal/plugins"
	"github.com/sapcc/castellum/internal/test"
	"github.com/sapcc/go-bits/assert"
)

//JSON serializations of the records in internal/api/fixtures/start-data.sql
var (
	initialFooResourceJSON = assert.JSONObject{
		"scraped_at":  1,
		"asset_count": 2,
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
		"checked": assert.JSONObject{
			"at":    3,
			"error": "datacenter is on fire",
		},
		"asset_count": 1,
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
	withHandler(t, core.Config{}, nil, func(h *handler, hh http.Handler, mv *MockValidator, _ []db.Resource, _ []db.Asset) {

		//endpoint requires a token with project access
		mv.Forbid("project:access")
		assert.HTTPRequest{
			Method:       "GET",
			Path:         "/v1/projects/project1",
			ExpectStatus: http.StatusForbidden,
		}.Check(t.T, hh)
		mv.Allow("project:access")

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
		mv.Forbid("project:show:bar")
		mv.Forbid("project:edit:foo") //this should not be an issue
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

	})
}

func TestGetResource(baseT *testing.T) {
	t := test.T{T: baseT}
	withHandler(t, core.Config{}, nil, func(h *handler, hh http.Handler, mv *MockValidator, _ []db.Resource, _ []db.Asset) {

		//endpoint requires a token with project access
		mv.Forbid("project:access")
		assert.HTTPRequest{
			Method:       "GET",
			Path:         "/v1/projects/project1/resources/foo",
			ExpectStatus: http.StatusForbidden,
		}.Check(t.T, hh)
		mv.Allow("project:access")

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
		mv.Forbid("project:show:foo")
		assert.HTTPRequest{
			Method:       "GET",
			Path:         "/v1/projects/project1/resources/foo",
			ExpectStatus: http.StatusForbidden,
		}.Check(t.T, hh)
		mv.Allow("project:show:foo")

		//happy path
		mv.Forbid("project:edit:foo") //this should not be an issue
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

	})
}

func TestPutResource(baseT *testing.T) {
	t := test.T{T: baseT}
	withHandler(t, core.Config{}, nil, func(h *handler, hh http.Handler, mv *MockValidator, allResources []db.Resource, _ []db.Asset) {

		//mostly like `initialFooResourceJSON`, but with some delays changed and
		//single-step resizing instead of percentage-based resizing
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
				"single": true,
			},
		}

		//endpoint requires a token with project access
		mv.Forbid("project:access")
		assert.HTTPRequest{
			Method:       "PUT",
			Path:         "/v1/projects/project1/resources/foo",
			Body:         newFooResourceJSON1,
			ExpectStatus: http.StatusForbidden,
		}.Check(t.T, hh)
		mv.Allow("project:access")

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
		mv.Forbid("project:show:foo")
		assert.HTTPRequest{
			Method:       "PUT",
			Path:         "/v1/projects/project1/resources/foo",
			Body:         newFooResourceJSON1,
			ExpectStatus: http.StatusForbidden,
		}.Check(t.T, hh)
		mv.Allow("project:show:foo")

		mv.Forbid("project:edit:foo")
		assert.HTTPRequest{
			Method:       "PUT",
			Path:         "/v1/projects/project1/resources/foo",
			Body:         newFooResourceJSON1,
			ExpectStatus: http.StatusForbidden,
		}.Check(t.T, hh)
		mv.Allow("project:edit:foo")

		//expect error when CheckResourceAllowed fails
		m, _ := h.Team.ForAssetType("foo")
		m.(*plugins.AssetManagerStatic).CheckResourceAllowedFails = true
		assert.HTTPRequest{
			Method:       "PUT",
			Path:         "/v1/projects/project1/resources/foo",
			Body:         newFooResourceJSON1,
			ExpectStatus: http.StatusUnprocessableEntity,
			ExpectBody:   assert.StringData("CheckResourceAllowed failing as requested\n"),
		}.Check(t.T, hh)
		m.(*plugins.AssetManagerStatic).CheckResourceAllowedFails = false

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
				cloned.SizeStepPercent = 0
				cloned.SingleStep = true
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
			"size_constraints": assert.JSONObject{
				"minimum_free": 23,
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
					DomainUUID:               "domain1",
					AssetType:                "foo",
					ScrapedAt:                res.ScrapedAt,
					CheckedAt:                *res.ScrapedAt,
					CriticalThresholdPercent: 98,
					SizeStepPercent:          15,
					MinimumFreeSize:          p2uint64(23),
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
			DomainUUID:               "domain1",
			AssetType:                "foo",
			CriticalThresholdPercent: 98,
			SizeStepPercent:          15,
			MinimumFreeSize:          p2uint64(23),
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
				"minimum":      0,
				"maximum":      42000,
				"minimum_free": 0,
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
					DomainUUID:               "domain1",
					AssetType:                "foo",
					ScrapedAt:                res.ScrapedAt,
					CheckedAt:                *res.ScrapedAt,
					CriticalThresholdPercent: 98,
					SizeStepPercent:          15,
					SingleStep:               false,
					MinimumSize:              nil, //0 gets normalized to NULL
					MaximumSize:              p2uint64(42000),
					MinimumFreeSize:          nil, //0 gets normalized to NULL
				})
			} else {
				newResources3 = append(newResources3, res)
			}
		}
		t.ExpectResources(h.DB, newResources3...)

	})
}

func TestPutResourceValidationErrors(baseT *testing.T) {
	t := test.T{T: baseT}

	var maxFooSize uint64 = 30
	cfg := core.Config{
		MaxAssetSize: map[db.AssetType]*uint64{
			"foo": &maxFooSize,
		},
	}

	withHandler(t, cfg, nil, func(h *handler, hh http.Handler, mv *MockValidator, allResources []db.Resource, _ []db.Asset) {

		expectErrors := func(body assert.JSONObject, errors ...string) {
			t.T.Helper()
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
			"maximum size must be configured for foo",
		)

		expectErrors(
			assert.JSONObject{
				"scraped_at":       12345,
				"checked":          assert.JSONObject{"at": 56789},
				"asset_count":      500,
				"low_threshold":    assert.JSONObject{"usage_percent": 20},
				"high_threshold":   assert.JSONObject{"usage_percent": 80},
				"size_steps":       assert.JSONObject{"percent": 10, "single": true},
				"size_constraints": assert.JSONObject{"minimum": 30, "maximum": 20},
			},
			"resource.scraped_at cannot be set via the API",
			"resource.checked cannot be set via the API",
			"resource.asset_count cannot be set via the API",
			"delay for low threshold is missing",
			"delay for high threshold is missing",
			"percentage-based step may not be configured when single-step resizing is used",
			"maximum size must be greater than minimum size",
		)

		expectErrors(
			assert.JSONObject{
				"critical_threshold": assert.JSONObject{"usage_percent": 95},
				"size_steps":         assert.JSONObject{"percent": 10},
				"size_constraints":   assert.JSONObject{"minimum": 20},
			},
			"maximum size must be configured for foo",
		)

		expectErrors(
			assert.JSONObject{
				"critical_threshold": assert.JSONObject{"usage_percent": 95, "delay_seconds": 60},
				"size_steps":         assert.JSONObject{"percent": 10},
				"size_constraints":   assert.JSONObject{"minimum": 20, "maximum": 40},
			},
			"critical threshold may not have a delay",
			"maximum size must be 30 or less",
		)

		expectErrors(
			assert.JSONObject{
				"low_threshold":    assert.JSONObject{"usage_percent": 120, "delay_seconds": 60},
				"high_threshold":   assert.JSONObject{"usage_percent": 80, "delay_seconds": 60},
				"size_steps":       assert.JSONObject{"percent": 10},
				"size_constraints": assert.JSONObject{"maximum": 30},
			},
			"low threshold must be between 0% and 100% of usage",
			"low threshold must be below high threshold",
		)

		expectErrors(
			assert.JSONObject{
				"high_threshold":     assert.JSONObject{"usage_percent": 120, "delay_seconds": 60},
				"critical_threshold": assert.JSONObject{"usage_percent": 105},
				"size_steps":         assert.JSONObject{"percent": 10},
				"size_constraints":   assert.JSONObject{"maximum": 30},
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
				"size_constraints":   assert.JSONObject{"maximum": 30},
			},
			"low threshold must be below critical threshold",
		)

		expectErrors(
			assert.JSONObject{
				"low_threshold":      assert.JSONObject{"usage_percent": -0.3, "delay_seconds": 60},
				"high_threshold":     assert.JSONObject{"usage_percent": -0.2, "delay_seconds": 60},
				"critical_threshold": assert.JSONObject{"usage_percent": -0.1},
				"size_steps":         assert.JSONObject{"percent": 10},
				"size_constraints":   assert.JSONObject{"maximum": 30},
			},
			"low threshold must be between 0% and 100% of usage",
			"high threshold must be between 0% and 100% of usage",
			"critical threshold must be between 0% and 100% of usage",
		)

		//there's one thing we can only test with a "bar" resource since "bar" has
		//ReportsAbsoluteUsage = false
		assert.HTTPRequest{
			Method: "PUT",
			Path:   "/v1/projects/project1/resources/bar",
			Body: assert.JSONObject{
				"critical_threshold": assert.JSONObject{"usage_percent": 90},
				"size_steps":         assert.JSONObject{"single": true},
				"size_constraints":   assert.JSONObject{"minimum_free": 10},
			},
			ExpectStatus: http.StatusUnprocessableEntity,
			ExpectBody:   assert.StringData("cannot use single-step resizing: asset type does not report absolute usage\ncannot use minimum free size constraint: asset type does not report absolute usage\n"),
		}.Check(t.T, hh)

		//none of this should have touched the DB
		t.ExpectResources(h.DB, allResources...)

	})
}

func TestDeleteResource(baseT *testing.T) {
	t := test.T{T: baseT}
	withHandler(t, core.Config{}, nil, func(h *handler, hh http.Handler, mv *MockValidator, allResources []db.Resource, allAssets []db.Asset) {

		//endpoint requires a token with project access
		mv.Forbid("project:access")
		assert.HTTPRequest{
			Method:       "DELETE",
			Path:         "/v1/projects/project1/resources/foo",
			ExpectStatus: http.StatusForbidden,
		}.Check(t.T, hh)
		mv.Allow("project:access")

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
		mv.Forbid("project:show:foo")
		assert.HTTPRequest{
			Method:       "DELETE",
			Path:         "/v1/projects/project1/resources/foo",
			ExpectStatus: http.StatusForbidden,
		}.Check(t.T, hh)
		mv.Allow("project:show:foo")

		mv.Forbid("project:edit:foo")
		assert.HTTPRequest{
			Method:       "DELETE",
			Path:         "/v1/projects/project1/resources/foo",
			ExpectStatus: http.StatusForbidden,
		}.Check(t.T, hh)
		mv.Allow("project:edit:foo")

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

	})
}
