// SPDX-FileCopyrightText: 2019 SAP SE or an SAP affiliate company
// SPDX-License-Identifier: Apache-2.0

package tasks_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/sapcc/go-api-declarations/castellum"
	"github.com/sapcc/go-bits/easypg"
	"github.com/sapcc/go-bits/jobloop"

	"github.com/sapcc/castellum/internal/core"
	"github.com/sapcc/castellum/internal/db"
	"github.com/sapcc/castellum/internal/plugins"
	"github.com/sapcc/castellum/internal/tasks"
	"github.com/sapcc/castellum/internal/test"
)

func runAssetScrapeTest(t test.T, action func(context.Context, test.Setup, *tasks.Context, func(plugins.StaticAsset), jobloop.Job)) {
	withContext(t, core.Config{}, func(ctx context.Context, s test.Setup, c *tasks.Context, amStatic *plugins.AssetManagerStatic, registry *prometheus.Registry) {
		scrapeJob := c.AssetScrapingJob(registry)

		// asset scrape without any resources just does nothing
		err := scrapeJob.ProcessOne(ctx)
		if !errors.Is(err, sql.ErrNoRows) {
			t.Errorf("expected sql.ErrNoRows, got %s instead", err.Error())
		}
		_, dbDump := easypg.NewTracker(t.T, c.DB.Db)
		dbDump.AssertEmpty()

		// create a resource and asset to test with
		t.Must(c.DB.Insert(&db.Resource{
			ScopeUUID:                "project1",
			AssetType:                "foo",
			LowThresholdPercent:      castellum.UsageValues{castellum.SingularUsageMetric: 20},
			LowDelaySeconds:          3600,
			HighThresholdPercent:     castellum.UsageValues{castellum.SingularUsageMetric: 80},
			HighDelaySeconds:         3600,
			CriticalThresholdPercent: castellum.UsageValues{castellum.SingularUsageMetric: 95},
			SizeStepPercent:          20,
			SingleStep:               false,
		}))
		t.Must(c.DB.Insert(&db.Asset{
			ResourceID:   1,
			UUID:         "asset1",
			Size:         1000,
			Usage:        castellum.UsageValues{castellum.SingularUsageMetric: 500},
			NextScrapeAt: s.Clock.Now(),
			NeverScraped: true,
			ExpectedSize: nil,
		}))

		// setup asset with configurable size
		amStatic.Assets = map[string]map[string]plugins.StaticAsset{
			"project1": {
				"asset1": {Size: 1000, Usage: 500},
			},
		}
		setAsset := func(a plugins.StaticAsset) {
			amStatic.Assets["project1"]["asset1"] = a
		}

		action(ctx, s, c, setAsset, scrapeJob)
	})
}

func TestNoOperationWhenNoThreshold(baseT *testing.T) {
	t := test.T{T: baseT}
	runAssetScrapeTest(t, func(ctx context.Context, s test.Setup, c *tasks.Context, setAsset func(plugins.StaticAsset), scrapeJob jobloop.Job) {
		// when no threshold is crossed, no operation gets created
		s.Clock.StepBy(10 * time.Minute)
		t.Must(scrapeJob.ProcessOne(ctx))
		t.ExpectPendingOperations(c.DB /*, nothing */)
		t.ExpectFinishedOperations(c.DB /*, nothing */)
	})
}

func TestNormalUpsizeTowardsGreenlight(baseT *testing.T) {
	t := test.T{T: baseT}
	runAssetScrapeTest(t, func(ctx context.Context, s test.Setup, c *tasks.Context, setAsset func(plugins.StaticAsset), scrapeJob jobloop.Job) {
		// set a maximum size that does not contradict the following operations
		// (down below, there's a separate test for when the maximum size actually
		// inhibits upsizing)
		t.MustExec(c.DB, `UPDATE resources SET max_size = 2000`)

		// when the "High" threshold gets crossed, a "High" operation gets created in
		// state "created"
		s.Clock.StepBy(10 * time.Minute)
		setAsset(plugins.StaticAsset{Size: 1000, Usage: 800})
		t.Must(scrapeJob.ProcessOne(ctx))

		expectedOp := db.PendingOperation{
			ID:        1,
			AssetID:   1,
			Reason:    castellum.OperationReasonHigh,
			OldSize:   1000,
			NewSize:   1200,
			Usage:     castellum.UsageValues{castellum.SingularUsageMetric: 800},
			CreatedAt: s.Clock.Now(),
		}
		t.ExpectPendingOperations(c.DB, expectedOp)
		t.ExpectFinishedOperations(c.DB /*, nothing */)

		// another scrape while the delay is not over should not change the state
		// (but for single-step resizing which takes the current usage into account,
		// the NewSize is updated to put the target size outside of the high
		// threshold again)
		s.Clock.StepBy(40 * time.Minute)
		setAsset(plugins.StaticAsset{Size: 1000, Usage: 820})
		t.Must(scrapeJob.ProcessOne(ctx))
		t.ExpectPendingOperations(c.DB, expectedOp)
		t.ExpectFinishedOperations(c.DB /*, nothing */)

		// when the delay is over, the next scrape moves into state "Confirmed/Greenlit"
		s.Clock.StepBy(40 * time.Minute)
		setAsset(plugins.StaticAsset{Size: 1000, Usage: 840})
		t.Must(scrapeJob.ProcessOne(ctx))
		expectedOp.ConfirmedAt = p2time(s.Clock.Now())
		expectedOp.GreenlitAt = p2time(s.Clock.Now())
		t.ExpectPendingOperations(c.DB, expectedOp)
		t.ExpectFinishedOperations(c.DB /*, nothing */)

		// since the operation is now greenlit and can be picked up by a worker at any
		// moment, we should not touch it anymore even if the reason disappears
		s.Clock.StepBy(40 * time.Minute)
		setAsset(plugins.StaticAsset{Size: 1000, Usage: 780})
		t.Must(scrapeJob.ProcessOne(ctx))
		t.ExpectPendingOperations(c.DB, expectedOp)
		t.ExpectFinishedOperations(c.DB /*, nothing */)
	})
}

func TestNormalUpsizeTowardsCancel(baseT *testing.T) {
	t := test.T{T: baseT}
	runAssetScrapeTest(t, func(ctx context.Context, s test.Setup, c *tasks.Context, setAsset func(plugins.StaticAsset), scrapeJob jobloop.Job) {
		// when the "High" threshold gets crossed, a "High" operation gets created in
		// state "created"
		s.Clock.StepBy(10 * time.Minute)
		setAsset(plugins.StaticAsset{Size: 1000, Usage: 800})
		t.Must(scrapeJob.ProcessOne(ctx))

		t.ExpectPendingOperations(c.DB, db.PendingOperation{
			ID:        1,
			AssetID:   1,
			Reason:    castellum.OperationReasonHigh,
			OldSize:   1000,
			NewSize:   1200,
			Usage:     castellum.UsageValues{castellum.SingularUsageMetric: 800},
			CreatedAt: s.Clock.Now(),
		})
		t.ExpectFinishedOperations(c.DB /*, nothing */)

		// when the reason disappears within the delay, the operation is cancelled
		s.Clock.StepBy(40 * time.Minute)
		setAsset(plugins.StaticAsset{Size: 1000, Usage: 790})
		t.Must(scrapeJob.ProcessOne(ctx))

		t.ExpectPendingOperations(c.DB /*, nothing */)
		t.ExpectFinishedOperations(c.DB, db.FinishedOperation{
			AssetID:    1,
			Reason:     castellum.OperationReasonHigh,
			Outcome:    castellum.OperationOutcomeCancelled,
			OldSize:    1000,
			NewSize:    1200,
			Usage:      castellum.UsageValues{castellum.SingularUsageMetric: 800},
			CreatedAt:  s.Clock.Now().Add(-40 * time.Minute),
			FinishedAt: s.Clock.Now(),
		})
	})
}

func TestNormalDownsizeTowardsGreenlight(baseT *testing.T) {
	t := test.T{T: baseT}
	runAssetScrapeTest(t, func(ctx context.Context, s test.Setup, c *tasks.Context, setAsset func(plugins.StaticAsset), scrapeJob jobloop.Job) {
		// set a minimum size that does not contradict the following operations
		// (down below, there's a separate test for when the minimum size actually
		// inhibits upsizing)
		t.MustExec(c.DB, `UPDATE resources SET min_size = 200`)

		// when the "Low" threshold gets crossed, a "Low" operation gets created in
		// state "created"
		s.Clock.StepBy(10 * time.Minute)
		setAsset(plugins.StaticAsset{Size: 1000, Usage: 200})
		t.Must(scrapeJob.ProcessOne(ctx))

		expectedOp := db.PendingOperation{
			ID:        1,
			AssetID:   1,
			Reason:    castellum.OperationReasonLow,
			OldSize:   1000,
			NewSize:   800,
			Usage:     castellum.UsageValues{castellum.SingularUsageMetric: 200},
			CreatedAt: s.Clock.Now(),
		}
		t.ExpectPendingOperations(c.DB, expectedOp)
		t.ExpectFinishedOperations(c.DB /*, nothing */)

		// another scrape while the delay is not over should not change the state
		// (but for single-step resizing which takes the current usage into account,
		// the NewSize is updated to put the target size above the low threshold
		// again)
		s.Clock.StepBy(40 * time.Minute)
		setAsset(plugins.StaticAsset{Size: 1000, Usage: 180})
		t.Must(scrapeJob.ProcessOne(ctx))
		t.ExpectPendingOperations(c.DB, expectedOp)
		t.ExpectFinishedOperations(c.DB /*, nothing */)

		// when the delay is over, the next scrape moves into state "Confirmed/Greenlit"
		s.Clock.StepBy(40 * time.Minute)
		setAsset(plugins.StaticAsset{Size: 1000, Usage: 160})
		t.Must(scrapeJob.ProcessOne(ctx))
		expectedOp.ConfirmedAt = p2time(s.Clock.Now())
		expectedOp.GreenlitAt = p2time(s.Clock.Now())
		t.ExpectPendingOperations(c.DB, expectedOp)
		t.ExpectFinishedOperations(c.DB /*, nothing */)

		// since the operation is now greenlit and can be picked up by a worker at any
		// moment, we should not touch it anymore even if the reason disappears
		s.Clock.StepBy(40 * time.Minute)
		setAsset(plugins.StaticAsset{Size: 1000, Usage: 220})
		t.Must(scrapeJob.ProcessOne(ctx))
		t.ExpectPendingOperations(c.DB, expectedOp)
		t.ExpectFinishedOperations(c.DB /*, nothing */)
	})
}

func TestNormalDownsizeTowardsCancel(baseT *testing.T) {
	t := test.T{T: baseT}
	runAssetScrapeTest(t, func(ctx context.Context, s test.Setup, c *tasks.Context, setAsset func(plugins.StaticAsset), scrapeJob jobloop.Job) {
		// when the "Low" threshold gets crossed, a "Low" operation gets created in
		// state "created"
		s.Clock.StepBy(10 * time.Minute)
		setAsset(plugins.StaticAsset{Size: 1000, Usage: 200})
		t.Must(scrapeJob.ProcessOne(ctx))

		t.ExpectPendingOperations(c.DB, db.PendingOperation{
			ID:        1,
			AssetID:   1,
			Reason:    castellum.OperationReasonLow,
			OldSize:   1000,
			NewSize:   800,
			Usage:     castellum.UsageValues{castellum.SingularUsageMetric: 200},
			CreatedAt: s.Clock.Now(),
		})
		t.ExpectFinishedOperations(c.DB /*, nothing */)

		// when the reason disappears within the delay, the operation is cancelled
		s.Clock.StepBy(40 * time.Minute)
		setAsset(plugins.StaticAsset{Size: 1000, Usage: 210})
		t.Must(scrapeJob.ProcessOne(ctx))

		t.ExpectPendingOperations(c.DB /*, nothing */)
		t.ExpectFinishedOperations(c.DB, db.FinishedOperation{
			AssetID:    1,
			Reason:     castellum.OperationReasonLow,
			Outcome:    castellum.OperationOutcomeCancelled,
			OldSize:    1000,
			NewSize:    800,
			Usage:      castellum.UsageValues{castellum.SingularUsageMetric: 200},
			CreatedAt:  s.Clock.Now().Add(-40 * time.Minute),
			FinishedAt: s.Clock.Now(),
		})
	})
}

func TestCriticalUpsizeTowardsGreenlight(baseT *testing.T) {
	t := test.T{T: baseT}
	runAssetScrapeTest(t, func(ctx context.Context, s test.Setup, c *tasks.Context, setAsset func(plugins.StaticAsset), scrapeJob jobloop.Job) {
		// when the "Critical" threshold gets crossed, a "Critical" operation gets
		// created and immediately confirmed/greenlit
		s.Clock.StepBy(10 * time.Minute)
		setAsset(plugins.StaticAsset{Size: 1000, Usage: 950})
		t.Must(scrapeJob.ProcessOne(ctx))

		expectedOp := db.PendingOperation{
			ID:          1,
			AssetID:     1,
			Reason:      castellum.OperationReasonCritical,
			OldSize:     1000,
			NewSize:     1200,
			Usage:       castellum.UsageValues{castellum.SingularUsageMetric: 950},
			CreatedAt:   s.Clock.Now(),
			ConfirmedAt: p2time(s.Clock.Now()),
			GreenlitAt:  p2time(s.Clock.Now()),
		}
		t.ExpectPendingOperations(c.DB, expectedOp)
		t.ExpectFinishedOperations(c.DB /*, nothing */)
	})
}

func TestReplaceNormalWithCriticalUpsize(baseT *testing.T) {
	t := test.T{T: baseT}
	runAssetScrapeTest(t, func(ctx context.Context, s test.Setup, c *tasks.Context, setAsset func(plugins.StaticAsset), scrapeJob jobloop.Job) {
		// when the "High" threshold gets crossed, a "High" operation gets created in
		// state "created"
		s.Clock.StepBy(10 * time.Minute)
		setAsset(plugins.StaticAsset{Size: 1000, Usage: 900})
		t.Must(scrapeJob.ProcessOne(ctx))

		t.ExpectPendingOperations(c.DB, db.PendingOperation{
			ID:        1,
			AssetID:   1,
			Reason:    castellum.OperationReasonHigh,
			OldSize:   1000,
			NewSize:   1200,
			Usage:     castellum.UsageValues{castellum.SingularUsageMetric: 900},
			CreatedAt: s.Clock.Now(),
		})
		t.ExpectFinishedOperations(c.DB /*, nothing */)

		// when the "Critical" threshold gets crossed while the "High" operation
		// is not yet confirmed, the "High" operation is cancelled and a "Critical"
		// operation replaces it
		s.Clock.StepBy(10 * time.Minute)
		setAsset(plugins.StaticAsset{Size: 1000, Usage: 960})
		t.Must(scrapeJob.ProcessOne(ctx))

		t.ExpectPendingOperations(c.DB, db.PendingOperation{
			ID:          2,
			AssetID:     1,
			Reason:      castellum.OperationReasonCritical,
			OldSize:     1000,
			NewSize:     1200,
			Usage:       castellum.UsageValues{castellum.SingularUsageMetric: 960},
			CreatedAt:   s.Clock.Now(),
			ConfirmedAt: p2time(s.Clock.Now()),
			GreenlitAt:  p2time(s.Clock.Now()),
		})
		t.ExpectFinishedOperations(c.DB, db.FinishedOperation{
			AssetID:    1,
			Reason:     castellum.OperationReasonHigh,
			Outcome:    castellum.OperationOutcomeCancelled,
			OldSize:    1000,
			NewSize:    1200,
			Usage:      castellum.UsageValues{castellum.SingularUsageMetric: 900},
			CreatedAt:  s.Clock.Now().Add(-10 * time.Minute),
			FinishedAt: s.Clock.Now(),
		})
	})
}

func TestAssetScrapeOrdering(baseT *testing.T) {
	t := test.T{T: baseT}
	withContext(t, core.Config{}, func(ctx context.Context, s test.Setup, c *tasks.Context, amStatic *plugins.AssetManagerStatic, registry *prometheus.Registry) {
		scrapeJob := c.AssetScrapingJob(registry)
		// create a resource and multiple assets to test with
		t.Must(c.DB.Insert(&db.Resource{
			ScopeUUID:                "project1",
			AssetType:                "foo",
			LowThresholdPercent:      castellum.UsageValues{castellum.SingularUsageMetric: 20},
			LowDelaySeconds:          3600,
			HighThresholdPercent:     castellum.UsageValues{castellum.SingularUsageMetric: 80},
			HighDelaySeconds:         3600,
			CriticalThresholdPercent: castellum.UsageValues{castellum.SingularUsageMetric: 95},
			SizeStepPercent:          20,
		}))
		assets := []db.Asset{
			{
				ResourceID:   1,
				UUID:         "asset1",
				Size:         1000,
				Usage:        castellum.UsageValues{castellum.SingularUsageMetric: 500},
				NextScrapeAt: s.Clock.Now(),
				ExpectedSize: nil,
			},
			{
				ResourceID:   1,
				UUID:         "asset2",
				Size:         1000,
				Usage:        castellum.UsageValues{castellum.SingularUsageMetric: 500},
				NextScrapeAt: s.Clock.Now(),
				ExpectedSize: nil,
			},
			{
				ResourceID:   1,
				UUID:         "asset3",
				Size:         1000,
				Usage:        castellum.UsageValues{castellum.SingularUsageMetric: 500},
				NextScrapeAt: s.Clock.Now(),
				ExpectedSize: nil,
			},
		}
		t.Must(c.DB.Insert(&assets[0]))
		t.Must(c.DB.Insert(&assets[1]))
		t.Must(c.DB.Insert(&assets[2]))

		amStatic.Assets = map[string]map[string]plugins.StaticAsset{
			"project1": {
				"asset1": {Size: 1000, Usage: 510},
				"asset2": {Size: 1000, Usage: 520},
				"asset3": {Size: 1000, Usage: 530},
			},
		}

		// this should scrape each asset once, in order
		s.Clock.StepBy(10 * time.Minute)
		t.Must(scrapeJob.ProcessOne(ctx))
		s.Clock.StepBy(time.Minute)
		t.Must(scrapeJob.ProcessOne(ctx))
		s.Clock.StepBy(time.Minute)
		t.Must(scrapeJob.ProcessOne(ctx))

		// so the asset table should look like this now
		assets[0].NextScrapeAt = s.Clock.Now().Add(3 * time.Minute)
		assets[1].NextScrapeAt = s.Clock.Now().Add(4 * time.Minute)
		assets[2].NextScrapeAt = s.Clock.Now().Add(5 * time.Minute)
		assets[0].Usage = castellum.UsageValues{castellum.SingularUsageMetric: 510}
		assets[1].Usage = castellum.UsageValues{castellum.SingularUsageMetric: 520}
		assets[2].Usage = castellum.UsageValues{castellum.SingularUsageMetric: 530}
		t.ExpectAssets(c.DB, assets...)

		// next scrape should work identically
		s.Clock.StepBy(10 * time.Minute)
		t.Must(scrapeJob.ProcessOne(ctx))
		s.Clock.StepBy(time.Minute)
		t.Must(scrapeJob.ProcessOne(ctx))
		s.Clock.StepBy(time.Minute)
		t.Must(scrapeJob.ProcessOne(ctx))
		assets[0].NextScrapeAt = s.Clock.Now().Add(3 * time.Minute)
		assets[1].NextScrapeAt = s.Clock.Now().Add(4 * time.Minute)
		assets[2].NextScrapeAt = s.Clock.Now().Add(5 * time.Minute)
		t.ExpectAssets(c.DB, assets...)

		// and all of this should not have created any operations
		t.ExpectPendingOperations(c.DB /*, nothing */)
		t.ExpectFinishedOperations(c.DB /*, nothing */)
	})
}

func TestAssetScrapeReflectingResizeOperationWithDelay(baseT *testing.T) {
	t := test.T{T: baseT}
	runAssetScrapeTest(t, func(ctx context.Context, s test.Setup, c *tasks.Context, setAsset func(plugins.StaticAsset), scrapeJob jobloop.Job) {
		// make asset look like it just completed a resize operation
		t.MustExec(c.DB, `UPDATE assets SET expected_size = 1100, resized_at = $1`, s.Clock.Now())
		setAsset(plugins.StaticAsset{
			Size:           1000,
			Usage:          1000,
			NewSize:        1100,
			RemainingDelay: 2,
		})
		asset := db.Asset{
			ID:           1,
			ResourceID:   1,
			UUID:         "asset1",
			Size:         1000,
			Usage:        castellum.UsageValues{castellum.SingularUsageMetric: 500},
			ExpectedSize: p2uint64(1100),
			ResizedAt:    p2time(s.Clock.Now()),
		}

		// first scrape will not touch anything about the asset, and also not create
		// any operations (even though it could because of the currently high usage)
		// because the backend does not yet reflect the changed size
		s.Clock.StepBy(5 * time.Minute)
		t.Must(scrapeJob.ProcessOne(ctx))

		asset.NextScrapeAt = s.Clock.Now().Add(5 * time.Minute)
		t.ExpectAssets(c.DB, asset)

		t.ExpectPendingOperations(c.DB /*, nothing */)

		// second scrape will see the new size and update the asset accordingly, and
		// it will also create an operation because the usage is still above 80% after
		// the resize
		s.Clock.StepBy(5 * time.Minute)
		t.Must(scrapeJob.ProcessOne(ctx))

		asset.Size = 1100
		asset.Usage = castellum.UsageValues{castellum.SingularUsageMetric: 1000}
		asset.NextScrapeAt = s.Clock.Now().Add(5 * time.Minute)
		asset.ExpectedSize = nil
		asset.ResizedAt = nil
		t.ExpectAssets(c.DB, asset)

		t.ExpectPendingOperations(c.DB, db.PendingOperation{
			ID:        1,
			AssetID:   1,
			Reason:    castellum.OperationReasonHigh,
			OldSize:   1100,
			NewSize:   1320,
			Usage:     castellum.UsageValues{castellum.SingularUsageMetric: 1000},
			CreatedAt: s.Clock.Now(),
		})
	})
}

func TestAssetScrapeObservingNewSizeWhileWaitingForResize(baseT *testing.T) {
	// This is very similar to TestAssetScrapeReflectingResizeOperationWithDelay,
	// but we simulate an unrelated user-triggered resize operation taking place
	// in parallel with Castellum's resize operation, so we observe a new size
	// that's different from the expected size.
	t := test.T{T: baseT}
	runAssetScrapeTest(t, func(ctx context.Context, s test.Setup, c *tasks.Context, setAsset func(plugins.StaticAsset), scrapeJob jobloop.Job) {
		// make asset look like it just completed a resize operation
		t.MustExec(c.DB, `UPDATE assets SET expected_size = 1100, resized_at = $1`, s.Clock.Now())
		setAsset(plugins.StaticAsset{
			Size:  1200, //!= asset.ExpectedSize (see above)
			Usage: 600,
		})

		s.Clock.StepBy(5 * time.Minute)
		t.Must(scrapeJob.ProcessOne(ctx))
		t.ExpectAssets(c.DB, db.Asset{
			ID:           1,
			ResourceID:   1,
			UUID:         "asset1",
			Size:         1200,
			Usage:        castellum.UsageValues{castellum.SingularUsageMetric: 600},
			NextScrapeAt: s.Clock.Now().Add(5 * time.Minute),
			ExpectedSize: nil,
		})
	})
}

func TestAssetScrapesGivesUpWaitingForResize(baseT *testing.T) {
	// This is very similar to TestAssetScrapeReflectingResizeOperationWithDelay,
	// but we simulate that the resize failed in the backend without error. After
	// an hour, Castellum should give up waiting on the resize to complete and
	// resume normal operation.
	t := test.T{T: baseT}
	runAssetScrapeTest(t, func(ctx context.Context, s test.Setup, c *tasks.Context, setAsset func(plugins.StaticAsset), scrapeJob jobloop.Job) {
		// make asset look like it just completed a resize operation
		t.MustExec(c.DB, `UPDATE assets SET expected_size = 1100, resized_at = $1`, s.Clock.Now())
		setAsset(plugins.StaticAsset{
			Size:  1000, // == asset.Size (i.e. size before resize)
			Usage: 500,
		})
		asset := db.Asset{
			ID:           1,
			ResourceID:   1,
			UUID:         "asset1",
			Size:         1000,
			Usage:        castellum.UsageValues{castellum.SingularUsageMetric: 500},
			ExpectedSize: p2uint64(1100),
			ResizedAt:    p2time(s.Clock.Now()),
		}

		// first scrape will not touch anything, since it's still waiting for the resize to complete
		s.Clock.StepBy(5 * time.Minute)
		t.Must(scrapeJob.ProcessOne(ctx))

		asset.NextScrapeAt = s.Clock.Now().Add(5 * time.Minute)
		t.ExpectAssets(c.DB, asset)
		t.ExpectPendingOperations(c.DB /*, nothing */)

		// after an hour, the scrape gives up waiting for the resize and resumes as normal
		s.Clock.StepBy(1 * time.Hour)
		t.Must(scrapeJob.ProcessOne(ctx))

		asset.ExpectedSize = nil
		asset.ResizedAt = nil
		asset.NextScrapeAt = s.Clock.Now().Add(5 * time.Minute)
		t.ExpectAssets(c.DB, asset)
		t.ExpectPendingOperations(c.DB /*, nothing */)
	})
}

func TestAssetScrapeWithGetAssetStatusError(baseT *testing.T) {
	// This tests the behavior when GetAssetStatus returns an error:
	// 1. If core.AssetNotFoundError is returned then the asset is deleted from
	// the db.
	// 2. All other errors are passed through to the caller of ScrapeNextAsset,
	// but the asset's next_scrape_at timestamp is still updated to ensure that
	// the main loop progresses to the next asset.
	t := test.T{T: baseT}
	runAssetScrapeTest(t, func(ctx context.Context, s test.Setup, c *tasks.Context, setAsset func(plugins.StaticAsset), scrapeJob jobloop.Job) {
		setAsset(plugins.StaticAsset{
			Size:                 1000,
			Usage:                600,
			CannotGetAssetStatus: true,
		})

		s.Clock.StepBy(5 * time.Minute)
		err := scrapeJob.ProcessOne(ctx)
		expectedMsg := `cannot query status of foo asset1: GetAssetStatus failing as requested`
		if err == nil {
			t.Error("ScrapeNextAsset should have failed here")
		} else if err.Error() != expectedMsg {
			t.Errorf("expected error %q, got %q", expectedMsg, err.Error())
		}

		t.ExpectAssets(c.DB, db.Asset{
			ID:                 1,
			ResourceID:         1,
			UUID:               "asset1",
			Size:               1000,
			Usage:              castellum.UsageValues{castellum.SingularUsageMetric: 500}, // changed usage not observed because of error
			NextScrapeAt:       s.Clock.Now().Add(5 * time.Minute),
			ExpectedSize:       nil,
			ScrapeErrorMessage: "GetAssetStatus failing as requested",
			NeverScraped:       true,
		})

		// when GetAssetStatus starts working again, next ScrapeNextAsset should clear
		// the error field
		setAsset(plugins.StaticAsset{Size: 1000, Usage: 600})
		s.Clock.StepBy(5 * time.Minute)
		t.Must(scrapeJob.ProcessOne(ctx))

		t.ExpectAssets(c.DB, db.Asset{
			ID:                 1,
			ResourceID:         1,
			UUID:               "asset1",
			Size:               1000,
			Usage:              castellum.UsageValues{castellum.SingularUsageMetric: 600},
			NextScrapeAt:       s.Clock.Now().Add(5 * time.Minute),
			ExpectedSize:       nil,
			ScrapeErrorMessage: "",
			NeverScraped:       false,
		})

		//Note: this test should be at the end, see below.
		// Run GetAssetStatus on the same asset again except this time the
		// ScrapeNextAsset should delete the asset from the db.
		setAsset(plugins.StaticAsset{Size: 1000, Usage: 600, CannotFindAsset: true})
		s.Clock.StepBy(5 * time.Minute)
		t.Must(scrapeJob.ProcessOne(ctx))
		t.ExpectAssets(c.DB /*, nothing */)
	})
}

func TestExternalResizeWhileOperationPending(baseT *testing.T) {
	t := test.T{T: baseT}
	runAssetScrapeTest(t, func(ctx context.Context, s test.Setup, c *tasks.Context, setAsset func(plugins.StaticAsset), scrapeJob jobloop.Job) {
		// create a "High" operation
		s.Clock.StepBy(10 * time.Minute)
		setAsset(plugins.StaticAsset{Size: 1000, Usage: 900})
		t.Must(scrapeJob.ProcessOne(ctx))

		expectedOp := db.PendingOperation{
			ID:        1,
			AssetID:   1,
			Reason:    castellum.OperationReasonHigh,
			OldSize:   1000,
			NewSize:   1200,
			Usage:     castellum.UsageValues{castellum.SingularUsageMetric: 900},
			CreatedAt: s.Clock.Now(),
		}
		t.ExpectPendingOperations(c.DB, expectedOp)
		t.ExpectFinishedOperations(c.DB /*, nothing */)

		// while it is not greenlit yet, simulate a resize operation
		// being performed by an unrelated user
		s.Clock.StepBy(10 * time.Minute)
		setAsset(plugins.StaticAsset{Size: 1100, Usage: 900}) // bigger, but still >80% usage
		t.Must(scrapeJob.ProcessOne(ctx))

		// ScrapeNextAsset should have adjusted the NewSize to CurrentSize + SizeStep
		expectedOp.NewSize = 1320
		t.ExpectPendingOperations(c.DB, expectedOp)
		t.ExpectFinishedOperations(c.DB /*, nothing */)
	})
}

func TestMaxAssetSizeRules(baseT *testing.T) {
	t := test.T{T: baseT}
	withContext(t, core.Config{MaxAssetSizeRules: []core.MaxAssetSizeRule{{AssetTypeRx: "foo", ScopeUUID: "project1", Value: 800}}}, func(ctx context.Context, s test.Setup, c *tasks.Context, amStatic *plugins.AssetManagerStatic, registry *prometheus.Registry) {
		scrapeJob := c.AssetScrapingJob(registry)
		t.Must(c.DB.Insert(&db.Resource{
			ScopeUUID:                "project1",
			AssetType:                "foo",
			LowThresholdPercent:      castellum.UsageValues{castellum.SingularUsageMetric: 20},
			LowDelaySeconds:          3600,
			HighThresholdPercent:     castellum.UsageValues{castellum.SingularUsageMetric: 80},
			HighDelaySeconds:         3600,
			CriticalThresholdPercent: castellum.UsageValues{castellum.SingularUsageMetric: 95},
			SizeStepPercent:          20,
		}))
		asset := db.Asset{
			ResourceID:   1,
			UUID:         "asset1",
			Size:         1000,
			Usage:        castellum.UsageValues{castellum.SingularUsageMetric: 500},
			NextScrapeAt: s.Clock.Now(),
			ExpectedSize: nil,
		}
		t.Must(c.DB.Insert(&asset))

		amStatic.Assets = map[string]map[string]plugins.StaticAsset{
			"project1": {
				"asset1": {Size: 1000, Usage: 510},
			},
		}

		s.Clock.StepBy(10 * time.Minute)
		t.Must(scrapeJob.ProcessOne(ctx))

		asset.NextScrapeAt = s.Clock.Now().Add(5 * time.Minute)
		asset.Usage = castellum.UsageValues{castellum.SingularUsageMetric: 510}
		t.ExpectAssets(c.DB, asset)

		t.ExpectPendingOperations(c.DB, db.PendingOperation{
			ID:        1,
			AssetID:   1,
			Reason:    castellum.OperationReasonLow,
			OldSize:   1000,
			NewSize:   800,
			Usage:     castellum.UsageValues{castellum.SingularUsageMetric: 510},
			CreatedAt: s.Clock.Now(),
		})
		t.ExpectFinishedOperations(c.DB /*, nothing */)
	})
}
