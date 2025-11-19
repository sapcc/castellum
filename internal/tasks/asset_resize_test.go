// SPDX-FileCopyrightText: 2019 SAP SE or an SAP affiliate company
// SPDX-License-Identifier: Apache-2.0

package tasks_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/sapcc/go-api-declarations/castellum"
	"github.com/sapcc/go-bits/jobloop"

	"github.com/sapcc/castellum/internal/db"
	"github.com/sapcc/castellum/internal/plugins"
	"github.com/sapcc/castellum/internal/tasks"
	"github.com/sapcc/castellum/internal/test"
)

func setupAssetResizeTest(t test.T, c *tasks.Context, amStatic *plugins.AssetManagerStatic, registry *prometheus.Registry, assetCount int) jobloop.Job {
	// create a resource and assets to test with
	t.Must(c.DB.Insert(&db.Resource{
		ScopeUUID: "project1",
		AssetType: "foo",
	}))
	amStatic.Assets = map[string]map[string]plugins.StaticAsset{
		"project1": {},
	}

	for idx := 1; idx <= assetCount; idx++ {
		uuid := fmt.Sprintf("asset%d", idx)
		t.Must(c.DB.Insert(&db.Asset{
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

	return c.AssetResizingJob(registry)
}

func TestSuccessfulResize(baseT *testing.T) {
	t := test.T{T: baseT}
	s := test.NewSetup(t.T)
	withContext(s, func(ctx context.Context, c *tasks.Context, amStatic *plugins.AssetManagerStatic, registry *prometheus.Registry) {
		resizeJob := setupAssetResizeTest(t, c, amStatic, registry, 1)

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
		t.Must(c.DB.Insert(&pendingOp))

		// ExecuteOne(AssetResizeJob{}) should do nothing right now because that operation is
		// only greenlit in the future, but not right now
		err := resizeJob.ProcessOne(ctx)
		if !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("expected sql.ErrNoRows, got %s instead", err.Error())
		}
		t.ExpectPendingOperations(c.DB, pendingOp)
		t.ExpectFinishedOperations(c.DB /*, nothing */)

		// go into the future and check that the operation gets executed
		s.Clock.StepBy(10 * time.Minute)
		err = resizeJob.ProcessOne(ctx)
		t.Must(err)
		t.ExpectPendingOperations(c.DB /*, nothing */)
		t.ExpectFinishedOperations(c.DB, db.FinishedOperation{
			AssetID:     1,
			Reason:      castellum.OperationReasonHigh,
			OldSize:     1000,
			NewSize:     1200,
			Usage:       castellum.UsageValues{castellum.SingularUsageMetric: 500},
			CreatedAt:   s.Clock.Now().Add(-15 * time.Minute),
			ConfirmedAt: p2time(s.Clock.Now().Add(-10 * time.Minute)),
			GreenlitAt:  p2time(s.Clock.Now().Add(-5 * time.Minute)),
			FinishedAt:  s.Clock.Now(),
			Outcome:     castellum.OperationOutcomeSucceeded,
		})

		// expect asset to report an expected size, but still show the old size
		// (until the next asset scrape)
		t.ExpectAssets(c.DB, db.Asset{
			ID:           1,
			ResourceID:   1,
			UUID:         "asset1",
			Size:         1000,
			Usage:        castellum.UsageValues{castellum.SingularUsageMetric: 500},
			ExpectedSize: p2uint64(1200),
			ResizedAt:    p2time(s.Clock.Now()),
		})
	})
}

func TestFailingResize(tBase *testing.T) {
	t := test.T{T: tBase}
	s := test.NewSetup(t.T)
	withContext(s, func(ctx context.Context, c *tasks.Context, amStatic *plugins.AssetManagerStatic, registry *prometheus.Registry) {
		resizeJob := setupAssetResizeTest(t, c, amStatic, registry, 1)

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
		t.Must(c.DB.Insert(&pendingOp))

		amStatic.SetAssetSizeFails = true
		t.Must(resizeJob.ProcessOne(ctx))

		// check that resizing fails as expected
		t.ExpectPendingOperations(c.DB /*, nothing */)
		t.ExpectFinishedOperations(c.DB, db.FinishedOperation{
			AssetID:      1,
			Reason:       castellum.OperationReasonLow,
			OldSize:      1000,
			NewSize:      600,
			Usage:        castellum.UsageValues{castellum.SingularUsageMetric: 500},
			CreatedAt:    s.Clock.Now().Add(-10 * time.Minute),
			ConfirmedAt:  p2time(s.Clock.Now().Add(-5 * time.Minute)),
			GreenlitAt:   p2time(s.Clock.Now().Add(-5 * time.Minute)),
			FinishedAt:   s.Clock.Now(),
			Outcome:      castellum.OperationOutcomeFailed,
			ErrorMessage: "SetAssetSize failing as requested",
		})

		// check that asset does not have an ExpectedSize
		t.ExpectAssets(c.DB, db.Asset{
			ID:           1,
			ResourceID:   1,
			UUID:         "asset1",
			Size:         1000,
			Usage:        castellum.UsageValues{castellum.SingularUsageMetric: 500},
			ExpectedSize: nil,
		})
	})
}

func TestErroringResize(tBase *testing.T) {
	t := test.T{T: tBase}
	s := test.NewSetup(t.T)
	withContext(s, func(ctx context.Context, c *tasks.Context, amStatic *plugins.AssetManagerStatic, registry *prometheus.Registry) {
		resizeJob := setupAssetResizeTest(t, c, amStatic, registry, 1)

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
		t.Must(c.DB.Insert(&pendingOp))

		// when the outcome of the resize is "errored", we can retry several times
		for range tasks.MaxRetries {
			s.Clock.StepBy(10 * time.Minute)
			t.Must(resizeJob.ProcessOne(ctx))

			pendingOp.ID++
			pendingOp.ErroredAttempts++
			pendingOp.RetryAt = p2time(s.Clock.Now().Add(tasks.RetryInterval))
			t.ExpectPendingOperations(c.DB, pendingOp)
			t.ExpectFinishedOperations(c.DB /*, nothing */)
		}

		// ExecuteOne(AssetResizeJob{}) should do nothing right now because, although the
		// operation is greenlit, its retry_at timestamp is in the future
		err := resizeJob.ProcessOne(ctx)
		if !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("expected sql.ErrNoRows, got %s instead", err.Error())
		}
		t.ExpectPendingOperations(c.DB, pendingOp)
		t.ExpectFinishedOperations(c.DB /*, nothing */)

		// check that resizing errors as expected once the retry budget is exceeded
		s.Clock.StepBy(10 * time.Minute)
		t.Must(resizeJob.ProcessOne(ctx))
		t.ExpectPendingOperations(c.DB /*, nothing */)
		t.ExpectFinishedOperations(c.DB, db.FinishedOperation{
			AssetID:         1,
			Reason:          castellum.OperationReasonLow,
			OldSize:         1000,
			NewSize:         400,
			Usage:           castellum.UsageValues{castellum.SingularUsageMetric: 500},
			CreatedAt:       pendingOp.CreatedAt,
			ConfirmedAt:     pendingOp.ConfirmedAt,
			GreenlitAt:      pendingOp.GreenlitAt,
			FinishedAt:      s.Clock.Now(),
			Outcome:         castellum.OperationOutcomeErrored,
			ErrorMessage:    "cannot set size smaller than current usage",
			ErroredAttempts: tasks.MaxRetries,
		})

		// check that asset does not have an ExpectedSize
		t.ExpectAssets(c.DB, db.Asset{
			ID:           1,
			ResourceID:   1,
			UUID:         "asset1",
			Size:         1000,
			Usage:        castellum.UsageValues{castellum.SingularUsageMetric: 500},
			ExpectedSize: nil,
		})
	})
}
