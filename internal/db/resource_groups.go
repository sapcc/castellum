// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company
// SPDX-License-Identifier: Apache-2.0

package db

import (
	"context"
	"database/sql"
	"time"

	"go.xyrillian.de/gg/gsql"
)

// SQLExecutor is the subset of *sql.DB / *sql.Tx used by helpers below.
// Both *gsql.DB and *gsql.Tx satisfy this via their embedded std sql types.
type SQLExecutor interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// Handle combines gsql.Handle with SQLExecutor, which is required to run raw
// ON CONFLICT inserts alongside the oblast-generated store queries.
type Handle interface {
	gsql.Handle
	SQLExecutor
}

// EnsureResourceGroup returns the ResourceGroup for the given
// (scopeUUID, assetManager), creating one if none exists yet.
// The insert uses ON CONFLICT to stay race-free under concurrent callers.
func EnsureResourceGroup(ctx context.Context, dbi Handle, scopeUUID, domainUUID, assetManager string) (ResourceGroup, error) {
	nextScrapeAt := time.Unix(0, 0).UTC()
	_, err := dbi.ExecContext(ctx,
		`INSERT INTO resource_groups (scope_uuid, domain_uuid, asset_manager, scrape_error_message, next_scrape_at, scrape_duration_secs)
			VALUES ($1, $2, $3, '', $4, 0)
			ON CONFLICT (scope_uuid, asset_manager) DO NOTHING`,
		scopeUUID, domainUUID, assetManager, nextScrapeAt,
	)
	if err != nil {
		return ResourceGroup{}, err
	}
	return ResourceGroupStore.SelectOneWhere(ctx, dbi,
		`scope_uuid = $1 AND asset_manager = $2`, scopeUUID, assetManager)
}

// GCResourceGroup deletes the ResourceGroup with the given ID if no Resources reference it anymore.
func GCResourceGroup(ctx context.Context, dbi SQLExecutor, groupID int64) error {
	_, err := dbi.ExecContext(ctx,
		`DELETE FROM resource_groups WHERE id = $1 AND NOT EXISTS (SELECT 1 FROM resources WHERE resource_group_id = $1)`,
		groupID,
	)
	return err
}
