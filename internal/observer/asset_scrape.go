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
	"errors"
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

	switch {
	case pendingOp == nil:
		return o.maybeCreateOperation(tx, res, asset)
	case pendingOp.ConfirmedAt == nil: //status "created"
		return o.maybeConfirmOrCancelOperation(tx, res, asset, *pendingOp)
	case pendingOp.GreenlitAt == nil: //status "confirmed"
		return o.maybeCancelOperation(tx, res, asset, *pendingOp)
	case pendingOp.GreenlitAt.After(o.TimeNow()): //status "will be greenlit"
		return o.maybeCancelOperation(tx, res, asset, *pendingOp)
	default:
		//do not touch operations in status "greenlit" - they may be executing on
		//a worker right now
	}

	return tx.Commit()
}

func (o Observer) maybeCreateOperation(tx *gorp.Transaction, res db.Resource, asset db.Asset) error {
	return errors.New("unimplemented") //TODO
}

func (o Observer) maybeConfirmOrCancelOperation(tx *gorp.Transaction, res db.Resource, asset db.Asset, pendingOp db.PendingOperation) error {
	return errors.New("unimplemented") //TODO
}

func (o Observer) maybeCancelOperation(tx *gorp.Transaction, res db.Resource, asset db.Asset, pendingOp db.PendingOperation) error {
	return errors.New("unimplemented") //TODO
}
