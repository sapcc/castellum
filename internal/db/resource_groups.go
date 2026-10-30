// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company
// SPDX-License-Identifier: Apache-2.0

package db

import (
	"context"
	"database/sql"
	"time"

	"github.com/sapcc/go-bits/sqlext"
	"go.xyrillian.de/gg/gsql"
)

// SQLExecutor is the subset of *sql.DB / *sql.Tx used by helpers below.
type SQLExecutor interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

var ensureResourceGroupInsertQuery = sqlext.SimplifyWhitespace(`
	INSERT INTO resource_groups (scope_uuid, domain_uuid, asset_manager, scrape_error_message, next_scrape_at, scrape_duration_secs)
	VALUES ($1, $2, $3, '', $4, 0)
	ON CONFLICT (scope_uuid, asset_manager) DO NOTHING
`)

// EnsureResourceGroup returns the ResourceGroup for the given (scopeUUID, assetManager), creating one if none exists yet.
// It reports whether the group existed before this call.
func EnsureResourceGroup(ctx context.Context, tx *gsql.Tx, scopeUUID, domainUUID, assetManager string, now time.Time) (ResourceGroup, bool, error) {
	groupOrNone, err := gsql.NoneIfNoRows(ResourceGroupStore.SelectOneWhere(ctx, tx,
		`scope_uuid = $1 AND asset_manager = $2 FOR UPDATE`, scopeUUID, assetManager))
	if err != nil {
		return ResourceGroup{}, false, err
	}
	if group, ok := groupOrNone.Unpack(); ok {
		return group, true, nil
	}

	_, err = tx.ExecContext(ctx, ensureResourceGroupInsertQuery, scopeUUID, domainUUID, assetManager, now.UTC())
	if err != nil {
		return ResourceGroup{}, false, err
	}
	group, err := ResourceGroupStore.SelectOneWhere(ctx, tx,
		`scope_uuid = $1 AND asset_manager = $2 FOR UPDATE`, scopeUUID, assetManager)
	return group, false, err
}

// GCResourceGroup deletes the ResourceGroup with the given ID if no Resources reference it anymore.
func GCResourceGroup(ctx context.Context, dbi SQLExecutor, groupID int64) error {
	_, err := dbi.ExecContext(ctx,
		`DELETE FROM resource_groups WHERE id = $1 AND NOT EXISTS (SELECT 1 FROM resources WHERE resource_group_id = $1)`,
		groupID,
	)
	return err
}
