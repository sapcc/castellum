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
	"fmt"

	"github.com/go-gorp/gorp/v3"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/sapcc/go-bits/logg"
	"github.com/sapcc/go-bits/sqlext"

	"github.com/sapcc/castellum/internal/db"
	"github.com/sapcc/castellum/internal/jobloop"
)

// query that finds the next resource that needs to be scraped
//
// WARNING: This must be run in a transaction, or else `FOR UPDATE SKIP LOCKED`
// will not work as expected.
var scrapeResourceSearchQuery = sqlext.SimplifyWhitespace(`
	SELECT * FROM resources
	WHERE next_scrape_at <= $1
	-- order by update priority (first outdated resources, then by ID for deterministic test behavior)
	ORDER BY next_scrape_at ASC, id ASC
	-- prevent other job loops from working on the same asset concurrently
	FOR UPDATE SKIP LOCKED LIMIT 1
`)

// ResourceScrapingJob returns a job where each task is a resource that needs
// to be scraped. The task looks for new and deleted assets within that resource.
func (c *Context) ResourceScrapingJob(registerer prometheus.Registerer) jobloop.Job {
	return (&jobloop.TxGuardedJob[*gorp.Transaction, db.Resource]{
		Metadata: jobloop.JobMetadata{
			Description:     "resource scraping",
			ConcurrencySafe: true, //because "FOR UPDATE SKIP LOCKED" is used
			CounterOpts: prometheus.CounterOpts{
				Name: "castellum_resource_scrapes",
				Help: "Counter for resource scrape operations.",
			},
			CounterLabels: []string{"asset_type"},
		},
		BeginTx:     c.DB.Begin,
		DiscoverRow: c.discoverResourceScrape,
		ProcessRow:  c.processResourceScrape,
	}).Setup(registerer)
}

func (c *Context) discoverResourceScrape(tx *gorp.Transaction, labels prometheus.Labels) (res db.Resource, err error) {
	err = tx.SelectOne(&res, scrapeResourceSearchQuery, c.TimeNow())
	if err == nil {
		labels["asset_type"] = string(res.AssetType)
	}
	return res, err
}

func (c *Context) processResourceScrape(tx *gorp.Transaction, res db.Resource, labels prometheus.Labels) error {
	manager, info := c.Team.ForAssetType(res.AssetType)
	if manager == nil {
		return fmt.Errorf("no asset manager for asset type %q", res.AssetType)
	}
	logg.Debug("scraping %s resource in scope %s using manager %v", res.AssetType, res.ScopeUUID, manager)

	//check which assets exist in this resource in OpenStack
	startedAt := c.TimeNow()
	assetUUIDs, err := manager.ListAssets(res)
	finishedAt := c.TimeNow()
	if err != nil {
		//In case of error we update next_scrape_at so that the next call continues
		//but fill the error message to indicate old data
		res.ScrapeErrorMessage = err.Error()
		res.NextScrapeAt = c.TimeNow().Add(c.AddJitter(ResourceScrapeInterval))
		_, dbErr := tx.Update(&res)
		if dbErr != nil {
			return dbErr
		}
		dbErr = tx.Commit()
		if dbErr != nil {
			return dbErr
		}
		return fmt.Errorf("cannot list %s assets in scope %s: %s", string(res.AssetType), res.ScopeUUID, err.Error())
	}
	logg.Debug("scraped %d assets for %s resource for scope %s", len(assetUUIDs), res.AssetType, res.ScopeUUID)
	isExistingAsset := make(map[string]bool, len(assetUUIDs))
	for _, uuid := range assetUUIDs {
		isExistingAsset[uuid] = true
	}

	//load existing asset entries from DB
	var dbAssets []db.Asset
	_, err = tx.Select(&dbAssets, `SELECT * FROM assets WHERE resource_id = $1`, res.ID)
	if err != nil {
		return err
	}

	//cleanup asset entries for deleted assets
	isAssetInDB := make(map[string]bool)
	for _, dbAsset := range dbAssets {
		isAssetInDB[dbAsset.UUID] = true
		if isExistingAsset[dbAsset.UUID] {
			continue
		}
		logg.Info("removing deleted %s asset from DB: UUID = %s, scope UUID = %s", res.AssetType, dbAsset.UUID, res.ScopeUUID)
		_, err = tx.Delete(&dbAsset) //nolint:gosec // Delete is not holding onto the pointer after it returns
		if err != nil {
			return err
		}
	}

	//create entries for new assets
	for _, assetUUID := range assetUUIDs {
		if isAssetInDB[assetUUID] {
			continue
		}
		logg.Info("adding new %s asset to DB: UUID = %s, scope UUID = %s", res.AssetType, assetUUID, res.ScopeUUID)
		err = tx.Insert(&db.Asset{
			ResourceID:   res.ID,
			UUID:         assetUUID,
			Size:         0,
			Usage:        info.MakeZeroUsageValues(),
			NextScrapeAt: c.TimeNow(),
			NeverScraped: true,
		})
		if err != nil {
			return err
		}
	}

	//record successful scrape
	res.ScrapeErrorMessage = ""
	res.NextScrapeAt = finishedAt.Add(c.AddJitter(ResourceScrapeInterval))
	res.ScrapeDurationSecs = finishedAt.Sub(startedAt).Seconds()
	_, err = tx.Update(&res)
	if err != nil {
		return err
	}

	return tx.Commit()
}
