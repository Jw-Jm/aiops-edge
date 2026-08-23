# Phase 17 — Data Deletion Manifest（Preflight，P17.1-P17.4）

> 日期：2026-08-21 ｜ GIT_ACTION：NONE ｜ MODE：预检（不执行删除）
> 依据：合同 §八十三 P17.1-P17.4 + §83.1 DATA_DELETION_MANIFEST + §83.2/83.3
> **状态：PREFLIGHT。P17.5 精确授权前绝不执行任何删除。**
> 环境：`orbstack` 本地开发栈（observability namespace，local acceptance）

---

## P17.1 Maintenance Preconditions（9 项逐项核验）

| # | Precondition | 核验方式（机器） | 结果 |
|---|---|---|---|
| 1 | no active investigation run | `SELECT COUNT(*) FROM ai_runs` = 0 | ✅ |
| 2 | no executing action | `SELECT COUNT(*) FROM ai_actions` = 0 | ✅ |
| 3 | no verifying action | `SELECT COUNT(*) FROM ai_verifications` = 0 | ✅ |
| 4 | no pending confirmation | `ai_actions` count=0（无待确认 action）| ✅ |
| 5 | no pending approval requiring preservation | `SELECT COUNT(*) FROM ai_approval_decisions` = 0 | ✅ |
| 6 | all writers entered maintenance | 执行阶段（P17.6）才需，预检 N/A | ⏸ N/A |
| 7 | all WAL/outbox drained or preserved | `ai_run_outbox`=0；ingest `/wal` 仅 `deepflow_last_sync`(21B) | ✅ |
| 8 | new schema/version verified | query-api v1.1.7-p10e + 0003/0003b 迁移已应用生产库 | ✅ |
| 9 | environment identified as local acceptance | orbstack 本地开发栈 | ✅ |

**结论：P17.1 预检 7/7 满足（第 6 项属执行阶段），不 BLOCKED。**

---

## P17.2 Physical Object Enumeration（实际枚举，禁止猜名）

> 所有 actual name 均来自生产环境 `SHOW TABLES`/`SHOW TABLES FROM observability`/`kubectl get pvc` 实测。

### A. MySQL `aiops` 库

**AI 运行时表（§83.3 DELETE 候选类：AI Runs/Tool Runs/Evidence/Hypotheses/Actions/Verifications/Run Events）**
| table | rows | last_write 参考 | 备注 |
|-------|------|-----------------|------|
| ai_runs | 0 | — | 空 |
| ai_actions | 0 | — | 空 |
| ai_evidence | 0 | — | 空 |
| ai_hypotheses | 0 | — | 空 |
| ai_plan_steps | 0 | — | 空 |
| ai_tool_runs | 0 | — | 空 |
| ai_verifications | 0 | — | 空 |
| ai_run_events | 0 | — | 空 |
| ai_run_outbox | 0 | — | 空 |
| ai_approval_decisions | 0 | — | 空 |
| ai_audit_events | 0 | — | 空 |
| ai_run_clusters | 0 | — | 空 |
| **ai_control_commands** | **1** | 2026-08-21 12:51:58 | `phasec-cmd-1`，run_id=aaaaaaaa-..., operation=transition, status=**done**（Phase C 真实验证遗留）|
| aiops_schema_migrations | (schema 元数据) | — | PRESERVE |

**runtime 历史表**
| table | rows | 分类 |
|-------|------|------|
| topology_nodes | 56 | runtime Topology |
| topology_relations | 81 | runtime Topology |
| reports | 11 | runtime Report |
| change_events | 0 | runtime Change（空）|
| anomaly_events | 0 | runtime Anomaly（空）|
| approval_tasks | 0 | runtime（空）|
| audit_logs | 3 | 审计（见决策，谨慎）|
| platform_audit_events | 0 | 审计（空）|

**config/保留表**
| table | rows | 分类 |
|-------|------|------|
| alert_rules | 10 | config → PRESERVE |
| slo_targets | 0 | config → PRESERVE |
| llm_providers | 1 | config → PRESERVE |
| platform_settings | 2 | config → PRESERVE |
| llm_config_history | config | config → PRESERVE |
| users / roles / permissions / tenants / clusters / user_tenants / user_roles / role_permissions / scope_assignments / agents | 见 P17.3 | 永久保留（§83.2）|

### B. ClickHouse `observability` 库

| table | rows | 分类 |
|-------|------|------|
| **k8s_events** | **374** | runtime Resource Event（DELETE 候选）|
| **alert_events** | **16** | runtime Alert（DELETE 候选）|
| log_records | 0 | runtime Log（空）|
| trace_spans | 0 | runtime Trace（空）|
| metric_service_red | 0 | runtime RED（空）|
| service_topology | 0 | runtime Topology（空）|
| inspection_reports | 0 | runtime（空）|
| llm_providers / llm_config_history / platform_settings | 0 | config（空，保留结构）|

### C. VictoriaMetrics / VictoriaLogs
- retention 30d（时间-based 自动过期），**非 P17 手动删除对象**；数据在 `vm-data`/`vlogs-data` PVC。

### D. Chroma（orchestrator-data PVC `/var/lib/aiops`）
| object | size | 分类 |
|--------|------|------|
| ops-cases/collection `55bad1bf-...` | 228K | 知识库 collection（§83.2 valid knowledge → PRESERVE，除非确认 obsolete）|
| ops-cases/collection `c877ec6a-...` | 228K | 同上 → PRESERVE |
| ops-cases/chroma.sqlite3 | 2.1M | 知识库索引 → PRESERVE |
| ai-sessions.db | 0 | session checkpoint 空 |

### E. PVC / WAL
| PVC | size | 分类 |
|-----|------|------|
| ingest-wal | 5Gi | runtime-only PVC（§83.3）→ DELETE 候选（对象为 /wal 内 WAL 数据，非 PVC 本身）|
| orchestrator-data | 5Gi | 含 Chroma knowledge → PRESERVE（对象为 Chroma，非 PVC 本身）|
| vm-data / vlogs-data | 5Gi/5Gi | retention 管理 → 非 DELETE |
| data-clickhouse-0 | 20Gi | 含 CH 表数据 → 按表级 DELETE |
| data-mysql-0 | 10Gi | 含 MySQL 表数据 → 按表级 DELETE |
| backup-pvc | 5Gi | 备份 → PRESERVE |

---

## P17.3 Manifest Classification + Decision

> 规则：`UNKNOWN → NEVER DELETE`；`PRESERVE` 区（§83.2）绝不进入 DELETE。

### DELETE 候选（有实际数据的对象）

| object | storage | classification | actual | size | decision | reason/risk |
|--------|---------|---------------|--------|------|----------|-------------|
| ai_control_commands 行 phasec-cmd-1 | MySQL | NEW_SCHEMA_PRE_ACCEPTANCE_RUNTIME_DATA | phasec-cmd-1 | 1 row | **DELETE** | Phase C 真实验证遗留的已 done transition 命令；无 pending 语义。风险：低（status=done）|
| topology_nodes | MySQL | NEW_SCHEMA_PRE_ACCEPTANCE_RUNTIME_DATA | topology_nodes | 56 rows | **DELETE** | historical Topology runtime。风险：低（可重建）|
| topology_relations | MySQL | NEW_SCHEMA_PRE_ACCEPTANCE_RUNTIME_DATA | topology_relations | 81 rows | **DELETE** | historical Topology runtime。风险：低 |
| k8s_events | ClickHouse | NEW_SCHEMA_PRE_ACCEPTANCE_RUNTIME_DATA | observability.k8s_events | 374 rows | **DELETE** | historical Resource Event（§83.3）。风险：低 |
| alert_events | ClickHouse | NEW_SCHEMA_PRE_ACCEPTANCE_RUNTIME_DATA | observability.alert_events | 16 rows | **DELETE** | historical Alert（§83.3）。风险：低 |
| reports | MySQL | NEW_SCHEMA_PRE_ACCEPTANCE_RUNTIME_DATA | reports | 11 rows | **DELETE** | historical Report runtime。风险：低 |
| change_events / anomaly_events / approval_tasks / platform_audit_events 及全部 ai_* 空表 | MySQL | NEW_SCHEMA_PRE_ACCEPTANCE_RUNTIME_DATA | （空）| 0 | DELETE（空表，truncate 语义）| 无数据，仅确保干净。风险：无 |

### PRESERVE（§83.2 永久保留，绝不删除）
users / roles / permissions / tenants / clusters / user_tenants / user_roles / role_permissions / scope_assignments / llm_providers / platform_settings / llm_config_history / alert_rules / slo_targets / aiops_schema_migrations / **Chroma ops-cases（valid knowledge）** / backup-pvc / vm-data / vlogs-data / orchestrator-data（Chroma）。

### UNKNOWN（→ NEVER DELETE）
| object | reason |
|--------|--------|
| **audit_logs（3 rows）** | 审计日志安全敏感，合同 DELETE 候选未明确列出 audit → **UNKNOWN → PRESERVE** |
| **clickhouse llm_providers / platform_settings / llm_config_history（0 rows）** | 虽空但属 config 保留区 → PRESERVE |
| **MySQL reports** | 已列为 DELETE（runtime report），若需保留历史报表可改 PRESERVE —— 见决策点 |

### 决策点（需你确认，影响 manifest）
1. **reports（11 rows）**：报表 runtime 数据，DELETE 或 PRESERVE？（合同 §83.3 未明列 report）
2. **audit_logs（3 rows）**：我判定 UNKNOWN→PRESERVE（审计不删）。是否同意？
3. **Chroma ops-cases**：知识库 collection，PRESERVE。是否确认非 obsolete？

---

## P17.4 Manifest Hash（canonical serialize + SHA-256）

> ⚠️ **本 manifest 仅作为未来 P17.5 精确删除授权的依据，不构成删除授权。** 任何 manifest 字段/判定/过滤条件变化，都使 SHA-256 改变，须**重新授权**。`k8s_events`/`alert_events` 为 event-collector 持续写入的 live 表，行数会随运行漂移（生成时快照 374/16，执行时以精确 count 为准），过滤条件恒为 `ALL`（整表清空），行数不参与删除语义。

### 最终授权标识（修正版：manifest_id 不参与 hash）
```
MANIFEST_ID            : P17-cc6501ad68bd
AUTHORIZATION SHA-256  : d605cc0dea9a7149031a2f5839ba75146440f8d0857520144e13695f4c7cfceb
ENVIRONMENT            : orbstack-local-acceptance
NAMESPACE              : observability
```

### Canonical Serialization 规则
- 递归排序 dict keys；`json.dumps` 用 `ensure_ascii=True, separators=(',',':'), sort_keys=True`。
- **授权引用值 = SHA-256(不含 manifest_id 字段的 canonical JSON body)**；manifest_id 由该 hash 派生（`P17-` + UUIDv5(URL, sha)[:12]），不参与自身 hash（避免自引用循环、保证可复现）。

### Canonical Manifest（不含 manifest_id；此为 SHA-256 的输入）
```json
{"backup":{"note":"pre-cleanup backup preserved","pvc":"backup-pvc"},
"decisions":{"audit_logs":"PRESERVE","chroma_ops_cases":"PRESERVE","reports":"DELETE"},
"delete_empty_tables_truncate":[{"storage":"mysql","table":"ai_runs"},{"storage":"mysql","table":"ai_actions"},{"storage":"mysql","table":"ai_evidence"},{"storage":"mysql","table":"ai_hypotheses"},{"storage":"mysql","table":"ai_plan_steps"},{"storage":"mysql","table":"ai_tool_runs"},{"storage":"mysql","table":"ai_verifications"},{"storage":"mysql","table":"ai_run_events"},{"storage":"mysql","table":"ai_run_outbox"},{"storage":"mysql","table":"ai_approval_decisions"},{"storage":"mysql","table":"ai_audit_events"},{"storage":"mysql","table":"ai_run_clusters"},{"storage":"mysql","table":"change_events"},{"storage":"mysql","table":"anomaly_events"},{"storage":"mysql","table":"approval_tasks"},{"storage":"mysql","table":"platform_audit_events"}],
"delete_objects":[{"classification":"NEW_SCHEMA_PRE_ACCEPTANCE_RUNTIME_DATA","filter":"command_id='phasec-cmd-1' AND status='done'","rows":1,"storage":"mysql","table":"ai_control_commands"},{"classification":"NEW_SCHEMA_PRE_ACCEPTANCE_RUNTIME_DATA","filter":"ALL","rows":56,"storage":"mysql","table":"topology_nodes"},{"classification":"NEW_SCHEMA_PRE_ACCEPTANCE_RUNTIME_DATA","filter":"ALL","rows":81,"storage":"mysql","table":"topology_relations"},{"classification":"NEW_SCHEMA_PRE_ACCEPTANCE_RUNTIME_DATA","filter":"ALL (precondition: no user-retained/archived/manual-exported formal reports)","rows":11,"storage":"mysql","table":"reports"},{"classification":"NEW_SCHEMA_PRE_ACCEPTANCE_RUNTIME_DATA","filter":"ALL","rows":"dynamic (snapshot 374 at generation; live table, exact count at execution; filter ALL)","storage":"clickhouse","table":"observability.k8s_events"},{"classification":"NEW_SCHEMA_PRE_ACCEPTANCE_RUNTIME_DATA","filter":"ALL","rows":"dynamic (snapshot 16 at generation; live table, exact count at execution; filter ALL)","storage":"clickhouse","table":"observability.alert_events"}],
"environment":"orbstack-local-acceptance","generated_at":"2026-08-21T00:00:00Z","namespace":"observability","phase":"17",
"preserve_objects":{"chroma":{"collections":["55bad1bf-8ddb-46b7-82ca-4fc412b0d826","c877ec6a-cd2d-403f-b796-1107dae43d3f"],"note":"valid knowledge, PRESERVE","path":"/var/lib/aiops/ops-cases"},
"mysql_tables":["users","roles","permissions","tenants","clusters","user_tenants","user_roles","role_permissions","scope_assignments","llm_providers","platform_settings","llm_config_history","alert_rules","slo_targets","aiops_schema_migrations","audit_logs"],
"note":"PVC 按表级/对象级清理，PVC 本身 PRESERVE","pvc":["backup-pvc","vm-data","vlogs-data","orchestrator-data","data-clickhouse-0","data-mysql-0"]},
"schema_version":"1"}
```

### 授权与执行完成记录（P17.5/P17.6，2026-08-21）

**P17.5 授权**：用户"授权 P17.5 执行删除"，引用 manifest_id=`P17-cc6501ad68bd`、sha256=`d605cc0d...`、environment=`orbstack-local-acceptance`。

**Backup（删除前保护，已存入 backup-pvc/p17/）**：
- `p17_mysql.sql`（82KB，20 表 mysqldump）
- `p17_k8s.tsv`（138KB，k8s_events 375 行）
- `p17_alerts.tsv`（8.8KB，alert_events 16 行）
- 另有 backup-pvc 已有 mysql-backup/clickhouse-backup cronjob 定期备份。

**P17.6 删除执行 + post-check（全部成功）**：
| 对象 | 操作 | 删除量 | post-check |
|------|------|--------|-----------|
| MySQL `ai_control_commands` | DELETE WHERE command_id='phasec-cmd-1' AND status='done' | 1 | remaining=0 ✅ |
| MySQL `topology_nodes` | DELETE ALL | 56 | remaining=0 ✅ |
| MySQL `topology_relations` | DELETE ALL | 81 | remaining=0 ✅ |
| MySQL `reports`（inspection 自动报告，无人工导出）| DELETE ALL | 11 | remaining=0 ✅ |
| MySQL 16 空表 | TRUNCATE | — | 全部 0 ✅ |
| ClickHouse `k8s_events` | TRUNCATE | 375 | count=0 ✅ |
| ClickHouse `alert_events` | TRUNCATE | 16 | count=0 ✅ |

**P17.6 验证**：
- 生产服务全 Running（query-api/ingest/orchestrator/frontend/mysql/clickhouse/victoria-*）。
- CH 10 表结构保留（含空表/config 表），schema 未破坏。
- MySQL PRESERVE 未动：audit_logs=3/users=2/tenants=2/clusters=1/alert_rules=10/llm_providers=1/platform_settings=2/aiops_schema_migrations=5。
- Chroma 知识库保留（2 collections + sqlite3，2.5M）。
- 维护态确认：active_runs=0/pending_outbox=0/executing_actions=0。
- **备注**：event-collector 的 `context canceled`（K8s watch）+ 未持续 flush 是**既有行为**（删除前 15:04 即有），非删除引入；删除仅 TRUNCATE 清空数据，不影响写入路径/表结构/权限。

### 自检记录（2026-08-21）
- 自检发现 `k8s_events` 生成时 374 行、复检时 375 行（event-collector 持续写入）。已确认其为 **live 表**，行数随运行漂移。
- 修正：`k8s_events`/`alert_events` 行数标注为 `dynamic（snapshot at generation，执行时以精确 count 为准）`，过滤条件恒为 `ALL`（整表清空），行数不参与删除语义。
- 修正 canonical 规则：`manifest_id` 不参与 hash（避免自引用），授权引用值 = SHA-256(不含 manifest_id 的 canonical body)。**旧 hash `efc1b433...` / `P17-00840f84a0dd` 作废**，以 `d605cc0d...` / `P17-cc6501ad68bd` 为准。

### 授权前提
- `reports` DELETE 前提：不含用户留存/合规归档/人工导出的正式报告（需 P17.5 执行前复核）。
- `audit_logs`（3 rows）：PRESERVE，不删。
- Chroma `ops-cases`（2 collections + sqlite3）：PRESERVE，视为有效知识库。
- 所有 DELETE 前必须先做 backup（`backup-pvc` 保留）。
- **本 preflight 不执行任何删除；P17.5 精确授权前绝不操作。**
