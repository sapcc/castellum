// SPDX-FileCopyrightText: 2020 SAP SE or an SAP affiliate company
// SPDX-License-Identifier: Apache-2.0

package api

import (
	"context"
	"net/http"

	"github.com/lib/pq"
	"github.com/sapcc/go-api-declarations/castellum"
	"github.com/sapcc/go-bits/httpapi"
	"github.com/sapcc/go-bits/respondwith"
	"github.com/sapcc/go-bits/sqlext"
	"go.xyrillian.de/gg/gsql"

	"github.com/sapcc/castellum/internal/db"
)

// loadGroupsForResources loads all db.ResourceGroup rows referenced by the
// given resources in a single query, keyed by group ID.
func loadGroupsForResources(ctx context.Context, dbi gsql.Handle, dbResources []db.Resource) (map[int64]db.ResourceGroup, error) {
	resourcesByGroupID := resourcesByGroupIDIndex.Partition(dbResources)
	if len(resourcesByGroupID) == 0 {
		return make(map[int64]db.ResourceGroup), nil
	}
	groupIDs := make([]int64, 0, len(resourcesByGroupID))
	for id := range resourcesByGroupID {
		groupIDs = append(groupIDs, id)
	}
	return resourceGroupByIDIndex.IndexFrom(
		db.ResourceGroupStore.SelectWhere(ctx, dbi, `id = ANY($1)`, pq.Array(groupIDs)),
	)
}

// GetResourceScrapeErrors handles GET /v1/admin/resource-scrape-errors.
func (h handler) GetResourceScrapeErrors(w http.ResponseWriter, r *http.Request) {
	httpapi.IdentifyEndpoint(r, "/v1/admin/resource-scrape-errors")
	ctx := r.Context()
	_, token := h.CheckToken(w, r)
	if token == nil {
		return
	}
	if !token.Require(w, "cluster:access") {
		return
	}

	dbResources, err := db.ResourceStore.Select(ctx, h.DB,
		`SELECT res.* FROM resources res JOIN resource_groups rg ON res.resource_group_id = rg.id WHERE rg.scrape_error_message != '' ORDER BY rg.id, res.asset_type`,
	).Collect()
	if respondwith.ObfuscatedErrorText(w, err) {
		return
	}

	groupsByID, err := loadGroupsForResources(ctx, h.DB, dbResources)
	if respondwith.ObfuscatedErrorText(w, err) {
		return
	}

	resScrapeErrs := []castellum.ResourceScrapeError{}
	for _, res := range dbResources {
		group := groupsByID[res.ResourceGroupID]
		projectID := ""
		if group.ScopeUUID != group.DomainUUID {
			projectID = group.ScopeUUID
		}
		resScrapeErrs = append(resScrapeErrs, castellum.ResourceScrapeError{
			ProjectUUID: projectID,
			DomainUUID:  group.DomainUUID,
			AssetType:   string(res.AssetType),
			Checked: castellum.Checked{
				ErrorMessage: group.ScrapeErrorMessage,
			},
		})
	}

	respondwith.JSON(w, http.StatusOK, struct {
		ResourceScrapeErrors []castellum.ResourceScrapeError `json:"resource_scrape_errors"`
	}{resScrapeErrs})
}

// GetAssetScrapeErrors handles GET /v1/admin/asset-scrape-errors.
func (h handler) GetAssetScrapeErrors(w http.ResponseWriter, r *http.Request) {
	httpapi.IdentifyEndpoint(r, "/v1/admin/asset-scrape-errors")
	ctx := r.Context()
	_, token := h.CheckToken(w, r)
	if token == nil {
		return
	}
	if !token.Require(w, "cluster:access") {
		return
	}

	dbResources, err := db.ResourceStore.Select(ctx, h.DB, `SELECT * FROM resources ORDER BY id`).Collect()
	if respondwith.ObfuscatedErrorText(w, err) {
		return
	}

	groupsByID, err := loadGroupsForResources(ctx, h.DB, dbResources)
	if respondwith.ObfuscatedErrorText(w, err) {
		return
	}

	assetScrapeErrs := []castellum.AssetScrapeError{}
	for _, res := range dbResources {
		group := groupsByID[res.ResourceGroupID]
		projectID := ""
		if group.ScopeUUID != group.DomainUUID {
			projectID = group.ScopeUUID
		}

		err := db.AssetStore.SelectWhere(ctx, h.DB, `scrape_error_message != '' AND resource_id = $1 ORDER BY id`, res.ID).
			Foreach(func(a db.Asset) error {
				assetScrapeErrs = append(assetScrapeErrs, castellum.AssetScrapeError{
					AssetUUID:   a.UUID,
					ProjectUUID: projectID,
					DomainUUID:  group.DomainUUID,
					AssetType:   string(res.AssetType),
					Checked: castellum.Checked{
						ErrorMessage: a.ScrapeErrorMessage,
					},
				})
				return nil
			})
		if respondwith.ObfuscatedErrorText(w, err) {
			return
		}
	}

	respondwith.JSON(w, http.StatusOK, struct {
		AssetScrapeErrors []castellum.AssetScrapeError `json:"asset_scrape_errors"`
	}{assetScrapeErrs})
}

// We only care about assets that are still problematic.
// So we want to skip "errored" operations where a more recent operation
// on the same asset finished as "succeeded", "cancelled", or "failed".
var getAssetResizeErrorsQuery = sqlext.SimplifyWhitespace(`
	WITH latest_finished_operations AS (
		SELECT DISTINCT ON (asset_id) o.* FROM finished_operations o
		  JOIN assets a ON a.id = o.asset_id
		 WHERE a.resource_id = $1
		 ORDER BY o.asset_id, o.finished_at DESC
	)
	SELECT * FROM latest_finished_operations WHERE outcome = 'errored'
`)

// GetAssetResizeErrors handles GET /v1/admin/asset-resize-errors.
func (h handler) GetAssetResizeErrors(w http.ResponseWriter, r *http.Request) {
	httpapi.IdentifyEndpoint(r, "/v1/admin/asset-resize-errors")
	ctx := r.Context()
	_, token := h.CheckToken(w, r)
	if token == nil {
		return
	}
	if !token.Require(w, "cluster:access") {
		return
	}

	dbResources, err := db.ResourceStore.Select(ctx, h.DB, `SELECT * FROM resources ORDER BY id`).Collect()
	if respondwith.ObfuscatedErrorText(w, err) {
		return
	}

	groupsByID, err := loadGroupsForResources(ctx, h.DB, dbResources)
	if respondwith.ObfuscatedErrorText(w, err) {
		return
	}

	assetResizeErrs := []castellum.AssetResizeError{}
	for _, res := range dbResources {
		group := groupsByID[res.ResourceGroupID]
		projectID := ""
		if group.ScopeUUID != group.DomainUUID {
			projectID = group.ScopeUUID
		}

		// find asset UUIDs
		assetUUIDs, err := h.getAssetUUIDMap(res)
		if respondwith.ObfuscatedErrorText(w, err) {
			return
		}

		err = db.FinishedOperationStore.Select(ctx, h.DB, getAssetResizeErrorsQuery, res.ID).
			Foreach(func(o db.FinishedOperation) error {
				assetResizeErrs = append(assetResizeErrs, castellum.AssetResizeError{
					AssetUUID:   assetUUIDs[o.AssetID],
					ProjectUUID: projectID,
					DomainUUID:  group.DomainUUID,
					AssetType:   string(res.AssetType),
					OldSize:     o.OldSize,
					NewSize:     o.NewSize,
					Finished: castellum.OperationFinish{
						AtUnix:       o.FinishedAt.Unix(),
						ErrorMessage: o.ErrorMessage,
					},
				})
				return nil
			})
		if respondwith.ObfuscatedErrorText(w, err) {
			return
		}
	}

	respondwith.JSON(w, http.StatusOK, struct {
		AssetScrapeErrors []castellum.AssetResizeError `json:"asset_resize_errors"`
	}{assetResizeErrs})
}
