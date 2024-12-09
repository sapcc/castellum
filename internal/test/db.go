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

	"github.com/go-gorp/gorp/v3"
	"github.com/sapcc/go-bits/easypg"

	"github.com/sapcc/castellum/internal/db"
)

// WithDB prepares a DB reference for this test, or fails the test if the DB
// is not ready.
func (t T) WithDB(fixtureFile *string, action func(dbi *gorp.DbMap)) {
	opts := []easypg.TestSetupOption{
		easypg.ClearTables("resources", "assets", "pending_operations", "finished_operations"),
		easypg.ResetPrimaryKeys("resources", "assets", "pending_operations"),
	}
	if fixtureFile != nil {
		opts = append(opts, easypg.LoadSQLFile(*fixtureFile))
	}

	dbConn := easypg.ConnectForTest(t.T, db.Configuration(), opts...)
	action(db.InitORM(dbConn))
	t.Must(dbConn.Close())
}

// MustUpdate aborts the test if dbi.Update(row) throws an error.
func (t T) MustUpdate(dbi *gorp.DbMap, row any) {
	_, err := dbi.Update(row)
	t.Must(err)
}

// ExpectAssets checks that the DB contains exactly the given assets.
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

// ExpectPendingOperations checks that the DB contains exactly the given pending ops.
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

// ExpectFinishedOperations checks that the DB contains exactly the given finished ops.
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

// AssertJSONEqual checks that both given values have identical JSON serializations.
func (t T) AssertJSONEqual(variable string, actual, expected any) {
	t.Helper()
	expectedJSON, _ := json.Marshal(expected) //nolint:errcheck
	actualJSON, _ := json.Marshal(actual)     //nolint:errcheck
	if string(expectedJSON) != string(actualJSON) {
		t.Errorf("expected %s = %s", variable, string(expectedJSON))
		t.Errorf("  actual %s = %s", variable, string(actualJSON))
	}
}
