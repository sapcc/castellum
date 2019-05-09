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

package test

import (
	"encoding/json"

	"github.com/sapcc/castellum/internal/db"
	"gopkg.in/gorp.v2"
)

//PrepareDB prepares a DB reference for this test, or fails the test if the DB
//is not ready.
func (t T) PrepareDB() *gorp.DbMap {
	dbi, err := db.Init("postgres://postgres@localhost:54321/castellum?sslmode=disable")
	if err != nil {
		t.Error(err)
		t.Log("Try prepending ./testing/with-postgres-db.sh to your command.")
		t.FailNow()
	}

	//wipe the DB clean if there are any leftovers from the previous test run
	t.MustExec(dbi, "DELETE FROM resources")
	t.MustExec(dbi, "DELETE FROM assets")
	t.MustExec(dbi, "DELETE FROM pending_operations")
	t.MustExec(dbi, "DELETE FROM finished_operations")
	//reset all primary key sequences for reproducible row IDs
	t.MustExec(dbi, "ALTER SEQUENCE resources_id_seq RESTART WITH 1")
	t.MustExec(dbi, "ALTER SEQUENCE assets_id_seq RESTART WITH 1")
	t.MustExec(dbi, "ALTER SEQUENCE pending_operations_id_seq RESTART WITH 1")

	return dbi
}

//ExpectResources checks that the DB contains exactly the given resources.
func (t T) ExpectResources(dbi *gorp.DbMap, resources ...db.Resource) {
	t.Helper()
	var dbResources []db.Resource
	_, err := dbi.Select(&dbResources, `SELECT * FROM resources ORDER BY id`)
	t.Must(err)
	if len(dbResources) == 0 {
		dbResources = nil
	}
	t.AssertJSONEqual("resources", dbResources, resources)
}

//ExpectAssets checks that the DB contains exactly the given assets.
func (t T) ExpectAssets(dbi *gorp.DbMap, assets ...db.Asset) {
	t.Helper()
	var dbAssets []db.Asset
	_, err := dbi.Select(&dbAssets, `SELECT * FROM assets ORDER BY id`)
	t.Must(err)
	if len(dbAssets) == 0 {
		dbAssets = nil
	}
	t.AssertJSONEqual("assets", dbAssets, assets)
}

//ExpectPendingOperations checks that the DB contains exactly the given pending ops.
func (t T) ExpectPendingOperations(dbi *gorp.DbMap, ops ...db.PendingOperation) {
	t.Helper()
	var dbOps []db.PendingOperation
	_, err := dbi.Select(&dbOps, `SELECT * FROM pending_operations ORDER BY id`)
	t.Must(err)
	if len(dbOps) == 0 {
		dbOps = nil
	}
	t.AssertJSONEqual("pending operations", dbOps, ops)
}

//ExpectFinishedOperations checks that the DB contains exactly the given finished ops.
func (t T) ExpectFinishedOperations(dbi *gorp.DbMap, ops ...db.FinishedOperation) {
	t.Helper()
	var dbOps []db.FinishedOperation
	_, err := dbi.Select(&dbOps, `SELECT * FROM finished_operations ORDER BY asset_id, created_at, finished_at`)
	t.Must(err)
	if len(dbOps) == 0 {
		dbOps = nil
	}
	t.AssertJSONEqual("finished operations", dbOps, ops)
}

//AssertJSONEqual checks that both given values have identical JSON serializations.
func (t T) AssertJSONEqual(variable string, actual, expected interface{}) {
	t.Helper()
	expectedJSON, _ := json.Marshal(expected)
	actualJSON, _ := json.Marshal(actual)
	if string(expectedJSON) != string(actualJSON) {
		t.Errorf("expected %s = %s", variable, string(expectedJSON))
		t.Errorf("  actual %s = %s", variable, string(actualJSON))
	}
}
