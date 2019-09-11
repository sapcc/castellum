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
	"fmt"
	"net/http"
	"strings"

	"github.com/gorilla/mux"
	"github.com/sapcc/castellum/internal/core"
	"github.com/sapcc/castellum/internal/db"
	"github.com/sapcc/go-bits/gopherpolicy"
	"github.com/sapcc/go-bits/respondwith"
	"gopkg.in/gorp.v2"
)

func (h handler) LoadMatchingResources(w http.ResponseWriter, r *http.Request) (map[int64]db.Resource, bool) {
	//CheckToken discovers project ID in both URL path and query
	var token *gopherpolicy.Token
	projectUUID, token := h.CheckToken(w, r)
	if token == nil {
		return nil, false
	}
	domainUUID := r.URL.Query().Get("domain")

	//get asset type from URL path or query
	assetTypeStr, exists := mux.Vars(r)["asset_type"]
	if exists {
		if assetTypeStr == "" {
			respondWithNotFound(w)
			return nil, false
		}
	} else {
		assetTypeStr = r.URL.Query().Get("asset-type")
	}
	if assetTypeStr != "" {
		manager, _ := h.Team.ForAssetType(db.AssetType(assetTypeStr))
		if manager == nil {
			//only report resources when we have an asset manager configured
			respondWithNotFound(w)
			return nil, false
		}
	}

	//find all matching resources
	var (
		sqlConditions []string
		sqlBindValues []interface{}
	)
	addSQLCondition := func(key string, value interface{}) {
		cond := fmt.Sprintf("%s = $%d", key, len(sqlBindValues)+1)
		sqlConditions = append(sqlConditions, cond)
		sqlBindValues = append(sqlBindValues, value)
	}
	if projectUUID != "" {
		addSQLCondition("scope_uuid", projectUUID)
	}
	if domainUUID != "" {
		addSQLCondition("domain_uuid", domainUUID)
	}
	if assetTypeStr != "" {
		addSQLCondition("asset_type", assetTypeStr)
	}
	if len(sqlConditions) == 0 {
		sqlConditions = []string{"TRUE"}
	}
	queryStr := `SELECT * FROM resources WHERE ` + strings.Join(sqlConditions, " AND ")
	var allResources []db.Resource
	_, err := h.DB.Select(&allResources, queryStr, sqlBindValues...)
	if respondwith.ErrorText(w, err) {
		return nil, false
	}

	//check if user has access to all these resources
	allowedResources := make(map[int64]db.Resource)
	canAccessAnyMatchingProject := false
	for _, res := range allResources {
		projectExists, err := h.SetTokenToProjectScope(token, res.ScopeUUID)
		if respondwith.ErrorText(w, err) {
			return nil, false
		}
		if !projectExists || !token.Check("project:access") {
			continue
		}
		canAccessAnyMatchingProject = true
		if token.Check(res.AssetType.PolicyRuleForRead()) {
			allowedResources[res.ID] = res
		}
	}

	//if there are no allowed resources, generate 4xx response
	if len(allowedResources) == 0 {
		if canAccessAnyMatchingProject {
			respondWithForbidden(w)
		} else {
			//do not leak information about project/resource existence to unauthorized users
			respondWithNotFound(w)
		}
		return nil, false
	}

	return allowedResources, true
}

func (h handler) GetPendingOperations(w http.ResponseWriter, r *http.Request) {
	dbResources, ok := h.LoadMatchingResources(w, r)
	if !ok {
		return
	}

	allOps := []Operation{}
	for _, dbResource := range dbResources {

		//find operations
		var ops []db.PendingOperation
		_, err := h.DB.Select(&ops, `
			SELECT o.* FROM pending_operations o
				JOIN assets a ON a.id = o.asset_id
			 WHERE a.resource_id = $1
		`, dbResource.ID)
		if respondwith.ErrorText(w, err) {
			return
		}

		//find asset UUIDs
		assetUUIDs, err := h.getAssetUUIDMap(dbResource)
		if respondwith.ErrorText(w, err) {
			return
		}

		//prepare for response body
		for _, op := range ops {
			allOps = append(allOps, PendingOperationFromDB(op, assetUUIDs[op.AssetID], &dbResource))
		}
	}

	respondwith.JSON(w, http.StatusOK, struct {
		PendingOperations []Operation `json:"pending_operations,keepempty"`
	}{allOps})
}

func (h handler) getAssetUUIDMap(res db.Resource) (map[int64]string, error) {
	rows, err := h.DB.Query(`SELECT id, uuid FROM assets WHERE resource_id = $1`, res.ID)
	if err != nil {
		return nil, err
	}

	assetUUIDs := make(map[int64]string)
	for rows.Next() {
		var (
			id   int64
			uuid string
		)
		err := rows.Scan(&id, &uuid)
		if err != nil {
			return nil, err
		}
		assetUUIDs[id] = uuid
	}
	return assetUUIDs, rows.Err()
}

func (h handler) GetRecentlyFailedOperations(w http.ResponseWriter, r *http.Request) {
	dbResources, ok := h.LoadMatchingResources(w, r)
	if !ok {
		return
	}

	relevantOps := []Operation{}
	for _, dbResource := range dbResources {

		failedOpsByAssetID, err := recentOperationQuery{
			DB:           h.DB,
			ResourceID:   dbResource.ID,
			Outcomes:     []db.OperationOutcome{db.OperationOutcomeFailed, db.OperationOutcomeErrored},
			OverriddenBy: `TRUE`,
		}.Execute()
		if respondwith.ErrorText(w, err) {
			return
		}

		//check if the assets in question are still eligible for resizing
		var assets []db.Asset
		_, err = h.DB.Select(&assets,
			`SELECT * FROM assets WHERE resource_id = $1 ORDER BY uuid`, dbResource.ID)
		if respondwith.ErrorText(w, err) {
			return
		}
		for _, asset := range assets {
			op, exists := failedOpsByAssetID[asset.ID]
			if !exists {
				continue
			}
			if _, exists := core.GetEligibleOperations(dbResource, asset)[op.Reason]; exists {
				relevantOps = append(relevantOps, FinishedOperationFromDB(op, asset.UUID, &dbResource))
			}
		}
	}

	respondwith.JSON(w, http.StatusOK, struct {
		Operations []Operation `json:"recently_failed_operations,keepempty"`
	}{relevantOps})
}

func (h handler) GetRecentlySucceededOperations(w http.ResponseWriter, r *http.Request) {
	dbResources, ok := h.LoadMatchingResources(w, r)
	if !ok {
		return
	}
	maxAge, err := ParseAge(r.URL.Query(), "max-age", "1d")
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	maxFinishedAt := h.TimeNow().Add(-maxAge)

	relevantOps := []Operation{}
	for _, dbResource := range dbResources {
		//find succeeded operations
		succeededOpsByAssetID, err := recentOperationQuery{
			DB:           h.DB,
			ResourceID:   dbResource.ID,
			Outcomes:     []db.OperationOutcome{db.OperationOutcomeSucceeded},
			OverriddenBy: fmt.Sprintf(`outcome != '%s'`, db.OperationOutcomeCancelled),
		}.Execute()
		if respondwith.ErrorText(w, err) {
			return
		}

		//apply filters and collect response data
		var assets []db.Asset
		_, err = h.DB.Select(&assets,
			`SELECT * FROM assets WHERE resource_id = $1 ORDER BY uuid`, dbResource.ID)
		if respondwith.ErrorText(w, err) {
			return
		}
		for _, asset := range assets {
			op, exists := succeededOpsByAssetID[asset.ID]
			if !exists || op.FinishedAt.Before(maxFinishedAt) {
				continue
			}
			relevantOps = append(relevantOps, FinishedOperationFromDB(op, asset.UUID, &dbResource))
		}
	}

	respondwith.JSON(w, http.StatusOK, struct {
		Operations []Operation `json:"recently_succeeded_operations,keepempty"`
	}{relevantOps})
}

type recentOperationQuery struct {
	DB           *gorp.DbMap
	ResourceID   int64
	Outcomes     []db.OperationOutcome
	OverriddenBy string //contains a condition for an SQL WHERE clause
}

//This returns the most recent finished operation with the outcomes `%[2]s` for
//each asset with `resource_id = $1`, unless there is a newer finished
//operation matching `%[1]s`.
var recentOperationQueryStr = `
	WITH tmp AS (
		SELECT asset_id, MAX(finished_at) AS max_finished_at
		  FROM finished_operations
		 WHERE %s
		 GROUP BY asset_id
	)
	SELECT o.* FROM finished_operations o
	  JOIN tmp ON tmp.asset_id = o.asset_id AND tmp.max_finished_at = o.finished_at
	  JOIN assets a ON a.id = o.asset_id
	 WHERE a.resource_id = $1 AND o.outcome IN ('%s')
`

func (q recentOperationQuery) Execute() (map[int64]db.FinishedOperation, error) {
	outcomes := make([]string, len(q.Outcomes))
	for idx, o := range q.Outcomes {
		outcomes[idx] = string(o)
	}

	queryStr := fmt.Sprintf(recentOperationQueryStr,
		q.OverriddenBy,
		strings.Join(outcomes, "', '"), //interpolating string constants into the query is safe here because q.Outcomes is always hardcoded
	)

	var matchingOps []db.FinishedOperation
	_, err := q.DB.Select(&matchingOps, queryStr, q.ResourceID)
	if err != nil {
		return nil, err
	}

	result := make(map[int64]db.FinishedOperation, len(matchingOps))
	for _, op := range matchingOps {
		result[op.AssetID] = op
	}
	return result, nil
}
