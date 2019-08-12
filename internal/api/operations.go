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

	"github.com/sapcc/castellum/internal/core"
	"github.com/sapcc/castellum/internal/db"
	"github.com/sapcc/go-bits/respondwith"
	"gopkg.in/gorp.v2"
)

func (h handler) GetPendingOperationsForResource(w http.ResponseWriter, r *http.Request) {
	projectUUID, token := h.CheckToken(w, r)
	if token == nil {
		return
	}
	dbResource, _ := h.LoadResource(w, r, projectUUID, token, false)
	if dbResource == nil {
		return
	}

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
	assetUUIDs, err := h.getAssetUUIDMap(*dbResource)
	if respondwith.ErrorText(w, err) {
		return
	}

	//render response body
	var response struct {
		PendingOperations []Operation `json:"pending_operations,keepempty"`
	}
	response.PendingOperations = make([]Operation, len(ops))
	for idx, op := range ops {
		response.PendingOperations[idx] = PendingOperationFromDB(op, assetUUIDs[op.AssetID])
	}
	respondwith.JSON(w, http.StatusOK, response)
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

func (h handler) GetRecentlyFailedOperationsForResource(w http.ResponseWriter, r *http.Request) {
	projectUUID, token := h.CheckToken(w, r)
	if token == nil {
		return
	}
	dbResource, _ := h.LoadResource(w, r, projectUUID, token, false)
	if dbResource == nil {
		return
	}

	failedOpsByAssetID, err := recentOperationQuery{
		DB:           h.DB,
		ResourceID:   dbResource.ID,
		Outcome:      db.OperationOutcomeFailed,
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
	relevantOps := []Operation{}
	for _, asset := range assets {
		op, exists := failedOpsByAssetID[asset.ID]
		if !exists {
			continue
		}
		if _, exists := core.GetEligibleOperations(*dbResource, asset)[op.Reason]; exists {
			relevantOps = append(relevantOps, FinishedOperationFromDB(op, asset.UUID))
		}
	}

	//render response body
	var response struct {
		Operations []Operation `json:"recently_failed_operations,keepempty"`
	}
	response.Operations = relevantOps
	respondwith.JSON(w, http.StatusOK, response)
}

func (h handler) GetRecentlySucceededOperationsForResource(w http.ResponseWriter, r *http.Request) {
	projectUUID, token := h.CheckToken(w, r)
	if token == nil {
		return
	}
	dbResource, _ := h.LoadResource(w, r, projectUUID, token, false)
	if dbResource == nil {
		return
	}
	maxAge, err := ParseAge(r.URL.Query(), "max-age", "1d")
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	maxFinishedAt := h.TimeNow().Add(-maxAge)

	//find succeeded operations
	succeededOpsByAssetID, err := recentOperationQuery{
		DB:           h.DB,
		ResourceID:   dbResource.ID,
		Outcome:      db.OperationOutcomeSucceeded,
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
	relevantOps := []Operation{}
	for _, asset := range assets {
		op, exists := succeededOpsByAssetID[asset.ID]
		if !exists || op.FinishedAt.Before(maxFinishedAt) {
			continue
		}
		relevantOps = append(relevantOps, FinishedOperationFromDB(op, asset.UUID))
	}

	//render response body
	var response struct {
		Operations []Operation `json:"recently_succeeded_operations,keepempty"`
	}
	response.Operations = relevantOps
	respondwith.JSON(w, http.StatusOK, response)
}

type recentOperationQuery struct {
	DB           *gorp.DbMap
	ResourceID   int64
	Outcome      db.OperationOutcome
	OverriddenBy string //contains a condition for an SQL WHERE clause
}

//This returns the most recent finished operation matching `outcome = $2` for
//each asset with `resource_id = $1`, unless there is a newer finished
//operation matching `%s`.
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
	 WHERE a.resource_id = $1 AND o.outcome = $2
`

func (q recentOperationQuery) Execute() (map[int64]db.FinishedOperation, error) {
	queryStr := fmt.Sprintf(recentOperationQueryStr, q.OverriddenBy)

	var matchingOps []db.FinishedOperation
	_, err := q.DB.Select(&matchingOps, queryStr, q.ResourceID, q.Outcome)
	if err != nil {
		return nil, err
	}

	result := make(map[int64]db.FinishedOperation, len(matchingOps))
	for _, op := range matchingOps {
		result[op.AssetID] = op
	}
	return result, nil
}
