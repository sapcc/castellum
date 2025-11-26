// SPDX-FileCopyrightText: 2020 SAP SE or an SAP affiliate company
// SPDX-License-Identifier: Apache-2.0

package api

import (
	"net/http"

	"github.com/sapcc/go-api-declarations/castellum"
	"github.com/sapcc/go-bits/httpapi"
	"github.com/sapcc/go-bits/respondwith"

	"github.com/sapcc/castellum/internal/db"
)

func (h handler) GetResourceScrapeErrors(w http.ResponseWriter, r *http.Request) {
	httpapi.IdentifyEndpoint(r, "/v1/admin/resource-scrape-errors")
	_, token := h.CheckToken(w, r)
	if token == nil {
		return
	}
	if !token.Require(w, "cluster:access") {
		return
	}

	var dbResources []db.Resource
	_, err := h.DB.Select(&dbResources,
		`SELECT * FROM resources WHERE scrape_error_message != '' ORDER BY id`)
	if respondwith.ObfuscatedErrorText(w, err) {
		return
	}

	resScrapeErrs := []castellum.ResourceScrapeError{}
	for _, res := range dbResources {
		projectID := ""
		// .ScopeUUID is either a domain- or project UUID.
		if res.ScopeUUID != res.DomainUUID {
			projectID = res.ScopeUUID
		}

		resScrapeErrs = append(resScrapeErrs,
			castellum.ResourceScrapeError{
				ProjectUUID: projectID,
				DomainUUID:  res.DomainUUID,
				AssetType:   string(res.AssetType),
				Checked: castellum.Checked{
					ErrorMessage: res.ScrapeErrorMessage,
				},
			})
	}

	respondwith.JSON(w, http.StatusOK, struct {
		ResourceScrapeErrors []castellum.ResourceScrapeError `json:"resource_scrape_errors"`
	}{resScrapeErrs})
}

func (h handler) GetAssetScrapeErrors(w http.ResponseWriter, r *http.Request) {
	httpapi.IdentifyEndpoint(r, "/v1/admin/asset-scrape-errors")
	_, token := h.CheckToken(w, r)
	if token == nil {
		return
	}
	if !token.Require(w, "cluster:access") {
		return
	}

	var dbResources []db.Resource
	_, err := h.DB.Select(&dbResources,
		`SELECT * FROM resources ORDER BY id`)
	if respondwith.ObfuscatedErrorText(w, err) {
		return
	}

	assetScrapeErrs := []castellum.AssetScrapeError{}
	for _, res := range dbResources {
		var dbAssets []db.Asset
		_, err := h.DB.Select(&dbAssets, `
			SELECT * FROM assets
			 WHERE scrape_error_message != '' AND resource_id = $1
			 ORDER BY id
			`, res.ID)
		if respondwith.ObfuscatedErrorText(w, err) {
			return
		}

		projectID := ""
		// res.ScopeUUID is either a domain- or project UUID.
		if res.ScopeUUID != res.DomainUUID {
			projectID = res.ScopeUUID
		}

		for _, a := range dbAssets {
			assetScrapeErrs = append(assetScrapeErrs,
				castellum.AssetScrapeError{
					AssetUUID:   a.UUID,
					ProjectUUID: projectID,
					DomainUUID:  res.DomainUUID,
					AssetType:   string(res.AssetType),
					Checked: castellum.Checked{
						ErrorMessage: a.ScrapeErrorMessage,
					},
				})
		}
	}

	respondwith.JSON(w, http.StatusOK, struct {
		AssetScrapeErrors []castellum.AssetScrapeError `json:"asset_scrape_errors"`
	}{assetScrapeErrs})
}

func (h handler) GetAssetResizeErrors(w http.ResponseWriter, r *http.Request) {
	httpapi.IdentifyEndpoint(r, "/v1/admin/asset-resize-errors")
	_, token := h.CheckToken(w, r)
	if token == nil {
		return
	}
	if !token.Require(w, "cluster:access") {
		return
	}

	var dbResources []db.Resource
	_, err := h.DB.Select(&dbResources,
		`SELECT * FROM resources ORDER BY id`)
	if respondwith.ObfuscatedErrorText(w, err) {
		return
	}

	assetResizeErrs := []castellum.AssetResizeError{}
	for _, res := range dbResources {
		var ops []db.FinishedOperation
		// We only care about assets that are still problematic.
		// So we want to skip "errored" operations where a more recent operation
		// on the same asset finished as "succeeded", "cancelled", or "failed".
		_, err := h.DB.Select(&ops, `
			WITH latest_finished_operations AS (
				SELECT DISTINCT ON (asset_id) o.* FROM finished_operations o
					JOIN assets a ON a.id = o.asset_id
				 WHERE a.resource_id = $1
				 ORDER BY o.asset_id, o.finished_at DESC
			)
			SELECT * FROM latest_finished_operations WHERE outcome = 'errored'
		`, res.ID)
		if respondwith.ObfuscatedErrorText(w, err) {
			return
		}

		projectID := ""
		// res.ScopeUUID is either a domain- or project UUID.
		if res.ScopeUUID != res.DomainUUID {
			projectID = res.ScopeUUID
		}

		// find asset UUIDs
		assetUUIDs, err := h.getAssetUUIDMap(res)
		if respondwith.ObfuscatedErrorText(w, err) {
			return
		}

		for _, o := range ops {
			assetResizeErrs = append(assetResizeErrs,
				castellum.AssetResizeError{
					AssetUUID:   assetUUIDs[o.AssetID],
					ProjectUUID: projectID,
					DomainUUID:  res.DomainUUID,
					AssetType:   string(res.AssetType),
					OldSize:     o.OldSize,
					NewSize:     o.NewSize,
					Finished: castellum.OperationFinish{
						AtUnix:       o.FinishedAt.Unix(),
						ErrorMessage: o.ErrorMessage,
					},
				})
		}
	}

	respondwith.JSON(w, http.StatusOK, struct {
		AssetScrapeErrors []castellum.AssetResizeError `json:"asset_resize_errors"`
	}{assetResizeErrs})
}
