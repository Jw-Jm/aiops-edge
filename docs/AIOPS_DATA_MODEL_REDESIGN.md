# AIOps V9.2 — Data Model Redesign (frozen target)

Status: **FROZEN TARGET / NOT_YET_IMPLEMENTED** (Phase 2 freezes; new schema lands Phase 4).

## Storage responsibilities (V9.2 §21)

```text
VictoriaMetrics   = raw metrics Source of Truth
VictoriaLogs      = raw logs Source of Truth
ClickHouse        = Trace/Span + RED + Topology + Alert + Resource Event + Change + LogPattern/derived
MySQL             = Users/Sessions/RBAC + Tenant/Cluster registry + Platform config + AI Runtime
MinIO             = large evidence object + knowledge object
ChromaDB          = knowledge vector index
```

Forbidden: raw logs full copy to VictoriaLogs + ClickHouse; moving all metrics/logs to a single DB.

## Identity model (V9.2 §6-10, §12)

- Tenant: true multi-tenant schema, one initialized tenant (UUID, slug=`default`). No `tenant_id=1`, no fallback, no singleton.
- User ↔ Tenant: many-to-many via `user_tenants` / `role_scope_assignments`. User identity global unique.
- Cluster: single-tenant ownership (tenant 1:N cluster), canonical immutable UUID; slug human-readable; name display.
- Resource: canonical Resource ID does NOT include tenant; tenant is isolation dimension.

## Target tables

**Auth/control plane (MySQL):**
```text
users, sessions(auth_sessions), roles, permissions, user_tenants, role_permissions,
role_scope_assignments, tenants, tenant_clusters, clusters, system_config, internal_request_nonces
```

**AI runtime (MySQL, Phase 4):**
```text
ai_runs, ai_run_clusters, ai_plan_steps, ai_tool_runs, ai_evidence, ai_hypotheses,
ai_actions, ai_verifications, ai_approval_decisions, ai_run_events, ai_audit_events
platform_audit_events (query-api-owned, independent)
```

**ClickHouse (Phase 4):**
```text
observability.trace_spans, service_topology, alert_events, k8s_events,
log_patterns/derived analytics
```
Raw logs stay in VictoriaLogs; raw metrics in VictoriaMetrics.

## Key columns / constraints

- `ai_runs`: run_id, request_id, tenant_id, principal, scope_kind, primary_cluster_id, intent, action_mode, target, time_range, status, state_version (BIGINT), parent_run_id, timestamps.
- `ai_run_clusters`: (run_id, cluster_id) unique.
- Multi-cluster isolation: ai_tool_runs/ai_evidence/ai_hypotheses/ai_actions/ai_verifications carry cluster_id NOT NULL. PlanStep aggregate nullable, tool-exec NOT NULL.
- `ai_evidence`: large payload → MinIO; MySQL stores raw_ref/digest/summary/metadata. provenance_fingerprint for dedup.
- Optimistic CAS on `state_version` (no last-write-wins).

## Schema versioning & initialization (V9.2 §70)

Unified schema init; runtime accounts without DDL permission can start; schema version recorded; second init idempotent. Phase 4 builds new structures without physically deleting old history. Raw Logs only to VictoriaLogs.

## History data principle (V9.2 §91)

NO migration/conversion/legacy adapter. NO physical delete before Phase 17 authorization. After Phase 6, old physical data may exist but old reader/writer must not.

## Implementation status

```text
New schema init:      PLANNED (Phase 4)
Writer refactor:      PLANNED (Phase 5)
Reader/query layer:   PLANNED (Phase 6)
```

---

# 更新：V9.3 当前实现状态（Phase 21 P21.1，2026-08-23）

## 已实现（与真实运行代码一致）
- **MySQL `aiops`（52 表）**：Authorization/审计/配置/catalog/Run/Approval 权威 SoT。关键表：users/sessions(auth_sessions)/roles/permissions/user_tenants/role_permissions/role_scope_assignments/tenants/tenant_clusters/clusters/system_config/internal_request_nonces + AI runtime 表（ai_runs/ai_run_clusters/ai_plan_steps/ai_tool_runs/ai_evidence/ai_hypotheses/ai_actions/ai_verifications/ai_approval_decisions/ai_run_events/ai_audit_events/platform_audit_events）。
- **VictoriaMetrics** = raw metrics SoT（new writer ACTIVE）。
- **VictoriaLogs** = raw logs SoT（new writer ACTIVE）。
- **ClickHouse `observability`** = k8s_events/alert_events/log_records/trace_spans/service_topology（legacy 数据保留，只停流量不删数据）。
- **Chroma** = knowledge vector index（orchestrator ops-cases，2 collections）。
- **MinIO**：本实现未使用（large evidence object 未接入；Chroma 承载 knowledge）。

## 已实现关键约束
- `ai_runs` CAS（state_version BIGINT）生效；`ai_run_clusters` (run_id,cluster_id) unique。
- 多 cluster 隔离：tool/evidence/hypothesis/action/verification 带 cluster_id NOT NULL；EvidenceScopeMismatch 阻断跨 cluster。
- provenance_fingerprint 去重生效。
- schema 迁移（aiops_schema_migrations，5 迁移）幂等 + checksum。

## 边界
- 真实系统接入（MinIO 大对象）未实施（In-memory/Chroma 承载）；执行 Production Execution NOT APPROVED。
- GIT_ACTION=NONE。
