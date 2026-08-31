-- mysql/0015-remove-implicit-change-scope
-- The legacy change_events table must not synthesize a shared "default"
-- cluster. New writes are required to carry the canonical cluster UUID.
ALTER TABLE change_events ALTER COLUMN cluster_id DROP DEFAULT;
