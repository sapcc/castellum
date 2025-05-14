// SPDX-FileCopyrightText: 2019 SAP SE
// SPDX-License-Identifier: Apache-2.0

package tasks

import (
	"context"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/sapcc/go-api-declarations/castellum"
	"github.com/sapcc/go-bits/mock"

	"github.com/sapcc/castellum/internal/core"
	"github.com/sapcc/castellum/internal/db"
	"github.com/sapcc/castellum/internal/plugins"
	"github.com/sapcc/castellum/internal/test"
)

func TestCollectGarbage(baseT *testing.T) {
	t := test.T{T: baseT}
	withContext(t, core.Config{}, func(_ context.Context, c *Context, _ *plugins.AssetManagerStatic, _ *mock.Clock, _ *prometheus.Registry) {
		fakeNow := time.Unix(0, 0).UTC()

		// setup some minimal scaffolding (we can only insert finished_operations
		// with valid asset IDs into the DB)
		t.Must(c.DB.Insert(&db.Resource{
			ScopeUUID: "project1",
			AssetType: "foo",
		}))
		t.Must(c.DB.Insert(&db.Asset{
			ResourceID: 1,
			UUID:       "asset1",
		}))
		t.Must(c.DB.Insert(&db.Asset{
			ResourceID: 1,
			UUID:       "asset2",
		}))

		ops := []db.FinishedOperation{
			{
				AssetID:    1,
				Reason:     castellum.OperationReasonHigh,
				Outcome:    castellum.OperationOutcomeCancelled,
				OldSize:    1000,
				NewSize:    1200,
				Usage:      castellum.UsageValues{castellum.SingularUsageMetric: 800},
				CreatedAt:  fakeNow.Add(-40 * time.Minute),
				FinishedAt: fakeNow.Add(-30 * time.Minute),
			},
			{
				AssetID:    2,
				Reason:     castellum.OperationReasonHigh,
				Outcome:    castellum.OperationOutcomeCancelled,
				OldSize:    1000,
				NewSize:    1200,
				Usage:      castellum.UsageValues{castellum.SingularUsageMetric: 800},
				CreatedAt:  fakeNow.Add(-25 * time.Minute),
				FinishedAt: fakeNow.Add(-20 * time.Minute),
			},
			{
				AssetID:     2,
				Reason:      castellum.OperationReasonCritical,
				Outcome:     castellum.OperationOutcomeSucceeded,
				OldSize:     1000,
				NewSize:     1200,
				Usage:       castellum.UsageValues{castellum.SingularUsageMetric: 800},
				CreatedAt:   fakeNow.Add(-20 * time.Minute),
				ConfirmedAt: p2time(fakeNow.Add(-20 * time.Minute)),
				GreenlitAt:  p2time(fakeNow.Add(-20 * time.Minute)),
				FinishedAt:  fakeNow.Add(-10 * time.Minute),
			},
		}
		for _, op := range ops {
			t.Must(c.DB.Insert(&op))
		}

		t.ExpectFinishedOperations(c.DB, ops...)
		t.Must(CollectGarbage(c.DB, fakeNow.Add(-15*time.Minute)))
		t.ExpectFinishedOperations(c.DB, ops[2])
	})
}
