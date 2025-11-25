// SPDX-FileCopyrightText: 2019 SAP SE or an SAP affiliate company
// SPDX-License-Identifier: Apache-2.0

package api_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/sapcc/go-api-declarations/cadf"
	"github.com/sapcc/go-api-declarations/castellum"
	"github.com/sapcc/go-bits/assert"
	"github.com/sapcc/go-bits/easypg"

	"github.com/sapcc/castellum/internal/core"
	"github.com/sapcc/castellum/internal/db"
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

func TestGetProject(t *testing.T) {
	s := test.NewSetup(t,
		commonSetupOptionsForAPITest(),
	)
	hh := s.Handler

	// endpoint requires a token with project access
	s.Validator.Enforcer.Forbid("project:access")
	assert.HTTPRequest{
		Method:       "GET",
		Path:         "/v1/projects/project1",
		ExpectStatus: http.StatusForbidden,
	}.Check(t, hh)
	s.Validator.Enforcer.Allow("project:access")

	// expect empty result for project with no resources
	assert.HTTPRequest{
		Method:       "GET",
		Path:         "/v1/projects/project2",
		ExpectStatus: http.StatusOK,
		ExpectBody: assert.JSONObject{
			"resources": assert.JSONObject{},
		},
	}.Check(t, hh)

	// expect non-empty result for project with resources
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
	}.Check(t, hh)

	// expect partial result when user is not allowed to view certain resources
	s.Validator.Enforcer.Forbid("project:show:bar")
	s.Validator.Enforcer.Forbid("project:edit:foo") // this should not be an issue
	assert.HTTPRequest{
		Method:       "GET",
		Path:         "/v1/projects/project1",
		ExpectStatus: http.StatusOK,
		ExpectBody: assert.JSONObject{
			"resources": assert.JSONObject{
				"foo": initialFooResourceJSON,
			},
		},
	}.Check(t, hh)
}

func TestGetResource(t *testing.T) {
	s := test.NewSetup(t,
		commonSetupOptionsForAPITest(),
	)
	hh := s.Handler

	// endpoint requires a token with project access
	s.Validator.Enforcer.Forbid("project:access")
	assert.HTTPRequest{
		Method:       "GET",
		Path:         "/v1/projects/project1/resources/foo",
		ExpectStatus: http.StatusForbidden,
	}.Check(t, hh)
	s.Validator.Enforcer.Allow("project:access")

	// expect error for unknown project or resource
	assert.HTTPRequest{
		Method:       "GET",
		Path:         "/v1/projects/project2/resources/foo",
		ExpectStatus: http.StatusNotFound,
	}.Check(t, hh)
	assert.HTTPRequest{
		Method:       "GET",
		Path:         "/v1/projects/project1/resources/doesnotexist",
		ExpectStatus: http.StatusNotFound,
	}.Check(t, hh)

	// the "unknown" resource exists, but it should be 404 regardless because we
	// don't have an asset manager for it
	assert.HTTPRequest{
		Method:       "GET",
		Path:         "/v1/projects/project1/resources/unknown",
		ExpectStatus: http.StatusNotFound,
	}.Check(t, hh)

	// expect error for inaccessible resource
	s.Validator.Enforcer.Forbid("project:show:foo")
	assert.HTTPRequest{
		Method:       "GET",
		Path:         "/v1/projects/project1/resources/foo",
		ExpectStatus: http.StatusForbidden,
	}.Check(t, hh)
	s.Validator.Enforcer.Allow("project:show:foo")

	// happy path
	s.Validator.Enforcer.Forbid("project:edit:foo") // this should not be an issue
	assert.HTTPRequest{
		Method:       "GET",
		Path:         "/v1/projects/project1/resources/foo",
		ExpectStatus: http.StatusOK,
		ExpectBody:   initialFooResourceJSON,
	}.Check(t, hh)
	assert.HTTPRequest{
		Method:       "GET",
		Path:         "/v1/projects/project1/resources/bar",
		ExpectStatus: http.StatusOK,
		ExpectBody:   initialBarResourceJSON,
	}.Check(t, hh)
}

func TestPutResource(t *testing.T) {
	s := test.NewSetup(t,
		commonSetupOptionsForAPITest(),
	)
	hh := s.Handler

	tr, tr0 := easypg.NewTracker(t, s.DB.Db)
	tr0.Ignore()

	// mostly like `initialFooResourceJSON`, but with some delays changed and
	// single-step resizing instead of percentage-based resizing
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

	// endpoint requires a token with project access
	s.Validator.Enforcer.Forbid("project:access")
	assert.HTTPRequest{
		Method:       "PUT",
		Path:         "/v1/projects/project1/resources/foo",
		Body:         newFooResourceJSON1,
		ExpectStatus: http.StatusForbidden,
	}.Check(t, hh)
	s.Validator.Enforcer.Allow("project:access")

	// expect error for unknown resource
	assert.HTTPRequest{
		Method:       "PUT",
		Path:         "/v1/projects/project1/resources/doesnotexist",
		Body:         newFooResourceJSON1,
		ExpectStatus: http.StatusNotFound,
	}.Check(t, hh)

	// the "unknown" resource exists, but it should be 404 regardless because we
	// don't have an asset manager for it
	assert.HTTPRequest{
		Method:       "PUT",
		Path:         "/v1/projects/project1/resources/unknown",
		Body:         newFooResourceJSON1,
		ExpectStatus: http.StatusNotFound,
	}.Check(t, hh)

	// expect error for inaccessible resource
	s.Validator.Enforcer.Forbid("project:show:foo")
	assert.HTTPRequest{
		Method:       "PUT",
		Path:         "/v1/projects/project1/resources/foo",
		Body:         newFooResourceJSON1,
		ExpectStatus: http.StatusForbidden,
	}.Check(t, hh)
	s.Validator.Enforcer.Allow("project:show:foo")

	s.Validator.Enforcer.Forbid("project:edit:foo")
	assert.HTTPRequest{
		Method:       "PUT",
		Path:         "/v1/projects/project1/resources/foo",
		Body:         newFooResourceJSON1,
		ExpectStatus: http.StatusForbidden,
	}.Check(t, hh)
	s.Validator.Enforcer.Allow("project:edit:foo")

	// expect error when CheckResourceAllowed fails
	mgr := s.ManagerForAssetType("foo")
	mgr.CheckResourceAllowedFails = true
	assert.HTTPRequest{
		Method:       "PUT",
		Path:         "/v1/projects/project1/resources/foo",
		Body:         newFooResourceJSON1,
		ExpectStatus: http.StatusUnprocessableEntity,
		ExpectBody:   assert.StringData("CheckResourceAllowed failing as requested\n"),
	}.Check(t, hh)
	mgr.CheckResourceAllowedFails = false

	// since all tests above were error cases, expect the DB to be unchanged
	tr.DBChanges().AssertEmpty()

	// happy path
	s.Auditor.IgnoreEventsUntilNow()
	assert.HTTPRequest{
		Method:       "PUT",
		Path:         "/v1/projects/project1/resources/foo",
		Body:         newFooResourceJSON1,
		ExpectStatus: http.StatusAccepted,
	}.Check(t, hh)

	// expect the resource to have been updated
	tr.DBChanges().AssertEqualf(`
		UPDATE resources SET low_delay_seconds = 1800, high_delay_seconds = 900, size_step_percent = 0, single_step = TRUE WHERE id = 1 AND scope_uuid = 'project1' AND asset_type = 'foo';
	`)
	s.Auditor.ExpectEvents(t, cadf.Event{
		Action:      "update/foo",
		Outcome:     "success",
		Reason:      cadf.Reason{ReasonType: "HTTP", ReasonCode: "202"},
		RequestPath: "/v1/projects/project1/resources/foo",
		Target: cadf.Resource{
			TypeURI:   "data/security/project",
			ID:        "project1",
			ProjectID: "project1",
			Attachments: []cadf.Attachment{{
				Name:    "payload",
				TypeURI: "mime:application/json",
				Content: toJSONVia[castellum.Resource](newFooResourceJSON1),
			}},
		},
	})

	// test disabling low and high thresholds, and enabling critical threshold
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
	}.Check(t, hh)
	tr.DBChanges().AssertEqualf(`
		UPDATE resources SET low_threshold_percent = '{"singular":0}', low_delay_seconds = 0, high_threshold_percent = '{"singular":0}', high_delay_seconds = 0, critical_threshold_percent = '{"singular":98}', size_step_percent = 15, min_free_size = 23, single_step = FALSE WHERE id = 1 AND scope_uuid = 'project1' AND asset_type = 'foo';
	`)

	// test enabling low and high thresholds, and disabling critical threshold
	assert.HTTPRequest{
		Method:       "PUT",
		Path:         "/v1/projects/project1/resources/foo",
		Body:         newFooResourceJSON1,
		ExpectStatus: http.StatusAccepted,
	}.Check(t, hh)
	tr.DBChanges().AssertEqualf(`
		UPDATE resources SET low_threshold_percent = '{"singular":20}', low_delay_seconds = 1800, high_threshold_percent = '{"singular":80}', high_delay_seconds = 900, critical_threshold_percent = '{"singular":0}', size_step_percent = 0, min_free_size = NULL, single_step = TRUE WHERE id = 1 AND scope_uuid = 'project1' AND asset_type = 'foo';
	`)

	// test creating a new resource from scratch (rather than updating an existing one)
	assert.HTTPRequest{
		Method:       "PUT",
		Path:         "/v1/projects/project3/resources/foo",
		Body:         newFooResourceJSON2,
		ExpectStatus: http.StatusAccepted,
	}.Check(t, hh)
	tr.DBChanges().AssertEqualf(`
		INSERT INTO resources (id, scope_uuid, asset_type, low_threshold_percent, low_delay_seconds, high_threshold_percent, high_delay_seconds, critical_threshold_percent, size_step_percent, min_free_size, domain_uuid, next_scrape_at) VALUES (5, 'project3', 'foo', '{"singular":0}', 0, '{"singular":0}', 0, '{"singular":98}', 15, 23, 'domain1', 0);
	`)

	// test setting constraints
	newFooResourceJSON3 := assert.JSONObject{
		"critical_threshold": assert.JSONObject{
			"usage_percent": 98,
		},
		"size_steps": assert.JSONObject{
			"percent": 15,
		},
		"size_constraints": assert.JSONObject{
			"minimum":                  0, // gets normalized into NULL
			"maximum":                  42000,
			"minimum_free":             200,
			"minimum_free_is_critical": true,
		},
	}
	assert.HTTPRequest{
		Method:       "PUT",
		Path:         "/v1/projects/project1/resources/foo",
		Body:         newFooResourceJSON3,
		ExpectStatus: http.StatusAccepted,
	}.Check(t, hh)

	// expect the resource to have been updated
	tr.DBChanges().AssertEqualf(`
		UPDATE resources SET low_threshold_percent = '{"singular":0}', low_delay_seconds = 0, high_threshold_percent = '{"singular":0}', high_delay_seconds = 0, critical_threshold_percent = '{"singular":98}', size_step_percent = 15, max_size = 42000, min_free_size = 200, single_step = FALSE, min_free_is_critical = TRUE WHERE id = 1 AND scope_uuid = 'project1' AND asset_type = 'foo';
	`)
}

func toJSONVia[T any](in any) string {
	// This is mostly the same as `must.Return(json.Marshal(in))`, but deserializes into
	// T in an intermediate step to render the JSON with the correct field order.
	// Used for audit event matches.
	buf, err := json.Marshal(in)
	if err != nil {
		panic(err.Error())
	}
	var intermediate T
	err = json.Unmarshal(buf, &intermediate)
	if err != nil {
		panic(err.Error())
	}
	buf, err = json.Marshal(intermediate)
	if err != nil {
		panic(err.Error())
	}
	return string(buf)
}

func TestMaxAssetSizeFor(t *testing.T) {
	var (
		maxBarSize  = uint64(42)
		maxFooSize  = uint64(30)
		maxSomeSize = uint64(23)
		cfg         = core.Config{
			MaxAssetSizeRules: []core.MaxAssetSizeRule{
				{AssetTypeRx: "foo.*", ScopeUUID: "", Value: maxFooSize},
				{AssetTypeRx: ".*bar", ScopeUUID: "", Value: maxBarSize},
				{AssetTypeRx: "some.*", ScopeUUID: "somescope", Value: maxSomeSize},
			},
		}
	)

	assert.DeepEqual(t, "foo", *cfg.MaxAssetSizeFor(db.AssetType("foo"), ""), maxFooSize)
	assert.DeepEqual(t, "bar", *cfg.MaxAssetSizeFor(db.AssetType("bar"), ""), maxBarSize)
	assert.DeepEqual(t, "foobar", *cfg.MaxAssetSizeFor(db.AssetType("foobar"), ""), maxBarSize)
	assert.DeepEqual(t, "somebar", *cfg.MaxAssetSizeFor(db.AssetType("somebar"), ""), maxBarSize)
	assert.DeepEqual(t, "somebar+scope", *cfg.MaxAssetSizeFor(db.AssetType("somebar"), "somescope"), maxSomeSize)
	assert.DeepEqual(t, "buz", cfg.MaxAssetSizeFor(db.AssetType("buz"), ""), (*uint64)(nil))
	assert.DeepEqual(t, "somefoo", cfg.MaxAssetSizeFor(db.AssetType("somefoo"), ""), (*uint64)(nil))
}

func TestPutResourceValidationErrors(t *testing.T) {
	s := test.NewSetup(t,
		commonSetupOptionsForAPITest(),
		test.WithConfig(`{
			"max_asset_sizes": [
				{ "asset_type": "foo", "value": 30 }
			]
		}`),
	)
	hh := s.Handler

	tr, tr0 := easypg.NewTracker(t, s.DB.Db)
	tr0.Ignore()

	expectErrors := func(assetType string, body assert.JSONObject, errors ...string) {
		t.Helper()
		assert.HTTPRequest{
			Method:       "PUT",
			Path:         "/v1/projects/project1/resources/" + assetType,
			Body:         body,
			ExpectStatus: http.StatusUnprocessableEntity,
			ExpectBody:   assert.StringData(strings.Join(errors, "\n") + "\n"),
		}.Check(t, hh)
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

	expectErrors("foo",
		assert.JSONObject{
			"critical_threshold": assert.JSONObject{"usage_percent": 95},
			"size_steps":         assert.JSONObject{"percent": 10},
			"size_constraints":   assert.JSONObject{"minimum": 20, "maximum": 30, "minimum_free_is_critical": true},
		},
		"threshold for minimum free space must be configured",
	)

	// none of this should have touched the DB
	tr.DBChanges().AssertEmpty()
}

func TestDeleteResource(t *testing.T) {
	s := test.NewSetup(t,
		commonSetupOptionsForAPITest(),
	)
	hh := s.Handler

	tr, tr0 := easypg.NewTracker(t, s.DB.Db)
	tr0.Ignore()

	// endpoint requires a token with project access
	s.Validator.Enforcer.Forbid("project:access")
	assert.HTTPRequest{
		Method:       "DELETE",
		Path:         "/v1/projects/project1/resources/foo",
		ExpectStatus: http.StatusForbidden,
	}.Check(t, hh)
	s.Validator.Enforcer.Allow("project:access")

	// expect error for unknown project or resource
	assert.HTTPRequest{
		Method:       "DELETE",
		Path:         "/v1/projects/project2/resources/foo",
		ExpectStatus: http.StatusNotFound,
	}.Check(t, hh)
	assert.HTTPRequest{
		Method:       "DELETE",
		Path:         "/v1/projects/project1/resources/doesnotexist",
		ExpectStatus: http.StatusNotFound,
	}.Check(t, hh)

	// the "unknown" resource exists, but it should be 404 regardless because we
	// don't have an asset manager for it
	assert.HTTPRequest{
		Method:       "DELETE",
		Path:         "/v1/projects/project1/resources/unknown",
		ExpectStatus: http.StatusNotFound,
	}.Check(t, hh)

	// expect error for inaccessible resource
	s.Validator.Enforcer.Forbid("project:show:foo")
	assert.HTTPRequest{
		Method:       "DELETE",
		Path:         "/v1/projects/project1/resources/foo",
		ExpectStatus: http.StatusForbidden,
	}.Check(t, hh)
	s.Validator.Enforcer.Allow("project:show:foo")

	s.Validator.Enforcer.Forbid("project:edit:foo")
	assert.HTTPRequest{
		Method:       "DELETE",
		Path:         "/v1/projects/project1/resources/foo",
		ExpectStatus: http.StatusForbidden,
	}.Check(t, hh)
	s.Validator.Enforcer.Allow("project:edit:foo")

	// since all tests above were error cases, expect all resources to still be there
	tr.DBChanges().AssertEmpty()

	// happy path
	s.Auditor.IgnoreEventsUntilNow()
	assert.HTTPRequest{
		Method:       "DELETE",
		Path:         "/v1/projects/project1/resources/foo",
		ExpectStatus: http.StatusNoContent,
	}.Check(t, hh)

	// expect this resource and its assets to be gone
	tr.DBChanges().AssertEqualf(`
		DELETE FROM assets WHERE id = 1 AND resource_id = 1 AND uuid = 'fooasset1';
		DELETE FROM assets WHERE id = 2 AND resource_id = 1 AND uuid = 'fooasset2';
		DELETE FROM finished_operations WHERE asset_id = 1 AND reason = 'critical' AND outcome = 'error-resolved' AND old_size = 0 AND new_size = 0 AND created_at = 21 AND confirmed_at = 22 AND greenlit_at = 22 AND finished_at = 23 AND greenlit_by_user_uuid = 'user3' AND error_message = '' AND errored_attempts = 0 AND usage = '{"singular":0}';
		DELETE FROM finished_operations WHERE asset_id = 1 AND reason = 'critical' AND outcome = 'errored' AND old_size = 1024 AND new_size = 1025 AND created_at = 51 AND confirmed_at = 52 AND greenlit_at = 52 AND finished_at = 53 AND greenlit_by_user_uuid = NULL AND error_message = 'datacenter is on fire' AND errored_attempts = 0 AND usage = '{"singular":983.04}';
		DELETE FROM finished_operations WHERE asset_id = 1 AND reason = 'high' AND outcome = 'succeeded' AND old_size = 1023 AND new_size = 1024 AND created_at = 41 AND confirmed_at = 42 AND greenlit_at = 43 AND finished_at = 44 AND greenlit_by_user_uuid = 'user2' AND error_message = '' AND errored_attempts = 0 AND usage = '{"singular":818.4}';
		DELETE FROM finished_operations WHERE asset_id = 1 AND reason = 'low' AND outcome = 'cancelled' AND old_size = 1000 AND new_size = 900 AND created_at = 31 AND confirmed_at = NULL AND greenlit_at = NULL AND finished_at = 32 AND greenlit_by_user_uuid = NULL AND error_message = '' AND errored_attempts = 0 AND usage = '{"singular":200}';
		DELETE FROM resources WHERE id = 1 AND scope_uuid = 'project1' AND asset_type = 'foo';
	`)
	s.Auditor.ExpectEvents(t, cadf.Event{
		Action:      "disable/foo",
		Outcome:     "success",
		Reason:      cadf.Reason{ReasonType: "HTTP", ReasonCode: "204"},
		RequestPath: "/v1/projects/project1/resources/foo",
		Target: cadf.Resource{
			TypeURI:   "data/security/project",
			ID:        "project1",
			ProjectID: "project1",
		},
	})
}

func TestSeedBlocksResourceUpdates(t *testing.T) {
	s := test.NewSetup(t,
		commonSetupOptionsForAPITest(),
		// this seed matches what we have in fixtures/start-data.sql
		test.WithConfig(`{
			"project_seeds": [
				{
					"project_name": "First Project",
					"domain_name":  "First Domain",
					"disabled_resources": [ "qux" ],
					"resources": {
						"foo": {
							"low_threshold":  { "usage_percent": 20, "delay_seconds": 3600 },
							"high_threshold": { "usage_percent": 80, "delay_seconds": 1800 },
							"size_steps":     { "percent": 20 }
						}
					}
				}
			]
		}`),
	)
	hh := s.Handler

	// cannot PUT an existing resource defined by the seed
	assert.HTTPRequest{
		Method:       "PUT",
		Path:         "/v1/projects/project1/resources/foo",
		Body:         initialFooResourceJSON,
		ExpectStatus: http.StatusConflict,
	}.Check(t, hh)

	// cannot DELETE an existing resource defined by the seed
	assert.HTTPRequest{
		Method:       "DELETE",
		Path:         "/v1/projects/project1/resources/foo",
		Body:         initialFooResourceJSON,
		ExpectStatus: http.StatusConflict,
	}.Check(t, hh)

	// cannot PUT a missing resource disabled by the seed
	assert.HTTPRequest{
		Method:       "PUT",
		Path:         "/v1/projects/project1/resources/qux",
		Body:         initialFooResourceJSON,
		ExpectStatus: http.StatusConflict,
	}.Check(t, hh)
}
