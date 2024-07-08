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
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"time"

	"github.com/go-gorp/gorp/v3"
	"github.com/gorilla/mux"
	"github.com/sapcc/go-bits/gopherpolicy"
	"github.com/sapcc/go-bits/httpapi"
	"github.com/sapcc/go-bits/logg"
	"github.com/sapcc/go-bits/respondwith"

	"github.com/sapcc/castellum/internal/core"
	"github.com/sapcc/castellum/internal/db"
)

type handler struct {
	Config    core.Config
	DB        *gorp.DbMap
	Team      core.AssetManagerTeam
	Validator gopherpolicy.Validator
	Provider  core.ProviderClient

	// dependency injection slots (filled with doubles in tests)
	TimeNow func() time.Time
}

// NewAPI constructs the main httpapi.API for this package.
func NewHandler(cfg core.Config, dbi *gorp.DbMap, team core.AssetManagerTeam, validator gopherpolicy.Validator, provider core.ProviderClient) httpapi.API {
	return &handler{Config: cfg, DB: dbi, Team: team, Validator: validator, Provider: provider, TimeNow: time.Now}
}

// AddTo implements the httpapi.API interface.
func (h *handler) AddTo(router *mux.Router) {
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
		HandlerFunc(h.GetPendingOperations)
	router.Methods("GET").
		Path(`/v1/projects/{project_id}/resources/{asset_type}/operations/recently-failed`).
		HandlerFunc(h.GetRecentlyFailedOperations)
	router.Methods("GET").
		Path(`/v1/projects/{project_id}/resources/{asset_type}/operations/recently-succeeded`).
		HandlerFunc(h.GetRecentlySucceededOperations)

	router.Methods("GET").
		Path(`/v1/operations/pending`).
		HandlerFunc(h.GetPendingOperations)
	router.Methods("GET").
		Path(`/v1/operations/recently-failed`).
		HandlerFunc(h.GetRecentlyFailedOperations)
	router.Methods("GET").
		Path(`/v1/operations/recently-succeeded`).
		HandlerFunc(h.GetRecentlySucceededOperations)

	router.Methods("GET").
		Path(`/v1/admin/resource-scrape-errors`).
		HandlerFunc(h.GetResourceScrapeErrors)
	router.Methods("GET").
		Path(`/v1/admin/asset-scrape-errors`).
		HandlerFunc(h.GetAssetScrapeErrors)
	router.Methods("GET").
		Path(`/v1/admin/asset-resize-errors`).
		HandlerFunc(h.GetAssetResizeErrors)
}

// RequireJSON will parse the request body into the given data structure, or
// write an error response if that fails.
func RequireJSON(w http.ResponseWriter, r *http.Request, data any) bool {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	err := dec.Decode(data)
	if err != nil {
		http.Error(w, "request body is not valid JSON: "+err.Error(), http.StatusUnprocessableEntity)
		return false
	}
	return true
}

func respondWithForbidden(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusForbidden)
	w.Write([]byte("403 Forbidden")) //nolint:errcheck
}

func respondWithNotFound(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusNotFound)
	w.Write([]byte("404 Not found")) //nolint:errcheck
}

func (h handler) CheckToken(w http.ResponseWriter, r *http.Request) (string, *gopherpolicy.Token) {
	// for endpoints requiring the `project_id` variable, check that it's not empty
	projectUUID, projectScoped := mux.Vars(r)["project_id"]
	if projectScoped && projectUUID == "" {
		respondWithNotFound(w)
		return "", nil
	}
	// other endpoints might have a project ID in the `project` query argument instead
	if !projectScoped {
		if id := r.URL.Query().Get("project"); id != "" {
			projectScoped = true
			projectUUID = id
		}
	}

	token := h.Validator.CheckToken(r)
	// all project-scoped endpoints require the user to have access to the
	// selected project
	if projectScoped {
		projectExists, err := h.SetTokenToProjectScope(r.Context(), token, projectUUID)
		if respondwith.ErrorText(w, err) || !token.Require(w, "project:access") {
			return "", nil
		}

		// only report 404 after having checked access rules, otherwise we might leak
		// information about which projects exist to unauthorized users
		if !projectExists {
			respondWithNotFound(w)
			return "", nil
		}
	}

	return projectUUID, token
}

func (h handler) SetTokenToProjectScope(ctx context.Context, token *gopherpolicy.Token, projectUUID string) (projectExists bool, err error) {
	objectAttrs := map[string]string{
		"project_id":        projectUUID,
		"target.project.id": projectUUID,
	}

	project, err := h.Provider.GetProject(ctx, projectUUID)
	if err != nil {
		return false, err
	}
	projectExists = project != nil
	if project != nil {
		objectAttrs["target.project.name"] = project.Name
		objectAttrs["target.project.domain.id"] = project.DomainID

		domain, err := h.Provider.GetDomain(ctx, project.DomainID)
		if err != nil {
			return false, err
		}
		if domain == nil {
			projectExists = false
		} else {
			objectAttrs["target.project.domain.name"] = domain.Name
		}
	}

	token.Context.Request = objectAttrs
	logg.Debug("token has object attributes = %v", token.Context.Request)
	return projectExists, nil
}

func (h handler) LoadResource(w http.ResponseWriter, r *http.Request, projectUUID string, token *gopherpolicy.Token, createIfMissing bool) *db.Resource {
	assetType := db.AssetType(mux.Vars(r)["asset_type"])
	if assetType == "" {
		respondWithNotFound(w)
		return nil
	}
	manager, _ := h.Team.ForAssetType(assetType)
	if manager == nil {
		// only report resources when we have an asset manager configured
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
	if errors.Is(err, sql.ErrNoRows) {
		if createIfMissing {
			proj, err := h.Provider.GetProject(r.Context(), projectUUID)
			if respondwith.ErrorText(w, err) {
				return nil
			}
			return &db.Resource{
				ScopeUUID:  projectUUID,
				DomainUUID: proj.DomainID,
				AssetType:  assetType,
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

func (h handler) rejectIfResourceSeeded(w http.ResponseWriter, r *http.Request, res db.Resource) bool {
	proj, err := h.Provider.GetProject(r.Context(), res.ScopeUUID)
	if respondwith.ErrorText(w, err) {
		return true
	}
	if proj == nil {
		http.Error(w, "project not found", http.StatusNotFound)
		return true
	}

	domain, err := h.Provider.GetDomain(r.Context(), proj.DomainID)
	if respondwith.ErrorText(w, err) {
		return true
	}
	if domain == nil {
		http.Error(w, "domain not found", http.StatusNotFound)
		return true
	}

	if h.Config.IsSeededResource(*proj, *domain, res.AssetType) {
		msg := fmt.Sprintf("cannot %s this resource because configuration comes from a static seed", r.Method)
		http.Error(w, msg, http.StatusConflict)
		return true
	}
	return false
}

var (
	ageRx    = regexp.MustCompile(`^(0|[1-9][0-9]*)([mhd])$`)
	ageUnits = map[string]time.Duration{
		"m": time.Minute,
		"h": time.Hour,
		"d": 24 * time.Hour,
	}
)

// ParseAge parses a query parameter containing an age specification
// like `30m`, `12h` or `7d`.
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
