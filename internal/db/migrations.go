// SPDX-FileCopyrightText: 2019 SAP SE
// SPDX-License-Identifier: Apache-2.0

package db

// SQLMigrations must be public because it's also used by tests.
var SQLMigrations = map[string]string{
	//NOTE: Migrations 1 through 21 have been rolled up into one at 2024-02-26
	// to better represent the current baseline of the DB schema.
	"021_rollup.down.sql": `
		DROP TABLE resources;
		DROP TABLE assets;
		DROP TYPE op_reason;
		DROP TYPE op_outcome;
		DROP TABLE pending_operations;
		DROP TABLE finished_operations;
	`,
	"021_rollup.up.sql": `
		CREATE TABLE resources (
			id                          BIGSERIAL         NOT NULL PRIMARY KEY,
			scope_uuid                  TEXT              NOT NULL,
			asset_type                  TEXT              NOT NULL,
			low_threshold_percent       TEXT              NOT NULL,
			low_delay_seconds           INTEGER           NOT NULL,
			high_threshold_percent      TEXT              NOT NULL,
			high_delay_seconds          INTEGER           NOT NULL,
			critical_threshold_percent  TEXT              NOT NULL,
			size_step_percent           DOUBLE PRECISION  DEFAULT NULL,
			min_size                    BIGINT            DEFAULT NULL,
			max_size                    BIGINT            DEFAULT NULL,
			min_free_size               BIGINT            DEFAULT NULL,
			single_step                 BOOLEAN           NOT NULL DEFAULT FALSE,
			domain_uuid                 TEXT              NOT NULL DEFAULT 'unknown',
			scrape_error_message        TEXT              NOT NULL DEFAULT '',
			config_json                 TEXT              NOT NULL DEFAULT '',
			next_scrape_at              TIMESTAMP         NOT NULL DEFAULT NOW(),
			scrape_duration_secs        REAL              NOT NULL DEFAULT 0,
			UNIQUE(scope_uuid, asset_type)
		);

		CREATE TABLE assets (
			id                    BIGSERIAL  NOT NULL PRIMARY KEY,
			resource_id           BIGINT     NOT NULL REFERENCES resources ON DELETE CASCADE,
			uuid                  TEXT       NOT NULL,
			size                  BIGINT     NOT NULL,
			expected_size         BIGINT     DEFAULT NULL,
			scrape_error_message  TEXT       NOT NULL DEFAULT '',
			usage                 TEXT       NOT NULL,
			critical_usages       TEXT       NOT NULL DEFAULT '',
			next_scrape_at        TIMESTAMP  NOT NULL DEFAULT NOW(),
			never_scraped         BOOLEAN    NOT NULL DEFAULT FALSE,
			scrape_duration_secs  REAL       NOT NULL DEFAULT 0,
			min_size              REAL       DEFAULT NULL,
			max_size              REAL       DEFAULT NULL,
			resized_at            TIMESTAMP  DEFAULT NULL,
			UNIQUE(resource_id, uuid)
		);

		-- NOTE: order of op_reason is important because we "ORDER BY reason" in some queries
		CREATE TYPE op_reason  AS ENUM ('critical', 'high', 'low');
		CREATE TYPE op_outcome AS ENUM ('succeeded', 'failed', 'errored', 'cancelled');

		CREATE TABLE pending_operations (
			id                     BIGSERIAL  NOT NULL PRIMARY KEY,
			asset_id               BIGINT     NOT NULL REFERENCES assets ON DELETE CASCADE,
			reason                 op_reason  NOT NULL,
			old_size               BIGINT     NOT NULL,
			new_size               BIGINT     NOT NULL,
			created_at             TIMESTAMP  NOT NULL,
			confirmed_at           TIMESTAMP  DEFAULT NULL,
			greenlit_at            TIMESTAMP  DEFAULT NULL,
			greenlit_by_user_uuid  TEXT       DEFAULT NULL,
			errored_attempts       INT        DEFAULT 0,
			retry_at               TIMESTAMP  DEFAULT NULL,
			usage                  TEXT       NOT NULL,
			UNIQUE(asset_id)
		);

		CREATE TABLE finished_operations (
			asset_id               BIGINT      NOT NULL REFERENCES assets ON DELETE CASCADE,
			reason                 op_reason   NOT NULL,
			outcome                op_outcome  NOT NULL,
			old_size               BIGINT      NOT NULL,
			new_size               BIGINT      NOT NULL,
			created_at             TIMESTAMP   NOT NULL,
			confirmed_at           TIMESTAMP   DEFAULT NULL,
			greenlit_at            TIMESTAMP   DEFAULT NULL,
			finished_at            TIMESTAMP   NOT NULL,
			greenlit_by_user_uuid  TEXT        DEFAULT NULL,
			error_message          TEXT        NOT NULL DEFAULT '',
			errored_attempts       INT         DEFAULT 0,
			usage                  TEXT        NOT NULL
		);
	`,
	"022_add_index_to_assets_next_scrape_at.up.sql": `
		CREATE INDEX ON assets (next_scrape_at);
	`,
	"022_add_index_to_assets_next_scrape_at.down.sql": `
		DROP INDEX assets_next_scrape_at_idx;
	`,
	"023_add_resource_min_free_is_critical_flag.down.sql": `
		ALTER TABLE resources
			DROP COLUMN min_free_is_critical;
	`,
	"023_add_resource_min_free_is_critical_flag.up.sql": `
		ALTER TABLE resources
			ADD COLUMN min_free_is_critical BOOLEAN DEFAULT FALSE;
	`,
	"024_add_error-resolved_to_op_outcome.up.sql": `
		ALTER TYPE op_outcome ADD VALUE 'error-resolved';
	`,
	"024_add_error-resolved_to_op_outcome.down.sql": `
		ALTER TYPE op_outcome REMOVE VALUE 'error-resolved';
	`,
	"025_clarify_asset_column_names.up.sql": `
		ALTER TABLE assets RENAME min_size TO strict_min_size;
		ALTER TABLE assets RENAME max_size TO strict_max_size;
	`,
	"025_clarify_asset_column_names.down.sql": `
		ALTER TABLE assets RENAME strict_min_size TO min_size;
		ALTER TABLE assets RENAME strict_max_size TO max_size;
	`,
}
