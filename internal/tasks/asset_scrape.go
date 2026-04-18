// SPDX-FileCopyrightText: 2019 SAP SE or an SAP affiliate company
// SPDX-License-Identifier: Apache-2.0

package tasks

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/majewsky/gg/is"
	. "github.com/majewsky/gg/option"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/sapcc/go-api-declarations/castellum"
	"github.com/sapcc/go-bits/errext"
	"github.com/sapcc/go-bits/jobloop"
	"github.com/sapcc/go-bits/logg"
	"github.com/sapcc/go-bits/must"
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
	return (&jobloop.TxGuardedJob[*sql.Tx, db.Asset]{
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

func (c *Context) discoverAssetScrape(ctx context.Context, tx *sql.Tx, labels prometheus.Labels) (db.Asset, error) {
	return db.AssetStore.SelectOne(tx, scrapeAssetSearchQuery, c.TimeNow())
}

func convertErrNoRowsToNone[T any](value T, err error) (Option[T], error) {
	switch {
	case err == nil:
		return Some(value), nil
	case errors.Is(err, sql.ErrNoRows):
		return None[T](), nil
	default:
		return None[T](), err
	}
}

func (c *Context) processAssetScrape(ctx context.Context, tx *sql.Tx, asset db.Asset, labels prometheus.Labels) error {
	// find resource for asset
	res, err := db.ResourceStore.SelectOneWhere(tx, `id = $1`, asset.ResourceID)
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
	pendingOp, err := convertErrNoRowsToNone(db.PendingOperationStore.SelectOneWhere(tx, `asset_id = $1`, asset.ID))
	if err != nil {
		return err
	}

	// check asset status
	oldStatus := None[core.AssetStatus]()
	if !asset.NeverScraped {
		oldStatus = Some(core.AssetStatus{
			Size:              asset.Size,
			Usage:             asset.Usage,
			StrictMinimumSize: asset.StrictMinimumSize,
			StrictMaximumSize: asset.StrictMaximumSize,
		})
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
			dbErr := db.AssetStore.Delete(tx, asset)
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
		dbErr := db.AssetStore.Update(tx, asset)
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
		if val, ok := status.StrictMinimumSize.Unpack(); ok {
			valueLogStrings = append(valueLogStrings, fmt.Sprintf("minimum size = %d", val))
		}
		if val, ok := status.StrictMaximumSize.Unpack(); ok {
			valueLogStrings = append(valueLogStrings, fmt.Sprintf("maximum size = %d", val))
		}
		for metric, usage := range status.Usage {
			valueLogStrings = append(valueLogStrings, fmt.Sprintf(
				"usage%s = %.3f (%.3f%%)",
				core.Identifier(metric, "[%s]"), usage, core.GetUsagePercent(status.Size, usage),
			))
		}
		logg.Info("observed %s %s at size = %d, %s, resource config = %s",
			res.AssetType, asset.UUID, status.Size, strings.Join(valueLogStrings, ", "), must.Return(json.Marshal(core.LogicOfResource(res, info))),
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
	case asset.ExpectedSize == None[uint64]():
		// normal case: no resize operation has recently completed -> record
		// status.Size as actual size
		writeScrapeResults = true
	case asset.ExpectedSize == Some(status.Size):
		// a resize operation has completed, and now we're seeing the new size in
		// the backend -> record status.Size as actualSize and clear ExpectedSize
		writeScrapeResults = true
	case asset.Size != status.Size:
		// while waiting for a resize operation to be reflected in the backend,
		// we're observing an entirely different size (i.e. neither the operation's
		// OldSize nor its NewSize) -> assume that some other user changed the size
		// in parallel and take that new value as the actual size
		writeScrapeResults = true
	case asset.ResizedAt.IsSomeAnd(is.Before(c.TimeNow().Add(-1 * time.Hour))):
		// we waited for a resize operation to be reflected in the backend, but it
		// has been more than an hour since then -> assume that the resize was
		// interrupted in some way and resume normal behavior
		writeScrapeResults = true
		logg.Info("giving up on waiting for resize of %s %s from size = %d to size = %d to be completed in the backend",
			res.AssetType, asset.UUID,
			asset.Size, asset.ExpectedSize.UnwrapOrPanic("cannot be None"),
		)
	default:
		// we are waiting for a resize operation to reflect in the backend, but
		// the backend is still reporting the old size -> do not touch anything until the backend is showing the new size
		writeScrapeResults = false
		logg.Info("still waiting for resize of %s %s from size = %d to size = %d to be completed in the backend",
			res.AssetType, asset.UUID,
			asset.Size, asset.ExpectedSize.UnwrapOrPanic("cannot be None"),
		)
	}
	if writeScrapeResults {
		asset.Size = status.Size
		asset.Usage = status.Usage
		asset.StrictMinimumSize = status.StrictMinimumSize
		asset.StrictMaximumSize = status.StrictMaximumSize
		asset.ExpectedSize = None[uint64]()
		asset.ResizedAt = None[time.Time]()
	}

	// compute value of `asset.CriticalUsages` field (for reporting to admin only)
	var criticalUsageMetrics []string
	if res.CriticalThresholdPercent.IsNonZero() && res.MaximumSize.IsNoneOr(is.Above(asset.Size)) {
		usagePerc := core.GetMultiUsagePercent(asset.Size, asset.Usage)
		for _, metric := range info.UsageMetrics {
			if usagePerc[metric] >= res.CriticalThresholdPercent[metric] {
				criticalUsageMetrics = append(criticalUsageMetrics, string(metric))
			}
		}
	}
	asset.CriticalUsages = strings.Join(criticalUsageMetrics, ",")

	// update asset in DB
	err = db.AssetStore.Update(tx, asset)
	if err != nil {
		return err
	}
	if !writeScrapeResults {
		return tx.Commit()
	}

	// never touch operations in status "greenlit" - they may be executing on a
	// worker right now
	if pendingOp.IsSomeAnd(func(op db.PendingOperation) bool { return op.GreenlitAt.IsSomeAnd(is.NotAfter(c.TimeNow())) }) {
		return tx.Commit()
	}

	// if there is a pending operation, try to move it forward
	if op, ok := pendingOp.Unpack(); ok {
		pendingOp, err = c.maybeCancelOperation(tx, res, asset, info, op)
		if err != nil {
			return fmt.Errorf("cannot cancel operation on %s %s: %s", res.AssetType, asset.UUID, err.Error())
		}
	}
	if op, ok := pendingOp.Unpack(); ok {
		pendingOp, err = c.maybeUpdateOperation(tx, res, asset, info, op)
		if err != nil {
			return fmt.Errorf("cannot update operation on %s %s: %s", res.AssetType, asset.UUID, err.Error())
		}
	}
	if op, ok := pendingOp.Unpack(); ok {
		pendingOp, err = c.maybeConfirmOperation(tx, res, asset, info, op)
		if err != nil {
			return fmt.Errorf("cannot confirm operation on %s %s: %s", res.AssetType, asset.UUID, err.Error())
		}
	}

	// if there is no pending operation (or if we just cancelled it), see if we can start one
	if pendingOp.IsNone() {
		err = c.maybeCreateOperation(tx, res, asset, info)
		if err != nil {
			return fmt.Errorf("cannot create operation on %s %s: %s", res.AssetType, asset.UUID, err.Error())
		}
	}

	return tx.Commit()
}

func (c Context) maybeCreateOperation(tx *sql.Tx, res db.Resource, asset db.Asset, info core.AssetTypeInfo) error {
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
		op.ConfirmedAt = Some(op.CreatedAt)
		// right now, nothing requires operator approval
		op.GreenlitAt = op.ConfirmedAt
	}

	core.CountStateTransition(res, asset.UUID, castellum.OperationStateDidNotExist, op.State())
	_, err := db.PendingOperationStore.Insert(tx, op)
	return err
}

func (c Context) maybeCancelOperation(tx *sql.Tx, res db.Resource, asset db.Asset, info core.AssetTypeInfo, op db.PendingOperation) (Option[db.PendingOperation], error) {
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
		return Some(op), nil
	}

	core.CountStateTransition(res, asset.UUID, op.State(), castellum.OperationStateCancelled)
	finishedOp := op.IntoFinishedOperation(castellum.OperationOutcomeCancelled, c.TimeNow())
	err := db.PendingOperationStore.Delete(tx, op)
	if err != nil {
		return None[db.PendingOperation](), err
	}
	_, err = db.FinishedOperationStore.Insert(tx, finishedOp)
	return None[db.PendingOperation](), err
}

func (c Context) maybeUpdateOperation(tx *sql.Tx, res db.Resource, asset db.Asset, info core.AssetTypeInfo, op db.PendingOperation) (Option[db.PendingOperation], error) {
	// do not touch `op` unless the corresponding threshold is still being crossed
	eligibleFor := core.GetEligibleOperations(core.LogicOfResource(res, info), core.StatusOfAsset(asset, c.Config, res))
	newSize, exists := eligibleFor[op.Reason]
	if !exists {
		return Some(op), nil
	}

	// if the asset size has changed since the operation has been created
	// (because of resizes not performed by Castellum), calculate a new target size
	if op.NewSize == newSize {
		// nothing to do
		return Some(op), nil
	}
	op.NewSize = newSize
	err := db.PendingOperationStore.Update(tx, op)
	return Some(op), err
}

func (c Context) maybeConfirmOperation(tx *sql.Tx, res db.Resource, asset db.Asset, info core.AssetTypeInfo, op db.PendingOperation) (Option[db.PendingOperation], error) {
	// can only confirm when the corresponding threshold is still being crossed
	if _, exists := core.GetEligibleOperations(core.LogicOfResource(res, info), core.StatusOfAsset(asset, c.Config, res))[op.Reason]; !exists {
		return Some(op), nil
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
		return Some(op), nil
	}

	previousState := op.State()
	confirmedAt := c.TimeNow()
	op.ConfirmedAt = Some(confirmedAt)
	op.GreenlitAt = op.ConfirmedAt // right now, nothing requires operator approval
	err := db.PendingOperationStore.Update(tx, op)
	core.CountStateTransition(res, asset.UUID, previousState, op.State())
	return Some(op), err
}
