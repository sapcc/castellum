/******************************************************************************
*
*  Copyright 2019 SAP SE
*
*  Licensed under the Apache License, Version 2.0 (the "License");
*  you may not use this file except in compliance with the License.
*  You may obtain a copy of the License at
*
*      http://www.apache.org/licenses/LICENSE-2.0
*
*  Unless required by applicable law or agreed to in writing, software
*  distributed under the License is distributed on an "AS IS" BASIS,
*  WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
*  See the License for the specific language governing permissions and
*  limitations under the License.
*
******************************************************************************/

package tasks

import (
	"database/sql"
	"testing"
	"time"

	"github.com/sapcc/go-bits/easypg"

	"github.com/sapcc/castellum/internal/db"
	"github.com/sapcc/castellum/internal/plugins"
	"github.com/sapcc/castellum/internal/test"
)

func TestResourceScraping(baseT *testing.T) {
	t := test.T{T: baseT}
	withContext(t, func(c *Context, amStatic *plugins.AssetManagerStatic, clock *test.FakeClock) {
		//ScrapeNextResource() without any resources just does nothing
		err := ExecuteOne(c.PollForResourceScrapes())
		if err != sql.ErrNoRows {
			t.Errorf("expected sql.ErrNoRows, got %s instead", err.Error())
		}
		tr, tr0 := easypg.NewTracker(t.T, c.DB.Db)
		tr0.AssertEmpty()

		//create some project resources for testing
		t.Must(c.DB.Insert(&db.Resource{
			ScopeUUID:                "project1",
			DomainUUID:               "domain1",
			AssetType:                "foo",
			LowThresholdPercent:      db.UsageValues{db.SingularUsageMetric: 0},
			HighThresholdPercent:     db.UsageValues{db.SingularUsageMetric: 0},
			CriticalThresholdPercent: db.UsageValues{db.SingularUsageMetric: 0},
			NextScrapeAt:             c.TimeNow(),
		}))
		t.Must(c.DB.Insert(&db.Resource{
			ScopeUUID:                "project3",
			DomainUUID:               "domain1",
			AssetType:                "foo",
			LowThresholdPercent:      db.UsageValues{db.SingularUsageMetric: 0},
			HighThresholdPercent:     db.UsageValues{db.SingularUsageMetric: 0},
			CriticalThresholdPercent: db.UsageValues{db.SingularUsageMetric: 0},
			NextScrapeAt:             c.TimeNow(),
		}))

		//create some mock assets that ScrapeNextResource() can find
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

		//first ScrapeNextResource() should scrape project1/foo
		clock.StepBy(time.Hour)
		t.Must(ExecuteOne(c.PollForResourceScrapes()))
		tr.DBChanges().AssertEqualf(`
				INSERT INTO assets (id, resource_id, uuid, size, usage, next_scrape_at) VALUES (1, 1, 'asset1', 1000, '{"singular":400}', %[1]d);
				INSERT INTO assets (id, resource_id, uuid, size, usage, next_scrape_at) VALUES (2, 1, 'asset2', 2000, '{"singular":1000}', %[1]d);
				UPDATE resources SET next_scrape_at = %[2]d WHERE id = 1 AND scope_uuid = 'project1' AND asset_type = 'foo';
			`,
			c.TimeNow().Add(5*time.Minute).Unix(),
			c.TimeNow().Add(30*time.Minute).Unix(),
		)

		//first ScrapeNextResource() should scrape project3/foo
		clock.StepBy(time.Hour)
		t.Must(ExecuteOne(c.PollForResourceScrapes()))
		tr.DBChanges().AssertEqualf(`
				INSERT INTO assets (id, resource_id, uuid, size, usage, next_scrape_at) VALUES (3, 2, 'asset5', 5000, '{"singular":2500}', %[1]d);
				INSERT INTO assets (id, resource_id, uuid, size, usage, next_scrape_at) VALUES (4, 2, 'asset6', 6000, '{"singular":2520}', %[1]d);
				UPDATE resources SET next_scrape_at = %[2]d WHERE id = 2 AND scope_uuid = 'project3' AND asset_type = 'foo';
			`,
			c.TimeNow().Add(5*time.Minute).Unix(),
			c.TimeNow().Add(30*time.Minute).Unix(),
		)

		//next ScrapeNextResource() should scrape project1/foo again because its
		//next_scrape_at timestamp is the smallest; there should be no changes except for
		//resources.next_scrape_at
		clock.StepBy(time.Hour)
		t.Must(ExecuteOne(c.PollForResourceScrapes()))
		tr.DBChanges().AssertEqualf(`
				UPDATE resources SET next_scrape_at = %d WHERE id = 1 AND scope_uuid = 'project1' AND asset_type = 'foo';
			`,
			c.TimeNow().Add(30*time.Minute).Unix(),
		)

		//simulate deletion of an asset
		delete(amStatic.Assets["project3"], "asset6")
		clock.StepBy(time.Hour)
		t.Must(ExecuteOne(c.PollForResourceScrapes()))
		tr.DBChanges().AssertEqualf(`
				DELETE FROM assets WHERE id = 4 AND resource_id = 2 AND uuid = 'asset6';
				UPDATE resources SET next_scrape_at = %d WHERE id = 2 AND scope_uuid = 'project3' AND asset_type = 'foo';
			`,
			c.TimeNow().Add(30*time.Minute).Unix(),
		)

		//simulate addition of a new asset
		amStatic.Assets["project1"]["asset7"] = plugins.StaticAsset{Size: 10, Usage: 3}
		t.Must(ExecuteOne(c.PollForResourceScrapes()))
		tr.DBChanges().AssertEqualf(`
				INSERT INTO assets (id, resource_id, uuid, size, usage, next_scrape_at) VALUES (5, 1, 'asset7', 10, '{"singular":3}', %[1]d);
				UPDATE resources SET next_scrape_at = %[2]d WHERE id = 1 AND scope_uuid = 'project1' AND asset_type = 'foo';
			`,
			c.TimeNow().Add(5*time.Minute).Unix(),
			c.TimeNow().Add(30*time.Minute).Unix(),
		)

		//check behavior on a resource without assets
		t.Must(c.DB.Insert(&db.Resource{
			ScopeUUID:    "project2",
			DomainUUID:   "domain1",
			AssetType:    "foo",
			NextScrapeAt: c.TimeNow(),
		}))
		amStatic.Assets["project2"] = nil
		t.Must(ExecuteOne(c.PollForResourceScrapes()))
		tr.DBChanges().AssertEqualf(`
				INSERT INTO resources (id, scope_uuid, asset_type, low_threshold_percent, low_delay_seconds, high_threshold_percent, high_delay_seconds, critical_threshold_percent, size_step_percent, domain_uuid, next_scrape_at) VALUES (3, 'project2', 'foo', '{"singular":0}', 0, '{"singular":0}', 0, '{"singular":0}', 0, 'domain1', %d);
			`,
			c.TimeNow().Add(30*time.Minute).Unix(),
		)
	})
}
