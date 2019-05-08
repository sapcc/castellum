CREATE OR REPLACE FUNCTION unix(i integer) RETURNS timestamp AS $$ SELECT TO_TIMESTAMP(i) AT TIME ZONE 'Etc/UTC' $$ LANGUAGE SQL;

-- insert some resources in 'project1' that we can actually list -- both have a different set of thresholds activated to exercise different code paths
INSERT INTO resources (id, scope_uuid, asset_type, scraped_at, low_threshold_percent, low_delay_seconds, high_threshold_percent, high_delay_seconds, critical_threshold_percent, size_step_percent) VALUES (1, 'project1', 'foo', UNIX(1), 20, 3600, 80, 1800, 0, 20);
INSERT INTO resources (id, scope_uuid, asset_type, scraped_at, low_threshold_percent, low_delay_seconds, high_threshold_percent, high_delay_seconds, critical_threshold_percent, size_step_percent) VALUES (2, 'project1', 'bar', UNIX(2), 0, 0, 0, 0, 95, 10);

-- insert some resources that we should not be able to list (ID=3 has wrong project ID, ID=4 has unknown asset type)
INSERT INTO resources (id, scope_uuid, asset_type, scraped_at, low_threshold_percent, low_delay_seconds, high_threshold_percent, high_delay_seconds, critical_threshold_percent, size_step_percent) VALUES (3, 'something-else', 'foo', UNIX(3), 20, 3600, 80, 1800, 95, 20);
INSERT INTO resources (id, scope_uuid, asset_type, scraped_at, low_threshold_percent, low_delay_seconds, high_threshold_percent, high_delay_seconds, critical_threshold_percent, size_step_percent) VALUES (4, 'project1', 'unknown', UNIX(4), 20, 3600, 80, 1800, 95, 20);
