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
	"encoding/json"
	"testing"
	"time"

	"github.com/sapcc/castellum/internal/core"
	"github.com/sapcc/castellum/internal/db"
	"github.com/sapcc/castellum/internal/plugins"
	"gopkg.in/gorp.v2"
)

////////////////////////////////////////////////////////////////////////////////
// This file contains functions and types that are used by multiple tests in
// this package.
////////////////////////////////////////////////////////////////////////////////

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err.Error())
	}
}

func mustExec(t *testing.T, dbi *gorp.DbMap, query string) {
	t.Helper()
	_, err := dbi.Exec(query)
	must(t, err)
}

//FakeClock is a clock that only changes when we tell it to.
type FakeClock int64

//Now is a double for time.Now().
func (f *FakeClock) Now() time.Time {
	return time.Unix(int64(*f), 0).UTC()
}

//Step advances the clock by one second.
func (f *FakeClock) Step() {
	*f++
}

//Step advances the clock by the given duration
func (f *FakeClock) StepBy(d time.Duration) {
	*f += FakeClock(d / time.Second)
}

func setupContext(t *testing.T) (*Context, *plugins.AssetManagerStatic, *FakeClock) {
	dbi, err := db.Init("postgres://postgres@localhost:54321/castellum?sslmode=disable")
	if err != nil {
		t.Error(err)
		t.Log("Try prepending ./testing/with-postgres-db.sh to your command.")
		t.FailNow()
	}

	//wipe the DB clean if there are any leftovers from the previous test run
	mustExec(t, dbi, "DELETE FROM resources")
	mustExec(t, dbi, "DELETE FROM assets")
	mustExec(t, dbi, "DELETE FROM pending_operations")
	mustExec(t, dbi, "DELETE FROM finished_operations")
	//reset all primary key sequences for reproducible row IDs
	mustExec(t, dbi, "ALTER SEQUENCE resources_id_seq RESTART WITH 1")
	mustExec(t, dbi, "ALTER SEQUENCE assets_id_seq RESTART WITH 1")
	mustExec(t, dbi, "ALTER SEQUENCE pending_operations_id_seq RESTART WITH 1")

	amStatic := &plugins.AssetManagerStatic{
		AssetType: "foo",
	}
	//clock starts at an easily recognizable value
	clockVar := FakeClock(99990)
	clock := &clockVar

	return &Context{
		DB:      dbi,
		Team:    core.AssetManagerTeam{amStatic},
		TimeNow: clock.Now,
	}, amStatic, clock
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
	_, err := dbi.Select(&dbOps, `SELECT * FROM finished_operations ORDER BY asset_id, created_at, finished_at`)
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
