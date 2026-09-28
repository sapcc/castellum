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
	"github.com/sapcc/go-bits/must"

	"github.com/sapcc/castellum/internal/db"
	"github.com/sapcc/castellum/internal/plugins"
	"github.com/sapcc/castellum/internal/test"
)

func TestResourceScraping(t *testing.T) {
	ctx := t.Context()
	s := test.NewSetup(t,
		commonSetupOptionsForWorkerTest(),
	)
	job := s.TaskContext.ResourceScrapingJob(s.Registry)

	// ScrapeNextResource() without any resources just does nothing
	err := job.ProcessOne(ctx)
	if !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("expected sql.ErrNoRows, got %s instead", err.Error())
	}
	tr, tr0 := easypg.NewTracker(t, s.DB.DB)
	tr0.AssertEmpty()

	// create some project resource groups + resources for testing
	must.SucceedT(t, db.ResourceGroupStore.Insert(ctx, s.DB, &db.ResourceGroup{
		ScopeUUID:    "project1",
		DomainUUID:   "domain1",
		AssetManager: "static",
		NextScrapeAt: s.Clock.Now(),
	}))
	must.SucceedT(t, db.ResourceStore.Insert(ctx, s.DB, &db.Resource{
		ResourceGroupID:          1,
		AssetType:                "foo",
		LowThresholdPercent:      castellum.UsageValues{castellum.SingularUsageMetric: 0},
		HighThresholdPercent:     castellum.UsageValues{castellum.SingularUsageMetric: 0},
		CriticalThresholdPercent: castellum.UsageValues{castellum.SingularUsageMetric: 0},
	}))
	must.SucceedT(t, db.ResourceGroupStore.Insert(ctx, s.DB, &db.ResourceGroup{
		ScopeUUID:    "project3",
		DomainUUID:   "domain1",
		AssetManager: "static",
		NextScrapeAt: s.Clock.Now(),
	}))
	must.SucceedT(t, db.ResourceStore.Insert(ctx, s.DB, &db.Resource{
		ResourceGroupID:          2,
		AssetType:                "foo",
		LowThresholdPercent:      castellum.UsageValues{castellum.SingularUsageMetric: 0},
		HighThresholdPercent:     castellum.UsageValues{castellum.SingularUsageMetric: 0},
		CriticalThresholdPercent: castellum.UsageValues{castellum.SingularUsageMetric: 0},
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
	must.SucceedT(t, job.ProcessOne(ctx))
	tr.DBChanges().AssertEqualf(`
			INSERT INTO assets (id, resource_id, uuid, size, usage, next_scrape_at, never_scraped) VALUES (1, 1, 'asset1', 0, '{"singular":0}', %[1]d, TRUE);
			INSERT INTO assets (id, resource_id, uuid, size, usage, next_scrape_at, never_scraped) VALUES (2, 1, 'asset2', 0, '{"singular":0}', %[1]d, TRUE);
			UPDATE resource_groups SET next_scrape_at = %[2]d WHERE id = 1 AND scope_uuid = 'project1' AND asset_manager = 'static';
		`,
		s.Clock.Now().Unix(),
		s.Clock.Now().Add(30*time.Minute).Unix(),
	)

	// first ScrapeNextResource() should scrape project3/foo
	s.Clock.StepBy(time.Hour)
	must.SucceedT(t, job.ProcessOne(ctx))
	tr.DBChanges().AssertEqualf(`
			INSERT INTO assets (id, resource_id, uuid, size, usage, next_scrape_at, never_scraped) VALUES (3, 2, 'asset5', 0, '{"singular":0}', %[1]d, TRUE);
			INSERT INTO assets (id, resource_id, uuid, size, usage, next_scrape_at, never_scraped) VALUES (4, 2, 'asset6', 0, '{"singular":0}', %[1]d, TRUE);
			UPDATE resource_groups SET next_scrape_at = %[2]d WHERE id = 2 AND scope_uuid = 'project3' AND asset_manager = 'static';
		`,
		s.Clock.Now().Unix(),
		s.Clock.Now().Add(30*time.Minute).Unix(),
	)

	// next ScrapeNextResource() should scrape project1/foo again because its
	// next_scrape_at timestamp is the smallest; there should be no changes except for
	// resource_groups.next_scrape_at
	s.Clock.StepBy(time.Hour)
	must.SucceedT(t, job.ProcessOne(ctx))
	tr.DBChanges().AssertEqualf(`
			UPDATE resource_groups SET next_scrape_at = %d WHERE id = 1 AND scope_uuid = 'project1' AND asset_manager = 'static';
		`,
		s.Clock.Now().Add(30*time.Minute).Unix(),
	)

	// simulate deletion of an asset
	delete(amStatic.Assets["project3"], "asset6")
	s.Clock.StepBy(time.Hour)
	must.SucceedT(t, job.ProcessOne(ctx))
	tr.DBChanges().AssertEqualf(`
			DELETE FROM assets WHERE id = 4 AND resource_id = 2 AND uuid = 'asset6';
			UPDATE resource_groups SET next_scrape_at = %d WHERE id = 2 AND scope_uuid = 'project3' AND asset_manager = 'static';
		`,
		s.Clock.Now().Add(30*time.Minute).Unix(),
	)

	// simulate addition of a new asset
	amStatic.Assets["project1"]["asset7"] = plugins.StaticAsset{Size: 10, Usage: 3}
	must.SucceedT(t, job.ProcessOne(ctx))
	tr.DBChanges().AssertEqualf(`
			INSERT INTO assets (id, resource_id, uuid, size, usage, next_scrape_at, never_scraped) VALUES (5, 1, 'asset7', 0, '{"singular":0}', %[1]d, TRUE);
			UPDATE resource_groups SET next_scrape_at = %[2]d WHERE id = 1 AND scope_uuid = 'project1' AND asset_manager = 'static';
		`,
		s.Clock.Now().Unix(),
		s.Clock.Now().Add(30*time.Minute).Unix(),
	)

	// check behavior on a resource without assets
	must.SucceedT(t, db.ResourceGroupStore.Insert(ctx, s.DB, &db.ResourceGroup{
		ScopeUUID:    "project2",
		DomainUUID:   "domain1",
		AssetManager: "static",
		NextScrapeAt: s.Clock.Now(),
	}))
	must.SucceedT(t, db.ResourceStore.Insert(ctx, s.DB, &db.Resource{
		ResourceGroupID: 3,
		AssetType:       "foo",
	}))
	amStatic.Assets["project2"] = nil
	must.SucceedT(t, job.ProcessOne(ctx))
	tr.DBChanges().AssertEqualf(`
			INSERT INTO resource_groups (id, scope_uuid, domain_uuid, asset_manager, next_scrape_at) VALUES (3, 'project2', 'domain1', 'static', %[1]d);
			INSERT INTO resources (id, asset_type, low_threshold_percent, low_delay_seconds, high_threshold_percent, high_delay_seconds, critical_threshold_percent, size_step_percent, resource_group_id) VALUES (3, 'foo', 'null', 0, 'null', 0, 'null', 0, 3);
		`,
		s.Clock.Now().Add(30*time.Minute).Unix(),
	)
}

// TestResourceScrapingMultipleTypesInGroup verifies that a single resource group containing two asset types is scraped in one pass
// and that when one manager fails the group error is recorded while assets of the successful manager stay correct.
func TestResourceScrapingMultipleTypesInGroup(t *testing.T) {
	ctx := t.Context()
	s := test.NewSetup(t,
		commonSetupOptionsForWorkerTest(),
	)
	job := s.TaskContext.ResourceScrapingJob(s.Registry)

	// single group with two resources of different asset types
	must.SucceedT(t, db.ResourceGroupStore.Insert(ctx, s.DB, &db.ResourceGroup{
		ScopeUUID:    "project1",
		DomainUUID:   "domain1",
		AssetManager: "static",
		NextScrapeAt: s.Clock.Now(),
	}))
	must.SucceedT(t, db.ResourceStore.Insert(ctx, s.DB, &db.Resource{
		ResourceGroupID:          1,
		AssetType:                "foo",
		LowThresholdPercent:      castellum.UsageValues{castellum.SingularUsageMetric: 0},
		HighThresholdPercent:     castellum.UsageValues{castellum.SingularUsageMetric: 0},
		CriticalThresholdPercent: castellum.UsageValues{castellum.SingularUsageMetric: 0},
	}))
	must.SucceedT(t, db.ResourceStore.Insert(ctx, s.DB, &db.Resource{
		ResourceGroupID:          1,
		AssetType:                "bar",
		LowThresholdPercent:      castellum.UsageValues{castellum.SingularUsageMetric: 0},
		HighThresholdPercent:     castellum.UsageValues{castellum.SingularUsageMetric: 0},
		CriticalThresholdPercent: castellum.UsageValues{castellum.SingularUsageMetric: 0},
	}))

	amFoo := s.ManagerForAssetType("foo")
	amFoo.Assets = map[string]map[string]plugins.StaticAsset{
		"project1": {
			"fooasset1": {Size: 1000, Usage: 400},
		},
	}
	amBar := s.ManagerForAssetType("bar")
	amBar.Assets = map[string]map[string]plugins.StaticAsset{
		"project1": {
			"barasset1": {Size: 2000, Usage: 800},
		},
	}
	tr, _ := easypg.NewTracker(t, s.DB.DB)

	// assets for both types should be discovered in one scrape
	s.Clock.StepBy(time.Hour)
	must.SucceedT(t, job.ProcessOne(ctx))
	tr.DBChanges().AssertEqualf(`
		INSERT INTO assets (id, resource_id, uuid, size, usage, next_scrape_at, never_scraped) VALUES (1, 1, 'fooasset1', 0, '{"singular":0}', %[1]d, TRUE);
		INSERT INTO assets (id, resource_id, uuid, size, usage, next_scrape_at, never_scraped) VALUES (2, 2, 'barasset1', 0, '{"singular":0}', %[1]d, TRUE);
		UPDATE resource_groups SET next_scrape_at = %[2]d WHERE id = 1 AND scope_uuid = 'project1' AND asset_manager = 'static';
	`, s.Clock.Now().Unix(), s.Clock.Now().Add(30*time.Minute).Unix())

	// Simulate the "foo" manager failing for this project
	delete(amFoo.Assets, "project1")
	s.Clock.StepBy(time.Hour)
	err := job.ProcessOne(ctx)
	if err == nil {
		t.Fatal("expected error from failing foo manager, got nil")
	}
	tr.DBChanges().AssertEqualf(`
		UPDATE resource_groups SET next_scrape_at = %d, scrape_error_message = 'cannot list foo assets in scope project1: no such project' WHERE id = 1 AND scope_uuid = 'project1' AND asset_manager = 'static';
	`, s.Clock.Now().Add(30*time.Minute).Unix())
}
