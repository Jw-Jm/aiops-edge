# AIOps V9.2 Phase 1 — Data Map

Baseline HEAD: `a8fdb5d`. Read-only inventory of storage, tables, writers, readers, and known conflicts.

## Storage roles (current)

| Store | Role today | V9.2 target role |
|---|---|---|
| VictoriaMetrics | raw metrics | raw metrics Source of Truth |
| VictoriaLogs | raw logs | raw logs Source of Truth |
| ClickHouse | trace/log/topology/alert | Trace/Span + RED + Topology + Alert + Resource Event + Change + LogPattern/derived |
| MySQL | config / catalog / auth / AI runtime | Users/Sessions/RBAC + Tenant/Cluster registry + platform config + AI runtime |
| MinIO | not present | large evidence object + knowledge object (future) |
| Chroma | not a service; in-container lib | knowledge vector index |

## ClickHouse tables (from `deploy/helm/aiops/files/clickhouse/init_clickhouse.sql`)

```text
observability.alert_events
observability.log_records
observability.trace_spans
observability.service_topology
```
`ai-event-collector` self-creates `observability.k8s_events` at runtime (not yet in unified init) — V9.2 Phase 4 must converge.

## MySQL tables (from helm mysql migrations + orchestrator migrations)

```text
schema_migrations
users / sessions / roles / permissions (auth)
clusters / tenant_clusters (registry)
catalog services / devices / tenants
alert rules / silences / alert events
audit_logs   (orchestrator, business AI/approval/execution/verification audit)
platform_audit_logs (query-api, separate)
change_events
reports / rules / agents / skills
approval_tasks / approval_decisions
ipmi_sel_events / ipmi_sensors / node_component_health
ai flows / sessions (old AI runtime)
```

**Known ownership conflict (V9.2 Phase 4 must resolve):** `audit_logs` is created by both `ai-apm-query-go/internal/store/mysql.go` and `ai-orchestrator/migrations/*.sql` with different column widths/indexes. Per V9.2, orchestrator = business-semantic owner of AI audit; query-api = MySQL persistence owner via `/internal/v1/control-plane/...`. `platform_audit_events` is query-api-owned and independent.

## V9.2 target AI runtime tables (new in Phase 4)

```text
ai_runs, ai_run_clusters, ai_plan_steps, ai_tool_runs, ai_evidence,
ai_hypotheses, ai_actions, ai_verifications, ai_approval_decisions,
ai_run_events, ai_audit_events
platform_audit_events (independent, query-api-owned)
```
`ai_runs.state_version` BIGINT; optimistic CAS updates. `ai_evidence` stores raw_ref/digest/summary/metadata (large payload → MinIO).

## Identity model (V9.2 frozen)

- `tenant_id`: generated UUID, slug=`default`, name=`Default Tenant`. No `tenant_id=1`, no fallback.
- `cluster_id`: immutable UUID (canonical). `slug` human-readable, `name` display. Resource ID built on `cluster_uuid`, not slug.
- Canonical Resource Identity: `service:<cluster_uuid>:<ns>:<svc>` etc. — **does not include tenant_id**; tenant is authorization/isolation dimension.
- `credential_ref`: MySQL stores `k8s-secret://<namespace>/<secret-name>`; per-cluster Secret; Kubernetes Access Boundary only resolves.

## Multi-cluster isolation (V9.2)

Objects carrying `cluster_id NOT NULL`: `ai_tool_runs`, `ai_evidence`, `ai_hypotheses`, `ai_actions`, `ai_verifications`. PlanStep: aggregate step nullable, tool-exec step NOT NULL. No single Tool queries A+B; no cross-cluster Evidence/Hypothesis mix. Write actions only from a derived single-cluster remediation run.

## Runtime data baseline (local acceptance env)

`observability` namespace has Running: ai-orchestrator, query-api, ingest, event-collector, frontend, mysql, clickhouse, victoria-metrics, victoria-logs; plus deepflow workloads and metrics-server. Old-flow data is physically present; V9.2 forbids any physical delete before Phase 17 authorization.
