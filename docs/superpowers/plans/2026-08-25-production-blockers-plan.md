# Production Blockers Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Close the five production-blocking consistency defects identified by the GitHub functionality audit.

**Architecture:** Keep Query API as the persistence authority and Action Executor as the only Kubernetes mutation boundary. Add explicit liveness/readiness semantics, propagate sink failures as retryable ingest failures, and keep LLM provider selection outside the forwarded provider API path.

**Tech Stack:** Go 1.x multi-module services, Python orchestrator configuration, Helm templates, Go `testing` and `httptest`.

**Spec:** `docs/superpowers/specs/2026-08-25-production-consistency-closure-design.md`

## Global Constraints

- `success` means the requested mutation was applied and verified by the data plane.
- Production Orchestrator must not regain direct MySQL or ClickHouse credentials.
- Query API → Action Executor signing keys remain separate from Query API → Orchestrator signing keys.
- Every behavior change gets a failing test before production code changes.

### Task 1: Action Executor fail-closed execution and trust-domain wiring

**Files:**
- Modify: `ai-action-executor/main.go`
- Test: `ai-action-executor/main_test.go`
- Modify: `deploy/helm/aiops/templates/ai-action-executor/deployment.yaml`
- Modify: `deploy/helm/aiops/templates/secrets.yaml` or the chart secret template that defines signing keys
- Test: existing executor and Helm chart tests, if present

**Interfaces:**
- Preserve the executor HTTP request/response contract.
- Approved execution without a usable mutation client returns a non-success status and an explicit non-applied message.
- The executor verifies Query API signatures with the dedicated `AI_ACTION_EXECUTOR_VERIFY_KEYS` secret.

- [ ] **Step 1: Write failing tests** for approved execution without Kubernetes access and for the dedicated verify-key environment binding.
- [ ] **Step 2: Run the targeted executor tests** and confirm they fail because the current path returns `success` and the chart references the wrong key.
- [ ] **Step 3: Implement the smallest fail-closed change**: reject startup or request execution for the invalid `approved + POD_SA_ACCESS=false` combination, and use the dedicated verify-key configuration.
- [ ] **Step 4: Run executor unit tests and `go vet`** for the module.
- [ ] **Step 5: Render the Helm chart** and assert the executor references the dedicated verify key and no longer references `QUERY_TO_ORCHESTRATOR_VERIFY_KEYS`.

### Task 2: Query API liveness/readiness and MySQL fail-fast

**Files:**
- Modify: `ai-apm-query-go/cmd/api/main.go`
- Modify: `ai-apm-query-go/internal/api/router.go` or the existing health handler file
- Modify: `ai-apm-query-go/internal/store/mysql.go`
- Test: existing Query API health/startup tests; add focused tests beside the health/router package
- Modify: `deploy/helm/aiops/templates/query-api/deployment.yaml`

**Interfaces:**
- `GET /livez` reports process liveness without requiring MySQL.
- `GET /readyz` reports dependency readiness and fails when MySQL or required bootstrap state is unavailable.
- `/health` may remain as a compatibility endpoint but must not be used as the Kubernetes readiness signal.

- [ ] **Step 1: Write failing tests** for `/livez` succeeding without DB and `/readyz` failing without DB.
- [ ] **Step 2: Run the targeted tests** and confirm the current `/health` behavior does not distinguish the states.
- [ ] **Step 3: Implement explicit liveness/readiness handlers** and make production startup return a fatal error when required MySQL initialization cannot complete.
- [ ] **Step 4: Update Query API Helm probes** to use `/livez`, `/readyz`, and `/livez` respectively.
- [ ] **Step 5: Run Query API package tests and module vet/build checks.**

### Task 3: Ingest log ACK semantics

**Files:**
- Modify: `ai-apm-ingest-go` OTLP/log receiver handler and the existing telemetry runtime wiring
- Test: the nearest OTLP/log handler test package
- Reuse: existing `WriteLog` failure tests in `ai-apm-ingest-go/internal/telemetry`

**Interfaces:**
- Any required log batch write failure produces a retryable HTTP 5xx response.
- Successful ACK is returned only after all required log writes succeed.

- [ ] **Step 1: Write a failing HTTP handler test** with a sink that returns an error and assert a retryable 5xx response.
- [ ] **Step 2: Run the focused ingest test** and confirm the handler currently returns 200.
- [ ] **Step 3: Propagate the existing `WriteResult`/sink error to the HTTP response** without changing successful payload compatibility.
- [ ] **Step 4: Run telemetry and receiver package tests.**

### Task 4: LLM Egress Proxy path and allowlist configuration

**Files:**
- Modify: `ai-llm-egress-proxy/main.go`
- Test: `ai-llm-egress-proxy/main_test.go`
- Modify: `ai-llm-egress-proxy/main_e2e_test.go`
- Modify: `deploy/helm/aiops/templates/ai-llm-egress-proxy/deployment.yaml` and the Orchestrator deployment template
- Modify: chart values and secret templates for proxy URL/token/allowlist

**Interfaces:**
- `/v1/proxy/{provider}/{path...}` forwards to `{provider-base}/v1/{path...}`.
- The configured `LLM_ALLOWLIST` controls provider authorization; default behavior remains explicit and deny-by-default.
- Orchestrator receives the proxy URL/token only when proxy mode is enabled.

- [ ] **Step 1: Strengthen the fake provider test** to assert `/v1/chat/completions` and add a configurable allowlist case.
- [ ] **Step 2: Run the focused proxy tests** and confirm the current implementation forwards the provider segment incorrectly or ignores the configured allowlist.
- [ ] **Step 3: Implement path forwarding and allowlist parsing** with explicit malformed-route errors.
- [ ] **Step 4: Wire Helm values and environment variables** for Orchestrator → Proxy without adding database credentials.
- [ ] **Step 5: Run proxy unit/E2E tests and render the relevant Helm templates.**

### Task 5: Production-blocker verification

**Files:**
- Modify only if needed: targeted test fixtures or deployment verification documentation

- [ ] **Step 1: Run all four affected Go module test suites.**
- [ ] **Step 2: Run Helm lint/template validation for the chart values used by production.**
- [ ] **Step 3: Check `git diff` for accidental credential or unrelated architecture changes.**
- [ ] **Step 4: Record remaining deferred findings explicitly:** Persistence Ownership, cold recovery, lease finishing, scheduler HA, and frontend release gates.
