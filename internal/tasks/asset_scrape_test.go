// SPDX-FileCopyrightText: 2019 SAP SE or an SAP affiliate company
// SPDX-License-Identifier: Apache-2.0

package tasks_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	. "github.com/majewsky/gg/option"
	"github.com/sapcc/go-api-declarations/castellum"
	"github.com/sapcc/go-bits/assert"
	"github.com/sapcc/go-bits/easypg"
	"github.com/sapcc/go-bits/jobloop"
	"github.com/sapcc/go-bits/must"

	"github.com/sapcc/castellum/internal/db"
	"github.com/sapcc/castellum/internal/plugins"
	"github.com/sapcc/castellum/internal/tasks"
	"github.com/sapcc/castellum/internal/test"
)

func runAssetScrapeTest(t *testing.T, action func(context.Context, test.Setup, func(plugins.StaticAsset), jobloop.Job)) {
	ctx := t.Context()
	s := test.NewSetup(t,
		commonSetupOptionsForWorkerTest(),
	)
	scrapeJob := s.TaskContext.AssetScrapingJob(s.Registry)

	// asset scrape without any resources just does nothing
	err := scrapeJob.ProcessOne(ctx)
	if !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("expected sql.ErrNoRows, got %s instead", err.Error())
	}
	_, dbDump := easypg.NewTracker(t, s.DB.Db)
	dbDump.AssertEmpty()

	// create a resource and asset to test with
	must.SucceedT(t, s.DB.Insert(&db.Resource{
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
	must.SucceedT(t, s.DB.Insert(&db.Asset{
		ResourceID:   1,
		UUID:         "asset1",
		Size:         1000,
		Usage:        castellum.UsageValues{castellum.SingularUsageMetric: 500},
		NextScrapeAt: s.Clock.Now(),
		NeverScraped: true,
		ExpectedSize: None[uint64](),
	}))

	// setup asset with configurable size
	amStatic := s.ManagerForAssetType("foo")
	amStatic.Assets = map[string]map[string]plugins.StaticAsset{
		"project1": {
			"asset1": {Size: 1000, Usage: 500},
		},
	}
	setAsset := func(a plugins.StaticAsset) {
		amStatic.Assets["project1"]["asset1"] = a
	}

	action(ctx, s, setAsset, scrapeJob)
}

func TestNoOperationWhenNoThreshold(t *testing.T) {
	runAssetScrapeTest(t, func(ctx context.Context, s test.Setup, setAsset func(plugins.StaticAsset), scrapeJob jobloop.Job) {
		tr, tr0 := easypg.NewTracker(t, s.DB.Db)
		tr0.Ignore()

		// when no threshold is crossed, no operation gets created
		s.Clock.StepBy(10 * time.Minute)
		must.SucceedT(t, scrapeJob.ProcessOne(ctx))

		tr.DBChanges().AssertEqualf(`
				UPDATE assets SET next_scrape_at = %[1]d, never_scraped = FALSE WHERE id = 1 AND resource_id = 1 AND uuid = 'asset1';
			`,
			s.Clock.Now().Add(tasks.AssetScrapeInterval).Unix(),
		)
	})
}

func TestNormalUpsizeTowardsGreenlight(t *testing.T) {
	runAssetScrapeTest(t, func(ctx context.Context, s test.Setup, setAsset func(plugins.StaticAsset), scrapeJob jobloop.Job) {
		tr, tr0 := easypg.NewTracker(t, s.DB.Db)
		tr0.Ignore()

		// set a maximum size that does not contradict the following operations
		// (down below, there's a separate test for when the maximum size actually
		// inhibits upsizing)
		must.SucceedT(t, s.DBExec(`UPDATE resources SET max_size = 2000`))
		tr.DBChanges().Ignore()

		// when the "High" threshold gets crossed, a "High" operation gets created in
		// state "created"
		s.Clock.StepBy(10 * time.Minute)
		setAsset(plugins.StaticAsset{Size: 1000, Usage: 800})
		must.SucceedT(t, scrapeJob.ProcessOne(ctx))

		tr.DBChanges().AssertEqualf(`
				UPDATE assets SET usage = '{"singular":800}', next_scrape_at = %[1]d, never_scraped = FALSE WHERE id = 1 AND resource_id = 1 AND uuid = 'asset1';
				INSERT INTO pending_operations (id, asset_id, reason, old_size, new_size, created_at, usage) VALUES (1, 1, 'high', 1000, 1200, %[2]d, '{"singular":800}');
			`,
			s.Clock.Now().Add(tasks.AssetScrapeInterval).Unix(),
			s.Clock.Now().Unix(),
		)

		// another scrape while the delay is not over should not change the state
		// (but for single-step resizing which takes the current usage into account,
		// the NewSize is updated to put the target size outside of the high
		// threshold again)
		s.Clock.StepBy(40 * time.Minute)
		setAsset(plugins.StaticAsset{Size: 1000, Usage: 820})
		must.SucceedT(t, scrapeJob.ProcessOne(ctx))

		tr.DBChanges().AssertEqualf(`
				UPDATE assets SET usage = '{"singular":820}', next_scrape_at = %[1]d WHERE id = 1 AND resource_id = 1 AND uuid = 'asset1';
			`,
			s.Clock.Now().Add(tasks.AssetScrapeInterval).Unix(),
		)

		// when the delay is over, the next scrape moves into state "Confirmed/Greenlit"
		s.Clock.StepBy(40 * time.Minute)
		setAsset(plugins.StaticAsset{Size: 1000, Usage: 840})
		must.SucceedT(t, scrapeJob.ProcessOne(ctx))

		tr.DBChanges().AssertEqualf(`
				UPDATE assets SET usage = '{"singular":840}', next_scrape_at = %[1]d WHERE id = 1 AND resource_id = 1 AND uuid = 'asset1';
				UPDATE pending_operations SET confirmed_at = %[2]d, greenlit_at = %[2]d WHERE id = 1 AND asset_id = 1;
			`,
			s.Clock.Now().Add(tasks.AssetScrapeInterval).Unix(),
			s.Clock.Now().Unix(),
		)

		// since the operation is now greenlit and can be picked up by a worker at any
		// moment, we should not touch it anymore even if the reason disappears
		s.Clock.StepBy(40 * time.Minute)
		setAsset(plugins.StaticAsset{Size: 1000, Usage: 780})
		must.SucceedT(t, scrapeJob.ProcessOne(ctx))

		tr.DBChanges().AssertEqualf(`
				UPDATE assets SET usage = '{"singular":780}', next_scrape_at = %[1]d WHERE id = 1 AND resource_id = 1 AND uuid = 'asset1';
			`,
			s.Clock.Now().Add(tasks.AssetScrapeInterval).Unix(),
		)
	})
}

func TestNormalUpsizeTowardsCancel(t *testing.T) {
	runAssetScrapeTest(t, func(ctx context.Context, s test.Setup, setAsset func(plugins.StaticAsset), scrapeJob jobloop.Job) {
		tr, tr0 := easypg.NewTracker(t, s.DB.Db)
		tr0.Ignore()

		// when the "High" threshold gets crossed, a "High" operation gets created in
		// state "created"
		s.Clock.StepBy(10 * time.Minute)
		setAsset(plugins.StaticAsset{Size: 1000, Usage: 800})
		must.SucceedT(t, scrapeJob.ProcessOne(ctx))

		opCreatedAt := s.Clock.Now()
		tr.DBChanges().AssertEqualf(`
				UPDATE assets SET usage = '{"singular":800}', next_scrape_at = %[1]d, never_scraped = FALSE WHERE id = 1 AND resource_id = 1 AND uuid = 'asset1';
				INSERT INTO pending_operations (id, asset_id, reason, old_size, new_size, created_at, usage) VALUES (1, 1, 'high', 1000, 1200, %[2]d, '{"singular":800}');
			`,
			s.Clock.Now().Add(tasks.AssetScrapeInterval).Unix(),
			opCreatedAt.Unix(),
		)

		// when the reason disappears within the delay, the operation is cancelled
		s.Clock.StepBy(40 * time.Minute)
		setAsset(plugins.StaticAsset{Size: 1000, Usage: 790})
		must.SucceedT(t, scrapeJob.ProcessOne(ctx))

		tr.DBChanges().AssertEqualf(`
				UPDATE assets SET usage = '{"singular":790}', next_scrape_at = %[1]d WHERE id = 1 AND resource_id = 1 AND uuid = 'asset1';
				INSERT INTO finished_operations (asset_id, reason, outcome, old_size, new_size, created_at, finished_at, usage) VALUES (1, 'high', 'cancelled', 1000, 1200, %[2]d, %[3]d, '{"singular":800}');
				DELETE FROM pending_operations WHERE id = 1 AND asset_id = 1;
			`,
			s.Clock.Now().Add(tasks.AssetScrapeInterval).Unix(),
			opCreatedAt.Unix(),
			s.Clock.Now().Unix(),
		)
	})
}

func TestNormalDownsizeTowardsGreenlight(t *testing.T) {
	runAssetScrapeTest(t, func(ctx context.Context, s test.Setup, setAsset func(plugins.StaticAsset), scrapeJob jobloop.Job) {
		tr, tr0 := easypg.NewTracker(t, s.DB.Db)
		tr0.Ignore()

		// set a minimum size that does not contradict the following operations
		// (down below, there's a separate test for when the minimum size actually
		// inhibits upsizing)
		must.SucceedT(t, s.DBExec(`UPDATE resources SET min_size = 200`))
		tr.DBChanges().Ignore()

		// when the "Low" threshold gets crossed, a "Low" operation gets created in
		// state "created"
		s.Clock.StepBy(10 * time.Minute)
		setAsset(plugins.StaticAsset{Size: 1000, Usage: 200})
		must.SucceedT(t, scrapeJob.ProcessOne(ctx))

		tr.DBChanges().AssertEqualf(`
				UPDATE assets SET usage = '{"singular":200}', next_scrape_at = %[1]d, never_scraped = FALSE WHERE id = 1 AND resource_id = 1 AND uuid = 'asset1';
				INSERT INTO pending_operations (id, asset_id, reason, old_size, new_size, created_at, usage) VALUES (1, 1, 'low', 1000, 800, %[2]d, '{"singular":200}');
			`,
			s.Clock.Now().Add(tasks.AssetScrapeInterval).Unix(),
			s.Clock.Now().Unix(),
		)

		// another scrape while the delay is not over should not change the state
		// (but for single-step resizing which takes the current usage into account,
		// the NewSize is updated to put the target size above the low threshold
		// again)
		s.Clock.StepBy(40 * time.Minute)
		setAsset(plugins.StaticAsset{Size: 1000, Usage: 180})
		must.SucceedT(t, scrapeJob.ProcessOne(ctx))

		tr.DBChanges().AssertEqualf(`
				UPDATE assets SET usage = '{"singular":180}', next_scrape_at = %[1]d WHERE id = 1 AND resource_id = 1 AND uuid = 'asset1';
			`,
			s.Clock.Now().Add(tasks.AssetScrapeInterval).Unix(),
		)

		// when the delay is over, the next scrape moves into state "Confirmed/Greenlit"
		s.Clock.StepBy(40 * time.Minute)
		setAsset(plugins.StaticAsset{Size: 1000, Usage: 160})
		must.SucceedT(t, scrapeJob.ProcessOne(ctx))

		tr.DBChanges().AssertEqualf(`
				UPDATE assets SET usage = '{"singular":160}', next_scrape_at = %[1]d WHERE id = 1 AND resource_id = 1 AND uuid = 'asset1';
				UPDATE pending_operations SET confirmed_at = %[2]d, greenlit_at = %[2]d WHERE id = 1 AND asset_id = 1;
			`,
			s.Clock.Now().Add(tasks.AssetScrapeInterval).Unix(),
			s.Clock.Now().Unix(),
		)

		// since the operation is now greenlit and can be picked up by a worker at any
		// moment, we should not touch it anymore even if the reason disappears
		s.Clock.StepBy(40 * time.Minute)
		setAsset(plugins.StaticAsset{Size: 1000, Usage: 220})
		must.SucceedT(t, scrapeJob.ProcessOne(ctx))

		tr.DBChanges().AssertEqualf(`
				UPDATE assets SET usage = '{"singular":220}', next_scrape_at = %[1]d WHERE id = 1 AND resource_id = 1 AND uuid = 'asset1';
			`,
			s.Clock.Now().Add(tasks.AssetScrapeInterval).Unix(),
		)
	})
}

func TestNormalDownsizeTowardsCancel(t *testing.T) {
	runAssetScrapeTest(t, func(ctx context.Context, s test.Setup, setAsset func(plugins.StaticAsset), scrapeJob jobloop.Job) {
		tr, tr0 := easypg.NewTracker(t, s.DB.Db)
		tr0.Ignore()

		// when the "Low" threshold gets crossed, a "Low" operation gets created in
		// state "created"
		s.Clock.StepBy(10 * time.Minute)
		setAsset(plugins.StaticAsset{Size: 1000, Usage: 200})
		must.SucceedT(t, scrapeJob.ProcessOne(ctx))

		opCreatedAt := s.Clock.Now()
		tr.DBChanges().AssertEqualf(`
				UPDATE assets SET usage = '{"singular":200}', next_scrape_at = %[1]d, never_scraped = FALSE WHERE id = 1 AND resource_id = 1 AND uuid = 'asset1';
				INSERT INTO pending_operations (id, asset_id, reason, old_size, new_size, created_at, usage) VALUES (1, 1, 'low', 1000, 800, %[2]d, '{"singular":200}');
			`,
			s.Clock.Now().Add(tasks.AssetScrapeInterval).Unix(),
			opCreatedAt.Unix(),
		)

		// when the reason disappears within the delay, the operation is cancelled
		s.Clock.StepBy(40 * time.Minute)
		setAsset(plugins.StaticAsset{Size: 1000, Usage: 210})
		must.SucceedT(t, scrapeJob.ProcessOne(ctx))

		tr.DBChanges().AssertEqualf(`
				UPDATE assets SET usage = '{"singular":210}', next_scrape_at = %[1]d WHERE id = 1 AND resource_id = 1 AND uuid = 'asset1';
				INSERT INTO finished_operations (asset_id, reason, outcome, old_size, new_size, created_at, finished_at, usage) VALUES (1, 'low', 'cancelled', 1000, 800, %[2]d, %[3]d, '{"singular":200}');
				DELETE FROM pending_operations WHERE id = 1 AND asset_id = 1;
			`,
			s.Clock.Now().Add(tasks.AssetScrapeInterval).Unix(),
			opCreatedAt.Unix(),
			s.Clock.Now().Unix(),
		)
	})
}

func TestCriticalUpsizeTowardsGreenlight(t *testing.T) {
	runAssetScrapeTest(t, func(ctx context.Context, s test.Setup, setAsset func(plugins.StaticAsset), scrapeJob jobloop.Job) {
		tr, tr0 := easypg.NewTracker(t, s.DB.Db)
		tr0.Ignore()

		// when the "Critical" threshold gets crossed, a "Critical" operation gets
		// created and immediately confirmed/greenlit
		s.Clock.StepBy(10 * time.Minute)
		setAsset(plugins.StaticAsset{Size: 1000, Usage: 950})
		must.SucceedT(t, scrapeJob.ProcessOne(ctx))

		tr.DBChanges().AssertEqualf(`
				UPDATE assets SET usage = '{"singular":950}', critical_usages = 'singular', next_scrape_at = %[1]d, never_scraped = FALSE WHERE id = 1 AND resource_id = 1 AND uuid = 'asset1';
				INSERT INTO pending_operations (id, asset_id, reason, old_size, new_size, created_at, confirmed_at, greenlit_at, usage) VALUES (1, 1, 'critical', 1000, 1200, %[2]d, %[2]d, %[2]d, '{"singular":950}');
			`,
			s.Clock.Now().Add(tasks.AssetScrapeInterval).Unix(),
			s.Clock.Now().Unix(),
		)
	})
}

func TestReplaceNormalWithCriticalUpsize(t *testing.T) {
	runAssetScrapeTest(t, func(ctx context.Context, s test.Setup, setAsset func(plugins.StaticAsset), scrapeJob jobloop.Job) {
		tr, tr0 := easypg.NewTracker(t, s.DB.Db)
		tr0.Ignore()

		// when the "High" threshold gets crossed, a "High" operation gets created in
		// state "created"
		s.Clock.StepBy(10 * time.Minute)
		setAsset(plugins.StaticAsset{Size: 1000, Usage: 900})
		must.SucceedT(t, scrapeJob.ProcessOne(ctx))

		opCreatedAt := s.Clock.Now()
		tr.DBChanges().AssertEqualf(`
				UPDATE assets SET usage = '{"singular":900}', next_scrape_at = %[1]d, never_scraped = FALSE WHERE id = 1 AND resource_id = 1 AND uuid = 'asset1';
				INSERT INTO pending_operations (id, asset_id, reason, old_size, new_size, created_at, usage) VALUES (1, 1, 'high', 1000, 1200, %[2]d, '{"singular":900}');
			`,
			s.Clock.Now().Add(tasks.AssetScrapeInterval).Unix(),
			opCreatedAt.Unix(),
		)

		// when the "Critical" threshold gets crossed while the "High" operation
		// is not yet confirmed, the "High" operation is cancelled and a "Critical"
		// operation replaces it
		s.Clock.StepBy(10 * time.Minute)
		setAsset(plugins.StaticAsset{Size: 1000, Usage: 960})
		must.SucceedT(t, scrapeJob.ProcessOne(ctx))

		tr.DBChanges().AssertEqualf(`
				UPDATE assets SET usage = '{"singular":960}', critical_usages = 'singular', next_scrape_at = %[1]d WHERE id = 1 AND resource_id = 1 AND uuid = 'asset1';
				INSERT INTO finished_operations (asset_id, reason, outcome, old_size, new_size, created_at, finished_at, usage) VALUES (1, 'high', 'cancelled', 1000, 1200, %[2]d, %[3]d, '{"singular":900}');
				DELETE FROM pending_operations WHERE id = 1 AND asset_id = 1;
				INSERT INTO pending_operations (id, asset_id, reason, old_size, new_size, created_at, confirmed_at, greenlit_at, usage) VALUES (2, 1, 'critical', 1000, 1200, %[3]d, %[3]d, %[3]d, '{"singular":960}');
			`,
			s.Clock.Now().Add(tasks.AssetScrapeInterval).Unix(),
			opCreatedAt.Unix(),
			s.Clock.Now().Unix(),
		)
	})
}

func TestAssetScrapeOrdering(t *testing.T) {
	ctx := t.Context()
	s := test.NewSetup(t,
		commonSetupOptionsForWorkerTest(),
	)
	scrapeJob := s.TaskContext.AssetScrapingJob(s.Registry)
	// create a resource and multiple assets to test with
	must.SucceedT(t, s.DB.Insert(&db.Resource{
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
			ExpectedSize: None[uint64](),
		},
		{
			ResourceID:   1,
			UUID:         "asset2",
			Size:         1000,
			Usage:        castellum.UsageValues{castellum.SingularUsageMetric: 500},
			NextScrapeAt: s.Clock.Now(),
			ExpectedSize: None[uint64](),
		},
		{
			ResourceID:   1,
			UUID:         "asset3",
			Size:         1000,
			Usage:        castellum.UsageValues{castellum.SingularUsageMetric: 500},
			NextScrapeAt: s.Clock.Now(),
			ExpectedSize: None[uint64](),
		},
	}
	must.SucceedT(t, s.DB.Insert(&assets[0]))
	must.SucceedT(t, s.DB.Insert(&assets[1]))
	must.SucceedT(t, s.DB.Insert(&assets[2]))

	amStatic := s.ManagerForAssetType("foo")
	amStatic.Assets = map[string]map[string]plugins.StaticAsset{
		"project1": {
			"asset1": {Size: 1000, Usage: 510},
			"asset2": {Size: 1000, Usage: 520},
			"asset3": {Size: 1000, Usage: 530},
		},
	}

	tr, tr0 := easypg.NewTracker(t, s.DB.Db)
	tr0.Ignore()

	// this should scrape each asset once, in order
	s.Clock.StepBy(10 * time.Minute)
	must.SucceedT(t, scrapeJob.ProcessOne(ctx))
	s.Clock.StepBy(time.Minute)
	must.SucceedT(t, scrapeJob.ProcessOne(ctx))
	s.Clock.StepBy(time.Minute)
	must.SucceedT(t, scrapeJob.ProcessOne(ctx))

	tr.DBChanges().AssertEqualf(`
			UPDATE assets SET usage = '{"singular":510}', next_scrape_at = %[1]d WHERE id = 1 AND resource_id = 1 AND uuid = 'asset1';
			UPDATE assets SET usage = '{"singular":520}', next_scrape_at = %[2]d WHERE id = 2 AND resource_id = 1 AND uuid = 'asset2';
			UPDATE assets SET usage = '{"singular":530}', next_scrape_at = %[3]d WHERE id = 3 AND resource_id = 1 AND uuid = 'asset3';
		`,
		s.Clock.Now().Add(3*time.Minute).Unix(),
		s.Clock.Now().Add(4*time.Minute).Unix(),
		s.Clock.Now().Add(5*time.Minute).Unix(),
	)

	// next scrape should work identically
	s.Clock.StepBy(10 * time.Minute)
	must.SucceedT(t, scrapeJob.ProcessOne(ctx))
	s.Clock.StepBy(time.Minute)
	must.SucceedT(t, scrapeJob.ProcessOne(ctx))
	s.Clock.StepBy(time.Minute)
	must.SucceedT(t, scrapeJob.ProcessOne(ctx))

	tr.DBChanges().AssertEqualf(`
			UPDATE assets SET next_scrape_at = %[1]d WHERE id = 1 AND resource_id = 1 AND uuid = 'asset1';
			UPDATE assets SET next_scrape_at = %[2]d WHERE id = 2 AND resource_id = 1 AND uuid = 'asset2';
			UPDATE assets SET next_scrape_at = %[3]d WHERE id = 3 AND resource_id = 1 AND uuid = 'asset3';
		`,
		s.Clock.Now().Add(3*time.Minute).Unix(),
		s.Clock.Now().Add(4*time.Minute).Unix(),
		s.Clock.Now().Add(5*time.Minute).Unix(),
	)
}

func TestAssetScrapeReflectingResizeOperationWithDelay(t *testing.T) {
	runAssetScrapeTest(t, func(ctx context.Context, s test.Setup, setAsset func(plugins.StaticAsset), scrapeJob jobloop.Job) {
		// make asset look like it just completed a resize operation
		resizedAt := s.Clock.Now()
		must.SucceedT(t, s.DBExec(`UPDATE assets SET expected_size = 1100, resized_at = $1`, resizedAt))
		setAsset(plugins.StaticAsset{
			Size:           1000,
			Usage:          1000,
			NewSize:        1100,
			RemainingDelay: 2,
		})

		tr, tr0 := easypg.NewTracker(t, s.DB.Db)
		tr0.Ignore()

		// first scrape will not touch anything about the asset, and also not create
		// any operations (even though it could because of the currently high usage)
		// because the backend does not yet reflect the changed size
		s.Clock.StepBy(5 * time.Minute)
		must.SucceedT(t, scrapeJob.ProcessOne(ctx))

		tr.DBChanges().AssertEqualf(`
				UPDATE assets SET next_scrape_at = %[1]d, never_scraped = FALSE WHERE id = 1 AND resource_id = 1 AND uuid = 'asset1';
			`,
			s.Clock.Now().Add(tasks.AssetScrapeInterval).Unix(),
		)

		// second scrape will see the new size and update the asset accordingly, and
		// it will also create an operation because the usage is still above 80% after
		// the resize
		s.Clock.StepBy(5 * time.Minute)
		must.SucceedT(t, scrapeJob.ProcessOne(ctx))

		tr.DBChanges().AssertEqualf(`
				UPDATE assets SET size = 1100, expected_size = NULL, usage = '{"singular":1000}', next_scrape_at = %[1]d, resized_at = NULL WHERE id = 1 AND resource_id = 1 AND uuid = 'asset1';
				INSERT INTO pending_operations (id, asset_id, reason, old_size, new_size, created_at, usage) VALUES (1, 1, 'high', 1100, 1320, %[2]d, '{"singular":1000}');
			`,
			s.Clock.Now().Add(tasks.AssetScrapeInterval).Unix(),
			s.Clock.Now().Unix(),
		)
	})
}

func TestAssetScrapeObservingNewSizeWhileWaitingForResize(t *testing.T) {
	// This is very similar to TestAssetScrapeReflectingResizeOperationWithDelay,
	// but we simulate an unrelated user-triggered resize operation taking place
	// in parallel with Castellum's resize operation, so we observe a new size
	// that's different from the expected size.
	runAssetScrapeTest(t, func(ctx context.Context, s test.Setup, setAsset func(plugins.StaticAsset), scrapeJob jobloop.Job) {
		// make asset look like it just completed a resize operation
		must.SucceedT(t, s.DBExec(`UPDATE assets SET expected_size = 1100, resized_at = $1`, s.Clock.Now()))
		setAsset(plugins.StaticAsset{
			Size:  1200, //!= asset.ExpectedSize (see above)
			Usage: 600,
		})

		tr, tr0 := easypg.NewTracker(t, s.DB.Db)
		tr0.Ignore()

		s.Clock.StepBy(5 * time.Minute)
		must.SucceedT(t, scrapeJob.ProcessOne(ctx))

		tr.DBChanges().AssertEqualf(`
				UPDATE assets SET size = 1200, expected_size = NULL, usage = '{"singular":600}', next_scrape_at = %[1]d, never_scraped = FALSE, resized_at = NULL WHERE id = 1 AND resource_id = 1 AND uuid = 'asset1';
			`,
			s.Clock.Now().Add(tasks.AssetScrapeInterval).Unix(),
		)
	})
}

func TestAssetScrapesGivesUpWaitingForResize(t *testing.T) {
	// This is very similar to TestAssetScrapeReflectingResizeOperationWithDelay,
	// but we simulate that the resize failed in the backend without error. After
	// an hour, Castellum should give up waiting on the resize to complete and
	// resume normal operation.
	runAssetScrapeTest(t, func(ctx context.Context, s test.Setup, setAsset func(plugins.StaticAsset), scrapeJob jobloop.Job) {
		// make asset look like it just completed a resize operation
		must.SucceedT(t, s.DBExec(`UPDATE assets SET expected_size = 1100, resized_at = $1`, s.Clock.Now()))
		setAsset(plugins.StaticAsset{
			Size:  1000, // == asset.Size (i.e. size before resize)
			Usage: 500,
		})

		tr, tr0 := easypg.NewTracker(t, s.DB.Db)
		tr0.Ignore()

		// first scrape will not touch anything, since it's still waiting for the resize to complete
		s.Clock.StepBy(5 * time.Minute)
		must.SucceedT(t, scrapeJob.ProcessOne(ctx))

		tr.DBChanges().AssertEqualf(`
				UPDATE assets SET next_scrape_at = %[1]d, never_scraped = FALSE WHERE id = 1 AND resource_id = 1 AND uuid = 'asset1';
			`,
			s.Clock.Now().Add(tasks.AssetScrapeInterval).Unix(),
		)

		// after an hour, the scrape gives up waiting for the resize and resumes as normal
		s.Clock.StepBy(1 * time.Hour)
		must.SucceedT(t, scrapeJob.ProcessOne(ctx))

		tr.DBChanges().AssertEqualf(`
				UPDATE assets SET expected_size = NULL, next_scrape_at = %[1]d, resized_at = NULL WHERE id = 1 AND resource_id = 1 AND uuid = 'asset1';
			`,
			s.Clock.Now().Add(tasks.AssetScrapeInterval).Unix(),
		)
	})
}

func TestAssetScrapeWithGetAssetStatusError(t *testing.T) {
	// This tests the behavior when GetAssetStatus returns an error:
	// 1. If core.AssetNotFoundError is returned then the asset is deleted from
	// the db.
	// 2. All other errors are passed through to the caller of ScrapeNextAsset,
	// but the asset's next_scrape_at timestamp is still updated to ensure that
	// the main loop progresses to the next asset.
	runAssetScrapeTest(t, func(ctx context.Context, s test.Setup, setAsset func(plugins.StaticAsset), scrapeJob jobloop.Job) {
		tr, tr0 := easypg.NewTracker(t, s.DB.Db)
		tr0.Ignore()

		setAsset(plugins.StaticAsset{
			Size:                 1000,
			Usage:                600,
			CannotGetAssetStatus: true,
		})

		s.Clock.StepBy(5 * time.Minute)
		err := scrapeJob.ProcessOne(ctx)
		assert.ErrEqual(t, err, "cannot query status of foo asset1: GetAssetStatus failing as requested")

		tr.DBChanges().AssertEqualf(`
				UPDATE assets SET scrape_error_message = '%[1]s', next_scrape_at = %[2]d WHERE id = 1 AND resource_id = 1 AND uuid = 'asset1';
			`,
			"GetAssetStatus failing as requested",
			s.Clock.Now().Add(tasks.AssetScrapeInterval).Unix(),
		)

		// when GetAssetStatus starts working again, next ScrapeNextAsset should clear
		// the error field
		setAsset(plugins.StaticAsset{Size: 1000, Usage: 600})
		s.Clock.StepBy(5 * time.Minute)
		must.SucceedT(t, scrapeJob.ProcessOne(ctx))

		tr.DBChanges().AssertEqualf(`
				UPDATE assets SET scrape_error_message = '', usage = '{"singular":600}', next_scrape_at = %[1]d, never_scraped = FALSE WHERE id = 1 AND resource_id = 1 AND uuid = 'asset1';
			`,
			s.Clock.Now().Add(tasks.AssetScrapeInterval).Unix(),
		)

		//Note: this test should be at the end, see below.
		// Run GetAssetStatus on the same asset again except this time the
		// ScrapeNextAsset should delete the asset from the db.
		setAsset(plugins.StaticAsset{Size: 1000, Usage: 600, CannotFindAsset: true})
		s.Clock.StepBy(5 * time.Minute)
		must.SucceedT(t, scrapeJob.ProcessOne(ctx))

		tr.DBChanges().AssertEqualf(`DELETE FROM assets WHERE id = 1 AND resource_id = 1 AND uuid = 'asset1';`)
	})
}

func TestExternalResizeWhileOperationPending(t *testing.T) {
	runAssetScrapeTest(t, func(ctx context.Context, s test.Setup, setAsset func(plugins.StaticAsset), scrapeJob jobloop.Job) {
		tr, tr0 := easypg.NewTracker(t, s.DB.Db)
		tr0.Ignore()

		// create a "High" operation
		s.Clock.StepBy(10 * time.Minute)
		setAsset(plugins.StaticAsset{Size: 1000, Usage: 900})
		must.SucceedT(t, scrapeJob.ProcessOne(ctx))

		tr.DBChanges().AssertEqualf(`
				UPDATE assets SET usage = '{"singular":900}', next_scrape_at = %[1]d, never_scraped = FALSE WHERE id = 1 AND resource_id = 1 AND uuid = 'asset1';
				INSERT INTO pending_operations (id, asset_id, reason, old_size, new_size, created_at, usage) VALUES (1, 1, 'high', 1000, 1200, %[2]d, '{"singular":900}');
			`,
			s.Clock.Now().Add(tasks.AssetScrapeInterval).Unix(),
			s.Clock.Now().Unix(),
		)

		// while it is not greenlit yet, simulate a resize operation
		// being performed by an unrelated user
		s.Clock.StepBy(10 * time.Minute)
		setAsset(plugins.StaticAsset{Size: 1100, Usage: 900}) // bigger, but still >80% usage
		must.SucceedT(t, scrapeJob.ProcessOne(ctx))

		// ScrapeJob should have adjusted the NewSize to CurrentSize + SizeStep
		tr.DBChanges().AssertEqualf(`
				UPDATE assets SET size = 1100, next_scrape_at = %[1]d WHERE id = 1 AND resource_id = 1 AND uuid = 'asset1';
				UPDATE pending_operations SET new_size = 1320 WHERE id = 1 AND asset_id = 1;
			`,
			s.Clock.Now().Add(tasks.AssetScrapeInterval).Unix(),
			s.Clock.Now().Unix(),
		)
	})
}

func TestMaxAssetSizeRules(t *testing.T) {
	ctx := t.Context()
	s := test.NewSetup(t,
		commonSetupOptionsForWorkerTest(),
		test.WithConfig(`{
			"max_asset_sizes": [
				{ "asset_type": "foo", "scope_uuid": "project1", "value": 800 }
			]
		}`),
	)
	scrapeJob := s.TaskContext.AssetScrapingJob(s.Registry)
	must.SucceedT(t, s.DB.Insert(&db.Resource{
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
		ExpectedSize: None[uint64](),
	}
	must.SucceedT(t, s.DB.Insert(&asset))

	tr, tr0 := easypg.NewTracker(t, s.DB.Db)
	tr0.Ignore()

	amStatic := s.ManagerForAssetType("foo")
	amStatic.Assets = map[string]map[string]plugins.StaticAsset{
		"project1": {
			"asset1": {Size: 1000, Usage: 510},
		},
	}

	s.Clock.StepBy(10 * time.Minute)
	must.SucceedT(t, scrapeJob.ProcessOne(ctx))

	tr.DBChanges().AssertEqualf(`
			UPDATE assets SET usage = '{"singular":510}', next_scrape_at = %[1]d WHERE id = 1 AND resource_id = 1 AND uuid = 'asset1';
			INSERT INTO pending_operations (id, asset_id, reason, old_size, new_size, created_at, usage) VALUES (1, 1, 'low', 1000, 800, %[2]d, '{"singular":510}');
		`,
		s.Clock.Now().Add(tasks.AssetScrapeInterval).Unix(),
		s.Clock.Now().Unix(),
	)
}
