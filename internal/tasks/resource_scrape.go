// SPDX-FileCopyrightText: 2019 SAP SE or an SAP affiliate company
// SPDX-License-Identifier: Apache-2.0

package tasks

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/sapcc/go-bits/jobloop"
	"github.com/sapcc/go-bits/logg"
	"github.com/sapcc/go-bits/sqlext"

	"github.com/sapcc/castellum/internal/db"
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
	return (&jobloop.TxGuardedJob[*sql.Tx, db.Resource]{
		Metadata: jobloop.JobMetadata{
			ReadableName:    "resource scraping",
			ConcurrencySafe: true, // because "FOR UPDATE SKIP LOCKED" is used
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

func (c *Context) discoverResourceScrape(ctx context.Context, tx *sql.Tx, labels prometheus.Labels) (db.Resource, error) {
	res, err := db.ResourceStore.SelectOne(tx, scrapeResourceSearchQuery, c.TimeNow())
	if err == nil {
		labels["asset_type"] = string(res.AssetType)
	}
	return res, err
}

func (c *Context) processResourceScrape(ctx context.Context, tx *sql.Tx, res db.Resource, labels prometheus.Labels) error {
	manager, info := c.Team.ForAssetType(res.AssetType)
	if manager == nil {
		return fmt.Errorf("no asset manager for asset type %q", res.AssetType)
	}
	logg.Debug("scraping %s resource in scope %s using manager %v", res.AssetType, res.ScopeUUID, manager)

	// check which assets exist in this resource in OpenStack
	startedAt := c.TimeNow()
	assetUUIDs, err := manager.ListAssets(ctx, res)
	finishedAt := c.TimeNow()
	if err != nil {
		// In case of error we update next_scrape_at so that the next call continues
		// but fill the error message to indicate old data
		res.ScrapeErrorMessage = err.Error()
		res.NextScrapeAt = c.TimeNow().Add(c.AddJitter(ResourceScrapeInterval))
		dbErr := db.ResourceStore.Update(tx, res)
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

	// load existing asset entries from DB
	dbAssets, err := db.AssetStore.SelectWhere(tx, `resource_id = $1`, res.ID)
	if err != nil {
		return err
	}

	// cleanup asset entries for deleted assets
	isAssetInDB := make(map[string]bool)
	for _, dbAsset := range dbAssets {
		isAssetInDB[dbAsset.UUID] = true
		if isExistingAsset[dbAsset.UUID] {
			continue
		}
		logg.Info("removing deleted %s asset from DB: UUID = %s, scope UUID = %s", res.AssetType, dbAsset.UUID, res.ScopeUUID)
		err := db.AssetStore.Delete(tx, dbAsset)
		if err != nil {
			return err
		}
	}

	// create entries for new assets
	for _, assetUUID := range assetUUIDs {
		if isAssetInDB[assetUUID] {
			continue
		}
		logg.Info("adding new %s asset to DB: UUID = %s, scope UUID = %s", res.AssetType, assetUUID, res.ScopeUUID)
		_, err := db.AssetStore.Insert(tx, db.Asset{
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

	// record successful scrape
	res.ScrapeErrorMessage = ""
	res.NextScrapeAt = finishedAt.Add(c.AddJitter(ResourceScrapeInterval))
	res.ScrapeDurationSecs = finishedAt.Sub(startedAt).Seconds()
	err = db.ResourceStore.Update(tx, res)
	if err != nil {
		return err
	}

	return tx.Commit()
}
