-- Remove the legacy shared default from change_events. Runtime ownership is
-- retired to query-api/ClickHouse; this migration keeps preserved legacy data
-- fail-closed for any explicitly invoked compatibility migrator.
ALTER TABLE change_events ALTER COLUMN cluster_id DROP DEFAULT;
