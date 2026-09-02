# ChatTool Audited Live Read Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

> **执行状态（2026-09-02）：** Tasks 1–5 的本机代码、迁移、边界测试和报告更新已落地并验证；候选 MySQL、真实 Provider 和多副本运行证据仍是发布门禁。本轮不自动提交或推送。

**Goal:** Enable ordinary AIOps Chat to perform narrowly scoped real-time read-only diagnostics only when every tool invocation has a durable Query API-owned ChatTool audit record, while keeping pure conversation data-free and complex RCA/actions on Investigation.

**Architecture:** Query API owns a new `ai_chat_tool_runs` MySQL table and creates/completes one audit row around each Chat read through the canonical `/internal/v1/query/*` boundary. The signed `TrustedRequestContext` remains the authorization source; it carries the authenticated user/session, tenant, cluster, capability, and `workload_kind=chat`, while the request body carries chat session/turn/tool-call identifiers. Orchestrator Chat tools use `InternalQueryClient` with a chat execution context; any missing audit persistence, scope mismatch, unsupported operation, or completion failure fails closed and never exposes live data.

**Tech Stack:** Go 1.x (`ai-apm-query-go`), MySQL versioned migrations, Python 3.14/FastAPI/LangGraph (`ai-orchestrator`), pytest, Go `go test`/`go vet`, existing EdDSA TrustedRequestContext and mTLS service boundary.

**Spec:** `docs/superpowers/specs/2026-08-25-aiops-workflow-convergence-design.md` (Chat Boundary, ToolRun/Evidence Contract, Workflow Convergence)

## Global Constraints

- Pure conversation performs no live collection.
- Ordinary Chat diagnostics may perform narrowly scoped read-only queries only with a separate persistent ChatTool audit record; they must not masquerade as an Investigation Run.
- Structured RCA, cross-source evidence correlation, action proposals, and execution require an explicit Investigation Run.
- Query API/MySQL remains the sole owner of Chat sessions, ChatTool audit records, runtime state, and authorization truth.
- Every internal data call uses a fresh signed `TrustedRequestContext`; body tenant/cluster cannot override it; no `X-Tenant-ID`, default tenant, default cluster, or direct backend/Kubernetes access.
- ChatTool audit stores hashes/digests and bounded metadata, never raw secrets or unrestricted result payloads.
- Audit start must succeed before datasource I/O; audit completion must succeed before the orchestrator returns live data.
- Existing Investigation `ai_tool_runs` semantics, lease fencing, evidence conversion, and public contracts must remain unchanged.
- Use TDD: each production behavior starts with a failing test and is verified before refactoring.
- Do not use fixture data as production evidence; local runtime tests may use existing isolated mocks only where the repository already defines them.

---

### Task 1: Freeze ChatTool audit contract and MySQL schema（本机已完成）

**Files:**
- Create: `ai-apm-query-go/internal/store/ai_chat_tool_runs.go`
- Create: `ai-apm-query-go/internal/store/ai_chat_tool_runs_test.go`
- Create: `ai-apm-query-go/internal/store/migrations/versions/0017_ai_chat_tool_runs.sql`
- Modify: `ai-orchestrator/contracts.py` (add `ChatToolAuditStatus`/`ChatToolAudit` wire model only if the Python boundary needs validation)
- Modify: `observability-frontend/src/api/contracts.ts` (mirror the public read-only audit summary only if exposed by an API)
- Test: `ai-apm-query-go/internal/store/migrations/schema_manifest_test.go`

**Interfaces:**
- Produces `store.AIChatToolRun` with fields `ChatToolRunID`, `PrincipalID`, `SessionID` (authenticated session), `ChatSessionID`, `TurnID`, `ToolCallID`, `TenantID`, `ClusterID`, `ToolName`, `Operation`, `Capability`, `ArgsHash`, `Status`, `ResultDigestSHA256`, `ResultCount`, `ErrorCode`, `StartedAt`, `CompletedAt`, and `CreatedAt`.
- Produces `AIChatToolRunDAO.Start(AIChatToolRun) (created bool, existing *AIChatToolRun, err error)` and `Finish(id, tenantID, clusterID, principalID, status, resultDigest, count, errorCode string) error`.
- Idempotency key is `(chat_session_id, turn_id, tool_call_id, args_hash)`; same key and same hash replays the existing row, same key with another hash returns a typed conflict.
- Allowed terminal statuses are `success`, `no_data`, `partial`, `failed`, and `unavailable`; a row is always created in `running` before datasource I/O.

- [ ] **Step 1: Write failing DAO and migration tests**

  Add tests that assert: schema contains the table and indexes; missing required scope/identity is rejected; two starts with the same key converge idempotently; a different `args_hash` is rejected; finish checks tenant/cluster/principal ownership and writes terminal fields; no raw result body is persisted.

- [ ] **Step 2: Run the focused tests to verify RED**

  Run `cd ai-apm-query-go && go test ./internal/store -run 'ChatTool|Migration' -count=1`.
  Expected: FAIL because the table, DAO, and model do not exist.

- [ ] **Step 3: Add migration 0017**

  Create an InnoDB table with UUID/identity/scope columns, bounded enums enforced in application validation, indexes for `(chat_session_id, turn_id, tool_call_id)`, `(tenant_id, cluster_id, created_at)`, and no foreign key to `ai_runs`. Store only `args_hash`, `result_digest_sha256`, counts, stable error codes, and timestamps.

- [ ] **Step 4: Implement the DAO**

  Use parameterized SQL and `INSERT ... ON DUPLICATE KEY` followed by a read-back. Validate canonical UUIDs, non-empty operation/capability, bounded tool name, and valid state transitions. Return explicit errors for ownership mismatch, idempotency conflict, missing row, or database unavailability.

- [ ] **Step 5: Run the focused tests to verify GREEN**

  Run the same `go test` command and require PASS with all `sqlmock` expectations satisfied.

- [ ] **Step 6: Commit the contract/schema unit**

  Run `git add ai-apm-query-go/internal/store ai-apm-query-go/internal/store/migrations/versions/0017_ai_chat_tool_runs.sql ai-orchestrator/contracts.py observability-frontend/src/api/contracts.ts` and commit with `feat: add durable chat tool audit contract`.

---

### Task 2: Enforce ChatTool audit at the Query API internal query boundary（本机已完成）

**Files:**
- Modify: `ai-apm-query-go/internal/api/handler.go` (wire `chatToolDAO`)
- Modify: `ai-apm-query-go/internal/api/internal_query_envelope.go` (retain principal/session/workload in trusted context)
- Modify: `ai-apm-query-go/internal/api/internal_query.go` (decode Chat fields and audit gate)
- Modify: `ai-apm-query-go/internal/api/toolrun_wrapper.go` or create `ai-apm-query-go/internal/api/chat_tool_wrapper.go` (Chat-specific start/finish/replay envelope)
- Modify: `ai-apm-query-go/internal/bootstrap/http.go` only if a dedicated audit route is needed; prefer embedding lifecycle in canonical query handlers
- Create/modify: `ai-apm-query-go/internal/api/chat_tool_audit_test.go`
- Modify: `ai-apm-query-go/internal/api/internal_query_test.go`

**Interfaces:**
- Internal request fields: `workload_kind`, `chat_session_id`, `chat_turn_id`, `chat_tool_call_id`, `chat_tool_name`, and `chat_args_hash`.
- `internalQueryCtx` gains trusted `PrincipalType`, `PrincipalID`, and `SessionID`; these values come only from the verified JWS.
- Every `/internal/v1/query/*` handler calls a shared `beginChatToolAudit` when the signed workload is `chat`; handlers never execute a Chat query without a running audit row.
- The shared completion path calls `finishChatToolAudit` and suppresses the result if persistence fails. Existing Investigation `beginToolRun`/`finishToolRun` behavior remains separate.

- [ ] **Step 1: Write failing boundary tests**

  Add tests for: signed `chat` plus complete identity and Chat fields creates a row before the repository is called; missing Chat fields returns `VALIDATION_FAILED` before ClickHouse/Kubernetes access; missing DAO/DB returns `TOOL_UNAVAILABLE` and the repository call count stays zero; successful query completes the row; completion failure returns `TOOL_UNAVAILABLE` without data; replay returns the durable envelope without a second datasource call; cross-tenant/session/turn ownership is denied; signed `investigation` and existing tests remain unchanged.

- [ ] **Step 2: Run the focused tests to verify RED**

  Run `cd ai-apm-query-go && go test ./internal/api -run 'ChatTool|InternalQuery.*Chat' -count=1`.
  Expected: FAIL because Chat workload currently bypasses ToolRun/audit and the context drops principal/session.

- [ ] **Step 3: Preserve trusted identity and add Chat request validation**

  Copy principal/session fields from the verified context into `internalQueryCtx`; require `principal_type=user`, non-empty authenticated `session_id`, canonical `chat_session_id`, `chat_turn_id`, and `chat_tool_call_id` for signed `workload_kind=chat`. Require body workload to equal the signed workload and reject body-only claims.

- [ ] **Step 4: Implement the shared Chat audit lifecycle**

  Before repository execution, compute/validate the canonical args hash, call DAO `Start`, and handle replay/conflict. After repository execution, call DAO `Finish` with status, digest, and count. If finish fails, return `TOOL_UNAVAILABLE` and do not send the data envelope. Do not put result payloads in the audit table.

- [ ] **Step 5: Keep data-source errors explicit**

  Map no-data to terminal `no_data` with an empty successful envelope; map backend errors to `failed`/`unavailable`; never convert an audit or backend error into an empty healthy result. Preserve existing Investigation semantics and lease fencing.

- [ ] **Step 6: Run focused and package tests to verify GREEN**

  Run `cd ai-apm-query-go && go test ./internal/api ./internal/store -count=1` and `go vet ./internal/api ./internal/store`.

- [ ] **Step 7: Commit the Query API boundary unit**

  Commit with `feat: enforce audited chat query boundary`.

---

### Task 3: Add Chat execution context and route orchestrator read tools through Query API（本机已完成）

**Files:**
- Modify: `ai-orchestrator/tool_execution_context.py`
- Modify: `ai-orchestrator/internal_query_client.py`
- Modify: `ai-orchestrator/tools.py`
- Modify: `ai-orchestrator/invocation_scope.py`
- Modify: `ai-orchestrator/orchestrator.py`
- Modify: `ai-orchestrator/main.py` (propagate canonical chat session/turn identifiers into `stream_sync`/`execute_sync`)
- Create/modify: `ai-orchestrator/tests/test_chat_tool_audit_client.py`
- Modify: `ai-orchestrator/tests/test_p72_internal_query_client.py`
- Modify: `ai-orchestrator/tests/test_node_collect_logs.py`

**Interfaces:**
- `ToolExecutionContext.for_chat(tenant_id, cluster_id, principal_id, session_id, chat_session_id, turn_id, tool_call_id, tool_name, params)` returns an immutable chat context.
- `ToolExecutionContext.to_body()` emits only the Chat audit fields plus `workload_kind=chat`; it never emits Investigation lease fields for Chat.
- `InternalQueryClient.query(..., execution_context=chat_context)` signs a fresh context with `principal_type=user`, the authenticated session ID, `workload_kind=chat`, and the tool capability; it sends the canonical chat fields in the body.
- `stream_sync`/`execute_sync` accept optional `chat_session_id` and `chat_turn_id`; canonical `/internal/v1/chat` and legacy `/api/v1/ai/chat` pass them. Every tool call derives a stable UUIDv5 `tool_call_id` from turn + tool + canonical args.
- Chat `query_metrics`, `query_logs`, `query_traces`, `query_topology`, `get_service_list`, `get_infrastructure`, and `_collect_alerts` use the internal client when workload is `chat`; no Chat branch calls `_get_json` on public data routes, local graph, direct K8s, or local Chroma.

- [ ] **Step 1: Write failing Python tests**

  Add tests asserting Chat context serialization, user/session identity in signed claims, stable per-turn tool-call IDs, canonical internal paths for metrics/logs/traces/topology/kubernetes/alerts, rejection when Chat audit fields are absent, and no legacy public-query call for a scoped Chat request. Add a node-level test proving an ordinary diagnostic routes to `live_read`/collection only with the Chat context.

- [ ] **Step 2: Run focused tests to verify RED**

  Run `cd ai-orchestrator && .venv314/bin/python -m pytest -q -p no:cacheprovider tests/test_chat_tool_audit_client.py tests/test_p72_internal_query_client.py tests/test_node_collect_logs.py`.
  Expected: FAIL because non-Investigation calls are currently signed as `platform` and tool functions use legacy public paths.

- [ ] **Step 3: Implement Chat context propagation**

  Add chat-only fields with strict UUID validation and preserve existing Investigation constructor compatibility. Extend the serializable scope projection with chat session/turn values; do not persist secrets or lease tokens in checkpoints. Generate tool-call IDs deterministically from canonical JSON.

- [ ] **Step 4: Implement the Chat client path**

  In `InternalQueryClient`, select `workload_kind=chat` only when an explicit Chat context is supplied; use authenticated user principal/session, never a synthetic system identity. Include Chat fields in the request body and preserve capability-to-route binding and HTTP error semantics.

- [ ] **Step 5: Convert every ordinary Chat read tool**

  Add a helper that creates a Chat execution context and invokes `InternalQueryClient`. Route each read-only tool listed above through it, normalize the existing response envelope, and return explicit unavailable/error text on audit or backend failure. Keep mutation tools unavailable in Chat.

- [ ] **Step 6: Run focused tests to verify GREEN**

  Re-run the focused command, then run the full Python suite with the repository’s documented exclusions:
  `AIOPS_DEPLOYMENT_MODE=development AIOPS_ENV=development LLM_MOCK=true .venv314/bin/python -m pytest -q -p no:cacheprovider -k 'not test_uvicorn_protocol_rejects_wrong_san_over_real_tls and not test_production_full_reachable_returns_remote and not test_check_control_plane_reachable_reachable'`.

- [ ] **Step 7: Commit the orchestrator Chat path unit**

  Commit with `feat: route chat diagnostics through audited query client`.

---

### Task 4: Keep explicit Chat/Investigation intent split

**Files:**
- Modify: `ai-orchestrator/orchestrator.py`
- Modify: `ai-orchestrator/main.py`
- Modify: `ai-orchestrator/tests/test_chat_investigation_split.py`
- Create/modify: `ai-orchestrator/tests/test_chat_intent_routing.py`
- Modify: `observability-frontend/src/pages/ai/AiChat.tsx` only if the returned intent/CTA contract requires a UI label change

**Interfaces:**
- `node_chat_classify` returns one of `conversation`, `knowledge`, `live_read`, `investigation`, or `mutation` plus `chat_pure`, `investigation_required`, and a stable user-facing response.
- `conversation` skips collection; `knowledge` uses only the audited/authorized knowledge path; `live_read` runs the scoped Chat collection; `investigation` returns the explicit CTA; `mutation` returns the Investigation/approval CTA and never executes.
- The frozen design uses an explicit deterministic CTA list for structured RCA/cross-service/graph/K8sGPT/mutation requests. Ordinary diagnostics remain audited live reads; pure conversation remains data-free. Any future semantic classifier must preserve this fail-closed boundary and add regression tests before rollout.

- [ ] **Step 1: Write failing intent tests**

  Cover greetings, general explanations, “总结当前集群告警” → `live_read`, “order-svc 当前 P99” → `live_read`, “解释 P99” → `knowledge`/conversation without live read, “完整根因分析” → `investigation`, and “重启 Pod/执行 kubectl” → `mutation`/CTA. Verify `live_read` without a complete Chat context returns unavailable and performs no datasource call.

- [ ] **Step 2: Run focused tests to verify RED**

  Run `cd ai-orchestrator && .venv314/bin/python -m pytest -q -p no:cacheprovider tests/test_chat_investigation_split.py tests/test_chat_intent_routing.py`.
  Expected: FAIL because the current classifier only distinguishes pure conversation and CTA keywords.

- [ ] **Step 3: Implement the explicit intent result and routing**

  Keep pure conversation data-free, admit only narrow read-only live requests to the Chat collection branch, and preserve Investigation CTA for RCA/correlation/action/mutation. Do not reintroduce fixed full collection for every Chat turn.

- [ ] **Step 4: Run focused and full Python tests**

  Re-run the focused tests and the full documented suite from Task 3.

- [ ] **Step 5: Commit the intent/routing unit**

  Commit with `feat: classify chat conversation and live-read intents`.

---

### Task 5: Update deployment contracts, production整改方案, and verification gates（本机已完成，候选验证待执行）

**Files:**
- Modify: `deploy/helm/aiops/values.yaml` and `deploy/helm/aiops/values-prod.yaml` only where migration/configuration is required; do not add a fallback database or default scope
- Modify: `AIOps平台生产整改实施与复审报告.md` (replace stale CTA-only Chat recommendation and add evidence/acceptance criteria)
- Modify: `验收执行记录_2026-08-27.md` only if a new dated verification entry is required
- Create/modify: `scripts/` or existing validation script only if the repository already has a canonical gate for migration/readiness

**Interfaces:**
- Helm schema-migrator applies migration `mysql/0017-ai-chat-tool-runs`; query-api readiness requires the migration checksum before serving Chat live reads.
- The report’s Chat boundary section states the final behavior: pure conversation has no live I/O; ordinary live-read Chat is allowed only with durable ChatTool audit; Investigation handles structured RCA/actions; audit failure is fail-closed.
- Acceptance evidence includes migration checksum, DAO/API tests, Python routing tests, cross-scope negative tests, and an isolated runtime smoke test showing an audit row precedes a read and reaches a terminal status.

- [ ] **Step 1: Write failing deployment/report checks**

  Add or update contract tests that assert migration 0017 is embedded, Helm rendered migration/readiness configuration includes it, and the report contains no statement that all ordinary diagnostics must always CTA. Assert the report contains the auditable live-read acceptance criteria.

- [ ] **Step 2: Run checks to verify RED**

  Run `make helm-lint`, the migration manifest tests, and the report consistency check (if present). Expected: report/config checks fail until updated.

- [ ] **Step 3: Update deployment and report**

  Add no new data owner or fallback. Document exact table/status/identity/scope rules, failure behavior, test commands, and the remaining external production gates (registry/KMS, real credentials/cert rotation, HA/PITR/RPO/RTO, real observability marker) separately from this code fix.

- [ ] **Step 4: Run all local verification**

  Run:
  - `cd ai-apm-query-go && go test ./... && go vet ./...`
  - the full Python command from Task 3
  - `make helm-lint`
  - repository migration/readiness/contract tests
  - existing no-fixture validator, recording any real-data marker limitation as `BLOCKED_BY_ENV` rather than substituting fixtures

- [ ] **Step 5: Commit the deployment/report unit**

  Commit with `docs: document audited chat live-read production gate` after all code and test commits are green.

---

## Acceptance Matrix

| Scenario | Expected result | Evidence |
|---|---|---|
| “你好” / general explanation | No live datasource I/O | intent test + tool call count 0 |
| “总结当前集群告警” with valid scope | Read-only query allowed; one durable ChatTool row per tool call | DAO/API integration test + terminal audit row |
| Missing MySQL/audit persistence | No datasource I/O; stable `TOOL_UNAVAILABLE`/CTA | negative boundary test |
| Audit completion failure | Data suppressed; stable unavailable response | failure-injection test |
| Cross-tenant/session/cluster/namespace access | 403/409; no datasource I/O | scope tests |
| Duplicate same turn/tool/args | Exact durable replay; no second datasource call | idempotency test |
| Same idempotency key with different args | 409 conflict | args-hash test |
| RCA/cross-source/action request | Explicit Investigation CTA | routing tests |
| Chat mutation/script request | Rejected in Chat; no mutation endpoint | mutation routing test |
| Investigation path | Existing `ai_tool_runs`/lease/evidence semantics unchanged | full Go/Python regression suite |

## Self-Review Checklist

- [ ] Every frozen Chat boundary requirement maps to Tasks 1–4.
- [ ] No task introduces a second production data owner.
- [ ] No task relies on an unbounded default tenant/cluster or a legacy public data path.
- [ ] Audit failures are fail-closed before and after datasource I/O.
- [ ] Tests prove both positive live-read behavior and negative isolation behavior.
- [ ] Report update distinguishes code-complete local evidence from external production gates.
