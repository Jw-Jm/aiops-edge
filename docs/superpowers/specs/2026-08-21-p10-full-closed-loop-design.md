# P10 完整闭环设计（Run Persistence / SSE / Recovery — 真实持久化接线）

- STATUS: DESIGN v0.2（评审修订，v0.1 REVISE_REQUIRED）
- Date: 2026-08-21
- Scope: 完整 P10 闭环（含 ManualBoundary rework、query-api 公共 Run 创建鉴权、可靠派发、Plan/Step/Tool/Action 恢复 DAO+端点、public SSE）
- 边界：代码闭环 + TDD（不改生产环境、不部署）。部署验证属后续真实环境 Integration Gate。
- 合同依据：`aiops-agentic-v9.3-deepseek-execution-r4-manual-ai-trigger.md` §七十六 Phase 10 + Gate 10 + P0-3（orchestrator 不直连 DB）。
- 三项核心架构裁决（D1/D2/D3）评审批准，v0.2 只收敛未决结构问题，不重议裁决。

---

## 1. 背景与现状

P10 In-memory MVP 已完成（orchestrator `run_persistence.py` + `sse_stream.py`，19 测试）。query-go `store/ai_runs.go`（AIRunDAO）已建，迁移 `0002_ai_runtime.sql` 已冻结 11 张 AI Runtime 表。

v0.1 评审 REVISE_REQUIRED，v0.2 闭环全部 P0/P1（见 §3 修订清单 + §4 评审闭环）。

### 目标架构（v0.2 修正版）

```
Browser
  → query-api 公共 POST /api/v1/ai/runs      (JWT + tenant + ai.investigate + ManualBoundary)
  → query-api 持久化 Run（权威）
  → query-api 可靠派发：durable outbox + pull/claim，向 orchestrator 派发 RunInvocation
  → orchestrator 经 /internal/v1/control-plane/* (system principal, 独立 capability)
  → query-api persistence owner → MySQL (AIRunDAO + AIRunEventDAO + Plan/Tool/Action 恢复 DAO)

orchestrator 状态层 = RunStateMachine(纯状态机) + PersistentRunRepository(远端提交优先) + RunCache(缓存 query-api 已提交结果)

SSE: Browser → query-api 公共 SSE（JWT 授权）→ query-api 直接从持久化 ai_run_events replay + live-tail
（query-api 是事件持久化/replay owner，不回到 orchestrator 取事件）
```

---

## 2. 设计决策（三项裁决修正，已批准）

### D1. capability：独立内部服务能力域，禁止混入 Tool Registry

`control-plane.*` 不加入 `tool_registry.KNOWN_CAPABILITIES`。按最小权限拆分：
- `control-plane.runs.mutate`  — Run **状态迁移/取消**（对已存在 Run；**不含业务 Run 创建**，见 R1）
- `control-plane.runs.recover` — 重启恢复（ScanUnfinished + 恢复读取）
- `control-plane.events.append` — 事件追加
- `control-plane.events.replay` — 事件重放

每个 control-plane 端点校验：`principal_type==system`、`principal_id==ai-orchestrator`、精确 capability（route→capability 固定映射）、issuer/调用方向、HTTP method/path→action、tenant/run/cluster scope 一致。

**不直接复用** `authorizeInternalQuery`。提取公共验签底座 `verifyInternalServiceToken+Context`，新增 `authorizeInternalControlPlane(capability, principalID, expectedIssuer)`。

### D2. orchestrator 适配层：远端提交优先，内存仅缓存（否决 local-first 双写）

否决"内存 mutation 成功 → 随后 HTTP 同步"。正确模型：
```
orchestrator 计算合法状态变化
  → 携带 command_id + expected_version 请求 query-api
  → query-api CAS + 持久化成功
  → 返回 committed Run
  → orchestrator 用返回结果更新内存缓存
```
三组件：`RunStateMachine`（纯状态机）、`PersistentRunRepository`（HTTP 读写/CAS/command_id 幂等）、`RunCache`（只缓存 query-api 已提交结果；HTTP 失败不推进；响应丢失用同 command_id 重试返回首次结果）。

### D3. event sequence：DB 单一分配者，否决裸 `MAX(sequence)+1`

`ai_runs.last_event_sequence BIGINT NOT NULL DEFAULT 0`（唯一分配者）。append 事务顺序（评审钦定）：
1. **先** `UPDATE ai_runs SET last_event_sequence = last_event_sequence + 1 WHERE run_id = ?`（行锁 Run sequence owner）
2. **再** 检查 `event_id` 是否已存在（`UNIQUE(run_id, event_id)`）——重复请求返回首次结果，**不**先递增再撞唯一键
3. 分配 sequence 并 `INSERT INTO ai_run_events (run_id, sequence, event_id, event_type, payload_json, created_at)`

幂等：稳定 `event_id`（UUID）+ `UNIQUE(run_id, event_id)`。响应丢失重试 → 命中既有 event，不追加。

---

## 3. 修订清单（v0.1 评审 P0/P1 闭环）

### R1（P0）Run 创建路径遵守"仅人工触发" + 可靠派发

- `ai_runs_api.py` POST `/api/v1/ai/runs` 不得作为 Browser 公共入口直接操作内存状态。
- 新流程：Browser → query-api 公共 `POST /api/v1/ai/runs` → JWT + tenant + `ai.investigate` → query-api 创建并持久化 Run（权威）→ **可靠派发**给 orchestrator（见 R6）。
- orchestrator control-plane 仅做 transition/event/recovery，不得创建业务 Run（system principal 不能创建 Run）。
- 复用既有 `run-invocations` 可信入口（main.py:531）已含 ManualBoundary + AuthorizationMatrix（ai.investigate），作为 orchestrator 侧接收 RunInvocation 的入口。

### R2（P0）request_id 幂等真正成立

- `ai_runs` 新增 `UNIQUE(tenant_id, request_id)`。
- **迁移前置**：处理历史空 `request_id`（同租户重复空值会撞唯一键）。预迁移步骤：对 `request_id=''` 的历史行分配确定性占位值（如 `legacy-<run_id>`）或迁移时逐行回填，确保迁移成功。
- `AIRunDAO.Create` 返回 `created/existing`；相同 request_id、不同不可变参数 fail-closed。
- **禁止** `ON DUPLICATE KEY UPDATE run_id = VALUES(run_id)`（当前 DAO 有误）。

### R3（P0）P10 Recovery Gate 需 Plan/Step/Tool/Action 恢复（0003 必须固化结构）

现有表不足以承载完整恢复，0003 必须新增/补列（见 §5.1 migration）：
- `ai_plan_steps`：补 `depends_on`（JSON 数组，完整 DAG 边）、`parameters`（JSON）、`attempt`、`outcome`、`result_ref`、`step_type` 扩展。
- `ai_tool_runs`：补稳定幂等键 `idempotency_key VARCHAR(255)` + `UNIQUE(run_id, idempotency_key)`。
- `ai_actions`：`idempotency_key` 补 `UNIQUE(run_id, idempotency_key)`。
- **control command 持久化表**：新增 `ai_control_commands`（command_id PK、run_id、operation、payload_json、status、idempotency_key、created_at），实现 control command 幂等。
- **一致性快照事务**：恢复端点读取 Run + Plan/Step + ToolRun + Action 必须走同一快照（事务/统一游标），保证重启恢复不重复 Tool/Action。

实现 `AIPlanStepDAO` / `AIToolRunDAO` / `AIActionDAO` / `AIControlCommandDAO`（完整 DAO）。

### R4（P1）`partial` 终态恢复偏差

- Go `AIRunDAO.ScanUnfinished` 排除列表加 `partial`：`NOT IN ('success','partial','failed','regressed','cancelled')`。

### R5（P1）SSE 公共闭环（query-api 直接 replay + live-tail）

- **所有权固定**：query-api 是 `ai_run_events` 持久化 + replay owner。公共 SSE 由 query-api 直接从持久化事件 replay 并 live-tail，**不回到 orchestrator** 取事件（否决 v0.1"Browser→query-api→orchestrator 事件源"）。
- 明确：通知/轮询机制（query-api live-tail 内部事件源 + DB 轮询兜底）、`Last-Event-ID` 格式（sequence）、heartbeat（10-15s）、`oldest_sequence`（保留窗口）、retention 越界响应（明确错误或完整 reload，不 silently skip）。
- 每次重连重新鉴权（JWT + Run tenant/cluster/resource 授权）；禁止 Browser 直接连 orchestrator。
- 内部 events replay 端点（control-plane）用于 orchestrator 恢复，**不能**代替公共 SSE。

### R6（P0）创建后可靠派发协议（durable outbox / pull-claim）

query-api 持久化 Run 后派发 RunInvocation 给 orchestrator 是跨服务双写，必须防"长期留下 created Run"。采用 **durable outbox + 可恢复 pull/claim**：
- query-api 侧 `ai_run_outbox` 表：派发记录（run_id、invocation_id、status=pending|claimed|delivered|expired、dispatch_count、next_retry_at、created_at）。
- 派发流程：Run 持久化成功后写 outbox（同事务）→ dispatcher 定期扫描 pending → POST 可信 RunInvocation 给 orchestrator `/internal/v1/run-invocations` → 成功标记 delivered；失败/超时保留 pending 指数退避重试（`dispatch_count`、`next_retry_at`）。
- `invocation_id` 唯一；orchestrator 侧对重复派发幂等（同 invocation_id 返回首次处理结果）。
- orchestrator 长时间不可用：outbox 行保持 pending，Run 状态不推进（明确"创建后未派发"中间态，绝不伪装成功），由后续 pull/claim 恢复。
- 超时与响应丢失：超时（默认 10s）不视为成功，重试用同 invocation_id；响应丢失重试命中幂等返回首次结果。

---

## 4. 评审闭环登记（v0.1 → v0.2）

| # | 评审项 | 优先级 | v0.2 处置 |
|---|--------|--------|-----------|
| 1 | 创建后可靠派发协议缺失 | P0 | §R6 durable outbox + pull/claim |
| 2 | SSE 数据源所有权矛盾 | P0 | §R5 query-api 直接 replay + live-tail，不回 orchestrator |
| 3 | 恢复模型现有表无法承载 | P0 | §R3 0003 固化 depends_on/parameters/attempt/outcome/幂等键/control command 表 |
| 4 | 权威 Run↔AIRun 映射未定义 | P0 | §6 逐字段映射/状态枚举/不可变字段/API 合同 |
| 5 | 内部 create 与安全裁决冲突 | P1 | 删 control_plane_runs.go 的 create，仅 transition/cancel/unfinished |
| 6 | 不应保留 orchestrator 公共代理 | P1 | ai_runs_api.py 移除公共路由或返回不可用 |
| 7 | 内存 mock 不能证明进程重启 | P1 | §7 真实 MySQL 集成测试 + 真实 orchestrator 进程重启；sqlmock 仅 DAO 单测 |
| 8 | 历史空 request_id 迁移失败 | 评审补充 | §R2 迁移前置回填 |
| 9 | event append 事务顺序 | 评审补充 | §D3 先锁 owner→查 event_id→分配 sequence |

---

## 5. 组件清单

### 5.1 query-go 侧（internal/api + internal/store + migrations）

| 文件 | 内容 |
|------|------|
| `internal/store/migrations/versions/0003_ai_runtime_v2.sql`（新） | request_id UNIQUE + 空值回填、last_event_sequence 列、ai_plan_steps 补列、tool_runs/actions 幂等键 UNIQUE、ai_control_commands 表、ai_run_outbox 表 |
| `internal/store/ai_runs.go`（改） | Create 返回 created/existing（删 run_id 改写）；ScanUnfinished 加 partial；字段补 principal_type/session_id/target/time_range/finished_at/cluster membership |
| `internal/store/ai_run_events.go`（新） | AIRunEventDAO：Append（锁 owner→查 event_id→分配 sequence）、ReplayAfter、LastSequence |
| `internal/store/ai_plan_steps.go`（新） | AIPlanStepDAO：Create/Update/ListByRun（含 DAG/运行态） |
| `internal/store/ai_tool_runs.go`（新） | AIToolRunDAO：Create/Update（幂等 key） |
| `internal/store/ai_actions.go`（新） | AIActionDAO：Create/Update（幂等 key） |
| `internal/store/ai_control_commands.go`（新） | AIControlCommandDAO：Create/Get（command_id 幂等） |
| `internal/store/ai_run_outbox.go`（新） | AIRunOutboxDAO：Insert/Claim/Deliver/ScanPending |
| `internal/api/control_plane.go`（新） | `authorizeInternalControlPlane` + 公共验签底座 |
| `internal/api/control_plane_runs.go`（新） | runs 端点（**transition/cancel/unfinished/get/list**，**不含 create**） |
| `internal/api/control_plane_events.go`（新） | events 端点（append/replay） |
| `internal/api/control_plane_recovery.go`（新） | 恢复端点（plan/tool/action/control command snapshot，一致性快照事务） |
| `internal/api/runs_public.go`（新） | 公共 `POST/GET /api/v1/ai/runs` 鉴权创建（JWT+tenant+ai.investigate+ManualBoundary）+ 写 outbox |
| `internal/api/run_dispatch.go`（新） | outbox dispatcher：扫描→派发 RunInvocation→claim/deliver/retry |
| `internal/api/sse_proxy.go`（新） | 公共 SSE（JWT 授权 + heartbeat + Last-Event-ID + retention + live-tail） |
| `cmd/api/main.go`（改） | 注册 control-plane + public runs + sse + dispatch；handler 注入 DAO |
| `internal/api/handler.go`（改） | Handler 增加全部 DAO 字段 |

### 5.2 orchestrator 侧（ai-orchestrator）

| 文件 | 内容 |
|------|------|
| `run_state_machine.py`（新） | 纯状态转换 + 语义校验（从 run_persistence 提取） |
| `persistent_run_repository.py`（新） | 远端提交优先 HTTP 读写、CAS、command_id 幂等 |
| `run_cache.py`（新） | 只缓存 query-api 已提交结果 |
| `control_plane_client.py`（新） | system principal 签发 + `/internal/v1/control-plane/*` 调用 |
| `run_persistence.py`（改） | RunStateStore 重构为组合 RunStateMachine + 可选持久化后端 |
| `sse_stream.py`（改） | 接 query-api 事件源（作为事件消费端），支持 Last-Event-ID/replay/retention |
| `ai_runs_api.py`（改） | **移除公共 create 路由**（返回不可用/仅保留只读列表引用 query-api） |
| `main.py`（改） | 接线 PersistentRunRepository + RunCache；RunStateStore 单例切换 |

---

## 6. 权威 Run ↔ AIRun 映射（评审 P0-4）

`contracts.Run`（orchestrator 权威，Python）与 `AIRun`（query-go DB）逐字段映射：

| contracts.Run（Python 权威） | AIRun DB 列 | 不可变 | 说明 |
|------------------------------|-------------|--------|------|
| run_id | run_id CHAR(36) PK | 是 | |
| request_id | request_id CHAR(36) | 是 | 唯一域 `(tenant_id, request_id)` |
| tenant_id | tenant_id CHAR(36) | 是 | |
| principal_type | principal_type VARCHAR(32) | 是 | 补列；user/system |
| principal_id | principal VARCHAR(255) | 是 | 由单一 principal 承载 principal_id |
| session_id | session_id CHAR(36) NULL | 是 | 补列 |
| scope_kind | scope_kind VARCHAR(16) | 是 | single_cluster/multi_cluster |
| primary_cluster_id | primary_cluster_id CHAR(36) NULL | 是 | multi 为 NULL |
| intent | intent VARCHAR(255) | 是 | |
| action_mode | action_mode VARCHAR(32) | 是 | |
| target_type | target_type VARCHAR(32) | 是 | 补列 |
| target_resource_id | target_resource_id VARCHAR(512) | 是 | 补列 |
| time_range_start/end | time_range_start/end DATETIME(3) | 是 | 补列 |
| status | status VARCHAR(32) | 否 | 见状态枚举 |
| state_version | state_version BIGINT | 否 | optimistic CAS |
| parent_run_id | parent_run_id CHAR(36) NULL | 是 | |
| created_at | created_at DATETIME(3) | 是 | |
| updated_at | updated_at DATETIME(3) | 否 | |
| finished_at | finished_at DATETIME(3) NULL | 否 | 补列 |
| cluster membership | ai_run_clusters 子表 | 是 | multi 时 >=2 行 |

**状态枚举**：DB 默认 `pending` 与 Python `created` 不一致——统一起点为 `created`。`0003` 将默认改为 `created`（或 DAO Create 显式写 `created`）。迁移时对既有 `pending` 行迁移为 `created`。

**不可变字段集合**：上述"不可变=是"字段在 Create 后不得被 transition/update 改写；相同 run_id 重复 create（不同不可变参数）fail-closed。

**公共 API 请求/响应合同**（`POST /api/v1/ai/runs`）：请求含 tenant_id/cluster_id(或 cluster_scope)/intent/action_mode/service/message；响应含 run_id/request_id/status=created/created_at。JWT 身份映射 principal_id/principal_type；capability=ai.investigate；ManualBoundary 校验 user_explicit。

**control-plane API 合同**：`POST /internal/v1/control-plane/runs/{id}/transition`（body: command_id/expected_version/target）、`.../{id}/cancel`、`GET .../runs/{id}`、`GET .../runs?tenant_id=`、`GET .../runs/unfinished`、`POST .../runs/{id}/events`（body: event_id/event_type/payload）、`GET .../runs/{id}/events?after_sequence=`。

---

## 7. 测试策略（TDD）

- **DAO 单测**：sqlmock（AI 全部 DAO）。**仅用于 DAO 单测**，不用于证明进程重启。
- **handler 单测**：httptest（control-plane/public runs/sse/dispatch）+ 故障注入（HTTP 500/超时/重试）+ 并发（CAS 冲突）+ 跨租户/跨 cluster 拒绝。
- **orchestrator**：`PersistentRunRepository` 用 fake HTTP transport（成功/失败/超时/响应丢失重试）；`RunCache` 失败不推进；`RunStateMachine` 纯状态机；跨租户拒绝。
- **真实 MySQL 集成测试**（评审 P1-3）：起真实 MySQL（orbstack 或本地）→ 执行 `0003` 迁移 → DAO round-trip → **真实 query-api 进程创建 Run → 销毁进程 → 重启 → ScanUnfinished 恢复 → 不重复 Tool/Action**（证明持久性/事务并发/唯一约束/关系恢复）。sqlmock 不替代此层。
- **真实 orchestrator 进程重启**：进程销毁后重启，验证 Run/Event/Plan/Tool/Action 从 query-api 恢复。

---

## 8. 实施计划拆分（评审建议：四个独立可验收计划）

`writing-plans` 拆为 4 个可独立验收的计划：

1. **Plan A — 权威映射 + 迁移 + 公共创建 + 可靠派发**：Run↔AIRun 映射、0003 迁移（约束/列/回填）、query-api 公共 `POST/GET /api/v1/ai/runs` 鉴权创建、outbox dispatcher 可靠派发。
2. **Plan B — control-plane 鉴权 + Run/Event DAO + 远端提交仓储**：authorizeInternalControlPlane、AIRunDAO/AIRunEventDAO、control-plane runs/events 端点、orchestrator PersistentRunRepository/RunCache。
3. **Plan C — Plan/Step/Tool/Action 持久化与重启恢复**：AIPlanStepDAO/AIToolRunDAO/AIActionDAO/AIControlCommandDAO、恢复端点、一致性快照、真实进程重启。
4. **Plan D — Public Control/SSE、授权重放及完整 Gate 10**：公共 SSE proxy（replay+live-tail+heartbeat+retention）、公共 Control 入口、Gate 10 断言 + 真实 MySQL/进程重启集成测试。

每计划独立验收后进入下一个。

---

## 9. 边界（冻结）

- 不改生产环境、不部署、不执行真实 K8s/OpenStack 动作。
- 红线 F1-F5 保持：Agent≠Execution；orchestrator 不直连 DB（经 query-api control-plane）。
- `control-plane.*` capability 不进用户 Tool Registry。
- Execution Production Execution = NOT YET APPROVED。
- Gate 10 完整通过需 Plan/Step/Tool/Action 恢复（R3）就绪 + 真实 MySQL/进程重启集成测试。
