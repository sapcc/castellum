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

	"github.com/sapcc/go-bits/assert"
	"github.com/sapcc/go-bits/easypg"

	"github.com/sapcc/castellum/internal/core"
	"github.com/sapcc/castellum/internal/db"
	"github.com/sapcc/castellum/internal/plugins"
	"github.com/sapcc/castellum/internal/test"
)

// JSON serializations of the records in internal/api/fixtures/start-data.sql
var (
	initialFooResourceJSON = assert.JSONObject{
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
		"checked": assert.JSONObject{
			"error": "datacenter is on fire",
		},
		"asset_count": 1,
		"config": assert.JSONObject{
			"foo": "bar",
		},
		"critical_threshold": assert.JSONObject{
			"usage_percent": assert.JSONObject{
				"first":  95,
				"second": 97,
			},
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
	clock := test.FakeClock(3600)
	withHandler(t, core.Config{}, clock.Now, func(h *handler, hh http.Handler, mv *MockValidator, allResources []db.Resource, _ []db.Asset) {
		tr, tr0 := easypg.NewTracker(t.T, h.DB.Db)
		tr0.Ignore()

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
		tr.DBChanges().AssertEmpty()

		//happy path
		assert.HTTPRequest{
			Method:       "PUT",
			Path:         "/v1/projects/project1/resources/foo",
			Body:         newFooResourceJSON1,
			ExpectStatus: http.StatusAccepted,
		}.Check(t.T, hh)

		//expect the resource to have been updated
		tr.DBChanges().AssertEqualf(`
			UPDATE resources SET low_delay_seconds = 1800, high_delay_seconds = 900, size_step_percent = 0, single_step = TRUE WHERE id = 1 AND scope_uuid = 'project1' AND asset_type = 'foo';
		`)

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
		tr.DBChanges().AssertEqualf(`
			UPDATE resources SET low_threshold_percent = '{"singular":0}', low_delay_seconds = 0, high_threshold_percent = '{"singular":0}', high_delay_seconds = 0, critical_threshold_percent = '{"singular":98}', size_step_percent = 15, min_free_size = 23, single_step = FALSE WHERE id = 1 AND scope_uuid = 'project1' AND asset_type = 'foo';
		`)

		//test enabling low and high thresholds, and disabling critical threshold
		assert.HTTPRequest{
			Method:       "PUT",
			Path:         "/v1/projects/project1/resources/foo",
			Body:         newFooResourceJSON1,
			ExpectStatus: http.StatusAccepted,
		}.Check(t.T, hh)
		tr.DBChanges().AssertEqualf(`
			UPDATE resources SET low_threshold_percent = '{"singular":20}', low_delay_seconds = 1800, high_threshold_percent = '{"singular":80}', high_delay_seconds = 900, critical_threshold_percent = '{"singular":0}', size_step_percent = 0, min_free_size = NULL, single_step = TRUE WHERE id = 1 AND scope_uuid = 'project1' AND asset_type = 'foo';
		`)

		//test creating a new resource from scratch (rather than updating an existing one)
		assert.HTTPRequest{
			Method:       "PUT",
			Path:         "/v1/projects/project3/resources/foo",
			Body:         newFooResourceJSON2,
			ExpectStatus: http.StatusAccepted,
		}.Check(t.T, hh)
		tr.DBChanges().AssertEqualf(`
			INSERT INTO resources (id, scope_uuid, asset_type, low_threshold_percent, low_delay_seconds, high_threshold_percent, high_delay_seconds, critical_threshold_percent, size_step_percent, min_free_size, domain_uuid, next_scrape_at) VALUES (5, 'project3', 'foo', '{"singular":0}', 0, '{"singular":0}', 0, '{"singular":98}', 15, 23, 'domain1', 0);
		`)

		//test setting constraints
		newFooResourceJSON3 := assert.JSONObject{
			"critical_threshold": assert.JSONObject{
				"usage_percent": 98,
			},
			"size_steps": assert.JSONObject{
				"percent": 15,
			},
			"size_constraints": assert.JSONObject{
				"minimum":      0, //gets normalized into NULL
				"maximum":      42000,
				"minimum_free": 0, //gets normalized into NULL
			},
		}
		assert.HTTPRequest{
			Method:       "PUT",
			Path:         "/v1/projects/project1/resources/foo",
			Body:         newFooResourceJSON3,
			ExpectStatus: http.StatusAccepted,
		}.Check(t.T, hh)

		//expect the resource to have been updated
		tr.DBChanges().AssertEqualf(`
			UPDATE resources SET low_threshold_percent = '{"singular":0}', low_delay_seconds = 0, high_threshold_percent = '{"singular":0}', high_delay_seconds = 0, critical_threshold_percent = '{"singular":98}', size_step_percent = 15, max_size = 42000, single_step = FALSE WHERE id = 1 AND scope_uuid = 'project1' AND asset_type = 'foo';
		`)
	})
}

func TestMaxAssetSizeFor(t *testing.T) {
	var (
		maxBarSize = uint64(42)
		maxFooSize = uint64(30)
		cfg        = core.Config{
			MaxAssetSizeRules: []core.MaxAssetSizeRule{
				{AssetTypeRx: "foo.*", Value: maxFooSize},
				{AssetTypeRx: ".*bar", Value: maxBarSize},
			},
		}
	)

	assert.DeepEqual(t, "foo", *cfg.MaxAssetSizeFor(db.AssetType("foo")), maxFooSize)
	assert.DeepEqual(t, "bar", *cfg.MaxAssetSizeFor(db.AssetType("bar")), maxBarSize)
	assert.DeepEqual(t, "foobar", *cfg.MaxAssetSizeFor(db.AssetType("foobar")), maxFooSize)
	assert.DeepEqual(t, "buz", cfg.MaxAssetSizeFor(db.AssetType("buz")), (*uint64)(nil))
	assert.DeepEqual(t, "somefoo", cfg.MaxAssetSizeFor(db.AssetType("somefoo")), (*uint64)(nil))
}

func TestPutResourceValidationErrors(baseT *testing.T) {
	var cfg = core.Config{
		MaxAssetSizeRules: []core.MaxAssetSizeRule{
			{AssetTypeRx: "foo", Value: 30},
		},
	}

	t := test.T{T: baseT}
	withHandler(t, cfg, nil, func(h *handler, hh http.Handler, mv *MockValidator, allResources []db.Resource, _ []db.Asset) {
		tr, tr0 := easypg.NewTracker(t.T, h.DB.Db)
		tr0.Ignore()

		expectErrors := func(assetType string, body assert.JSONObject, errors ...string) {
			t.T.Helper()
			assert.HTTPRequest{
				Method:       "PUT",
				Path:         "/v1/projects/project1/resources/" + assetType,
				Body:         body,
				ExpectStatus: http.StatusUnprocessableEntity,
				ExpectBody:   assert.StringData(strings.Join(errors, "\n") + "\n"),
			}.Check(t.T, hh)
		}

		expectErrors("foo",
			assert.JSONObject{
				"critical_dingsbums": assert.JSONObject{"usage_percent": 95},
			},
			`request body is not valid JSON: json: unknown field "critical_dingsbums"`,
		)

		expectErrors("foo",
			assert.JSONObject{},
			"at least one threshold must be configured",
			"size step must be greater than 0%",
			"maximum size must be configured for foo",
		)

		expectErrors("foo",
			assert.JSONObject{
				"asset_count":      500,
				"config":           assert.JSONObject{"foo": "bar"},
				"low_threshold":    assert.JSONObject{"usage_percent": 20},
				"high_threshold":   assert.JSONObject{"usage_percent": 80},
				"size_steps":       assert.JSONObject{"percent": 10, "single": true},
				"size_constraints": assert.JSONObject{"minimum": 30, "maximum": 20},
			},
			"no configuration allowed for this asset type",
			"resource.asset_count cannot be set via the API",
			"delay for low threshold is missing",
			"delay for high threshold is missing",
			"percentage-based step may not be configured when single-step resizing is used",
			"maximum size must be greater than minimum size",
		)

		expectErrors("foo",
			assert.JSONObject{
				"critical_threshold": assert.JSONObject{"usage_percent": 95},
				"size_steps":         assert.JSONObject{"percent": 10},
				"size_constraints":   assert.JSONObject{"minimum": 20},
			},
			"maximum size must be configured for foo",
		)

		expectErrors("foo",
			assert.JSONObject{
				"critical_threshold": assert.JSONObject{"usage_percent": 95, "delay_seconds": 60},
				"size_steps":         assert.JSONObject{"percent": 10},
				"size_constraints":   assert.JSONObject{"minimum": 20, "maximum": 40},
			},
			"critical threshold may not have a delay",
			"maximum size must be 30 or less",
		)

		expectErrors("foo",
			assert.JSONObject{
				"low_threshold":    assert.JSONObject{"usage_percent": 120, "delay_seconds": 60},
				"high_threshold":   assert.JSONObject{"usage_percent": 80, "delay_seconds": 60},
				"size_steps":       assert.JSONObject{"percent": 10},
				"size_constraints": assert.JSONObject{"maximum": 30},
			},
			"low threshold must be above 0% and below or at 100% of usage",
			"low threshold must be below high threshold",
		)

		expectErrors("foo",
			assert.JSONObject{
				"high_threshold":     assert.JSONObject{"usage_percent": 120, "delay_seconds": 60},
				"critical_threshold": assert.JSONObject{"usage_percent": 105},
				"size_steps":         assert.JSONObject{"percent": 10},
				"size_constraints":   assert.JSONObject{"maximum": 30},
			},
			"high threshold must be above 0% and below or at 100% of usage",
			"critical threshold must be above 0% and below or at 100% of usage",
			"high threshold must be below critical threshold",
		)

		expectErrors("foo",
			assert.JSONObject{
				"low_threshold":      assert.JSONObject{"usage_percent": 20, "delay_seconds": 60},
				"critical_threshold": assert.JSONObject{"usage_percent": 15},
				"size_steps":         assert.JSONObject{"percent": 10},
				"size_constraints":   assert.JSONObject{"maximum": 30},
			},
			"low threshold must be below critical threshold",
		)

		expectErrors("foo",
			assert.JSONObject{
				"low_threshold":      assert.JSONObject{"usage_percent": -0.3, "delay_seconds": 60},
				"high_threshold":     assert.JSONObject{"usage_percent": -0.2, "delay_seconds": 60},
				"critical_threshold": assert.JSONObject{"usage_percent": -0.1},
				"size_steps":         assert.JSONObject{"percent": 10},
				"size_constraints":   assert.JSONObject{"maximum": 30},
			},
			"low threshold must be above 0% and below or at 100% of usage",
			"high threshold must be above 0% and below or at 100% of usage",
			"critical threshold must be above 0% and below or at 100% of usage",
		)

		expectErrors("foo",
			assert.JSONObject{
				"low_threshold":      assert.JSONObject{"usage_percent": assert.JSONObject{"first": 1, "second": 2}, "delay_seconds": 60},
				"high_threshold":     assert.JSONObject{"usage_percent": assert.JSONObject{"first": 2, "second": 4}, "delay_seconds": 60},
				"critical_threshold": assert.JSONObject{"usage_percent": assert.JSONObject{"first": 3, "second": 6}},
				"size_steps":         assert.JSONObject{"percent": 10},
				"size_constraints":   assert.JSONObject{"maximum": 30},
			},
			"missing low threshold",
			`low threshold specified for metric "first" which is not valid for this asset type`,
			`low threshold specified for metric "second" which is not valid for this asset type`,
			"missing high threshold",
			`high threshold specified for metric "first" which is not valid for this asset type`,
			`high threshold specified for metric "second" which is not valid for this asset type`,
			"missing critical threshold",
			`critical threshold specified for metric "first" which is not valid for this asset type`,
			`critical threshold specified for metric "second" which is not valid for this asset type`,
		)

		expectErrors("bar",
			assert.JSONObject{
				"config":             assert.JSONObject{"foo": "bar"},
				"low_threshold":      assert.JSONObject{"usage_percent": 1, "delay_seconds": 60},
				"high_threshold":     assert.JSONObject{"usage_percent": 2, "delay_seconds": 60},
				"critical_threshold": assert.JSONObject{"usage_percent": 3},
				"size_steps":         assert.JSONObject{"percent": 10},
				"size_constraints":   assert.JSONObject{"maximum": 30},
			},
			"missing low threshold for first",
			"missing low threshold for second",
			`low threshold specified for metric "singular" which is not valid for this asset type`,
			"missing high threshold for first",
			"missing high threshold for second",
			`high threshold specified for metric "singular" which is not valid for this asset type`,
			"missing critical threshold for first",
			"missing critical threshold for second",
			`critical threshold specified for metric "singular" which is not valid for this asset type`,
		)

		expectErrors("bar",
			assert.JSONObject{
				"config":             assert.JSONObject{"foo": "wrong"},
				"critical_threshold": assert.JSONObject{"usage_percent": assert.JSONObject{"first": 1, "second": 2}},
				"size_steps":         assert.JSONObject{"percent": 10},
			},
			"wrong configuration was supplied",
		)

		expectErrors("bar",
			assert.JSONObject{
				"critical_threshold": assert.JSONObject{"usage_percent": assert.JSONObject{"first": 1, "second": 2}},
				"size_steps":         assert.JSONObject{"percent": 10},
			},
			"type-specific configuration must be provided for this asset type",
		)

		expectErrors("qux",
			assert.JSONObject{
				"critical_threshold": assert.JSONObject{"usage_percent": 90},
				"size_steps":         assert.JSONObject{"percent": 10},
			},
			"cannot create qux resource because there is a foo resource",
		)

		//none of this should have touched the DB
		tr.DBChanges().AssertEmpty()
	})
}

func TestDeleteResource(baseT *testing.T) {
	t := test.T{T: baseT}
	withHandler(t, core.Config{}, nil, func(h *handler, hh http.Handler, mv *MockValidator, allResources []db.Resource, allAssets []db.Asset) {
		tr, tr0 := easypg.NewTracker(t.T, h.DB.Db)
		tr0.Ignore()

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
		tr.DBChanges().AssertEmpty()

		//happy path
		assert.HTTPRequest{
			Method:       "DELETE",
			Path:         "/v1/projects/project1/resources/foo",
			ExpectStatus: http.StatusNoContent,
		}.Check(t.T, hh)

		//expect this resource and its assets to be gone
		tr.DBChanges().AssertEqualf(`
			DELETE FROM assets WHERE id = 1 AND resource_id = 1 AND uuid = 'fooasset1';
			DELETE FROM assets WHERE id = 2 AND resource_id = 1 AND uuid = 'fooasset2';
			DELETE FROM finished_operations WHERE asset_id = 1 AND reason = 'critical' AND outcome = 'errored' AND old_size = 1024 AND new_size = 1025 AND created_at = 51 AND confirmed_at = 52 AND greenlit_at = 52 AND finished_at = 53 AND greenlit_by_user_uuid = NULL AND error_message = 'datacenter is on fire' AND errored_attempts = 0 AND usage = '{"singular":983.04}';
			DELETE FROM finished_operations WHERE asset_id = 1 AND reason = 'high' AND outcome = 'succeeded' AND old_size = 1023 AND new_size = 1024 AND created_at = 41 AND confirmed_at = 42 AND greenlit_at = 43 AND finished_at = 44 AND greenlit_by_user_uuid = 'user2' AND error_message = '' AND errored_attempts = 0 AND usage = '{"singular":818.4}';
			DELETE FROM finished_operations WHERE asset_id = 1 AND reason = 'low' AND outcome = 'cancelled' AND old_size = 1000 AND new_size = 900 AND created_at = 31 AND confirmed_at = NULL AND greenlit_at = NULL AND finished_at = 32 AND greenlit_by_user_uuid = NULL AND error_message = '' AND errored_attempts = 0 AND usage = '{"singular":200}';
			DELETE FROM resources WHERE id = 1 AND scope_uuid = 'project1' AND asset_type = 'foo';
		`)
	})
}
