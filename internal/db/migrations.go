// SPDX-FileCopyrightText: 2019 SAP SE or an SAP affiliate company
// SPDX-License-Identifier: Apache-2.0

package db

var sqlMigrations = map[int64]string{
	//NOTE: Migrations 1 through 21 have been rolled up into one at 2024-02-26
	// to better represent the current baseline of the DB schema.
	21: `
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
	22: `
		CREATE INDEX ON assets (next_scrape_at);
	`,
	23: `
		ALTER TABLE resources
			ADD COLUMN min_free_is_critical BOOLEAN DEFAULT FALSE;
	`,
	24: `
		ALTER TYPE op_outcome ADD VALUE 'error-resolved';
	`,
	25: `
		ALTER TABLE assets RENAME min_size TO strict_min_size;
		ALTER TABLE assets RENAME max_size TO strict_max_size;
	`,
	26: `
		CREATE TABLE resource_groups (
			id                    BIGSERIAL NOT NULL PRIMARY KEY,
			scope_uuid            TEXT      NOT NULL,
			domain_uuid           TEXT      NOT NULL,
			asset_manager         TEXT      NOT NULL,
			next_scrape_at        TIMESTAMP NOT NULL DEFAULT NOW(),
			scrape_error_message  TEXT      NOT NULL DEFAULT '',
			scrape_duration_secs  REAL      NOT NULL DEFAULT 0,
			UNIQUE(scope_uuid, asset_manager)
		);

		ALTER TABLE resources ADD COLUMN resource_group_id BIGINT
			REFERENCES resource_groups ON DELETE CASCADE;

		INSERT INTO resource_groups
			(scope_uuid, domain_uuid, asset_manager,
			 next_scrape_at, scrape_error_message, scrape_duration_secs)
		SELECT
			scope_uuid,
			MIN(domain_uuid),
			CASE
				WHEN asset_type = 'nfs-shares'           THEN 'nfs-shares'
				WHEN asset_type LIKE 'nfs-shares-type:%' THEN 'nfs-shares'
				WHEN asset_type = 'server-groups'        THEN 'server-groups'
				WHEN asset_type LIKE 'server-group:%'    THEN 'server-groups'
				ELSE 'static'
			END,
			MIN(next_scrape_at),
			COALESCE(MAX(NULLIF(scrape_error_message, '')), ''),
			MAX(scrape_duration_secs)
		FROM resources
		GROUP BY scope_uuid, 3;

		UPDATE resources r SET resource_group_id = g.id
		FROM resource_groups g
		WHERE r.scope_uuid = g.scope_uuid
		  AND g.asset_manager = CASE
				WHEN r.asset_type = 'nfs-shares'           THEN 'nfs-shares'
				WHEN r.asset_type LIKE 'nfs-shares-type:%' THEN 'nfs-shares'
				WHEN r.asset_type = 'server-groups'        THEN 'server-groups'
				WHEN r.asset_type LIKE 'server-group:%'    THEN 'server-groups'
				ELSE 'static'
			END;

		ALTER TABLE resources
			ALTER COLUMN resource_group_id SET NOT NULL,
			DROP CONSTRAINT resources_scope_uuid_asset_type_key,
			ADD  CONSTRAINT resources_resource_group_id_asset_type_key
				UNIQUE (resource_group_id, asset_type),
			DROP COLUMN scope_uuid,
			DROP COLUMN domain_uuid,
			DROP COLUMN next_scrape_at,
			DROP COLUMN scrape_error_message,
			DROP COLUMN scrape_duration_secs;
	`,
}
