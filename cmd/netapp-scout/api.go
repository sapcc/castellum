/*******************************************************************************
*
* Copyright 2023 SAP SE
*
* Licensed under the Apache License, Version 2.0 (the "License");
* you may not use this file except in compliance with the License.
* You should have received a copy of the License along with this
* program. If not, you may obtain a copy of the License at
*
*     http://www.apache.org/licenses/LICENSE-2.0
*
* Unless required by applicable law or agreed to in writing, software
* distributed under the License is distributed on an "AS IS" BASIS,
* WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
* See the License for the specific language governing permissions and
* limitations under the License.
*
*******************************************************************************/

package main

import (
	"errors"
	"fmt"
	"math"
	"net/http"

	"github.com/gorilla/mux"
	"github.com/sapcc/go-bits/httpapi"
	"github.com/sapcc/go-bits/respondwith"
)

// AddTo implements the httpapi.API interface.
func (e *Engine) AddTo(r *mux.Router) {
	r.Methods("GET").
		Path(`/v1/projects/{project_id}/shares/{share_id}`).
		HandlerFunc(e.handleGetShare)
	r.Methods("GET").
		Path(`/v1/projects/{project_id}/shares/{share_id}/exclusion-reasons`).
		HandlerFunc(e.handleGetExclusionReasons)
}

// CheckDataAvailability is used by the GET /healthcheck endpoint.
func (e *Engine) CheckDataAvailability() error {
	e.DataMutex.RLock()
	defer e.DataMutex.RUnlock()

	if e.Data == nil {
		return errors.New("initializing dataset")
	}
	return nil
}

func (e *Engine) handleGetShare(w http.ResponseWriter, r *http.Request) {
	httpapi.IdentifyEndpoint(r, "/v1/projects/:project_id/shares/:share_id")
	vars := mux.Vars(r)

	shareData := e.GetShareData(ShareIdentity{
		ProjectID: vars["project_id"],
		ShareID:   vars["share_id"],
	})
	if shareData == nil {
		http.Error(w, "no such share found in Prometheus", http.StatusNotFound)
		return
	}

	sizeBytes, err := shareData.GetSizeBytes()
	if respondwith.ErrorText(w, err) {
		return
	}
	usageBytes, err := shareData.GetUsageBytes()
	if respondwith.ErrorText(w, err) {
		return
	}

	// This endpoint makes a lot of noise that can easily obscure error logs.
	// Therefore, do not log the request if it comes out ok. We don't need the
	// request log here to see that the scout is working. We can rely on the
	// "observed nfs-shares" logs in the observer for that.
	httpapi.SkipRequestLog(r)

	respondwith.JSON(w, http.StatusOK, map[string]any{
		"size_gib":  uint64(math.Round(convertBytesToGiB(sizeBytes))),
		"usage_gib": convertBytesToGiB(usageBytes),
	})
}

func convertBytesToGiB(x float64) float64 {
	if x <= 0 {
		return 0
	}
	return x / 1024 / 1024 / 1024
}

func (e *Engine) handleGetExclusionReasons(w http.ResponseWriter, r *http.Request) {
	httpapi.IdentifyEndpoint(r, "/v1/projects/:project_id/shares/:share_id/exclusion-reasons")
	vars := mux.Vars(r)
	projectID, shareID := vars["project_id"], vars["share_id"]

	//grab a sample of this share's metrics to check the labels on it
	query := fmt.Sprintf(`netapp_volume_total_bytes{project_id=%q,share_id=%q}`, projectID, shareID)
	resultVector, err := e.PromClient.GetVector(query)
	if err != nil {
		msg := fmt.Sprintf("cannot check volume_type for share %q: %s", shareID, err.Error())
		http.Error(w, msg, http.StatusInternalServerError)
		return
	}

	//check the labels on the obtained samples
	hasDPMetrics := false
	hasNonDPMetrics := false
	isOnline := false
	for _, sample := range resultVector {
		if sample.Metric["volume_type"] == "dp" {
			hasDPMetrics = true
		} else {
			hasNonDPMetrics = true
			if sample.Metric["volume_state"] != "offline" {
				isOnline = true
			}
		}
	}

	// This endpoint makes a lot of noise that can easily obscure error logs.
	// Therefore, do not log the request if it comes out ok.
	httpapi.SkipRequestLog(r)

	//Any field in the response being "true" will cause castellum-observer to ignore this share entirely.
	respondwith.JSON(w, http.StatusOK, map[string]bool{
		//We want to ignore "shares" that are actually snapmirror targets (sapcc-specific
		//extension). The castellum-observer takes care of the checks on the level of
		//the Manila API. What we check here is that we actually have metrics of the
		//share itself (volume_type!="dp"), rather than of a snapmirror (volume_type="dp").
		//
		//It's possible that we have both metrics with volume_type="dp" and other
		//volume_type values, in which case we will only use the non-dp metrics.
		//This check is specifically about excluding shares that are *only* snapmirrors.
		//
		//NOTE: Not having any useful metrics at all is not a valid reason for
		//ignoring the share. If we lack metrics about a share, we want to be alerted
		//by the failing scrape.
		"volume_type = dp": hasDPMetrics && !hasNonDPMetrics,
		//Scraping will fail on shares in state "offline" because their size is
		//always reported as 0.
		"volume_state = offline": !isOnline,
	})
}
