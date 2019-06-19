CREATE OR REPLACE FUNCTION unix(i integer) RETURNS timestamp AS $$ SELECT TO_TIMESTAMP(i) AT TIME ZONE 'Etc/UTC' $$ LANGUAGE SQL;

-- insert some resources in 'project1' that we can actually list -- both have a different set of thresholds activated to exercise different code paths
INSERT INTO resources (id, scope_uuid, asset_type, scraped_at, low_threshold_percent, low_delay_seconds, high_threshold_percent, high_delay_seconds, critical_threshold_percent, size_step_percent, min_size, max_size) VALUES (1, 'project1', 'foo', UNIX(1), 20, 3600, 80, 1800, 0, 20, NULL, NULL);
INSERT INTO resources (id, scope_uuid, asset_type, scraped_at, low_threshold_percent, low_delay_seconds, high_threshold_percent, high_delay_seconds, critical_threshold_percent, size_step_percent, min_size, max_size) VALUES (2, 'project1', 'bar', UNIX(2), 0, 0, 0, 0, 95, 10, NULL, 20000);

-- insert some resources that we should not be able to list (ID=3 has wrong project ID, ID=4 has unknown asset type)
INSERT INTO resources (id, scope_uuid, asset_type, scraped_at, low_threshold_percent, low_delay_seconds, high_threshold_percent, high_delay_seconds, critical_threshold_percent, size_step_percent, min_size, max_size) VALUES (3, 'something-else', 'foo', UNIX(3), 20, 3600, 80, 1800, 95, 20, NULL, NULL);
INSERT INTO resources (id, scope_uuid, asset_type, scraped_at, low_threshold_percent, low_delay_seconds, high_threshold_percent, high_delay_seconds, critical_threshold_percent, size_step_percent, min_size, max_size) VALUES (4, 'project1', 'unknown', UNIX(4), 20, 3600, 80, 1800, 95, 20, NULL, NULL);

-- insert some assets in 'project1' that we can list
INSERT INTO assets (id, resource_id, uuid, size, usage_percent, scraped_at, expected_size, checked_at, scrape_error_message) VALUES (1, 1, 'fooasset1', 1024, 50, UNIX(11), 1200, UNIX(11), '');
INSERT INTO assets (id, resource_id, uuid, size, usage_percent, scraped_at, expected_size, checked_at, scrape_error_message) VALUES (2, 1, 'fooasset2', 512, 80, UNIX(12), NULL, UNIX(12), '');
INSERT INTO assets (id, resource_id, uuid, size, usage_percent, scraped_at, expected_size, checked_at, scrape_error_message) VALUES (3, 2, 'barasset1', 2000, 10, UNIX(13), NULL, UNIX(13), '');
-- insert a bogus asset in an unknown asset type; we should not be able to list this in the API
INSERT INTO assets (id, resource_id, uuid, size, usage_percent, scraped_at, expected_size, checked_at, scrape_error_message) VALUES (4, 4, 'bogusasset', 100, 50, UNIX(14), NULL, UNIX(14), '');

INSERT INTO finished_operations (asset_id, reason, outcome, old_size, new_size, usage_percent, created_at, confirmed_at, greenlit_at, greenlit_by_user_uuid, finished_at, error_message) VALUES (1, 'low', 'cancelled', 1000, 900, 20, UNIX(31), NULL, NULL, NULL, UNIX(32), '');
INSERT INTO finished_operations (asset_id, reason, outcome, old_size, new_size, usage_percent, created_at, confirmed_at, greenlit_at, greenlit_by_user_uuid, finished_at, error_message) VALUES (1, 'high', 'succeeded', 1023, 1024, 80, UNIX(41), UNIX(42), UNIX(43), 'user2', UNIX(44), '');
INSERT INTO finished_operations (asset_id, reason, outcome, old_size, new_size, usage_percent, created_at, confirmed_at, greenlit_at, greenlit_by_user_uuid, finished_at, error_message) VALUES (1, 'critical', 'failed', 1024, 1025, 97, UNIX(51), UNIX(52), UNIX(52), NULL, UNIX(53), 'datacenter is on fire');
