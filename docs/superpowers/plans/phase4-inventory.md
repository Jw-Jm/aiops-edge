# Phase 4 Schema-Creation / MySQL-Usage Inventory（P4.1 交付物）

生成日期：2026-08-20。来源：多模式代码扫描 + 只读 subagent 审计。

---

## 一、SCHEMA_CREATION_CALLSITES（运行时 DDL）

> 判定：任何在服务进程内执行 CREATE/ALTER/DROP/CREATE DATABASE/CREATE INDEX 或 db.migrate()/EnsureSchema()/get_or_create_collection() 的调用点。这些必须是 Phase 4/8 移除运行时 DDL 的目标。

| # | 服务 | 文件:行 | 函数/调用 | 语句类型 | 对象 | 账号 | 归属处理(Task) |
|---|---|---|---|---|---|---|---|
| 1 | query-api | cmd/api/main.go:93 | store.EnsureSchema() | CREATE/ALTER TABLE | 全部控制面表（见下节） | root | T3→migrator + T8→app |
| 2 | query-api | internal/store/mysql.go:96-121 | ensureClusterAuthorityMetadata | ALTER TABLE clusters + ADD UNIQUE INDEX | clusters | root | T3/T8 |
| 3 | query-api | internal/store/mysql.go:145-487 | CREATE TABLE IF NOT EXISTS | users/service_catalog/devices/clusters/topology_*/platform_settings/llm_*/alert_rules/slo_targets/dashboard_panels/alert_silences/tenants/service_metadata/anomaly_events/audit_logs | root | T3/T8 |
| 4 | query-api | internal/store/authorization.go:153-219 | CREATE TABLE IF NOT EXISTS | user_sessions/user_tenants/roles/permissions/user_roles/role_permissions/scope_assignments/tenant_clusters | root | T3/T8 |
| 5 | ai-orchestrator | main.py:79-80 | db.migrate() | CREATE TABLE (migrations/*.sql 4 个) | schema_migrations(64)+approval_tasks/audit_logs/agents/reports/rules/ipmi_*/node_component_health/change_events | root | T8→移除直连建表 |
| 6 | ai-orchestrator | db.py:49-85 | migrate() | CREATE TABLE IF NOT EXISTS schema_migrations + Split(";") 执行 | 同上 | root | T8→移除 |
| 7 | ai-event-collector | clickhouse.go:27-44 | 启动自建 | CREATE TABLE observability.k8s_events | k8s_events | ch default | T5→迁入迁移器 |
| 8 | ai-event-collector | clickhouse.go:124 | 启动自建 | CREATE DATABASE IF NOT EXISTS observability | 库 | ch default | T5→迁入 |
| 9 | ai-orchestrator | rag.py:112-122 | get_or_create_collection | Chroma collection | ops_cases/ops_playbooks | chroma | T7→迁入 bootstrap |
| 10 | ai-orchestrator | session_store.py:22 | CREATE TABLE IF NOT EXISTS | sessions（**SQLite 本地**） | 本地 | local | T8（本地态，非 MySQL DDL） |
| 11 | ai-orchestrator | flow_engine/store.py:25-35 | CREATE TABLE IF NOT EXISTS | flows/flow_runs/flow_run_nodes（**SQLite 本地**） | 本地 | local | T8（本地态） |
| 12 | ai-orchestrator | marketplace.py:165 | CREATE TABLE IF NOT EXISTS | installed_packs（**SQLite 本地**） | 本地 | local | T8（本地态） |

> 注：8-12 项中 10-12 是 SQLite 本地状态（flows/sessions/packs），非 MySQL 运行账号 DDL 权限问题，但属于"运行时 schema creation"，记录在案。第 8 项（ClickHouse CREATE DATABASE）由 T5 迁入。

### query-api 控制面 CREATE TABLE 完整清单（第 3、4 项展开）

users, service_catalog, devices, clusters, topology_nodes, topology_relations, topology_node_types, topology_relation_types, platform_settings, llm_providers, llm_config_history, alert_rules, slo_targets, dashboard_panels, alert_silences, tenants, service_metadata, anomaly_events, audit_logs, user_sessions, user_tenants, roles, permissions, user_roles, role_permissions, scope_assignments, tenant_clusters

### orchestrator migrations/*.sql 建表（第 5、6 项展开）

0001: approval_tasks, audit_logs, agents, reports, rules
0002: ipmi_sensors, ipmi_sel_events, node_component_health
0003: ALTER TABLE reports ADD llm_mode
0004: change_events（注意：`cluster_id VARCHAR(64) NOT NULL DEFAULT 'default'` 违反 Phase 3 冻结语义）

---

## 二、STORAGE_SCHEMA_MAP（数据源 → DDL → 版本化现状）

| 数据源 | DDL 位置 | 版本化现状 | 迁移归属 |
|---|---|---|---|
| MySQL 控制面 | query-api mysql.go EnsureSchema() | 无版本表 | T3 schema-migrator（aiops_schema_migrations）|
| MySQL orchestrator | db.py migrate() | schema_migrations(64)，Split(";") | T3 LEGACY_MIGRATION_METADATA |
| MySQL helm | 0001_init.sql | schema_migrations(255) | T3 收敛 |
| ClickHouse observability | helm init_clickhouse.sql | 无 | T5 版本化（observability.aiops_schema_migrations）|
| ClickHouse k8s_events | event-collector 运行时自建 | 无 | T5 迁入 |
| VictoriaLogs | 无 streamFields 配置 | 无 | T6 label contract |
| VictoriaMetrics | remote write（无 DDL） | 无 | T6 label contract |
| ChromaDB | rag.py 懒加载 get_or_create | 无 | T7 bootstrap create + runtime get |
| MinIO/S3-compatible | 当前代码无 MinIO deployment | 无 | T7 建立 object-store bootstrap |

---

## 三、ORCHESTRATOR_MYSQL_USAGE（P0-E：DDL / DML reads / DML writes 三类）

> 连接机制 3 种：`db.get_conn()`（连接池）、`db_audit.py` 独立 pymysql、`kg_graph.py`/`multicluster_demo.py` 独立 pymysql。
> **无任何 `ai_*` 新 V9.2 runtime 表访问。**

### DDL（运行时建表）

| 文件:行 | 对象 |
|---|---|
| db.py:49-85 migrate() | schema_migrations(64) + migrations/*.sql 全部表 |
| main.py:79-80 | 调用 db.migrate() |

### DML writes / reads（按表汇总，读/写/降级）

| 表名 | 归属域判断 | 读位置 | 写位置 | 内存降级 |
|---|---|---|---|---|
| approval_tasks | orchestrator（审批） | db_approval.py, artifacts.py | db_approval.py | ✅ |
| audit_logs | orchestrator（审计） | db_audit.py | db_audit.py（orchestrator.py:711 委托）| ✅ |
| agents | orchestrator（Agent 配置） | db_agents.py | db_agents.py | ✅ |
| reports | orchestrator（报告） | db_agents.py, artifacts.py | db_agents.py | ✅ |
| rules | orchestrator（规则） | db_agents.py | db_agents.py | ✅ |
| ipmi_sensors | orchestrator（硬件） | ipmi_ingest.py, node_health.py | ipmi_ingest.py | ✅ |
| ipmi_sel_events | orchestrator（硬件） | ipmi_ingest.py | ipmi_ingest.py | ✅ |
| node_component_health | orchestrator（部件健康） | node_health.py | node_health.py | ⚠️ 读降空/写跳过 |
| anomaly_events | **归属存疑**（query-api 告警域？） | main.py:2168 | detector.py:125 | ⚠️ best-effort |
| change_events | orchestrator（变更，注 `cluster_id DEFAULT 'default'`） | kg_api/kg_tools/kg_graph/main | main.py, kg_graph | ❌ 无降级 |
| platform_settings | **配置域（query-api 归属？）** | recovery_policy.py | recovery_policy.py | ✅ get 降默认 |
| topology_nodes | **query-api 域（越权高风险）** | kg_api.py, kg_graph.py, multicluster_demo.py | kg_graph.py, multicluster_demo.py | ❌ 无降级 |
| topology_relations | **query-api 域（越权高风险）** | kg_api.py, kg_graph.py, multicluster_demo.py | kg_graph.py, multicluster_demo.py | ❌ 无降级 |
| clusters | **query-api 域（越权高风险）** | kg_graph.py | multicluster_demo.py | ❌ 无降级 |

### 处置结论（P0-E/P0-F 输入）

- **新 V9.2 runtime 表**（ai_runs 等）：orchestrator `DIRECT MYSQL ACCESS = FORBIDDEN`（当前已无访问，保持）。
- **越权高风险（query-api 域）**：`topology_nodes`/`topology_relations`/`clusters` 由 orchestrator 直读写 → 需收敛（标 `LEGACY_DIRECT_MYSQL_DEPENDENCY`，原因=知识图谱/集群配置本属 query-api，planned removal 由 Phase 6/11 query_k8s 收敛）。
- **归属存疑**：`anomaly_events`（query-api 告警域?）、`platform_settings`（配置域?）→ Phase 4 内确认归属，若属 query-api 则 orchestrator 标 legacy。
- **orchestrator 自有 legacy 业务表**：`approval_tasks`/`audit_logs`/`agents`/`reports`/`rules`/`ipmi_*`/`node_component_health`/`change_events` → `LEGACY_DIRECT_MYSQL_DEPENDENCY`，空环境 bootstrap 必须创建（P0-F），不访问新 runtime 表、不成为新表 writer。
- **`change_events.cluster_id DEFAULT 'default'`**：违反 Phase 3 冻结语义，P0-F 中标记需修正为可空 canonical UUID。

---

## 四、OWNERSHIP_CONFLICTS（供 T2 收敛）

1. `audit_logs` 双写：query-api mysql.go:487（CREATE）+ orchestrator db_audit.py（INSERT/DELETE? 读）→ 违单写者，收敛到 T4（LEGACY + platform_audit_events）。
2. `log_records` 双写：ClickHouse 完整副本(ingest) + VictoriaLogs(shipper) → 违 V9.2，T5 标 LEGACY。
3. 三套 schema_migrations（orchestrator 64 / helm 255 / 无）→ T3 收敛为 `aiops_schema_migrations`（不复用旧表）。
4. `topology_nodes/relations`、`clusters` orchestrator 越权直写 query-api 域 → P0-E 收敛。
5. `anomaly_events`、`platform_settings` 归属不明 → 本 Phase 确认。

---

## 五、完整性核验

多模式扫描（CREATE TABLE/DATABASE/INDEX、ALTER TABLE、migrate()、EnsureSchema、get_or_create_collection）覆盖：
- query-api Go：✅（mysql.go, authorization.go, main.go:93）
- orchestrator Python：✅（db.py, main.py, rag.py, session_store.py, flow_engine/store.py, marketplace.py）
- event-collector Go：✅（clickhouse.go:27,124）
- SQLite 本地态：✅（session_store, flow_engine, marketplace 已归档）

差集=0（人工核对无遗漏 callsite）。

---

## P4.3 执行记录（2026-08-20）

**STATUS: PASS_WITH_PLANNED_CUTOVER_DEPENDENCY**

IMPLEMENTED:
- `internal/store/migrations/migrator.go`：`aiops_schema_migrations`（migration_id VARCHAR(255) PK + checksum CHAR(64) + applied_at DATETIME(3)）；GET_LOCK 迁移锁；checksum 校验；`-- statement-breakpoint` 解析（非 Split(";")）；幂等执行（成功后才 INSERT 记录）；`RunMigrations`（可注入）+ `Run`（embed versions）+ `RequireCurrentVersion`（只读）。
- 错误码：`ErrSchemaOutdated`（missing）、`ErrSchemaChecksumMismatch`。
- `versions/0001_control_plane_baseline.sql`：TARGET_TABLES（users/auth_sessions）+ LEGACY_RUNTIME_REQUIRED_TABLES（approval_tasks/reports/change_events，P0-F）。
- `cmd/schema-migrator/main.go`：独立迁移执行器（MYSQL_USER=aiops_migrator）。
- 5 个单测全绿（首次应用 + 幂等 + checksum mismatch fail-closed + RequireCurrentVersion OK/missing/mismatch）。

**CUTOVER（DEVIATION，计划依赖调整，非缺陷）:**
```text
query-api cmd/api/main.go:93
store.EnsureSchema() → RequireCurrentVersion()
MOVED_TO: P4.4 final substep
REASON: legacy EnsureSchema 仍拥有尚未被 authoritative versioned migrations
覆盖的 schema 对象（20+ 张表 + schema evolution + backfill DML）。P4.4 完成
全部现有 MySQL DDL 版本化接管前，立即切换会破坏 fresh/current deployment 自建表能力。
SECURITY_IMPACT: None（现有行为临时保留）。
CUTOVER_DEADLINE: P4.4 before entering P4.5。
```

**P4.4 增强职责（用户拍板）：** 不只建 AI Runtime 新表，还须：
1. Inventory 当前 `EnsureSchema()` 行为（DDL / backfill DML / bootstrap DML 三类）。
2. 全部 DDL → versioned migrations（runtime DDL callsite = 0）。
3. Schema backfill DML（如 `UPDATE users SET user_uuid`）→ versioned migration。
4. 合法 startup DML → 独立 DML-only bootstrap 函数（NO DDL，aiops_app 可用）。
5. 对现有环境跑 schema-migrator 验证。
6. `TestMigratedSchemaCoversLegacyEnsureSchema`：fresh DB schema-migrator → snapshot A vs legacy EnsureSchema → snapshot B，验证 required tables/columns/indexes A covers B（含 LEGACY runtime-required 表）。
7. 切换 query-api：`EnsureSchema()` → `RequireCurrentVersion()` + optional DML-only bootstrap。
8. 证明 query-api startup 零 DDL。

---

## P4.4 EnsureSchema 接管 Inventory（Step 7 交付）

### 类别 1：DDL（→ versioned migration）

**CREATE TABLE（~25 张）：** users, service_catalog, devices, clusters, topology_nodes, topology_relations, topology_node_types, topology_relation_types, platform_settings, llm_providers, llm_config_history, alert_rules, slo_targets, dashboard_panels, alert_silences, tenants, service_metadata, anomaly_events, audit_logs + authorization（users 已在上; RBAC 见 authorizationSchemaStatements）

**ALTER TABLE（schema evolution）：**
- clusters：ADD cluster_id/tenant_id/slug/environment/credential_ref/lifecycle_status/type/capabilities/labels/deleted_at/kubernetes_identity_uid + UNIQUE uq_clusters_cluster_id/uq_clusters_slug
- users：ADD scope/is_approver/user_uuid
- alert_rules：ADD webhook_url/cooldown/dampening/baseline_seconds/anomaly_method/slo_id/keyword/cluster

### 类别 2：Schema backfill DML（→ versioned migration）

- `UPDATE users SET user_uuid=LOWER(UUID()) WHERE user_uuid IS NULL OR user_uuid=''`（一次性历史修正）
- `UPDATE clusters SET cluster_id=LOWER(UUID()) WHERE cluster_id IS NULL OR cluster_id=''`（clusterAuthorityBackfillStatements）
- `UPDATE clusters SET slug=CONCAT('legacy-', id) WHERE slug IS NULL OR slug=''`
- `UPDATE clusters SET lifecycle_status=CASE status ... END WHERE lifecycle_status='registered'`
- `INSERT IGNORE INTO service_metadata SELECT ... FROM service_catalog WHERE ...`（旧数据迁移）

### 类别 3：Bootstrap seed DML（→ store.EnsureBootstrapData()，DML-only）

- dashboard_panels 首次初始化 4 个默认面板（`SELECT count(*)` + `INSERT IGNORE`）
- 种子 admin / roles / permissions（若 EnsureSchema/authorizationSchemaStatements 存在）— 需在 authorization.go 确认

> `authorizationSchemaStatements()`（authorization.go:153-219）另有 ~7 张 RBAC 表（user_sessions→auth_sessions, user_tenants, roles, permissions, user_roles, role_permissions, scope_assignments, tenant_clusters）的 CREATE TABLE，需并入 DDL 迁移清单。

### Cutover 待办（P4.4 final substep）
1. 全部 DDL → 0001/0002/0003 版本 SQL
2. backfill DML → 版本 SQL
3. bootstrap seed → `store.EnsureBootstrapData()`（NO DDL）
4. `TestMigratedSchemaCoversLegacyEnsureSchema` coverage
5. `cmd/api/main.go:93` EnsureSchema() → RequireCurrentVersion() + EnsureBootstrapData()

