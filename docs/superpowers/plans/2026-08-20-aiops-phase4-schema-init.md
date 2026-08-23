# AIOps Phase 4：统一 Schema Ownership 与初始化体系 Implementation Plan（修订版 R3）

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 建立统一 versioned schema bootstrap 与权威 Schema Ownership，使空环境幂等初始化、二次初始化幂等、runtime 服务账号（MySQL + ClickHouse）无 DDL 权限即可启动，且严格复现 V9.2 已冻结的 AI Runtime / 控制面数据模型。

**Architecture:** 以独立 `schema-migrator`（MySQL `aiops_migrator`）与 ClickHouse bootstrap Job 作为唯一 DDL authority，各自使用**专用迁移元数据表 `aiops_schema_migrations`**（不复用/不改造旧 `schema_migrations`）。query-api runtime 用 `aiops_app`（DML only）只做只读 `RequireCurrentVersion`（含 checksum）readiness check；ClickHouse runtime 账号无 DDL。MinIO 作为冻结的 Object Store SoT 必须在 Phase 4 建立真实 bootstrap（无 Erratum 不得静默移除）。orchestrator 对新 V9.2 表**禁止直连 MySQL**，只经 query-api 内部控制面；旧 legacy 直连依赖需显式列出并在空环境保持可启动。

**Tech Stack:** Go (query-api store + schema-migrator + event-collector), Python (orchestrator), Helm (deploy), MySQL 8.4, ClickHouse, VictoriaLogs, VictoriaMetrics, ChromaDB, MinIO-compatible object store。

## Global Constraints

- **不物理删除旧历史数据**（V9.2 §七十）；旧表/旧迁移元数据标 `LEGACY`，writer/reader cutover 在 Phase 5/6。
- **runtime 账号无 DDL**：MySQL `aiops_app` 仅 DML；ClickHouse runtime 账号（app/ingest/collector）无 CREATE/ALTER/DROP。DDL 仅由 `schema-migrator`（`aiops_migrator`）/ ClickHouse bootstrap Job 执行。
- **权威迁移元数据表 = `aiops_schema_migrations`**（MySQL + ClickHouse 同名）：
  `migration_id VARCHAR(255) PRIMARY KEY, checksum CHAR(64) NOT NULL, applied_at DATETIME(3)`。
  **不复用/不改造旧 `schema_migrations`**（存在 64/255 版本列冲突 + namespace 冲突），旧表标 `LEGACY_MIGRATION_METADATA / PRESERVE / NO PHYSICAL DELETE / NO ALTER`。
- **运行时 readiness = 版本 + checksum 双校验**：`RequireCurrentVersion` 只 `SELECT`，校验全部 required migration 存在且 DB checksum == embedded checksum；missing → `ErrSchemaOutdated`，checksum mismatch → `ErrSchemaChecksumMismatch`，均 fail-closed。禁止 CREATE/ALTER/INSERT。
- **AI Runtime 表严格复现 V9.2 冻结模型**；表/列/nullability/index/unique/check 以 `AIOPS_DATA_MODEL_REDESIGN.md`/`AIOPS_CONTRACTS.md` 的 frozen definition 为唯一来源，实现 Agent 禁止自行增删字段。
- **`ai_runs` 不用空字符串表达 default cluster**：用 `scope_kind(single_cluster|multi_cluster)` + `primary_cluster_id`(nullable) + `ai_run_clusters`；`state_version` 为 CAS 字段。
- **Raw Logs 只进 VictoriaLogs**；ClickHouse `log_records` 完整副本标 LEGACY。
- **Phase 4 = schema/ownership/init**，不做 long dual-write、不切 reader/writer（Phase 5/6）。
- **MinIO 是冻结 Object Store SoT（large Evidence + Knowledge objects）**：Phase 4 必须建立真实 bootstrap（create/validate bucket），无正式 Erratum 不得静默移除。
- **orchestrator 对新 V9.2 runtime 表 `DIRECT MYSQL ACCESS = FORBIDDEN`**：只经 query-api internal control-plane persistence（本 Phase 只建 schema，不建 Run 业务 API）。旧 legacy 直连依赖需显式列出（legacy tables + reason + planned removal phase），不得访问任何新 V9.2 runtime 表。
- **空环境 legacy compatibility**：schema-migrator 的 baseline 必须同时创建 `TARGET_TABLES` 与 `LEGACY_RUNTIME_REQUIRED_TABLES`（标 LEGACY、不删、非最终 SoT），保证旧 orchestrator runtime 在空环境启动不因缺表失败。
- **Git 冻结**：全程 `NO git add / NO git commit / NO git push`，直到 Phase 21 + 单独授权。

---

### Task 1：Runtime DDL / Schema Creation Inventory（P4.1）

**Files:**
- Create: `docs/superpowers/plans/phase4-inventory.md`
- 只读参考：`ai-apm-query-go/cmd/api/main.go:93`, `ai-orchestrator/main.py:78-80`, `ai-orchestrator/db.py`, `ai-event-collector/clickhouse.go:27`, `deploy/helm/aiops/files/clickhouse/init_clickhouse.sql`, `deploy/helm/aiops/templates/mysql/init-job.yaml`, `ai-orchestrator/rag.py`, `ai-orchestrator/flow_engine/store.py`, `ai-orchestrator/session_store.py`

**Interfaces:**
- Consumes: 无（纯盘点）
- Produces: `SCHEMA_CREATION_CALLSITES` + `STORAGE_SCHEMA_MAP` + **`ORCHESTRATOR_MYSQL_USAGE`（DDL / DML reads / DML writes 三类，P0-E 输入）**

- [ ] **Step 1: 多模式扫描全部 schema-creation callsite**

不要用单一 `grep CREATE TABLE ... | wc -l` 作完整性证明（会漏 ALTER / CREATE DATABASE / CREATE INDEX / db.migrate() / EnsureSchema / get_or_create_collection）。多模式扫描 + 逐项人工归档：

```bash
grep -rnE "CREATE (TABLE|DATABASE|INDEX)|ALTER TABLE" ai-apm-query-go ai-orchestrator ai-event-collector --include="*.go" --include="*.py"
grep -rnE "EnsureSchema|db\.migrate|migrate\(\)|get_or_create_collection|create_collection|get_collection" ai-apm-query-go ai-orchestrator ai-event-collector --include="*.go" --include="*.py"
```

- [ ] **Step 2: 建立 orchestrator MySQL 用途三类清单（P0-E）**

逐条归档 orchestrator 每个 MySQL 访问点属于：`DDL` / `DML reads` / `DML writes`，涉及哪些表。格式：

```markdown
## ORCHESTRATOR_MYSQL_USAGE
| 文件:行 | 类别 | 表 | 是否新V9.2 runtime表 | 处置 |
|---|---|---|---|---|
| main.py:78-80 | DDL | db.migrate() 全部 | 是(部分) | 移除直连建表，迁移到 schema-migrator |
| db.py:xxx | DML writes | approval_tasks/audit_logs | 旧 | LEGACY_DIRECT_MYSQL_DEPENDENCY 或经控制面 |
| ... | ... | ... | ... | ... |
```

锁定：任何访问**新 V9.2 表**（`ai_runs` 等）的 orchestrator 直连 → 标 `FORBIDDEN`；旧业务表直连 → 标 `LEGACY_DIRECT_MYSQL_DEPENDENCY`（legacy tables + reason + planned removal phase）。

- [ ] **Step 3: 完整性交叉核验**

对每类对象（table/database/index/migrate/get_or_create），确认至少一个 callsite 被归档；`SCHEMA_CREATION_CALLSITES` 与代码搜索结果差集 = 0（逐个人工确认，非数字相等）。

---

### Task 2：Authoritative Schema Ownership Matrix（P4.2）

**Files:**
- Modify: `docs/SCHEMA_OWNERSHIP.md`（追加 Phase 4 落地列 + `auth_sessions` + 移除 resource_scopes/cluster_nodes 作新授权表）
- Create: `docs/superpowers/plans/phase4-ownership.md`

**Interfaces:**
- Consumes: Task 1 `STORAGE_SCHEMA_MAP` + `ORCHESTRATOR_MYSQL_USAGE`
- Produces: `AUTHORITATIVE_OWNERSHIP_MATRIX`（唯一权威）

- [ ] **Step 1: 修正会话表名**

所有 `user_sessions` → **`auth_sessions`**（Phase 3 冻结）。

- [ ] **Step 2: 明确不新增第二套授权表**

`resource_scopes` / `cluster_nodes` 不作为 Phase 4 新增授权/控制面表；授权唯一物理表是 `scope_assignments`。`cluster_nodes` 若需 Node inventory，先判定归属（ClickHouse derived vs MySQL authoritative），本 Phase 不新增。

- [ ] **Step 3: 落地矩阵 + Audit 边界 + orchestrator 所有权**

- `audit_logs` → `LEGACY / PRESERVE / NO PHYSICAL DELETE`，非最终 Audit SoT；最终分离 `platform_audit_events` + `ai_audit_events`。
- 明确每张 V9.2 冻结 AI Runtime 表 owner=query-api Control Plane Persistence，physical writer=query-api，orchestrator 对新表 `FORBIDDEN`。
- 生成 `phase4-ownership.md`（每行：`table, business_owner, physical_writer, ddl_location, bootstrap_file, migration_id, runtime_mysql_access`）。

---

### Task 3：Unified MySQL Versioned Migrator + schema-migrator（P4.3）

**Files:**
- Create: `ai-apm-query-go/internal/store/migrations/migrator.go`
- Create: `ai-apm-query-go/internal/store/migrations/migrator_test.go`
- Create: `ai-apm-query-go/cmd/schema-migrator/main.go`
- Modify: `ai-apm-query-go/cmd/api/main.go:91-93`（→ 只读 `RequireCurrentVersion`）
- Create: `ai-apm-query-go/internal/store/migrations/versions/0001_control_plane_baseline.sql`
- Modify: `deploy/helm/aiops/templates/mysql/init-job.yaml`（改用 schema-migrator）

**Interfaces:**
- Consumes: Task 2 matrix；Task 1 `ORCHESTRATOR_MYSQL_USAGE`（确定 baseline 需含哪些 LEGACY_RUNTIME_REQUIRED_TABLES，P0-F）
- Produces: `migrations.Migrator.Run(*sql.DB)`（仅 schema-migrator 调用）、`migrations.RequireCurrentVersion(*sql.DB) error`（只读）、`cmd/schema-migrator`

**设计约束（P0-A/P0-B + 修正 4/5）：**
- **权威元数据表 = `aiops_schema_migrations`**，结构：
  ```sql
  CREATE TABLE IF NOT EXISTS aiops_schema_migrations (
      migration_id VARCHAR(255) PRIMARY KEY,
      checksum CHAR(64) NOT NULL,
      applied_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3)
  );
  ```
  **不复用/不 ALTER 旧 `schema_migrations`**（orchestrator 64 / helm 255）。旧表标 `LEGACY_MIGRATION_METADATA / PRESERVE / NO PHYSICAL DELETE / NO ALTER`。所有后续引用 `schema_migrations` 一律改 `aiops_schema_migrations`。
- **migration_id 用 namespaced name**：`mysql/0001-control-plane-baseline`、`mysql/0002-ai-runtime`、`mysql/0003-platform-audit`（避免 orchestrator `0001` 碰撞）。
- **checksum**：每个 migration 文件 SHA256(hex, 64 char) 存 `checksum`。
- **无 DDL 事务原子性**：MySQL DDL implicit commit。流程 = `GET_LOCK('aiops_migrate', timeout)` → 检查已应用 → 校验 checksum → 逐条幂等执行 → **全部成功后才** insert → 释放锁。中途失败不标 applied，靠 DDL 幂等恢复。
- **不 `Split(";")`**：用明确 delimiter `-- statement-breakpoint` 拆分。
- **`Run` 仅由 schema-migrator（`aiops_migrator`）执行**；`cmd/api/main.go` **只调用 `RequireCurrentVersion`**，不含任何 CREATE/ALTER/INSERT。

- [ ] **Step 1: 写失败测试（幂等 + checksum + 新表名）**

```go
// migrator_test.go
func TestMigratorUsesAiopsSchemaMigrations(t *testing.T) {
    db := testMySQL(t)
    if err := Run(db); err != nil { t.Fatalf("first run: %v", err) }
    var n int
    db.QueryRow("SELECT COUNT(*) FROM aiops_schema_migrations").Scan(&n)
    if n != 1 { t.Fatalf("applied = %d, want 1", n) }
    var cksum string
    db.QueryRow("SELECT checksum FROM aiops_schema_migrations").Scan(&cksum)
    if len(cksum) != 64 { t.Fatalf("checksum len = %d, want 64", len(cksum)) }
    if err := Run(db); err != nil { t.Fatalf("second run: %v", err) } // 幂等
    db.QueryRow("SELECT COUNT(*) FROM aiops_schema_migrations").Scan(&n)
    if n != 1 { t.Fatalf("after 2nd run applied = %d, want 1", n) }
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/store/migrations/ -run TestMigratorUsesAiopsSchemaMigrations -v`
Expected: FAIL（`Run` 未定义）。

- [ ] **Step 3: 实现 migrator + RequireCurrentVersion（含 checksum）**

`migrator.go`：
```go
// Run：仅由 cmd/schema-migrator 调用（aiops_migrator）。拿 GET_LOCK，校验 checksum，
// 幂等执行未应用 migration（-- statement-breakpoint 拆分），成功后 insert aiops_schema_migrations。
func Run(db *sql.DB) error { /* ... */ }

// RequireCurrentVersion：只读 readiness。SELECT migration_id, checksum FROM aiops_schema_migrations；
// 从 embed versions 重算 expected checksum；required 全在 + checksum 全一致 → nil；
// missing → ErrSchemaOutdated；checksum mismatch → ErrSchemaChecksumMismatch。
// 禁止 CREATE/ALTER/INSERT。
func RequireCurrentVersion(db *sql.DB) error { /* ... */ }
```

`cmd/schema-migrator/main.go`：
```go
func main() {
    db := openDB(os.Getenv("MYSQL_USER")) // 期望 aiops_migrator
    if err := migrations.Run(db); err != nil { log.Fatal(err) }
}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/store/migrations/ -run TestMigratorUsesAiopsSchemaMigrations -v`
Expected: PASS。

- [ ] **Step 5: runtime cutover DEFERRED_TO_P4.4（计划依赖调整，非缺陷）**

按用户拍板（A），query-api 的 `EnsureSchema()` → `RequireCurrentVersion()` 切换**不在 P4.3 执行**。原因：`EnsureSchema()` 仍拥有尚未被 versioned migrations 覆盖的 schema 对象（20+ 张表 + schema evolution + backfill DML），立即切换会破坏 fresh/current deployment 自建表能力。

```text
DEVIATION:
Runtime cutover intentionally moved from P4.3 to P4.4 because legacy
EnsureSchema still owns schema objects not yet represented by authoritative
versioned migrations.
SECURITY_IMPACT: None. Existing behavior remains temporarily until migration
coverage is complete.
CUTOVER_DEADLINE: P4.4 before entering P4.5.
```

`RequireCurrentVersion` 已在 P4.3 实现并通过测试；实际切换在 P4.4 final substep 完成（此时 DDL 全部版本化）。

- [ ] **Step 6: baseline 同时含 TARGET_TABLES + LEGACY_RUNTIME_REQUIRED_TABLES（P0-F）**

`0001_control_plane_baseline.sql` 分两节：
```sql
-- mysql/0001-control-plane-baseline
-- === TARGET_TABLES ===
CREATE TABLE IF NOT EXISTS auth_sessions (...);   -- 控制面目标表
CREATE TABLE IF NOT EXISTS users (...);
...
-- === LEGACY_RUNTIME_REQUIRED_TABLES ===
-- 旧 orchestrator runtime 空环境启动仍依赖（P0-F）：标 LEGACY，不删，非最终 SoT
CREATE TABLE IF NOT EXISTS approval_tasks (...);
CREATE TABLE IF NOT EXISTS rules (...);
CREATE TABLE IF NOT EXISTS reports (...);
CREATE TABLE IF NOT EXISTS sessions (...);
```
此清单以 Task 1 `ORCHESTRATOR_MYSQL_USAGE` 为准；空环境必须验证旧 runtime 能启动。

- [ ] **Step 7: 更新 mysql init Job 调用 schema-migrator**

`init-job.yaml` 运行 `schema-migrator`，`MYSQL_USER=aiops_migrator`。

---

### Task 4：Frozen MySQL Control Plane / AI Runtime Schema（P4.4）

> **P4.4 核心职责（用户拍板，不只建 AI Runtime 新表）：** 完整接管当前 `EnsureSchema()` 的 MySQL DDL authority，并执行 runtime cutover。三个类别拆分：
> 1. **DDL**（CREATE/ALTER/CREATE INDEX + authority metadata + schema evolution）→ 全部 versioned migration（最终 runtime DDL callsite = 0）。
> 2. **Schema backfill DML**（如 `UPDATE users SET user_uuid`）→ versioned migration（`mysql/000x-...`），不每次启动跑。
> 3. **合法 bootstrap seed**（初始 admin/role/permission/system settings）→ 独立 **DML-only** 函数（`store.EnsureBootstrapData()`：NO CREATE/ALTER/DROP/INDEX，`aiops_app` 可用）。
> 最后执行 cutover（用户 A 拍板）：`EnsureSchema()` → `RequireCurrentVersion()` + optional DML-only bootstrap，并证明 startup 零 DDL。

**Files:**
- Create: `ai-apm-query-go/internal/store/migrations/versions/0002_ai_runtime.sql`
- Create: `ai-apm-query-go/internal/store/migrations/versions/0003_platform_audit.sql`
- Create: `ai-apm-query-go/internal/store/migrations/schema_manifest_test.go`
- Create: `ai-apm-query-go/internal/store/migrations/coverage_test.go`（`TestMigratedSchemaCoversLegacyEnsureSchema`）
- Modify: `ai-apm-query-go/cmd/api/main.go:93`（final substep 切换 EnsureSchema → RequireCurrentVersion + EnsureBootstrapData）
- Create: `ai-apm-query-go/internal/store/bootstrap.go`（DML-only seed）
- Modify: `ai-orchestrator/migrations/0001_business_tables.sql`（legacy 表标 LEGACY）

**Interfaces:**
- Consumes: Task 3 `Migrator`；`AIOPS_DATA_MODEL_REDESIGN.md`/`AIOPS_CONTRACTS.md`（frozen definition 唯一来源，P1-1）
- Produces: V9.2 冻结 AI Runtime + audit 表 DDL；**EnsureSchema DDL authority 完整接管**；`store.EnsureBootstrapData()`（DML-only）；query-api runtime cutover（EnsureSchema → RequireCurrentVersion）

**Cutover 顺序（用户锁死）：**
```text
1. Inventory current EnsureSchema behavior（DDL / backfill DML / bootstrap DML 三类）
2. All DDL → versioned migrations
3. Schema backfill DML → versioned migrations
4. Legitimate startup DML → separate DML-only bootstrap function
5. Run schema-migrator against existing environment
6. Verify migrations complete
7. Replace query-api: EnsureSchema() → RequireCurrentVersion() + optional DML-only bootstrap
8. prove query-api startup performs zero DDL
```

**表清单（V9.2 frozen，P0-1）：**
```text
ai_runs
ai_run_clusters
ai_plan_steps
ai_tool_runs
ai_evidence
ai_hypotheses
ai_actions
ai_verifications
ai_approval_decisions
ai_run_events
ai_audit_events
platform_audit_events
```
**NOT 目标**：`approval_tasks`/`ops_actions`/`verification_runs`；**不实现 Run 业务 API**（无 `/internal/v1/runs`）。

**字段唯一来源（P1-1）**：每张表列/nullability/PK/unique/index/check 必须取自 `AIOPS_DATA_MODEL_REDESIGN.md` / `AIOPS_CONTRACTS.md` 的 frozen definition。实现 Agent **禁止自行增删字段**；`schema_manifest_test` 逐表核对。

- [ ] **Step 1: 写 ai_runs + ai_run_clusters DDL（0002）**

```sql
-- mysql/0002-ai-runtime：V9.2 冻结 AI Runtime 控制面表
CREATE TABLE IF NOT EXISTS ai_runs (
  run_id CHAR(36) PRIMARY KEY,
  tenant_id CHAR(36) NOT NULL,
  scope_kind VARCHAR(16) NOT NULL
    CHECK (scope_kind IN ('single_cluster','multi_cluster')),   -- DB CHECK 表达冻结语义
  primary_cluster_id CHAR(36) NULL,   -- single: = cluster; multi: NULL
  intent VARCHAR(255) NOT NULL DEFAULT '',
  action_mode VARCHAR(32) NOT NULL DEFAULT '',
  target_type VARCHAR(32) NULL,
  target_resource_id VARCHAR(512) NULL,
  status VARCHAR(32) NOT NULL,
  state_version BIGINT NOT NULL DEFAULT 0,   -- CAS
  parent_run_id CHAR(36) NULL,
  created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  -- 若 DB 支持 CHECK 关联：single_cluster → primary_cluster_id NOT NULL；multi → NULL。
  -- 否则以 schema_manifest_test + 业务层强制（P1-1）
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS ai_run_clusters (
  run_id CHAR(36) NOT NULL,
  cluster_id CHAR(36) NOT NULL,      -- canonical UUID，NOT NULL
  PRIMARY KEY (run_id, cluster_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
```
其余表（`ai_plan_steps.cluster_id NULL`；`ai_tool_runs/ai_evidence/ai_hypotheses/ai_actions/ai_verifications/ai_approval_decisions.cluster_id NOT NULL`）**逐表写全列**，不省略。以 frozen definition 为准。

- [ ] **Step 2: 写 platform_audit_events DDL（0003）**

`mysql/0003-platform-audit`：`platform_audit_events(audit_id, request_id, run_id NULL, tenant_id, cluster_id NULL, user_id, service_identity, action, result, created_at)`。

- [ ] **Step 3: 写 schema_manifest_test（逐表核对，替代临时 API）**

```go
// schema_manifest_test.go：逐表断言 table/column/type/nullable/PK/unique/index
// 与 AIOPS_DATA_MODEL_REDESIGN.md frozen definition 一致。
func TestAIRuntimeSchemaManifest(t *testing.T) {
    // ai_runs: scope_kind/primary_cluster_id/state_version 存在；cluster_id 列不存在
    // ai_run_clusters: PK(run_id, cluster_id)
    // ai_tool_runs/ai_evidence/ai_hypotheses/ai_actions/ai_verifications: cluster_id NOT NULL
    // ai_plan_steps: cluster_id NULL
    // 二次 migrate 幂等（aiops_schema_migrations 版本数不变）
}
```
用 `information_schema.columns` 断言；**不用跨语言 API fixture 验证 DB schema**（修正 3）。

- [ ] **Step 4: 运行测试确认失败**

Run: `go test ./internal/store/migrations/ -run TestAIRuntimeSchemaManifest -v`
Expected: FAIL。

- [ ] **Step 5: 落 DDL 文件并验证**

创建 0002/0003；再次运行，Expected: PASS。

- [ ] **Step 6: 标注 orchestrator 旧表 LEGACY**

`0001_business_tables.sql` 中 legacy 表保留（不删），文件头加 `-- LEGACY` 注释，标注由 V9.2 新表取代。

- [ ] **Step 7: Inventory 当前 EnsureSchema() 行为（接管第一步）**

对 `store.EnsureSchema()` 逐条归档到 `phase4-inventory.md`：每条属于 `DDL` / `Schema backfill DML` / `bootstrap seed DML` 三类。例如：
- `CREATE/ALTER TABLE ...` → DDL → versioned migration
- `UPDATE users SET user_uuid=...` → backfill DML → versioned migration
- `INSERT admin / roles / permissions` → bootstrap seed → `store.EnsureBootstrapData()`

- [ ] **Step 8: 全部 DDL → versioned migrations（扩展 0001/0002/0003）**

把 `EnsureSchema()` 的全部 CREATE/ALTER/CREATE INDEX 迁入版本 SQL（含 authority metadata 列、tenant/cluster schema evolution）。目标：**runtime DDL callsite = 0**。

- [ ] **Step 9: 写 coverage test（`TestMigratedSchemaCoversLegacyEnsureSchema`）**

```go
// coverage_test.go：证明 authoritative migrations 覆盖 legacy EnsureSchema 的
// Phase 4 runtime-required 对象（含 LEGACY 但当前 runtime 仍依赖的表）。
func TestMigratedSchemaCoversLegacyEnsureSchema(t *testing.T) {
    // fresh DB → schema-migrator → information_schema snapshot A
    // legacy EnsureSchema on controlled reference DB → information_schema snapshot B
    // 对所有 Phase 4 runtime-required 对象：required tables/columns/indexes A covers B
}
```

- [ ] **Step 10: 实现 store.EnsureBootstrapData()（DML-only）+ runtime cutover**

`bootstrap.go`：`EnsureBootstrapData(db) error`，只含 DML（INSERT/UPDATE，幂等），**NO CREATE/ALTER/DROP/INDEX**，可用 `aiops_app`。最终 substep 切换 `cmd/api/main.go:93`：
```go
if err := migrations.RequireCurrentVersion(store.GetDB()); err != nil {
    log.Fatalf("schema not ready (read-only checksum check): %v", err)
}
if err := store.EnsureBootstrapData(store.GetDB()); err != nil {
    log.Fatalf("bootstrap data: %v", err)
}
```
并证明 query-api startup 零 DDL（DDL 已全部移出版本 SQL；`EnsureBootstrapData` 仅 DML）。

---

### Task 5：Versioned ClickHouse Bootstrap（P4.5）

**Files:**
- Create: `deploy/helm/aiops/templates/clickhouse/bootstrap-job.yaml`（ClickHouse bootstrap runner）
- Create: `deploy/helm/aiops/files/clickhouse/migrations/0001_observability_baseline.sql`
- Modify: `deploy/helm/aiops/files/clickhouse/init_clickhouse.sql`（→ LEGACY / 单一 authority 收敛）
- Modify: `ai-event-collector/clickhouse.go:27-44`（移除运行时 CREATE，改为只读 schema 兼容校验）

**Interfaces:**
- Consumes: Task 2 ClickHouse 归属
- Produces: **真实 ClickHouse versioned migrator**（`observability.aiops_schema_migrations`）+ `log_records` LEGACY + `k8s_events` 迁入

**设计约束（P0-C）：**
- **ClickHouse 需要真实 bootstrap runner**，不能只靠 `clickhouse-client < 0001.sql` 两次。流程：
  ```
  ClickHouse bootstrap Job
    → 读 migrations/*.sql
    → 计算 SHA256
    → 读 observability.aiops_schema_migrations
    → 已应用：checksum 同 → skip；不同 → FAIL CLOSED
    → 未应用：execute；success → record migration_id/checksum
  ```
- **元数据表 = `observability.aiops_schema_migrations`**（不复用通用 `schema_migrations`），结构与 MySQL 对齐：`migration_id String, checksum String, applied_at DateTime`。
- 本地部署 V1 依赖**单个 Helm migration Job** 保证无并发 migrator（暂不设计 ClickHouse 分布式锁）。
- **必须真实测试**：first bootstrap PASS / second bootstrap PASS(skip) / **modified migration → checksum mismatch FAIL**。
- event-collector 只做 `EnsureSchemaCompatible()`（`SHOW TABLES`/`DESCRIBE`，缺失/不兼容 fail-closed），**不调用迁移器**（否则变 runtime DDL）。

- [ ] **Step 1: 建立 ClickHouse bootstrap runner + 元数据表**

`bootstrap-job.yaml` 运行 ClickHouse migration runner（独立于 runtime）。`0001_observability_baseline.sql` 以 `observability.aiops_schema_migrations` 开头，后接 `trace_spans`/`service_topology`/`alert_events`/`k8s_events`（迁入自 event-collector）。

- [ ] **Step 2: 标注 log_records LEGACY**

`log_records` 加注释：`-- LEGACY: Raw Logs 完整副本；V9.2 要求 Raw Logs 只进 VictoriaLogs，新写入不再产生完整副本`。

- [ ] **Step 3: event-collector 改只读校验**

删除 `const chDDL` 和启动 CREATE；改 `EnsureSchemaCompatible()` 只读校验。

- [ ] **Step 4: 真实三态测试**

```bash
# first bootstrap
clickhouse-client --multiquery < bootstrap 流程   # PASS，1 条 migration 记录
# second bootstrap                                  # PASS / skip，版本数不变
# 篡改 0001 内容后重跑                              # FAIL checksum mismatch（用专用 fixture，勿改真文件忘恢复）
```

---

### Task 6：VictoriaMetrics / VictoriaLogs Scope Label Contracts（P4.6）

**Files:**
- Create: `docs/contracts/vmlogs-label-contract.md`
- Create: `docs/contracts/vmetrics-label-contract.md`
- Create: `ai-apm-ingest-go/internal/telemetrylabels/labels.go`
- Create: `ai-apm-ingest-go/internal/telemetrylabels/labels_test.go`

**Interfaces:**
- Consumes: Phase 3 UUID validator（**复用，不重写第二套 regex**，P1-2）
- Produces: `telemetrylabels.ValidateScopeLabels` / `NormalizeScopeLabels`（独立包，不绑 ClickHouse wal.go）

- [ ] **Step 1: 写 label contract 文档**

必选 `tenant_id`、`cluster_id`（canonical UUID）；`resource_id` 按 scope（resource→REQUIRED；cluster/aggregate→按 contract）。

- [ ] **Step 2: 实现独立包（复用 UUID validator）**

```go
// 复用 Phase 3 的 canonical UUID validator（不得在 ingest 重写不同 regex）。
func ValidateScopeLabels(labels map[string]string, scope string) error {
    if !isCanonicalUUID(labels["tenant_id"]) { return errors.New("tenant_id must be canonical uuid") }
    if !isCanonicalUUID(labels["cluster_id"]) { return errors.New("cluster_id must be canonical uuid") }
    if scope == "resource" && !isCanonicalUUID(labels["resource_id"]) {
        return errors.New("resource_id must be canonical uuid for resource scope")
    }
    return nil
}
```

- [ ] **Step 3: 失败测试（拒绝非 UUID）**

`labels_test.go` 断言：
```go
// cluster_id=orbstack → reject
// cluster_id=1        → reject
// cluster_id=default  → reject
// valid UUID          → pass
// resource scope 缺 resource_id → reject
// cluster scope 缺 resource_id → pass
```

---

### Task 7：Object Store + Chroma Bootstrap Contracts（P4.7）

**Files:**
- Create: `docs/contracts/object-store-contract.md`
- Create: `deploy/helm/aiops/templates/object-store/bootstrap-job.yaml`
- Create: `docs/contracts/chroma-collection-contract.md`
- Create: `ai-orchestrator/rag_bootstrap.py`
- Modify: `ai-orchestrator/rag.py:112-122`（get_or_create → bootstrap create + runtime get）

**Interfaces:**
- Consumes: V9.2 冻结 MinIO Object SoT；Task 2 matrix
- Produces: **真实 Object Store bootstrap**（P0-D）+ Chroma collection 进 bootstrap（修正 7）

**设计约束（P0-D）：**
- **Phase 4 必须建立真实 Object Store bootstrap（MinIO-compatible，选项 A）**；无正式 Erratum 不得走选项 B。
- bootstrap：`create_or_validate bucket aiops-evidence` + `aiops-knowledge`；二次 bootstrap 幂等；**runtime 不负责 CreateBucket**。
- 若仓库确实完全无 MinIO deployment/runtime dependency，实施中可判 `BLOCKED_OBJECT_STORE_RUNTIME_MISSING`，但**不能 Gate PASS 后推迟**。

- [ ] **Step 1: 写 Object Store contract**

`object-store-contract.md` 冻结：
```text
evidence bucket:  aiops-evidence
  prefix: <tenant_id>/<cluster_id>/<run_id>/<evidence_id>
  raw_digest: sha256 存 ai_evidence.raw_digest_sha256
  retention: 与 ai_evidence 生命周期一致
knowledge bucket: aiops-knowledge
  prefix: <tenant_id>/<cluster_id>/<doc_id>
  tenant isolation: object key 强制含 tenant_id
```

- [ ] **Step 2: 建立 Object Store bootstrap**

`object-store/bootstrap-job.yaml` 在 init 阶段 `create_or_validate` 两个 bucket，幂等；验证 bucket exists + prefix contract。runtime 不创建 bucket。若部署产物缺失，记录 `BLOCKED_OBJECT_STORE_RUNTIME_MISSING` 但保持 Gate 不可 PASS 跳过。

- [ ] **Step 3: 写 Chroma collection contract + 移出 runtime**

`chroma-collection-contract.md`：`ops_cases` / `ops_playbooks`。`rag_bootstrap.py` 在 bootstrap 阶段 create/validate collection；`rag.py` runtime 只 `get_collection`，缺失 → readiness 失败（不 get_or_create）。补测试断言 runtime 不创建 collection。

---

### Task 8：Remove Runtime Schema Creation（P4.8）

**Files:**
- Create: `deploy/helm/aiops/templates/mysql/users-init-job.yaml`
- Create/Modify: ClickHouse users/profile（`clickhouse_migrator` + `clickhouse_app/ingest/collector`）
- Modify: `deploy/helm/aiops/templates/query-api/deployment.yaml:70-74`
- Modify: `deploy/helm/aiops/templates/ai-orchestrator/deployment.yaml:63-67`（按 P0-E 决定保留或移除 MYSQL credentials）
- Modify: `deploy/helm/aiops/templates/mysql/migration-job.yaml`（顺序 hook）
- Modify: `deploy/helm/aiops/values.yaml`（账号名 + Secret reference）
- Modify: `ai-orchestrator/main.py:78-80`（移除 db.migrate()）
- Modify: `ai-event-collector/clickhouse.go`

**Interfaces:**
- Consumes: Task 1 `ORCHESTRATOR_MYSQL_USAGE`；Task 3/5 migrator；Task 7 Chroma bootstrap
- Produces: MySQL `aiops_app`(DML)/`aiops_migrator`(DDL)、ClickHouse `clickhouse_app`(DML)/`clickhouse_migrator`(DDL)、orchestrator 移除直连建表

**设计约束（P0-E/P0-F + P1-3/P1-4）：**
- **orchestrator 对 V9.2 新表直连 = FORBIDDEN**；只经 query-api 控制面。旧 legacy 直连依赖按 Task 1 清单保留（标 `LEGACY_DIRECT_MYSQL_DEPENDENCY`），不访问新 runtime 表、不成为新表 writer、不新增直连 repository。若 orchestrator 完全不需要 DB → **Phase 4 直接移除其 MYSQL credentials**。
- **ClickHouse 双账号（P1-3）**：`clickhouse_migrator`（DDL）+ `clickhouse_app/ingest/collector`（INSERT/SELECT，NO CREATE/ALTER/DROP）。Gate 跑 `SHOW GRANTS`、`CREATE TABLE → denied`、`INSERT/SELECT → allowed`。**不只是一句"确认权限不含 CREATE"**。
- **Helm 启动顺序 + 密码轮换（P1-4）**：
  ```text
  1. MySQL ready
  2. DB/user bootstrap Job（root credential）
  3. aiops_app / aiops_migrator ready
  4. schema-migrator Job
  5. runtime Deployments
  ```
  用 hook weight / dependency 保证。`CREATE USER IF NOT EXISTS` + `ALTER USER ... IDENTIFIED BY ...` 使 Secret rotation 后重新 bootstrap 对齐密码。**Secret 只来自 K8s Secret env/mount：禁止写入 ConfigMap / 日志 / migration SQL 文件**。

- [ ] **Step 1: 写 MySQL 受限账号 Job**

```sql
CREATE USER IF NOT EXISTS 'aiops_app'@'%' IDENTIFIED BY '<from_secret>';
ALTER USER 'aiops_app'@'%' IDENTIFIED BY '<from_secret>';
GRANT SELECT, INSERT, UPDATE, DELETE ON aiops.* TO 'aiops_app'@'%'; -- 无 CREATE/ALTER/DROP/INDEX

CREATE USER IF NOT EXISTS 'aiops_migrator'@'%' IDENTIFIED BY '<from_secret>';
ALTER USER 'aiops_migrator'@'%' IDENTIFIED BY '<from_secret>';
GRANT CREATE, ALTER, DROP, INDEX, SELECT, INSERT, UPDATE, DELETE ON aiops.* TO 'aiops_migrator'@'%';
```
账号密码来自 Secret env/mount（`aiops-secrets`），不落 ConfigMap/日志/SQL 文件。

- [ ] **Step 2: ClickHouse 双账号部署产物**

创建 ClickHouse `clickhouse_migrator`（DDL）+ `clickhouse_app`（INSERT/SELECT）users/profile/config 或 init user job。Gate 验证 `CREATE TABLE → denied`。

- [ ] **Step 3: 改写运行时账号 + 按 P0-E 处理 orchestrator**

query-api deployment `MYSQL_USER=aiops_app`。orchestrator：若无需 DB → 移除 MYSQL credentials；若保留 legacy → 明确 legacy tables + reason + removal phase。移除 `main.py` 的 `db.migrate()`。

- [ ] **Step 4: 验证启动顺序 + 失败测试**

Helm hook 保证 migrator Job 在 user bootstrap 之后、runtime 之前。部署后：`mysql -u aiops_app -e "CREATE TABLE x(y int)"` → `Access denied`；SELECT 正常；ClickHouse `CREATE TABLE` → denied；`INSERT/SELECT` → allowed；`db.migrate()` 不再被调用。

---

### Task 9：Gate Tests（P4.9）

**Files:**
- Create: `deploy/scripts/phase4-gate.sh`
- Create: `ai-apm-query-go/internal/store/migrations/idempotency_test.go`

**Interfaces:**
- Consumes: Task 3-8 全部
- Produces: Gate 4 证据（A/B/C + restricted accounts + runtime startup + checksum/version）

**设计约束：**
- **不 DROP 当前验收库**；用 ephemeral MySQL 或隔离库 `aiops_phase4_gate_<id>`（修正 8）。
- **Gate 拆三环境**：A empty bootstrap、B second bootstrap/idempotency、C existing-schema upgrade/preservation。
- **users/config unchanged 在 C 验证**，不在空库（修正 8）。
- **Secret 验证措辞修正**：MySQL 侧验证 `users unchanged` / `platform_settings unchanged` / `clusters.credential_ref unchanged`；K8s 侧验证 `existing credential Secret objects unchanged`（只比 existence/metadata/digest，**不打 Secret data 进报告**）。不写"secrets unchanged"这种不准确表述。
- **篡改 migration 测试用专用 fixture**，不是改真文件忘恢复（修正 9）。

- [ ] **Step 1: empty init（环境 A，隔离库）**

跑 migrator + init，断言控制面表 + LEGACY_RUNTIME_REQUIRED_TABLES 全存在、`aiops_schema_migrations` migration_id 全量、无报错。

- [ ] **Step 2: second bootstrap / idempotency（环境 B）**

再跑一遍，版本数不变、无 "already exists"。`idempotency_test.go` 断言二次 `Run` 不重复 INSERT migration_id。

- [ ] **Step 3: existing-schema upgrade / preservation（环境 C）**

含现有表的库跑升级：旧表 LEGACY 数据保留、`users`/`platform_settings`/`clusters.credential_ref` 内容不变、新增表正确建立、K8s credential Secret objects unchanged（existence/metadata/digest）。

- [ ] **Step 4: restricted runtime accounts + startup**

`aiops_app` SELECT/INSERT 成功、CREATE 失败；`clickhouse_app` CREATE 失败；`RequireCurrentVersion` 正常返回启动；version 缺失或 **checksum mismatch** → fail-closed。

- [ ] **Step 5: checksum / version verification（用 fixture）**

专用 fixture migration：篡改内容 → checksum mismatch → `RequireCurrentVersion` 报 `ErrSchemaChecksumMismatch`；缺失 migration → `ErrSchemaOutdated`。

---

### Task 10：Phase 4 Gate Report（P4.10）

**Files:**
- Modify: `docs/AIOPS_AGENTIC_IMPLEMENTATION_REPORT.md`
- Create: `docs/superpowers/plans/phase4-gate-result.md`

- [ ] **Step 1: 汇总 Gate 4 三环境证据**

```text
A empty environment init PASS（含 LEGACY_RUNTIME_REQUIRED_TABLES）
B second idempotent init PASS
C existing-schema upgrade/preservation PASS（MySQL users/platform_settings/clusters.credential_ref + K8s credential Secret objects unchanged）
runtime accounts without DDL can start PASS（MySQL + ClickHouse）
migration checksum/version verification PASS（ErrSchemaOutdated / ErrSchemaChecksumMismatch）
```

- [ ] **Step 2: 记录 LEGACY 项与 Phase 5/6 交接**

`log_records`、`audit_logs`、旧 `schema_migrations`（LEGACY_MIGRATION_METADATA）、orchestrator 直连建表、`get_or_create_collection`、legacy runtime 直连依赖（含 removal phase）等。明确 Phase 4 不做 dual-write、writer cutover Phase 5、reader cutover Phase 6。

- [ ] **Step 3: 更新实施报告**

状态更新为 `Phase 4 PASS (Gate 4)`；P4.1-P4.10 全 COMPLETE。输出 `NEXT_PHASE: 5` 前**停止**。

---

## Self-Review（对照 R3 审查逐条核对）

**6 项 P0：**
- P0-A 不复用旧 `schema_migrations`，新权威表 `aiops_schema_migrations`（VARCHAR(255)+checksum+applied_at），旧表 LEGACY/PRESERVE/NO ALTER → Task 3 ✅
- P0-B `RequireCurrentVersion` 校验 checksum（不只看版本号）：missing→`ErrSchemaOutdated`、mismatch→`ErrSchemaChecksumMismatch`，均 fail-closed，只 SELECT → Task 3/9 ✅
- P0-C ClickHouse 真实 versioned migrator（`observability.aiops_schema_migrations` + bootstrap Job + SHA256 + 三态测试）→ Task 5 ✅
- P0-D MinIO 真实 bootstrap（create/validate bucket + 幂等 + runtime 不建 bucket；`BLOCKED_OBJECT_STORE_RUNTIME_MISSING` 不可 Gate PASS 推迟）→ Task 7 ✅
- P0-E orchestrator MySQL 用途三类盘点；新 V9.2 表直连 FORBIDDEN；legacy 显式列出 removal phase；不需要则移除 credentials → Task 1/2/8 ✅
- P0-F 空环境 legacy compatibility：baseline 含 TARGET_TABLES + LEGACY_RUNTIME_REQUIRED_TABLES → Task 3/9 ✅

**4 项 P1：**
- P1-1 Task 4 逐表写全列，以 frozen definition 为唯一来源，schema_manifest_test 逐表核对；ai_runs CHECK scope_kind + primary_cluster_id 语义 → Task 4 ✅
- P1-2 Task 6 复用 Phase 3 UUID validator，测试拒 orbstack/1/default → Task 6 ✅
- P1-3 Task 8 ClickHouse 双账号部署产物 + SHOW GRANTS/CREATE denied/INSERT allowed → Task 8 ✅
- P1-4 Task 8 Helm 顺序 + `CREATE USER IF NOT EXISTS` + `ALTER USER ... IDENTIFIED BY` + Secret 只来自 env/mount → Task 8 ✅

**Gate 9 措辞：**
- 环境 C 分 MySQL（users/platform_settings/clusters.credential_ref）与 K8s（credential Secret objects existence/metadata/digest），不打 Secret data → Task 9 ✅
- 篡改 migration 用专用 fixture → Task 9 ✅

**复用原 R2 已确认项：**
- AI Runtime frozen model 表名 / ai_runs multi-cluster 语义 / /internal/v1/runs 删除 / migration 无事务原子性(GET_LOCK+checksum) / namespaced id / ClickHouse 单 authority / Chroma 移出 runtime / 隔离库 A/B/C / 账号收紧+Secret → 全保留 ✅

**任务树不变：** P4.1-P4.10 ✅
