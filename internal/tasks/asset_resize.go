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
	"fmt"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/sapcc/castellum/internal/core"
	"github.com/sapcc/castellum/internal/db"
	"github.com/sapcc/go-bits/logg"
)

//WARNING: This must be run in a transaction, or else `FOR UPDATE SKIP LOCKED`
//will not work as expected.
var selectAndDeleteNextResizeQuery = `
	DELETE FROM pending_operations WHERE id = (
		SELECT id FROM pending_operations WHERE greenlit_at < $1
		ORDER BY reason ASC LIMIT 1
		FOR UPDATE SKIP LOCKED
	) RETURNING *
`

//ExecuteNextResize finds the next pending operation and executes it, i.e.
//moves it from status "greenlit" to either "succeeded" or "failed".
//
//Returns sql.ErrNoRows when no operation was in the queue, to indicate to the
//caller to slow down.
//
//The caller will usually not need the `targetAssetType` return value. We just
//return it so that a deferred function inside this function has it in scope.
func (c Context) ExecuteNextResize() (targetAssetType db.AssetType, returnedError error) {
	defer func() {
		if targetAssetType != "" {
			labels := prometheus.Labels{"asset": string(targetAssetType)}
			if returnedError == nil {
				assetResizeCounter.With(labels).Inc()
			} else {
				assetResizeErroredCounter.With(labels).Inc()
			}
		}
	}()

	//we need a DB transaction for the row-level locking to work correctly
	tx, err := c.DB.Begin()
	if err != nil {
		return "", err
	}
	defer core.RollbackUnlessCommitted(tx)

	//select the next greenlit PendingOperation (and delete it immediately)
	var op db.PendingOperation
	err = tx.SelectOne(&op, selectAndDeleteNextResizeQuery, c.TimeNow())
	if err != nil {
		if err == sql.ErrNoRows {
			logg.Debug("no assets to resize - slowing down...")
			return "", sql.ErrNoRows
		}
		return "", err
	}

	//find the corresponding asset, resource and asset manager
	var asset db.Asset
	err = tx.SelectOne(&asset, `SELECT * FROM assets WHERE id = $1`, op.AssetID)
	if err != nil {
		return "", err
	}
	var res db.Resource
	err = tx.SelectOne(&res, `SELECT * FROM resources WHERE id = $1`, asset.ResourceID)
	if err != nil {
		return "", err
	}
	manager := c.Team.ForAssetType(res.AssetType)
	if manager == nil {
		return res.AssetType, fmt.Errorf("no asset manager for asset type %q", res.AssetType)
	}

	//when running in a unit test, wait for the test harness to unblock us
	if c.Blocker != nil {
		for range c.Blocker {
		}
	}

	//perform the resize operation
	err = manager.SetAssetSize(res, asset.UUID, op.OldSize, op.NewSize)
	outcome := db.OperationOutcomeSucceeded
	errorMessage := ""
	if err != nil {
		logg.Error("cannot resize %s %s to size %d: %s", res.AssetType, asset.UUID, op.NewSize, err.Error())
		outcome = db.OperationOutcomeFailed
		errorMessage = err.Error()
	}

	finishedOp := op.IntoFinishedOperation(outcome, c.TimeNow())
	finishedOp.ErrorMessage = errorMessage
	err = tx.Insert(&finishedOp)
	if err != nil {
		return res.AssetType, err
	}

	//mark asset as having just completed as resize operation (see
	//logic in ScrapeNextAsset() for details)
	if outcome == db.OperationOutcomeSucceeded {
		_, err := tx.Exec(`UPDATE assets SET expected_size = $1 WHERE id = $2`,
			finishedOp.NewSize, asset.ID)
		if err != nil {
			return res.AssetType, err
		}
	}

	core.CountStateTransition(res, db.OperationStateGreenlit, finishedOp.State())
	return res.AssetType, tx.Commit()
}