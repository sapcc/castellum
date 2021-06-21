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

package db

//WARNING: The value 'errored' was added to op_outcome at schema version 10.
//However, I could not add it using ALTER TYPE because ALTER TYPE cannot run
//inside a transaction, and golang-migrate/migrate *insists* on running
//everything in a transaction. Databases that don't have the 'errored' value in
//type op_outcome must be upgraded manually with:
//
//  ALTER TYPE op_outcome ADD VALUE IF NOT EXISTS 'errored' AFTER 'failed';

//SQLMigrations must be public because it's also used by tests.
var SQLMigrations = map[string]string{
	"001_initial.down.sql": `
		DROP TABLE resources;
		DROP TABLE assets;
		DROP TYPE op_reason;
		DROP TYPE op_outcome;
		DROP TABLE operations;
		DROP TABLE operations_history;
	`,
	"001_initial.up.sql": `
		CREATE TABLE resources (
			id         BIGSERIAL NOT NULL PRIMARY KEY,
			scope_uuid TEXT      NOT NULL,
			asset_type TEXT      NOT NULL,

			scraped_at TIMESTAMP DEFAULT NULL,

			low_threshold_percent      INTEGER NOT NULL,
			low_delay_seconds          INTEGER NOT NULL,
			high_threshold_percent     INTEGER NOT NULL,
			high_delay_seconds         INTEGER NOT NULL,
			critical_threshold_percent INTEGER NOT NULL,

			size_step_percent BIGINT,

			UNIQUE(scope_uuid, asset_type)
		);

		CREATE TABLE assets (
			id          BIGSERIAL NOT NULL PRIMARY KEY,
			resource_id BIGINT    NOT NULL REFERENCES resources ON DELETE CASCADE,
			uuid        TEXT      NOT NULL,

			size          BIGINT    NOT NULL,
			usage_percent INTEGER   NOT NULL,
			scraped_at    TIMESTAMP NOT NULL,
			expected_size BIGINT    DEFAULT NULL,

			UNIQUE(resource_id, uuid)
		);

		-- NOTE: order of op_reason is important because we "ORDER BY reason" in some queries
		CREATE TYPE op_reason  AS ENUM ('critical', 'high', 'low');
		CREATE TYPE op_outcome AS ENUM ('succeeded', 'failed', 'errored', 'cancelled');

		CREATE TABLE pending_operations (
			id       BIGSERIAL NOT NULL PRIMARY KEY,
			asset_id BIGINT    NOT NULL REFERENCES assets ON DELETE CASCADE,
			reason   op_reason NOT NULL,

			old_size      BIGINT NOT NULL,
			new_size      BIGINT NOT NULL,
			usage_percent INTEGER NOT NULL,

			created_at   TIMESTAMP NOT NULL,
			confirmed_at TIMESTAMP DEFAULT NULL,
			greenlit_at  TIMESTAMP DEFAULT NULL,

			greenlit_by_user_uuid TEXT DEFAULT NULL,

			UNIQUE(asset_id)
		);

		CREATE TABLE finished_operations (
			asset_id BIGINT     NOT NULL REFERENCES assets ON DELETE CASCADE,
			reason   op_reason  NOT NULL,
			outcome  op_outcome NOT NULL,

			old_size      BIGINT NOT NULL,
			new_size      BIGINT NOT NULL,
			usage_percent INTEGER NOT NULL,

			created_at   TIMESTAMP  NOT NULL,
			confirmed_at TIMESTAMP  DEFAULT NULL,
			greenlit_at  TIMESTAMP  DEFAULT NULL,
			finished_at  TIMESTAMP  NOT NULL,

			greenlit_by_user_uuid TEXT DEFAULT NULL,
			error_message         TEXT NOT NULL DEFAULT ''
		);
	`,
	"002_add_assets_checked_at.down.sql": `
		ALTER TABLE assets DROP COLUMN checked_at;
	`,
	"002_add_assets_checked_at.up.sql": `
		ALTER TABLE assets ADD COLUMN checked_at TIMESTAMP;
		UPDATE assets SET checked_at = scraped_at;
		ALTER TABLE assets ALTER COLUMN checked_at SET NOT NULL;
	`,
	"003_add_resources_min_size_max_size.down.sql": `
		ALTER TABLE resources DROP COLUMN min_size;
		ALTER TABLE resources DROP COLUMN max_size;
	`,
	"003_add_resources_min_size_max_size.up.sql": `
		ALTER TABLE resources ADD COLUMN min_size BIGINT DEFAULT NULL;
		ALTER TABLE resources ADD COLUMN max_size BIGINT DEFAULT NULL;
	`,
	"004_add_assets_scrape_error_message.down.sql": `
		ALTER TABLE assets DROP COLUMN scrape_error_message;
	`,
	"004_add_assets_scrape_error_message.up.sql": `
		ALTER TABLE assets ADD COLUMN scrape_error_message TEXT NOT NULL DEFAULT '';
	`,
	"005_make_assets_scraped_at_optional.down.sql": `
		ALTER TABLE assets ALTER COLUMN scraped_at SET NOT NULL;
	`,
	"005_make_assets_scraped_at_optional.up.sql": `
		ALTER TABLE assets ALTER COLUMN scraped_at DROP NOT NULL;
	`,
	"006_add_assets_absolute_usage.down.sql": `
		ALTER TABLE assets DROP COLUMN absolute_usage;
	`,
	"006_add_assets_absolute_usage.up.sql": `
		ALTER TABLE assets ADD COLUMN absolute_usage BIGINT DEFAULT NULL;
	`,
	"007_add_resources_min_free_size.down.sql": `
		ALTER TABLE resources DROP COLUMN min_free_size;
	`,
	"007_add_resources_min_free_size.up.sql": `
		ALTER TABLE resources ADD COLUMN min_free_size BIGINT DEFAULT NULL;
	`,
	"008_floating_point_usage_percent.down.sql": `
		ALTER TABLE resources ALTER COLUMN low_threshold_percent TYPE INTEGER;
		ALTER TABLE resources ALTER COLUMN high_threshold_percent TYPE INTEGER;
		ALTER TABLE resources ALTER COLUMN critical_threshold_percent TYPE INTEGER;
		ALTER TABLE resources ALTER COLUMN size_step_percent TYPE BIGINT;
		ALTER TABLE assets ALTER COLUMN usage_percent TYPE INTEGER;
		ALTER TABLE pending_operations ALTER COLUMN usage_percent TYPE INTEGER;
		ALTER TABLE finished_operations ALTER COLUMN usage_percent TYPE INTEGER;
	`,
	"008_floating_point_usage_percent.up.sql": `
		ALTER TABLE resources ALTER COLUMN low_threshold_percent TYPE DOUBLE PRECISION;
		ALTER TABLE resources ALTER COLUMN high_threshold_percent TYPE DOUBLE PRECISION;
		ALTER TABLE resources ALTER COLUMN critical_threshold_percent TYPE DOUBLE PRECISION;
		ALTER TABLE resources ALTER COLUMN size_step_percent TYPE DOUBLE PRECISION;
		ALTER TABLE assets ALTER COLUMN usage_percent TYPE DOUBLE PRECISION;
		ALTER TABLE pending_operations ALTER COLUMN usage_percent TYPE DOUBLE PRECISION;
		ALTER TABLE finished_operations ALTER COLUMN usage_percent TYPE DOUBLE PRECISION;
	`,
	"009_add_resources_single_step.down.sql": `
		ALTER TABLE resources DROP COLUMN single_step;
	`,
	"009_add_resources_single_step.up.sql": `
		ALTER TABLE resources ADD COLUMN single_step BOOLEAN NOT NULL DEFAULT FALSE;
	`,
	"010_add_resources_domain_uuid.down.sql": `
		ALTER TABLE resources DROP COLUMN domain_uuid;
	`,
	"010_add_resources_domain_uuid.up.sql": `
		ALTER TABLE resources ADD COLUMN domain_uuid TEXT NOT NULL DEFAULT 'unknown';
	`,
	"011_add_resources_checked_at.down.sql": `
		ALTER TABLE resources DROP COLUMN checked_at;
	`,
	"011_add_resources_checked_at.up.sql": `
		ALTER TABLE resources ADD COLUMN checked_at TIMESTAMP;
		UPDATE resources SET checked_at = scraped_at;
		ALTER TABLE resources ALTER COLUMN checked_at SET NOT NULL;
	`,
	"012_add_resources_scrape_error_message.down.sql": `
		ALTER TABLE resources DROP COLUMN scrape_error_message;
	`,
	"012_add_resources_scrape_error_message.up.sql": `
		ALTER TABLE resources ADD COLUMN scrape_error_message TEXT NOT NULL DEFAULT '';
	`,
	"013_add_operations_retry_counter.down.sql": `
		ALTER TABLE pending_operations
			DROP COLUMN errored_attempts,
			DROP COLUMN retry_at;
		ALTER TABLE finished_operations
			DROP COLUMN errored_attempts;
	`,
	"013_add_operations_retry_counter.up.sql": `
		ALTER TABLE pending_operations
			ADD COLUMN errored_attempts INT DEFAULT 0,
			ADD COLUMN retry_at TIMESTAMP;
		ALTER TABLE finished_operations
			ADD COLUMN errored_attempts INT DEFAULT 0;
	`,
	"014_refactor_usage_values.up.sql": `
		ALTER TABLE resources
			ALTER COLUMN low_threshold_percent SET DATA TYPE text
				USING CONCAT('{"singular":', low_threshold_percent, '}'),
			ALTER COLUMN high_threshold_percent SET DATA TYPE text
				USING CONCAT('{"singular":', high_threshold_percent, '}'),
			ALTER COLUMN critical_threshold_percent SET DATA TYPE text
				USING CONCAT('{"singular":', critical_threshold_percent, '}');

		ALTER TABLE assets ADD COLUMN usage TEXT NOT NULL DEFAULT '';
		UPDATE assets SET usage = CONCAT('{"singular":', absolute_usage, '}');
		ALTER TABLE assets
			ALTER COLUMN usage DROP DEFAULT,
			DROP COLUMN absolute_usage,
			DROP COLUMN usage_percent;

		ALTER TABLE finished_operations ADD COLUMN usage TEXT NOT NULL DEFAULT '';
		UPDATE finished_operations SET usage = CONCAT('{"singular":', old_size * usage_percent / 100, '}');
		ALTER TABLE finished_operations
			ALTER COLUMN usage DROP DEFAULT,
			DROP COLUMN usage_percent;

		ALTER TABLE pending_operations ADD COLUMN usage TEXT NOT NULL DEFAULT '';
		UPDATE pending_operations SET usage = CONCAT('{"singular":', old_size * usage_percent / 100, '}');
		ALTER TABLE pending_operations
			ALTER COLUMN usage DROP DEFAULT,
			DROP COLUMN usage_percent;
	`,
	"014_refactor_usage_values.down.sql": `
		-- in all likelihood, we are not going to need this rollback, and it would
		-- be very tedious to write (and test!), so we don't bother
		ROLLBACK;
	`,
}
