// SPDX-FileCopyrightText: 2019 SAP SE
// SPDX-License-Identifier: Apache-2.0

package api

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/go-gorp/gorp/v3"
	"github.com/sapcc/go-api-declarations/castellum"
	"github.com/sapcc/go-bits/assert"
	"github.com/sapcc/go-bits/audittools"
	"github.com/sapcc/go-bits/easypg"
	"github.com/sapcc/go-bits/httpapi"
	"github.com/sapcc/go-bits/mock"

	"github.com/sapcc/castellum/internal/core"
	"github.com/sapcc/castellum/internal/db"
	"github.com/sapcc/castellum/internal/plugins"
	"github.com/sapcc/castellum/internal/test"
)

func TestMain(m *testing.M) {
	easypg.WithTestDB(m, func() int { return m.Run() })
}

func withHandler(t test.T, cfg core.Config, timeNow func() time.Time, action func(*handler, http.Handler, *mock.Validator[*mock.Enforcer], *audittools.MockAuditor, []db.Resource, []db.Asset)) {
	baseline := "fixtures/start-data.sql"
	t.WithDB(&baseline, func(dbi *gorp.DbMap) {
		team := core.AssetManagerTeam{
			&plugins.AssetManagerStatic{AssetType: "foo"},
			&plugins.AssetManagerStatic{AssetType: "bar", UsageMetrics: []castellum.UsageMetric{"first", "second"}, ExpectsConfiguration: true},
			&plugins.AssetManagerStatic{AssetType: "qux", ConflictsWithAssetType: "foo"},
		}
		mv := mock.NewValidator(mock.NewEnforcer(), nil)
		mpc := test.MockProviderClient{
			Domains: map[string]core.CachedDomain{
				"domain1": {Name: "First Domain"},
			},
			Projects: map[string]core.CachedProject{
				"project1": {Name: "First Project", DomainID: "domain1"},
				"project2": {Name: "Second Project", DomainID: "domain1"},
				"project3": {Name: "Third Project", DomainID: "domain1"},
			},
		}
		auditor := audittools.NewMockAuditor()

		var resources []db.Resource
		_, err := dbi.Select(&resources, `SELECT * FROM resources ORDER BY ID`)
		t.Must(err)

		var assets []db.Asset
		_, err = dbi.Select(&assets, `SELECT * FROM assets ORDER BY ID`)
		t.Must(err)

		if timeNow == nil {
			timeNow = time.Now
		}
		h := &handler{Config: cfg, DB: dbi, Team: team, Validator: mv, Auditor: auditor, Provider: mpc, TimeNow: timeNow}
		hh := httpapi.Compose(h, httpapi.WithoutLogging())
		action(h, hh, mv, auditor, resources, assets)
	})
}

func testCommonEndpointBehavior(t test.T, hh http.Handler, validator *mock.Validator[*mock.Enforcer], pathPattern string) {
	path := func(projectID, resourceID string) string {
		return fmt.Sprintf(pathPattern, projectID, resourceID)
	}

	// endpoint requires a token with project access
	validator.Enforcer.Forbid("project:access")
	assert.HTTPRequest{
		Method:       "GET",
		Path:         path("project1", "foo"),
		ExpectStatus: http.StatusForbidden,
	}.Check(t.T, hh)
	validator.Enforcer.Allow("project:access")

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
	validator.Enforcer.Forbid("project:show:foo")
	assert.HTTPRequest{
		Method:       "GET",
		Path:         path("project1", "foo"),
		ExpectStatus: http.StatusForbidden,
	}.Check(t.T, hh)
	validator.Enforcer.Allow("project:show:foo")
}
