// SPDX-FileCopyrightText: 2019 SAP SE or an SAP affiliate company
// SPDX-License-Identifier: Apache-2.0

package tasks_test

import (
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/sapcc/go-api-declarations/castellum"
	"github.com/sapcc/go-bits/easypg"

	"github.com/sapcc/castellum/internal/db"
	"github.com/sapcc/castellum/internal/plugins"
	"github.com/sapcc/castellum/internal/test"
)

func TestResourceScraping(baseT *testing.T) {
	t := test.T{T: baseT}
	ctx := t.Context()
	s := test.NewSetup(t.T,
		commonSetupOptionsForWorkerTest(),
	)
	withContext(s, func() {
		job := s.TaskContext.ResourceScrapingJob(s.Registry)

		// ScrapeNextResource() without any resources just does nothing
		err := job.ProcessOne(ctx)
		if !errors.Is(err, sql.ErrNoRows) {
			t.Errorf("expected sql.ErrNoRows, got %s instead", err.Error())
		}
		tr, tr0 := easypg.NewTracker(t.T, s.DB.Db)
		tr0.AssertEmpty()

		// create some project resources for testing
		t.Must(s.DB.Insert(&db.Resource{
			ScopeUUID:                "project1",
			DomainUUID:               "domain1",
			AssetType:                "foo",
			LowThresholdPercent:      castellum.UsageValues{castellum.SingularUsageMetric: 0},
			HighThresholdPercent:     castellum.UsageValues{castellum.SingularUsageMetric: 0},
			CriticalThresholdPercent: castellum.UsageValues{castellum.SingularUsageMetric: 0},
			NextScrapeAt:             s.Clock.Now(),
		}))
		t.Must(s.DB.Insert(&db.Resource{
			ScopeUUID:                "project3",
			DomainUUID:               "domain1",
			AssetType:                "foo",
			LowThresholdPercent:      castellum.UsageValues{castellum.SingularUsageMetric: 0},
			HighThresholdPercent:     castellum.UsageValues{castellum.SingularUsageMetric: 0},
			CriticalThresholdPercent: castellum.UsageValues{castellum.SingularUsageMetric: 0},
			NextScrapeAt:             s.Clock.Now(),
		}))

		// create some mock assets that ScrapeNextResource() can find
		amStatic := s.ManagerForAssetType("foo")
		amStatic.Assets = map[string]map[string]plugins.StaticAsset{
			"project1": {
				"asset1": {Size: 1000, Usage: 400},
				"asset2": {Size: 2000, Usage: 1000},
			},
			"project3": {
				"asset5": {Size: 5000, Usage: 2500},
				"asset6": {Size: 6000, Usage: 2520},
			},
		}
		tr.DBChanges().Ignore()

		// first ScrapeNextResource() should scrape project1/foo
		s.Clock.StepBy(time.Hour)
		t.Must(job.ProcessOne(ctx))
		tr.DBChanges().AssertEqualf(`
				INSERT INTO assets (id, resource_id, uuid, size, usage, next_scrape_at, never_scraped) VALUES (1, 1, 'asset1', 0, '{"singular":0}', %[1]d, TRUE);
				INSERT INTO assets (id, resource_id, uuid, size, usage, next_scrape_at, never_scraped) VALUES (2, 1, 'asset2', 0, '{"singular":0}', %[1]d, TRUE);
				UPDATE resources SET next_scrape_at = %[2]d WHERE id = 1 AND scope_uuid = 'project1' AND asset_type = 'foo';
			`,
			s.Clock.Now().Unix(),
			s.Clock.Now().Add(30*time.Minute).Unix(),
		)

		// first ScrapeNextResource() should scrape project3/foo
		s.Clock.StepBy(time.Hour)
		t.Must(job.ProcessOne(ctx))
		tr.DBChanges().AssertEqualf(`
				INSERT INTO assets (id, resource_id, uuid, size, usage, next_scrape_at, never_scraped) VALUES (3, 2, 'asset5', 0, '{"singular":0}', %[1]d, TRUE);
				INSERT INTO assets (id, resource_id, uuid, size, usage, next_scrape_at, never_scraped) VALUES (4, 2, 'asset6', 0, '{"singular":0}', %[1]d, TRUE);
				UPDATE resources SET next_scrape_at = %[2]d WHERE id = 2 AND scope_uuid = 'project3' AND asset_type = 'foo';
			`,
			s.Clock.Now().Unix(),
			s.Clock.Now().Add(30*time.Minute).Unix(),
		)

		// next ScrapeNextResource() should scrape project1/foo again because its
		// next_scrape_at timestamp is the smallest; there should be no changes except for
		// resources.next_scrape_at
		s.Clock.StepBy(time.Hour)
		t.Must(job.ProcessOne(ctx))
		tr.DBChanges().AssertEqualf(`
				UPDATE resources SET next_scrape_at = %d WHERE id = 1 AND scope_uuid = 'project1' AND asset_type = 'foo';
			`,
			s.Clock.Now().Add(30*time.Minute).Unix(),
		)

		// simulate deletion of an asset
		delete(amStatic.Assets["project3"], "asset6")
		s.Clock.StepBy(time.Hour)
		t.Must(job.ProcessOne(ctx))
		tr.DBChanges().AssertEqualf(`
				DELETE FROM assets WHERE id = 4 AND resource_id = 2 AND uuid = 'asset6';
				UPDATE resources SET next_scrape_at = %d WHERE id = 2 AND scope_uuid = 'project3' AND asset_type = 'foo';
			`,
			s.Clock.Now().Add(30*time.Minute).Unix(),
		)

		// simulate addition of a new asset
		amStatic.Assets["project1"]["asset7"] = plugins.StaticAsset{Size: 10, Usage: 3}
		t.Must(job.ProcessOne(ctx))
		tr.DBChanges().AssertEqualf(`
				INSERT INTO assets (id, resource_id, uuid, size, usage, next_scrape_at, never_scraped) VALUES (5, 1, 'asset7', 0, '{"singular":0}', %[1]d, TRUE);
				UPDATE resources SET next_scrape_at = %[2]d WHERE id = 1 AND scope_uuid = 'project1' AND asset_type = 'foo';
			`,
			s.Clock.Now().Unix(),
			s.Clock.Now().Add(30*time.Minute).Unix(),
		)

		// check behavior on a resource without assets
		t.Must(s.DB.Insert(&db.Resource{
			ScopeUUID:    "project2",
			DomainUUID:   "domain1",
			AssetType:    "foo",
			NextScrapeAt: s.Clock.Now(),
		}))
		amStatic.Assets["project2"] = nil
		t.Must(job.ProcessOne(ctx))
		tr.DBChanges().AssertEqualf(`
				INSERT INTO resources (id, scope_uuid, asset_type, low_threshold_percent, low_delay_seconds, high_threshold_percent, high_delay_seconds, critical_threshold_percent, size_step_percent, domain_uuid, next_scrape_at) VALUES (3, 'project2', 'foo', '{"singular":0}', 0, '{"singular":0}', 0, '{"singular":0}', 0, 'domain1', %d);
			`,
			s.Clock.Now().Add(30*time.Minute).Unix(),
		)
	})
}
