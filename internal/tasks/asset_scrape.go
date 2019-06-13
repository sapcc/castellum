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
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/sapcc/castellum/internal/core"
	"github.com/sapcc/castellum/internal/db"
	"github.com/sapcc/go-bits/logg"
	"gopkg.in/gorp.v2"
)

//query that finds the next resource that needs to be scraped
var scrapeAssetSearchQuery = `
	SELECT a.* FROM assets a JOIN resources r ON r.asset_type = $1
	WHERE a.checked_at < $2
	-- order by update priority (first outdated assets, then by ID for deterministic test behavior)
	ORDER BY a.checked_at ASC, a.id ASC
	LIMIT 1
`

//ScrapeNextAsset finds the next asset of the given type that needs scraping
//and scrapes it, i.e. checks its status and creates/confirms/cancels
//operations accordingly.
//
//Returns sql.ErrNoRows when no asset needed scraping, to indicate to the
//caller to slow down.
func (c Context) ScrapeNextAsset(assetType db.AssetType, maxCheckedAt time.Time) (returnedError error) {
	defer func() {
		labels := prometheus.Labels{"asset": string(assetType)}
		if returnedError == nil {
			assetScrapeSuccessCounter.With(labels).Inc()
		} else if returnedError != sql.ErrNoRows {
			assetScrapeFailedCounter.With(labels).Inc()
		}
	}()

	manager := c.Team.ForAssetType(assetType)
	if manager == nil {
		panic(fmt.Sprintf("no asset manager for asset type %q", assetType))
	}

	//find asset
	var asset db.Asset
	err := c.DB.SelectOne(&asset, scrapeAssetSearchQuery, assetType, maxCheckedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			logg.Debug("no %s assets to scrape - slowing down...", assetType)
			return sql.ErrNoRows
		}
		return err
	}

	//find corresponding resource
	var res db.Resource
	err = c.DB.SelectOne(&res, `SELECT * FROM resources WHERE id = $1`, asset.ResourceID)
	if err != nil {
		return err
	}
	logg.Debug("scraping %s asset %s in project %s", assetType, asset.UUID, res.ScopeUUID)

	//get pending operation for this asset
	var pendingOps []db.PendingOperation
	_, err = c.DB.Select(&pendingOps, `SELECT * FROM pending_operations WHERE asset_id = $1`, asset.ID)
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
		//GetAssetStatus may fail for single assets, e.g. for Manila shares in
		//transitional states like Creating/Deleting; in that case, update
		//checked_at so that the next call continues with the next asset, but leave
		//scraped_at unchanged to indicate old data
		asset.CheckedAt = c.TimeNow()
		_, dbErr := c.DB.Update(&asset)
		if dbErr != nil {
			return dbErr
		}
		return fmt.Errorf("cannot query status of %s %s: %s", assetType, asset.UUID, err.Error())
	}

	//update asset attributes - We have four separate cases here, which
	//correspond to the branches of the `switch` statement. When changing any of
	//this, tread very carefully.
	asset.CheckedAt = c.TimeNow()
	asset.ScrapedAt = asset.CheckedAt
	canTouchPendingOperations := true
	switch {
	case asset.ExpectedSize == nil:
		//normal case: no resize operation has recently completed -> record
		//status.Size as actual size
		fallthrough
	case *asset.ExpectedSize == status.Size:
		//a resize operation has completed, and now we're seeing the new size in
		//the backend -> record status.Size as actualSize and clear ExpectedSize
		fallthrough
	case asset.Size != status.Size:
		//while waiting for a resize operation to be reflected in the backend,
		//we're observing an entirely different size (i.e. neither the operation's
		//OldSize nor its NewSize) -> assume that some other user changed the size
		//in parallel and take that new value as the actual size
		asset.Size = status.Size
		asset.UsagePercent = status.UsagePercent
		asset.ExpectedSize = nil
	default:
		//we are waiting for a resize operation to reflect in the backend, but
		//the backend is still reporting the old size -> do not touch anything until the backend is showing the new size
		canTouchPendingOperations = false
	}

	//update asset in DB
	tx, err := c.DB.Begin()
	if err != nil {
		return err
	}
	defer core.RollbackUnlessCommitted(tx)
	_, err = tx.Update(&asset)
	if err != nil {
		return err
	}
	if !canTouchPendingOperations {
		return tx.Commit()
	}

	//never touch operations in status "greenlit" - they may be executing on a
	//worker right now
	if pendingOp != nil && pendingOp.GreenlitAt != nil && !pendingOp.GreenlitAt.After(c.TimeNow()) {
		return tx.Commit()
	}

	//if there is a pending operation, try to move it forward
	if pendingOp != nil {
		pendingOp, err = c.maybeCancelOperation(tx, res, asset, *pendingOp)
		if err != nil {
			return fmt.Errorf("cannot cancel operation on %s %s: %s", assetType, asset.UUID, err.Error())
		}
	}
	if pendingOp != nil {
		pendingOp, err = c.maybeConfirmOperation(tx, res, asset, *pendingOp)
		if err != nil {
			return fmt.Errorf("cannot confirm operation on %s %s: %s", assetType, asset.UUID, err.Error())
		}
	}
	//if there is no pending operation (or if we just cancelled it), see if we can start one
	if pendingOp == nil {
		err = c.maybeCreateOperation(tx, res, asset)
		if err != nil {
			return fmt.Errorf("cannot create operation on %s %s: %s", assetType, asset.UUID, err.Error())
		}
	}

	return tx.Commit()
}

func (c Context) maybeCreateOperation(tx *gorp.Transaction, res db.Resource, asset db.Asset) error {
	op := db.PendingOperation{
		AssetID:      asset.ID,
		OldSize:      asset.Size,
		UsagePercent: asset.UsagePercent,
		CreatedAt:    c.TimeNow(),
	}

	match := getMatchingReasons(res, asset)
	switch {
	case match[db.OperationReasonCritical]:
		op.Reason = db.OperationReasonCritical
		op.NewSize = getNewSize(res, asset, true)
	case match[db.OperationReasonHigh]:
		op.Reason = db.OperationReasonHigh
		op.NewSize = getNewSize(res, asset, true)
	case match[db.OperationReasonLow]:
		op.Reason = db.OperationReasonLow
		op.NewSize = getNewSize(res, asset, false)
	default:
		//no threshold exceeded -> do not create an operation
		return nil
	}

	//skip the operation if the size would not change (this is especially true
	//for reason "low" and oldSize = 1)
	if op.OldSize == op.NewSize {
		return nil
	}

	//critical operations can be confirmed immediately
	if op.Reason == db.OperationReasonCritical {
		op.ConfirmedAt = &op.CreatedAt
		//right now, nothing requires operator approval
		op.GreenlitAt = op.ConfirmedAt
	}

	core.CountStateTransition(res, asset.UUID, db.OperationStateDidNotExist, op.State())
	return tx.Insert(&op)
}

func (c Context) maybeCancelOperation(tx *gorp.Transaction, res db.Resource, asset db.Asset, op db.PendingOperation) (*db.PendingOperation, error) {
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

	core.CountStateTransition(res, asset.UUID, op.State(), db.OperationStateCancelled)
	finishedOp := op.IntoFinishedOperation(db.OperationOutcomeCancelled, c.TimeNow())
	_, err := tx.Delete(&op)
	if err != nil {
		return nil, err
	}
	return nil, tx.Insert(&finishedOp)
}

func (c Context) maybeConfirmOperation(tx *gorp.Transaction, res db.Resource, asset db.Asset, op db.PendingOperation) (*db.PendingOperation, error) {
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
	if c.TimeNow().Before(earliestConfirm) {
		return &op, nil
	}

	previousState := op.State()
	confirmedAt := c.TimeNow()
	op.ConfirmedAt = &confirmedAt
	op.GreenlitAt = op.ConfirmedAt //right now, nothing requires operator approval
	_, err := tx.Update(&op)
	core.CountStateTransition(res, asset.UUID, previousState, op.State())
	return &op, err
}

func getMatchingReasons(res db.Resource, asset db.Asset) map[db.OperationReason]bool {
	result := make(map[db.OperationReason]bool)
	if res.LowThresholdPercent > 0 && asset.UsagePercent <= res.LowThresholdPercent {
		if canDownsize(res, asset) {
			result[db.OperationReasonLow] = true
		}
	}
	if res.HighThresholdPercent > 0 && asset.UsagePercent >= res.HighThresholdPercent {
		if canUpsize(res, asset) {
			result[db.OperationReasonHigh] = true
		}
	}
	if res.CriticalThresholdPercent > 0 && asset.UsagePercent >= res.CriticalThresholdPercent {
		if canUpsize(res, asset) {
			result[db.OperationReasonCritical] = true
		}
	}
	return result
}

func canDownsize(res db.Resource, asset db.Asset) bool {
	if res.MinimumSize == nil {
		return true
	}
	return getNewSize(res, asset, false) >= *res.MinimumSize
}

func canUpsize(res db.Resource, asset db.Asset) bool {
	if res.MaximumSize == nil {
		return true
	}
	return getNewSize(res, asset, true) <= *res.MaximumSize
}

func getNewSize(res db.Resource, asset db.Asset, up bool) uint64 {
	step := (asset.Size * uint64(res.SizeStepPercent)) / 100
	//a small fraction of a small value (e.g. 10% of size = 6) may round down to zero
	if step == 0 {
		step = 1
	}

	if up {
		return asset.Size + step
	}

	//when going down, we have to take care not to end up with zero
	if asset.Size < 1+step {
		//^ This condition is equal to `asset.Size - step < 1`, but cannot overflow below 0.
		return 1
	}
	return asset.Size - step
}
