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
	"github.com/sapcc/castellum/internal/db"
	"github.com/sapcc/go-bits/logg"
)

//query that finds the next resource that needs to be scraped
var scrapeResourceSearchQuery = `
	SELECT * FROM resources
	WHERE asset_type = $1 AND (scraped_at IS NULL or scraped_at < $2)
	-- order by update priority (first new resources, then outdated resources, then ID for deterministic test behavior)
	ORDER BY COALESCE(scraped_at, to_timestamp(-1)) ASC, id ASC
	LIMIT 1
`

//ScrapeNextResource finds the next resource of the given asset type that needs
//scraping and scrapes it, i.e. it looks for new and deleted assets within that
//resource.
//
//Returns sql.ErrNoRows when no resource needed scraping, to indicate to the
//caller to slow down.
func (c Context) ScrapeNextResource(assetType db.AssetType, maxScrapedAt time.Time) (returnedError error) {
	defer func() {
		labels := prometheus.Labels{"asset": string(assetType)}
		if returnedError == nil {
			resourceScrapeSuccessCounter.With(labels).Inc()
		} else if returnedError != sql.ErrNoRows {
			resourceScrapeFailedCounter.With(labels).Inc()
		}
	}()

	manager, _ := c.Team.ForAssetType(assetType)
	if manager == nil {
		panic(fmt.Sprintf("no asset manager for asset type %q", assetType))
	}

	logg.Debug("looking for %s resource to scrape, maxScrapedAt = %s", assetType, maxScrapedAt.String())
	var res db.Resource
	err := c.DB.SelectOne(&res, scrapeResourceSearchQuery, assetType, maxScrapedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			logg.Debug("no %s resources to scrape - slowing down...", assetType)
			return sql.ErrNoRows
		}
		return err
	}
	logg.Debug("scraping %s resource for project %s", assetType, res.ScopeUUID)

	//check which assets exist in this resource in OpenStack
	assetUUIDs, err := manager.ListAssets(res)
	if err != nil {
		//In case of error we update checked_at so that the next call continues
		//but leave scraped_at unchanged to indicate old data
		res.CheckedAt = c.TimeNow()
		res.ScrapeErrorMessage = err.Error()
		_, dbErr := c.DB.Update(&res)
		if dbErr != nil {
			return dbErr
		}

		return fmt.Errorf("cannot list %s assets in project %s: %s", string(assetType), res.ScopeUUID, err.Error())
	}
	logg.Debug("scraped %d assets for %s resource for project %s", len(assetUUIDs), assetType, res.ScopeUUID)
	isExistingAsset := make(map[string]bool, len(assetUUIDs))
	for _, uuid := range assetUUIDs {
		isExistingAsset[uuid] = true
	}

	//load existing asset entries from DB
	var dbAssets []db.Asset
	_, err = c.DB.Select(&dbAssets, `SELECT * FROM assets WHERE resource_id = $1`, res.ID)
	if err != nil {
		return err
	}

	now := c.TimeNow()
	res.CheckedAt = now
	res.ScrapedAt = &now
	res.ScrapeErrorMessage = ""

	//cleanup asset entries for deleted assets
	isAssetInDB := make(map[string]bool)
	for _, dbAsset := range dbAssets {
		isAssetInDB[dbAsset.UUID] = true
		if isExistingAsset[dbAsset.UUID] {
			continue
		}
		logg.Info("removing deleted %s asset from DB: UUID = %s, scope UUID = %s", assetType, dbAsset.UUID, res.ScopeUUID)
		_, err = c.DB.Delete(&dbAsset)
		if err != nil {
			return err
		}
	}

	//create entries for new assets
	for _, assetUUID := range assetUUIDs {
		if isAssetInDB[assetUUID] {
			continue
		}
		logg.Info("adding new %s asset to DB: UUID = %s, scope UUID = %s", assetType, assetUUID, res.ScopeUUID)
		now := c.TimeNow()
		dbAsset := db.Asset{
			ResourceID:   res.ID,
			UUID:         assetUUID,
			CheckedAt:    now,
			ExpectedSize: nil,
		}

		status, err := manager.GetAssetStatus(res, assetUUID, nil)
		labels := prometheus.Labels{"asset": string(assetType)}
		if err == nil {
			assetScrapeSuccessCounter.With(labels).Inc()
			dbAsset.Size = status.Size
			dbAsset.UsagePercent = status.UsagePercent
			dbAsset.AbsoluteUsage = status.AbsoluteUsage
			dbAsset.ScrapedAt = &now
		} else {
			assetScrapeFailedCounter.With(labels).Inc()
			dbAsset.Size = 0
			dbAsset.UsagePercent = 0
			dbAsset.AbsoluteUsage = nil
			dbAsset.ScrapeErrorMessage = err.Error()
		}

		err = c.DB.Insert(&dbAsset)
		if err != nil {
			return err
		}
	}

	//record successful scrape
	_, err = c.DB.Update(&res)
	if err != nil {
		return err

	}

	return nil
}
