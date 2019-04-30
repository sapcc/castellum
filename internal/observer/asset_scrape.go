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
	"fmt"
	"time"

	"github.com/sapcc/castellum/internal/core"
	"github.com/sapcc/castellum/internal/db"
	"github.com/sapcc/go-bits/logg"
	"gopkg.in/gorp.v2"
)

//query that finds the next resource that needs to be scraped
var scrapeAssetSearchQuery = `
	SELECT a.* FROM assets a JOIN resources r ON r.asset_type = $1
	WHERE a.scraped_at < $2 OR a.stale
	-- order by update priority (first stale assets, then outdated assets, then by ID for deterministic test behavior)
	ORDER BY a.stale DESC, a.scraped_at ASC, a.id ASC
	LIMIT 1
`

//ScrapeNextAsset finds the next asset of the given type that needs scraping
//and scrapes it, i.e. checks its status and creates/confirms/cancels
//operations accordingly.
//
//Returns sql.ErrNoRows when no asset needed scraping, to indicate to the
//caller to slow down.
func (o Observer) ScrapeNextAsset(assetType string, maxScrapedAt time.Time) error {
	manager := o.Team.ForAssetType(assetType)
	if manager == nil {
		panic(fmt.Sprintf("no asset manager for asset type %q", assetType))
	}

	//find asset
	var asset db.Asset
	err := o.DB.SelectOne(&asset, scrapeAssetSearchQuery, assetType, maxScrapedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			logg.Debug("no %s assets to scrape - slowing down...", assetType)
			return sql.ErrNoRows
		}
		return err
	}

	//find corresponding resource
	var res db.Resource
	err = o.DB.SelectOne(&res, `SELECT * FROM resources WHERE id = $1`, asset.ResourceID)
	if err != nil {
		return err
	}
	logg.Debug("scraping %s asset %s in project %s", assetType, asset.UUID, res.ScopeUUID)

	//get pending operation for this asset
	var pendingOps []db.PendingOperation
	_, err = o.DB.Select(&pendingOps, `SELECT * FROM pending_operations WHERE asset_id = $1`, asset.ID)
	var pendingOp *db.PendingOperation
	switch err {
	case nil:
		if len(pendingOps) > 0 {
			pendingOp = &pendingOps[0]
		}
	case sql.ErrNoRows:
		pendingOp = nil
	default:
		return err
	}

	//check asset status
	oldStatus := core.AssetStatus{
		Size:         asset.Size,
		UsagePercent: asset.UsagePercent,
	}
	status, err := manager.GetAssetStatus(res, asset.UUID, &oldStatus)
	if err != nil {
		return fmt.Errorf("cannot query status of %s %s: %s", assetType, asset.UUID, err.Error())
	}

	//update asset in DB
	tx, err := o.DB.Begin()
	if err != nil {
		return err
	}
	defer core.RollbackUnlessCommitted(tx)
	asset.Size = status.Size
	asset.UsagePercent = status.UsagePercent
	asset.ScrapedAt = o.TimeNow()
	asset.Stale = false
	_, err = tx.Update(&asset)
	if err != nil {
		return err
	}

	//never touch operations in status "greenlit" - they may be executing on a
	//worker right now
	if pendingOp != nil && pendingOp.GreenlitAt != nil && !pendingOp.GreenlitAt.After(o.TimeNow()) {
		return tx.Commit()
	}

	//if there is a pending operation, try to move it forward
	if pendingOp != nil {
		pendingOp, err = o.maybeCancelOperation(tx, res, asset, *pendingOp)
		if err != nil {
			return fmt.Errorf("cannot cancel operation on %s %s: %s", assetType, asset.UUID, err.Error())
		}
	}
	if pendingOp != nil {
		pendingOp, err = o.maybeConfirmOperation(tx, res, asset, *pendingOp)
		if err != nil {
			return fmt.Errorf("cannot confirm operation on %s %s: %s", assetType, asset.UUID, err.Error())
		}
	}
	//if there is no pending operation (or if we just cancelled it), see if we can start one
	if pendingOp == nil {
		err = o.maybeCreateOperation(tx, res, asset)
		if err != nil {
			return fmt.Errorf("cannot create operation on %s %s: %s", assetType, asset.UUID, err.Error())
		}
	}

	return tx.Commit()
}

func (o Observer) maybeCreateOperation(tx *gorp.Transaction, res db.Resource, asset db.Asset) error {
	op := db.PendingOperation{
		AssetID:      asset.ID,
		OldSize:      asset.Size,
		UsagePercent: asset.UsagePercent,
		CreatedAt:    o.TimeNow(),
	}

	match := getMatchingReasons(res, asset)
	switch {
	case match[db.OperationReasonCritical]:
		op.Reason = db.OperationReasonCritical
		op.NewSize = getNewSize(asset, res, true)
	case match[db.OperationReasonHigh]:
		op.Reason = db.OperationReasonHigh
		op.NewSize = getNewSize(asset, res, true)
	case match[db.OperationReasonLow]:
		op.Reason = db.OperationReasonLow
		op.NewSize = getNewSize(asset, res, false)
	default:
		//no threshold exceeded -> do not create an operation
		return nil
	}

	//critical operations can be confirmed immediately
	if op.Reason == db.OperationReasonCritical {
		op.ConfirmedAt = &op.CreatedAt
		//right now, nothing requires operator approval
		op.GreenlitAt = op.ConfirmedAt
	}

	return tx.Insert(&op)
}

func (o Observer) maybeCancelOperation(tx *gorp.Transaction, res db.Resource, asset db.Asset, op db.PendingOperation) (*db.PendingOperation, error) {
	//cancel when the threshold that triggered this operation is no longer being crossed
	match := getMatchingReasons(res, asset)
	doCancel := !match[op.Reason]
	if op.Reason == db.OperationReasonHigh && match[db.OperationReasonCritical] {
		//as an exception, cancel a "High" operation when we've crossed the
		//"Critical" threshold in the meantime - when we get to
		//maybeCreateOperation() next, a new operation with reason "Critical" will
		//be created instead
		doCancel = true
	}
	if !doCancel {
		return &op, nil
	}

	finishedOp := op.IntoFinishedOperation(db.OperationOutcomeCancelled, o.TimeNow())
	_, err := tx.Delete(&op)
	if err != nil {
		return nil, err
	}
	return nil, tx.Insert(&finishedOp)
}

func (o Observer) maybeConfirmOperation(tx *gorp.Transaction, res db.Resource, asset db.Asset, op db.PendingOperation) (*db.PendingOperation, error) {
	//can only confirm when the corresponding threshold is still being crossed
	if !getMatchingReasons(res, asset)[op.Reason] {
		return &op, nil
	}

	//can only confirm when it has been like this for at least the configured delay
	var earliestConfirm time.Time
	switch op.Reason {
	case db.OperationReasonLow:
		earliestConfirm = op.CreatedAt.Add(time.Duration(res.LowDelaySeconds) * time.Second)
	case db.OperationReasonHigh:
		earliestConfirm = op.CreatedAt.Add(time.Duration(res.HighDelaySeconds) * time.Second)
	case db.OperationReasonCritical:
		//defense in depth - maybeCreateOperation() should already have confirmed this
		earliestConfirm = op.CreatedAt
	}
	if o.TimeNow().Before(earliestConfirm) {
		return &op, nil
	}

	confirmedAt := o.TimeNow()
	op.ConfirmedAt = &confirmedAt
	//right now, nothing requires operator approval
	op.GreenlitAt = op.ConfirmedAt
	_, err := tx.Update(&op)
	return &op, err
}

func getMatchingReasons(res db.Resource, asset db.Asset) map[db.OperationReason]bool {
	result := make(map[db.OperationReason]bool)
	if res.LowThresholdPercent > 0 && asset.UsagePercent <= res.LowThresholdPercent {
		result[db.OperationReasonLow] = true
	}
	if res.HighThresholdPercent > 0 && asset.UsagePercent >= res.HighThresholdPercent {
		result[db.OperationReasonHigh] = true
	}
	if res.CriticalThresholdPercent > 0 && asset.UsagePercent >= res.CriticalThresholdPercent {
		result[db.OperationReasonCritical] = true
	}
	return result
}

func getNewSize(asset db.Asset, res db.Resource, up bool) uint64 {
	step := (asset.Size * uint64(res.SizeStepPercent)) / 100
	if up {
		return asset.Size + step
	}
	return asset.Size - step
}
