# Phase 0 Data Map

## Current storage ownership and observed schema

| Store | Current data | Current writer(s) | Current reader(s) | Ownership issue |
|---|---|---|---|---|
| ClickHouse `observability.log_records` | raw/normalized logs | ingest writer; query-api reads | query-api, frontend, orchestrator through query-api | target requires VictoriaLogs as raw-log primary; current ClickHouse log path remains |
| ClickHouse `observability.trace_spans` | spans/traces | ingest writer | query-api, frontend, orchestrator through query-api | current identity still mixes service/pod/default cluster conventions |
| ClickHouse `observability.service_topology` | derived topology | ingest and query-api sync paths | query-api/frontend/orchestrator KG | duplicate derivation and incomplete canonical identity |
| ClickHouse `observability.alert_events` | alert lifecycle/events | query-api alert engine | query-api/frontend | current table lacks tenant column in initialization and uses string/default cluster semantics |
| ClickHouse `observability.k8s_events` | Kubernetes resource events | event collector runtime path | query-api/orchestrator | not present in the central initialization file; collector ownership/DDL must be resolved |
| VictoriaMetrics | aggregated metrics and self-metrics | ingest, categraf, alert components | query-api/frontend/orchestrator | labels and tenant/cluster enforcement are not yet uniform |
| VictoriaLogs | shipped/queryable logs | query-api log shipper and/or deployment integrations | query-api/frontend | target primary raw-log ownership must be frozen and implemented |
| MySQL `aiops` | users, tenants, clusters, settings, alerts/config, catalog, topology, audit | query-api `EnsureSchema` and DAOs | query-api/frontend | startup DDL is broad; `clusters.kubeconfig` is sensitive plaintext storage |
| MySQL orchestrator migrations | approvals, audit, agents, reports, rules, IPMI, changes | orchestrator migration runner | orchestrator/query-api | `audit_logs` has competing definitions/writers with query-api |
| LangGraph SQLite/checkpoint/session/Flow stores | AI runtime/session/workflow history | orchestrator | orchestrator | target business source of truth is ControlPlaneRun; old history is not migrated |
| ChromaDB | RAG/vector index if configured | orchestrator knowledge/RAG paths | Knowledge/RAG paths | runtime availability not proven; valid knowledge assets must be preserved |
| MinIO | large evidence/artifacts if configured | orchestrator/report paths | report/evidence paths | runtime availability and prefixes not proven; runtime history must be separated from knowledge objects |

## Current ClickHouse schema facts

The central Helm SQL defines `log_records`, `service_topology`, `trace_spans`, and `alert_events` with 30-day TTLs and `ReplacingMergeTree`. Several columns use `cluster_id String DEFAULT 'default'`; `alert_events` currently does not include `tenant_id`. The schema includes service/pod fields rather than the target canonical `resource_id` contract.

## Current MySQL conflicts

- Query-api `internal/store/mysql.go` creates `audit_logs` and performs inline compatibility ALTERs.
- Orchestrator `migrations/0001_business_tables.sql` also creates `audit_logs` with a different definition.
- Query-api `clusters` schema and DAO persist `kubeconfig`; target requires `credential_ref` only.
- Orchestrator `0004_change_events.sql` uses `cluster_id VARCHAR(64) DEFAULT 'default'`, which is incompatible with immutable UUID-only identity.

## Target data protection inventory rule

No deletion was performed. Before Phase 16, the implementation must generate a `DATA_DELETION_MANIFEST` from actual tables, PVCs, Chroma collections, MinIO prefixes, checkpoints, and Flow stores. Each item must be `DELETE`, `PRESERVE`, or `UNKNOWN`; `UNKNOWN` is protected. Users, RBAC, tenants, Secrets, certificates, current LLM/data-source settings, valid knowledge, Runbooks, and active governance configuration are preserved.
