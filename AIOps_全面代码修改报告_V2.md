# AIOps 全面代码修改报告 V2

## 基于 GitHub `main@dedb3ce6e85faefff80920196f4a73d0e3a9df87` 的代码事实、残余缺口与生产验证合同

**文档版本：V2.0**  
**审查日期：2026-08-25**  
**代码仓库：`Jw-Jm/aiops-edge`**  
**代码基线：`dedb3ce6e85faefff80920196f4a73d0e3a9df87`**  
**上一代码基线：`50cbec78cf5f597a1eb6951f27140b368e244ae5`**  
**验证环境边界：当前具备单节点 Kubernetes 验证条件；多节点故障、数据库真实 HA、跨节点存储故障等必须在对应环境验证。**

```text
ARCHITECTURE_DIRECTION = APPROVED
CODE_CONVERGENCE = PARTIAL
A_C_PRODUCTION_CANDIDATE = BLOCKED_BY_RESIDUAL_P0_P1
STAGE_D = SCAFFOLD_ONLY
EXECUTION_MODE = disabled
PRODUCTION_READY = false
```

本 V2 不以提交说明、历史整改文档或测试数量作为“已经生产可用”的依据。所有“已实现”结论以 `dedb3ce6` 源码中存在可执行实现为准；所有“已验证”结论必须明确验证层级。Git 提交说明记录的测试通过情况只能作为仓库已有验证证据，不能替代本次复审没有实际重新执行的测试。

---

# 1. V2 结论

`dedb3ce6` 相比上一代码基线已经完成一次实质性的 Runtime 收敛，新增或加强了 Run Lease、Runtime Commit、MySQL Replay Guard、Recovery Scanner、ToolRun、Evidence 一次消费、Trace SpanSink、Alert Leader、进程角色、LLM Egress Proxy 和独立 Action Executor 等能力。

因此，平台已经不再处于“只有目标设计、核心 Runtime 尚未开始实现”的状态。但源码复审同时确认，**不能把当前版本标记为 IMPLEMENTATION_CONVERGED 或 PRODUCTION_READY**。原因不是总体架构方向错误，而是已有实现中仍存在会破坏并发正确性、安全边界或生产语义的残余缺口。

当前必须优先修复的 P0 为：

1. Browser Cancel 仍走旧的非原子写路径，且 Cancel 未原子失效 Run Lease；
2. Runtime Commit 的精确幂等、payload hash 冲突检测和合法状态迁移校验仍不完整；
3. Run Lease 的 Claim/Renew 仍混用应用时钟，Renew 可在 Lease 已过期后恢复有效期，Claim 响应丢失后无法重获明文 token；
4. Orchestrator Lease-aware wrapper 未真正实施 `LEASE_UNCERTAIN/LOST` 停止规则，且 Commit 重试会生成新的 `commit_id`；
5. Canonical Internal Query 的 ToolRun wrapper 当前没有正确携带真实 `run_id`；`ai_tool_runs.run_id` 虽为 NOT NULL，但空字符串仍可写入，因此会形成无法与真实 Run 建立 Lease/Fencing/Evidence/Recovery 关联的孤儿 ToolRun；
6. Tool 查询执行前没有完成 server-side Run Lease token fencing，结束时也没有统一使用 fencing-aware 完成接口；
7. Evidence consume 端点把 `tool_run_id` 当作 `run_id` 做授权，正常情况下无法通过；Evidence 可接受状态还由调用方传入；
8. Stage D `ai-action-executor` 目前只是安全脚手架，`approved` 模式尚不能代表真实动作已被执行，必须继续保持 `disabled`。

本轮复审后，平台的正确定位应为：

```text
当前：
  已具备面向生产的控制面架构骨架
  已完成若干关键持久化/并发组件
  尚存在关键代码级闭环缺口
  适合继续做受控只读 Investigation 的整改与验证
  不允许开启真实自动处置

目标：
  A-C -> CONTROLLED_AI_INVESTIGATION_CANDIDATE
  D   -> CONTROLLED_ACTION_CANDIDATE（必须单独复审）
```

---

# 2. 代码事实基线

## 2.1 本次确认的主要新增实现

从 `50cbec78` 到 `dedb3ce6`，与生产收敛直接相关的代码主要包括：

```text
ai-apm-query-go/
  internal/api/
    control_plane_lease.go
    mysql_replay_cache.go
    security_replay.go
    toolrun_wrapper.go
    tool_reconciler.go
    control_plane_metrics.go
    alert_engine.go
    run_dispatch.go
    control_plane_runs.go
    internal_query.go
  internal/store/
    ai_run_lease.go
    ai_runtime_commit.go
    ai_evidence.go
    ai_tool_runs.go
    ai_control_commands.go
    alert_runtime_state.go
  internal/store/migrations/versions/
    0004_runtime_convergence.sql
    0005_alert_leader.sql

ai-apm-ingest-go/
  internal/tracesink/clickhouse_span_sink.go

ai-orchestrator/
  control_plane_client.py
  lease_aware_execution.py
  main.py
  orchestrator.py

ai-action-executor/
  main.go

ai-llm-egress-proxy/
  main.go
```

## 2.2 状态分类

本 V2 使用四种状态：

| 状态 | 含义 |
|---|---|
| `IMPLEMENTED` | 源码中已经存在主实现，接口/DAO/迁移能形成基本闭环 |
| `PARTIAL` | 已有实现，但存在源码级缺口，不能按目标能力验收 |
| `BLOCKED` | 关键正确性或安全条件未满足，必须修复后才能进入下一 Gate |
| `BLOCKED_BY_ENV` | 代码设计可继续，但当前单节点/现有环境无法完成所需生产级验证 |

禁止把“有类/有函数/有测试文件”自动解释为 `IMPLEMENTED`；禁止把 Git commit message 中的 “PASS” 自动解释为本次复审实际执行结果。

---

# 3. 冻结的目标总体架构（不是完成声明）

最终架构继续采用“语义编排、运行时控制、只读数据访问、生产写执行”四层隔离，不增加第二套 Run Store、第二套 Tool Store、Redis Runtime SoT 或新的消息队列作为本轮前置条件。

```text
Frontend
  -> Query API
      Browser Trust Boundary
      Runtime Control Plane
      Read-only Tool Execution Plane
      Run Event/SSE
      RunInvocation Outbox
  -> MySQL Runtime SoT

AI Orchestrator
  - Intent / Planner / Agent Runtime
  - Tool Selection
  - Evidence Interpretation
  - Hypothesis / RCA
  - LLM
  -> Query API internal APIs

Trace:
  Ingest -> ClickHouseSpanSink -> ClickHouse trace_spans

Stage D only:
  Query API Approval/Action SoT
    -> ai-action-executor
    -> short-lived scoped credential
    -> Kubernetes WRITE
```

权威定义固定为：

```text
Runtime Semantic Owner       = AI Orchestrator
Runtime Persistence Owner    = Query API
Runtime Persistent SoT       = MySQL
Run Status Authority         = Query API
Run Execution Authority      = DB Run Lease + epoch/token fencing
Tool Selection Owner         = AI Orchestrator
Tool Execution Authority     = Query API Internal Query Plane
Tool Execution Fact          = ai_tool_runs
Evidence Fact                = eligible ToolRun -> ai_evidence
Runtime Event Log            = ai_run_events
RunInvocation Delivery Queue = ai_run_outbox
Trace Persistent SoT         = ClickHouse observability.trace_spans
Schema DDL Owner             = schema-migrator
Production Write Boundary    = ai-action-executor（Stage D，当前 disabled）
```

---

# 4. Persistence Ownership

| 数据 | 语义 Owner | 写入 Authority | SoT |
|---|---|---|---|
| Run 基础属性 | Query API 创建，Orchestrator 使用 | Query API | MySQL `ai_runs` |
| Run `status/state_version` | Orchestrator 提议 | Query API | MySQL `ai_runs` |
| Lease / Runtime wait | Query API | Query API | MySQL `ai_runs` / `ai_run_claims` |
| Runtime Commit 幂等记录 | Query API | Query API | `ai_runtime_commits` |
| Run Event | Orchestrator/Tool 产生语义 | Query API | `ai_run_events` |
| RunInvocation 派发 | Query API | Query API | `ai_run_outbox` |
| Tool 选择 | Orchestrator | 不作为执行事实 | Planner/Event |
| Tool 执行 | Query API | Query API | `ai_tool_runs` |
| Evidence | Orchestrator 提出消费 | Query API 验证并写入 | `ai_evidence` |
| Hypothesis / RCA | Orchestrator | Query API | MySQL |
| Trace | Ingest | Ingest SpanSink | ClickHouse |
| Alert Runtime State | Alert evaluator | Query API worker | MySQL |
| Action Proposal | Orchestrator | Query API | MySQL |
| Approval | 人工/审批域 | Query API | MySQL |
| Action Execution Result | Action Executor 返回 | Query API 应最终持久化 | MySQL |

禁止：

```text
Orchestrator -> MySQL authoritative runtime write
Orchestrator -> Kubernetes WRITE
Frontend -> Orchestrator privileged internal endpoint
Tool selection event == Tool execution proof
LLM output == Evidence fact
```

---

# 5. Run 状态模型：保留 V9.2 状态，不建立第二套 RunStatus

这是 V2 对 V1 最重要的修正之一。

当前代码和 `0004_runtime_convergence.sql` 已经明确选择：**保留 V9.2 业务 RunStatus，Runtime wait/retry 作为正交元数据，不新增第二套持久化 RunStatus。**

唯一业务状态继续是：

```text
created
planning
investigating
awaiting_confirmation
awaiting_approval
executing
verifying
success
partial
failed
regressed
cancelled
```

终态：

```text
success
partial
failed
regressed
cancelled
```

Runtime 等待信息独立保存：

```text
runtime_wait_kind
retry_not_before
retry_attempt
last_failure_code
runtime_metadata_json
```

`runtime_wait_kind` 只用于运行调度，不得演变为第二套状态机。

V2 明确废弃 V1 中以下持久化状态设计：

```text
QUEUED
RUNNING
WAITING_TOOL
WAITING_LLM
WAITING_RETRY
SUCCEEDED
...
```

这些概念可作为局部执行态/内存态命名，但不得再创建 `RunStateV2`、第二状态列或第二状态转换权威。

---

# 6. Run 创建与 RunInvocation Outbox

`AIRunDAO.CreateWithOutbox()` 已经在同一 MySQL 事务内完成 `ai_runs + ai_run_outbox`，这解决“Run 已创建但派发记录缺失”的基础一致性问题。

`ai_run_outbox` 仅用于：

```text
Run 创建
  -> durable RunInvocation
  -> dispatcher claim
  -> POST orchestrator /internal/v1/run-invocations
  -> delivered / retry
```

它不是 `ai_run_events` 的投递队列，也不得扩展成另一套 Runtime Event SoT。

最新 Outbox 已增加 `claimed_by/dispatch_epoch/claim_token/claim_expires_at` 并支持 stale `claimed` reclaim，修复了上一基线中的明确缺陷。

残余 P1：

1. `dispatch_epoch` 当前由应用时钟纳秒值生成，不是数据库自增序列；
2. retry 时间仍有应用时钟参与；
3. Deliver 应同时校验 claim 未过期。

最终要求：

```text
dispatch_epoch = dispatch_epoch + 1  -- DB 内完成
claim_expires_at = DB_NOW + TTL
next_retry_at = DB_NOW + backoff
```

Deliver 必须匹配：

```text
status=claimed
claimed_by
claim_epoch
claim_token
claim_expires_at > DB_NOW
```

否则返回 `OUTBOX_CLAIM_LOST`。

---

# 7. Run Lease：核心路径已实现，但正确性仍有 P0 缺口

## 7.1 已实现

当前 `RuntimeLeaseDAO` 已具备：

```text
lease_owner_id
lease_epoch
lease_claim_id
lease_token_hash
lease_expires_at
heartbeat_at
ai_run_claims
```

并提供 Claim、Renew、Release、ScanRecoveryCandidates、LeaseFencingTx。Runtime Commit 最终事务会调用 `LeaseFencingTx`，方向正确。

## 7.2 P0-LEASE-01：Claim/Renew 仍使用应用时间

源码注释声明 DB time 是权威，但 `Claim()` 和 `Renew()` 实际使用 `time.Now()` 计算 retry 判定、expiry 和 heartbeat。只有部分 recovery/fencing 查询使用 `CURRENT_TIMESTAMP(3)`。

必须统一：Claim、Renew、Cancel、Recovery 的 Lease 有效性和时间生成以 DB time 为准。API response 返回：

```text
server_now
lease_expires_at
lease_remaining_ms
```

## 7.3 P0-LEASE-02：过期 Lease 可以被 Renew 复活

当前 Renew SQL 没有 `lease_expires_at >= CURRENT_TIMESTAMP(3)` 条件。旧 Owner 在 Lease 已过期但尚未被新 Owner Claim 时仍能再次续约。

修复后 RowsAffected != 1 必须返回：

```text
409 RUN_LEASE_LOST
```

## 7.4 P0-LEASE-03：Claim 响应丢失无法精确恢复

当前 Claim 的 `claim_id` 与明文 `lease_token` 由服务端生成，DB 只存 token hash。若成功提交后 HTTP response 丢失，调用方无法恢复原明文 token。

最终合同改为 caller-generated：

```json
{
  "executor_id": "...",
  "claim_id": "caller-generated UUID",
  "lease_token": "caller-generated >=256-bit random",
  "claim_source": "LIVE_INVOCATION|RECOVERY"
}
```

规则：

```text
same run + claim_id + executor + token_hash + active lease
  -> 返回当前同一 Lease metadata，epoch 不变

same claim_id but different executor/token
  -> 409 CLAIM_ID_REUSED

same claim_id exact retry but original lease already expired
  -> 409 CLAIM_ID_EXPIRED

new ownership after expiry
  -> new claim_id + new lease_token
```

---

# 8. Orchestrator Lease 生命周期

`lease_aware_execution.py` 已经实现 claim、background renew、commit 和 context manager 骨架，但仍有两个 P0。

## 8.1 P0-ORCH-LEASE-01：Renew 失败没有传播为 Lease Lost

必须定义本地状态：

```text
ACTIVE
UNCERTAIN
LOST
RELEASED
```

规则：

```text
ACTIVE    -> 允许下一步
UNCERTAIN -> 禁止启动 Planner/Tool/LLM/Commit，只允许 Renew
LOST      -> 禁止新工作；已完成结果不得提交
```

使用服务端 `lease_remaining_ms` 和本地 monotonic clock 计算保守 safe deadline，不能依赖 Pod wall clock。

## 8.2 P0-ORCH-COMMIT-01：commit_id 必须稳定

当前 `commit()` 每次调用会生成新 UUID。Transport retry 必须复用第一次发送前固定的逻辑 `commit_id`，否则服务端幂等无效。

---

# 9. Runtime Commit：已实现基础事务，但仍有 P0

当前 Runtime Commit 已做到：

```text
LeaseFencingTx
Run state_version CAS
Run status update
ai_run_events AppendTx
ai_runtime_commits
same MySQL transaction
```

但当前原子范围主要是 Run status + Event + Commit Idempotency，并没有自动原子包含所有 Evidence/Hypothesis/RCA/Planner 产物。V2 不再虚构这一点。

## 9.1 P0-COMMIT-01：fast-path 没有 payload hash 冲突判断

已有 `(run_id, commit_id)` 时，必须比较 semantic payload hash：

```text
same key + same hash      -> return first result
same key + different hash -> 409 IDEMPOTENCY_KEY_REUSED
```

## 9.2 P0-COMMIT-02：并发同 commit_id exact replay 不完整

目标事务：

```text
1 auth
2 compute hash
3 fast lookup
4 BEGIN
5 SELECT Run FOR UPDATE
6 recheck commit_id inside tx
7 same hash -> first result; different -> 409
8 non-terminal
9 lease fencing DB-time
10 expected state_version
11 legal transition
12 write refs/runtime metadata
13 update Run
14 append events
15 insert first response
16 COMMIT
```

## 9.3 P0-COMMIT-03：Runtime Commit 未校验合法状态迁移

当前 Commit 使用 `TransitionTx`，后者主要是 state_version CAS。必须下沉唯一 `ValidateRunTransition(current,target)`，由 Internal transition、Runtime Commit、Public Cancel、Admin control 共同使用。终态不可复活。

---

# 10. Cancel：当前仍有明确 P0

Internal control-plane Cancel 已增加 expected_version、payload hash、stored response 和事务化 command，属于明显改进。

但 Browser `PublicCancelRun` 仍是旧路径：Get -> record command -> `runDAO.Cancel()`，没有 caller expected_version、exact stored response、统一 Domain transaction、Event 和 Lease invalidation。

必须统一：

```text
Public/Internal/Admin Cancel
  -> RunControlService.CancelTx
```

Cancel 同一事务必须：

```text
SELECT Run FOR UPDATE
validate expected_version/non-terminal
set cancelled
state_version++
lease_epoch++
clear owner/claim/token/expiry/heartbeat
append RUN_CANCELLED
store command exact response
COMMIT
```

Cancel 后旧 Executor 的 Renew/Commit/Tool start 都必须被 Fence。

---

# 11. Shared Replay Guard

MySQL Replay Guard 已存在并解决跨 Pod Trusted Context nonce replay 的核心存储问题；由于 expiry/consume 仍混用应用时间且生产 GC/故障语义未闭合，本 V2 将其整体状态标记为 `PARTIAL`。

必须继续区分：

```text
Security replay nonce != business idempotency
```

业务幂等仍由 commit_id、command_id、claim_id、Tool idem key、action idem key 分别负责。

残余：expiry 判定与 GC 必须统一使用 DB time；DB unavailable 的 privileged internal route 必须 fail-closed。

---

# 12. Recovery

Recovery 已收敛到专用 capability：

```text
control_plane.runs.recover.global
```

`ScanRecoveryCandidates()` 会过滤 active lease 和未来 retry_not_before，方向正确，标记 `IMPLEMENTED`。

恢复 SoT 只能来自 MySQL Run/Plan/ToolRun/Evidence/Event/Action/Approval/runtime metadata，不得依赖旧 Pod 内存、MemorySaver 或本地 SQLite 作为 authoritative Run state。

---

# 13. Canonical Internal Query / Tool Execution Plane

继续复用现有八类 typed route，不新建第二套通用 Tool Gateway：

```text
/internal/v1/query/metrics
/logs
/traces
/alerts
/topology
/kubernetes
/changes
/knowledge
```

Orchestrator 决定“查什么”；Query API 决定“能不能查、查哪个 datasource、使用什么 scope/credential、结果如何持久化”。

Kubernetes query 已开始用 complete/partial/failed 表达数据质量，旧 silent-empty 问题明显改善。

---

# 14. P0：ToolRun wrapper 当前不能作为生产执行事实

## 14.1 P0-TOOL-01：RunID 没有进入 ToolRun

`newToolRunFromRequest()` 当前明确设置 `RunID: ""`。`ai_tool_runs.run_id` 虽为 `NOT NULL`，但 MySQL 会接受空字符串，因此该路径不是必然插入失败，而是可能成功写出 `run_id=''` 的孤儿 ToolRun。这样的记录无法与真实 Run 做 Lease/Fencing、Recovery 或 Evidence 一致性关联，属于更隐蔽的 P0 正确性缺陷。Query API 必须在执行任何数据源 I/O 前校验 `run_id` 为规范 UUID、Run 存在且 tenant/cluster 与 signed context 一致。
同时应在 migration 中增加可执行的完整性约束（至少禁止空字符串；若采用 CHECK，必须确认目标 MySQL 版本实际强制执行），避免仅依赖应用层校验。

Internal Query request 必须增加：

```json
{
  "run_id": "...",
  "tool_run_id": "...",
  "idempotency_key": "...",
  "executor_id": "...",
  "lease_epoch": 12,
  "lease_token": "...",
  "query_window_start": "...",
  "query_window_end": "..."
}
```

Query API 必须用 signed Run context 约束 run/tenant/cluster，body 只能做一致性匹配。

## 14.2 P0-TOOL-02：执行前没有 Lease token fencing

任何真实 datasource I/O 之前必须检查：

```text
Run non-terminal
owner == executor
epoch match
token hash match
lease not expired
tenant/cluster match
read_only tool
budget
idempotency
```

## 14.3 P0-TOOL-03：结束 ToolRun 没有统一使用 fencing-aware 接口

所有 finish 必须走 `FinishToolRunWithFencing` 或同等事务逻辑。Lease 已丢失的晚到结果只能审计，必须 `eligible_for_evidence=false`。

## 14.4 P0-TOOL-04：Tool retry 没有返回第一次完整结果

规则固定：

```text
terminal success/no_data/partial -> return stored first ToolResultEnvelope
running, not stale             -> 409 TOOL_IN_PROGRESS
same idem + different args_hash -> 409 TOOL_INVOCATION_ID_REUSED
stale running                  -> reconciler convergence
```

Idempotency 唯一域为 `(run_id,idempotency_key)`。

---

# 15. ToolResultEnvelope 数据质量合同

目标结构：

```json
{
  "tool_run_id": "...",
  "run_id": "...",
  "quality": "complete|partial|failed",
  "empty": false,
  "truncated": false,
  "returned_count": 0,
  "original_count": 0,
  "query_window_start": "...",
  "query_window_end": "...",
  "observed_at": "...",
  "source_errors": [],
  "digest": "...",
  "data": {}
}
```

核心：

```text
empty != source failure
partial != complete
truncated != unbounded-complete
timeout != no_data
```

当前超过 2MiB 后直接按 byte slicing 截断 JSON，会产生非法 JSON，这是 P0。

A-C 固定规则：优先按 typed item 数量限制；序列化仍超限则 `RESULT_TOO_LARGE`。只有能在 JSON item boundary 做 deterministic truncation 时才允许 `truncated=true`。

`count` 不再由 wrapper 猜测顶层 JSON 是否数组，各 typed handler 明确给出 `returned_count/original_count`。

V2 不把 MinIO 作为修复 B1 的前置条件。大 Evidence Object Storage 以后通过 StorageAdapter 独立演进。

---

# 16. Observation Window：必须可复现

Run 已有 time range 字段。相对时间语义在 ToolRun 创建前必须冻结为绝对 UTC：

```text
analysis_anchor_at
query_window_start
query_window_end
observed_at
```

同一 ToolRun transport retry 不重新计算时间窗；用户显式刷新才创建新 ToolRun。

---

# 17. Evidence：DAO 方向正确，但 API 当前有 P0 bug

`ConsumeToolRunAsEvidence()` 已经使用事务锁 ToolRun，验证 eligible/unconsumed/run/tenant/cluster，写 Evidence 并设置 `evidence_consumed_at`，这个模型正确。

## 17.1 P0-EVIDENCE-01：授权把 tool_run_id 当 run_id

当前 `/tools/{toolRunID}/evidence/consume` 调用 `authorizeControlPlaneForRun(..., toolRunID)`，而该函数需要 run_id。正常情况下无法通过。

修复顺序：

```text
load ToolRun minimal metadata
-> derive actual run_id
-> authorize actual Run
-> verify tenant/cluster
-> BEGIN
-> ToolRun FOR UPDATE
-> recheck
-> Evidence consume
```

## 17.2 P0-EVIDENCE-02：allowed statuses 不能由调用方决定

当前 body 的 `allowed_statuses` 必须删除。Evidence eligibility 是 Query API server-owned policy。

默认：complete success/no_data 可进入 Evidence；partial 若允许使用，必须保留 `evidence_quality=partial + source_errors`，RCA 明确披露不完整证据。

---

# 18. Tool Reconciler

已存在 ScanExpiredRunning、Run->ToolRun lock order、timeout convergence、eligible=false 等基础能力，方向正确。

多副本可同时发现候选，但事务 recheck 只能一个完成收敛。Stage D 写动作绝不能套用 read-only Tool 的“timeout 后可安全再执行”逻辑。

---

# 19. Event Store 与 SSE

`ai_run_events` 继续作为 Run durable event log。SSE 从 Query API 按 tenant/run 鉴权后，以 Run 内 sequence replay + DB live-tail，不依赖 Pod-local subscriber 正确性。

游标固定为 `sequence`；event_id 用于事件去重/审计。

历史 cursor 不再可用时应返回 `EVENT_CURSOR_EXPIRED` 和 earliest available sequence，让客户端重新加载 Run snapshot。

---

# 20. Chat 与 Investigation

前端已修复 `cluster_id=all` 默认发送问题，只允许 concrete canonical cluster 发 Chat。

V2 产品边界继续冻结：普通 Chat 不固定采集实时指标/日志/Trace/K8s；需要实时事实/RCA 的请求必须 `investigation_required -> 用户显式开始调查 -> 创建 Run`。

但当前 `AiChat.tsx` 仍保留 `executeSuggestion/finalReport` 和 suggestion/execresult/report 等旧 UI 模型，说明 Legacy 产品入口尚未完全清理。

A-C 要求：Chat 不直接触发真实 Action；UI Tool Activity 只能展示真实 ToolRun/Event，不用图节点推断冒充真实工具调用。

---

# 21. Orchestrator 责任边界

目标仍为 Intent/Planner/Tool Selection/Evidence Interpretation/Hypothesis/RCA/LLM，不拥有 Runtime DB 写权威和生产 K8s 写凭据。

最新代码已经加强 Remote Persistence 和 Lease-aware 主线，但 `orchestrator.py` 仍保留若干历史 local checkpoint/direct-looking tool 模块。必须通过 Secret 清单、静态扫描和 E2E 证明 Orchestrator Pod 不含 MySQL authoritative credential、观测数据源直连凭据和 K8s write kubeconfig。

---

# 22. Trace SoT：实现已存在，生产 Gate 尚未完成

`ClickHouseSpanSink` 已实现并固定写 `observability.trace_spans`，V1 的 Trace A/B 二选一取消。

唯一路径：

```text
OTLP/DeepFlow input
-> ingest
-> ClickHouseSpanSink
-> trace_spans
-> TraceRepository
-> Internal Query
-> ToolRun
-> Evidence
```

残余 P1：`span_dedup_key` 不是唯一约束，需要验证 ReplacingMergeTree/查询层在 merge 前后对重复 Span 的逻辑一致性；SpanSink failure 的 readiness/backpressure/WAL 行为也要真实验证。

---

# 23. Query API 进程角色：代码支持，Helm 尚未真正拆开

binary 已支持 `api/run-dispatch/alert-eval`，但当前 Helm Query API Deployment 没有按角色拆成独立 workload；默认 `api` 兼容模式仍会同时启动后台工作。

状态：

```text
process-role code support = IMPLEMENTED
production role separation = NOT_DEPLOYED
```

生产 role 语义固定为：

```text
api          -> HTTP/SSE only
run-dispatch -> Outbox + Tool Reconciler
alert-eval   -> Alert evaluator
all          -> 仅本机兼容模式
```

Helm 生成三个独立 Deployment。

迁移时必须同时修改 binary role 语义：当前 `--role=api` 仍会启动 dispatch 和 alert，因此生产拆分不能仅新增两个 Worker Deployment 后继续让 API Deployment 使用现语义，否则会重复启动 Worker。最终固定为 `api=HTTP/SSE only`、`run-dispatch=worker only`、`alert-eval=worker only`、`all=本机兼容`，并为非法 role 启动时 fail-fast。

---

# 24. Alert Leader：部分实现

新增 leader/state 表是正确方向，但仍有 P1：

1. Acquire/Renew 最终应统一 DB time；
2. expired leader 不得 Renew 复活；
3. breach streak 计算必须从持久化 `BreachStreak` 恢复，而不是 leader 切换后从本地 map 重新从 0 开始；
4. production 不得在 leader DAO 缺失时 fail-open 执行 alert evaluation。

必须演练 leader kill -> new leader -> streak/cooldown 连续且不重复 Webhook。

---

# 25. LLM Egress：当前只是部分实现

生产目标固定：Orchestrator -> `ai-llm-egress-proxy` -> approved provider，Provider API Key 只在 Proxy。

当前代码已有 proxy scaffold，但 Helm 默认未启用；proxy token 可为空；Orchestrator 仍存在直接 key/base URL 路径；provider path 拼装需要真实 E2E 核实；production default-deny egress 未证明已全局开启。

因此：

```text
LLM proxy code = PARTIAL
provider key isolation = NOT_PROVEN
```

最终 Proxy 必须 token/identity 缺失时 startup fail，未知 provider/path fail-closed，禁止 arbitrary URL；Orchestrator Secret 中不得出现 provider key。

---

# 26. Kubernetes TLS 与 NetworkPolicy

生产固定 `K8S_INSECURE_SKIP_VERIFY=false`，使用 CA/kubeconfig CA bundle。单节点本机 profile 可显式允许自签，但不能作为 production default。

NetworkPolicy 目标是 default deny；Orchestrator 只允许 Query API、LLM Proxy、DNS；Action Executor 的写网络和 RBAC 独立。

---

# 27. Schema / Migrator

继续保持 schema-migrator 为唯一 DDL Owner，Runtime app 不执行 DDL。

新增 migration 必须覆盖 caller claim id/token contract、Cancel lease invalidation、ToolRun run_id/args_hash、server fencing、Evidence policy、Action durable state 等。

采用 expand -> migrator -> rolling app upgrade -> 后续 contract，不额外发明 exact schema version 等于 binary version 的强约束。

---

# 28. MySQL 是控制面一致性中心

当前 MySQL 承载 Run、state version、lease、claim、commit、command、replay、outbox、tool run、evidence、approval/action metadata、alert leader/state。因此它是 Control Plane consistency point。

当前 Helm 主要仍是单实例语义，不等于生产 HA。生产必须具备 single writable primary、InnoDB durable transaction、UTC、backup、PITR、restore drill、failover、checksum。

真实 MySQL primary failover 未验证前标记 `BLOCKED_BY_ENV`。

---

# 29. Stage D Action Executor：当前只允许 Scaffold

独立 `ai-action-executor` 是正确安全边界，但当前实现仍不是生产真实执行：认证可以是可选 shared token；signed execution context 未完整验证；目标 reread/TOCTOU 是模拟；Credential Broker 未真正接通；approved 路径没有真实 Kubernetes mutation；结果主要进程内；rollback/reconcile/verify 不是真实闭环。

因此：

```text
STAGE_D = BLOCKED
EXECUTION_MODE = disabled
```

真正 D Gate 必须完成 immutable Action/Approval binding、signed context、short-lived credential、actual target reread、UID+resourceVersion precondition、real write、durable idempotency、UNKNOWN reconcile-before-retry、rollback、post verification、audit。

---

# 30. Control Plane 自观测

当前已有 `control_plane_metrics.go` 基础。生产至少监控 Run、Commit、Lease、Recovery、Outbox、Tool、Evidence、Replay、SSE、Alert、Trace Sink、LLM 和 Action 的 success/error/latency/backlog/fencing 指标。

日志 correlation 使用 request_id/run_id/commit_id/command_id/claim_id/lease_epoch/tool_run_id/evidence_id/event_id/action_id/trace_id；严禁日志 lease token、K8s credential、provider API key、Authorization。

---

# 31. 当前测试证据能证明什么

Git commit 记录声明 query-go/ingest/orchestrator 和 real MySQL integration PASS。本次 V2 复审没有重新执行这些测试，因此只记为 `REPOSITORY_RECORDED_PASS`。

已阅读的 MySQL integration test 能证明部分 Claim/Renew fencing、RuntimeCommitDAO insert/get/duplicate、Alert leader/state 基础行为；不能充分证明 expired renew、claim response loss、HTTP Runtime Commit exact replay、commit payload mismatch、terminal resurrection、public cancel、ToolRun/Evidence、clock skew 等关键场景。

---

# 32. 必须新增的测试矩阵

## Runtime

| ID | 场景 | 预期 |
|---|---|---|
| R-01 | 双 executor Claim | 仅一个成功 |
| R-02 | Claim 成功响应丢失 | exact retry 恢复同 lease |
| R-03 | expired claim_id retry | CLAIM_ID_EXPIRED |
| R-04 | expired Lease Renew | RUN_LEASE_LOST |
| R-05 | old epoch/token Commit | fenced |
| R-06 | Commit response lost | same ID first result |
| R-07 | same commit ID different payload | IDEMPOTENCY_KEY_REUSED |
| R-08 | concurrent same commit ID | exact first result |
| R-09 | illegal transition via Commit | rejected |
| R-10 | terminal resurrection | rejected |
| R-11 | Cancel vs Commit race | 单一合法最终事实 |
| R-12 | Browser Cancel replay | exact |
| R-13 | Cancel 后 old owner | renew/commit/tool 全 fenced |

## Tool/Evidence

| ID | 场景 | 预期 |
|---|---|---|
| T-01 | valid run_id ToolRun | insert success |
| T-02 | missing run_id | fail-closed |
| T-03 | old lease Tool start | pre-I/O reject |
| T-04 | Tool response loss retry | no duplicate / first result |
| T-05 | same idem different args | 409 |
| T-06 | execution loses lease | stale/no evidence |
| T-07 | partial K8s source | partial + source errors |
| T-08 | all upstream fail | failed |
| T-09 | true empty | complete + empty |
| T-10 | oversize | valid truncation or RESULT_TOO_LARGE |
| E-01 | eligible consume | exactly once |
| E-02 | second consume | rejected/idempotent |
| E-03 | tenant/cluster mismatch | rejected |
| E-04 | caller status escalation | rejected |
| E-05 | fenced ToolRun | no evidence |

## Infra

需要真实验证 Trace duplicate/outage、SSE restart、Outbox crash/reclaim、Alert leader failover、LLM Proxy deny/key isolation、K8s TLS、default deny。

---

# 33. 本机单节点可验证范围

可以验证：双 Orchestrator/Query API 竞争、Lease/Commit/Replay/Outbox/ToolRun/Evidence/SSE/Alert leader/Trace/NetworkPolicy/LLM Proxy/Action disabled gate。

不能证明：worker node failure、跨节点 PVC/RWO、MySQL primary failover、multi-AZ、真实 network partition、multi-DC。统一标记 `BLOCKED_BY_ENV`。

---

# 34. V2 整改任务顺序

## Phase A0：控制面 P0

1. Public Cancel 统一到 `RunControlService.CancelTx`；
2. Cancel 原子 `lease_epoch++` + clear lease + Event；
3. Runtime Commit payload hash exact replay + in-tx recheck；
4. Runtime Commit legal transition / terminal protection；
5. caller claim_id/token + DB-time + expired Renew reject；
6. Orchestrator ACTIVE/UNCERTAIN/LOST + stable commit_id。

Gate：`RUNTIME_CORRECTNESS_CANDIDATE`。

## Phase B1：Tool/Evidence

1. Internal Query 增加 run_id + lease token；
2. datasource I/O 前 server fencing；
3. `(run_id,idempotency_key,args_hash)` exact Tool idempotency；
4. finish fencing；
5. valid ToolResultEnvelope 和 semantic limits；
6. Evidence authorization + server-owned eligibility。

## Phase B2：Chat/Investigation

1. 普通 Chat 无固定实时采集；
2. live diagnosis -> explicit Run；
3. 封死 Chat executeSuggestion 写旁路；
4. UI Tool activity 只展示真实 ToolRun/Event；
5. 清理 Orchestrator direct legacy paths。

## Phase C：生产环境

1. Trace dedup/failure E2E；
2. Query API Helm role split；
3. Alert DB-time + persisted streak；
4. LLM Proxy path/auth/key isolation；
5. production egress default deny；
6. K8s CA；
7. MySQL backup/PITR/failover drill。

## Phase D：独立受控执行

保持 disabled，按 immutable Action、signed context、Credential Broker、real reread、TOCTOU、write、unknown reconcile、rollback、verify、audit 顺序单独推进。

---

# 35. 发布禁止条件

A-C 任一存在则禁止生产候选：Public Cancel 未统一；Cancel 未 Fence Lease；expired Lease 可 Renew；Claim response-loss 无 exact retry；Commit same-key-different-payload 未拒绝；Commit 可非法迁移/终态复活；ToolRun 无 run_id；Tool I/O 前无 token fencing；Tool finish 无 fencing；Tool exact replay 未实现；raw-byte JSON truncation；Evidence authorization bug；Evidence policy caller-owned；Orchestrator 仍有 authoritative DB/K8s write；Chat 绕 Run 做 live RCA；production TLS/default-deny/key isolation 未完成。

Stage D 任一真实执行条件未完成，`EXECUTION_MODE` 必须保持 disabled。

---

# 36. 错误码合同

```text
RUN_STATE_CONFLICT
RUN_LEASE_HELD
RUN_LEASE_LOST
RUN_LEASE_FENCING
RUN_RETRY_BACKOFF
CLAIM_ID_REUSED
CLAIM_ID_EXPIRED
IDEMPOTENCY_KEY_REUSED
TOOL_IN_PROGRESS
TOOL_INVOCATION_ID_REUSED
TOOL_LEASE_LOST
TOOL_RESULT_STALE
RESULT_TOO_LARGE
EVIDENCE_NOT_ELIGIBLE
CONTEXT_REPLAYED
EVENT_CURSOR_EXPIRED
EVENT_CURSOR_INVALID
ACTION_PRECONDITION_CHANGED
ACTION_UNKNOWN
ACTION_EXECUTION_DISABLED
```

禁止自由文本驱动业务逻辑。

---

# 37. AI 编码任务模板

```text
Task ID:
Phase:
Priority:

Code facts:
Target:
Files allowed:
Files prohibited:
Existing API reused:
New API:
DB migration:
Concurrency contract:
Idempotency contract:
Auth/scope contract:
Error codes:
Failure cases:
Rollback:
Unit tests:
Contract tests:
Integration tests:
E2E tests:
Local single-node verification:
Production-only verification:
Done definition:
```

禁止新建第二 Run Store/RunStatus/Tool Authority，禁止恢复 Orchestrator Runtime MySQL/K8s write，禁止用内存 map 充当分布式权威，禁止把 Mock 成功当真实成功。

---

# 38. 独立复审结论

V2 生成后按五个问题反向复审：是否把 commit message 的“完成”误认为源码闭环；是否再次设计第二 RunStatus；是否把已有实现误写成完全缺失；是否把 scaffold 误写成 production-ready；是否出现同一职责两个权威入口。

复审结论：总体架构无需推倒重来。以下主干应保持：Query API 是 Trust/Runtime/Persistence authority；MySQL 是 Runtime SoT；Orchestrator 是 semantic reasoning owner；Internal Query 是 read-only Tool execution authority；`ai_tool_runs` 是 Tool fact；`ai_evidence` 是 verified evidence；ClickHouse `trace_spans` 是 Trace SoT；Action Executor 是 Stage-D-only write boundary。

但 `dedb3ce6` 不能标记为实施完全收敛。当前最高优先级不是增加新 Agent，而是修复 Cancel、Commit、Lease、ToolRun、Evidence 五条正确性主线。

应保留并原位修复而不是重做的能力：V9.2 RunStatus、`ai_runs`、`ai_run_events`、`ai_run_outbox`、control-plane route family、`/internal/v1/query/*`、`ai_tool_runs`、MySQL Replay Guard、Recovery global capability、ClickHouseSpanSink、Alert leader tables、Action Executor service boundary、LLM Egress Proxy service boundary。

最终准入状态必须按顺序：

```text
RUNTIME_CORRECTNESS_CANDIDATE
  -> CONTROLLED_AI_INVESTIGATION_CANDIDATE
  -> CONTROLLED_ACTION_CANDIDATE
```

当前尚未达到第一个状态。

---

# 39. 最终执行清单

## P0

- [ ] Public Cancel 事务统一
- [ ] Cancel lease_epoch++ / clear lease / event
- [ ] Runtime Commit hash conflict + concurrent exact replay
- [ ] Runtime Commit legal transition / terminal protection
- [ ] stable Orchestrator commit_id
- [ ] Lease 全 DB-time
- [ ] expired Renew fail
- [ ] caller claim_id/token exact retry
- [ ] LEASE_UNCERTAIN/LOST 执行阻断
- [ ] ToolRun 使用非空规范 run_id，并校验真实 Run/tenant/cluster
- [ ] Tool pre-I/O token fencing
- [ ] Tool finish fencing
- [ ] Tool exact result replay
- [ ] ToolResult semantic truncation
- [ ] Evidence consume authorization 修复
- [ ] Evidence policy server-owned
- [ ] Stage D 保持 disabled

## P1

- [ ] Outbox DB monotonic epoch/time
- [ ] Shared Replay expiry/GC 全 DB-time
- [ ] Alert DB-time + persisted breach streak
- [ ] Query API Helm role split
- [ ] Trace duplicate/failure/tenant E2E
- [ ] LLM Proxy path/auth/key-isolation E2E
- [ ] production egress default deny
- [ ] K8s TLS CA
- [ ] Orchestrator legacy direct capability 清理
- [ ] Control Plane metrics/SLO
- [ ] MySQL backup/PITR/failover drill

## BLOCKED_BY_ENV

- [ ] 多节点 worker failover
- [ ] MySQL real primary failover
- [ ] 跨节点 PVC/RWO failure
- [ ] network partition
- [ ] multi-AZ/DC

---

# 40. 源码证据索引

本节用于防止后续 AI 编码只读取结论而不回看实现。状态以 `main@dedb3ce6e85faefff80920196f4a73d0e3a9df87` 为准。

| 架构域 | 主要源码/配置 | V2 判定 |
|---|---|---|
| Run/Outbox | `ai-apm-query-go/internal/store/ai_runs.go`、`ai_run_outbox.go`、`internal/api/run_dispatch.go` | Run+Outbox 原子创建已实现；Dispatch 仍需 DB-time/epoch 收口 |
| Run Lease | `internal/store/ai_run_lease.go`、`internal/api/control_plane_lease.go` | 核心路径已实现；DB-time、expired renew、claim exact retry 为 P0 |
| Runtime Commit | `internal/store/ai_runtime_commit.go`、`internal/api/control_plane_lease.go` | 基础事务已实现；payload hash exact replay、in-tx recheck、合法迁移为 P0 |
| Cancel | `internal/api/control_plane_runs.go`、`runs_control.go`、`internal/store/ai_runs.go` | Internal 路径增强；Public Cancel 仍未统一且未原子 Fence Lease |
| Shared Replay | `internal/api/mysql_replay_cache.go`、`security_replay.go` | 共享 MySQL Guard 已实现；time/GC 仍需收口 |
| Recovery | `internal/api/control_plane_recovery.go`、`control_plane_runs.go`、`internal/store/ai_run_lease.go` | global capability + recovery candidates 已实现 |
| Internal Query | `internal/api/internal_query.go`、`internal/query/*` | canonical typed read plane 已形成；K8s partial/error 语义已增强 |
| ToolRun | `internal/api/toolrun_wrapper.go`、`internal/store/ai_tool_runs.go` | P0：真实 run_id、pre-I/O fencing、finish fencing、exact replay 未闭合 |
| Evidence | `internal/store/ai_evidence.go`、`internal/api/control_plane_tools.go`、`control_plane_lease.go` | DAO 一次消费方向正确；API authorization/policy 为 P0 |
| Tool Reconciler | `internal/api/tool_reconciler.go` | 已实现 read-only Tool 超时收敛骨架 |
| Trace | `ai-apm-ingest-go/internal/tracesink/clickhouse_span_sink.go`、ClickHouse migration | ClickHouse Trace SoT writer 已实现；真实重复/故障 E2E 未验证 |
| Alert | `internal/api/alert_engine.go`、`internal/store/alert_runtime_state.go` | Leader/RuleState 已实现；DB-time/leader failover streak 为 P1 |
| Query API Role | `cmd/api/main.go`、Helm `templates/query-api/deployment.yaml` | binary 支持 role；生产 Helm 尚未物理拆分 |
| LLM Egress | `ai-llm-egress-proxy/main.go`、Orchestrator LLM 代码、Helm values | Proxy scaffold 存在；强认证、强制路由、key isolation 未证明 |
| Stage D | `ai-action-executor/main.go`、`ai-orchestrator/execution_adapter.py`、`credential_broker.py` | 仅 Scaffold；真实 mutation/credential/TOCTOU/durable result 未闭环 |
| Frontend Chat | `observability-frontend/src/pages/ai/AiChat.tsx` | concrete cluster 发送约束已增强；legacy suggestion/execute UI 仍需按产品边界继续清理 |
| Schema | `internal/store/migrations/versions/0002_ai_runtime.sql`、`0004_runtime_convergence.sql`、`0005_alert_leader.sql` | migrator 路径成立；剩余 P0/P1 需要新增 migration |

复审修订说明：V2 初稿曾把 `ToolRun.RunID=""` 描述成“会因 `NOT NULL` 必然插入失败”。源码二次核对后已修正：空字符串不是 SQL NULL，当前更可能形成 `run_id=''` 的孤儿 ToolRun，因此必须在应用层和 schema 层同时拒绝。

---

# 41. 文档最终状态

本 V2 基于 GitHub `main@dedb3ce6e85faefff80920196f4a73d0e3a9df87` 源码重新生成，取代 V1 中以下已不准确内容：Runtime Lease/Shared Replay/Trace SpanSink/Alert leader/Action Executor “完全未实现”，以及 `QUEUED/RUNNING/WAITING_*` 作为唯一持久化 RunStatus 的设计。

同时拒绝以下过度乐观表述：V9.3 已全部整改完成、Runtime correctness 已生产验证、ToolRun/Evidence 已闭环、Stage D 已可 approved 执行、LLM provider key 已完全与 Orchestrator 隔离、Query API worker 已完成物理角色拆分。

```text
DESIGN = APPROVED
CODE = SUBSTANTIALLY_IMPROVED_WITH_RESIDUAL_P0
A_C_CODE_GATE = BLOCKED_UNTIL_P0_CLOSED
A_C_PRODUCTION_CANDIDATE = BLOCKED_UNTIL_P0_P1_AND_ENV_GATES_CLOSED
D = DISABLED_AND_REQUIRES_INDEPENDENT_REVIEW
PRODUCTION = NOT_READY
```
