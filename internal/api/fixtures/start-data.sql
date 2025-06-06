-- SPDX-FileCopyrightText: 2025 SAP SE
--
-- SPDX-License-Identifier: Apache-2.0

CREATE OR REPLACE FUNCTION unix(i integer) RETURNS timestamp AS $$ SELECT TO_TIMESTAMP(i) AT TIME ZONE 'Etc/UTC' $$ LANGUAGE SQL;

-- insert some resources in 'project1' that we can actually list -- both have a different set of thresholds activated to exercise different code paths
INSERT INTO resources (id, scope_uuid, asset_type, low_threshold_percent, low_delay_seconds, high_threshold_percent, high_delay_seconds, critical_threshold_percent, size_step_percent, min_size, max_size, min_free_size, single_step, domain_uuid, scrape_error_message, config_json, next_scrape_at) VALUES (1, 'project1', 'foo', '{"singular":20}', 3600, '{"singular":80}', 1800, '{"singular":0}', 20, NULL, NULL, NULL, FALSE, 'domain1', '', '', UNIX(1801));
INSERT INTO resources (id, scope_uuid, asset_type, low_threshold_percent, low_delay_seconds, high_threshold_percent, high_delay_seconds, critical_threshold_percent, size_step_percent, min_size, max_size, min_free_size, single_step, domain_uuid, scrape_error_message, config_json, next_scrape_at) VALUES (2, 'project1', 'bar', '{"first":0,"second":0}', 0, '{"first":0,"second":0}', 0, '{"first":95,"second":97}', 10, NULL, 20000, NULL, FALSE, 'domain1', 'datacenter is on fire', '{"foo":"bar"}', UNIX(1802));

-- insert some resources that we should not be able to list (ID=3 has wrong project ID, ID=4 has unknown asset type)
INSERT INTO resources (id, scope_uuid, asset_type, low_threshold_percent, low_delay_seconds, high_threshold_percent, high_delay_seconds, critical_threshold_percent, size_step_percent, min_size, max_size, min_free_size, single_step, domain_uuid, scrape_error_message, config_json, next_scrape_at) VALUES (3, 'something-else', 'foo', '{"singular":20}', 3600, '{"singular":80}', 1800, '{"singular":95}', 20, NULL, NULL, NULL, FALSE, 'domain1', 'datacenter is on fire', '', UNIX(1803));
INSERT INTO resources (id, scope_uuid, asset_type, low_threshold_percent, low_delay_seconds, high_threshold_percent, high_delay_seconds, critical_threshold_percent, size_step_percent, min_size, max_size, min_free_size, single_step, domain_uuid, scrape_error_message, config_json, next_scrape_at) VALUES (4, 'project1', 'unknown', '{"singular":20}', 3600, '{"singular":80}', 1800, '{"singular":95}', 20, NULL, NULL, NULL, FALSE, 'domain1', '', '', UNIX(1804));

-- insert some assets in 'project1' that we can list
INSERT INTO assets (id, resource_id, uuid, size, expected_size, scrape_error_message, usage, critical_usages, next_scrape_at, strict_min_size, strict_max_size) VALUES (1, 1, 'fooasset1', 1024, 1200, '', '{"singular":512}', '', UNIX(311), NULL, NULL);
INSERT INTO assets (id, resource_id, uuid, size, expected_size, scrape_error_message, usage, critical_usages, next_scrape_at, strict_min_size, strict_max_size) VALUES (2, 1, 'fooasset2', 512, NULL, 'unexpected uptime', '{"singular":409.6}', '', UNIX(312), 256, 1024);
INSERT INTO assets (id, resource_id, uuid, size, expected_size, scrape_error_message, usage, critical_usages, next_scrape_at, strict_min_size, strict_max_size) VALUES (3, 2, 'barasset1', 2000, NULL, '', '{"first":200,"second":222}', '', UNIX(313), NULL, NULL);
-- insert a bogus asset in an unknown asset type; we should not be able to list this in the API
INSERT INTO assets (id, resource_id, uuid, size, expected_size, scrape_error_message, usage, critical_usages, next_scrape_at, strict_min_size, strict_max_size) VALUES (4, 4, 'bogusasset', 100, NULL, '', '{"singular":50}', '', UNIX(314), NULL, NULL);

-- insert a dummy operation that should not be listed
INSERT INTO finished_operations (asset_id, reason, outcome, old_size, new_size, created_at, confirmed_at, greenlit_at, greenlit_by_user_uuid, finished_at, error_message, usage) VALUES (1, 'critical', 'error-resolved', 0, 0, UNIX(21), UNIX(22), UNIX(22), 'user3', UNIX(23), '', '{"singular":0}');
-- insert some operations that we can list
INSERT INTO finished_operations (asset_id, reason, outcome, old_size, new_size, created_at, confirmed_at, greenlit_at, greenlit_by_user_uuid, finished_at, error_message, usage) VALUES (1, 'low', 'cancelled', 1000, 900, UNIX(31), NULL, NULL, NULL, UNIX(32), '', '{"singular":200}');
INSERT INTO finished_operations (asset_id, reason, outcome, old_size, new_size, created_at, confirmed_at, greenlit_at, greenlit_by_user_uuid, finished_at, error_message, usage) VALUES (1, 'high', 'succeeded', 1023, 1024, UNIX(41), UNIX(42), UNIX(43), 'user2', UNIX(44), '', '{"singular":818.4}');
INSERT INTO finished_operations (asset_id, reason, outcome, old_size, new_size, created_at, confirmed_at, greenlit_at, greenlit_by_user_uuid, finished_at, error_message, usage) VALUES (1, 'critical', 'errored', 1024, 1025, UNIX(51), UNIX(52), UNIX(52), NULL, UNIX(53), 'datacenter is on fire', '{"singular":983.04}');
