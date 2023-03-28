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
	"fmt"
	"net/http"
	"sort"
	"strings"
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
// data types

// Resource is how a db.Resource looks like in the API.

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

// UpdateDBResource updates the given db.Resource with the values provided in
// this api.Resource.
func UpdateDBResource(resource castellum.Resource, res *db.Resource, manager core.AssetManager, info core.AssetTypeInfo, maxAssetSize *uint64, existingResources []db.AssetType) (errors []string) {
	complain := func(msg string, args ...interface{}) {
		if len(args) > 0 {
			msg = fmt.Sprintf(msg, args...)
		}
		errors = append(errors, msg)
	}

	if resource.ConfigJSON == nil {
		res.ConfigJSON = ""
	} else {
		res.ConfigJSON = string(*resource.ConfigJSON)
	}
	err := manager.CheckResourceAllowed(res.AssetType, res.ScopeUUID, res.ConfigJSON, existingResources)
	if err != nil {
		complain(err.Error())
	}

	if resource.Checked != nil {
		complain("resource.checked cannot be set via the API")
	}
	if resource.AssetCount != 0 {
		complain("resource.asset_count cannot be set via the API")
	}

	//helper function to check the internal consistency of {Low,High,Critical}ThresholdPercent
	checkThresholdCommon := func(tType string, vals castellum.UsageValues) {
		isMetric := make(map[castellum.UsageMetric]bool)
		for _, metric := range info.UsageMetrics {
			isMetric[metric] = true
			val, exists := vals[metric]
			if !exists {
				complain("missing %s threshold%s", tType, core.Identifier(metric, " for %s"))
				continue
			}
			if val <= 0 || val > 100 {
				complain("%s threshold%s must be above 0%% and below or at 100%% of usage", tType, core.Identifier(metric, " for %s"))
			}
		}

		providedMetrics := make([]string, 0, len(vals))
		for metric := range vals {
			providedMetrics = append(providedMetrics, string(metric))
		}
		sort.Strings(providedMetrics) //for deterministic order of error messages in unit test
		for _, metric := range providedMetrics {
			if !isMetric[castellum.UsageMetric(metric)] {
				complain("%s threshold specified for metric %q which is not valid for this asset type", tType, metric)
			}
		}
	}

	if resource.LowThreshold == nil {
		res.LowThresholdPercent = info.MakeZeroUsageValues()
		res.LowDelaySeconds = 0
	} else {
		res.LowThresholdPercent = resource.LowThreshold.UsagePercent
		checkThresholdCommon("low", res.LowThresholdPercent)
		res.LowDelaySeconds = resource.LowThreshold.DelaySeconds
		if res.LowDelaySeconds == 0 {
			complain("delay for low threshold is missing")
		}
	}

	if resource.HighThreshold == nil {
		res.HighThresholdPercent = info.MakeZeroUsageValues()
		res.HighDelaySeconds = 0
	} else {
		res.HighThresholdPercent = resource.HighThreshold.UsagePercent
		checkThresholdCommon("high", res.HighThresholdPercent)
		res.HighDelaySeconds = resource.HighThreshold.DelaySeconds
		if res.HighDelaySeconds == 0 {
			complain("delay for high threshold is missing")
		}
	}

	if resource.CriticalThreshold == nil {
		res.CriticalThresholdPercent = info.MakeZeroUsageValues()
	} else {
		res.CriticalThresholdPercent = resource.CriticalThreshold.UsagePercent
		checkThresholdCommon("critical", res.CriticalThresholdPercent)
		if resource.CriticalThreshold.DelaySeconds != 0 {
			complain("critical threshold may not have a delay")
		}
	}

	if resource.LowThreshold != nil && resource.HighThreshold != nil {
		for _, metric := range info.UsageMetrics {
			if res.LowThresholdPercent[metric] > res.HighThresholdPercent[metric] {
				complain("low threshold%s must be below high threshold", core.Identifier(metric, " for %s"))
			}
		}
	}
	if resource.LowThreshold != nil && resource.CriticalThreshold != nil {
		for _, metric := range info.UsageMetrics {
			if res.LowThresholdPercent[metric] > res.CriticalThresholdPercent[metric] {
				complain("low threshold%s must be below critical threshold", core.Identifier(metric, " for %s"))
			}
		}
	}
	if resource.HighThreshold != nil && resource.CriticalThreshold != nil {
		for _, metric := range info.UsageMetrics {
			if res.HighThresholdPercent[metric] > res.CriticalThresholdPercent[metric] {
				complain("high threshold%s must be below critical threshold", core.Identifier(metric, " for %s"))
			}
		}
	}

	if resource.LowThreshold == nil && resource.HighThreshold == nil && resource.CriticalThreshold == nil {
		complain("at least one threshold must be configured")
	}

	res.SizeStepPercent = resource.SizeSteps.Percent
	res.SingleStep = resource.SizeSteps.Single
	if res.SingleStep {
		if res.SizeStepPercent != 0 {
			complain("percentage-based step may not be configured when single-step resizing is used")
		}
	} else {
		if res.SizeStepPercent == 0 {
			complain("size step must be greater than 0%")
		}
	}

	if resource.SizeConstraints == nil {
		if maxAssetSize != nil {
			complain(fmt.Sprintf("maximum size must be configured for %s", info.AssetType))
		}
		res.MinimumSize = nil
		res.MaximumSize = nil
		res.MinimumFreeSize = nil
	} else {
		res.MinimumSize = resource.SizeConstraints.Minimum
		if res.MinimumSize != nil && *res.MinimumSize == 0 {
			res.MinimumSize = nil
		}

		res.MaximumSize = resource.SizeConstraints.Maximum
		if res.MaximumSize == nil {
			if maxAssetSize != nil {
				complain(fmt.Sprintf("maximum size must be configured for %s", info.AssetType))
			}
		} else {
			min := uint64(0)
			if res.MinimumSize != nil {
				min = *res.MinimumSize
			}
			if *res.MaximumSize <= min {
				complain("maximum size must be greater than minimum size")
			}
			if maxAssetSize != nil && *res.MaximumSize > *maxAssetSize {
				complain(fmt.Sprintf("maximum size must be %d or less", *maxAssetSize))
			}
		}

		res.MinimumFreeSize = resource.SizeConstraints.MinimumFree
		if res.MinimumFreeSize != nil && *res.MinimumFreeSize == 0 {
			res.MinimumFreeSize = nil
		}
	}

	return
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

	var input castellum.Resource
	if !RequireJSON(w, r, &input) {
		return
	}

	manager, info := h.Team.ForAssetType(dbResource.AssetType)
	maxAssetSize := h.Config.MaxAssetSizeFor(info.AssetType)

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

	errs := UpdateDBResource(input, dbResource, manager, info, maxAssetSize, existingResources)
	if len(errs) > 0 {
		doAudit(http.StatusUnprocessableEntity)
		http.Error(w, strings.Join(errs, "\n"), http.StatusUnprocessableEntity)
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
