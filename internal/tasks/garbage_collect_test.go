// SPDX-FileCopyrightText: 2019 SAP SE or an SAP affiliate company
// SPDX-License-Identifier: Apache-2.0

package tasks_test

import (
	"testing"
	"time"

	"github.com/sapcc/go-api-declarations/castellum"

	"github.com/sapcc/castellum/internal/db"
	"github.com/sapcc/castellum/internal/tasks"
	"github.com/sapcc/castellum/internal/test"
)

func TestCollectGarbage(baseT *testing.T) {
	t := test.T{T: baseT}
	s := test.NewSetup(t.T,
		commonSetupOptionsForWorkerTest(),
	)
	fakeNow := time.Unix(0, 0).UTC()

	// setup some minimal scaffolding (we can only insert finished_operations
	// with valid asset IDs into the DB)
	t.Must(s.DB.Insert(&db.Resource{
		ScopeUUID: "project1",
		AssetType: "foo",
	}))
	t.Must(s.DB.Insert(&db.Asset{
		ResourceID: 1,
		UUID:       "asset1",
	}))
	t.Must(s.DB.Insert(&db.Asset{
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
		t.Must(s.DB.Insert(&op))
	}

	t.ExpectFinishedOperations(s.DB, ops...)
	t.Must(tasks.CollectGarbage(s.DB, fakeNow.Add(-15*time.Minute)))
	t.ExpectFinishedOperations(s.DB, ops[2])
}
