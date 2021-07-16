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

	"github.com/sapcc/castellum/internal/db"
	"github.com/sapcc/castellum/internal/plugins"
	"github.com/sapcc/castellum/internal/test"
	"github.com/sapcc/go-bits/easypg"
)

func TestResourceScraping(baseT *testing.T) {
	t := test.T{T: baseT}
	withContext(t, func(c *Context, amStatic *plugins.AssetManagerStatic, clock *test.FakeClock) {

		//ScrapeNextResource() without any resources just does nothing
		err := ExecuteOne(c.PollForResourceScrapes(0))
		if err != sql.ErrNoRows {
			t.Errorf("expected sql.ErrNoRows, got %s instead", err.Error())
		}
		_, dbDump := easypg.NewTracker(t.T, c.DB.Db)
		dbDump.AssertEmpty()

		//create some project resources for testing
		t.Must(c.DB.Insert(&db.Resource{
			ScopeUUID:                "project1",
			DomainUUID:               "domain1",
			AssetType:                "foo",
			CheckedAt:                c.TimeNow(),
			LowThresholdPercent:      db.UsageValues{db.SingularUsageMetric: 0},
			HighThresholdPercent:     db.UsageValues{db.SingularUsageMetric: 0},
			CriticalThresholdPercent: db.UsageValues{db.SingularUsageMetric: 0},
		}))
		t.Must(c.DB.Insert(&db.Resource{
			ScopeUUID:                "project3",
			DomainUUID:               "domain1",
			AssetType:                "foo",
			CheckedAt:                c.TimeNow(),
			LowThresholdPercent:      db.UsageValues{db.SingularUsageMetric: 0},
			HighThresholdPercent:     db.UsageValues{db.SingularUsageMetric: 0},
			CriticalThresholdPercent: db.UsageValues{db.SingularUsageMetric: 0},
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
		tr, _ := easypg.NewTracker(t.T, c.DB.Db)

		//first ScrapeNextResource() should scrape project1/foo
		clock.Step()
		t.Must(ExecuteOne(c.PollForResourceScrapes(0)))
		tr.DBChanges().AssertEqualf(`
				INSERT INTO assets (id, resource_id, uuid, size, scraped_at, expected_size, checked_at, scrape_error_message, usage, critical_usages) VALUES (1, 1, 'asset1', 1000, %[1]d, NULL, %[1]d, '', '{"singular":400}', '');
				INSERT INTO assets (id, resource_id, uuid, size, scraped_at, expected_size, checked_at, scrape_error_message, usage, critical_usages) VALUES (2, 1, 'asset2', 2000, %[1]d, NULL, %[1]d, '', '{"singular":1000}', '');
				UPDATE resources SET scraped_at = %[1]d, checked_at = %[1]d WHERE id = 1 AND scope_uuid = 'project1' AND asset_type = 'foo';
			`,
			c.TimeNow().Unix(),
		)

		//first ScrapeNextResource() should scrape project3/foo
		//(NOT project2 because its resource has a different asset type)
		clock.Step()
		t.Must(ExecuteOne(c.PollForResourceScrapes(0)))
		tr.DBChanges().AssertEqualf(`
				INSERT INTO assets (id, resource_id, uuid, size, scraped_at, expected_size, checked_at, scrape_error_message, usage, critical_usages) VALUES (3, 2, 'asset5', 5000, %[1]d, NULL, %[1]d, '', '{"singular":2500}', '');
				INSERT INTO assets (id, resource_id, uuid, size, scraped_at, expected_size, checked_at, scrape_error_message, usage, critical_usages) VALUES (4, 2, 'asset6', 6000, %[1]d, NULL, %[1]d, '', '{"singular":2520}', '');
				UPDATE resources SET scraped_at = %[1]d, checked_at = %[1]d WHERE id = 2 AND scope_uuid = 'project3' AND asset_type = 'foo';
			`,
			c.TimeNow().Unix(),
		)

		//next ScrapeNextResource() should scrape project1/foo again because its
		//scraped_at timestamp is the smallest; there should be no changes except for
		//resources.scraped_at and resource.checked_at
		clock.Step()
		t.Must(ExecuteOne(c.PollForResourceScrapes(0)))
		tr.DBChanges().AssertEqualf(`
				UPDATE resources SET scraped_at = %[1]d, checked_at = %[1]d WHERE id = 1 AND scope_uuid = 'project1' AND asset_type = 'foo';
			`,
			c.TimeNow().Unix(),
		)

		//simulate deletion of an asset
		delete(amStatic.Assets["project3"], "asset6")
		clock.Step()
		t.Must(ExecuteOne(c.PollForResourceScrapes(0)))
		tr.DBChanges().AssertEqualf(`
				DELETE FROM assets WHERE id = 4 AND resource_id = 2 AND uuid = 'asset6';
				UPDATE resources SET scraped_at = %[1]d, checked_at = %[1]d WHERE id = 2 AND scope_uuid = 'project3' AND asset_type = 'foo';
			`,
			c.TimeNow().Unix(),
		)

		//simulate addition of a new asset
		amStatic.Assets["project1"]["asset7"] = plugins.StaticAsset{Size: 10, Usage: 3}
		clock.Step()
		t.Must(ExecuteOne(c.PollForResourceScrapes(0)))
		tr.DBChanges().AssertEqualf(`
				INSERT INTO assets (id, resource_id, uuid, size, scraped_at, expected_size, checked_at, scrape_error_message, usage, critical_usages) VALUES (5, 1, 'asset7', 10, %[1]d, NULL, %[1]d, '', '{"singular":3}', '');
				UPDATE resources SET scraped_at = %[1]d, checked_at = %[1]d WHERE id = 1 AND scope_uuid = 'project1' AND asset_type = 'foo';
			`,
			c.TimeNow().Unix(),
		)

		//check behavior on a resource without assets
		t.Must(c.DB.Insert(&db.Resource{
			ScopeUUID:  "project2",
			DomainUUID: "domain1",
			AssetType:  "foo",
			CheckedAt:  c.TimeNow(),
		}))
		amStatic.Assets["project2"] = nil
		clock.Step()
		t.Must(ExecuteOne(c.PollForResourceScrapes(0)))
		tr.DBChanges().AssertEqualf(`
				INSERT INTO resources (id, scope_uuid, asset_type, scraped_at, low_threshold_percent, low_delay_seconds, high_threshold_percent, high_delay_seconds, critical_threshold_percent, size_step_percent, min_size, max_size, min_free_size, single_step, domain_uuid, checked_at, scrape_error_message, config_json) VALUES (3, 'project2', 'foo', %[1]d, '{"singular":0}', 0, '{"singular":0}', 0, '{"singular":0}', 0, NULL, NULL, NULL, FALSE, 'domain1', %[1]d, '', '');
			`,
			c.TimeNow().Unix(),
		)
	})
}
