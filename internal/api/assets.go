// SPDX-FileCopyrightText: 2019 SAP SE
// SPDX-License-Identifier: Apache-2.0

package api

import (
	"database/sql"
	"errors"
	"net/http"
	"sort"

	"github.com/gorilla/mux"
	"github.com/sapcc/go-api-declarations/castellum"
	"github.com/sapcc/go-bits/httpapi"
	"github.com/sapcc/go-bits/respondwith"
	"github.com/sapcc/go-bits/sqlext"

	"github.com/sapcc/castellum/internal/core"
	"github.com/sapcc/castellum/internal/db"
)

// AssetFromDB converts a db.Asset into an api.Asset.
func AssetFromDB(asset db.Asset) castellum.Asset {
	a := castellum.Asset{
		UUID:         asset.UUID,
		Size:         asset.Size,
		MinimumSize:  asset.StrictMinimumSize,
		MaximumSize:  asset.StrictMaximumSize,
		UsagePercent: core.GetMultiUsagePercent(asset.Size, asset.Usage),
		Stale:        asset.ExpectedSize != nil,
	}
	if asset.ScrapeErrorMessage != "" {
		a.Checked = &castellum.Checked{
			ErrorMessage: asset.ScrapeErrorMessage,
		}
	}
	return a
}

// PendingOperationFromDB converts a db.PendingOperation into an api.Operation.
func PendingOperationFromDB(dbOp db.PendingOperation, assetID string, res *db.Resource) castellum.StandaloneOperation {
	op := castellum.StandaloneOperation{
		AssetID: assetID,
		Operation: castellum.Operation{
			State:   dbOp.State(),
			Reason:  dbOp.Reason,
			OldSize: dbOp.OldSize,
			NewSize: dbOp.NewSize,
			Created: castellum.OperationCreation{
				AtUnix:       dbOp.CreatedAt.Unix(),
				UsagePercent: core.GetMultiUsagePercent(dbOp.OldSize, dbOp.Usage),
			},
			Finished: nil,
		},
	}
	if res != nil {
		op.ProjectUUID = res.ScopeUUID
		op.AssetType = string(res.AssetType)
	}
	if dbOp.ConfirmedAt != nil {
		op.Confirmed = &castellum.OperationConfirmation{
			AtUnix: dbOp.ConfirmedAt.Unix(),
		}
	}
	if dbOp.GreenlitAt != nil {
		op.Greenlit = &castellum.OperationGreenlight{
			AtUnix:     dbOp.GreenlitAt.Unix(),
			ByUserUUID: dbOp.GreenlitByUserUUID,
		}
	}
	return op
}

// FinishedOperationFromDB converts a db.FinishedOperation into an api.Operation.
func FinishedOperationFromDB(dbOp db.FinishedOperation, assetID string, res *db.Resource) castellum.StandaloneOperation {
	op := castellum.StandaloneOperation{
		AssetID: assetID,
		Operation: castellum.Operation{
			State:   dbOp.State(),
			Reason:  dbOp.Reason,
			OldSize: dbOp.OldSize,
			NewSize: dbOp.NewSize,
			Created: castellum.OperationCreation{
				AtUnix:       dbOp.CreatedAt.Unix(),
				UsagePercent: core.GetMultiUsagePercent(dbOp.OldSize, dbOp.Usage),
			},
			Finished: &castellum.OperationFinish{
				AtUnix:       dbOp.FinishedAt.Unix(),
				ErrorMessage: dbOp.ErrorMessage,
			},
		},
	}
	if res != nil {
		op.ProjectUUID = res.ScopeUUID
		op.AssetType = string(res.AssetType)
	}
	if dbOp.ConfirmedAt != nil {
		op.Confirmed = &castellum.OperationConfirmation{
			AtUnix: dbOp.ConfirmedAt.Unix(),
		}
	}
	if dbOp.GreenlitAt != nil {
		op.Greenlit = &castellum.OperationGreenlight{
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

	assets := make([]castellum.Asset, len(dbAssets))
	for idx, dbAsset := range dbAssets {
		assets[idx] = AssetFromDB(dbAsset)
	}
	sort.Slice(assets, func(i, j int) bool { return assets[i].UUID < assets[j].UUID })

	result := struct {
		Assets []castellum.Asset `json:"assets"`
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
	if errors.Is(err, sql.ErrNoRows) {
		http.NotFound(w, r)
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
	switch {
	case errors.Is(err, sql.ErrNoRows):
		asset.PendingOperation = nil
	case respondwith.ErrorText(w, err):
		return
	default:
		op := PendingOperationFromDB(dbPendingOp, "", nil)
		asset.PendingOperation = &op
	}

	_, wantsFinishedOps := r.URL.Query()["history"]
	if wantsFinishedOps {
		var dbFinishedOps []db.FinishedOperation
		_, err = h.DB.Select(&dbFinishedOps,
			`SELECT * FROM finished_operations 
			 WHERE asset_id = $1 AND outcome != 'error-resolved'
			 ORDER BY finished_at`,
			dbAsset.ID)
		if respondwith.ErrorText(w, err) {
			return
		}
		finishedOps := make([]castellum.StandaloneOperation, len(dbFinishedOps))
		for idx, op := range dbFinishedOps {
			finishedOps[idx] = FinishedOperationFromDB(op, "", nil)
		}
		asset.FinishedOperations = &finishedOps
	}

	respondwith.JSON(w, http.StatusOK, asset)
}

var (
	checkLastFinishedOperationQuery = sqlext.SimplifyWhitespace(`
		SELECT a.id, fo.reason, fo.outcome
		FROM assets a
		LEFT JOIN finished_operations fo ON a.id = fo.asset_id
		WHERE a.resource_id = $1 AND a.uuid = $2
		ORDER BY fo.finished_at DESC LIMIT 1
	`)
)

func (h handler) PostAssetErrorResolved(w http.ResponseWriter, r *http.Request) {
	httpapi.IdentifyEndpoint(r, "/v1/projects/:id/assets/:type/:uuid/error-resolved")

	projectUUID, token := h.CheckToken(w, r)
	if token == nil {
		return
	}
	if !token.Require(w, "cluster:access") {
		return
	}
	dbResource := h.LoadResource(w, r, projectUUID, token, false)
	if dbResource == nil {
		return
	}

	var (
		assetID     int64
		lastReason  castellum.OperationReason
		lastOutcome castellum.OperationOutcome
	)
	err := h.DB.QueryRow(checkLastFinishedOperationQuery, dbResource.ID, mux.Vars(r)["asset_uuid"]).
		Scan(&assetID, &lastReason, &lastOutcome)
	if errors.Is(err, sql.ErrNoRows) {
		http.NotFound(w, r)
		return
	}
	if respondwith.ErrorText(w, err) {
		return
	}
	if lastOutcome != castellum.OperationOutcomeErrored {
		http.Error(w, "last operation of the asset is not in an errored state and cannot be resolved.", http.StatusConflict)
		return
	}

	now := h.TimeNow()
	userUUID := token.UserUUID()
	err = h.DB.Insert(&db.FinishedOperation{
		AssetID:            assetID,
		Reason:             lastReason,
		Outcome:            castellum.OperationOutcomeErrorResolved,
		CreatedAt:          now,
		ConfirmedAt:        &now,
		GreenlitAt:         &now,
		FinishedAt:         now,
		GreenlitByUserUUID: &userUUID,
	})

	if respondwith.ErrorText(w, err) {
		return
	}

	w.WriteHeader(http.StatusOK)
}
