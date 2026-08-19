# AIOps Phase 1 Architecture and Contracts Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Freeze schema ownership and create the first cross-service contract types and fixtures without switching any production request path.

**Architecture:** Query-api remains the external trust boundary and owns Control Plane Persistence; orchestrator owns AI Runtime state transitions but calls query-api through versioned internal APIs. Python, Go, and TypeScript fixtures share the same JSON contract examples, with strict status/enumeration validation and canonical UUID cluster identity.

**Tech Stack:** Python 3.12 target runtime with Pydantic/dataclasses, Go standard library JSON types and tests, TypeScript fixture types, MySQL/ClickHouse ownership documentation.

**Spec:** `aiops-agentic.md` Tasks 1.1 and 1.2 plus confirmed constraints in section 0.6.

## Global Constraints

- Query-api is the browser’s only business entry point.
- MySQL is the authoritative dynamic authorization source.
- `cluster_id` is an immutable UUID; `slug` is not a persistence foreign key.
- Every new runtime/control-plane record includes `tenant_id` and canonical `cluster_id`.
- Orchestrator never connects directly to MySQL or reads Kubernetes credentials.
- `audit_logs` is owned by orchestrator’s AI/approval/execution audit domain; query-api owns separate `platform_audit_logs`.
- Invalid contracts return stable error codes and field paths, not internal stack traces.
- Phase 1 does not cut production traffic, migrate historical runtime data, or delete existing tables.

### Task 1: Freeze schema ownership and target data model documentation

**Files:**
- Modify: `docs/SCHEMA_OWNERSHIP.md`
- Create: `docs/AIOPS_AGENTIC_ARCHITECTURE.md`
- Create: `docs/AIOPS_DATA_MODEL_REDESIGN.md`
- Test: `docs/luna/phase1-ownership-check.md`

- [ ] Record every current table/storage family with owner, writer, reader, retention, initialization, and cutover status.
- [ ] Resolve the `audit_logs` conflict explicitly: orchestrator owns `audit_logs`; query-api owns `platform_audit_logs`.
- [ ] Define raw-log ownership as VictoriaLogs, derived ClickHouse observability data, and MySQL control-plane/AI Runtime data.
- [ ] Define UUID-only `cluster_id`, explicit `tenant_id`, ResourceRef provenance, and the no-history-migration policy.
- [ ] Record exact current conflicts and a Phase 1 cutover order without changing runtime DDL or deleting tables.
- [ ] Run a static ownership check that flags duplicate DDL names and missing owners in the documented inventory.
- [ ] Commit as `docs(architecture): freeze phase 1 ownership and data model`.

### Task 2: Add Python contracts and failing contract tests

**Files:**
- Create: `ai-orchestrator/contracts.py`
- Create: `ai-orchestrator/tests/test_contracts.py`
- Create: `docs/contracts/contract-fixtures.json`

**Interfaces:**

```text
RequestContext, ResourceRef, ToolResult, Evidence, Hypothesis,
OpsAction, VerificationResult, OpsIntent, PlanStep, InvestigationPlan,
SSEEnvelope
```

- [ ] Write tests first for valid/invalid enum values, required UUID context, evidence references, and structured validation errors.
- [ ] Run the focused test file and confirm it fails because the contract module is absent.
- [ ] Implement minimal Pydantic v2 models or dataclasses with strict status/enumeration validation and JSON serialization.
- [ ] Add valid and invalid fixtures, including same-name services in two UUID clusters and a missing-cluster rejection.
- [ ] Run focused Python contract tests with the available Python runtime; record the required `.venv-312` blocker if unavailable.
- [ ] Commit as `feat(contracts): add python control plane contracts`.

### Task 3: Add Go RequestContext/ResourceRef contract types and tests

**Files:**
- Create: `ai-apm-query-go/internal/contract/context.go`
- Create: `ai-apm-query-go/internal/contract/context_test.go`
- Create: `ai-apm-query-go/internal/contract/fixture_test.go`
- Modify: `docs/contracts/contract-fixtures.json`

- [ ] Write Go tests first for JSON round-trip, UUID-only cluster identity, absent client roles/allowed clusters, and same-name resources across clusters.
- [ ] Run focused Go tests and confirm failure because the package/types are absent.
- [ ] Implement typed `RequestContext` and `ResourceRef` with JSON tags and explicit validation helpers; do not wire handlers yet.
- [ ] Verify Go serialization matches the Python fixture field names and status strings.
- [ ] Run focused and package Go tests with the writable cache; use network-enabled execution only when the sandbox blocks local test listeners.
- [ ] Commit as `feat(contracts): add go request context types`.

### Task 4: Add TypeScript fixture types and contract type-check

**Files:**
- Create: `observability-frontend/src/api/contracts.ts`
- Create: `observability-frontend/src/api/contracts.test-fixtures.ts`
- Modify: `docs/contracts/contract-fixtures.json`

- [ ] Add TypeScript types for the shared envelope and fixture objects; no page or API caller migration in this task.
- [ ] Add compile-time fixture assignments for valid and invalid-shape examples.
- [ ] Run the available frontend type-check/build command; if `node_modules` remains absent, record the environment blocker without installing dependencies or changing the lockfile in Phase 1.
- [ ] Commit as `feat(contracts): add frontend contract types`.

### Task 5: Phase 1 Gate review

- [ ] Re-run ownership static checks, Python/Go contract tests, TypeScript type-check where available, and `git diff --check`.
- [ ] Verify no production route, writer, reader, schema, dependency, or runtime data changed.
- [ ] Verify fixtures have no Secret/token/private-key values and all cluster references use UUIDs.
- [ ] Record Gate 1A and Gate 1 outcomes in `docs/luna/phase1-gate.md`.
- [ ] Stop before Phase 2 if contract or ownership checks fail.
