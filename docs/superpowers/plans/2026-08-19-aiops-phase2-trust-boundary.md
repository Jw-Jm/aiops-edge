# AIOps Phase 2 Trust Boundary Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement the P0 multi-tenant/multi-cluster trust boundary so JWT proves only identity/session, MySQL remains the authorization source, and all internal orchestrator requests use a signed short-lived `TrustedRequestContext`.

**Architecture:** query-api remains the browser and Kubernetes read trust boundary. A browser `X-Tenant-ID` is accepted only as a temporary requested scope and is revalidated against MySQL; it is never treated as an authorization fact. JWT `role`/`scope`, integer cluster `id=1 → default`, and implicit current-kube-context fallback are removed from authorization semantics. Internal orchestrator calls use a service credential plus Ed25519/JWS context with replay protection.

**Tech Stack:** Go 1.25 standard library, `github.com/golang-jwt/jwt/v5` EdDSA/JWS support, MySQL through the existing `database/sql` store, Python 3.12 target with `cryptography`, React/TypeScript frontend.

**Spec:** `aiops-agentic.md` Phase 2 Task 2.1; confirmed constraints in `aiops-agentic.md` section 0.6; [AIOPS_AGENTIC_ARCHITECTURE.md](../../AIOPS_AGENTIC_ARCHITECTURE.md); [AIOPS_DATA_MODEL_REDESIGN.md](../../AIOPS_DATA_MODEL_REDESIGN.md).

## Global Constraints

- `cluster_id` is an immutable lowercase UUID; `slug` is a readable reference and never a persistence authority.
- MySQL is the only dynamic source for user, session, tenant, role, permission, cluster and scope authorization.
- JWT contains only `sub=user_id`, `sid=session_id`, `iat`, `exp`, `iss`, `aud` and optional token-version metadata; JWT `role` and `scope` are ignored and no longer emitted for authorization.
- `X-Tenant-ID` is a compatibility request hint only; the server resolves and authorizes the requested tenant from MySQL before use, then the compatibility header is removed from callers.
- No request may use `cluster_id=all`, empty cluster, integer cluster id, name, kube-context or current-context fallback as an implicit target for a cluster-scoped operation.
- Orchestrator never reads MySQL directly, never receives Kubernetes credentials, and never submits client-controlled roles, allowed clusters, approval conclusions or credential references.
- Signed context lifetime is 30–60 seconds, audience-bound, nonce-bound and replay-protected; service authentication and context signing keys are separate and rotatable.
- Existing `clusters.id` and `clusters.kubeconfig` may remain as explicitly marked legacy storage during this phase for non-destructive migration, but no new API contract may use either as authority and no new code may read `kubeconfig`.
- Phase 2 does not delete historical observability/runtime data or run destructive cleanup.

---

### Task 1: Signed TrustedRequestContext primitives

**Files:**

- Create: `ai-apm-query-go/internal/auth/trusted_context.go`
- Test: `ai-apm-query-go/internal/auth/trusted_context_test.go`
- Create: `ai-orchestrator/trusted_context.py`
- Test: `ai-orchestrator/tests/test_trusted_context.py`
- Modify: `docs/contracts/contract-fixtures.json`

**Interfaces:**

- Go: `SignTrustedRequestContext(ctx contract.RequestContext, privateKey ed25519.PrivateKey) (string, error)` and `VerifyTrustedRequestContext(token string, cfg VerifyConfig, now time.Time) (contract.RequestContext, error)`.
- Go: `VerifyConfig` contains `Audience`, `Issuer`, `PublicKeys map[string]ed25519.PublicKey`, `ServiceToken`, `ReplayCache`, `ClockSkew`.
- Python: `sign_trusted_request_context(context: Mapping[str, Any], private_key: Ed25519PrivateKey) -> str`.
- Python: `TrustedContextError` exposes stable `error_code` values: `invalid_service`, `invalid_signature`, `invalid_context`, `expired_context`, `replayed_context`, `wrong_audience`.

- [ ] Write Go tests for Ed25519 JWS round-trip, algorithm/key-id validation, audience/issuer validation, 30–60 second lifetime, expired/future context, nonce replay and service-token separation.
- [ ] Run `go test ./internal/auth` and confirm failure because the package is absent.
- [ ] Write Python tests for signing, base64url/JWS shape, context field exclusion (`roles`, `permissions`, `allowed_clusters`, `credential_ref`, `approval`), expiry and tamper rejection.
- [ ] Run `python3 -m pytest -q tests/test_trusted_context.py` and confirm failure because the module is absent.
- [ ] Implement strict JWS EdDSA signing/verifying with constant-time service token comparison and a bounded replay cache; do not add a new dependency if the existing runtime provides Ed25519 support.
- [ ] Implement Python signing only; Python verification is not the authorization boundary.
- [ ] Run both focused test suites and update the shared fixture with a signed-token-free claims payload only; never commit private keys or tokens.
- [ ] Commit as `feat(security): add signed trusted request context primitives`.

### Task 2: MySQL authority and canonical Cluster Registry

**Files:**

- Modify: `ai-apm-query-go/internal/store/mysql.go`
- Modify: `ai-apm-query-go/internal/store/users.go`
- Modify: `ai-apm-query-go/internal/store/clusters.go`
- Create: `ai-apm-query-go/internal/store/authorization.go`
- Test: `ai-apm-query-go/internal/store/authorization_test.go`
- Test: `ai-apm-query-go/internal/store/clusters_registry_test.go`
- Create: `docs/luna/phase2-schema-cutover.md`

**Interfaces:**

- `Cluster` exposes `ClusterID`, `TenantID`, `Slug`, `Name`, `Environment`, `Region`, `CredentialRef`, `Status`, and legacy `ID` only as non-authoritative migration metadata.
- `ClusterDAO.ResolveRef(tenantID, clusterRef string) (*Cluster, error)` accepts UUID or slug and returns only the canonical UUID-backed record.
- `AuthorizationQuery` contains `UserID`, `SessionID`, `TenantRef`, `ClusterRef`, `Namespace`, `ResourceType`, `ResourceName`, and `Action`; `AuthorizationDecision` is computed only from current MySQL rows.
- `AuthorizationDAO.Authorize(ctx AuthorizationQuery) (AuthorizationDecision, error)` checks current user/session/tenant/cluster/scope/action state in MySQL.
- `AuthorizationDecision` contains `Allowed`, `UserID`, `TenantID`, `ClusterID`, `Action`, and a stable denial code; it never trusts role/scope claims supplied by a caller.

- [ ] Write sqlmock tests for current-state authorization, tenant mismatch, cluster mismatch, namespace/resource/action denial, disabled user/session and requested-tenant compatibility input.
- [ ] Write sqlmock tests proving UUID/slug resolution returns the same immutable UUID, same-name clusters remain distinct, and unknown/ambiguous refs return stable errors.
- [ ] Run focused store tests and confirm the new DAO/types are absent.
- [ ] Extend idempotent schema initialization with `user_uuid`, session records, tenant membership, role/permission/scope assignment, and canonical cluster metadata (`cluster_id`, `tenant_id`, `slug`, `environment`, `credential_ref`, lifecycle status); backfill only effective metadata, never observability history.
- [ ] Make `ClusterDAO` stop selecting or writing `kubeconfig` in all new methods; retain legacy column only for later controlled cleanup and never serialize it.
- [ ] Make authorization reads fail closed when MySQL is unavailable; no default tenant, admin, cluster or scope fallback.
- [ ] Run focused store tests and package tests; document exact legacy columns and later deletion order.
- [ ] Commit as `feat(authz): add mysql authority and canonical cluster registry`.

### Task 3: query-api authentication, compatibility scope and Resource Resolver

**Files:**

- Modify: `ai-apm-query-go/internal/api/auth.go`
- Modify: `ai-apm-query-go/internal/api/handler.go`
- Modify: `ai-apm-query-go/internal/api/clusters.go`
- Modify: `ai-apm-query-go/internal/api/settings.go`
- Modify: `ai-apm-query-go/internal/api/settings.go` (current `ProxyAI` implementation)
- Modify: `ai-apm-query-go/cmd/api/main.go`
- Create: `ai-apm-query-go/internal/biz/resource_resolver.go`
- Test: `ai-apm-query-go/internal/biz/resource_resolver_test.go`
- Create: `ai-apm-query-go/internal/api/resource.go`
- Modify: `ai-apm-query-go/internal/api/fixes_test.go`
- Test: `ai-apm-query-go/internal/api/authz_context_test.go`

**Interfaces:**

- `RequestAuthorizationContext(r *http.Request) (AuthorizationContext, error)` validates JWT identity/session and resolves current MySQL authorization.
- `ResourceResolver.Resolve(ResourceQuery) (contract.ResourceRef, error)` canonicalizes UUID/slug and preserves tenant/cluster/namespace/name provenance.
- `GET /api/v1/resources/resolve?type=&name=&cluster_id=&namespace=` returns a canonical `ResourceRef` only after authorization.
- Internal proxy requests carry service authentication plus signed context; forged `X-Internal-*`, client role/scope and approval headers are stripped.

- [ ] Write failing middleware tests for JWT role/scope tampering, forged `X-Tenant-ID`, missing/disabled session, internal service token without signed context, invalid signature, replay and wrong audience.
- [ ] Write failing resolver tests for UUID/slug, same-name resources in two clusters, `all`/empty cluster rejection, and no implicit current-context fallback.
- [ ] Run focused API/biz tests and confirm failure before implementation.
- [ ] Change JWT generation/validation to use only identity/session claims; query-api loads current role/permission/scope from MySQL for every protected request.
- [ ] Treat `X-Tenant-ID` only as a requested tenant hint, compare it with MySQL membership, and remove it from the effective internal context.
- [ ] Require canonical cluster resolution for cluster-scoped handlers; return `invalid_context` 400, `permission_denied` 403, `resource_not_found` 404, `cluster_unavailable` 503 or `ambiguous_resource` 409 as specified.
- [ ] Remove `id=1`, `default`, integer id, name and current-kube-context fallback from authorization and resource resolution. Keep legacy endpoints fail-closed until later route deletion.
- [ ] Make `GET /settings/llm` return only minimal non-secret health/configuration status and require authorization for sensitive configuration paths.
- [ ] Run focused and full Go tests; verify no direct handler reads `role`/`scope` from JWT and no new code reads `kubeconfig`.
- [ ] Commit as `feat(security): enforce mysql authorization and resource resolution`.

### Task 4: orchestrator/query-api internal caller migration

**Files:**

- Modify: `ai-orchestrator/investigator.py`
- Modify: `ai-orchestrator/rca.py`
- Modify: `ai-orchestrator/orchestrator.py`
- Modify: `ai-orchestrator/skills/alert_ops.py`
- Modify: `ai-orchestrator/skills/diagnose.py`
- Modify: `ai-orchestrator/node_health.py`
- Modify: `ai-orchestrator/shell_ws.py`
- Test: affected Python tests plus `ai-orchestrator/tests/test_internal_context_callers.py`
- Modify: `ai-apm-query-go/internal/api` proxy tests

- [ ] Add failing tests proving callers no longer send `X-Internal-Role`, `X-Internal-User`, client tenant/cluster authorization claims or raw credential references.
- [ ] Add a caller helper that receives the already-authorized request context and sends service credential plus signed context to query-api; it must fail closed when context or signing key is absent.
- [ ] Migrate read/query callers from permanent `INTERNAL_TOKEN`-only authorization to the new two-layer request.
- [ ] Keep R4 shell explicitly out of the canonical internal read/write path; do not turn raw shell into a fallback.
- [ ] Run all affected Python tests, Go proxy tests and compile checks; no production route may accept the old internal role headers as authority.
- [ ] Commit as `feat(security): migrate orchestrator internal callers to signed context`.

### Task 5: frontend compatibility migration and Phase 2 Gate

**Files:**

- Modify: `observability-frontend/src/api/client.ts`
- Modify: `observability-frontend/src/store/uiStore.ts`
- Modify: `observability-frontend/src/api/contracts.ts`
- Test: `observability-frontend/src/api/contracts.test-fixtures.ts`
- Create: `docs/luna/phase2-gate.md`

- [ ] Add tests/fixture assertions that `X-Tenant-ID` is marked compatibility-only and frontend state never treats role/localStorage scope as authorization.
- [ ] Keep the header only where existing callers need a requested tenant scope; do not use it to construct internal trusted context.
- [ ] Change cluster selection/types to canonical UUID with slug as display/reference; remove UI use of integer cluster id and `'all'` for cluster-scoped operations.
- [ ] Preserve explicit cross-cluster aggregate requests only for server-authorized aggregate endpoints.
- [ ] Run frontend type-check/build, Python tests, Go tests, ownership/security grep checks and `git diff --check`.
- [ ] Record P0 acceptance, compatibility boundary, known legacy paths and blockers in `docs/luna/phase2-gate.md`.
- [ ] Commit as `docs(gate): record phase 2 trust boundary verification`.
