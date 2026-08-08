# P1b：引入 MySQL 业务状态库 + 审批中心/审计页面 + 知识库/MCP 前端

**日期**: 2026-08-08
**范围**: ai-orchestrator（Python）+ observability-frontend（React）+ Helm 部署
**驱动**: 实施计划 P1b — "知识库/MCP 前端 + 审批/审计 + 引入 MySQL（审批/审计/Agent/规则/报告持久化，版本化迁移）"

---

## 1. 背景与问题

### 1.1 现状

| 数据 | 当前存储 | 问题 |
|------|---------|------|
| 审批任务（`_task_store`） | 内存 dict | **重启丢失** |
| 审计日志（`_audit_log`） | ClickHouse `ops_audit`（VL HTTP 接口）| 塞进时序库，职责混乱 |
| Agent（`ExpertRegistry`） | 内置 + `/tmp` JSON | 非持久化，多副本不一致 |
| 报告元数据 | ClickHouse `inspection_reports` | 塞进时序库 |
| 知识库 | 无 | 无 |
| 规则 | 无 | 无 |

### 1.2 架构判断（已确认）

**ClickHouse 不能整体移除**，但**职责要拆分**：

```
MySQL      ← 业务状态数据：审批/审计/Agent/规则/报告/知识库（低频小量，事务型）
ClickHouse ← 可观测性时序数据：trace_spans/log_records/metric_service_red/service_topology（海量，时间聚合，保留）
```

时序表（`trace_spans` 等）承载 `quantile`、`countIf`、`toStartOfMinute` 等列式分析查询，MySQL 行存无法胜任，必须保留在 ClickHouse。P1b 只把**业务状态类**数据统一到 MySQL，让组件职责单一。

---

## 2. 技术选型（已确认）

| 决策 | 选择 | 理由 |
|------|------|------|
| 数据归属 | **ai-orchestrator 直连 MySQL** | 数据产生者就是 orchestrator，改动集中 |
| Python 驱动 | **pymysql + 原生 SQL + DBUtils 连接池** | 最轻量、最适合生产；与现有 `sqlite3` 风格一致 |
| ORM | **不用**（轻量 DAO 层） | 避免 SQLAlchemy 重量依赖 |
| 迁移 | **轻量版本化迁移器**（`schema_migrations` + `NNNN_*.sql`）| 生产可版本化回滚，不引入 Alembic |
| 历史数据 | **直接重来** | 开发环境，MySQL 从零开始 |
| ClickHouse | 业务表废弃，时序表保留 | 见 §1.2 |

---

## 3. MySQL 表设计

Helm 已建 `aiops` 库 + `schema_migrations` 表。新增业务表（迁移器执行）：

### 3.1 `approval_tasks` — 审批任务（替代 `_task_store`）

| 字段 | 类型 | 说明 |
|------|------|------|
| id | BIGINT PK AI | |
| task_id | VARCHAR(64) UNIQUE | 现有 tid |
| service_name | VARCHAR(128) | 目标服务 |
| status | VARCHAR(24) | waiting/approved/rejected/done/failed |
| plan | TEXT | 方案 |
| script | TEXT | 脚本 |
| risk_score | FLOAT | 风险分 |
| risk_reason | TEXT | 风险理由 |
| diagnosis | TEXT | 诊断 |
| report | TEXT | 报告摘要 |
| requester | VARCHAR(64) | 发起人 |
| created_at | DATETIME | |
| decided_at | DATETIME NULL | 审批时间 |
| decision_by | VARCHAR(64) NULL | 审批人 |

索引：`status`、`created_at`

### 3.2 `audit_logs` — 审计日志（替代 ClickHouse `ops_audit`）

| 字段 | 类型 | 说明 |
|------|------|------|
| id | BIGINT PK AI | |
| task_id | VARCHAR(64) | |
| action | VARCHAR(64) | 动作 |
| operator | VARCHAR(64) | 操作者 |
| target_service | VARCHAR(128) | |
| command | TEXT | |
| result | VARCHAR(24) | |
| detail | JSON | 扩展信息 |
| created_at | DATETIME | |

索引：`(action, created_at)`、`(operator, created_at)`

### 3.3 `agents` — AI Agent（替代 `ExpertRegistry` JSON）

| 字段 | 类型 | 说明 |
|------|------|------|
| id | BIGINT PK AI | |
| name | VARCHAR(64) UNIQUE | |
| role | VARCHAR(128) | |
| goal | TEXT | |
| backstory | TEXT | |
| enabled | BOOL | |
| builtin | BOOL | 内置（不可删只可禁用）|
| created_at / updated_at | DATETIME | |

### 3.4 `reports` — 报告元数据（替代 ClickHouse `inspection_reports`）

| 字段 | 类型 | 说明 |
|------|------|------|
| id | BIGINT PK AI | |
| task_id | VARCHAR(64) | |
| service_name | VARCHAR(128) | |
| report_type | VARCHAR(64) | |
| verdict | VARCHAR(24) | |
| risk_score | FLOAT | |
| summary | TEXT | |
| content | LONGTEXT | 正文（文件仍存 MinIO，此列存摘要/文本）|
| file_key | VARCHAR(255) NULL | MinIO 对象键 |
| created_at | DATETIME | |

索引：`(service_name, created_at)`

### 3.5 `knowledge_base` — 知识库条目（含代码索引）

| 字段 | 类型 | 说明 |
|------|------|------|
| id | BIGINT PK AI | |
| title | VARCHAR(255) | |
| content | LONGTEXT | 文本内容 |
| source | VARCHAR(64) | manual / code_index / rag |
| tags | VARCHAR(255) | 逗号分隔 |
| code_ref | JSON NULL | 代码索引信息（file, line, symbol, language）|
| created_at / updated_at | DATETIME | |

### 3.6 `rules` — 规则（参考 ongrid Alert Rules 概念，适配本项目）

**不照搬 ongrid 完整告警引擎**，提炼其字段模型，用于平台通用规则配置（告警规则、审批规则、工作流条件等）。

| 字段 | 类型 | 说明 |
|------|------|------|
| id | BIGINT PK AI | |
| rule_key | VARCHAR(64) UNIQUE | 稳定标识 |
| name | VARCHAR(128) | 规则名 |
| kind | VARCHAR(32) | metric/log/trace/approval/flow（类型，决定 conditions 解释）|
| severity | VARCHAR(16) | warning / critical |
| enabled | BOOL | 启用状态（求值器只拉 enabled）|
| scope_type | VARCHAR(32) | global / service / approval |
| join_mode | VARCHAR(8) | all / any |
| conditions_json | JSON | **条件表达式，Kind 驱动解释**（不拆扁平列）|
| source_type | VARCHAR(32) | builtin / custom（内置不可删只可禁用）|
| created_by | VARCHAR(64) | |
| created_at / updated_at | DATETIME | |
| deleted_at | DATETIME NULL | 软删除 |

索引：`enabled`、`(kind, enabled)`

---

## 4. DAO 层（ai-orchestrator/db.py）

新增 `db.py`：
- **连接池**：`DBUtils.PooledDB(pymysql)`，线程安全
- **迁移器**：`migrate()` 读取 `migrations/` 下 `NNNN_*.sql`，查 `schema_migrations` 顺序执行未应用者
- **DAO 类**：`ApprovalStore` / `AuditStore` / `AgentStore` / `ReportStore` / `KnowledgeStore` / `RuleStore`

每个 DAO 提供 CRUD + 查询。**DB 不可达时降级为内存**（沿用现有 `_task_store` 静默模式），不阻塞服务。

### 代码结构

```
ai-orchestrator/
  db.py                # 连接池 + 迁移器 + DAO 类
  migrations/
    0001_approval_audit.sql
    0002_agents_reports.sql
    0003_knowledge_rules.sql
```

---

## 5. 后端 API 改动（ai-orchestrator/main.py）

| 端点 | 改动 |
|------|------|
| `/ops/tasks` CRUD | `_task_store` → `ApprovalStore` |
| `/ops/tasks/{tid}/approve|reject` | → `ApprovalStore.decide()` |
| `_audit_log()` | ClickHouse → `AuditStore.log()` |
| `/api/v1/ai/agents` CRUD | `ExpertRegistry` → `AgentStore` |
| `/ops/reports/history|trend` | ClickHouse → `ReportStore` |
| **`GET /api/v1/ops/audit-logs`** | 新增：审计分页查询 |
| **`GET/POST/DELETE /api/v1/ai/knowledge`** | 新增：知识库 CRUD（含代码索引写入）|
| **`GET/POST/PUT/DELETE /api/v1/ai/rules`** | 新增：规则 CRUD |

### 代码索引能力（rag.py）

- 新增代码索引采集：扫描配置的仓库/目录，提取文件/行/符号 → 写入 `knowledge_base`（`source=code_index`）
- RAG 查询可命中代码索引条目

---

## 6. 前端页面（observability-frontend）

新增 4 个路由（菜单挂"智能运维"分组）：

| 路由 | 页面 | 功能 |
|------|------|------|
| `/approvals` | 审批中心 | 审批任务列表（状态/风险分/服务），批准/驳回 |
| `/audit` | 审计日志 | 分页 + 按操作者/动作/服务过滤 |
| `/knowledge` | 知识库 | 条目增删改查 + 搜索 + 代码索引展示 |
| `/rules` | 规则管理 | 规则列表/启停/编辑（参考 ongrid AlertRules 交互）|

- API client（`client.ts`）新增对应函数
- 复用 AntD Table/Form + 现有 `uiStore`

---

## 7. 部署改动（Helm）

- ai-orchestrator deployment 注入 `MYSQL_HOST/PORT/USER/PASSWORD/DB`（密码用已有 `aiops-secrets.MYSQL_ROOT_PASSWORD`）
- `requirements.txt` 新增 `pymysql`、`DBUtils`
- `migrations/*.sql` 随镜像 COPY，启动时 `db.migrate()` 自动执行

---

## 8. 测试与验证

- `tests/` 新增：`test_db.py`（迁移器幂等）、`test_approval_store.py`、`test_audit_store.py`、`test_agents.py`、`test_reports.py`
- 现有 42 个测试不回归
- 本地：MySQL 不可用→内存降级可启动；MySQL 可用→落库
- 前端：`npm run build` + tsc

---

## 9. 风险矩阵

| 风险 | 概率 | 影响 | 缓解 |
|------|------|------|------|
| MySQL 未部署 | 低（Helm 已部署）| 中 | DB 降级内存，不阻塞 |
| 迁移脚本错误 | 低 | 中 | 版本化 + 幂等 + 测试 |
| 审批状态迁移丢失 | 中 | 低 | 直接重来（已确认）|
| ClickHouse 误移除 | 低 | 高 | 明确保留时序表 |
| 前端回归 | 低 | 中 | 独立验收 + build 验证 |

---

## 10. 自审清单

- [x] 无 TBD/TODO 占位符
- [x] 架构一致：业务→MySQL，时序→ClickHouse
- [x] 范围聚焦 P1b：审批/审计/Agent/规则/报告/知识库落库 + 前端页面
- [x] rules 表明确参考 ongrid Alert Rules 概念但适配本项目，不照搬
- [x] 无歧义：DAO/表/API/迁移机制均明确
