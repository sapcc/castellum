// SPDX-FileCopyrightText: 2019 SAP SE or an SAP affiliate company
// SPDX-License-Identifier: Apache-2.0

package api

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"time"

	"github.com/lib/pq"
	"github.com/sapcc/go-api-declarations/cadf"
	"github.com/sapcc/go-api-declarations/castellum"
	"github.com/sapcc/go-bits/audittools"
	"github.com/sapcc/go-bits/httpapi"
	"github.com/sapcc/go-bits/respondwith"
	"github.com/sapcc/go-bits/sqlext"
	"go.xyrillian.de/gg/gsql"
	. "go.xyrillian.de/gg/option"

	"github.com/sapcc/castellum/internal/core"
	"github.com/sapcc/castellum/internal/db"
)

////////////////////////////////////////////////////////////////////////////////
// conversion and validation methods

// ResourceFromDB converts a db.Resource into an castellum.Resource.
func (h handler) ResourceFromDB(group db.ResourceGroup, res db.Resource) (castellum.Resource, error) {
	var assetCount int64
	err := h.DB.QueryRow(`SELECT COUNT(*) FROM assets WHERE resource_id = $1`, res.ID).Scan(&assetCount)
	if err != nil {
		return castellum.Resource{}, err
	}

	result := castellum.Resource{
		AssetCount: assetCount,
		SizeSteps:  castellum.SizeSteps{Percent: res.SizeStepPercent, Single: res.SingleStep},
	}
	if res.ConfigJSON != "" {
		result.ConfigJSON = Some(json.RawMessage(res.ConfigJSON))
	}
	if group.ScrapeErrorMessage != "" {
		result.Checked = Some(castellum.Checked{
			ErrorMessage: group.ScrapeErrorMessage,
		})
	}
	if res.LowThresholdPercent.IsNonZero() {
		result.LowThreshold = Some(castellum.Threshold{
			UsagePercent: res.LowThresholdPercent,
			DelaySeconds: res.LowDelaySeconds,
		})
	}
	if res.HighThresholdPercent.IsNonZero() {
		result.HighThreshold = Some(castellum.Threshold{
			UsagePercent: res.HighThresholdPercent,
			DelaySeconds: res.HighDelaySeconds,
		})
	}
	if res.CriticalThresholdPercent.IsNonZero() {
		result.CriticalThreshold = Some(castellum.Threshold{
			UsagePercent: res.CriticalThresholdPercent,
		})
	}
	if res.MinimumSize.IsSome() || res.MaximumSize.IsSome() || res.MinimumFreeSize.IsSome() || res.MinimumFreeIsCritical {
		result.SizeConstraints = Some(castellum.SizeConstraints{
			Minimum:               res.MinimumSize,
			Maximum:               res.MaximumSize,
			MinimumFree:           res.MinimumFreeSize,
			MinimumFreeIsCritical: res.MinimumFreeIsCritical,
		})
	}
	return result, nil
}

////////////////////////////////////////////////////////////////////////////////
// HTTP handlers

// GetProject handles GET /v1/projects/:id.
func (h handler) GetProject(w http.ResponseWriter, r *http.Request) {
	httpapi.IdentifyEndpoint(r, "/v1/projects/:id")
	ctx := r.Context()
	projectUUID, token := h.CheckToken(w, r)
	if token == nil {
		return
	}

	// preload groups for this scope so we can pass them into ResourceFromDB
	groupsByID, err := resourceGroupByIDIndex.IndexFrom(
		db.ResourceGroupStore.SelectWhere(ctx, h.DB, `scope_uuid = $1`, projectUUID),
	)
	if respondwith.ObfuscatedErrorText(w, err) {
		return
	}

	// show only those resources where there is a corresponding asset manager, and
	// where the user has permission to see the resource
	var result struct {
		Resources map[db.AssetType]castellum.Resource `json:"resources"`
	}
	result.Resources = make(map[db.AssetType]castellum.Resource)
	groupIDs := make([]int64, 0, len(groupsByID))
	for id := range groupsByID {
		groupIDs = append(groupIDs, id)
	}
	err = db.ResourceStore.SelectWhere(ctx, h.DB, `resource_group_id = ANY($1) ORDER BY asset_type`, pq.Array(groupIDs)).
		Foreach(func(res db.Resource) error {
			manager, _ := h.Team.ForAssetType(res.AssetType)
			if manager == nil {
				return nil
			}
			if token.Check(res.AssetType.PolicyRuleForRead()) {
				group := groupsByID[res.ResourceGroupID]
				var err error
				result.Resources[res.AssetType], err = h.ResourceFromDB(group, res)
				if err != nil {
					return err
				}
			}
			return nil
		})
	if respondwith.ObfuscatedErrorText(w, err) {
		return
	}

	respondwith.JSON(w, http.StatusOK, result)
}

// GetResource handles GET /v1/projects/:id/resources/:type.
func (h handler) GetResource(w http.ResponseWriter, r *http.Request) {
	httpapi.IdentifyEndpoint(r, "/v1/projects/:id/resources/:type")
	projectUUID, token := h.CheckToken(w, r)
	if token == nil {
		return
	}
	dbGroup, dbResource := h.LoadResourceAndGroup(w, r, projectUUID, token, false)
	if dbResource == nil {
		return
	}

	resource, err := h.ResourceFromDB(*dbGroup, *dbResource)
	if respondwith.ObfuscatedErrorText(w, err) {
		return
	}
	respondwith.JSON(w, http.StatusOK, resource)
}

// PutResource handles PUT /v1/projects/:id/resources/:type.
func (h handler) PutResource(w http.ResponseWriter, r *http.Request) {
	httpapi.IdentifyEndpoint(r, "/v1/projects/:id/resources/:type")
	ctx := r.Context()
	requestTime := time.Now()
	projectUUID, token := h.CheckToken(w, r)
	if token == nil {
		return
	}
	dbGroup, dbResource := h.LoadResourceAndGroup(w, r, projectUUID, token, true)
	if dbResource == nil {
		return
	}
	if !token.Require(w, dbResource.AssetType.PolicyRuleForWrite()) {
		return
	}
	if h.rejectIfResourceSeeded(w, r, *dbGroup, *dbResource) {
		return
	}

	var input castellum.Resource
	if !RequireJSON(w, r, &input) {
		return
	}

	action := cadf.UpdateAction
	if dbResource.ID == 0 {
		action = cadf.EnableAction
	}
	// this allows to reuse h.Auditor.Record() with same parameters except reasonCode
	doAudit := func(statusCode int) {
		h.Auditor.Record(audittools.Event{
			Time:       requestTime,
			Request:    r,
			User:       token,
			ReasonCode: statusCode,
			Action:     cadf.Action(string(action) + "/" + string(dbResource.AssetType)),
			Target: scalingEventTarget{
				projectID: projectUUID,
				resource:  &input,
			},
		})
	}

	existingResources := make(map[db.AssetType]struct{})
	err := sqlext.ForeachRow(h.DB,
		`SELECT res.asset_type FROM resources res JOIN resource_groups rg ON res.resource_group_id = rg.id WHERE rg.scope_uuid = $1`,
		[]any{projectUUID},
		func(rows *sql.Rows) error {
			var assetType db.AssetType
			err := rows.Scan(&assetType)
			if err == nil {
				existingResources[assetType] = struct{}{}
			}
			return err
		},
	)
	if respondwith.ObfuscatedErrorText(w, err) {
		doAudit(http.StatusInternalServerError)
		return
	}

	errs := core.ApplyResourceSpecInto(r.Context(), *dbGroup, dbResource, input, existingResources, h.Config, h.Team)
	if len(errs) > 0 {
		doAudit(http.StatusUnprocessableEntity)
		http.Error(w, errs.Join("\n"), http.StatusUnprocessableEntity)
		return
	}

	err = h.DB.WithinTransaction(ctx, nil, func(tx *gsql.Tx) error {
		// lock the resource group, so that it cannot be delete it in a race condition
		groupOrNone, err := gsql.NoneIfNoRows(db.ResourceGroupStore.SelectOneWhere(ctx, tx,
			`scope_uuid = $1 AND asset_manager = $2 FOR UPDATE`,
			dbGroup.ScopeUUID, dbGroup.AssetManager,
		))
		if err != nil {
			return err
		}

		if group, ok := groupOrNone.Unpack(); ok {
			// reset NextScrapeAt so the new resource gets scraped next
			if dbResource.ID == 0 {
				group.NextScrapeAt = time.Unix(0, 0).UTC()
				if err := db.ResourceGroupStore.Update(ctx, tx, group); err != nil {
					return err
				}
			}
			*dbGroup = group
		} else {
			group, err := db.EnsureResourceGroup(ctx, tx, dbGroup.ScopeUUID, dbGroup.DomainUUID, dbGroup.AssetManager)
			if err != nil {
				return err
			}
			*dbGroup = group
		}

		dbResource.ResourceGroupID = dbGroup.ID
		return db.ResourceStore.Upsert(ctx, tx, dbResource)
	})
	if respondwith.ObfuscatedErrorText(w, err) {
		doAudit(http.StatusInternalServerError)
		return
	}

	doAudit(http.StatusAccepted)
	w.WriteHeader(http.StatusAccepted)
}

// DeleteResource handles DELETE /v1/projects/:id/resources/:type.
func (h handler) DeleteResource(w http.ResponseWriter, r *http.Request) {
	httpapi.IdentifyEndpoint(r, "/v1/projects/:id/resources/:type")
	ctx := r.Context()
	requestTime := time.Now()
	projectUUID, token := h.CheckToken(w, r)
	if token == nil {
		return
	}
	dbGroup, dbResource := h.LoadResourceAndGroup(w, r, projectUUID, token, false)
	if dbResource == nil {
		return
	}
	if !token.Require(w, dbResource.AssetType.PolicyRuleForWrite()) {
		return
	}
	if h.rejectIfResourceSeeded(w, r, *dbGroup, *dbResource) {
		return
	}

	// this allows to reuse h.Auditor.Record() with same parameters except reasonCode
	doAudit := func(statusCode int) {
		h.Auditor.Record(audittools.Event{
			Time:       requestTime,
			Request:    r,
			User:       token,
			ReasonCode: statusCode,
			Action:     cadf.Action("disable/" + string(dbResource.AssetType)),
			Target: scalingEventTarget{
				projectID: projectUUID,
			},
		})
	}

	err := h.DB.WithinTransaction(ctx, nil, func(tx *gsql.Tx) error {
		if err := db.ResourceStore.Delete(ctx, tx, *dbResource); err != nil {
			return err
		}
		return db.GCResourceGroup(ctx, tx, dbGroup.ID)
	})
	if respondwith.ObfuscatedErrorText(w, err) {
		doAudit(http.StatusInternalServerError)
		return
	}

	doAudit(http.StatusNoContent)
	w.WriteHeader(http.StatusNoContent)
}
