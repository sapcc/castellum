// SPDX-FileCopyrightText: 2019 SAP SE or an SAP affiliate company
// SPDX-License-Identifier: Apache-2.0

package tasks

import (
	"context"
	"fmt"
	"strings"

	"github.com/lib/pq"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/sapcc/go-bits/jobloop"
	"github.com/sapcc/go-bits/logg"
	"github.com/sapcc/go-bits/sqlext"
	"go.xyrillian.de/gg/gsql"
	"go.xyrillian.de/oblast"

	"github.com/sapcc/castellum/internal/db"
)

var assetsByResourceIDIndex = oblast.NewRuntimeIndex(func(a db.Asset) int64 { return a.ResourceID })

// query that finds the next resource group that needs to be scraped
//
// WARNING: This must be run in a transaction, or else `FOR UPDATE SKIP LOCKED`
// will not work as expected.
var scrapeResourceGroupSearchQuery = sqlext.SimplifyWhitespace(`
	SELECT * FROM resource_groups
	WHERE next_scrape_at <= $1
	-- order by update priority (first outdated groups, then by ID for deterministic test behavior)
	ORDER BY next_scrape_at ASC, id ASC
	-- prevent other job loops from working on the same group concurrently
	FOR UPDATE SKIP LOCKED LIMIT 1
`)

// ResourceScrapingJob returns a job where each task is a resource group that
// needs to be scraped. The task looks for new and deleted assets within all
// resources of that group.
func (c *Context) ResourceScrapingJob(registerer prometheus.Registerer) jobloop.Job {
	return (&jobloop.TxGuardedJob[*gsql.Tx, db.ResourceGroup]{
		Metadata: jobloop.JobMetadata{
			ReadableName:    "resource scraping",
			ConcurrencySafe: true, // because "FOR UPDATE SKIP LOCKED" is used
			CounterOpts: prometheus.CounterOpts{
				Name: "castellum_resource_scrapes",
				Help: "Counter for resource scrape operations.",
			},
			CounterLabels: []string{"asset_manager"},
		},
		BeginTx:     c.DB.Begin,
		DiscoverRow: c.discoverResourceGroupScrape,
		ProcessRow:  c.processResourceGroupScrape,
	}).Setup(registerer)
}

func (c *Context) discoverResourceGroupScrape(ctx context.Context, tx *gsql.Tx, labels prometheus.Labels) (db.ResourceGroup, error) {
	group, err := db.ResourceGroupStore.SelectOne(ctx, tx, scrapeResourceGroupSearchQuery, c.TimeNow())
	if err == nil {
		labels["asset_manager"] = group.AssetManager
	}
	return group, err
}

func (c *Context) processResourceGroupScrape(ctx context.Context, tx *gsql.Tx, group db.ResourceGroup, labels prometheus.Labels) error {
	// load all resources in this group
	dbResources, err := db.ResourceStore.SelectWhere(ctx, tx, `resource_group_id = $1`, group.ID).Collect()
	if err != nil {
		return err
	}

	// load all assets for this group
	resourceIDs := make([]int64, 0, len(dbResources))
	for _, res := range dbResources {
		resourceIDs = append(resourceIDs, res.ID)
	}
	assetsByResourceID, err := assetsByResourceIDIndex.PartitionFrom(
		db.AssetStore.SelectWhere(ctx, tx, `resource_id = ANY($1)`, pq.Array(resourceIDs)),
	)
	if err != nil {
		return err
	}

	// iterate every resource within the group and let its asset manager discover
	// the current asset UUIDs; the group row is locked, so no other worker can
	// scrape the same (scope, asset_manager) pair concurrently
	startedAt := c.TimeNow()
	scrapeErrs := []string{}
	for _, res := range dbResources {
		manager, info := c.Team.ForAssetType(res.AssetType)
		if manager == nil {
			scrapeErrs = append(scrapeErrs, fmt.Sprintf("no asset manager for asset type %q", res.AssetType))
			continue
		}
		logg.Debug("scraping %s resource in scope %s using manager %v", res.AssetType, group.ScopeUUID, manager)

		assetUUIDs, err := manager.ListAssets(ctx, group, res)
		if err != nil {
			scrapeErrs = append(scrapeErrs, fmt.Sprintf("cannot list %s assets in scope %s: %s", string(res.AssetType), group.ScopeUUID, err.Error()))
			continue
		}
		logg.Debug("scraped %d assets for %s resource for scope %s", len(assetUUIDs), res.AssetType, group.ScopeUUID)

		isExistingAsset := make(map[string]bool, len(assetUUIDs))
		for _, uuid := range assetUUIDs {
			isExistingAsset[uuid] = true
		}

		// cleanup asset entries for deleted assets
		dbAssets := assetsByResourceID[res.ID]
		isAssetInDB := make(map[string]bool)
		for _, dbAsset := range dbAssets {
			isAssetInDB[dbAsset.UUID] = true
			if isExistingAsset[dbAsset.UUID] {
				continue
			}
			logg.Info("removing deleted %s asset from DB: UUID = %s, scope UUID = %s", res.AssetType, dbAsset.UUID, group.ScopeUUID)
			err := db.AssetStore.Delete(ctx, tx, dbAsset)
			if err != nil {
				return err
			}
		}

		// create entries for new assets
		for _, assetUUID := range assetUUIDs {
			if isAssetInDB[assetUUID] {
				continue
			}
			logg.Info("adding new %s asset to DB: UUID = %s, scope UUID = %s", res.AssetType, assetUUID, group.ScopeUUID)
			err := db.AssetStore.Insert(ctx, tx, &db.Asset{
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
	}

	// record scrape outcome on the group
	finishedAt := c.TimeNow()
	group.NextScrapeAt = finishedAt.Add(c.AddJitter(ResourceScrapeInterval))
	group.ScrapeDurationSecs = finishedAt.Sub(startedAt).Seconds()
	if len(scrapeErrs) == 0 {
		group.ScrapeErrorMessage = ""
	} else {
		group.ScrapeErrorMessage = strings.Join(scrapeErrs, "; ")
	}
	err = db.ResourceGroupStore.Update(ctx, tx, group)
	if err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	if len(scrapeErrs) > 0 {
		return fmt.Errorf("errors during scrape of scope %s with manager %s: %s", group.ScopeUUID, group.AssetManager, group.ScrapeErrorMessage)
	}
	return nil
}
