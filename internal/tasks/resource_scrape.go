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
		} else {
			resourceScrapeFailedCounter.With(labels).Inc()
		}
	}()

	manager := c.Team.ForAssetType(assetType)
	if manager == nil {
		panic(fmt.Sprintf("no asset manager for asset type %q", assetType))
	}

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
		return fmt.Errorf("cannot list %s assets in project %s: %s", assetType, res.ScopeUUID, err.Error())
	}
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
	resourceScrapedTime := c.TimeNow()

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
		status, err := manager.GetAssetStatus(res, assetUUID, nil)
		if err != nil {
			return fmt.Errorf("cannot query status of %s %s: %s", assetType, assetUUID, err.Error())
		}

		dbAsset := db.Asset{
			ResourceID:   res.ID,
			UUID:         assetUUID,
			Size:         status.Size,
			UsagePercent: status.UsagePercent,
			ScrapedAt:    c.TimeNow(),
			ExpectedSize: nil,
		}
		err = c.DB.Insert(&dbAsset)
		if err != nil {
			return err
		}
	}

	//record successful scrape
	_, err = c.DB.Exec("UPDATE resources SET scraped_at = $1 WHERE id = $2", resourceScrapedTime, res.ID)
	if err != nil {
		return err
	}

	return nil
}