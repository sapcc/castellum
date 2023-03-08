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
		http.Error(w, "no such share", http.StatusNotFound)
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
