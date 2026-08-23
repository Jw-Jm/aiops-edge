# AIOps V9.2 — Data Ownership (frozen)

Status: **FROZEN TARGET** (Phase 2). Resolves the audit_logs double-DDL conflict and fixes the AI Runtime persistence boundary.

## Ownership model (V9.2 §25)

```text
ai-orchestrator = AI Runtime business-semantic owner
ai-apm-query-go = MySQL persistence owner
```

- orchestrator decides when AI runtime entities are produced, how state advances, and event semantics.
- query-api owns MySQL schema, transactions, repositories, tenant isolation, DB writes, queries — via `/internal/v1/control-plane/...`.
- orchestrator must NOT maintain its own MySQL repository.
- Platform's own audit is separate (`platform_audit_events`, query-api-owned).

## AI Runtime tables (query-api persistence owner)

```text
ai_runs, ai_run_clusters, ai_plan_steps, ai_tool_runs, ai_evidence,
ai_hypotheses, ai_actions, ai_verifications, ai_approval_decisions,
ai_run_events, ai_audit_events
```

## Platform audit (query-api-owned, independent)

```text
platform_audit_events
```

## Conflict resolution

Current known conflict: `audit_logs` is created by both `ai-apm-query-go/internal/store/mysql.go` and `ai-orchestrator/migrations/*.sql` with different column widths/indexes. Per V9.2:

- AI audit events (orchestrator business semantics) → `ai_audit_events`, persisted by query-api.
- Platform audit (query-api operations) → `platform_audit_events`, query-api-owned.
- No two services own the same table DDL/repository.

## Canonical logical entity → physical table mapping (V9.2 amended)

```text
Canonical logical entity: RoleScopeAssignment
Physical MySQL table:     scope_assignments
```

No parallel `role_scope_assignments` table is created. `scope_assignments` is evolved in-place and is the single physical table for RoleScopeAssignment.

`tenant_clusters` is added to explicitly express Tenant 1:N Cluster:
```text
tenant_clusters(tenant_id, cluster_id, created_at)
UNIQUE(cluster_id)
UNIQUE(tenant_id, cluster_id)
```
`cluster_id` unique guarantees a cluster has exactly one owning tenant.

## Persistence boundary (V9.2 §25, §18)

Chain:

```text
orchestrator → /internal/v1/control-plane/... → query-api → MySQL
```

orchestrator never holds kubeconfig/credential content (Kubernetes Access Boundary only). Execution Adapter lives in-process in query-api as an independent security module.

## Phase 3 migration classification

The following production artifacts still use old semantics and are registered as `PHASE3_PENDING_MIGRATION` (must be fixed before Phase 3 Gate; do not hide as success):

```text
ai-apm-query-go/internal/biz/resource_resolver.go   (generates tenant-containing urn)
ai-apm-query-go/internal/api/* authz context tests  (old ResourceRef semantics)
ai-orchestrator/tests/test_internal_context_callers.py::test_orchestrator_alert_collection_requires_explicit_context
```

## Implementation status

```text
Persistence owner split:  PLANNED (Phase 4/5/7)
audit_logs conflict fix:   PLANNED (Phase 4)
Phase3 migration items:    PHASE3_PENDING_MIGRATION
```
