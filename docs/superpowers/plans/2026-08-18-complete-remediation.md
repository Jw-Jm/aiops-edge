# Complete Remaining Remediation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Close the documented August 18 backend-contract and user-visible remediation gaps without overwriting the existing uncommitted remediation work.

**Architecture:** Preserve the existing frontend → query-api → ai-orchestrator boundary. Add missing query-api contracts with focused Go tests, make stored chat-session data lossless enough for the existing UI message model, then complete the front-end feedback and accessibility work against those stable contracts. Keep dependencies pinned and buildable from `deploy/offline` whenever a new artifact is required.

**Tech Stack:** React 18, TypeScript, Vite, Go `net/http`, Python FastAPI, pytest, Helm.

**Spec:** `AIOPS_FIX_PLAN_2026-08-18.md` sections 7.2–7.3 and `AIOPS_TEST_REPORT_R3_2026-08-18.md` sections 2–5.

## Global Constraints

- Work directly on the user-authorized `main` worktree; preserve all pre-existing uncommitted changes.
- Do not reset, checkout, stash, or bulk-format unrelated files.
- Add an automated regression test before each production behavior change.
- Do not add an unpinned dependency; use the existing package lock, Go module metadata, and `deploy/offline` cache flow.
- Do not claim runtime validation for UI or cluster behavior until it has been exercised against a deployed stack.

---

### Task 1: Complete query-api contracts

**Files:**
- Modify: `ai-apm-query-go/internal/api/alerts.go`
- Modify: `ai-apm-query-go/internal/api/handler.go`
- Modify: `ai-apm-query-go/internal/api/infrastructure.go`
- Test: `ai-apm-query-go/internal/api/alerts_test.go`
- Test: `ai-apm-query-go/internal/api/handler_test.go`
- Test: `ai-apm-query-go/internal/api/nodes_metrics_test.go`

**Interfaces:**
- Produces `PUT /api/v1/alerts/rules/{id}` with the same validated rule shape as rule creation.
- Produces `GET /api/v1/traces?hours=<1..>` with a bounded `start_time` predicate.
- Produces node metrics filtered by `cluster_id` when it is neither blank nor `all`.

- [ ] Write tests that expect PUT to update an alert rule, `hours=6` to add a six-hour Trace predicate, and a cluster-specific node query to include the cluster filter.
- [ ] Run the focused Go tests and record the expected red failures.
- [ ] Implement the three minimal handler changes, preserving existing GET/DELETE and all-cluster behavior.
- [ ] Run the focused package tests and `go test ./...` in `ai-apm-query-go`.

### Task 2: Make chat-session and marketplace contracts match the UI

**Files:**
- Modify: `ai-orchestrator/main.py`
- Modify: `ai-orchestrator/marketplace.py` only if source-type propagation is absent there
- Test: `ai-orchestrator/tests/test_checkpointer.py`
- Test: `ai-orchestrator/tests/test_marketplace.py`

**Interfaces:**
- Produces session responses whose `messages` retain `kind`, plan, script, risk and execution-result fields used by `AiChat`.
- Consumes the optional `source_type` supplied by marketplace installation without changing existing default behavior.

- [ ] Write regression tests for a checkpoint containing a suggestion or execution-result message and for a marketplace installation carrying `source_type`.
- [ ] Run tests in the repository’s compatible Python runtime; if imports fail, establish the pinned offline dependency environment before changing production code.
- [ ] Implement lossless session serialization and source-type propagation with backward-compatible defaults.
- [ ] Run the focused pytest files and the orchestrator suite available in the compatible environment.

### Task 3: Fix the R3 interaction gaps

**Files:**
- Modify: `ai-apm-query-go/internal/api/handler.go` or service-detail handler module
- Modify: `ai-orchestrator/flow_engine/store.py` and/or `flow_engine/engine.py`
- Modify: `observability-frontend/src/pages/ai/Knowledge.tsx`
- Modify: `observability-frontend/src/pages/infra/Changes.tsx`
- Modify: `observability-frontend/src/pages/ai/AiChat.tsx`
- Add or modify focused tests beside the responsible Go/Python modules

**Interfaces:**
- Service detail supplies the fields actually rendered by the service-detail drawer, or the drawer consumes the canonical topology endpoint.
- Workflow runs persist and return a terminal run record.
- Playbook content is rendered after YAML frontmatter removal.
- Invalid change submissions show validation/submission errors; knowledge insertion reports inserted versus duplicate results.

- [ ] Write focused failing tests for each backend behavior; add TypeScript-level test coverage where the project has a compatible runner, otherwise verify with `tsc` and production build after the smallest change.
- [ ] Implement service-detail data and workflow-run persistence before changing their corresponding UI consumers.
- [ ] Implement frontmatter stripping, change-form feedback, and knowledge insertion feedback without changing API semantics.
- [ ] Run Go/Python focused tests and the frontend production build.

### Task 4: Close remaining usability and AI-routing gaps

**Files:**
- Modify: `observability-frontend/src/pages/ai/AiTools.tsx`
- Modify: `observability-frontend/src/pages/infra/K8sActions.tsx`
- Modify: affected paginated pages under `observability-frontend/src/pages/`
- Modify: `ai-orchestrator/orchestrator.py` and tests only if explicit tool-keyword routing is not already enforced

**Interfaces:**
- Destructive NL2SQL requests receive an explicit read-only explanation.
- K8s actions require a visible confirmation after successful preflight and before execution.
- Explicit `k8sgpt` and knowledge-search requests route to the matching tool and present its result.

- [ ] Add failing backend tests for explicit tool-keyword routing and read-only NL2SQL messaging.
- [ ] Implement the smallest routing and message changes, then run their pytest coverage in the compatible environment.
- [ ] Add the K8s confirmation and remaining front-end feedback/accessibility adjustments.
- [ ] Run the frontend build and the focused backend tests.

### Task 5: Make Python verification reproducible offline and perform final regression

**Files:**
- Modify: `deploy/scripts/fetch-offline-deps.sh` only if it does not already cache the pinned Python wheels needed by `ai-orchestrator/requirements.txt`
- Modify: relevant dependency metadata only when a tested compatible pin is required
- Test: existing Go, Python, Helm, and frontend validation commands

**Interfaces:**
- A local cache contains every newly required, version-pinned Python distribution before it is used by the build/test environment.

- [ ] Inspect installed and pinned Python package versions to identify the exact incompatibility.
- [ ] Prefer an existing cached artifact; if absent, request network approval before downloading a pinned wheel into `deploy/offline` and record its source/version.
- [ ] Create or use an isolated local virtual environment from the cached wheels and run the orchestrator regression suite.
- [ ] Run `npm run build`, all three Go service test suites, Python tests, Helm lint/template, and `git diff --check`; report every remaining failure explicitly.
