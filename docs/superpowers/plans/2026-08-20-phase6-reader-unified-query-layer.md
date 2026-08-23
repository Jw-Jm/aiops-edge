# Phase 6 Unified Query Layer — Reader Implementation & Atomic Cutover Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.
>
> **范围（两种工作性质，必须严格区分）:**
> 1. **Reader implementation / readiness（P6.1–P6.4，本计划当前重点）**：实现统一事实查询层 + VM/VLogs reader adapter + feature-switch 就绪 + cutover rehearsal。**此阶段绝不切走任何生产 writer。**
> 2. **Production atomic cutover（P6.5，仅规划，不在此执行）**：writer+reader 在同一受控原子窗口切换。**此阶段由明确 gate 放行，绝不在 reader 未就绪时先切 writer。**

**Goal:** 重构 `ai-apm-query-go` 为统一事实查询层（unified query layer），覆盖 resource/metrics/logs/traces/alerts/topology/kubernetes/changes/knowledge 9 类资源；复用底层 repository/query service，消除 duplicate SQL；统一错误语义（no_data ≠ permission_denied ≠ unavailable ≠ timeout）；为 Phase 6 原子 cutover 做 reader readiness。

**Architecture:** 遵循 R2 §71/§72 原子 cutover 规则。本计划**只做 reader 实现/readiness**（P6.1–P6.4），**不执行 production cutover**（P6.5 仅规划框架 + abort 条件，作为 Gate 6 放行项）。

**Tech Stack:** Go 1.23、`ai-apm-query-go`（标准库 http.ServeMux）、ClickHouse HTTP、VictoriaMetrics /api/v1、VictoriaLogs /select/logsql、MySQL（store DAO 可复用）。

## Global Constraints

- V9.2 §58/§63 统一错误码：no_data / permission_denied / unavailable / timeout 必须可区分。
- V9.2 §71/§72：writer+reader cutover 同一受控原子窗口；**本计划不执行 cutover**。
- 禁止 duplicate SQL：所有 observability 查询经统一 repository。
- V9.2 §5：API 时间 UTC/RFC3339；数据库业务时间 TIMESTAMP(6)。
- 禁止打印 Secret/token/kubeconfig/证书私钥/API key（§90）。
- **禁止 git add / commit / push。**

---

## 阶段 A：Reader Implementation / Readiness（当前执行范围）

## 任务 P6.1 — Reader Inventory + legacy/new mapping（已完成探索）

已在 baseline 盘点（code-explorer）确认 9 类资源现状：

| # | 资源 | 当前实现 | 数据源 | 现状 |
|---|------|---------|--------|------|
| 1 | resource | `ResolveResource`（api/resource.go）+ `biz.ResourceResolver` + store DAO | MySQL | ✅ 规范（非 duplicate SQL） |
| 2 | metrics | `QueryMetrics`（handler.go:804 双轨）+ `QueryRange`（victoriametrics.go） | ClickHouse trace_spans（service 路径）+ VM | ⚠️ 双轨，CH 为默认 |
| 3 | logs | `QueryLogs`（handler.go:1648 双轨）+ `LogAggregate` + `TraceContext` | ClickHouse log_records + VLogs | ⚠️ 双轨，CH 为默认 |
| 4 | traces | `ListTraces`/`TraceDetail`/`TraceContext` | ClickHouse trace_spans + VLogs 关联 | ⚠️ duplicate SQL |
| 5 | alerts | `AlertRules`/`AlertEvents`（alerts.go） | MySQL alert_rules + CH alert_events + VM | ⚠️ duplicate SQL |
| 6 | topology | `GlobalTopology`/`TopologyNodeDetail` | ClickHouse + MySQL fallback | ⚠️ duplicate SQL |
| 7 | kubernetes | `Nodes`/`Pods`/`Deployments`（infrastructure.go） | K8s API 直读 | ✅ 无 SQL |
| 8 | changes | 未实现（走 ProxyAI） | — | ⛔ 待实现 |
| 9 | knowledge | 未实现（走 ProxyAI） | — | ⛔ 待实现 |

**核心问题确认：**
- **无统一 observability repository**：20+ 处 `queryClickHouse` 直拼 SQL（trace_spans 聚合重复 20+ 次）。
- **MySQL store DAO 层可复用**（干净分层）。
- **错误语义未统一**：contract 有 V9.2 错误码，但 reader handler 未接入；no_data/timeout 与 generic 500 混在一起，permission_denied 是字符串 token。

### P6.1 输出
- [ ] 建立 `internal/query/` 统一查询层包骨架（见 P6.2）。

---

## 任务 P6.2 — Unified Query Layer implementation

**Files:**
- Create: `internal/query/` 包（统一 observability repository）
  - `query.go`：`Querier` 接口 + 统一返回类型
  - `clickhouse.go`：ClickHouse 事实查询 repository（trace_spans/service_topology/log_records/alert_events）
  - `vmetrics.go`：VictoriaMetrics repository（RED 聚合 + PromQL 透传）
  - `vlogs.go`：VictoriaLogs repository（log query/aggregate）
  - `errors.go`：统一错误类型（NO_DATA / PERMISSION_DENIED / UNAVAILABLE / TIMEOUT）
  - `source.go`：数据源路由抽象（legacy/new + feature-switch）
- Create: `internal/query/*_test.go`
- Modify: `internal/api/handler.go` 及 20+ 处 handler 逐步改用 `query.Querier`

**Interfaces:**
- Consumes: `Handler` 现有的 `queryClickHouse`/`writeClickHouse`/`vmRangeQuery`/`queryVictoriaLogs`（迁移到 repository 后 handler 不再直拼 SQL）
- Produces:
  - `type QueryError struct { Code ErrorCode; Message string; Retryable bool }`
  - `ErrorCodeNoData` / `ErrorCodePermissionDenied` / `ErrorCodeUnavailable` / `ErrorCodeTimeout`
  - `type Querier interface { ... }`

- [ ] **Step 1: 写失败测试** `errors_test.go` — 验证统一错误码区分（no_data ≠ permission_denied ≠ unavailable ≠ timeout）
- [ ] **Step 2: 运行测试确认失败**（类型未定义）
- [ ] **Step 3: 实现** `errors.go` — 统一 `QueryError` + 错误码 + `HTTPStatusCode()` 映射
- [ ] **Step 4: 运行测试确认通过**
- [ ] **Step 5: 实现** `clickhouse.go` — 将 trace_spans RED/趋势/分位数聚合抽到 repository，handler 不再直拼 SQL
- [ ] **Step 6: 实现** `source.go` — 数据源路由：`SourceLegacy`（读 CH 旧 schema）/ `SourceNew`（读 VM/VLogs）/ feature-switch config（`QUERY_READER_MODE=legacy|new`，默认 legacy）
- [ ] **Step 7: 逐步迁移 handler**（TDD：每迁移一个端点即测试）
- [ ] **Step 8: 全量测试** — `cd ai-apm-query-go && go build ./... && go vet ./... && go test ./...`
- [ ] **Step 9: 记录**

---

## 任务 P6.3 — VM/VLogs + backend reader adapters（feature-switch 就绪）

**Files:**
- Modify: `internal/query/vmetrics.go`、`vlogs.go`（补全 VM/VLogs reader adapter）
- Create: `internal/query/vmetrics_test.go`、`vlogs_test.go`
- Modify: `internal/api/handler.go`（logs/metrics 默认源改由 `QUERY_READER_MODE` 控制）

**Interfaces:**
- Produces:
  - `type VMQuerier interface { Query(ctx, promQL string, ts) (*VMResult, error); QueryRange(ctx, promQL, start, end, step) (*VMRangeResult, error) }`
  - `type VLogsQuerier interface { QueryLogs(ctx, query string, limit int, fields []string) (*VLogsResult, error) }`

- [ ] **Step 1: 写失败测试** `vlogs_test.go` — VLogs 查询结果归一化（含 tenant_id/cluster_id/resource_id 字段）
- [ ] **Step 2: 运行测试确认失败**
- [ ] **Step 3: 实现** VLogs reader adapter（复用 telemetrylabels 校验的字段命名）
- [ ] **Step 4: 运行测试确认通过**
- [ ] **Step 5: 写失败测试** `vmetrics_test.go` — VM RED 聚合查询
- [ ] **Step 6: 运行测试确认失败 → 实现 → 通过**
- [ ] **Step 7: logs/metrics 默认源接入 `QUERY_READER_MODE`（默认 legacy 读 CH，new 读 VM/VLogs）**
- [ ] **Step 8: 全量测试** — Expected: PASS
- [ ] **Step 9: 记录**

---

## 任务 P6.4 — Atomic cutover rehearsal + 4 前置条件

**Files:**
- Create: `docs/AIOPS_PHASE6_PREREQUISITES.md`（4 前置条件 + abort 定义）
- Modify: 部署配置（helm）注入 canonical UUID

### 4 个 cutover 前置条件（锁死）

1. **canonical runtime identity 修正**：
   - [ ] 现有 helm `eventCollector.clusterId="default"`/`tenantId=""` → 改为 Registry 真实 `TENANT_ID=<canonical tenant UUID>`/`CLUSTER_ID=<canonical cluster UUID>`。
   - [ ] ingest/event-collector 部署注入 canonical UUID（不可恢复 default fallback）。

2. **真实多 Pod Lease 竞争验证**（kind-02 单节点用两个普通 Deployment replica 模拟）：
   - [ ] pod-A + pod-B 竞争同一 Lease → exactly one holder。
   - [ ] follower 不启动 K8s watch。
   - [ ] kill leader → follower takeover。
   - [ ] old leader 不继续写。

3. **VM/VLogs new mode 真实 backend smoke**：
   - [ ] `QUERY_READER_MODE=new` 下：VM 收 scoped metrics、VLogs 收 scoped logs。
   - [ ] tenant_id/cluster_id/resource_id labels 正确。
   - [ ] query-api new reader 能读取。

4. **cutover rollback/abort 条件先定义**：
   - [ ] `reader not ready → 不切 writer`。
   - [ ] `writer activation fail → abort`。
   - [ ] `fresh data invisible → abort`。
   - [ ] `scope mismatch → abort`。
   - [ ] `permission semantic mismatch → abort`。

- [ ] **Step 1: 编写 P6.4 前置条件文档**
- [ ] **Step 2: 执行前置条件 1（注入 canonical UUID，部署验证）**
- [ ] **Step 3: 执行前置条件 2（两 Deployment replica Lease 竞争，真实验证）**
- [ ] **Step 4: 执行前置条件 3（VM/VLogs new mode smoke）**
- [ ] **Step 5: 记录**（P6.4 rehearsal 结果，仍不切生产 writer）

---

## 阶段 B：Production Atomic Cutover（仅规划，不在此执行）

## 任务 P6.5 — Production atomic cutover + Gate 6（规划框架）

> **此任务仅在 P6.1–P6.4 全部 PASS 且明确 gate 放行后执行。本计划不执行。**

- [ ] 原子窗口：`switch new writer active → switch new reader active → verify fresh data visible`
- [ ] `stop old writer → stop old reader`
- [ ] `remove old writer adapter → remove old reader adapter → remove fallback`
- [ ] 最终态：`new writer ACTIVE / new reader ACTIVE / old writer ABSENT / old reader ABSENT / old active adapter ABSENT / old physical historical data PRESENT BUT UNREACHABLE`

### Gate 6 判定标准
- [ ] frontend/query/tool fact semantics consistent
- [ ] `no_data != permission_denied`
- [ ] `unavailable != no_data`
- [ ] `timeout != generic network error`

---

## 记录的输出格式（每个任务后）

```text
PHASE: 6
STAGE: READER_IMPLEMENTATION (P6.x) 或 PRODUCTION_CUTOVER (P6.5, 未执行)
STATUS: PASS / NOT_STARTED
GIT_ACTION: NONE
```

---

## 进度记录

### 2026-08-20 — P6.1 完成，P6.2 基础设施完成（reader readiness 第一阶段）

**完成：**
- ✅ **P6.1 Reader Inventory**：9 类资源 mapping 已确认（code-explorer 盘点）。
- ✅ **P6.2 Unified Query Layer 基础设施**（`internal/query/` 新包，TDD 全绿）：
  - `errors.go`：统一 `QueryError` + 4 种错误码（no_data→200 / permission_denied→403 / unavailable→503 / timeout→504），复用 contract 错误码。测试：3 个。
  - `source.go`：`ReaderMode`（legacy/new）+ `ParseReaderMode` + `SourceRouter.ReaderFor(resource)`（traces 固定 CH，logs/metrics 按 mode 路由）。测试：2 个。
  - `clickhouse.go`：`ClickHouseRepo.Query` 统一 CH 查询执行 + 错误语义（NoData/Unavailable/Timeout）。测试：4 个。
  - **QueryMetrics handler 迁移示范**：改用 `h.repo.Query` + `respondQueryError`（no_data→200 空列表）；`newTestHandler` 初始化 repo 指向 mock CH。api 包测试全绿。

**验证：** `go build ./... && go vet ./... && go test ./internal/query/ ./internal/api/` 全绿。
**已知 pre-existing 失败：** `internal/auth` `TestV2RejectsExpiredAndReplay`（trusted context 遗留，与本次改动无关，未触及 auth）。

**后续工作（本轮未完成，作为 P6.2/P6.3 继续）：**
- P6.2 其余 19 处 handler duplicate SQL 迁移（ListServices/ListTraces/TraceDetail/LogAggregate/GlobalTopology/TopologyNodeDetail/queryAlertEvents/DashboardStats 等）→ 统一经 repo。
- P6.3 VM/VLogs reader adapter + `QUERY_READER_MODE` 接入 logs/metrics 默认源。
- P6.4 4 项 cutover 前置条件（canonical identity 注入 / 多 Pod Lease 竞争 / VM/VLogs smoke / rollback-abort 定义）。

**本阶段纪律确认：** 只做 reader implementation/readiness，**未切走任何生产 writer**。VM/VLogs `Enabled()` 恒 false（ModeLegacy）。`GIT_ACTION: NONE`。
