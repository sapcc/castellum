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

package api

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"time"

	"github.com/sapcc/go-api-declarations/cadf"
	"github.com/sapcc/go-api-declarations/castellum"
	"github.com/sapcc/go-bits/httpapi"
	"github.com/sapcc/go-bits/respondwith"
	"github.com/sapcc/go-bits/sqlext"

	"github.com/sapcc/castellum/internal/core"
	"github.com/sapcc/castellum/internal/db"
)

////////////////////////////////////////////////////////////////////////////////
// conversion and validation methods

// ResourceFromDB converts a db.Resource into an castellum.Resource.
func (h handler) ResourceFromDB(res db.Resource) (castellum.Resource, error) {
	assetCount, err := h.DB.SelectInt(
		`SELECT COUNT(*) FROM assets WHERE resource_id = $1`,
		res.ID)
	if err != nil {
		return castellum.Resource{}, err
	}

	result := castellum.Resource{
		AssetCount: assetCount,
		SizeSteps:  castellum.SizeSteps{Percent: res.SizeStepPercent, Single: res.SingleStep},
	}
	if res.ConfigJSON != "" {
		val := json.RawMessage(res.ConfigJSON)
		result.ConfigJSON = &val
	}
	if res.ScrapeErrorMessage != "" {
		result.Checked = &castellum.Checked{
			ErrorMessage: res.ScrapeErrorMessage,
		}
	}
	if res.LowThresholdPercent.IsNonZero() {
		result.LowThreshold = &castellum.Threshold{
			UsagePercent: res.LowThresholdPercent,
			DelaySeconds: res.LowDelaySeconds,
		}
	}
	if res.HighThresholdPercent.IsNonZero() {
		result.HighThreshold = &castellum.Threshold{
			UsagePercent: res.HighThresholdPercent,
			DelaySeconds: res.HighDelaySeconds,
		}
	}
	if res.CriticalThresholdPercent.IsNonZero() {
		result.CriticalThreshold = &castellum.Threshold{
			UsagePercent: res.CriticalThresholdPercent,
		}
	}
	if res.MinimumSize != nil || res.MaximumSize != nil || res.MinimumFreeSize != nil {
		result.SizeConstraints = &castellum.SizeConstraints{
			Minimum:     res.MinimumSize,
			Maximum:     res.MaximumSize,
			MinimumFree: res.MinimumFreeSize,
		}
	}
	return result, nil
}

////////////////////////////////////////////////////////////////////////////////
// HTTP handlers

func (h handler) GetProject(w http.ResponseWriter, r *http.Request) {
	httpapi.IdentifyEndpoint(r, "/v1/projects/:id")
	projectUUID, token := h.CheckToken(w, r)
	if token == nil {
		return
	}

	var dbResources []db.Resource
	_, err := h.DB.Select(&dbResources,
		`SELECT * FROM resources WHERE scope_uuid = $1 ORDER BY asset_type`, projectUUID)
	if respondwith.ErrorText(w, err) {
		return
	}

	//show only those resources where there is a corresponding asset manager, and
	//where the user has permission to see the resource
	var result struct {
		Resources map[db.AssetType]castellum.Resource `json:"resources"`
	}
	result.Resources = make(map[db.AssetType]castellum.Resource)
	for _, res := range dbResources {
		manager, _ := h.Team.ForAssetType(res.AssetType)
		if manager == nil {
			continue
		}
		if token.Check(res.AssetType.PolicyRuleForRead()) {
			result.Resources[res.AssetType], err = h.ResourceFromDB(res)
			if respondwith.ErrorText(w, err) {
				return
			}
		}
	}

	respondwith.JSON(w, http.StatusOK, result)
}

func (h handler) GetResource(w http.ResponseWriter, r *http.Request) {
	httpapi.IdentifyEndpoint(r, "/v1/projects/:id/resources/:type")
	projectUUID, token := h.CheckToken(w, r)
	if token == nil {
		return
	}
	dbResource := h.LoadResource(w, r, projectUUID, token, false)
	if dbResource == nil {
		return
	}

	resource, err := h.ResourceFromDB(*dbResource)
	if respondwith.ErrorText(w, err) {
		return
	}
	respondwith.JSON(w, http.StatusOK, resource)
}

func (h handler) PutResource(w http.ResponseWriter, r *http.Request) {
	httpapi.IdentifyEndpoint(r, "/v1/projects/:id/resources/:type")
	requestTime := time.Now()
	projectUUID, token := h.CheckToken(w, r)
	if token == nil {
		return
	}
	dbResource := h.LoadResource(w, r, projectUUID, token, true)
	if dbResource == nil {
		return
	}
	if !token.Require(w, dbResource.AssetType.PolicyRuleForWrite()) {
		return
	}
	if h.rejectIfResourceSeeded(w, r, *dbResource) {
		return
	}

	var input castellum.Resource
	if !RequireJSON(w, r, &input) {
		return
	}

	action := cadf.UpdateAction
	if dbResource.ID == 0 {
		action = cadf.EnableAction
	}
	// this allows to reuse the logAndPublishEvent() with same parameters except reasonCode
	doAudit := func(statusCode int) {
		logAndPublishEvent(requestTime, r, token, statusCode,
			scalingEventTarget{
				action:            action,
				projectID:         projectUUID,
				resourceType:      string(dbResource.AssetType),
				attachmentContent: targetAttachmentContent{resource: input},
			})
	}

	var existingResources []db.AssetType
	err := sqlext.ForeachRow(h.DB,
		`SELECT asset_type FROM resources WHERE scope_uuid = $1`, []any{projectUUID},
		func(rows *sql.Rows) error {
			var assetType db.AssetType
			err := rows.Scan(&assetType)
			if err == nil {
				existingResources = append(existingResources, assetType)
			}
			return err
		},
	)
	if respondwith.ErrorText(w, err) {
		doAudit(http.StatusInternalServerError)
		return
	}

	errs := core.ApplyResourceSpecInto(dbResource, input, existingResources, h.Config, h.Team)
	if len(errs) > 0 {
		doAudit(http.StatusUnprocessableEntity)
		http.Error(w, errs.Join("\n"), http.StatusUnprocessableEntity)
		return
	}

	if dbResource.ID == 0 {
		dbResource.NextScrapeAt = time.Unix(0, 0).UTC() //give new resources a very early next_scrape_at to prioritize them in the scrape queue
		err = h.DB.Insert(dbResource)
	} else {
		_, err = h.DB.Update(dbResource)
	}
	if respondwith.ErrorText(w, err) {
		doAudit(http.StatusInternalServerError)
		return
	}

	doAudit(http.StatusAccepted)
	w.WriteHeader(http.StatusAccepted)
}

func (h handler) DeleteResource(w http.ResponseWriter, r *http.Request) {
	httpapi.IdentifyEndpoint(r, "/v1/projects/:id/resources/:type")
	requestTime := time.Now()
	projectUUID, token := h.CheckToken(w, r)
	if token == nil {
		return
	}
	dbResource := h.LoadResource(w, r, projectUUID, token, false)
	if dbResource == nil {
		return
	}
	if !token.Require(w, dbResource.AssetType.PolicyRuleForWrite()) {
		return
	}
	if h.rejectIfResourceSeeded(w, r, *dbResource) {
		return
	}

	// this allows to reuse the logAndPublishEvent() with same parameters except reasonCode
	doAudit := func(statusCode int) {
		logAndPublishEvent(requestTime, r, token, statusCode,
			scalingEventTarget{
				action:       cadf.DisableAction,
				projectID:    projectUUID,
				resourceType: string(dbResource.AssetType),
			})
	}

	_, err := h.DB.Exec(`DELETE FROM resources WHERE id = $1`, dbResource.ID)
	if respondwith.ErrorText(w, err) {
		doAudit(http.StatusInternalServerError)
		return
	}

	doAudit(http.StatusNoContent)
	w.WriteHeader(http.StatusNoContent)
}
