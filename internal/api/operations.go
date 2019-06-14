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

	var ops []db.PendingOperation
	_, err := h.DB.Select(&ops, `
		SELECT o.* FROM pending_operations o
		  JOIN assets a ON a.id = o.asset_id
		 WHERE a.resource_id = $1
	`, dbResource.ID)
	if respondwith.ErrorText(w, err) {
		return
	}
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
