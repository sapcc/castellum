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

	"github.com/sapcc/castellum/internal/db"
	"github.com/sapcc/castellum/internal/plugins"
	"github.com/sapcc/go-bits/postlite"
)

func setupAssetScrapeTest(t *testing.T) (*Context, func(uint64), *FakeClock) {
	c, amStatic, clock := setupContext(t)

	//ScrapeNextAsset() without any resources just does nothing
	err := c.ScrapeNextAsset("foo", c.TimeNow())
	if err != sql.ErrNoRows {
		t.Errorf("expected sql.ErrNoRows, got %s instead", err.Error())
	}
	postlite.AssertDBContent(t, c.DB.Db, "fixtures/resource-scrape-0.sql")

	//create a resource and asset to test with
	must(t, c.DB.Insert(&db.Resource{
		ScopeUUID:                "project1",
		AssetType:                "foo",
		LowThresholdPercent:      20,
		LowDelaySeconds:          3600,
		HighThresholdPercent:     80,
		HighDelaySeconds:         3600,
		CriticalThresholdPercent: 95,
		SizeStepPercent:          20,
	}))
	must(t, c.DB.Insert(&db.Asset{
		ResourceID:   1,
		UUID:         "asset1",
		Size:         1000,
		UsagePercent: 50,
		ScrapedAt:    c.TimeNow(),
		Stale:        false,
	}))

	//setup asset with configurable size
	amStatic.Assets = map[string]map[string]plugins.StaticAsset{
		"project1": {
			"asset1": {Size: 1000, Usage: 500},
		},
	}
	setAssetUsagePercent := func(percent uint64) {
		amStatic.Assets["project1"]["asset1"] = plugins.StaticAsset{
			Size:  1000,
			Usage: percent * 10,
		}
	}

	return c, setAssetUsagePercent, clock
}

func TestNoOperationWhenNoThreshold(t *testing.T) {
	c, _, clock := setupAssetScrapeTest(t)

	//when no threshold is crossed, no operation gets created
	clock.StepBy(10 * time.Minute)
	must(t, c.ScrapeNextAsset("foo", c.TimeNow()))
	expectPendingOperations(t, c.DB /*, nothing */)
	expectFinishedOperations(t, c.DB /*, nothing */)
}

func TestNormalUpsizeTowardsGreenlight(t *testing.T) {
	c, setAssetUsagePercent, clock := setupAssetScrapeTest(t)

	//when the "High" threshold gets crossed, a "High" operation gets created in
	//state "created"
	clock.StepBy(10 * time.Minute)
	setAssetUsagePercent(80)
	must(t, c.ScrapeNextAsset("foo", c.TimeNow()))

	expectedOp := db.PendingOperation{
		ID:           1,
		AssetID:      1,
		Reason:       db.OperationReasonHigh,
		OldSize:      1000,
		NewSize:      1200,
		UsagePercent: 80,
		CreatedAt:    c.TimeNow(),
	}
	expectPendingOperations(t, c.DB, expectedOp)
	expectFinishedOperations(t, c.DB /*, nothing */)

	//another scrape while the delay is not over should not change the state
	clock.StepBy(40 * time.Minute)
	setAssetUsagePercent(82)
	must(t, c.ScrapeNextAsset("foo", c.TimeNow()))
	expectPendingOperations(t, c.DB, expectedOp)
	expectFinishedOperations(t, c.DB /*, nothing */)

	//when the delay is over, the next scrape moves into state "Confirmed/Greenlit"
	clock.StepBy(40 * time.Minute)
	setAssetUsagePercent(84)
	must(t, c.ScrapeNextAsset("foo", c.TimeNow()))
	expectedOp.ConfirmedAt = p2time(c.TimeNow())
	expectedOp.GreenlitAt = p2time(c.TimeNow())
	expectPendingOperations(t, c.DB, expectedOp)
	expectFinishedOperations(t, c.DB /*, nothing */)

	//since the operation is now greenlit and can be picked up by a worker at any
	//moment, we should not touch it anymore even if the reason disappears
	clock.StepBy(40 * time.Minute)
	setAssetUsagePercent(78)
	must(t, c.ScrapeNextAsset("foo", c.TimeNow()))
	expectPendingOperations(t, c.DB, expectedOp)
	expectFinishedOperations(t, c.DB /*, nothing */)
}

func TestNormalUpsizeTowardsCancel(t *testing.T) {
	c, setAssetUsagePercent, clock := setupAssetScrapeTest(t)

	//when the "High" threshold gets crossed, a "High" operation gets created in
	//state "created"
	clock.StepBy(10 * time.Minute)
	setAssetUsagePercent(80)
	must(t, c.ScrapeNextAsset("foo", c.TimeNow()))

	expectPendingOperations(t, c.DB, db.PendingOperation{
		ID:           1,
		AssetID:      1,
		Reason:       db.OperationReasonHigh,
		OldSize:      1000,
		NewSize:      1200,
		UsagePercent: 80,
		CreatedAt:    c.TimeNow(),
	})
	expectFinishedOperations(t, c.DB /*, nothing */)

	//when the reason disappears within the delay, the operation is cancelled
	clock.StepBy(40 * time.Minute)
	setAssetUsagePercent(79)
	must(t, c.ScrapeNextAsset("foo", c.TimeNow()))

	expectPendingOperations(t, c.DB /*, nothing */)
	expectFinishedOperations(t, c.DB, db.FinishedOperation{
		AssetID:      1,
		Reason:       db.OperationReasonHigh,
		Outcome:      db.OperationOutcomeCancelled,
		OldSize:      1000,
		NewSize:      1200,
		UsagePercent: 80,
		CreatedAt:    c.TimeNow().Add(-40 * time.Minute),
		FinishedAt:   c.TimeNow(),
	})
}

func TestNormalDownsizeTowardsGreenlight(t *testing.T) {
	c, setAssetUsagePercent, clock := setupAssetScrapeTest(t)

	//when the "Low" threshold gets crossed, a "Low" operation gets created in
	//state "created"
	clock.StepBy(10 * time.Minute)
	setAssetUsagePercent(20)
	must(t, c.ScrapeNextAsset("foo", c.TimeNow()))

	expectedOp := db.PendingOperation{
		ID:           1,
		AssetID:      1,
		Reason:       db.OperationReasonLow,
		OldSize:      1000,
		NewSize:      800,
		UsagePercent: 20,
		CreatedAt:    c.TimeNow(),
	}
	expectPendingOperations(t, c.DB, expectedOp)
	expectFinishedOperations(t, c.DB /*, nothing */)

	//another scrape while the delay is not over should not change the state
	clock.StepBy(40 * time.Minute)
	setAssetUsagePercent(18)
	must(t, c.ScrapeNextAsset("foo", c.TimeNow()))
	expectPendingOperations(t, c.DB, expectedOp)
	expectFinishedOperations(t, c.DB /*, nothing */)

	//when the delay is over, the next scrape moves into state "Confirmed/Greenlit"
	clock.StepBy(40 * time.Minute)
	setAssetUsagePercent(16)
	must(t, c.ScrapeNextAsset("foo", c.TimeNow()))
	expectedOp.ConfirmedAt = p2time(c.TimeNow())
	expectedOp.GreenlitAt = p2time(c.TimeNow())
	expectPendingOperations(t, c.DB, expectedOp)
	expectFinishedOperations(t, c.DB /*, nothing */)

	//since the operation is now greenlit and can be picked up by a worker at any
	//moment, we should not touch it anymore even if the reason disappears
	clock.StepBy(40 * time.Minute)
	setAssetUsagePercent(22)
	must(t, c.ScrapeNextAsset("foo", c.TimeNow()))
	expectPendingOperations(t, c.DB, expectedOp)
	expectFinishedOperations(t, c.DB /*, nothing */)
}

func TestNormalDownsizeTowardsCancel(t *testing.T) {
	c, setAssetUsagePercent, clock := setupAssetScrapeTest(t)

	//when the "Low" threshold gets crossed, a "Low" operation gets created in
	//state "created"
	clock.StepBy(10 * time.Minute)
	setAssetUsagePercent(20)
	must(t, c.ScrapeNextAsset("foo", c.TimeNow()))

	expectPendingOperations(t, c.DB, db.PendingOperation{
		ID:           1,
		AssetID:      1,
		Reason:       db.OperationReasonLow,
		OldSize:      1000,
		NewSize:      800,
		UsagePercent: 20,
		CreatedAt:    c.TimeNow(),
	})
	expectFinishedOperations(t, c.DB /*, nothing */)

	//when the reason disappears within the delay, the operation is cancelled
	clock.StepBy(40 * time.Minute)
	setAssetUsagePercent(21)
	must(t, c.ScrapeNextAsset("foo", c.TimeNow()))

	expectPendingOperations(t, c.DB /*, nothing */)
	expectFinishedOperations(t, c.DB, db.FinishedOperation{
		AssetID:      1,
		Reason:       db.OperationReasonLow,
		Outcome:      db.OperationOutcomeCancelled,
		OldSize:      1000,
		NewSize:      800,
		UsagePercent: 20,
		CreatedAt:    c.TimeNow().Add(-40 * time.Minute),
		FinishedAt:   c.TimeNow(),
	})
}

func TestCriticalUpsizeTowardsGreenlight(t *testing.T) {
	c, setAssetUsagePercent, clock := setupAssetScrapeTest(t)

	//when the "Critical" threshold gets crossed, a "Critical" operation gets
	//created and immediately confirmed/greenlit
	clock.StepBy(10 * time.Minute)
	setAssetUsagePercent(95)
	must(t, c.ScrapeNextAsset("foo", c.TimeNow()))

	expectedOp := db.PendingOperation{
		ID:           1,
		AssetID:      1,
		Reason:       db.OperationReasonCritical,
		OldSize:      1000,
		NewSize:      1200,
		UsagePercent: 95,
		CreatedAt:    c.TimeNow(),
		ConfirmedAt:  p2time(c.TimeNow()),
		GreenlitAt:   p2time(c.TimeNow()),
	}
	expectPendingOperations(t, c.DB, expectedOp)
	expectFinishedOperations(t, c.DB /*, nothing */)
}

func TestReplaceNormalWithCriticalUpsize(t *testing.T) {
	c, setAssetUsagePercent, clock := setupAssetScrapeTest(t)

	//when the "High" threshold gets crossed, a "High" operation gets created in
	//state "created"
	clock.StepBy(10 * time.Minute)
	setAssetUsagePercent(90)
	must(t, c.ScrapeNextAsset("foo", c.TimeNow()))

	expectPendingOperations(t, c.DB, db.PendingOperation{
		ID:           1,
		AssetID:      1,
		Reason:       db.OperationReasonHigh,
		OldSize:      1000,
		NewSize:      1200,
		UsagePercent: 90,
		CreatedAt:    c.TimeNow(),
	})
	expectFinishedOperations(t, c.DB /*, nothing */)

	//when the "Critical" threshold gets crossed while the the "High" operation
	//is not yet confirmed, the "High" operation is cancelled and a "Critical"
	//operation replaces it
	clock.StepBy(10 * time.Minute)
	setAssetUsagePercent(96)
	must(t, c.ScrapeNextAsset("foo", c.TimeNow()))

	expectPendingOperations(t, c.DB, db.PendingOperation{
		ID:           2,
		AssetID:      1,
		Reason:       db.OperationReasonCritical,
		OldSize:      1000,
		NewSize:      1200,
		UsagePercent: 96,
		CreatedAt:    c.TimeNow(),
		ConfirmedAt:  p2time(c.TimeNow()),
		GreenlitAt:   p2time(c.TimeNow()),
	})
	expectFinishedOperations(t, c.DB, db.FinishedOperation{
		AssetID:      1,
		Reason:       db.OperationReasonHigh,
		Outcome:      db.OperationOutcomeCancelled,
		OldSize:      1000,
		NewSize:      1200,
		UsagePercent: 90,
		CreatedAt:    c.TimeNow().Add(-10 * time.Minute),
		FinishedAt:   c.TimeNow(),
	})
}

func TestAssetScrapeOrdering(t *testing.T) {
	c, amStatic, clock := setupContext(t)

	//create a resource and multiple assets to test with
	must(t, c.DB.Insert(&db.Resource{
		ScopeUUID:                "project1",
		AssetType:                "foo",
		LowThresholdPercent:      20,
		LowDelaySeconds:          3600,
		HighThresholdPercent:     80,
		HighDelaySeconds:         3600,
		CriticalThresholdPercent: 95,
		SizeStepPercent:          20,
	}))
	assets := []db.Asset{
		{
			ResourceID:   1,
			UUID:         "asset1",
			Size:         1000,
			UsagePercent: 50,
			ScrapedAt:    c.TimeNow(),
			Stale:        false,
		},
		{
			ResourceID:   1,
			UUID:         "asset2",
			Size:         1000,
			UsagePercent: 50,
			ScrapedAt:    c.TimeNow(),
			Stale:        false,
		},
		{
			ResourceID:   1,
			UUID:         "asset3",
			Size:         1000,
			UsagePercent: 50,
			ScrapedAt:    c.TimeNow(),
			Stale:        false,
		},
	}
	must(t, c.DB.Insert(&assets[0]))
	must(t, c.DB.Insert(&assets[1]))
	must(t, c.DB.Insert(&assets[2]))

	amStatic.Assets = map[string]map[string]plugins.StaticAsset{
		"project1": {
			"asset1": {Size: 1000, Usage: 510},
			"asset2": {Size: 1000, Usage: 520},
			"asset3": {Size: 1000, Usage: 530},
		},
	}

	//this should scrape each asset once, in order
	clock.StepBy(time.Minute)
	must(t, c.ScrapeNextAsset("foo", c.TimeNow()))
	clock.StepBy(time.Minute)
	must(t, c.ScrapeNextAsset("foo", c.TimeNow()))
	clock.StepBy(time.Minute)
	must(t, c.ScrapeNextAsset("foo", c.TimeNow()))

	//so the asset table should look like this now
	assets[0].ScrapedAt = c.TimeNow().Add(-2 * time.Minute)
	assets[1].ScrapedAt = c.TimeNow().Add(-time.Minute)
	assets[2].ScrapedAt = c.TimeNow()
	assets[0].UsagePercent = 51
	assets[1].UsagePercent = 52
	assets[2].UsagePercent = 53
	expectAssets(t, c.DB, assets...)

	//next scrape should work identically
	clock.StepBy(time.Minute)
	must(t, c.ScrapeNextAsset("foo", c.TimeNow()))
	clock.StepBy(time.Minute)
	must(t, c.ScrapeNextAsset("foo", c.TimeNow()))
	clock.StepBy(time.Minute)
	must(t, c.ScrapeNextAsset("foo", c.TimeNow()))
	assets[0].ScrapedAt = c.TimeNow().Add(-2 * time.Minute)
	assets[1].ScrapedAt = c.TimeNow().Add(-time.Minute)
	assets[2].ScrapedAt = c.TimeNow()
	expectAssets(t, c.DB, assets...)

	//and all of this should not have created any operations
	expectPendingOperations(t, c.DB /*, nothing */)
	expectFinishedOperations(t, c.DB /*, nothing */)
}
