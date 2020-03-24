/******************************************************************************
*
*  Copyright 2020 SAP SE
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

package api

import (
	"net/http"

	"github.com/sapcc/castellum/internal/db"
	"github.com/sapcc/go-bits/respondwith"
	"github.com/sapcc/go-bits/sre"
)

///////////////////////////////////////////////////////////////////////////////
// data types

// ResourceScrapeError is how a resource's scrape error appears in API.
type ResourceScrapeError struct {
	ProjectUUID string  `json:"project_id,omitempty"`
	DomainUUID  string  `json:"domain_id"`
	AssetType   string  `json:"asset_type"`
	Checked     Checked `json:"checked"`
}

// AssetScrapeError is how a resource's scrape error appears in API.
type AssetScrapeError struct {
	AssetUUID   string  `json:"asset_id"`
	ProjectUUID string  `json:"project_id,omitempty"`
	DomainUUID  string  `json:"domain_id"`
	AssetType   string  `json:"asset_type"`
	Checked     Checked `json:"checked"`
}

///////////////////////////////////////////////////////////////////////////////
// HTTP handlers

func (h handler) GetResourceScrapeErrors(w http.ResponseWriter, r *http.Request) {
	sre.IdentifyEndpoint(r, "/v1/admin/resource-scrape-errors")
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
	if respondwith.ErrorText(w, err) {
		return
	}

	var result struct {
		ResourceScrapeErrors []ResourceScrapeError `json:"resource_scrape_errors"`
	}
	for _, res := range dbResources {
		projectID := ""
		// .ScopeUUID is either a domain- or project UUID.
		if res.ScopeUUID != res.DomainUUID {
			projectID = res.ScopeUUID
		}

		result.ResourceScrapeErrors = append(result.ResourceScrapeErrors,
			ResourceScrapeError{
				ProjectUUID: projectID,
				DomainUUID:  res.DomainUUID,
				AssetType:   string(res.AssetType),
				Checked: Checked{
					AtUnix:       res.CheckedAt.Unix(),
					ErrorMessage: res.ScrapeErrorMessage,
				},
			})
	}

	respondwith.JSON(w, http.StatusOK, result)
}

func (h handler) GetAssetScrapeErrors(w http.ResponseWriter, r *http.Request) {
	sre.IdentifyEndpoint(r, "/v1/admin/asset-scrape-errors")
	_, token := h.CheckToken(w, r)
	if token == nil {
		return
	}
	if !token.Require(w, "cluster:access") {
		return
	}

	var result struct {
		AssetScrapeErrors []AssetScrapeError `json:"asset_scrape_errors"`
	}

	var dbResources []db.Resource
	_, err := h.DB.Select(&dbResources,
		`SELECT * FROM resources ORDER BY id`)
	if respondwith.ErrorText(w, err) {
		return
	}

	for _, res := range dbResources {
		var dbAssets []db.Asset
		_, err := h.DB.Select(&dbAssets,
			`SELECT * FROM assets
			 WHERE scrape_error_message != '' AND resource_id = $1
			 ORDER BY id
			`, res.ID)
		if respondwith.ErrorText(w, err) {
			return
		}

		projectID := ""
		// res.ScopeUUID is either a domain- or project UUID.
		if res.ScopeUUID != res.DomainUUID {
			projectID = res.ScopeUUID
		}

		for _, a := range dbAssets {
			result.AssetScrapeErrors = append(result.AssetScrapeErrors,
				AssetScrapeError{
					AssetUUID:   a.UUID,
					ProjectUUID: projectID,
					DomainUUID:  res.DomainUUID,
					AssetType:   string(res.AssetType),
					Checked: Checked{
						AtUnix:       a.CheckedAt.Unix(),
						ErrorMessage: a.ScrapeErrorMessage,
					},
				})
		}
	}

	respondwith.JSON(w, http.StatusOK, result)
}
