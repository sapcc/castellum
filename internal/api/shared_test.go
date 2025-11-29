// SPDX-FileCopyrightText: 2019 SAP SE or an SAP affiliate company
// SPDX-License-Identifier: Apache-2.0

package api_test

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/sapcc/go-api-declarations/castellum"
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

func testCommonEndpointBehavior(t *testing.T, s test.Setup, pathPattern string) {
	ctx := t.Context()
	getPath := func(projectID, resourceID string) string {
		return "GET " + fmt.Sprintf(pathPattern, projectID, resourceID)
	}

	// endpoint requires a token with project access
	s.Validator.Enforcer.Forbid("project:access")
	s.Handler.RespondTo(ctx, getPath("project1", "foo")).
		ExpectStatus(t, http.StatusForbidden)
	s.Validator.Enforcer.Allow("project:access")

	// expect error for unknown project or resource
	s.Handler.RespondTo(ctx, getPath("project2", "foo")).
		ExpectStatus(t, http.StatusNotFound)
	s.Handler.RespondTo(ctx, getPath("project1", "doesnotexist")).
		ExpectStatus(t, http.StatusNotFound)

	// the "unknown" resource exists, but it should be 404 regardless because we
	// don't have an asset manager for it
	s.Handler.RespondTo(ctx, getPath("project1", "unknown")).
		ExpectStatus(t, http.StatusNotFound)

	// expect error for inaccessible resource
	s.Validator.Enforcer.Forbid("project:show:foo")
	s.Handler.RespondTo(ctx, getPath("project1", "foo")).
		ExpectStatus(t, http.StatusForbidden)
	s.Validator.Enforcer.Allow("project:show:foo")
}
