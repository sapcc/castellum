INSERT INTO assets (id, resource_id, uuid, size, usage_percent, scraped_at, expected_size) VALUES (1, 1, 'asset1', 1000, 40, 99991, NULL);
INSERT INTO assets (id, resource_id, uuid, size, usage_percent, scraped_at, expected_size) VALUES (2, 1, 'asset2', 2000, 50, 99991, NULL);

INSERT INTO resources (id, scope_uuid, asset_type, scraped_at, low_threshold_percent, low_delay_seconds, high_threshold_percent, high_delay_seconds, critical_threshold_percent, size_step_percent) VALUES (1, 'project1', 'foo', 99991, 0, 0, 0, 0, 0, 0);
INSERT INTO resources (id, scope_uuid, asset_type, scraped_at, low_threshold_percent, low_delay_seconds, high_threshold_percent, high_delay_seconds, critical_threshold_percent, size_step_percent) VALUES (2, 'project2', 'bar', NULL, 0, 0, 0, 0, 0, 0);
INSERT INTO resources (id, scope_uuid, asset_type, scraped_at, low_threshold_percent, low_delay_seconds, high_threshold_percent, high_delay_seconds, critical_threshold_percent, size_step_percent) VALUES (3, 'project3', 'foo', NULL, 0, 0, 0, 0, 0, 0);