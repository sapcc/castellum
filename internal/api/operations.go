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
	"time"

	"github.com/sapcc/castellum/internal/core"
	"github.com/sapcc/castellum/internal/db"
	"github.com/sapcc/go-bits/respondwith"
)

func (h handler) GetPendingOperationsForResource(w http.ResponseWriter, r *http.Request) {
	projectUUID, token := h.CheckToken(w, r)
	if token == nil {
		return
	}
	dbResource := h.LoadResource(w, r, projectUUID, token, false)
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
	dbResource := h.LoadResource(w, r, projectUUID, token, false)
	if dbResource == nil {
		return
	}

	//find failed operations
	var failedOps []db.FinishedOperation
	_, err := h.DB.Select(&failedOps, `
		SELECT o.* FROM finished_operations o
		  JOIN assets a ON a.id = o.asset_id
		 WHERE a.resource_id = $1 AND o.outcome = 'failed'
	`, dbResource.ID)
	if respondwith.ErrorText(w, err) {
		return
	}

	//only consider the most recent failed operation for each asset
	failedOpsByAssetID := make(map[int64]db.FinishedOperation)
	for _, op := range failedOps {
		otherOp, exists := failedOpsByAssetID[op.AssetID]
		if !exists || otherOp.FinishedAt.Before(op.FinishedAt) {
			failedOpsByAssetID[op.AssetID] = op
		}
	}

	//filter failed operations where a later operation completed without error
	rows, err := h.DB.Query(`
		SELECT o.asset_id, MAX(o.finished_at) FROM finished_operations o
		  JOIN assets a on a.id = o.asset_id
		 WHERE a.resource_id = $1
		 GROUP BY o.asset_id
	`, dbResource.ID)
	if respondwith.ErrorText(w, err) {
		return
	}
	for rows.Next() {
		var (
			assetID       int64
			maxFinishedAt time.Time
		)
		err := rows.Scan(&assetID, &maxFinishedAt)
		if respondwith.ErrorText(w, err) {
			return
		}
		op, exists := failedOpsByAssetID[assetID]
		if exists && op.FinishedAt.Before(maxFinishedAt) {
			delete(failedOpsByAssetID, assetID)
		}
	}
	if respondwith.ErrorText(w, rows.Err()) {
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
		if core.GetMatchingReasons(*dbResource, asset)[op.Reason] {
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
