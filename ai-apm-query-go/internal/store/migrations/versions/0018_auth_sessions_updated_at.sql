-- mysql/0018-auth-sessions-updated-at
-- Align auth_sessions with the legacy EnsureSchema column set. Phase 4 coverage
-- gate requires migrated schema A to cover every legacy column; updated_at was
-- present in the legacy definition but missing from the authoritative baseline.
ALTER TABLE auth_sessions
  ADD COLUMN updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP;
