# Phase 4 Authoritative Schema Ownership Matrix（P4.2 交付物）

生成日期：2026-08-20。唯一权威来源：本文件 + `docs/SCHEMA_OWNERSHIP.md`（Phase 4 落地节）。机器可读。

> DDL 一律经 `schema-migrator`（MySQL `aiops_migrator`）/ ClickHouse bootstrap Job 执行。权威迁移元数据表 = `aiops_schema_migrations`。runtime 账号仅 DML。

## 约定
- `bootstrap_file` = 迁移 SQL 版本文件（`ai-apm-query-go/internal/store/migrations/versions/<file>`）。
- `runtime_mysql_access` = 服务进程对 MySQL 的允许访问（DML only / FORBIDDEN / LEGACY）。
- orchestrator 对新 V9.2 runtime 表 = `DIRECT MYSQL ACCESS FORBIDDEN`，只经 query-api 内部控制面。

## MySQL 表矩阵

| table | business_owner | physical_writer | ddl_location | bootstrap_file | migration_id | runtime_mysql_access |
|---|---|---|---|---|---|---|
| users | Identity/RBAC | query-api | store/mysql.go | 0001_control_plane_baseline.sql | mysql/0001-control-plane-baseline | query-api DML |
| auth_sessions | Identity/RBAC | query-api | store/authorization.go | 0001 | mysql/0001 | query-api DML |
| user_tenants | Identity/RBAC | query-api | store/authorization.go | 0001 | mysql/0001 | query-api DML |
| roles | Identity/RBAC | query-api | store/authorization.go | 0001 | mysql/0001 | query-api DML |
| permissions | Identity/RBAC | query-api | store/authorization.go | 0001 | mysql/0001 | query-api DML |
| user_roles | Identity/RBAC | query-api | store/authorization.go | 0001 | mysql/0001 | query-api DML |
| role_permissions | Identity/RBAC | query-api | store/authorization.go | 0001 | mysql/0001 | query-api DML |
| scope_assignments | Identity/RBAC | query-api | store/authorization.go | 0001 | mysql/0001 | query-api DML |
| tenants | Identity/RBAC | query-api | store/mysql.go | 0001 | mysql/0001 | query-api DML |
| clusters | Cluster Registry | query-api | store/mysql.go | 0001 | mysql/0001 | query-api DML（orchestrator 直连=越权）|
| tenant_clusters | Cluster Registry | query-api | store/authorization.go | 0001 | mysql/0001 | query-api DML |
| service_catalog | Catalog | query-api | store/mysql.go | 0001 | mysql/0001 | query-api DML |
| devices | Catalog | query-api | store/mysql.go | 0001 | mysql/0001 | query-api DML |
| topology_nodes | Topology | query-api | store/mysql.go | 0001 | mysql/0001 | query-api DML（orchestrator 直连=越权）|
| topology_relations | Topology | query-api | store/mysql.go | 0001 | mysql/0001 | query-api DML（orchestrator 直连=越权）|
| topology_node_types | Topology | query-api | store/mysql.go | 0001 | mysql/0001 | query-api DML |
| topology_relation_types | Topology | query-api | store/mysql.go | 0001 | mysql/0001 | query-api DML |
| platform_settings | Platform Config | query-api | store/mysql.go | 0001 | mysql/0001 | query-api DML（orchestrator 收敛）|
| llm_providers | Platform Config | query-api | store/mysql.go | 0001 | mysql/0001 | query-api DML |
| llm_config_history | Platform Config | query-api | store/mysql.go | 0001 | mysql/0001 | query-api DML |
| alert_rules | Alerting | query-api | store/mysql.go | 0001 | mysql/0001 | query-api DML |
| slo_targets | Alerting | query-api | store/mysql.go | 0001 | mysql/0001 | query-api DML |
| dashboard_panels | Dashboard | query-api | store/mysql.go | 0001 | mysql/0001 | query-api DML |
| alert_silences | Alerting | query-api | store/mysql.go | 0001 | mysql/0001 | query-api DML |
| service_metadata | Catalog | query-api | store/mysql.go | 0001 | mysql/0001 | query-api DML |
| anomaly_events | Alerting | query-api | store/mysql.go | 0001 | mysql/0001 | query-api DML（orchestrator 收敛）|
| ai_runs | AI Runtime | query-api CP Persistence | migrations/0002_ai_runtime.sql | 0002_ai_runtime.sql | mysql/0002-ai-runtime | orchestrator FORBIDDEN |
| ai_run_clusters | AI Runtime | query-api CP Persistence | migrations/0002 | 0002 | mysql/0002 | orchestrator FORBIDDEN |
| ai_plan_steps | AI Runtime | query-api CP Persistence | migrations/0002 | 0002 | mysql/0002 | orchestrator FORBIDDEN |
| ai_tool_runs | AI Runtime | query-api CP Persistence | migrations/0002 | 0002 | mysql/0002 | orchestrator FORBIDDEN |
| ai_evidence | Evidence/RCA | query-api CP Persistence | migrations/0002 | 0002 | mysql/0002 | orchestrator FORBIDDEN |
| ai_hypotheses | AI Runtime | query-api CP Persistence | migrations/0002 | 0002 | mysql/0002 | orchestrator FORBIDDEN |
| ai_actions | Risk/Execution | query-api CP Persistence | migrations/0002 | 0002 | mysql/0002 | orchestrator FORBIDDEN |
| ai_verifications | Risk/Execution | query-api CP Persistence | migrations/0002 | 0002 | mysql/0002 | orchestrator FORBIDDEN |
| ai_approval_decisions | Risk/Execution | query-api CP Persistence | migrations/0002 | 0002 | mysql/0002 | orchestrator FORBIDDEN |
| ai_run_events | AI Runtime | query-api CP Persistence | migrations/0002 | 0002 | mysql/0002 | orchestrator FORBIDDEN |
| ai_audit_events | AI Runtime/Execution | query-api CP Persistence | migrations/0002 | 0002 | mysql/0002 | orchestrator FORBIDDEN |
| platform_audit_events | Platform Security | query-api | migrations/0003_platform_audit.sql | 0003_platform_audit.sql | mysql/0003-platform-audit | query-api DML |

## orchestrator LEGACY 直连表（P0-E/P0-F：空环境 bootstrap 保留，非新 SoT）

| table | 当前 orchestrator 访问 | runtime_mysql_access | planned removal |
|---|---|---|---|
| approval_tasks | read+write | LEGACY_DIRECT_MYSQL_DEPENDENCY | Phase 14（legacy approval path）|
| audit_logs | read+write | LEGACY_DIRECT_MYSQL_DEPENDENCY | 由 ai_audit_events 取代 |
| agents | read+write | LEGACY_DIRECT_MYSQL_DEPENDENCY | Phase 14 |
| reports | read+write | LEGACY_DIRECT_MYSQL_DEPENDENCY | Phase 14 |
| rules | read+write | LEGACY_DIRECT_MYSQL_DEPENDENCY | Phase 14 |
| ipmi_sensors | read+write | LEGACY_DIRECT_MYSQL_DEPENDENCY | Phase 14 |
| ipmi_sel_events | read+write | LEGACY_DIRECT_MYSQL_DEPENDENCY | Phase 14 |
| node_component_health | read+write | LEGACY_DIRECT_MYSQL_DEPENDENCY | Phase 14 |
| change_events | read+write | LEGACY_DIRECT_MYSQL_DEPENDENCY（修正 cluster_id DEFAULT 'default'）| Phase 14 |
| topology_nodes | read+write（越权）| LEGACY（reason=属 query-api 拓扑域）| Phase 6/11 query_k8s 收敛 |
| topology_relations | read+write（越权）| LEGACY | Phase 6/11 |
| clusters | read（越权）| LEGACY | Phase 6/11 |
| platform_settings | read+write（越权）| 收敛为 query-api 经控制面 | Phase 5 |

## 数据源版本化契约

| 数据源 | 迁移元数据表 | 迁移器 | runtime 校验 |
|---|---|---|---|
| MySQL | `aiops_schema_migrations` | cmd/schema-migrator（aiops_migrator）| query-api RequireCurrentVersion（只读含 checksum）|
| ClickHouse | `observability.aiops_schema_migrations` | ClickHouse bootstrap Job | event-collector EnsureSchemaCompatible |
| VictoriaLogs | label contract | telemetrylabels | ingest ValidateScopeLabels |
| VictoriaMetrics | label contract | telemetrylabels | ingest ValidateScopeLabels |
| ChromaDB | collection 契约 | rag_bootstrap create/validate | orchestrator get_collection |
| Object Store（MinIO-compatible）| bucket 契约 | object-store bootstrap | runtime 不 CreateBucket |

## 本 Phase 需收敛的 Ownership 冲突（继承 P4.1）

1. audit_logs 双写 → ai_audit_events + platform_audit_events 取代，audit_logs LEGACY。
2. log_records 双写（CH 完整副本）→ LEGACY，Raw Logs 归 VictoriaLogs。
3. 三套 schema_migrations → 收敛为 `aiops_schema_migrations`（不复用旧表）。
4. topology/clusters orchestrator 越权直写 → LEGACY + 收敛。
5. anomaly_events / platform_settings 归属 → 收敛为 query-api。
