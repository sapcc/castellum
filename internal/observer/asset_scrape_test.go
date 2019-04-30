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

package observer

import (
	"database/sql"
	"encoding/json"
	"testing"
	"time"

	"github.com/sapcc/castellum/internal/db"
	"github.com/sapcc/castellum/internal/plugins"
	"github.com/sapcc/go-bits/postlite"
	"gopkg.in/gorp.v2"
)

func setupAssetScrapeTest(t *testing.T) (*Observer, func(uint64), *FakeClock) {
	o, amStatic, clock := setupObserver(t)

	//ScrapeNextAsset() without any resources just does nothing
	err := o.ScrapeNextAsset("foo", o.TimeNow())
	if err != sql.ErrNoRows {
		t.Errorf("expected sql.ErrNoRows, got %s instead", err.Error())
	}
	postlite.AssertDBContent(t, o.DB.Db, "fixtures/resource-scrape-0.sql")

	//create a resource and asset to test with
	must(t, o.DB.Insert(&db.Resource{
		ScopeUUID:                "project1",
		AssetType:                "foo",
		LowThresholdPercent:      20,
		LowDelaySeconds:          3600,
		HighThresholdPercent:     80,
		HighDelaySeconds:         3600,
		CriticalThresholdPercent: 95,
		SizeStepPercent:          20,
	}))
	must(t, o.DB.Insert(&db.Asset{
		ResourceID:   1,
		UUID:         "asset1",
		Size:         1000,
		UsagePercent: 50,
		ScrapedAt:    o.TimeNow(),
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

	return o, setAssetUsagePercent, clock
}

func TestNoOperationWhenNoThreshold(t *testing.T) {
	o, _, clock := setupAssetScrapeTest(t)

	//when no threshold is crossed, no operation gets created
	clock.StepBy(10 * time.Minute)
	must(t, o.ScrapeNextAsset("foo", o.TimeNow()))
	expectPendingOperations(t, o.DB /*, nothing */)
	expectFinishedOperations(t, o.DB /*, nothing */)
}

func TestNormalUpsizeTowardsGreenlight(t *testing.T) {
	o, setAssetUsagePercent, clock := setupAssetScrapeTest(t)

	//when the "High" threshold gets crossed, a "High" operation gets created in
	//state "created"
	clock.StepBy(10 * time.Minute)
	setAssetUsagePercent(80)
	must(t, o.ScrapeNextAsset("foo", o.TimeNow()))

	expectedOp := db.PendingOperation{
		ID:           1,
		AssetID:      1,
		Reason:       db.OperationReasonHigh,
		OldSize:      1000,
		NewSize:      1200,
		UsagePercent: 80,
		CreatedAt:    o.TimeNow(),
	}
	expectPendingOperations(t, o.DB, expectedOp)
	expectFinishedOperations(t, o.DB /*, nothing */)

	//another scrape while the delay is not over should not change the state
	clock.StepBy(40 * time.Minute)
	setAssetUsagePercent(82)
	must(t, o.ScrapeNextAsset("foo", o.TimeNow()))
	expectPendingOperations(t, o.DB, expectedOp)
	expectFinishedOperations(t, o.DB /*, nothing */)

	//when the delay is over, the next scrape moves into state "Confirmed/Greenlit"
	clock.StepBy(40 * time.Minute)
	setAssetUsagePercent(84)
	must(t, o.ScrapeNextAsset("foo", o.TimeNow()))
	expectedOp.ConfirmedAt = p2time(o.TimeNow())
	expectedOp.GreenlitAt = p2time(o.TimeNow())
	expectPendingOperations(t, o.DB, expectedOp)
	expectFinishedOperations(t, o.DB /*, nothing */)

	//since the operation is now greenlit and can be picked up by a worker at any
	//moment, we should not touch it anymore even if the reason disappears
	clock.StepBy(40 * time.Minute)
	setAssetUsagePercent(78)
	must(t, o.ScrapeNextAsset("foo", o.TimeNow()))
	expectPendingOperations(t, o.DB, expectedOp)
	expectFinishedOperations(t, o.DB /*, nothing */)
}

func TestNormalUpsizeTowardsCancel(t *testing.T) {
	o, setAssetUsagePercent, clock := setupAssetScrapeTest(t)

	//when the "High" threshold gets crossed, a "High" operation gets created in
	//state "created"
	clock.StepBy(10 * time.Minute)
	setAssetUsagePercent(80)
	must(t, o.ScrapeNextAsset("foo", o.TimeNow()))

	expectPendingOperations(t, o.DB, db.PendingOperation{
		ID:           1,
		AssetID:      1,
		Reason:       db.OperationReasonHigh,
		OldSize:      1000,
		NewSize:      1200,
		UsagePercent: 80,
		CreatedAt:    o.TimeNow(),
	})
	expectFinishedOperations(t, o.DB /*, nothing */)

	//when the reason disappears within the delay, the operation is cancelled
	clock.StepBy(40 * time.Minute)
	setAssetUsagePercent(79)
	must(t, o.ScrapeNextAsset("foo", o.TimeNow()))

	expectPendingOperations(t, o.DB /*, nothing */)
	expectFinishedOperations(t, o.DB, db.FinishedOperation{
		AssetID:      1,
		Reason:       db.OperationReasonHigh,
		Outcome:      db.OperationOutcomeCancelled,
		OldSize:      1000,
		NewSize:      1200,
		UsagePercent: 80,
		CreatedAt:    o.TimeNow().Add(-40 * time.Minute),
		FinishedAt:   o.TimeNow(),
	})
}

func TestNormalDownsizeTowardsGreenlight(t *testing.T) {
	o, setAssetUsagePercent, clock := setupAssetScrapeTest(t)

	//when the "Low" threshold gets crossed, a "Low" operation gets created in
	//state "created"
	clock.StepBy(10 * time.Minute)
	setAssetUsagePercent(20)
	must(t, o.ScrapeNextAsset("foo", o.TimeNow()))

	expectedOp := db.PendingOperation{
		ID:           1,
		AssetID:      1,
		Reason:       db.OperationReasonLow,
		OldSize:      1000,
		NewSize:      800,
		UsagePercent: 20,
		CreatedAt:    o.TimeNow(),
	}
	expectPendingOperations(t, o.DB, expectedOp)
	expectFinishedOperations(t, o.DB /*, nothing */)

	//another scrape while the delay is not over should not change the state
	clock.StepBy(40 * time.Minute)
	setAssetUsagePercent(18)
	must(t, o.ScrapeNextAsset("foo", o.TimeNow()))
	expectPendingOperations(t, o.DB, expectedOp)
	expectFinishedOperations(t, o.DB /*, nothing */)

	//when the delay is over, the next scrape moves into state "Confirmed/Greenlit"
	clock.StepBy(40 * time.Minute)
	setAssetUsagePercent(16)
	must(t, o.ScrapeNextAsset("foo", o.TimeNow()))
	expectedOp.ConfirmedAt = p2time(o.TimeNow())
	expectedOp.GreenlitAt = p2time(o.TimeNow())
	expectPendingOperations(t, o.DB, expectedOp)
	expectFinishedOperations(t, o.DB /*, nothing */)

	//since the operation is now greenlit and can be picked up by a worker at any
	//moment, we should not touch it anymore even if the reason disappears
	clock.StepBy(40 * time.Minute)
	setAssetUsagePercent(22)
	must(t, o.ScrapeNextAsset("foo", o.TimeNow()))
	expectPendingOperations(t, o.DB, expectedOp)
	expectFinishedOperations(t, o.DB /*, nothing */)
}

func TestNormalDownsizeTowardsCancel(t *testing.T) {
	o, setAssetUsagePercent, clock := setupAssetScrapeTest(t)

	//when the "Low" threshold gets crossed, a "Low" operation gets created in
	//state "created"
	clock.StepBy(10 * time.Minute)
	setAssetUsagePercent(20)
	must(t, o.ScrapeNextAsset("foo", o.TimeNow()))

	expectPendingOperations(t, o.DB, db.PendingOperation{
		ID:           1,
		AssetID:      1,
		Reason:       db.OperationReasonLow,
		OldSize:      1000,
		NewSize:      800,
		UsagePercent: 20,
		CreatedAt:    o.TimeNow(),
	})
	expectFinishedOperations(t, o.DB /*, nothing */)

	//when the reason disappears within the delay, the operation is cancelled
	clock.StepBy(40 * time.Minute)
	setAssetUsagePercent(21)
	must(t, o.ScrapeNextAsset("foo", o.TimeNow()))

	expectPendingOperations(t, o.DB /*, nothing */)
	expectFinishedOperations(t, o.DB, db.FinishedOperation{
		AssetID:      1,
		Reason:       db.OperationReasonLow,
		Outcome:      db.OperationOutcomeCancelled,
		OldSize:      1000,
		NewSize:      800,
		UsagePercent: 20,
		CreatedAt:    o.TimeNow().Add(-40 * time.Minute),
		FinishedAt:   o.TimeNow(),
	})
}

func TestCriticalUpsizeTowardsGreenlight(t *testing.T) {
	o, setAssetUsagePercent, clock := setupAssetScrapeTest(t)

	//when the "Critical" threshold gets crossed, a "Critical" operation gets
	//created and immediately confirmed/greenlit
	clock.StepBy(10 * time.Minute)
	setAssetUsagePercent(95)
	must(t, o.ScrapeNextAsset("foo", o.TimeNow()))

	expectedOp := db.PendingOperation{
		ID:           1,
		AssetID:      1,
		Reason:       db.OperationReasonCritical,
		OldSize:      1000,
		NewSize:      1200,
		UsagePercent: 95,
		CreatedAt:    o.TimeNow(),
		ConfirmedAt:  p2time(o.TimeNow()),
		GreenlitAt:   p2time(o.TimeNow()),
	}
	expectPendingOperations(t, o.DB, expectedOp)
	expectFinishedOperations(t, o.DB /*, nothing */)
}

func TestReplaceNormalWithCriticalUpsize(t *testing.T) {
	o, setAssetUsagePercent, clock := setupAssetScrapeTest(t)

	//when the "High" threshold gets crossed, a "High" operation gets created in
	//state "created"
	clock.StepBy(10 * time.Minute)
	setAssetUsagePercent(90)
	must(t, o.ScrapeNextAsset("foo", o.TimeNow()))

	expectPendingOperations(t, o.DB, db.PendingOperation{
		ID:           1,
		AssetID:      1,
		Reason:       db.OperationReasonHigh,
		OldSize:      1000,
		NewSize:      1200,
		UsagePercent: 90,
		CreatedAt:    o.TimeNow(),
	})
	expectFinishedOperations(t, o.DB /*, nothing */)

	//when the "Critical" threshold gets crossed while the the "High" operation
	//is not yet confirmed, the "High" operation is cancelled and a "Critical"
	//operation replaces it
	clock.StepBy(10 * time.Minute)
	setAssetUsagePercent(96)
	must(t, o.ScrapeNextAsset("foo", o.TimeNow()))

	expectPendingOperations(t, o.DB, db.PendingOperation{
		ID:           2,
		AssetID:      1,
		Reason:       db.OperationReasonCritical,
		OldSize:      1000,
		NewSize:      1200,
		UsagePercent: 96,
		CreatedAt:    o.TimeNow(),
		ConfirmedAt:  p2time(o.TimeNow()),
		GreenlitAt:   p2time(o.TimeNow()),
	})
	expectFinishedOperations(t, o.DB, db.FinishedOperation{
		AssetID:      1,
		Reason:       db.OperationReasonHigh,
		Outcome:      db.OperationOutcomeCancelled,
		OldSize:      1000,
		NewSize:      1200,
		UsagePercent: 90,
		CreatedAt:    o.TimeNow().Add(-10 * time.Minute),
		FinishedAt:   o.TimeNow(),
	})
}

func TestAssetScrapeOrdering(t *testing.T) {
	o, amStatic, clock := setupObserver(t)

	//create a resource and multiple assets to test with
	must(t, o.DB.Insert(&db.Resource{
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
			ScrapedAt:    o.TimeNow(),
			Stale:        false,
		},
		{
			ResourceID:   1,
			UUID:         "asset2",
			Size:         1000,
			UsagePercent: 50,
			ScrapedAt:    o.TimeNow(),
			Stale:        false,
		},
		{
			ResourceID:   1,
			UUID:         "asset3",
			Size:         1000,
			UsagePercent: 50,
			ScrapedAt:    o.TimeNow(),
			Stale:        false,
		},
	}
	must(t, o.DB.Insert(&assets[0]))
	must(t, o.DB.Insert(&assets[1]))
	must(t, o.DB.Insert(&assets[2]))

	amStatic.Assets = map[string]map[string]plugins.StaticAsset{
		"project1": {
			"asset1": {Size: 1000, Usage: 510},
			"asset2": {Size: 1000, Usage: 520},
			"asset3": {Size: 1000, Usage: 530},
		},
	}

	//this should scrape each asset once, in order
	clock.StepBy(time.Minute)
	must(t, o.ScrapeNextAsset("foo", o.TimeNow()))
	clock.StepBy(time.Minute)
	must(t, o.ScrapeNextAsset("foo", o.TimeNow()))
	clock.StepBy(time.Minute)
	must(t, o.ScrapeNextAsset("foo", o.TimeNow()))

	//so the asset table should look like this now
	assets[0].ScrapedAt = o.TimeNow().Add(-2 * time.Minute)
	assets[1].ScrapedAt = o.TimeNow().Add(-time.Minute)
	assets[2].ScrapedAt = o.TimeNow()
	assets[0].UsagePercent = 51
	assets[1].UsagePercent = 52
	assets[2].UsagePercent = 53
	expectAssets(t, o.DB, assets...)

	//next scrape should work identically
	clock.StepBy(time.Minute)
	must(t, o.ScrapeNextAsset("foo", o.TimeNow()))
	clock.StepBy(time.Minute)
	must(t, o.ScrapeNextAsset("foo", o.TimeNow()))
	clock.StepBy(time.Minute)
	must(t, o.ScrapeNextAsset("foo", o.TimeNow()))
	assets[0].ScrapedAt = o.TimeNow().Add(-2 * time.Minute)
	assets[1].ScrapedAt = o.TimeNow().Add(-time.Minute)
	assets[2].ScrapedAt = o.TimeNow()
	expectAssets(t, o.DB, assets...)

	//and all of this should not have created any operations
	expectPendingOperations(t, o.DB /*, nothing */)
	expectFinishedOperations(t, o.DB /*, nothing */)
}

func expectAssets(t *testing.T, dbi *gorp.DbMap, assets ...db.Asset) {
	t.Helper()
	var dbAssets []db.Asset
	_, err := dbi.Select(&dbAssets, `SELECT * FROM assets ORDER BY id`)
	must(t, err)
	if len(dbAssets) == 0 {
		dbAssets = nil
	}
	assertJSONEqual(t, "assets", dbAssets, assets)
}

func expectPendingOperations(t *testing.T, dbi *gorp.DbMap, ops ...db.PendingOperation) {
	t.Helper()
	var dbOps []db.PendingOperation
	_, err := dbi.Select(&dbOps, `SELECT * FROM pending_operations ORDER BY id`)
	must(t, err)
	if len(dbOps) == 0 {
		dbOps = nil
	}
	assertJSONEqual(t, "pending operations", dbOps, ops)
}

func expectFinishedOperations(t *testing.T, dbi *gorp.DbMap, ops ...db.FinishedOperation) {
	t.Helper()
	var dbOps []db.FinishedOperation
	_, err := dbi.Select(&dbOps, `SELECT * FROM finished_operations ORDER BY created_at, finished_at`)
	must(t, err)
	if len(dbOps) == 0 {
		dbOps = nil
	}
	assertJSONEqual(t, "finished operations", dbOps, ops)
}

func assertJSONEqual(t *testing.T, variable string, actual, expected interface{}) {
	expectedJSON, _ := json.Marshal(expected)
	actualJSON, _ := json.Marshal(actual)
	if string(expectedJSON) != string(actualJSON) {
		t.Errorf("expected %s = %s", variable, string(expectedJSON))
		t.Errorf("  actual %s = %s", variable, string(actualJSON))
	}
}

//Take pointer to time.Time expression.
func p2time(t time.Time) *time.Time {
	return &t
}
