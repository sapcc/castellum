// SPDX-FileCopyrightText: 2019 SAP SE or an SAP affiliate company
// SPDX-License-Identifier: Apache-2.0

package test

import (
	"encoding/json"

	"github.com/go-gorp/gorp/v3"

	"github.com/sapcc/castellum/internal/db"
)

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
