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
	"fmt"
	"net/http"
	"time"

	"github.com/go-gorp/gorp/v3"
	"github.com/sapcc/go-api-declarations/castellum"
	"github.com/sapcc/go-bits/assert"
	"github.com/sapcc/go-bits/httpapi"
	"github.com/sapcc/go-bits/mock"

	"github.com/sapcc/castellum/internal/core"
	"github.com/sapcc/castellum/internal/db"
	"github.com/sapcc/castellum/internal/plugins"
	"github.com/sapcc/castellum/internal/test"
)

func withHandler(t test.T, cfg core.Config, timeNow func() time.Time, action func(*handler, http.Handler, *mock.Validator[*mock.Enforcer], []db.Resource, []db.Asset)) {
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

		var resources []db.Resource
		_, err := dbi.Select(&resources, `SELECT * FROM resources ORDER BY ID`)
		t.Must(err)

		var assets []db.Asset
		_, err = dbi.Select(&assets, `SELECT * FROM assets ORDER BY ID`)
		t.Must(err)

		if timeNow == nil {
			timeNow = time.Now
		}
		h := &handler{Config: cfg, DB: dbi, Team: team, Validator: mv, Provider: mpc, TimeNow: timeNow}
		hh := httpapi.Compose(h, httpapi.WithoutLogging())
		action(h, hh, mv, resources, assets)
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
