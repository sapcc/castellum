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
	"net/url"
	"regexp"
	"strconv"
	"time"

	"github.com/gorilla/mux"
	"github.com/sapcc/castellum/internal/core"
	"github.com/sapcc/castellum/internal/db"
	"github.com/sapcc/go-bits/gopherpolicy"
	"github.com/sapcc/go-bits/logg"
	"github.com/sapcc/go-bits/respondwith"
	"gopkg.in/gorp.v2"
)

type handler struct {
	DB        *gorp.DbMap
	Team      core.AssetManagerTeam
	Validator gopherpolicy.Validator
	Provider  core.ProviderClientInterface

	//dependency injection slots (filled with doubles in tests)
	TimeNow func() time.Time
}

//NewHandler constructs the main http.Handler for this package.
func NewHandler(dbi *gorp.DbMap, team core.AssetManagerTeam, validator gopherpolicy.Validator, provider core.ProviderClientInterface) http.Handler {
	h := &handler{DB: dbi, Team: team, Validator: validator, Provider: provider, TimeNow: time.Now}
	return h.BuildRouter()
}

func (h *handler) BuildRouter() http.Handler {
	router := mux.NewRouter()
	router.Methods("GET").
		Path(`/v1/projects/{project_id}`).
		HandlerFunc(h.GetProject)

	router.Methods("GET").
		Path(`/v1/projects/{project_id}/resources/{asset_type}`).
		HandlerFunc(h.GetResource)
	router.Methods("PUT").
		Path(`/v1/projects/{project_id}/resources/{asset_type}`).
		HandlerFunc(h.PutResource)
	router.Methods("DELETE").
		Path(`/v1/projects/{project_id}/resources/{asset_type}`).
		HandlerFunc(h.DeleteResource)

	router.Methods("GET").
		Path(`/v1/projects/{project_id}/assets/{asset_type}`).
		HandlerFunc(h.GetAssets)
	router.Methods("GET").
		Path(`/v1/projects/{project_id}/assets/{asset_type}/{asset_uuid}`).
		HandlerFunc(h.GetAsset)

	router.Methods("GET").
		Path(`/v1/projects/{project_id}/resources/{asset_type}/operations/pending`).
		HandlerFunc(h.GetPendingOperationsForResource)
	router.Methods("GET").
		Path(`/v1/projects/{project_id}/resources/{asset_type}/operations/recently-failed`).
		HandlerFunc(h.GetRecentlyFailedOperationsForResource)
	router.Methods("GET").
		Path(`/v1/projects/{project_id}/resources/{asset_type}/operations/recently-succeeded`).
		HandlerFunc(h.GetRecentlySucceededOperationsForResource)

	return router
}

//RequireJSON will parse the request body into the given data structure, or
//write an error response if that fails.
func RequireJSON(w http.ResponseWriter, r *http.Request, data interface{}) bool {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	err := dec.Decode(data)
	if err != nil {
		http.Error(w, "request body is not valid JSON: "+err.Error(), http.StatusUnprocessableEntity)
		return false
	}
	return true
}

func respondWithNotFound(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusNotFound)
	w.Write([]byte("404 Not found"))
}

func (h handler) CheckToken(w http.ResponseWriter, r *http.Request) (string, *gopherpolicy.Token) {
	//all endpoints include the `project_id` variable, so it must definitely be there
	projectUUID := mux.Vars(r)["project_id"]
	if projectUUID == "" {
		respondWithNotFound(w)
		return "", nil
	}

	//collect object attributes for policy check
	objectAttrs := map[string]string{
		"project_id":        projectUUID,
		"target.project.id": projectUUID,
	}
	project, err := h.Provider.GetProject(projectUUID)
	if respondwith.ErrorText(w, err) {
		return "", nil
	}
	if project != nil {
		objectAttrs["target.project.name"] = project.Name
		objectAttrs["target.project.domain.id"] = project.DomainID
		domain, err := h.Provider.GetDomain(project.DomainID)
		if respondwith.ErrorText(w, err) {
			return "", nil
		}
		if domain != nil {
			objectAttrs["target.project.domain.name"] = domain.Name
		}
	}

	//all endpoints are project-scoped, so we require the user to have access to
	//the selected project
	token := h.Validator.CheckToken(r)
	token.Context.Logger = logg.Debug
	token.Context.Request = objectAttrs
	logg.Debug("token has auth = %v", token.Context.Auth)
	logg.Debug("token has roles = %v", token.Context.Roles)
	logg.Debug("token has object attributes = %v", token.Context.Request)
	if !token.Require(w, "project:access") {
		return "", nil
	}
	return projectUUID, token
}

func (h handler) LoadResource(w http.ResponseWriter, r *http.Request, projectUUID string, token *gopherpolicy.Token, createIfMissing bool) *db.Resource {
	assetType := db.AssetType(mux.Vars(r)["asset_type"])
	if assetType == "" {
		respondWithNotFound(w)
		return nil
	}
	manager, _ := h.Team.ForAssetType(assetType)
	if manager == nil {
		//only report resources when we have an asset manager configured
		respondWithNotFound(w)
		return nil
	}

	if !token.Require(w, assetType.PolicyRuleForRead()) {
		return nil
	}

	var res db.Resource
	err := h.DB.SelectOne(&res,
		`SELECT * FROM resources WHERE scope_uuid = $1 AND asset_type = $2`,
		projectUUID, assetType,
	)
	if err == sql.ErrNoRows {
		if createIfMissing {
			return &db.Resource{
				ScopeUUID: projectUUID,
				AssetType: assetType,
			}
		}
		respondWithNotFound(w)
		return nil
	}
	if respondwith.ErrorText(w, err) {
		return nil
	}
	return &res
}

func timeOrNullToUnix(t *time.Time) *int64 {
	if t == nil {
		return nil
	}
	tst := t.Unix()
	return &tst
}

var (
	ageRx    = regexp.MustCompile(`^(0|[1-9][0-9]*)([mhd])$`)
	ageUnits = map[string]time.Duration{
		"m": time.Minute,
		"h": time.Hour,
		"d": 24 * time.Hour,
	}
)

//ParseAge parses a query parameter containing an age specification
//like `30m`, `12h` or `7d`.
func ParseAge(query url.Values, key, defaultValue string) (time.Duration, error) {
	spec := query.Get(key)
	if spec == "" {
		spec = defaultValue
	}
	match := ageRx.FindStringSubmatch(spec)
	if match == nil {
		return 0, fmt.Errorf(`invalid %s: expected a value like "30m", "12h" or "7d"; got %q`, key, spec)
	}
	val, err := strconv.ParseUint(match[1], 10, 64)
	if err != nil {
		return 0, err
	}
	return time.Duration(val) * ageUnits[match[2]], nil
}
