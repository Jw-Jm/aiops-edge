# First Login Password Change Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make fresh local bootstrap use `admin/admin123`, force the first admin login through a secure password-change flow, and verify the complete flow in the deployed application.

**Architecture:** Add an additive MySQL migration and authoritative user flag, enforce the flag in the JWT middleware, and expose a password-change endpoint that rotates sessions. Persist the flag in the frontend auth store and route first-time users to a dedicated change-password page; preserve existing accounts during normal upgrades and explicitly migrate the current local admin once.

**Tech Stack:** Go 1.x, `database/sql` + MySQL, bcrypt, JWT sessions, React 18, TypeScript, React Router, Zustand, Ant Design, Vitest, Helm/Kubernetes.

**Spec:** `docs/superpowers/specs/2026-08-26-first-login-password-change-design.md`

## Global Constraints

- `admin/admin123` is only a first-bootstrap credential; existing users are never overwritten by startup or Helm upgrade.
- `users.must_change_password` is the authoritative force-change state.
- Forced-change browser requests return `403` with `password_change_required` except password change and `/me`.
- New passwords are at least 8 characters, confirmed twice, and stored only as bcrypt hashes.
- Every implementation batch must pass its focused tests, be committed, image-built, Helm-deployed, and real-verified before the next batch.
- Never print the initial password, replacement password, JWT, or Kubernetes Secret value in command output or final text.

### Task 1: Commit the existing frontend acceptance fixes separately

**Files:**
- Modify: `observability-frontend/src/pages/Overview/index.tsx`
- Modify: `observability-frontend/src/pages/investigation/InvestigationCenter.tsx`
- Modify: `observability-frontend/src/pages/investigation/InvestigationCenter.test.tsx`
- Modify: `observability-frontend/src/pages/observability/Trace.tsx`
- Modify: `observability-frontend/src/pages/observability/VirtualMachines.tsx`
- Create: `observability-frontend/src/pages/Overview/Overview.test.tsx`
- Create: `observability-frontend/src/pages/observability/Trace.test.tsx`
- Create: `observability-frontend/src/pages/observability/VirtualMachines.test.tsx`

**Interfaces:** Produces a clean baseline where the current frontend suite has 10 test files and 13 passing tests before auth changes. Does not touch authentication behavior.

- [ ] **Step 1: Run the focused failure-state tests**

Run `npm test -- --run` from `observability-frontend`.

Expected: 10 test files and 13 tests pass; only existing library deprecation/future-flag warnings may remain.

- [ ] **Step 2: Commit the baseline fixes**

Run `git add observability-frontend/src/pages/Overview/index.tsx observability-frontend/src/pages/investigation/InvestigationCenter.tsx observability-frontend/src/pages/investigation/InvestigationCenter.test.tsx observability-frontend/src/pages/observability/Trace.tsx observability-frontend/src/pages/observability/VirtualMachines.tsx observability-frontend/src/pages/Overview/Overview.test.tsx observability-frontend/src/pages/observability/Trace.test.tsx observability-frontend/src/pages/observability/VirtualMachines.test.tsx && git commit -m "fix: surface frontend data source failures"`.

- [ ] **Step 3: Verify the commit is pushed**

Run `git push origin main` and then `git rev-parse HEAD origin/main`.

Expected: both hashes are identical.

### Task 2: Add the authoritative password-change schema and user projection

**Files:**
- Create: `ai-apm-query-go/internal/store/migrations/versions/0010_auth_password_bootstrap.sql`
- Modify: `ai-apm-query-go/internal/store/users.go`
- Modify: `ai-apm-query-go/internal/store/mysql.go`
- Modify: `ai-apm-query-go/internal/store/bootstrap.go`
- Test: `ai-apm-query-go/internal/store/authorization_bootstrap_test.go`
- Test: `ai-apm-query-go/internal/store/users_uuid_test.go`

**Interfaces:** Adds `store.User.MustChangePassword`, `UserDAO.GetByUsername`, `GetByUUID`, `List`, `GetByID`, and `Create` projections with the new column; `SeedAdmin(hash string)` inserts `must_change_password=1` only when `admin` does not exist; add DAO methods `SetPasswordAndClearForceChange(userUUID, hash string)` and `RevokeActiveSessions(userUUID string)`.

- [ ] **Step 1: Add a failing migration/seed test**

Add SQL-mock expectations proving the admin seed insert contains `must_change_password=1`, an existing admin is not updated, and the new DAO update clears the flag.

- [ ] **Step 2: Run the focused store tests and verify failure**

Run `go test ./internal/store -run 'Test(EnsureCanonicalBootstrapData|SeedAdmin|.*Password)'` from `ai-apm-query-go`.

Expected: FAIL because the new column and DAO methods are absent.

- [ ] **Step 3: Add migration `0010_auth_password_bootstrap.sql`**

Use:

```sql
ALTER TABLE users ADD COLUMN must_change_password TINYINT NOT NULL DEFAULT 0;
-- statement-breakpoint
UPDATE users SET must_change_password=0 WHERE must_change_password IS NULL;
```

- [ ] **Step 4: Update all user projections and seed behavior**

Append `must_change_password` to every explicit user SELECT/scan, set it on `User`, add the additive legacy `hasColumn` fallback in `EnsureSchema`, and change the seed insert to:

```sql
INSERT IGNORE INTO users (user_uuid, username, password_hash, display_name, role, must_change_password)
VALUES (LOWER(UUID()), 'admin', ?, '管理员', 'admin', 1)
```

Do not add an update clause to this insert.

- [ ] **Step 5: Implement the DAO password/session mutation methods**

Use one transaction for `UPDATE users SET password_hash=?, must_change_password=0 WHERE user_uuid=?` and `UPDATE auth_sessions SET status='revoked', revoked_at=UTC_TIMESTAMP(), token_version=token_version+1 WHERE user_uuid=? AND status='active'`; return the transaction error without partially reporting success.

- [ ] **Step 6: Run focused store tests**

Run the command from Step 2 and `go test ./internal/store/...`.

Expected: PASS.

- [ ] **Step 7: Commit and deploy the schema batch**

Run `git add ai-apm-query-go/internal/store && git commit -m "feat: track first-login password changes" && git push origin main`. Build the query-api/schema-migrator images from the commit and Helm upgrade the release; wait for the migration job and query-api readiness.

### Task 3: Implement backend login metadata, forced-change middleware, and change-password API

**Files:**
- Modify: `ai-apm-query-go/internal/api/auth.go`
- Modify: `ai-apm-query-go/internal/api/users.go`
- Modify: `ai-apm-query-go/cmd/api/main.go`
- Create: `ai-apm-query-go/internal/api/password_test.go`
- Modify: `ai-apm-query-go/internal/api/login_session_test.go`
- Modify: `ai-apm-query-go/internal/api/authz_context_test.go`

**Interfaces:** Adds `POST /api/v1/auth/change-password`; login response includes `must_change_password`; `AuthMiddleware` returns `403 {"error":"password_change_required"}` for browser business requests while the flag is set; successful change returns a newly issued token.

- [ ] **Step 1: Write failing API tests**

Cover:

```text
login(admin/admin123) -> 200, must_change_password=true
GET /api/v1/overview with that token -> 403 password_change_required
POST /api/v1/auth/change-password with wrong current password -> 401
POST with mismatched/short new password -> 400
POST with valid new password -> 200, new token, must_change_password=false
old token -> 403/401; new token -> protected route proceeds
```

Use existing SQL-mock auth/session setup and never print token values.

- [ ] **Step 2: Run focused API tests and verify failure**

Run `go test ./internal/api -run 'Test(Login|Password|AuthMiddleware)'` from `ai-apm-query-go`.

Expected: FAIL because the response flag, endpoint, and middleware gate do not exist.

- [ ] **Step 3: Add shared session issuance and login metadata**

Refactor the successful-login session creation into a helper that returns `(token string, error)`, add `must_change_password` to the response, and keep the existing 503/fail-closed behavior.

- [ ] **Step 4: Add password validation and `ChangePassword`**

Parse `{current_password,new_password,confirm_password}`, require an authenticated `AuthorizationContext`, load the authoritative user by UUID, compare the current bcrypt hash, require `len([]rune(new_password)) >= 8`, reject mismatched confirmation, hash with bcrypt, atomically update the user and revoke sessions, create a new session, and return the new token.

- [ ] **Step 5: Enforce the flag in `AuthMiddleware`**

Extend the MySQL authorization query to carry `must_change_password` for browser JWT requests. Permit `/api/v1/auth/change-password` and `/api/v1/me`; reject other `/api/v1/` requests before dispatch with `403 password_change_required`. Leave signed internal service calls on their existing service boundary.

- [ ] **Step 6: Register routes and include `/me` metadata**

Register `/api/v1/auth/change-password` before the global middleware and update `/api/v1/me` to resolve the current user by the UUID from the authorization context and return `must_change_password`.

- [ ] **Step 7: Change the bootstrap default without overwriting existing users**

In `cmd/api/main.go`, use `ADMIN_INITIAL_PASSWORD`, then `ADMIN_PASSWORD`, then the local default `admin123`; remove random password generation/logging. In Helm/apply helpers, preserve explicit secret reuse and use `admin123` only when a new local Secret has no override. Do not print the value.

- [ ] **Step 8: Run backend tests**

Run `go test ./internal/api ./internal/store` and then `go test ./...` from `ai-apm-query-go`.

Expected: PASS.

- [ ] **Step 9: Commit, build, deploy, and real-verify backend behavior**

Commit/push. Build query-api and schema-migrator with the commit tag, Helm upgrade, wait for migration and readiness, and use a shell script that only prints HTTP statuses/error codes to verify the three API states. Do not print passwords or JWTs.

### Task 4: Add the frontend forced-change flow

**Files:**
- Modify: `observability-frontend/src/store/authStore.ts`
- Modify: `observability-frontend/src/api/client.ts`
- Modify: `observability-frontend/src/components/RequireAuth.tsx`
- Modify: `observability-frontend/src/App.tsx`
- Modify: `observability-frontend/src/pages/Login/index.tsx`
- Create: `observability-frontend/src/pages/ChangePassword/index.tsx`
- Create: `observability-frontend/src/pages/ChangePassword/ChangePassword.test.tsx`
- Modify: `observability-frontend/src/pages/Login/Login.test.tsx`
- Create: `observability-frontend/src/components/RequireAuth.test.tsx`

**Interfaces:** `login(token, username, role, displayName, mustChangePassword)` persists the flag; `changePassword` posts the three password fields; `/change-password` renders the form and stores the new token.

- [ ] **Step 1: Write failing frontend tests**

Test that a login response with `must_change_password=true` navigates to `/change-password`, the form rejects short/mismatched values, successful change persists the returned token and navigates to `/overview`, and `RequireAuth` redirects a flagged session away from `/overview` while allowing `/change-password`.

- [ ] **Step 2: Run focused frontend tests and verify failure**

Run `npx vitest run src/pages/Login/Login.test.tsx src/pages/ChangePassword/ChangePassword.test.tsx src/components/RequireAuth.test.tsx`.

Expected: FAIL because the route, API method, and store flag do not exist.

- [ ] **Step 3: Implement the API/store contracts**

Add `mustChangePassword` to the store and localStorage, add `changePassword`, clear the flag on successful response, and remove it on logout.

- [ ] **Step 4: Implement route guard and page**

Add `/change-password` outside `AppLayout` but behind a token-aware guard, route flagged users there, render the three fields with client-side length/confirmation validation, and display only server error messages that do not contain secrets.

- [ ] **Step 5: Update Login and App routes**

Persist the login response flag and navigate to `/change-password` when true; otherwise preserve `/overview`. Ensure the change page cannot be reached without a token.

- [ ] **Step 6: Run frontend tests and build**

Run `npm test -- --run` and `npm run build` from `observability-frontend`.

Expected: all frontend tests pass and TypeScript/Vite build succeeds.

- [ ] **Step 7: Commit, build, deploy, and real-verify the frontend**

Commit/push, build `observability-frontend` from the new commit, Helm upgrade only the frontend image while retaining backend images, then inspect the deployed page and API-driven redirects.

### Task 5: Migrate the current local admin and complete acceptance evidence

**Files:**
- Modify: `部署验证.md` (document the non-secret migration command and expected states)
- Modify: `AIOps前端全功能及真实性验收测试方案_细化排查版_最终版.md` only if the acceptance evidence needs a cross-reference; preserve the user-owned untracked file unless explicitly editing it.

**Interfaces:** Current cluster has `admin` password reset to the default bootstrap credential once, with `must_change_password=1`; all later upgrades preserve the changed password.

- [ ] **Step 1: Read current deployment identity without printing secrets**

Confirm release, images, migration version, and admin row status. Do not print password hashes, Secret values, tokens, or database credentials.

- [ ] **Step 2: Execute the one-time local admin migration**

Use the deployed query-api/database boundary or a temporary admin migration job to set the bcrypt hash for `admin` to the default bootstrap password and set `must_change_password=1`; record only the migration action and result status.

- [ ] **Step 3: Real API acceptance**

Verify status-only assertions: default login 200 + `must_change_password=true`; business request 403 with `password_change_required`; successful change 200 + new token; old password 401; new password 200; refresh `/me` reports false.

- [ ] **Step 4: Real browser acceptance**

After explicit user authorization to type the initial password in the already-open browser, fill `admin/admin123`, verify redirect to the change-password page, enter a user-provided replacement password, verify Overview, then verify logout/login behavior without revealing either password.

- [ ] **Step 5: Full acceptance rerun**

Run repository/backend/frontend test suites, the acceptance-plan API matrix, deployed image/readiness checks, and the critical browser route matrix. Record any remaining unavailable capability as a real dependency failure, not a fabricated success.

- [ ] **Step 6: Final consistency check**

Run `git diff --check`, `git status --short --branch`, compare `git rev-parse HEAD` with `git rev-parse origin/main`, and verify each running self-built image tag maps to the pushed commit. Do not mark the overall goal complete until every acceptance item has authoritative evidence.
