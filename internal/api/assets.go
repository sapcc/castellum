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
	"net/http"
	"sort"

	"github.com/gorilla/mux"
	"github.com/sapcc/castellum/internal/db"
	"github.com/sapcc/go-bits/respondwith"
)

////////////////////////////////////////////////////////////////////////////////
// data types

//AssetInList is how a db.Asset looks like in the API endpoint where assets are
//listed.
type AssetInList struct {
	UUID string `json:"id"`
}

//Asset is how a db.Asset looks like in the API.
type Asset struct {
	UUID               string       `json:"id"`
	Size               uint64       `json:"size"`
	UsagePercent       uint32       `json:"usage_percent"`
	ScrapedAtUnix      int64        `json:"scraped_at"`
	Stale              bool         `json:"stale"`
	PendingOperation   *Operation   `json:"pending_operation,omitempty"`
	FinishedOperations *[]Operation `json:"finished_operations,omitempty"`
}

//Operation is how a db.PendingOperation or db.FinishedOperation looks like in
//the API.
type Operation struct {
	State     db.OperationState      `json:"state"`
	Reason    db.OperationReason     `json:"reason"`
	OldSize   uint64                 `json:"old_size"`
	NewSize   uint64                 `json:"new_size"`
	Created   OperationCreation      `json:"created"`
	Confirmed *OperationConfirmation `json:"confirmed,omitempty"`
	Greenlit  *OperationGreenlight   `json:"greenlit,omitempty"`
	Finished  *OperationFinish       `json:"finished,omitempty"`
}

//OperationCreation appears in type Operation.
type OperationCreation struct {
	AtUnix       int64  `json:"at"`
	UsagePercent uint32 `json:"usage_percent"`
}

//OperationConfirmation appears in type Operation.
type OperationConfirmation struct {
	AtUnix int64 `json:"at"`
}

//OperationGreenlight appears in type Operation.
type OperationGreenlight struct {
	AtUnix     int64   `json:"at"`
	ByUserUUID *string `json:"by_user,omitempty"`
}

//OperationFinish appears in type Operation.
type OperationFinish struct {
	AtUnix       int64  `json:"at"`
	ErrorMessage string `json:"error,omitempty"`
}

////////////////////////////////////////////////////////////////////////////////
// conversion and validation methods

//AssetListFromDB converts a []db.Asset into an []api.AssetInList.
func AssetListFromDB(assets []db.Asset) []AssetInList {
	result := make([]AssetInList, len(assets))
	for idx, asset := range assets {
		result[idx] = AssetInList{UUID: asset.UUID}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].UUID < result[j].UUID })
	return result
}

//AssetFromDB converts a db.Asset into an api.Asset.
func AssetFromDB(asset db.Asset) Asset {
	return Asset{
		UUID:          asset.UUID,
		Size:          asset.Size,
		UsagePercent:  asset.UsagePercent,
		ScrapedAtUnix: asset.ScrapedAt.Unix(),
		Stale:         asset.ExpectedSize != nil,
	}
}

//PendingOperationFromDB converts a db.PendingOperation into an api.Operation.
func PendingOperationFromDB(dbOp db.PendingOperation) *Operation {
	op := Operation{
		State:   dbOp.State(),
		Reason:  dbOp.Reason,
		OldSize: dbOp.OldSize,
		NewSize: dbOp.NewSize,
		Created: OperationCreation{
			AtUnix:       dbOp.CreatedAt.Unix(),
			UsagePercent: dbOp.UsagePercent,
		},
		Finished: nil,
	}
	if dbOp.ConfirmedAt != nil {
		op.Confirmed = &OperationConfirmation{
			AtUnix: dbOp.ConfirmedAt.Unix(),
		}
	}
	if dbOp.GreenlitAt != nil {
		op.Greenlit = &OperationGreenlight{
			AtUnix:     dbOp.GreenlitAt.Unix(),
			ByUserUUID: dbOp.GreenlitByUserUUID,
		}
	}
	return &op
}

//FinishedOperationFromDB converts a db.FinishedOperation into an api.Operation.
func FinishedOperationFromDB(dbOp db.FinishedOperation) Operation {
	op := Operation{
		State:   dbOp.State(),
		Reason:  dbOp.Reason,
		OldSize: dbOp.OldSize,
		NewSize: dbOp.NewSize,
		Created: OperationCreation{
			AtUnix:       dbOp.CreatedAt.Unix(),
			UsagePercent: dbOp.UsagePercent,
		},
		Finished: &OperationFinish{
			AtUnix:       dbOp.FinishedAt.Unix(),
			ErrorMessage: dbOp.ErrorMessage,
		},
	}
	if dbOp.ConfirmedAt != nil {
		op.Confirmed = &OperationConfirmation{
			AtUnix: dbOp.ConfirmedAt.Unix(),
		}
	}
	if dbOp.GreenlitAt != nil {
		op.Greenlit = &OperationGreenlight{
			AtUnix:     dbOp.GreenlitAt.Unix(),
			ByUserUUID: dbOp.GreenlitByUserUUID,
		}
	}
	return op
}

////////////////////////////////////////////////////////////////////////////////
// HTTP handlers

func (h handler) GetAssets(w http.ResponseWriter, r *http.Request) {
	projectUUID, token := h.CheckToken(w, r)
	if token == nil {
		return
	}
	dbResource := h.LoadResource(w, r, projectUUID, token)
	if dbResource == nil {
		return
	}

	var dbAssets []db.Asset
	_, err := h.DB.Select(&dbAssets,
		`SELECT * FROM assets WHERE resource_id = $1 ORDER BY uuid`, dbResource.ID)
	if respondwith.ErrorText(w, err) {
		return
	}

	result := struct {
		Assets []AssetInList `json:"assets"`
	}{
		Assets: AssetListFromDB(dbAssets),
	}
	respondwith.JSON(w, http.StatusOK, result)
}

func (h handler) GetAsset(w http.ResponseWriter, r *http.Request) {
	projectUUID, token := h.CheckToken(w, r)
	if token == nil {
		return
	}
	dbResource := h.LoadResource(w, r, projectUUID, token)
	if dbResource == nil {
		return
	}

	var dbAsset db.Asset
	err := h.DB.SelectOne(&dbAsset,
		`SELECT * FROM assets WHERE resource_id = $1 AND uuid = $2`,
		dbResource.ID, mux.Vars(r)["asset_uuid"])
	if err == sql.ErrNoRows {
		respondWithNotFound(w)
		return
	}
	if respondwith.ErrorText(w, err) {
		return
	}
	asset := AssetFromDB(dbAsset)

	var dbPendingOp db.PendingOperation
	err = h.DB.SelectOne(&dbPendingOp,
		`SELECT * FROM pending_operations WHERE asset_id = $1`,
		dbAsset.ID)
	if err == sql.ErrNoRows {
		asset.PendingOperation = nil
	} else if respondwith.ErrorText(w, err) {
		return
	} else {
		asset.PendingOperation = PendingOperationFromDB(dbPendingOp)
	}

	_, wantsFinishedOps := r.URL.Query()["history"]
	if wantsFinishedOps {
		var dbFinishedOps []db.FinishedOperation
		_, err = h.DB.Select(&dbFinishedOps,
			`SELECT * FROM finished_operations WHERE asset_id = $1`,
			dbAsset.ID)
		if respondwith.ErrorText(w, err) {
			return
		}
		finishedOps := make([]Operation, len(dbFinishedOps))
		for idx, op := range dbFinishedOps {
			finishedOps[idx] = FinishedOperationFromDB(op)
		}
		asset.FinishedOperations = &finishedOps
	}

	respondwith.JSON(w, http.StatusOK, asset)
}