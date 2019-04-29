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

func TestNormalUpsizeUntilConfirmed(t *testing.T) {
	o, setAssetUsagePercent, clock := setupAssetScrapeTest(t)

	//when no threshold is crossed, no operation gets created
	clock.StepBy(10 * time.Minute)
	must(t, o.ScrapeNextAsset("foo", o.TimeNow()))
	expectPendingOperations(t, o.DB /*, nothing */)
	expectFinishedOperations(t, o.DB /*, nothing */)

	//when the "High" threshold gets crossed, a "High" operation gets created in
	//state "created"
	clock.StepBy(10 * time.Minute)
	setAssetUsagePercent(80)
	must(t, o.ScrapeNextAsset("foo", o.TimeNow()))
	expectPendingOperations(t, o.DB,
		db.PendingOperation{
			ID:           1,
			AssetID:      1,
			Reason:       db.OperationReasonHigh,
			OldSize:      1000,
			NewSize:      1200,
			UsagePercent: 80,
			CreatedAt:    o.TimeNow(),
		},
	)
	expectFinishedOperations(t, o.DB /*, nothing */)
}

//TODO TestNormalDownsizeUntilConfirmed
//TODO TestCriticalUpsizeUntilConfirmed
//TODO TestReplaceNormalWithCriticalUpsize
//TODO TestNoOperationWhenNoThreshold

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
