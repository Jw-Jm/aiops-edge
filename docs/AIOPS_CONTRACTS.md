# AIOps V9.2 — Contracts

This document records the frozen V9.2 contracts as implemented in Phase 2. It is the authoritative cross-language contract reference.

## Source of truth

The contract mainline is the shared fixtures + three language implementations. No parallel contract set exists.

```text
docs/contracts/contract-fixtures.json                (shared fixtures, single source of truth)
ai-orchestrator/contracts.py                          (Python)
ai-apm-query-go/internal/contract/*.go               (Go)
observability-frontend/src/api/contracts.ts          (TypeScript)
```

Changes are contract-freeze changes and must be mirrored across all three languages plus fixtures and contract tests.

## Encoding / format conventions (V9.2 §5)

- JSON: UTF-8, `Content-Type: application/json`, `snake_case`
- Business UUIDs: RFC 4122 textual; MySQL `CHAR(36)`; no `BINARY(16)`
- API times: UTC `RFC3339 / RFC3339Nano`; DB business time `TIMESTAMP(6)`
- No dependency on OS local timezone for run logic

## Three internal contexts (V9.2 §11)

| Context | Direction | Purpose | operation / scope |
|---|---|---|---|
| `RunInvocationContext` | query-api → orchestrator | create a new Run | `cluster_scope` = target cluster(s) for this run only |
| `RunControlContext` | query-api → orchestrator | control an existing Run | operation: cancel / stream / action_decision |
| `TrustedRequestContext` | orchestrator → query-api | tool/data access | scope_kind: cluster (cluster_id NOT NULL) or run (control_plane.* only, cluster_id NULL) |

Common claims (all three): version, context_type, issuer, audience, request_id, principal_type (user|system), principal_id, session_id (user MUST NOT NULL; system MUST NULL), tenant_id, issued_at, expires_at, nonce.

Forbidden in all contexts: roles, permissions, allowed_clusters, is_admin, credentials. Lifetime: 1–60s. Default TTL 60s, clock skew max 30s.

`RequestContext` is a DEPRECATED legacy single-context compatibility type (Phase 2 only). It is not a target contract, must not gain callers, removed after Phase 3 callers switch.

## Capability set (V9.2 §16)

```text
observability.metrics.read / logs.read / traces.read / alerts.read / topology.read
kubernetes.resources.read / events.read / logs.read
changes.read
knowledge.search
control_plane.run.read / run.write / event.write
execution.precheck / execute / verify
```

No ad-hoc capabilities (e.g. logs.query2, cluster.read_all, admin_anything).

## Resource identity (V9.2 §10)

Canonical Resource ID does NOT include tenant_id.

```text
service:<cluster_uuid>:<namespace>:<service>
deployment:<cluster_uuid>:<namespace>:<deployment>
statefulset:<cluster_uuid>:<namespace>:<statefulset>
daemonset:<cluster_uuid>:<namespace>:<daemonset>
pod:<cluster_uuid>:<namespace>:<pod>
node:<cluster_uuid>:<node>
```

`tenant_id` is an ownership/authorization/isolation dimension stored alongside, never inside `resource_id`. Same name in different clusters must yield different resource IDs. Negative tests cover: tenant in id → reject; slug instead of cluster UUID → reject; missing cluster → reject; missing namespace (namespaced) → reject; same-name different cluster → distinct; tenant change → id unchanged.

## Fixed enums (V9.2 §28)

- `ToolStatus`: success / partial / no_data / failed / timeout / unavailable / permission_denied
- `EvidenceType`: metric_anomaly / log_pattern / log_error / trace_anomaly / k8s_state / k8s_event / alert / change / knowledge_case / topology_relation / resource_state / capacity_anomaly / hardware_event
- `ClaimType`: fact / inference / knowledge / unknown
- `HypothesisStatus`: candidate / supported / rejected / unknown / confirmed
- `VerificationStatus`: success / partial / failed / regressed / unknown
- `RiskLevel`: R0 / R1 / R2 / R3 / R4
- `PlanStepStatus`: pending / ready / running / success / partial / no_data / failed / timeout / unavailable / permission_denied / cancelled / skipped
- `RunStatus`: created / planning / investigating / awaiting_confirmation / awaiting_approval / executing / verifying / success / partial / failed / regressed / cancelled
- `RunScopeKind`: single_cluster / multi_cluster

No synonyms allowed (e.g. completed / complete / done / succeeded).

## ToolResult semantics (V9.2 §32)

- tool success + empty result → `success=true`, `status=no_data`
- binary missing / backend unreachable → `status=unavailable`
- authorization reject → `status=permission_denied` (requires structured error_code)
- deadline exceeded → `status=timeout`
- backend query error → `status=failed`

"Executed successfully" and "has data" are distinct concepts. Phase 2 fixed the orchestrator `stream_sync` empty k8sgpt/rag results from `unavailable` → `no_data` (contract-level semantic).

## Evidence model (V9.2 §33-34)

Evidence is immutable. Fields: evidence_id, run_id, tenant_id, cluster_id, evidence_type, claim_type, source, source_reliability, resource_id, namespace, service, pod, node, trace_id, observed_at, time_range, fact, raw_ref, raw_digest_sha256, metadata, provenance_fingerprint, created_at.

claim_type rules: fact → must reference on-scene data; inference → must reference supporting evidence IDs; knowledge → must reference document/source; unknown → must record missing reason. Hypothesis is a separate entity.

## OpsAction (V9.2 §45-46)

Fields: action_id, run_id, tenant_id, cluster_id, target_resource_id, resource_version, action_type, parameters, proposed_risk, authoritative_risk, expected_effect, verification_policy_id, rollback_strategy, action_hash, idempotency_key, created_by, created_at.

`authoritative_risk` is computed by query-api Execution Policy Engine and must be ≥ `proposed_risk` (LLM only proposes).

## Unified error codes (V9.2 §58)

AUTH_REQUIRED, SESSION_REVOKED, SERVICE_AUTH_FAILED, INVALID_CONTEXT, CONTEXT_EXPIRED, CONTEXT_REPLAYED, CONTEXT_SCOPE_MISMATCH, TENANT_ACCESS_DENIED, CLUSTER_ACCESS_DENIED, RESOURCE_NOT_FOUND, RESOURCE_AMBIGUOUS, CLUSTER_UNAVAILABLE, NO_DATA, BACKEND_UNAVAILABLE, TOOL_UNAVAILABLE, TOOL_TIMEOUT, VALIDATION_FAILED, RUN_STATE_CONFLICT, RUN_CANCELLED, ACTION_NOT_ALLOWED, ACTION_CONFIRMATION_REQUIRED, ACTION_APPROVAL_REQUIRED, APPROVAL_EXPIRED, APPROVAL_SCOPE_MISMATCH, RESOURCE_VERSION_CONFLICT, MAINTENANCE_MODE.

HTTP: 401 identity / 403 authorization / 404 not found / 409 state-conflict / 422 validation / 503 unavailable / 504 timeout. `no_data` = HTTP 200 with semantic status.

## Cross-language fixtures

`docs/contracts/contract-fixtures.json` is parsed by Python, Go, and TypeScript tests to guarantee identical semantics. Python `DecodeStrict` (Go) / Pydantic `extra="forbid"` (Python) reject unknown fields.

## Run / SSE / Action / RCA models

- `Run`: run_id, request_id, tenant_id, principal, scope_kind, primary_cluster_id, intent, action_mode, target, time_range, status, state_version, parent_run_id, timestamps. Optimistic CAS on `state_version`.
- `SSEEvent`: event, run_id, sequence, timestamp, tenant_id, cluster_id (null for multi-cluster aggregate), payload.
- RCA scoring / Source Reliability / Hypothesis thresholds: see `aiops-agentic-v9.2-final.md` §36-40.
- Approval: binds action_hash, cluster, target, resourceVersion, expiry. R3/R4 no self-approval.

## Contract errata (Phase 3, INTERNAL-AUTH-P0-011)

- **Service identity signing = JWS EdDSA / Ed25519** (amended from HS256). Each direction uses an independent Ed25519 keypair; the verifier holds only the public key of the opposite direction. JWS `typ=AIOPS-CONTEXT`; legacy `typ=JWT` for internal contexts is forbidden. Service Credential (`X-Internal-Token`) is distinct from context signing. Replay (nonce + cache) is unified across all three contexts.
- **RoleScopeAssignment** is the canonical logical entity; its physical table is `scope_assignments` (evolved in-place). No parallel `role_scope_assignments` table.
- **`tenant_clusters`** added to express Tenant 1:N Cluster (UNIQUE(cluster_id), UNIQUE(tenant_id, cluster_id)).
- **`token_version`**: JWT carries and verifies `token_version`; exactly ONE MySQL authoritative token-version source (per current design), no duplicated authority. `token_version` is an invalidation mechanism, not an authorization fact.

## Service Identity deployment (P3.5)

Four independent credential/key groups are provisioned via Helm Secret `aiops-secrets`:

```text
ORCHESTRATOR_TO_QUERY_TOKEN           (orchestrator→query service credential)
ORCHESTRATOR_TO_QUERY_SIGNING_KEY     (Ed25519 private, signer: orchestrator only)
ORCHESTRATOR_TO_QUERY_VERIFY_KEYS     (Ed25519 public, verifier: query-api only)
QUERY_TO_ORCHESTRATOR_TOKEN           (query→orchestrator service credential)
QUERY_TO_ORCHESTRATOR_SIGNING_KEY     (Ed25519 private, signer: query-api only)
QUERY_TO_ORCHESTRATOR_VERIFY_KEYS     (Ed25519 public, verifier: orchestrator only)
```

Private keys mount only on the signer side; public keys only on the verifier side; no private key in ConfigMap; no key content in logs/reports. Legacy single `INTERNAL_TOKEN` remains as transition until P3.9 cutover.

## PHASE3_TRANSITION_ONLY

The legacy signer/verifier (typ=JWT, signed single `contract.RequestContext`) is retained **only** for existing production callers until P3.9 cutover. It is not a long-term compatibility architecture. After P3.9, production legacy signer call sites = 0 and legacy verifier call sites = 0, then the legacy signing path is deleted (not deferred to Phase 14).

## Phase 2 test status

```text
Python contract tests (tests/test_contracts.py): 19 passed
Go contract tests (internal/contract): ok
Frontend typecheck (tsc --noEmit): PASS
Frontend production build: PASS
```

The old-path tests to be removed in Phase 14 (`test_loop_iterations`, `test_checkpointer`) and the Phase 3 production-caller integration (`test_internal_context_callers`) are recorded, not fixed in Phase 2.
