// SPDX-FileCopyrightText: 2019 SAP SE or an SAP affiliate company
// SPDX-License-Identifier: Apache-2.0

package api_test

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/sapcc/go-api-declarations/castellum"
	"github.com/sapcc/go-bits/easypg"
	"github.com/sapcc/go-bits/must"
	. "go.xyrillian.de/gg/option"

	"github.com/sapcc/castellum/internal/db"
	"github.com/sapcc/castellum/internal/plugins"
	"github.com/sapcc/castellum/internal/test"
)

func TestMain(m *testing.M) {
	easypg.WithTestDB(m, func() int { return m.Run() })
}

func commonSetupOptionsForAPITest() test.SetupOption {
	return test.WithAssetManagers(
		&plugins.AssetManagerStatic{AssetType: "foo"},
		&plugins.AssetManagerStatic{AssetType: "bar", UsageMetrics: []castellum.UsageMetric{"first", "second"}, ExpectsConfiguration: true},
		&plugins.AssetManagerStatic{AssetType: "qux", ConflictsWithAssetType: "foo"},
	)
}

// Called at the start of all tests to fill the initially empty test database with some records.
func commonSetupFillDB(t *testing.T, s test.Setup) {
	ctx := t.Context()
	singular := func(value float64) castellum.UsageValues {
		return castellum.UsageValues{castellum.SingularUsageMetric: value}
	}
	multi := func(first, second float64) castellum.UsageValues {
		return castellum.UsageValues{"first": first, "second": second}
	}
	unix := func(timestamp int64) time.Time {
		return time.Unix(timestamp, 0).UTC()
	}

	resources := []*db.Resource{
		// insert some resources in 'project1' that we can actually list -- both have a different set of thresholds activated to exercise different code paths
		{
			ScopeUUID:                "project1",
			DomainUUID:               "domain1",
			AssetType:                "foo",
			LowThresholdPercent:      singular(20),
			LowDelaySeconds:          3600,
			HighThresholdPercent:     singular(80),
			HighDelaySeconds:         1800,
			CriticalThresholdPercent: singular(0),
			SizeStepPercent:          20,
			NextScrapeAt:             unix(1801),
		},
		{
			ScopeUUID:                "project1",
			DomainUUID:               "domain1",
			AssetType:                "bar",
			ConfigJSON:               `{"foo":"bar"}`,
			LowThresholdPercent:      multi(0, 0),
			LowDelaySeconds:          0,
			HighThresholdPercent:     multi(0, 0),
			HighDelaySeconds:         0,
			CriticalThresholdPercent: multi(95, 97),
			SizeStepPercent:          10,
			MaximumSize:              Some[uint64](20000),
			ScrapeErrorMessage:       "datacenter is on fire",
			NextScrapeAt:             unix(1802),
		},
		// insert some resources that we should not be able to list
		{
			ScopeUUID:                "something-else", // wrong project ID
			DomainUUID:               "domain1",
			AssetType:                "foo",
			LowThresholdPercent:      singular(20),
			LowDelaySeconds:          3600,
			HighThresholdPercent:     singular(80),
			HighDelaySeconds:         1800,
			CriticalThresholdPercent: singular(95),
			SizeStepPercent:          20,
			ScrapeErrorMessage:       "datacenter is on fire",
			NextScrapeAt:             unix(1803),
		},
		{
			ScopeUUID:                "project1",
			DomainUUID:               "domain1",
			AssetType:                "unknown", // unknown asset type
			LowThresholdPercent:      singular(20),
			LowDelaySeconds:          3600,
			HighThresholdPercent:     singular(80),
			HighDelaySeconds:         1800,
			CriticalThresholdPercent: singular(95),
			SizeStepPercent:          20,
			NextScrapeAt:             unix(1804),
		},
	}
	must.SucceedT(t, db.ResourceStore.Insert(ctx, s.DB, resources...))

	assets := []*db.Asset{
		// insert some assets in "project1" that we can list
		{
			ResourceID:   resources[0].ID,
			UUID:         "fooasset1",
			Size:         1024,
			Usage:        singular(512),
			ExpectedSize: Some[uint64](1200),
			NextScrapeAt: unix(311),
		},
		{
			ResourceID:         resources[0].ID,
			UUID:               "fooasset2",
			Size:               512,
			Usage:              singular(409.6),
			StrictMinimumSize:  Some[uint64](256),
			StrictMaximumSize:  Some[uint64](1024),
			ScrapeErrorMessage: "unexpected uptime",
			NextScrapeAt:       unix(312),
		},
		{
			ResourceID:   resources[1].ID,
			UUID:         "barasset1",
			Size:         2000,
			Usage:        multi(200, 222),
			NextScrapeAt: unix(313),
		},
		// insert a bogus asset in an unknown asset type; we should not be able to list this in the API
		{
			ResourceID:   resources[3].ID,
			UUID:         "bogusasset",
			Size:         100,
			Usage:        singular(50),
			NextScrapeAt: unix(314),
		},
	}
	must.SucceedT(t, db.AssetStore.Insert(ctx, s.DB, assets...))

	finishedOps := []*db.FinishedOperation{
		// insert a dummy operation that should not be listed
		{
			AssetID:            assets[0].ID,
			Reason:             castellum.OperationReasonCritical,
			Outcome:            castellum.OperationOutcomeErrorResolved,
			OldSize:            0,
			NewSize:            0,
			CreatedAt:          unix(21),
			ConfirmedAt:        Some(unix(22)),
			GreenlitAt:         Some(unix(22)),
			GreenlitByUserUUID: Some("user3"),
			FinishedAt:         unix(23),
			Usage:              singular(0),
		},
		// insert some operations that we can list
		{
			AssetID:    assets[0].ID,
			Reason:     castellum.OperationReasonLow,
			Outcome:    castellum.OperationOutcomeCancelled,
			OldSize:    1000,
			NewSize:    900,
			CreatedAt:  unix(31),
			FinishedAt: unix(32),
			Usage:      singular(200),
		},
		{
			AssetID:            assets[0].ID,
			Reason:             castellum.OperationReasonHigh,
			Outcome:            castellum.OperationOutcomeSucceeded,
			OldSize:            1023,
			NewSize:            1024,
			CreatedAt:          unix(41),
			ConfirmedAt:        Some(unix(42)),
			GreenlitAt:         Some(unix(43)),
			GreenlitByUserUUID: Some("user2"),
			FinishedAt:         unix(44),
			Usage:              singular(818.4),
		},
		{
			AssetID:      assets[0].ID,
			Reason:       castellum.OperationReasonCritical,
			Outcome:      castellum.OperationOutcomeErrored,
			OldSize:      1024,
			NewSize:      1025,
			CreatedAt:    unix(51),
			ConfirmedAt:  Some(unix(52)),
			GreenlitAt:   Some(unix(52)),
			FinishedAt:   unix(53),
			ErrorMessage: "datacenter is on fire",
			Usage:        singular(983.04),
		},
	}
	must.SucceedT(t, db.FinishedOperationStore.Insert(ctx, s.DB, finishedOps...))
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
