// SPDX-FileCopyrightText: 2019 SAP SE or an SAP affiliate company
// SPDX-License-Identifier: Apache-2.0

package api_test

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/sapcc/go-api-declarations/castellum"
	"github.com/sapcc/go-bits/assert"
	"github.com/sapcc/go-bits/easypg"

	"github.com/sapcc/castellum/internal/plugins"
	"github.com/sapcc/castellum/internal/test"
)

func TestMain(m *testing.M) {
	easypg.WithTestDB(m, func() int { return m.Run() })
}

func commonSetupOptionsForAPITest() test.SetupOption {
	return test.WithSeveral(
		test.WithDBFixtureFile("fixtures/start-data.sql"),
		test.WithAssetManagers(
			&plugins.AssetManagerStatic{AssetType: "foo"},
			&plugins.AssetManagerStatic{AssetType: "bar", UsageMetrics: []castellum.UsageMetric{"first", "second"}, ExpectsConfiguration: true},
			&plugins.AssetManagerStatic{AssetType: "qux", ConflictsWithAssetType: "foo"},
		),
	)
}

func withHandler(action func()) {
	// TODO: remove this in the next commit (not done yet to avoid huge whitespace changes in the current commit)
	action()
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
