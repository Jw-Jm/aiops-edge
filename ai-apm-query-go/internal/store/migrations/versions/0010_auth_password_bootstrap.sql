-- mysql/0010-auth-password-bootstrap
-- First-login password policy is additive; existing users remain unchanged.
ALTER TABLE users ADD COLUMN must_change_password TINYINT NOT NULL DEFAULT 0;
-- statement-breakpoint
UPDATE users SET must_change_password=0 WHERE must_change_password IS NULL;
