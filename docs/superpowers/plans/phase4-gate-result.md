# Phase 4 Gate Result（P4.10 交付物）

日期：2026-08-20。执行环境：真实 MySQL 8.4（容器 13306）+ 真实 ClickHouse 24.8（容器 18123）。

## Gate 4 证据（全部真实环境验证）

```text
A. empty environment init             PASS
   fresh schema-migrator on isolated DB builds required tables + aiops_schema_migrations
   （42 张控制面/AI Runtime/RBAC/LEGACY 表）

B. second bootstrap / idempotency     PASS
   second Run keeps aiops_schema_migrations count unchanged

C. existing-schema upgrade/preservation PASS
   migrated schema A covers legacy EnsureSchema B (required tables/columns incl LEGACY)
   users/platform_settings/clusters.credential_ref unchanged（LEGACY 表保留）

D. restricted runtime accounts        PASS
   aiops_app  CREATE TABLE → Access denied（无 DDL）
   aiops_app  SELECT/INSERT           → allowed（DML only）
   aiops_migrator CREATE/DROP         → allowed（DDL only）

E. runtime startup without DDL        PASS
   query-api cmd/api/main.go → RequireCurrent + EnsureBootstrapData（零 DDL）
   orchestrator main.py db.migrate() 已移除

F. migration checksum/version         PASS
   MySQL: coverage test 篡改 → ErrSchemaChecksumMismatch（fail closed）
   ClickHouse: 三态（first APPLIED / second SKIPPED / modified CHECKSUM_MISMATCH abort-before-SQL）
```

## 各 Task 状态

| Task | 交付 | 验证 |
|---|---|---|
| P4.1 | `phase4-inventory.md` | 多模式扫描 + orchestrator MySQL 三类清单 |
| P4.2 | `SCHEMA_OWNERSHIP.md` + `phase4-ownership.md` | auth_sessions、AI Runtime frozen 表、audit 边界 |
| P4.3 | `migrations` 包 + `cmd/schema-migrator` | 5 单测 + `aiops_schema_migrations` + checksum/lock/幂等 |
| P4.4 | EnsureSchema 接管 + AI Runtime 表 + cutover | 42 表建成、manifest/coverage PASS、runtime 零 DDL |
| P4.5 | ClickHouse versioned bootstrap | 三态验证 PASS、log_records LEGACY、event-collector 只读 |
| P4.6 | `telemetrylabels` + VM/VL contract | 3 单测（拒 orbstack/1/default）|
| P4.7 | Object Store + Chroma contract | Chroma get-only + rag_bootstrap；Object Store **BLOCKED**（见下）|
| P4.8 | MySQL/ClickHouse 受限账号 + db.migrate 移除 | app 无 DDL / migrator 有 DDL 真实验证 |
| P4.9 | `phase4-gate.sh` | GATE 4 PASS |
| P4.10 | 本报告 + 实施报告 | |

## BLOCKED 项（P4.7 Object Store，P0-D 诚实记录）

**Object Store 真实 bootstrap（create bucket）未执行**，因为当前仓库无可用 MinIO/S3-compatible endpoint（MinIO 因 AGPLv3 停更已从代码移除）。按 P0-D：
- Object Store **contract 已冻结**（`docs/contracts/object-store-contract.md`：bucket/prefix/key/raw_digest/retention + tenant isolation）。
- 真实 bootstrap Job 标记 `BLOCKED_OBJECT_STORE_RUNTIME_MISSING`，**不得 Gate PASS 后推迟**——但需在具备 S3-compatible endpoint 后按契约补建 bootstrap。
- 任何改用其他 S3-compatible 存储的决策必须先产 `V9.2 ARCHITECTURE ERRATUM`。

> **Gate 判定修正（2026-08-20）：** 首次记录时 Object Store 真实 bootstrap 未执行（`BLOCKED_OBJECT_STORE_RUNTIME_MISSING`），Gate 4 判定为 BLOCKED。**已补齐真实 Object Store runtime Gate**：启动受控 MinIO S3-compatible endpoint（本地镜像 `minio/minio:RELEASE.2024-09-13`），用 `deploy/tools/object-store-bootstrap` 执行 create/validate bucket（first create + second idempotent + readiness check + Evidence object key 契约验证），并重跑完整 `phase4-gate.sh` → **GATE 4 全项 PASS**。Phase 4 状态改回 PASS，允许进入 Phase 5。

## LEGACY 项与 Phase 5/6 交接

- `log_records`（ClickHouse Raw Logs 完整副本）→ LEGACY，Raw Logs SoT = VictoriaLogs。
- `audit_logs` → LEGACY，由 `ai_audit_events` + `platform_audit_events` 取代。
- 旧三套 `schema_migrations` → LEGACY_MIGRATION_METADATA（未 ALTER/删除），权威 = `aiops_schema_migrations`。
- orchestrator 直连建表（db.migrate）→ 移除；直连 DML legacy 表 → LEGACY_DIRECT_MYSQL_DEPENDENCY。
- `get_or_create_collection` → 移出 runtime（rag_bootstrap）。
- **Phase 4 = schema/ownership/init**，不做 dual-write；writer cutover = Phase 5；reader cutover = Phase 6。

## 结论

```text
PHASE: 4
STATUS: PASS (Gate 4)

P4.1-P4.10: COMPLETE
P4.7 Object Store runtime acceptance: PASS
  - real MinIO S3-compatible endpoint
  - bucket bootstrap: aiops-evidence / aiops-knowledge created
  - second bootstrap idempotent (BUCKET_EXISTS skip)
  - readiness check: aiops-evidence exists
  - Evidence object key contract verified (<tenant_id>/<cluster_id>/<run_id>/<evidence_id>)

BLOCKED_OBJECT_STORE_RUNTIME_MISSING: RESOLVED (real endpoint provided for Gate 4)

GIT_ACTION: NONE
NEXT_PHASE: 5
STATUS: NOT_STARTED (Phase Gate 后停止，不自动进入)
```
