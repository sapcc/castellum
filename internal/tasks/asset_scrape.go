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
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/go-gorp/gorp/v3"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/sapcc/go-api-declarations/castellum"
	"github.com/sapcc/go-bits/errext"
	"github.com/sapcc/go-bits/jobloop"
	"github.com/sapcc/go-bits/logg"
	"github.com/sapcc/go-bits/osext"
	"github.com/sapcc/go-bits/sqlext"

	"github.com/sapcc/castellum/internal/core"
	"github.com/sapcc/castellum/internal/db"
)

// query that finds the next resource that needs to be scraped
//
// WARNING: This must be run in a transaction, or else `FOR UPDATE SKIP LOCKED`
// will not work as expected.
var scrapeAssetSearchQuery = sqlext.SimplifyWhitespace(`
	SELECT * FROM assets
	WHERE next_scrape_at <= $1
	-- order by update priority (first outdated assets, then by ID for deterministic test behavior)
	ORDER BY next_scrape_at ASC, id ASC
	-- prevent other job loops from working on the same asset concurrently
	FOR UPDATE SKIP LOCKED LIMIT 1
`)

var logScrapes = osext.GetenvBool("CASTELLUM_LOG_SCRAPES")

// AssetScrapingJob returns a job where each task is a asset that needs to be
// scraped. The task checks its status and creates/confirms/cancels operations accordingly.
func (c *Context) AssetScrapingJob(registerer prometheus.Registerer) jobloop.Job {
	return (&jobloop.TxGuardedJob[*gorp.Transaction, db.Asset]{
		Metadata: jobloop.JobMetadata{
			ReadableName:    "asset scraping",
			ConcurrencySafe: true, // because "FOR UPDATE SKIP LOCKED" is used
			CounterOpts: prometheus.CounterOpts{
				Name: "castellum_asset_scrapes",
				Help: "Counter for asset scrape operations.",
			},
			CounterLabels: []string{"asset_type"},
		},
		BeginTx:     c.DB.Begin,
		DiscoverRow: c.discoverAssetScrape,
		ProcessRow:  c.processAssetScrape,
	}).Setup(registerer)
}

func (c *Context) discoverAssetScrape(ctx context.Context, tx *gorp.Transaction, labels prometheus.Labels) (asset db.Asset, err error) {
	err = tx.SelectOne(&asset, scrapeAssetSearchQuery, c.TimeNow())
	return asset, err
}

func (c *Context) processAssetScrape(ctx context.Context, tx *gorp.Transaction, asset db.Asset, labels prometheus.Labels) error {
	// find resource for asset
	var res db.Resource
	err := tx.SelectOne(&res, `SELECT * FROM resources WHERE id = $1`, asset.ResourceID)
	if err != nil {
		return err
	}
	labels["asset_type"] = string(res.AssetType)

	manager, info := c.Team.ForAssetType(res.AssetType)
	if manager == nil {
		return fmt.Errorf("no asset manager for asset type %q", res.AssetType)
	}

	logg.Debug("scraping %s asset %s in scope %s using manager %v", res.AssetType, asset.UUID, res.ScopeUUID, manager)

	// get pending operation for this asset
	var pendingOp *db.PendingOperation
	err = tx.SelectOne(&pendingOp, `SELECT * FROM pending_operations WHERE asset_id = $1`, asset.ID)
	if errors.Is(err, sql.ErrNoRows) {
		pendingOp = nil
	} else if err != nil {
		return err
	}

	// check asset status
	var oldStatus *core.AssetStatus
	if !asset.NeverScraped {
		oldStatus = &core.AssetStatus{
			Size:              asset.Size,
			Usage:             asset.Usage,
			StrictMinimumSize: asset.StrictMinimumSize,
			StrictMaximumSize: asset.StrictMaximumSize,
		}
	}
	startedAt := c.TimeNow()
	status, err := manager.GetAssetStatus(ctx, res, asset.UUID, oldStatus)
	finishedAt := c.TimeNow()
	if err != nil {
		errMsg := fmt.Errorf("cannot query status of %s %s: %s", string(res.AssetType), asset.UUID, err.Error())
		if errext.IsOfType[core.AssetNotFoundError](err) {
			// asset was deleted since the last scrape of this resource
			logg.Error(errMsg.Error())
			logg.Info("removing deleted %s asset from DB: UUID = %s, scope UUID = %s", res.AssetType, asset.UUID, res.ScopeUUID)
			_, dbErr := tx.Delete(&asset)
			if dbErr != nil {
				return dbErr
			}
			return tx.Commit()
		}

		// GetAssetStatus may fail for single assets, e.g. for Manila shares in
		// transitional states like Creating/Deleting; in that case, update
		// next_scrape_at so that the next call continues with the next asset, but
		// fill the scrape error message to indicate old data
		asset.ScrapeErrorMessage = err.Error()
		asset.NextScrapeAt = c.TimeNow().Add(c.AddJitter(AssetScrapeInterval))
		_, dbErr := tx.Update(&asset)
		if dbErr != nil {
			return dbErr
		}
		dbErr = tx.Commit()
		if dbErr != nil {
			return dbErr
		}
		return errMsg
	}

	if logScrapes {
		var valueLogStrings []string
		if status.StrictMinimumSize != nil {
			valueLogStrings = append(valueLogStrings, fmt.Sprintf("minimum size = %d", *status.StrictMinimumSize))
		}
		if status.StrictMaximumSize != nil {
			valueLogStrings = append(valueLogStrings, fmt.Sprintf("maximum size = %d", *status.StrictMaximumSize))
		}
		for metric, usage := range status.Usage {
			valueLogStrings = append(valueLogStrings, fmt.Sprintf(
				"usage%s = %.3f (%.3f%%)",
				core.Identifier(metric, "[%s]"), usage, core.GetUsagePercent(status.Size, usage),
			))
		}
		logg.Info("observed %s %s at size = %d, %s",
			res.AssetType, asset.UUID, status.Size, strings.Join(valueLogStrings, ", "),
		)
	}

	// update asset attributes - We have four separate cases here, which
	// correspond to the branches of the `switch` statement. When changing any of
	// this, tread very carefully.
	asset.NextScrapeAt = finishedAt.Add(c.AddJitter(AssetScrapeInterval))
	asset.ScrapeDurationSecs = finishedAt.Sub(startedAt).Seconds()
	asset.ScrapeErrorMessage = ""
	asset.NeverScraped = false
	var writeScrapeResults bool
	switch {
	case asset.ExpectedSize == nil:
		// normal case: no resize operation has recently completed -> record
		// status.Size as actual size
		writeScrapeResults = true
	case *asset.ExpectedSize == status.Size:
		// a resize operation has completed, and now we're seeing the new size in
		// the backend -> record status.Size as actualSize and clear ExpectedSize
		writeScrapeResults = true
	case asset.Size != status.Size:
		// while waiting for a resize operation to be reflected in the backend,
		// we're observing an entirely different size (i.e. neither the operation's
		// OldSize nor its NewSize) -> assume that some other user changed the size
		// in parallel and take that new value as the actual size
		writeScrapeResults = true
	case asset.ResizedAt != nil && asset.ResizedAt.Before(c.TimeNow().Add(-1*time.Hour)):
		// we waited for a resize operation to be reflected in the backend, but it
		// has been more than an hour since then -> assume that the resize was
		// interrupted in some way and resume normal behavior
		writeScrapeResults = true
		logg.Info("giving up on waiting for resize of %s %s from size = %d to size = %d to be completed in the backend",
			res.AssetType, asset.UUID,
			asset.Size, *asset.ExpectedSize,
		)
	default:
		// we are waiting for a resize operation to reflect in the backend, but
		// the backend is still reporting the old size -> do not touch anything until the backend is showing the new size
		writeScrapeResults = false
		logg.Info("still waiting for resize of %s %s from size = %d to size = %d to be completed in the backend",
			res.AssetType, asset.UUID,
			asset.Size, *asset.ExpectedSize,
		)
	}
	if writeScrapeResults {
		asset.Size = status.Size
		asset.Usage = status.Usage
		asset.StrictMinimumSize = status.StrictMinimumSize
		asset.StrictMaximumSize = status.StrictMaximumSize
		asset.ExpectedSize = nil
		asset.ResizedAt = nil
	}

	// compute value of `asset.CriticalUsages` field (for reporting to admin only)
	var criticalUsageMetrics []string
	if res.CriticalThresholdPercent.IsNonZero() && (res.MaximumSize == nil || asset.Size < *res.MaximumSize) {
		usagePerc := core.GetMultiUsagePercent(asset.Size, asset.Usage)
		for _, metric := range info.UsageMetrics {
			if usagePerc[metric] >= res.CriticalThresholdPercent[metric] {
				criticalUsageMetrics = append(criticalUsageMetrics, string(metric))
			}
		}
	}
	asset.CriticalUsages = strings.Join(criticalUsageMetrics, ",")

	// update asset in DB
	_, err = tx.Update(&asset)
	if err != nil {
		return err
	}
	if !writeScrapeResults {
		return tx.Commit()
	}

	// never touch operations in status "greenlit" - they may be executing on a
	// worker right now
	if pendingOp != nil && pendingOp.GreenlitAt != nil && !pendingOp.GreenlitAt.After(c.TimeNow()) {
		return tx.Commit()
	}

	// if there is a pending operation, try to move it forward
	if pendingOp != nil {
		pendingOp, err = c.maybeCancelOperation(tx, res, asset, info, *pendingOp)
		if err != nil {
			return fmt.Errorf("cannot cancel operation on %s %s: %s", res.AssetType, asset.UUID, err.Error())
		}
	}
	if pendingOp != nil {
		pendingOp, err = c.maybeUpdateOperation(tx, res, asset, info, *pendingOp)
		if err != nil {
			return fmt.Errorf("cannot update operation on %s %s: %s", res.AssetType, asset.UUID, err.Error())
		}
	}
	if pendingOp != nil {
		pendingOp, err = c.maybeConfirmOperation(tx, res, asset, info, *pendingOp)
		if err != nil {
			return fmt.Errorf("cannot confirm operation on %s %s: %s", res.AssetType, asset.UUID, err.Error())
		}
	}

	// if there is no pending operation (or if we just cancelled it), see if we can start one
	if pendingOp == nil {
		err = c.maybeCreateOperation(tx, res, asset, info)
		if err != nil {
			return fmt.Errorf("cannot create operation on %s %s: %s", res.AssetType, asset.UUID, err.Error())
		}
	}

	return tx.Commit()
}

func (c Context) maybeCreateOperation(tx *gorp.Transaction, res db.Resource, asset db.Asset, info core.AssetTypeInfo) error {
	op := db.PendingOperation{
		AssetID:   asset.ID,
		OldSize:   asset.Size,
		Usage:     asset.Usage,
		CreatedAt: c.TimeNow(),
	}

	eligibleFor := core.GetEligibleOperations(core.LogicOfResource(res, info), core.StatusOfAsset(asset, c.Config, res))
	if val, exists := eligibleFor[castellum.OperationReasonCritical]; exists {
		op.Reason = castellum.OperationReasonCritical
		op.NewSize = val
	} else if val, exists := eligibleFor[castellum.OperationReasonHigh]; exists {
		op.Reason = castellum.OperationReasonHigh
		op.NewSize = val
	} else if val, exists := eligibleFor[castellum.OperationReasonLow]; exists {
		op.Reason = castellum.OperationReasonLow
		op.NewSize = val
	} else {
		// no threshold exceeded -> do not create an operation
		return nil
	}

	// skip the operation if the size would not change (this is especially true
	// for reason "low" and oldSize = 1)
	if op.OldSize == op.NewSize {
		return nil
	}

	// critical operations can be confirmed immediately
	if op.Reason == castellum.OperationReasonCritical {
		op.ConfirmedAt = &op.CreatedAt
		// right now, nothing requires operator approval
		op.GreenlitAt = op.ConfirmedAt
	}

	core.CountStateTransition(res, asset.UUID, castellum.OperationStateDidNotExist, op.State())
	return tx.Insert(&op)
}

func (c Context) maybeCancelOperation(tx *gorp.Transaction, res db.Resource, asset db.Asset, info core.AssetTypeInfo, op db.PendingOperation) (*db.PendingOperation, error) {
	// cancel when the threshold that triggered this operation is no longer being crossed
	eligibleFor := core.GetEligibleOperations(core.LogicOfResource(res, info), core.StatusOfAsset(asset, c.Config, res))
	_, isEligible := eligibleFor[op.Reason]
	if op.Reason == castellum.OperationReasonHigh {
		if _, canBeUpgraded := eligibleFor[castellum.OperationReasonCritical]; canBeUpgraded {
			// as an exception, cancel a "High" operation when we've crossed the
			// "Critical" threshold in the meantime - when we get to
			// maybeCreateOperation() next, a new operation with reason "Critical" will
			// be created instead
			isEligible = false
		}
	}
	if isEligible {
		return &op, nil
	}

	core.CountStateTransition(res, asset.UUID, op.State(), castellum.OperationStateCancelled)
	finishedOp := op.IntoFinishedOperation(castellum.OperationOutcomeCancelled, c.TimeNow())
	_, err := tx.Delete(&op)
	if err != nil {
		return nil, err
	}
	return nil, tx.Insert(&finishedOp)
}

func (c Context) maybeUpdateOperation(tx *gorp.Transaction, res db.Resource, asset db.Asset, info core.AssetTypeInfo, op db.PendingOperation) (*db.PendingOperation, error) {
	// do not touch `op` unless the corresponding threshold is still being crossed
	eligibleFor := core.GetEligibleOperations(core.LogicOfResource(res, info), core.StatusOfAsset(asset, c.Config, res))
	newSize, exists := eligibleFor[op.Reason]
	if !exists {
		return &op, nil
	}

	// if the asset size has changed since the operation has been created
	// (because of resizes not performed by Castellum), calculate a new target size
	if op.NewSize == newSize {
		// nothing to do
		return &op, nil
	}
	op.NewSize = newSize
	_, err := tx.Update(&op)
	return &op, err
}

func (c Context) maybeConfirmOperation(tx *gorp.Transaction, res db.Resource, asset db.Asset, info core.AssetTypeInfo, op db.PendingOperation) (*db.PendingOperation, error) {
	// can only confirm when the corresponding threshold is still being crossed
	if _, exists := core.GetEligibleOperations(core.LogicOfResource(res, info), core.StatusOfAsset(asset, c.Config, res))[op.Reason]; !exists {
		return &op, nil
	}

	// can only confirm when it has been like this for at least the configured delay
	var earliestConfirm time.Time
	switch op.Reason {
	case castellum.OperationReasonLow:
		earliestConfirm = op.CreatedAt.Add(time.Duration(res.LowDelaySeconds) * time.Second)
	case castellum.OperationReasonHigh:
		earliestConfirm = op.CreatedAt.Add(time.Duration(res.HighDelaySeconds) * time.Second)
	case castellum.OperationReasonCritical:
		// defense in depth - maybeCreateOperation() should already have confirmed this
		earliestConfirm = op.CreatedAt
	}
	if c.TimeNow().Before(earliestConfirm) {
		return &op, nil
	}

	previousState := op.State()
	confirmedAt := c.TimeNow()
	op.ConfirmedAt = &confirmedAt
	op.GreenlitAt = op.ConfirmedAt // right now, nothing requires operator approval
	_, err := tx.Update(&op)
	core.CountStateTransition(res, asset.UUID, previousState, op.State())
	return &op, err
}
