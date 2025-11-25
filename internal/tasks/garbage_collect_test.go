// SPDX-FileCopyrightText: 2019 SAP SE or an SAP affiliate company
// SPDX-License-Identifier: Apache-2.0

package tasks_test

import (
	"testing"
	"time"

	"github.com/sapcc/go-api-declarations/castellum"
	"github.com/sapcc/go-bits/easypg"

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

	tr, tr0 := easypg.NewTracker(t, s.DB.Db)
	tr0.Ignore()

	t.Must(tasks.CollectGarbage(s.DB, fakeNow.Add(-15*time.Minute)))

	// NOTE: `finished_operations` does not have a primary key, so this diff shows the full deleted records insteadj
	tr.DBChanges().AssertEqualf(`
			DELETE FROM finished_operations WHERE asset_id = 1 AND reason = 'high' AND outcome = 'cancelled' AND old_size = 1000 AND new_size = 1200 AND created_at = %[1]d AND confirmed_at = NULL AND greenlit_at = NULL AND finished_at = %[2]d AND greenlit_by_user_uuid = NULL AND error_message = '' AND errored_attempts = 0 AND usage = '{"singular":800}';
			DELETE FROM finished_operations WHERE asset_id = 2 AND reason = 'high' AND outcome = 'cancelled' AND old_size = 1000 AND new_size = 1200 AND created_at = %[3]d AND confirmed_at = NULL AND greenlit_at = NULL AND finished_at = %[4]d AND greenlit_by_user_uuid = NULL AND error_message = '' AND errored_attempts = 0 AND usage = '{"singular":800}';
		`,
		ops[0].CreatedAt.Unix(),
		ops[0].FinishedAt.Unix(),
		ops[1].CreatedAt.Unix(),
		ops[1].FinishedAt.Unix(),
	)
}
