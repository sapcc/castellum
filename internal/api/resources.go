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
	"net/http"
	"strings"
	"time"

	"github.com/sapcc/castellum/internal/core"
	"github.com/sapcc/castellum/internal/db"
	"github.com/sapcc/go-bits/respondwith"
	"github.com/sapcc/go-bits/sre"
)

////////////////////////////////////////////////////////////////////////////////
// data types

//Resource is how a db.Resource looks like in the API.
type Resource struct {
	ScrapedAtUnix     *int64           `json:"scraped_at,omitempty"`
	AssetCount        int64            `json:"asset_count"`
	LowThreshold      *Threshold       `json:"low_threshold,omitempty"`
	HighThreshold     *Threshold       `json:"high_threshold,omitempty"`
	CriticalThreshold *Threshold       `json:"critical_threshold,omitempty"`
	SizeConstraints   *SizeConstraints `json:"size_constraints,omitempty"`
	SizeSteps         SizeSteps        `json:"size_steps"`
}

//Threshold appears in type Resource.
type Threshold struct {
	UsagePercent float64 `json:"usage_percent"`
	DelaySeconds uint32  `json:"delay_seconds,omitempty"`
}

//SizeSteps appears in type Resource.
type SizeSteps struct {
	Percent float64 `json:"percent,omitempty"`
	Single  bool    `json:"single,omitempty"`
}

//SizeConstraints appears in type Resource.
type SizeConstraints struct {
	Minimum     *uint64 `json:"minimum,omitempty"`
	Maximum     *uint64 `json:"maximum,omitempty"`
	MinimumFree *uint64 `json:"minimum_free,omitempty"`
}

////////////////////////////////////////////////////////////////////////////////
// conversion and validation methods

//ResourceFromDB converts a db.Resource into an api.Resource.
func (h handler) ResourceFromDB(res db.Resource) (Resource, error) {
	assetCount, err := h.DB.SelectInt(
		`SELECT COUNT(*) FROM assets WHERE resource_id = $1`,
		res.ID)
	if err != nil {
		return Resource{}, err
	}

	result := Resource{
		ScrapedAtUnix: timeOrNullToUnix(res.ScrapedAt),
		AssetCount:    assetCount,
		SizeSteps:     SizeSteps{Percent: res.SizeStepPercent, Single: res.SingleStep},
	}
	if res.LowThresholdPercent > 0 {
		result.LowThreshold = &Threshold{
			UsagePercent: res.LowThresholdPercent,
			DelaySeconds: res.LowDelaySeconds,
		}
	}
	if res.HighThresholdPercent > 0 {
		result.HighThreshold = &Threshold{
			UsagePercent: res.HighThresholdPercent,
			DelaySeconds: res.HighDelaySeconds,
		}
	}
	if res.CriticalThresholdPercent > 0 {
		result.CriticalThreshold = &Threshold{
			UsagePercent: res.CriticalThresholdPercent,
		}
	}
	if res.MinimumSize != nil || res.MaximumSize != nil || res.MinimumFreeSize != nil {
		result.SizeConstraints = &SizeConstraints{
			Minimum:     res.MinimumSize,
			Maximum:     res.MaximumSize,
			MinimumFree: res.MinimumFreeSize,
		}
	}
	return result, nil
}

//UpdateDBResource updates the given db.Resource with the values provided in
//this api.Resource.
func (r Resource) UpdateDBResource(res *db.Resource, info core.AssetTypeInfo) (errors []string) {
	complain := func(msg string) { errors = append(errors, msg) }

	if r.ScrapedAtUnix != nil {
		complain("resource.scraped_at cannot be set via the API")
	}
	if r.AssetCount != 0 {
		complain("resource.asset_count cannot be set via the API")
	}

	if r.LowThreshold == nil {
		res.LowThresholdPercent = 0
		res.LowDelaySeconds = 0
	} else {
		res.LowThresholdPercent = r.LowThreshold.UsagePercent
		res.LowDelaySeconds = r.LowThreshold.DelaySeconds
		if res.LowThresholdPercent < 0 || res.LowThresholdPercent > 100 {
			complain("low threshold must be between 0% and 100% of usage")
		}
		if res.LowDelaySeconds == 0 {
			complain("delay for low threshold is missing")
		}
	}

	if r.HighThreshold == nil {
		res.HighThresholdPercent = 0
		res.HighDelaySeconds = 0
	} else {
		res.HighThresholdPercent = r.HighThreshold.UsagePercent
		res.HighDelaySeconds = r.HighThreshold.DelaySeconds
		if res.HighThresholdPercent < 0 || res.HighThresholdPercent > 100 {
			complain("high threshold must be between 0% and 100% of usage")
		}
		if res.HighDelaySeconds == 0 {
			complain("delay for high threshold is missing")
		}
	}

	if r.CriticalThreshold == nil {
		res.CriticalThresholdPercent = 0
	} else {
		res.CriticalThresholdPercent = r.CriticalThreshold.UsagePercent
		if res.CriticalThresholdPercent < 0 || res.CriticalThresholdPercent > 100 {
			complain("critical threshold must be between 0% and 100% of usage")
		}
		if r.CriticalThreshold.DelaySeconds != 0 {
			complain("critical threshold may not have a delay")
		}
	}

	if r.LowThreshold != nil && r.HighThreshold != nil {
		if res.LowThresholdPercent > res.HighThresholdPercent {
			complain("low threshold must be below high threshold")
		}
	}
	if r.LowThreshold != nil && r.CriticalThreshold != nil {
		if res.LowThresholdPercent > res.CriticalThresholdPercent {
			complain("low threshold must be below critical threshold")
		}
	}
	if r.HighThreshold != nil && r.CriticalThreshold != nil {
		if res.HighThresholdPercent > res.CriticalThresholdPercent {
			complain("high threshold must be below critical threshold")
		}
	}

	if r.LowThreshold == nil && r.HighThreshold == nil && r.CriticalThreshold == nil {
		complain("at least one threshold must be configured")
	}

	res.SizeStepPercent = r.SizeSteps.Percent
	res.SingleStep = r.SizeSteps.Single
	if res.SingleStep {
		if !info.ReportsAbsoluteUsage {
			complain("cannot use single-step resizing: asset type does not report absolute usage")
		}
		if res.SizeStepPercent != 0 {
			complain("percentage-based step may not be configured when single-step resizing is used")
		}
	} else {
		if res.SizeStepPercent == 0 {
			complain("size step must be greater than 0%")
		}
	}

	if r.SizeConstraints == nil {
		res.MinimumSize = nil
		res.MaximumSize = nil
		res.MinimumFreeSize = nil
	} else {
		res.MinimumSize = r.SizeConstraints.Minimum
		if res.MinimumSize != nil && *res.MinimumSize == 0 {
			res.MinimumSize = nil
		}

		res.MaximumSize = r.SizeConstraints.Maximum
		if res.MaximumSize != nil {
			min := uint64(0)
			if res.MinimumSize != nil {
				min = *res.MinimumSize
			}
			if *res.MaximumSize <= min {
				complain("maximum size must be greater than minimum size")
			}
		}

		res.MinimumFreeSize = r.SizeConstraints.MinimumFree
		if res.MinimumFreeSize != nil && *res.MinimumFreeSize == 0 {
			res.MinimumFreeSize = nil
		}
		if res.MinimumFreeSize != nil && !info.ReportsAbsoluteUsage {
			complain("cannot use minimum free size constraint: asset type does not report absolute usage")
		}
	}

	return
}

////////////////////////////////////////////////////////////////////////////////
// HTTP handlers

func (h handler) GetProject(w http.ResponseWriter, r *http.Request) {
	sre.IdentifyEndpoint(r, "/v1/projects/:id")
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
		Resources map[db.AssetType]Resource `json:"resources"`
	}
	result.Resources = make(map[db.AssetType]Resource)
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
	sre.IdentifyEndpoint(r, "/v1/projects/:id/resources/:type")
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
	sre.IdentifyEndpoint(r, "/v1/projects/:id/resources/:type")
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

	manager, info := h.Team.ForAssetType(dbResource.AssetType)
	err := manager.CheckResourceAllowed(dbResource.AssetType, dbResource.ScopeUUID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnprocessableEntity)
		return
	}

	var input Resource
	if !RequireJSON(w, r, &input) {
		return
	}

	action := updateAction
	if dbResource.ID == 0 {
		action = enableAction
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

	errs := input.UpdateDBResource(dbResource, info)
	if len(errs) > 0 {
		doAudit(http.StatusUnprocessableEntity)
		http.Error(w, strings.Join(errs, "\n"), http.StatusUnprocessableEntity)
		return
	}
	if dbResource.ID == 0 {
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
	sre.IdentifyEndpoint(r, "/v1/projects/:id/resources/:type")
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
				action:       disableAction,
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
