// SPDX-FileCopyrightText: 2019 SAP SE or an SAP affiliate company
// SPDX-License-Identifier: Apache-2.0

package tasks_test

import (
	"database/sql"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/sapcc/go-api-declarations/castellum"
	"github.com/sapcc/go-bits/easypg"
	"github.com/sapcc/go-bits/jobloop"
	"github.com/sapcc/go-bits/must"

	"github.com/sapcc/castellum/internal/db"
	"github.com/sapcc/castellum/internal/plugins"
	"github.com/sapcc/castellum/internal/tasks"
	"github.com/sapcc/castellum/internal/test"
)

func setupAssetResizeTest(t *testing.T, s test.Setup, assetCount int) jobloop.Job {
	amStatic := s.ManagerForAssetType("foo")

	// create a resource and assets to test with
	must.SucceedT(t, s.DB.Insert(&db.Resource{
		ScopeUUID: "project1",
		AssetType: "foo",
	}))
	amStatic.Assets = map[string]map[string]plugins.StaticAsset{
		"project1": {},
	}

	for idx := 1; idx <= assetCount; idx++ {
		uuid := fmt.Sprintf("asset%d", idx)
		must.SucceedT(t, s.DB.Insert(&db.Asset{
			ResourceID:   1,
			UUID:         uuid,
			Size:         1000,
			Usage:        castellum.UsageValues{castellum.SingularUsageMetric: 500},
			ExpectedSize: nil,
		}))

		amStatic.Assets["project1"][uuid] = plugins.StaticAsset{
			Size:  1000,
			Usage: 500,
		}
	}

	return s.TaskContext.AssetResizingJob(s.Registry)
}

func TestSuccessfulResize(t *testing.T) {
	ctx := t.Context()
	s := test.NewSetup(t,
		commonSetupOptionsForWorkerTest(),
	)
	resizeJob := setupAssetResizeTest(t, s, 1)

	// add a greenlit PendingOperation
	s.Clock.StepBy(5 * time.Minute)
	pendingOp := db.PendingOperation{
		AssetID:     1,
		Reason:      castellum.OperationReasonHigh,
		OldSize:     1000,
		NewSize:     1200,
		Usage:       castellum.UsageValues{castellum.SingularUsageMetric: 500},
		CreatedAt:   s.Clock.Now().Add(-5 * time.Minute),
		ConfirmedAt: p2time(s.Clock.Now()),
		GreenlitAt:  p2time(s.Clock.Now().Add(5 * time.Minute)),
	}
	must.SucceedT(t, s.DB.Insert(&pendingOp))

	tr, tr0 := easypg.NewTracker(t, s.DB.Db)
	tr0.Ignore()

	// ExecuteOne(AssetResizeJob{}) should do nothing right now because that operation is
	// only greenlit in the future, but not right now
	err := resizeJob.ProcessOne(ctx)
	if !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("expected sql.ErrNoRows, got %s instead", err.Error())
	}
	tr.DBChanges().AssertEmpty()

	// go into the future and check that the operation gets executed;
	// also the asset should now report an expected size, but still show the old size
	// (until the next asset scrape)
	s.Clock.StepBy(10 * time.Minute)
	must.SucceedT(t, resizeJob.ProcessOne(ctx))
	tr.DBChanges().AssertEqualf(`
			UPDATE assets SET expected_size = 1200, resized_at = %[4]d WHERE id = 1 AND resource_id = 1 AND uuid = 'asset1';
			INSERT INTO finished_operations (asset_id, reason, outcome, old_size, new_size, created_at, confirmed_at, greenlit_at, finished_at, usage) VALUES (1, 'high', 'succeeded', 1000, 1200, %[1]d, %[2]d, %[3]d, %[4]d, '{"singular":500}');
			DELETE FROM pending_operations WHERE id = 1 AND asset_id = 1;
		`,
		s.Clock.Now().Add(-15*time.Minute).Unix(),
		s.Clock.Now().Add(-10*time.Minute).Unix(),
		s.Clock.Now().Add(-5*time.Minute).Unix(),
		s.Clock.Now().Unix(),
	)
}

func TestFailingResize(t *testing.T) {
	ctx := t.Context()
	s := test.NewSetup(t,
		commonSetupOptionsForWorkerTest(),
	)
	resizeJob := setupAssetResizeTest(t, s, 1)

	// add a greenlit PendingOperation
	s.Clock.StepBy(10 * time.Minute)
	pendingOp := db.PendingOperation{
		AssetID:     1,
		Reason:      castellum.OperationReasonLow,
		OldSize:     1000,
		NewSize:     600,
		Usage:       castellum.UsageValues{castellum.SingularUsageMetric: 500},
		CreatedAt:   s.Clock.Now().Add(-10 * time.Minute),
		ConfirmedAt: p2time(s.Clock.Now().Add(-5 * time.Minute)),
		GreenlitAt:  p2time(s.Clock.Now().Add(-5 * time.Minute)),
	}
	must.SucceedT(t, s.DB.Insert(&pendingOp))

	tr, tr0 := easypg.NewTracker(t, s.DB.Db)
	tr0.Ignore()

	amStatic := s.ManagerForAssetType("foo")
	amStatic.SetAssetSizeFails = true
	must.SucceedT(t, resizeJob.ProcessOne(ctx))

	// check that resizing fails as expected,
	// and thus the asset does not have an ExpectedSize
	tr.DBChanges().AssertEqualf(`
			INSERT INTO finished_operations (asset_id, reason, outcome, old_size, new_size, created_at, confirmed_at, greenlit_at, finished_at, error_message, usage) VALUES (1, 'low', 'failed', 1000, 600, %[1]d, %[2]d, %[2]d, %[3]d, '%[4]s', '{"singular":500}');
			DELETE FROM pending_operations WHERE id = 1 AND asset_id = 1;
		`,
		s.Clock.Now().Add(-10*time.Minute).Unix(),
		s.Clock.Now().Add(-5*time.Minute).Unix(),
		s.Clock.Now().Unix(),
		"SetAssetSize failing as requested",
	)
}

func TestErroringResize(t *testing.T) {
	ctx := t.Context()
	s := test.NewSetup(t,
		commonSetupOptionsForWorkerTest(),
	)
	resizeJob := setupAssetResizeTest(t, s, 1)

	// add a greenlit PendingOperation that will error in SetAssetSize()
	s.Clock.StepBy(10 * time.Minute)
	pendingOp := db.PendingOperation{
		AssetID:     1,
		Reason:      castellum.OperationReasonLow,
		OldSize:     1000,
		NewSize:     400, // will error because `new_size < usage` (usage = 500, see above)
		Usage:       castellum.UsageValues{castellum.SingularUsageMetric: 500},
		CreatedAt:   s.Clock.Now().Add(-10 * time.Minute),
		ConfirmedAt: p2time(s.Clock.Now().Add(-5 * time.Minute)),
		GreenlitAt:  p2time(s.Clock.Now().Add(-5 * time.Minute)),
	}
	must.SucceedT(t, s.DB.Insert(&pendingOp))

	tr, tr0 := easypg.NewTracker(t, s.DB.Db)
	tr0.Ignore()

	// when the outcome of the resize is "errored", we can retry several times
	for attempt := range tasks.MaxRetries {
		s.Clock.StepBy(10 * time.Minute)
		must.SucceedT(t, resizeJob.ProcessOne(ctx))

		tr.DBChanges().AssertEqualf(`
				DELETE FROM pending_operations WHERE id = %[1]d AND asset_id = 1;
				INSERT INTO pending_operations (id, asset_id, reason, old_size, new_size, created_at, confirmed_at, greenlit_at, errored_attempts, retry_at, usage) VALUES (%[2]d, 1, 'low', 1000, 400, %[3]d, %[4]d, %[5]d, %[1]d, %[6]d, '{"singular":500}');
			`,
			attempt+1, // ID of pending operation deleted in this attempt
			attempt+2, // ID of pending operation created after this attempt
			pendingOp.CreatedAt.Unix(),
			pendingOp.ConfirmedAt.Unix(),
			pendingOp.GreenlitAt.Unix(),
			s.Clock.Now().Add(tasks.RetryInterval).Unix(),
		)
	}

	// ExecuteOne(AssetResizeJob{}) should do nothing right now because, although the
	// operation is greenlit, its retry_at timestamp is in the future
	err := resizeJob.ProcessOne(ctx)
	if !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("expected sql.ErrNoRows, got %s instead", err.Error())
	}
	tr.DBChanges().AssertEmpty()

	// check that resizing errors as expected once the retry budget is exceeded,
	// and thus the asset does not have an ExpectedSize
	s.Clock.StepBy(10 * time.Minute)
	must.SucceedT(t, resizeJob.ProcessOne(ctx))
	tr.DBChanges().AssertEqualf(`
			INSERT INTO finished_operations (asset_id, reason, outcome, old_size, new_size, created_at, confirmed_at, greenlit_at, finished_at, error_message, errored_attempts, usage) VALUES (1, 'low', 'errored', 1000, 400, %[1]d, %[2]d, %[3]d, %[4]d, '%[5]s', 3, '{"singular":500}');
			DELETE FROM pending_operations WHERE id = 4 AND asset_id = 1;
		`,
		pendingOp.CreatedAt.Unix(),
		pendingOp.ConfirmedAt.Unix(),
		pendingOp.GreenlitAt.Unix(),
		s.Clock.Now().Unix(),
		"cannot set size smaller than current usage",
	)
}
