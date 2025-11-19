// SPDX-FileCopyrightText: 2019 SAP SE or an SAP affiliate company
// SPDX-License-Identifier: Apache-2.0

package api_test

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/sapcc/go-api-declarations/castellum"
	"github.com/sapcc/go-bits/assert"
	"github.com/sapcc/go-bits/easypg"
	"github.com/sapcc/go-bits/httpapi"

	"github.com/sapcc/castellum/internal/api"
	"github.com/sapcc/castellum/internal/core"
	"github.com/sapcc/castellum/internal/db"
	"github.com/sapcc/castellum/internal/plugins"
	"github.com/sapcc/castellum/internal/test"
)

func TestMain(m *testing.M) {
	easypg.WithTestDB(m, func() int { return m.Run() })
}

func withHandler(t test.T, cfg core.Config, timeNow func() time.Time, action func(test.Setup, http.Handler, core.AssetManagerTeam, []db.Resource, []db.Asset)) {
	s := test.NewSetup(t.T,
		test.WithDBFixtureFile("fixtures/start-data.sql"),
	)
	team := core.AssetManagerTeam{
		&plugins.AssetManagerStatic{AssetType: "foo"},
		&plugins.AssetManagerStatic{AssetType: "bar", UsageMetrics: []castellum.UsageMetric{"first", "second"}, ExpectsConfiguration: true},
		&plugins.AssetManagerStatic{AssetType: "qux", ConflictsWithAssetType: "foo"},
	}

	var resources []db.Resource
	_, err := s.DB.Select(&resources, `SELECT * FROM resources ORDER BY ID`)
	t.Must(err)

	var assets []db.Asset
	_, err = s.DB.Select(&assets, `SELECT * FROM assets ORDER BY ID`)
	t.Must(err)

	if timeNow == nil {
		timeNow = time.Now
	}
	hh := httpapi.Compose(
		api.NewHandler(cfg, s.DB, team, s.Validator, s.ProviderClient, s.Auditor, timeNow),
		httpapi.WithoutLogging(),
	)
	action(s, hh, team, resources, assets)
}

func testCommonEndpointBehavior(t test.T, hh http.Handler, s test.Setup, pathPattern string) {
	path := func(projectID, resourceID string) string {
		return fmt.Sprintf(pathPattern, projectID, resourceID)
	}

	// endpoint requires a token with project access
	s.Validator.Enforcer.Forbid("project:access")
	assert.HTTPRequest{
		Method:       "GET",
		Path:         path("project1", "foo"),
		ExpectStatus: http.StatusForbidden,
	}.Check(t.T, hh)
	s.Validator.Enforcer.Allow("project:access")

	// expect error for unknown project or resource
	assert.HTTPRequest{
		Method:       "GET",
		Path:         path("project2", "foo"),
		ExpectStatus: http.StatusNotFound,
	}.Check(t.T, hh)
	assert.HTTPRequest{
		Method:       "GET",
		Path:         path("project1", "doesnotexist"),
		ExpectStatus: http.StatusNotFound,
	}.Check(t.T, hh)

	// the "unknown" resource exists, but it should be 404 regardless because we
	// don't have an asset manager for it
	assert.HTTPRequest{
		Method:       "GET",
		Path:         path("project1", "unknown"),
		ExpectStatus: http.StatusNotFound,
	}.Check(t.T, hh)

	// expect error for inaccessible resource
	s.Validator.Enforcer.Forbid("project:show:foo")
	assert.HTTPRequest{
		Method:       "GET",
		Path:         path("project1", "foo"),
		ExpectStatus: http.StatusForbidden,
	}.Check(t.T, hh)
	s.Validator.Enforcer.Allow("project:show:foo")
}
