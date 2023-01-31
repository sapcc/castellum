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
	"github.com/sapcc/go-bits/httpapi"
	"github.com/sapcc/go-bits/respondwith"

	"github.com/sapcc/castellum/internal/core"
	"github.com/sapcc/castellum/internal/db"
)

////////////////////////////////////////////////////////////////////////////////
// data types

// Asset is how a db.Asset looks like in the API.
type Asset struct {
	UUID               string         `json:"id"`
	Size               uint64         `json:"size,omitempty"`
	UsagePercent       db.UsageValues `json:"usage_percent"`
	Checked            *Checked       `json:"checked,omitempty"`
	Stale              bool           `json:"stale"`
	PendingOperation   *Operation     `json:"pending_operation,omitempty"`
	FinishedOperations *[]Operation   `json:"finished_operations,omitempty"`
}

// Checked appears in type Asset and Resource.
type Checked struct {
	ErrorMessage string `json:"error,omitempty"`
}

// Operation is how a db.PendingOperation or db.FinishedOperation looks like in
// the API.
type Operation struct {
	ProjectUUID string       `json:"project_id,omitempty"`
	AssetType   db.AssetType `json:"asset_type,omitempty"`
	AssetID     string       `json:"asset_id,omitempty"`
	//^ These fields are left empty when Operation appears inside type Asset.
	State     db.OperationState      `json:"state"`
	Reason    db.OperationReason     `json:"reason"`
	OldSize   uint64                 `json:"old_size"`
	NewSize   uint64                 `json:"new_size"`
	Created   OperationCreation      `json:"created"`
	Confirmed *OperationConfirmation `json:"confirmed,omitempty"`
	Greenlit  *OperationGreenlight   `json:"greenlit,omitempty"`
	Finished  *OperationFinish       `json:"finished,omitempty"`
}

// OperationCreation appears in type Operation.
type OperationCreation struct {
	AtUnix       int64          `json:"at"`
	UsagePercent db.UsageValues `json:"usage_percent"`
}

// OperationConfirmation appears in type Operation.
type OperationConfirmation struct {
	AtUnix int64 `json:"at"`
}

// OperationGreenlight appears in type Operation.
type OperationGreenlight struct {
	AtUnix     int64   `json:"at"`
	ByUserUUID *string `json:"by_user,omitempty"`
}

// OperationFinish appears in type Operation.
type OperationFinish struct {
	AtUnix       int64  `json:"at"`
	ErrorMessage string `json:"error,omitempty"`
}

////////////////////////////////////////////////////////////////////////////////
// conversion and validation methods

// AssetFromDB converts a db.Asset into an api.Asset.
func AssetFromDB(asset db.Asset) Asset {
	a := Asset{
		UUID:         asset.UUID,
		Size:         asset.Size,
		UsagePercent: core.GetMultiUsagePercent(asset.Size, asset.Usage),
		Stale:        asset.ExpectedSize != nil,
	}
	if asset.ScrapeErrorMessage != "" {
		a.Checked = &Checked{
			ErrorMessage: asset.ScrapeErrorMessage,
		}
	}
	return a
}

// PendingOperationFromDB converts a db.PendingOperation into an api.Operation.
func PendingOperationFromDB(dbOp db.PendingOperation, assetID string, res *db.Resource) Operation {
	op := Operation{
		AssetID: assetID,
		State:   dbOp.State(),
		Reason:  dbOp.Reason,
		OldSize: dbOp.OldSize,
		NewSize: dbOp.NewSize,
		Created: OperationCreation{
			AtUnix:       dbOp.CreatedAt.Unix(),
			UsagePercent: core.GetMultiUsagePercent(dbOp.OldSize, dbOp.Usage),
		},
		Finished: nil,
	}
	if res != nil {
		op.ProjectUUID = res.ScopeUUID
		op.AssetType = res.AssetType
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

// FinishedOperationFromDB converts a db.FinishedOperation into an api.Operation.
func FinishedOperationFromDB(dbOp db.FinishedOperation, assetID string, res *db.Resource) Operation {
	op := Operation{
		AssetID: assetID,
		State:   dbOp.State(),
		Reason:  dbOp.Reason,
		OldSize: dbOp.OldSize,
		NewSize: dbOp.NewSize,
		Created: OperationCreation{
			AtUnix:       dbOp.CreatedAt.Unix(),
			UsagePercent: core.GetMultiUsagePercent(dbOp.OldSize, dbOp.Usage),
		},
		Finished: &OperationFinish{
			AtUnix:       dbOp.FinishedAt.Unix(),
			ErrorMessage: dbOp.ErrorMessage,
		},
	}
	if res != nil {
		op.ProjectUUID = res.ScopeUUID
		op.AssetType = res.AssetType
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
	httpapi.IdentifyEndpoint(r, "/v1/projects/:id/assets/:type")
	projectUUID, token := h.CheckToken(w, r)
	if token == nil {
		return
	}
	dbResource := h.LoadResource(w, r, projectUUID, token, false)
	if dbResource == nil {
		return
	}

	var dbAssets []db.Asset
	_, err := h.DB.Select(&dbAssets,
		`SELECT * FROM assets WHERE resource_id = $1 ORDER BY uuid`, dbResource.ID)
	if respondwith.ErrorText(w, err) {
		return
	}

	assets := make([]Asset, len(dbAssets))
	for idx, dbAsset := range dbAssets {
		assets[idx] = AssetFromDB(dbAsset)
	}
	sort.Slice(assets, func(i, j int) bool { return assets[i].UUID < assets[j].UUID })

	result := struct {
		Assets []Asset `json:"assets"`
	}{assets}
	respondwith.JSON(w, http.StatusOK, result)
}

func (h handler) GetAsset(w http.ResponseWriter, r *http.Request) {
	httpapi.IdentifyEndpoint(r, "/v1/projects/:id/assets/:type/:uuid")
	projectUUID, token := h.CheckToken(w, r)
	if token == nil {
		return
	}
	dbResource := h.LoadResource(w, r, projectUUID, token, false)
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
		op := PendingOperationFromDB(dbPendingOp, "", nil)
		asset.PendingOperation = &op
	}

	_, wantsFinishedOps := r.URL.Query()["history"]
	if wantsFinishedOps {
		var dbFinishedOps []db.FinishedOperation
		_, err = h.DB.Select(&dbFinishedOps,
			`SELECT * FROM finished_operations WHERE asset_id = $1 ORDER BY finished_at`,
			dbAsset.ID)
		if respondwith.ErrorText(w, err) {
			return
		}
		finishedOps := make([]Operation, len(dbFinishedOps))
		for idx, op := range dbFinishedOps {
			finishedOps[idx] = FinishedOperationFromDB(op, "", nil)
		}
		asset.FinishedOperations = &finishedOps
	}

	respondwith.JSON(w, http.StatusOK, asset)
}
