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

	"github.com/sapcc/castellum/internal/db"
	"github.com/sapcc/go-bits/respondwith"
)

////////////////////////////////////////////////////////////////////////////////
// data types

//Resource is how a db.Resource looks like in the API.
type Resource struct {
	ScrapedAtUnix     *int64     `json:"scraped_at,omitempty"`
	LowThreshold      *Threshold `json:"low_threshold,omitempty"`
	HighThreshold     *Threshold `json:"high_threshold,omitempty"`
	CriticalThreshold *Threshold `json:"critical_threshold,omitempty"`
	SizeSteps         SizeSteps  `json:"size_steps"`
}

//Threshold appears in type Resource.
type Threshold struct {
	UsagePercent uint32 `json:"usage_percent"`
	DelaySeconds uint32 `json:"delay_seconds,omitempty"`
}

//SizeSteps appears in type Resource.
type SizeSteps struct {
	Percent uint32 `json:"percent"`
}

////////////////////////////////////////////////////////////////////////////////
// conversion and validation methods

//ResourceFromDB converts a db.Resource into an api.Resource.
func ResourceFromDB(res db.Resource) Resource {
	result := Resource{
		ScrapedAtUnix: timeOrNullToUnix(res.ScrapedAt),
		SizeSteps:     SizeSteps{Percent: res.SizeStepPercent},
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
	return result
}

//UpdateDBResource updates the given db.Resource with the values provided in
//this api.Resource.
func (r Resource) UpdateDBResource(res *db.Resource) (errors []string) {
	complain := func(msg string) { errors = append(errors, msg) }

	if r.ScrapedAtUnix != nil {
		complain("resource.scraped_at cannot be set via the API")
	}

	if r.LowThreshold == nil {
		res.LowThresholdPercent = 0
		res.LowDelaySeconds = 0
	} else {
		res.LowThresholdPercent = r.LowThreshold.UsagePercent
		res.LowDelaySeconds = r.LowThreshold.DelaySeconds
		if res.LowThresholdPercent > 100 {
			complain("low threshold must be between 0% and 100% of usage")
		}
	}

	if r.HighThreshold == nil {
		res.HighThresholdPercent = 0
		res.HighDelaySeconds = 0
	} else {
		res.HighThresholdPercent = r.HighThreshold.UsagePercent
		res.HighDelaySeconds = r.HighThreshold.DelaySeconds
		if res.HighThresholdPercent > 100 {
			complain("high threshold must be between 0% and 100% of usage")
		}
	}

	if r.CriticalThreshold == nil {
		res.CriticalThresholdPercent = 0
	} else {
		res.CriticalThresholdPercent = r.CriticalThreshold.UsagePercent
		if res.CriticalThresholdPercent > 100 {
			complain("critical threshold must be between 0% and 100% of usage")
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
	if res.SizeStepPercent == 0 {
		complain("size step must be greater than 0%")
	}

	return
}

////////////////////////////////////////////////////////////////////////////////
// HTTP handlers

func (h handler) GetProject(w http.ResponseWriter, r *http.Request) {
	projectUUID, token := h.CheckToken(w, r)
	if token == nil {
		return
	}

	var dbResources []db.Resource
	_, err := h.DB.Select(&dbResources,
		`SELECT * FROM resources WHERE scope_uuid = $1`, projectUUID)
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
		if h.Team.ForAssetType(res.AssetType) == nil {
			continue
		}
		if token.Check(res.AssetType.PolicyRuleForRead()) {
			result.Resources[res.AssetType] = ResourceFromDB(res)
		}
	}

	respondwith.JSON(w, http.StatusOK, result)
}

func (h handler) GetResource(w http.ResponseWriter, r *http.Request) {
	projectUUID, token := h.CheckToken(w, r)
	if token == nil {
		return
	}
	dbResource := h.LoadResource(w, r, projectUUID, token)
	if dbResource == nil {
		return
	}

	respondwith.JSON(w, http.StatusOK, dbResource)
}

func (h handler) PutResource(w http.ResponseWriter, r *http.Request) {
	projectUUID, token := h.CheckToken(w, r)
	if token == nil {
		return
	}
	dbResource := h.LoadResource(w, r, projectUUID, token)
	if dbResource == nil {
		return
	}
	if !token.Require(w, dbResource.AssetType.PolicyRuleForWrite()) {
		return
	}

	var input Resource
	if !RequireJSON(w, r, &input) {
		return
	}

	errs := input.UpdateDBResource(dbResource)
	if len(errs) > 0 {
		http.Error(w, strings.Join(errs, "\n"), http.StatusUnprocessableEntity)
	}
	_, err := h.DB.Update(dbResource)
	if respondwith.ErrorText(w, err) {
		return
	}

	w.WriteHeader(http.StatusAccepted)
}

func (h handler) DeleteResource(w http.ResponseWriter, r *http.Request) {
	projectUUID, token := h.CheckToken(w, r)
	if token == nil {
		return
	}
	dbResource := h.LoadResource(w, r, projectUUID, token)
	if dbResource == nil {
		return
	}
	if !token.Require(w, dbResource.AssetType.PolicyRuleForWrite()) {
		return
	}

	_, err := h.DB.Exec(`DELETE FROM resources WHERE id = $1`, dbResource.ID)
	if respondwith.ErrorText(w, err) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
