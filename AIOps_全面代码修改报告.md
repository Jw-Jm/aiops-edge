# AIOps 全面代码修改报告

## 生产收敛实施合同（当前代码对齐版）

文档状态：**ARCHITECTURE_APPROVED_WITH_REQUIRED_AMENDMENTS / NOT_PRODUCTION_READY**  
代码事实基线：GitHub `Jw-Jm/aiops-edge`，`main` HEAD `50cbec78cf5f597a1eb6951f27140b368e244ae5`（2026-08-24）；涉及代码实现的判断均按该提交及其当前目录、迁移、契约与部署文件复核。  
本地限制：当前会话只挂载本 Markdown，源码未挂载到执行容器，因此本轮不能在本地直接运行仓库测试；测试通过事实仅采用该提交链中可核验的仓库验证记录，并与“本轮未执行”明确区分。  
适用范围：Run/Control Plane Persistence、Runtime Commit、Lease、Replay、Canonical Internal Query、Tool Run、Evidence、Event/SSE、Trace、Query API 运行角色、Alert Evaluation、MySQL HA/DR、网络与凭据、Schema、Agent 主链、前端入口、Stage D Action Executor、平台自观测、测试与发布门禁。  
验证环境目标：本机单节点 Kubernetes；允许通过两个进程/测试 Deployment 验证并发与 fencing，但不把单节点结果表述为生产多节点 HA。  
阶段 A-C 目标：`FOUNDATION_LOCAL_VERIFIED`；生产候选目标：`CONTROLLED_AI_INVESTIGATION_CANDIDATE`。  
Chat 专项源码核对：**已完成**。最终判定为“当前实现保留固定实时采集；A-C 目标删除普通 Chat 固定实时采集，实时事实查询统一进入 Investigation Run”。  
本轮架构复审：**已完成**。新增结论已按当前 HEAD 代码再次验证：Query API 后台 Worker 多副本边界、Alert 进程内状态、Recovery 全局扫描、Kubernetes internal query 静默吞错、ToolResult 数据质量与时间窗、Trace SoT、Stage D 执行物理边界、MySQL 控制面 HA/DR 与平台自观测均纳入本合同。

### 文档使用规则

1. **真实代码优先于历史方案假设。** 本文已将当前代码中已经存在的能力标记为“保留/增强”，不再重复设计第二套实现。
2. **不得新建平行权威。** 现有 `ai_runs`、`ai_run_events`、`ai_run_outbox`、`ai_tool_runs`、`ai_actions`、`ai_approval_decisions` 以及 `/internal/v1/control-plane/*`、`/internal/v1/query/*` 都应原位演进。
3. **跨语言冻结契约优先兼容。** `ai-orchestrator/contracts.py`、Go `internal/contract`、前端 `api/contracts.ts` 已形成跨语言契约；除非确有必要，不通过另起一套状态名、Context 类型或 API namespace 来规避迁移。
4. **“已实现”不等于“生产完成”。** 例如 DB-backed SSE replay、Run+Outbox 原子创建、远端 Run persistence 已存在，但 Lease、Runtime Commit、共享 Replay、防止生产 remote 模式读回退等仍是待整改项。
5. 后续 AI 编码任务发现本文与仓库事实再次不一致时，应先修订本文并说明代码证据，再继续实现。

---

## 1. 总体结论与当前实施边界

当前代码已经完成了比历史审阅基线更多的控制面能力，不能再按“Run/Event/Outbox/远端持久化均待建设”处理。经当前仓库复核，以下能力属于**已实现基线，应保留并增强**：

- Query API 已持久化 `ai_runs`、`ai_run_events`、`ai_run_outbox`、`ai_plan_steps`、`ai_tool_runs`、`ai_actions`、`ai_approval_decisions` 等 AI Runtime 表；
- `AIRunDAO.CreateWithOutbox` 已把 Run 创建与 Run Invocation Outbox 写入同一个 MySQL 事务；
- Query API 已拥有 `/internal/v1/control-plane/runs/*` 的 transition/cancel/recovery/event 控制面；
- Orchestrator 已有 `PersistentRunRepository` / `PersistentRunStateStore`，生产模式要求 `run_persistence=remote`，远端提交成功后才更新本地 RunCache；
- `ai_run_events` 已按 Run 内单调 `sequence` 持久化，SSE 已由 Query API 基于 MySQL 做 replay + live DB tail，`Last-Event-ID` 已使用 sequence；
- `/internal/v1/query/*` 已是 Orchestrator 获取 metrics/logs/traces/alerts/topology/kubernetes/changes/knowledge 的 canonical typed 查询面，Orchestrator 的 `InternalQueryClient` 已明确禁止 direct DB/VM/VLogs/Kubernetes 旁路；
- Schema Migrator 已是 DDL Owner，Query API runtime 不应承担 DDL；
- Helm 已默认不向 Orchestrator 注入 MySQL/ClickHouse 密码，真实执行开关与 K8s 写权限默认关闭；
- ingest 当前 Trace 解析存在，但 `SpanSink=nil`，现行 ingest 主链仍没有 Trace 持久化写入。

本轮真正需要完成的是**在现有主线上补齐生产并发与恢复语义**，而不是改名重造系统：

1. 在现有 `ai_runs` 与 `/internal/v1/control-plane/*` 上增加 DB-backed Run Lease、epoch/token fencing；
2. 在现有控制面增加原子 Runtime Commit，逐步替代“状态、Event、Artifact 分散提交”的关键路径；
3. 修正 Orchestrator production remote 模式仍可能在远端读取失败时回退本地 fallback 的行为；
4. 修正 Cancel 客户端丢失 `expected_version/command_id` 的真实代码缺陷；
5. 将内存 ReplayCache 从“安全正确性的唯一依据”降为本地快速防护，并为需要跨 Pod 防重放的 Context 增加共享持久化校验；
6. 将现有 `/internal/v1/query/*` 增强为“有 ToolRun 审计、幂等、Lease 绑定”的 canonical read-only Tool execution plane，而不是再增加 `/internal/v1/runtime/.../tools` 平行接口；
7. 复用现有 `ai_tool_runs`，不创建 `ai_tool_invocations` 第二张权威表；
8. 保留当前 V9.2 `RunStatus` 枚举，Lease/Tool/LLM/Retry 等运行等待原因使用正交 Runtime Metadata 表达，不引入 `QUEUED/RUNNING/WAITING_*` 第二套状态机；
9. A-C 阶段只形成可恢复、可审计的只读调查闭环；Stage D 继续复用 `ai_actions/ai_approval_decisions/ai_verifications`，真实执行仍独立上线；
10. 修复 AiChat 当前 `cluster_id=all` 与 Query API 强制 concrete cluster 的真实契约断点，以及遗留 `/ai/suggestion/execute` / `final_report` 调用；
11. 移除普通 Chat 当前 `chat_graph -> collect` 的固定实时观测采集，把任何需要 metrics/logs/traces/alerts/K8s/topology/changes 的请求统一升级为显式 Investigation Run，避免 Chat 旁路 `InternalQueryClient + ai_tool_runs + Run Lease`。
12. Query API 的**逻辑职责继续集中**，但生产部署必须把 HTTP API、Run Dispatch Worker、Alert Evaluation Worker 拆成独立运行角色；三者可复用同一 Go 代码库/镜像，但不得再由每个 HTTP Pod 无条件启动全部后台循环。
13. Alert Evaluation 必须使用 Kubernetes Lease 单 Leader 执行，并把 cooldown/dampening 等规则运行状态持久化到 MySQL；进程内 `lastRuleTrigger/ruleStreak` 只能作为缓存，不能成为多副本正确性来源。
14. `/internal/v1/control-plane/runs/unfinished` 必须改成 dedicated global recovery service capability，不能在仅完成 Trusted Context 校验后直接 `ScanUnfinished()` 返回所有租户 Run；单 Run Snapshot/Claim 继续按 tenant/run 绑定。
15. `/internal/v1/query/*` 必须统一 ToolResult 数据质量合同，明确 `complete/partial/failed`、`truncated`、`observed_at`、绝对查询时间窗、source errors 与 digest；数据源失败绝不能被静默转换为空集合。
16. Investigation Run 创建时必须把“最近 5m/30m/1h”等相对时间解析并冻结为 `ai_runs.time_range_start/time_range_end` 的绝对时间；同一 ToolRun 的 transport retry 不得因重试时间变化而查询另一时间窗。
17. Trace 不再保留二选一：平台 `ClickHouse trace_spans` 固定为 Trace Persistent SoT，OTLP/DeepFlow 只作为输入来源；必须补回实际 SpanSink 写链。
18. MySQL 已是 Runtime/Lease/Replay/Approval/Action 控制面一致性点，生产候选必须具备单写主端点语义、ACK 后不丢 Runtime 控制事务、PITR/恢复演练和 failover 验证；Runtime 权威读写不得走延迟只读副本。
19. Stage D 必须新增**独立的 `ai-action-executor` 运行边界**；当前 Orchestrator 内的 `ExecutionAdapter/CredentialBroker` 只作为领域原型，不允许通过给 Orchestrator 增加真实 K8s/OpenStack/Shell 写凭据的方式直接生产化。
20. 平台自身 Runtime/Lease/Tool/Outbox/Recovery/Replay/SSE/LLM/Alert/Action 必须具有统一 control-plane metrics 与 correlation IDs，否则不能把“可恢复、可审计”仅停留在数据库模型层。

A-C 不新增 Redis、消息队列、新持久化 Runtime 微服务、第四种 Trusted Context、第二套 Run Store、第二套 Tool Registry、第二套 Event Store 或第二套 Action 模型。`run-dispatch-worker`/`alert-worker` 只是 Query API 同一代码库的独立运行角色，不拥有新的事实源。Stage D 允许且要求新增唯一的 `ai-action-executor` 安全执行边界，但它不得成为第二个 Action SoT。

## 2. 最终目标架构（复用当前主线）

```text
Frontend
  |
  v
Query API
  - Browser Auth / Tenant / Cluster Authority
  - Existing Run / Event / Outbox Persistence
  - Existing /internal/v1/control-plane/*
      + Runtime Commit
      + Run Lease / Claim / Renew
      + Shared replay consumption where required
  - Existing /internal/v1/query/*
      + typed data access
      + ai_tool_runs lifecycle/audit
      + lease/scope/idempotency binding
  - SSE DB replay/tail
  |
  +--> MySQL
  +--> VictoriaMetrics
  +--> VictoriaLogs
  +--> ClickHouse
  +--> Kubernetes API
  +--> MinIO（仅在引入大 Evidence 对象存储后）

AI Orchestrator
  - Intent / Planner / Agent Runtime
  - Existing ToolRegistry（选择侧）
  - Existing InternalQueryClient（事实查询客户端）
  - Evidence interpretation / Hypothesis / RCA
  - LLM invocation
  - RunCache only as cache, never SoT
  |
  +--> Query API /internal/v1/control-plane/*
  +--> Query API /internal/v1/query/*
  +--> approved LLM egress
```

**当前 Chat 例外必须在阶段 B 消除：** `chat_graph` 现仍固定进入 `node_collect`，并通过 `tools.py` 的 public/legacy 查询 helper 采集 services/logs/alerts/traces，K8s 虽走 `/internal/v1/query/kubernetes` 但 helper 自行重签 capability。该路径是当前代码事实，不是目标架构；A-C 完成后普通 Chat 不再读取实时观测事实。

### 2.1 生产运行角色：逻辑 Query API 不等于一个进程包办全部后台任务

生产 profile 固定拆为三个运行角色，复用同一 `ai-apm-query-go` 代码库/镜像：

```text
query-api
  role=api
  replicas >= 2
  只提供 HTTP / Auth / Control Plane / Internal Query / SSE

run-dispatch-worker
  role=run-dispatch
  replicas >= 2
  只扫描/Claim/派发 ai_run_outbox
  correctness = DB dispatch fencing，不使用 leader election

alert-worker
  role=alert-eval
  replicas >= 2
  Kubernetes Lease = aiops-alert-evaluator
  只有 Leader 执行 evaluateAlerts/Webhook
  cooldown/dampening state = MySQL
```

`cmd/api/main.go` 当前无条件启动 `RunDispatchLoop()` 与 `StartAlertEvaluation()`；目标实现必须增加明确 `--role=api|run-dispatch|alert-eval`（或等价单一枚举配置），每个 Pod 只启动对应职责。禁止 `api` 角色继续隐式启动 Worker。三个角色分别暴露 `/healthz`/`/readyz`；Worker readiness 必须验证 MySQL，`alert-eval` 还必须验证 Kubernetes Lease API 可达。

本机单节点可以同时运行上述多个 Pod 验证进程竞争、Lease/Fencing 与 Leader 切换；这只能证明**同节点多进程正确性**，不能替代生产节点故障验证。

### 2.2 唯一权威定义

```text
Runtime semantic decision owner    = AI Orchestrator
Runtime persistent SoT             = MySQL
Runtime persistence owner          = Query API
Run state validation authority     = Query API
Run execution ownership            = DB Lease + lease_epoch + lease_token
Canonical control-plane namespace  = /internal/v1/control-plane/*
Read-only tool execution plane      = /internal/v1/query/*
Tool selection registry            = ai-orchestrator/tool_registry.py
Data-source credential owner       = Query API
Tool run persistence               = existing ai_tool_runs
Runtime event SoT                   = existing ai_run_events
Run invocation dispatch outbox     = existing ai_run_outbox
Browser SSE owner                   = Query API + ai_run_events DB tail
Schema DDL owner                    = schema-migrator
HTTP API runtime role                = query-api role=api
Run dispatch runtime role            = run-dispatch-worker + DB dispatch fencing
Alert evaluation runtime role        = alert-worker + Kubernetes Lease + MySQL rule state
Trace persistent SoT                 = ClickHouse trace_spans
Stage D mutation authority           = independent ai-action-executor
```

特别说明：`ai_run_outbox` 在当前代码中的真实职责是 **Run 创建后的 RunInvocation 可靠派发**，不是通用 Event Delivery Queue。SSE 直接读取 `ai_run_events`；Runtime Event 不要求再复制一份到 `ai_run_outbox`。

### 2.3 不再采用的历史设计假设

以下设计从本版本起删除，不得按旧章节恢复：

- 不新增 `/internal/v1/runtime/*` 平行 namespace；
- 不新增 `QUEUED/RUNNING/WAITING_TOOL/WAITING_LLM/WAITING_RETRY` 第二套 RunStatus；
- 不新增 `InternalServiceRequest` 或 `ChatQueryContext` 作为第四/第五种跨服务 Context；
- 不新增 `ai_tool_invocations` 与现有 `ai_tool_runs` 竞争 Tool I/O 权威；
- 不把 `ai_run_outbox` 改造成 Event Outbox；
- 不要求 Query API 复制一套完整 Python ToolRegistry；固定 `/internal/v1/query/<operation>` + capability 映射本身就是服务端执行 allowlist。

## 3. 当前代码事实、已实现能力与必须修复项

### 3.1 代码事实基线

| 领域 | 当前真实实现 | 本文处理方式 |
|---|---|---|
| Run Persistence | Query API MySQL + Orchestrator remote-first repository 已存在 | 保留；修复 remote 模式读 fallback |
| Run Status | V9.2 `created/planning/investigating/...` 跨 Python/Go/TS 冻结 | 保留枚举；只做最小 transition 修正 |
| Run CAS | `state_version` + control command idempotency 已存在 | 保留；Runtime Commit 复用并增强 |
| Run creation | `CreateWithOutbox` 已同事务写 Run + `ai_run_outbox` | 保留；可同事务补 `RUN_CREATED` Event |
| Run dispatch | `run_dispatch.go` 扫 `ai_run_outbox`，claim/retry/deliver | 保留；与 Run execution Lease 明确区分 |
| Event Store | `ai_run_events` + `event_id` + Run sequence owner 已存在 | 保留；增加 Tx-aware append 给 Runtime Commit |
| SSE | Query API JWT 重授权 + replay + DB live tail 已存在 | 保留；作为现行正确基线 |
| Recovery snapshot | Query API 单事务加载 Run/Plan/Tool/Action/Approval/last seq 已存在 | 保留；加入 Lease/Runtime metadata |
| Typed internal query | 8 个 `/internal/v1/query/*` 已存在 | 直接作为 canonical read-only Tool execution plane 基线 |
| Tool registry | Orchestrator Python ToolRegistry 已存在；执行类 disabled | 保留；不复制第二套 Registry |
| Tool persistence | `ai_tool_runs` 已存在 | 原位扩字段，不建新表 |
| Action/Approval | `ai_actions`、`ai_approval_decisions` 已存在；真实执行仍禁止 | Stage D 原位增强 |
| Replay | Orchestrator verifier 使用 bounded in-memory `ReplayCache` | 仅作本地防护；生产共享语义需补齐 |
| Schema | versioned migration + schema-migrator 已存在 | 新增 0004，不修改已应用 migration |
| Orchestrator DB creds | Helm 默认不注入 MySQL/CH password | 保留 fail-closed；清理残余 runtime 依赖 |
| Orchestrator PVC | Chroma/SQLite/checkpoint 共用 RWO PVC | Run SoT 不依赖它；多副本 HA 仍受限 |
| Trace | ingest `SpanSink=nil` | 仍是明确缺口 |
| Chat | Query API 已是 trusted `ai.chat` 入口且不创建 Investigation Run，但 `chat_graph` 仍固定先执行 `collect`；前端仍默认 `cluster_id=all`，并保留 legacy executeSuggestion/finalReport | 必须拆除固定实时采集并修复产品入口 |

### 3.2 当前真实代码缺陷：必须显式纳入整改

**F-01 Production remote read 仍存在 local fallback 风险。** `PersistentRunStateStore.get()` 在 `_repo` 已启用时，cache miss 后远端 refresh 失败会尝试 `_fallback.get(run_id)`；生产模式下这会破坏 Query API/MySQL 作为唯一 SoT 的 fail-closed 边界。生产 `remote` 后端必须禁止返回 fallback Run。

**F-02 Cancel 客户端未兑现自身 CAS/幂等参数。** `PersistentRunRepository.cancel()` 接收 `expected_version`、`command_id`，但当前调用 `ControlPlaneClient.cancel()` 时未传入，客户端最终 POST 空 body。必须修为与 transition 相同的显式 `expected_version + command_id` 语义。

**F-03 Run 状态机在 Python 与 Go 中各有一份同构迁移表。** 当前两边一致，但长期存在漂移风险。实施时应把“共享 fixture/contract test”作为单一变更门禁，而不是再加入第三套状态机。

**F-04 Canonical internal query 已存在，历史方案新增 Generic Tool Gateway 会形成重复执行面。** 本文已改为增强现有 `/internal/v1/query/*`。

**F-05 `ai_run_outbox` 当前是 RunInvocation dispatch outbox。** 不得把 Event/Command 强行塞入同一表改变其业务语义。

**F-06 Orchestrator 仍有 `db*.py`、KG/知识初始化、K8s chat 工具注册、AsyncSqliteSaver 等遗留/非 Runtime 路径。** 不能只凭“文件存在”判定生产仍直连数据面；应按实际入口逐条清理。目标是：Run correctness 不依赖这些本地/直连路径，且默认部署不授予对应秘密或写权限。

**F-07 AiChat 当前前端仍可发送 `cluster_id=all`，且存在遗留执行卡入口。** Query API `ProxyChat` 当前明确拒绝 missing/`all` cluster，并只接受 concrete canonical cluster；因此这不是“诊断模式才冲突”，而是当前所有 canonical Chat 请求都可能因前端默认 `all` 直接失败。阶段 B 必须先修前端 cluster 选择与错误提示，再讨论 Chat/Investigation 分流。

**F-08 Outbox dispatch lease 使用应用 `time.Now()`。** 它与新增 Run execution Lease 不是同一种 Lease。后者必须以 DB time 为权威；如后续要提升 dispatcher 多副本一致性，可将 dispatch lease 也迁移到 DB time，但不要混用字段/错误码。

**F-09 现有 `ai_control_commands` 还没有完整的幂等语义。** 表里已有 `payload_json`，但 `recordControlCommand()` 当前没有保存实际 payload；handler 对“done 的相同 command_id”只检查 operation，并返回此刻的 current Run，没有验证 target/expected_version/payload 是否与首次请求一致，也没有保存首次 response。相同 command_id 被错误复用于不同请求时可能被当成 replay。Transition/Cancel 必须统一补 `payload_hash + response_json`，并把 command 记录与 Run CAS/state mutation 放进同一事务。

**F-10 Stage D 的 Action 幂等当前也只是“重复键即 existing”。** `AIActionDAO.Create()` 命中 `(run_id,idempotency_key)`/主键重复时直接返回 `created=false`，当前 Action handler 不反查现有 Action 的 `action_hash/action_type/cluster_id/params` 是否与本次一致。A-C 因真实执行关闭不构成当前生产 mutation 风险，但 Stage D 开启前必须把“同 key 同语义 replay / 同 key 不同语义 409”写进现有 `ai_actions` 路径。

**F-11 `ai_run_outbox` 的 stale claimed 回收当前实现不闭环。** `ScanPending()` 会返回 `status='claimed' AND next_retry_at<=now` 的过期 in-flight 记录，但 `Claim()` 只执行 `WHERE status='pending'`，因此 dispatcher 崩溃后留下的 stale claimed 会被反复扫描却永远无法重新 claim。必须把“pending 到期 / stale claimed 到期”统一纳入一个原子 CAS Claim，并改用 MySQL `CURRENT_TIMESTAMP(3)` 作为 dispatch lease/retry 的时间权威。

**F-12 当前 Helm 默认 `queryApi.k8sInsecureSkipVerify: "true"`。** 这可以服务本机自签单节点验证，但不能作为生产候选的 Kubernetes API TLS 策略。必须区分 local 与 production profile：生产 Query API 必须验证目标集群 API Server 证书/CA，`insecureSkipVerify=true` 只能在明确标记的本地验证 profile 使用。

**F-13 Browser canonical route allowlist 与已注册产品 API 不完整对齐。** `AuthMiddleware` 对 `/api/v1/*` 采取 fail-closed：只有 `isCanonicalProtectedRoute()` 明确列出的 path 才放行。当前 main 已注册但 allowlist 未覆盖的高影响只读路由至少包括 `/api/v1/metrics/query`、`/api/v1/metrics/query_range`、`/api/v1/traces/{id}`/`context`、`/api/v1/topology/node/{name}`、多项 `/api/v1/infrastructure/*`，以及普通 `/api/v1/ai/runs/{runID}` 详情（events/cancel 有单独前缀例外）。这些 handler 即使实现存在，浏览器请求仍会先得到 `permission_denied`。修复不能用“放开整个前缀”解决；每个需要保留的路由必须先迁到 canonical tenant/cluster/resource scope，再加入 allowlist；不用的 legacy route 应删除。

**F-14 LLM secret internal endpoint 的认证强度低于现有 internal control/query 边界。** `/api/v1/settings/llm/internal` 当前由 `AuthMiddleware` 特判放行，仅在 handler 内比较共享 `X-Internal-Token`，随后直接返回解密后的 Provider API Key；没有校验 Ed25519 `TrustedRequestContext`、audience/capability，也位于 `/api/v1` 而不是 internal namespace。该路径只允许作为 Stage C 迁移桥接：迁移期升级为 service token + TrustedRequestContext + 固定 `llm.config.read` capability + internal-only routing + TLS；Stage C 结束前必须停止向 Orchestrator 下发 Provider API Key，由固定的 LLM Egress Proxy 独占外部 Provider Key。另需让 `ModelsLLM` 复用 `validateLLMBaseURL()`，避免它成为绕过 Save/Test SSRF 校验的第二条 Provider 请求路径。

**F-15 普通 Chat 当前仍固定采集实时观测数据，而且没有统一走 canonical Tool execution plane。** 当前可信入口是 `Query API ProxyChat -> Orchestrator /internal/v1/chat -> brain.stream_sync(mode="chat")`；`chat_graph` 的 entry point 固定为 `collect`。因此每轮 Chat 在做 light-query 判断之前就固定尝试：服务概览、K8s 基础设施、告警、日志；识别到具体 service 时还尝试 RED 指标与 Trace；K8sGPT 才是条件调用。非 light query 后续继续进入 RCA/RAG/CrewAI，并可能产生二次查询。更具体的代码缺陷包括：

- `get_service_list()` 先尝试 `kg_graph._load_graph()`，该函数直接连接 Orchestrator 本地 MySQL 配置读取 `topology_nodes/topology_relations`；默认 Helm 不注入 Orchestrator MySQL password 时通常失败并 fallback，但它仍是实际可达的 direct-DB 代码旁路，必须移出 Chat 主链；
- services/logs/alerts/traces 等固定采集多数仍通过 `signed_query_api_request()` 调用 `/api/v1/*` legacy/browser 查询面，而不是 `InternalQueryClient -> /internal/v1/query/*`；这些公共路径并不按 TrustedRequestContext 的 capability 做 route-capability 精确绑定；
- `get_infrastructure()` 虽然调用 canonical `/internal/v1/query/kubernetes`，但它在 Chat 内部自行新建 `TrustedContextIssuer`，把进入 Chat 的 `ai.chat` 语义重新签为 `kubernetes.resources.read` 的 system principal context，绕开 `InternalQueryClient` 的 Tool Registry capability binding；
- `query_metrics()` 调用 `/api/v1/services/{service}`，但当前 Query API canonical allowlist 只放行精确 `/api/v1/services`，详情路径会先被 AuthMiddleware 403；节点随后静默吞掉，导致 `red_metrics` 在真实 canonical Chat 中并不可靠；
- `query_traces(service, ...)` 实际请求 `/api/v1/traces?limit=5&cluster_id=...`，没有把 `service` 放进 URL，因此即使成功也不是该目标服务的 Trace 证据；
- `node_collect()` 产生并且 `node_crewai()` 读取 `logs_data`，但 `AgentState` 当前没有声明 `logs_data` 字段。无论当前 LangGraph 版本如何处理未声明 channel，生产代码都必须把它纳入明确 state schema 或删除该字段，禁止依赖隐式行为。

A-C 目标不是把这套固定采集“搬到另一个 helper”后继续保留，而是**普通 Chat 删除固定实时观测采集**。任何需要当前 metrics/logs/traces/alerts/K8s/topology/changes 的请求都必须显式创建 Investigation Run，由 Run Lease + `InternalQueryClient` + `ai_tool_runs` + Evidence 闭环执行。Chat 只保留 LLM 对话、会话历史与静态/知识类内容；不得在 Chat 内部把 `ai.chat` 隐式提升为 observability/Kubernetes read capability。

**F-16 Query API 当前进程职责过度耦合。** `cmd/api/main.go` 在同一个 HTTP 进程中无条件 `go handler.RunDispatchLoop(...)`，随后 `handler.StartAlertEvaluation()`。一旦 `query-api` 扩为多副本，Run Dispatcher 虽可通过 DB Claim 收敛，但 Alert evaluator 会在每个 Pod 各跑一套 60s 循环；因此生产部署必须拆运行角色，而不是靠“当前 replicas=1”掩盖问题。

**F-17 Alert cooldown/dampening 仍是进程内权威。** `alert_engine.go` 的 `lastRuleTrigger` 与 `ruleStreak` 是 package-level map；它解决了单进程每轮重置问题，却不能覆盖多 Pod 或 Leader 切换。必须增加 MySQL `alert_rule_runtime_state`，Leader 失效后新 Leader 从 DB 恢复 `last_triggered_at/breach_streak/last_eval_state`。Webhook 继续按 at-least-once 语义处理，并携带稳定 `alert_event_id`/Idempotency-Key，不宣称 exactly-once。

**F-18 Recovery `unfinished` 端点存在全局扫描作用域缺口。** `internalControlPlaneRunUnfinished()` 当前只调用 `authorizeInternalControlPlane(...)`，随后直接 `runDAO.ScanUnfinished()`；而 `ScanUnfinished()` SQL 没有 tenant 条件，会返回全部非终态 Run。必须把该端点改为 dedicated global recovery capability，且只允许明确的 system recovery identity；普通 tenant-scoped `control_plane.runs.recover` 不能调用。

**F-19 Kubernetes canonical internal query 当前会把数据源错误伪装成空结果。** `InternalQueryKubernetes()` 对 `ListNodeDetails/ListPods/ListNodeNames` 三个错误均使用 `if err == nil { ... }`，失败后仍返回 HTTP 200 和空数组。AI 调查无法区分“确实没有 Pod/Node”与“Kubernetes API 查询失败”。这是 Evidence truth boundary 缺陷，必须改成 machine-readable complete/partial/failed 结果。

**F-20 Internal Query/ToolRun 缺少统一观测时间与结果质量合同。** 当前请求主要用 `minutes/hours/since` 相对时间，返回各 endpoint 自定义 JSON；`ai_tool_runs` 也没有 `observed_at/query_window/result_quality/truncated/result_digest`。响应丢失后晚几分钟重试可能查询不同时间窗，却仍被 Planner 当作同一调查步骤。必须冻结绝对时间窗和统一 ToolResult Envelope。

**F-21 Trace 仍存在设计二义性。** 当前 ingest 事实是 `SpanSink=nil`，而旧文档同时保留“平台写 ClickHouse”与“DeepFlow 外部 SoT”两条方案。为避免长期双 ownership，本版固定 `ClickHouse trace_spans` 为平台 Trace Persistent SoT；DeepFlow 只能作为 Span 输入/补充来源。

**F-22 Stage D 当前执行基础仍是 Orchestrator 内的内存原型。** `execution_adapter.py` 的真实适配入口可以挂 `real_adapter`，但幂等仅在 `_executed` 内存 map；`credential_broker.py` 也明确是内存 MVP，credential/audit 都在进程内。它们不能通过“直接给 Orchestrator 写权限”升级成生产执行面，必须迁入独立 `ai-action-executor`，并把权威 Action/Approval/Execution Result 持久化继续留在 Query API/MySQL。

**F-23 MySQL 与平台自观测尚未被提升到控制面基础设施合同。** 完成 Runtime Commit/Lease/Replay 后，MySQL 已承担控制面一致性点；若 MySQL ACK 后事务可在 failover 中丢失，或平台看不到 lease lost/outbox oldest age/tool partial/recovery lag，就无法证明“可恢复、可审计”。生产候选必须补 HA/DR 与 control-plane telemetry 门禁。

## 4. Persistence Ownership 与现有模型复用矩阵

| 数据/能力 | 当前载体 | 目标 Owner | 改造方式 |
|---|---|---|---|
| Run | `ai_runs` / AIRunDAO | Query API | 原位 ALTER，增加 Lease/Runtime metadata |
| Run state | `status/state_version` | Query API validation | 保留 V9.2 status；统一 contract tests |
| Runtime snapshot | 当前 recovery 由多表重建 | Query API | 优先新增 snapshot JSON/结构化字段到现有模型，不建 RunStateV2 |
| Plan/Step | `ai_plan_steps` | Query API persistence / Orchestrator semantics | 保留 |
| Tool run | `ai_tool_runs` | Query API | 原位增强幂等、lease epoch、deadline、evidence consume |
| Evidence | `ai_evidence` | Query API | 保留；大对象再引 MinIO ref |
| Hypothesis | `ai_hypotheses` | Query API | 保留 |
| Action | `ai_actions` | Query API | Stage D 保留并增强 |
| Approval | `ai_approval_decisions` | Query API | Stage D 保留并增强 |
| Verification | `ai_verifications` | Query API | Stage D 保留 |
| Runtime Event | `ai_run_events` | Query API | 保留；增加 AppendTx |
| SSE cursor | `ai_runs.last_event_sequence` + event sequence | Query API | 已实现，保留 |
| Run dispatch | `ai_run_outbox` | Query API dispatcher | 保留当前语义 |
| Control command idem | `ai_control_commands` | Query API | 保留；Runtime Commit 增独立 commit idempotency |
| Run execution Lease | 尚无 | Query API | 原位增加到 `ai_runs` + claim record |
| Shared replay | 当前局部内存 | Query API/MySQL + local verify | 新增共享表，不恢复 DB 旁路 |
| Chat checkpoint | AsyncSqliteSaver/RWO | Orchestrator session concern | 不得成为 Run SoT |

### 4.1 强制原则

1. 现有表能原位扩展就不新建语义等价表。
2. `ai_run_outbox` 只负责 Run Invocation dispatch；若未来确有独立异步 Event 消费者，再单独评审 Event delivery 设计，当前 SSE 不需要。
3. Runtime Commit 只原子提交“必须一致”的 DB 事实；真实外部 Tool I/O 永远不包在长 MySQL 事务中。
4. Orchestrator 的 RunCache、fallback RunStateStore、SQLite checkpoint 只能是缓存/会话状态，生产 Run correctness 不允许依赖它们。
5. Query API 不承担 LLM/Planner/RCA 语义，Orchestrator 不持久化权威 Runtime 事实。

## 5. Runtime Commit：在现有 Control Plane 上增强

### 5.1 接口位置

新增接口必须进入现有 canonical namespace：

```text
POST /internal/v1/control-plane/runs/{run_id}/commit
```

不得新增 `/internal/v1/runtime/runs/{run_id}/commit` 平行入口。

迁移期保留现有：

```text
POST /internal/v1/control-plane/runs/{run_id}/transition
POST /internal/v1/control-plane/runs/{run_id}/cancel
POST /internal/v1/control-plane/runs/{run_id}/events
GET  /internal/v1/control-plane/recovery/snapshot
```

完成主链 cutover 后：

- `transition` 可作为兼容/人工控制接口继续保留，但 Orchestrator 调查主链不再用“transition + event + artifact 多请求”表达一个语义步骤；
- Event 单独 append 仍可用于不要求与状态/Artifact 原子的纯通知事件；
- 关键调查步骤统一走 Commit。

### 5.2 请求契约

```text
commit_id
executor_id
lease_epoch
lease_token
expected_version
target_status | null
state_reason | null
state_message | null
runtime_metadata
plan_updates
tool_result_consumptions
evidence
hypotheses
rca | null
events
```

`target_status` 必须使用现有 `contracts.RunStatus` 字符串，不增加另一套状态名。

### 5.3 Commit 处理顺序

```text
1. INTERNAL_TOKEN / service identity
2. verify fresh TrustedRequestContext（现有 context_type=trusted_request）
3. 校验 run scope / tenant / capability=control_plane.*
4. canonicalize semantic payload 并计算 hash
5. transaction 外快速查 (run_id, commit_id)
6. 已存在且 hash 相同 → 返回首次 CommitResult
7. 已存在但 hash 不同 → 409 IDEMPOTENCY_KEY_REUSED
8. 开始 MySQL transaction
9. SELECT ai_runs ... FOR UPDATE
10. transaction 内二次检查 commit_id
11. 校验非终态、Lease owner/epoch/token/DB expiry
12. 校验 expected state_version
13. 若 target_status != null，使用与 V9.2 contract 对齐的 validateTransition
14. 校验 runtime_metadata/retry 字段
15. 锁并消费本 Commit 声明的 ai_tool_runs（只允许 eligible 且未消费）
16. 写 plan/evidence/hypothesis/rca/runtime metadata
17. 在最后一次权威 Run UPDATE 前重新读取 DB `CURRENT_TIMESTAMP(3)`，再次校验 owner/epoch/token 且 `lease_expires_at > DB_NOW`；Runtime Commit 内禁止任何外部 I/O
18. 更新 ai_runs status/state_version/finished_at 等
19. 使用同一 tx 顺序追加 ai_run_events
20. 写 Commit Idempotency Record + stable response
21. COMMIT
```

**这里不写 `ai_run_outbox`。** `ai_run_outbox` 的现行语义是 RunInvocation dispatch；Runtime Commit 事件由 `ai_run_events` 直接支撑 SSE/replay。

### 5.4 成功后响应丢失

同 `commit_id` + 同 semantic hash 的重试必须返回首次结果，即使当前 Lease 已过期、state_version 已变化或 Run 已被另一个 Executor 接管。只有**新 Commit**才校验当前 Lease/version。

### 5.5 锁顺序

统一数据库锁序：

```text
ai_runs row
→ ai_plan_steps / ai_tool_runs / ai_evidence / ai_hypotheses 等 artifact rows
→ ai_run_events sequence allocation
→ runtime_commit_idempotency
```

已有 `AIRunEventDAO.Append()` 目前自行开事务，不能直接嵌入 Runtime Commit。必须新增 `AppendTx(tx, event)`/等价内部函数，并让现有 `Append()` 复用该函数，确保 sequence owner 与 Commit 在同一事务内。

### 5.6 Commit 幂等记录

新增表固定为 `ai_runtime_commits`：

```text
run_id
commit_id
payload_hash
committed_state_version
result_status
first_event_sequence
last_event_sequence
response_json
created_at
PRIMARY KEY(run_id, commit_id)
```

payload hash 只覆盖稳定业务语义，不包含 Authorization、Trusted Context JWS、lease_token、网络重试时间戳。

`ai_runtime_commits` 是响应丢失后的首次成功结果权威记录，保留期不得短于所属 Run 的保留期；禁止在 Run 仍可查询、恢复或审计时先行 GC Commit Idempotency Record。归档/删除 Run 时才能按同一 retention policy 清理其 Commit Record。

## 6. Runtime Event Store、Run Invocation Outbox 与 SSE

### 6.1 已实现基线：必须保留

当前代码已经具备：

```text
ai_run_events
  - PRIMARY KEY(run_id, sequence)
  - UNIQUE(run_id, event_id)
  - ai_runs.last_event_sequence 作为 sequence owner

Query API SSE
  - browser JWT auth
  - tenant/run authorization
  - Last-Event-ID = run sequence
  - DB replay
  - DB live tail polling
  - heartbeat
  - retention/window check
```

这部分不是“待重新设计”，后续只做原位增强和测试。

### 6.2 `ai_run_outbox` 的真实职责

当前 `ai_run_outbox`：

```text
Run creation
→ pending invocation
→ dispatcher claim
→ POST /internal/v1/run-invocations
→ delivered / retry
```

它是 **RunInvocation dispatch outbox**。

当前实现存在一个必须先修的恢复缺陷：`ScanPending()` 能扫描 stale `claimed`，但 `Claim()` 只允许从 `pending` 进入 `claimed`。因此 claim 后进程崩溃留下的 stale claimed 不能真正回收。仅把 `Claim()` 改成“允许 stale claimed”仍不够，因为旧 dispatcher 在 lease 失效后仍可能晚到执行 `Deliver/Retry`，覆盖新 dispatcher 的结果。

`ai_run_outbox` 必须增加独立 dispatch fencing 字段：

```text
dispatch_owner_id
dispatch_epoch
dispatch_token_hash
dispatch_expires_at
next_retry_at            # 只表示重试调度时间，不再兼任 claim lease
```

Claim 必须原子允许：

```text
(status=pending AND retry_due)
OR
(status=claimed AND dispatch_expires_at <= DB_NOW)
→ dispatch_epoch++
→ claimed + new owner/token/expiry + dispatch_count++
```

`Deliver/Retry` 必须同时匹配 `invocation_id + dispatch_owner_id + dispatch_epoch + dispatch_token`，且 `Deliver` 只允许未过期的当前 Claim 完成；旧 epoch/token 返回 `DISPATCH_LEASE_LOST`，不得改变 outbox。`ScanPending/Claim/Retry/Deliver` 的到期判断统一使用 DB `CURRENT_TIMESTAMP(3)`，不要由多个 Worker 用 `time.Now()` 各自决定 lease 是否过期。

禁止把如下数据混入同一 status machine：

- Runtime Event delivery；
- generic command bus；
- Tool result delivery；
- SSE cursor。

SSE 的权威源始终是 `ai_run_events`。

### 6.3 Event 与 Runtime Commit

Runtime Commit 中的 Event 必须与状态/Artifact 同事务写入。实现时把当前 Event DAO 抽出 transaction-aware append：

```text
AppendTx(tx, run_id, event_id, type, payload)
```

同一个 `event_id` 的并发重试不得制造 sequence gap；现有唯一键行为继续保留。

### 6.4 Tool Event

Tool lifecycle event 必须来自真实 `ai_tool_runs` 生命周期，而不是 LangGraph 节点进入推断：

```text
TOOL_STARTED
TOOL_SUCCEEDED
TOOL_FAILED
TOOL_TIMEOUT
TOOL_RESULT_LATE
```

`TOOL_PLANNED` 可由 Planner/Commit 记录计划事实，但不能被前端理解为 I/O 已执行。

### 6.5 SSE 多副本语义

继续使用现有 DB-tail 模型，不引入 Pod-local subscriber 作为正确性依赖：

```text
cursor = Last-Event-ID
repeat:
    SELECT events WHERE run_id=? AND sequence>cursor ORDER BY sequence
    send
    cursor=max(sequence)
```

可以以后用通知机制降低 polling 延迟，但通知仅作 wake-up，DB event store 仍是 correctness source。

## 7. 唯一 Run 状态机：保留 V9.2 合同，等待原因正交化

### 7.1 Canonical RunStatus

当前 Python、Go、TypeScript 已围绕以下状态形成合同与实现。本文不再引入第二套状态名：

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

终态保持：

```text
success / partial / failed / regressed / cancelled
```

### 7.2 A-C 阶段最小状态迁移调整

现实现将调查与执行闭环放在一张 Run 状态机里，`investigating` 目前不能直接进入 `success/partial`。A-C 阶段固定执行以下**最小兼容扩展**，使只读调查能够在不进入 Stage D 的情况下正常结束：

```text
created -> planning | cancelled
planning -> investigating | awaiting_confirmation | failed | cancelled
investigating -> awaiting_confirmation | success | partial | failed | cancelled
awaiting_confirmation -> investigating | success | partial | cancelled
```

Stage D 才使用已有：

```text
awaiting_approval -> executing -> verifying -> success/partial/failed/regressed
```

不要删除现有 enum 值；只修改允许迁移集合，并同步 Python/Go/TS fixture/contract tests。

### 7.3 Tool/LLM/Retry 不再占用 RunStatus

新增/扩展 Runtime Metadata：

```text
runtime_wait_kind = none | tool | llm | retry | external_confirmation | approval
retry_not_before
retry_attempt
last_failure_code
pending_tool_run_ids
pending_llm_call_id
```

示例：

```text
status=investigating + runtime_wait_kind=tool
status=investigating + runtime_wait_kind=llm
status=investigating + runtime_wait_kind=retry
```

这样不破坏现有跨语言 `RunStatus`，Lease/Recovery 仍能准确识别等待原因。

### 7.4 Retry

进入 retry 等待时：

```text
runtime_wait_kind=retry
retry_attempt = previous + 1
retry_not_before > DB_NOW
last_failure_code=<machine-readable>
```

Recovery/Claim 必须使用 MySQL 时间判断 `retry_not_before`。恢复工作后把 `runtime_wait_kind` 切回 `none/tool/llm`，按语义保留 retry audit 字段。

### 7.5 新 Run

保持当前代码合同：

```text
status=created
state_version=0
lease_epoch=0
last_event_sequence=0 initially
```

扩展 `CreateWithOutbox` 同事务完成：

```text
insert ai_runs
append RUN_CREATED event（sequence=1）
insert ai_run_outbox pending invocation
commit
```

不再增加 `RUN_QUEUED` / `QUEUED` 语义。

## 8. State Version、Cancel 与 Fencing

### 8.1 state_version

继续使用现有 `state_version`，每次真正改变 Run 业务状态/Runtime Commit 成功后递增；Lease claim/renew、SSE tail、Outbox dispatch claim 不修改 `state_version`。

### 8.2 当前 Cancel 缺陷与修复

当前 `PersistentRunRepository.cancel()` 虽接收 `expected_version` 与 `command_id`，但 `ControlPlaneClient.cancel()` 不接收两者并发送空 body。必须首先修复为：

```text
POST /internal/v1/control-plane/runs/{run_id}/cancel
{
  "expected_version": <caller view>,
  "command_id": "<stable UUID>"
}
```

Query API：

```text
service auth + TrustedRequestContext
→ validate command_id + explicit expected_version
→ canonical payload hash
→ BEGIN
→ SELECT ai_control_commands(command_id) FOR UPDATE / detect existing
   - same run+operation+payload_hash + done => return stored response_json
   - same command_id but different payload => 409 IDEMPOTENCY_KEY_REUSED
→ SELECT ai_runs FOR UPDATE
→ expected_version CAS
→ non-terminal check
→ status=cancelled
→ state_version++
→ clear execution lease + lease_epoch++
→ runtime_wait_kind=none
→ AppendTx RUN_CANCELLED
→ upsert command status=done + payload_json/payload_hash/response_json
→ COMMIT
```

不得再由 handler 在请求缺少 expected_version 时“先读当前 version 再当作 caller expected version”，因为这会吞掉调用方并发冲突。JSON body 应能区分“未提供 expected_version”和合法数值（例如使用 `*int64`/显式字段校验），不能依赖 Go `int64` 零值猜测。

同一套 control-command 幂等规则同时应用于现有 `transition`；不能继续出现“相同 command_id + 不同 target 仍被当成成功 replay”的情况。`ai_control_commands` 的作用是业务命令幂等，不是一个只记录 `done` 标志的旁路审计表。

### 8.3 Cancel 后 fencing

Cancel 后旧 Tool/LLM 可以产生“真实已经发生”的晚到审计结果，但不能被消费成 Evidence/Run mutation。任何旧 lease epoch 的新 Commit 必须返回 `RUN_LEASE_LOST`/等价稳定错误。

## 9. Run Execution Lease：新增在现有 Control Plane

### 9.1 与现有 Outbox dispatch lease 分离

当前 `run_dispatch.go`/`ai_run_outbox` 已有 30s 左右的 dispatcher claim/retry lease，它只保护“哪一个 Query API dispatcher 正在派发 RunInvocation”。

本节新增的是：

```text
Run execution lease
= 哪一个 Orchestrator executor 有权继续修改一个 Run
```

两者不得共用字段、token、错误码或生命周期。

### 9.2 ai_runs 新增字段

```text
lease_owner_id
lease_epoch BIGINT NOT NULL DEFAULT 0
lease_claim_id
lease_token_hash
lease_expires_at
heartbeat_at
runtime_wait_kind
retry_not_before
retry_attempt
last_failure_code
```

### 9.3 API

复用 control-plane namespace：

```text
POST /internal/v1/control-plane/runs/{run_id}/claim
POST /internal/v1/control-plane/runs/{run_id}/lease/renew
```

请求继续使用现有 `TrustedRequestContext` + service token，不增加新 Context 类型。

### 9.4 Claim

```text
SELECT ai_runs FOR UPDATE
→ claim idempotency lookup
→ terminal? reject
→ active lease? reject
→ runtime_wait_kind=retry 且 DB_NOW < retry_not_before? reject
→ awaiting_confirmation / awaiting_approval? 默认不由 Recovery 自动抢占
→ lease_epoch++
→ store owner / claim_id / SHA256(token) / DB expiry
→ commit
```

A-C 自动可恢复状态主要是：

```text
created
planning
investigating
```

`awaiting_confirmation` 必须等用户控制事件；`awaiting_approval/executing/verifying` 属 Stage D 规则。

### 9.5 Claim 幂等

```text
same claim_id + same executor + same token hash + same epoch + lease valid
→ 返回当前 lease metadata，idempotent_replay=true

same claim_id + different executor/token
→ 409 CLAIM_ID_REUSED

same claim_id whose generation already expired/lost
→ 409 CLAIM_ID_EXPIRED
```

### 9.6 Renew

只用 DB 时间判断有效性，返回：

```text
server_now
lease_expires_at
lease_remaining_ms
lease_epoch
```

固定默认值为 TTL 30s / heartbeat 10s；启动时强制校验 TTL >= heartbeat*3，不满足则进程 readiness fail-closed。

### 9.7 Orchestrator 本地行为

每个正在执行的 Run 启动独立 renew task。`lease_remaining_ms` 转换为 monotonic deadline，进入 `LEASE_UNCERTAIN` 后禁止新的 Planner/Tool/LLM/Commit，只重试 Renew 或保守停止。

## 10. Trusted Context 与 Shared Replay：严格复用现有三类合同

当前跨服务合同已经明确只有三类：

```text
RunInvocationContext  query-api -> orchestrator，新 Run 派发
RunControlContext     query-api -> orchestrator，cancel/stream/action decision
TrustedRequestContext orchestrator -> query-api，control-plane/query access
```

本文不新增 `InternalServiceRequest`、`ChatQueryContext` 等平行 Context。

### 10.1 当前事实

两端当前都已有正确的签名、issuer/audience、lifetime、nonce 校验，但 replay 存储仍是进程内实现：

- Orchestrator `trusted_context.py` 使用 bounded in-memory `ReplayCache`；
- Query API `internal/auth/trusted_context.go` 的现有具体实现是 `InMemoryReplayCache`，`VerifyConfig` 虽抽象了 `ReplayCache` 接口，但当前仓库没有 DB-backed 实现。

因此当前 replay 防护能覆盖单进程生命周期，不能证明 Query API/Orchestrator 多 Pod、Pod 重启后的共享一次性消费。A-C 生产候选必须把需要一次性消费的 Context 接到共享持久化 Replay Store。

Orchestrator -> Query API 的 `ControlPlaneClient` / `InternalQueryClient` 已经为**每个请求新签** `TrustedRequestContext`，包含新的 request_id/nonce；不应复用 RunInvocationContext 做后续请求。

### 10.2 正确性与安全分层

必须区分：

```text
业务幂等：request_id / command_id / commit_id / tool_run_id / invocation_id
执行并发正确性：Run Lease + epoch fencing
安全防重放：signed Context nonce/jti replay guard
```

不能把其中一个当成另外两个的替代。

### 10.3 Query API 验证 TrustedRequestContext

Query API 本身就是 verifier，可直接把 nonce/replay 记录落 MySQL：

```text
issuer
audience
nonce
consumer_service
request_hash
consumed_at
expires_at
UNIQUE(issuer, audience, nonce)
```

`consumer_service` 必须从已经通过认证的内部服务身份派生，禁止从请求 body/query/header 中接受调用方自报值。`issuer/audience/consumer_service` 都属于安全上下文，不得由业务 payload 覆盖。

相同业务请求的 HTTP retry 不应通过“复用同一个一次性 Context”实现；客户端保留同一个业务 idempotency key，但**重新签发新的 TrustedRequestContext**。服务端先完成安全验证，再靠业务幂等记录返回第一次结果。

这比历史设计的 `nonce + consume_id` 同时承担 transport idempotency 更符合现有客户端实现。

### 10.4 Query API -> Orchestrator Context

RunInvocation/RunControl 的跨 Pod replay 固定采用 issuer-owned shared consume，不允许 Orchestrator 直写 MySQL：

```text
POST /internal/v1/security/replay/consume
```

固定流程：

1. Orchestrator 本地先校验 Query API 签名、issuer、audience、lifetime；
2. Orchestrator 使用自己的已认证 service identity 和**新签发** TrustedRequestContext 调用 replay consume endpoint；
3. 请求只携带待消费 Context 的 `issuer/audience/nonce` 与稳定 context digest，不接受 caller-supplied `consumer_service`；Query API 从认证服务身份派生 consumer；
4. Query API 在 MySQL 中以 `UNIQUE(issuer,audience,nonce)` 原子消费；首次成功返回 `consumed=true`，已消费返回 `409 CONTEXT_REPLAYED`；
5. 只有 replay consume 成功后，Orchestrator 才能继续处理 RunInvocation/RunControl；随后仍以 `invocation_id`/command id 做业务幂等，并在任何 Runtime mutation 前获得有效 Run Lease；
6. replay consume endpoint 或 MySQL 不可用时，新的 RunInvocation/RunControl **fail closed**，不得继续 Claim、Tool、LLM 或 Commit。业务幂等 + DB Lease fencing 是正确性第二道防线，不能替代安全 replay protection。

该 endpoint 只能消费 Query API 自己签发且 audience 匹配 Orchestrator 的一次性 Context，不得提供任意 nonce 写入、查询或删除能力，也不得成为 Runtime/数据面旁路。

### 10.5 清理

Replay record 的删除时间不得早于 `context expires_at + max_clock_skew + retry safety window`。GC 必须按 DB 时间执行；在该时间点之前即使业务 Run 已终态，也不得提前释放 nonce 使旧 Context 再次可用。

## 11. Canonical Internal Query = 现有只读 Tool Execution Plane

### 11.1 不新增 Generic Tool API

当前代码已经存在：

```text
POST /internal/v1/query/metrics
POST /internal/v1/query/logs
POST /internal/v1/query/traces
POST /internal/v1/query/alerts
POST /internal/v1/query/topology
POST /internal/v1/query/kubernetes
POST /internal/v1/query/changes
POST /internal/v1/query/knowledge
```

每个端点：

- 使用 strict TrustedRequestContext；
- 有固定 capability；
- 复用 Query API typed repository；
- tenant/cluster scope 服务端校验；
- 禁止 arbitrary SQL/PromQL/URL passthrough。

这就是 A-C 阶段的 Query API Tool execution authority。历史方案中的 `/internal/v1/runtime/runs/{run_id}/tools/{tool_name}:invoke` 删除。

### 11.2 Orchestrator ToolRegistry 的角色

继续保留 `ai-orchestrator/tool_registry.py` 作为**选择侧 registry**：

```text
planner_selectable
read_only
risk
capability
backend
input/output schema
```

Query API 不复制一套完整 registry；服务端通过“固定 route -> required capability -> typed repository”形成执行 allowlist。

以下当前特殊 Tool 在 A-C 必须处理：

- `execute_k8s.v1` / `execute_shell.v1`：继续 disabled；
- `k8sgpt_diagnose.v1`：如果仍需直接在 Orchestrator 容器用 kubeconfig/K8s API，则不满足最终凭据边界，A-C 生产候选前必须迁移到 Query API 受控实现或标记 unavailable；
- knowledge：优先走现有 `/internal/v1/query/knowledge`，不要因为 Chroma 本地存在就恢复 Runtime MySQL 旁路。

### 11.3 复用 `ai_tool_runs`，不建 `ai_tool_invocations`

在现有 `ai_tool_runs` 原位增加：

```text
args_hash
lease_epoch_at_start
executor_id
deadline_at
eligible_for_evidence
evidence_consumed_at
provider_request_id/query_id（可选）
```

`tool_run_id` 本身就是稳定业务幂等键。

### 11.4 InternalQueryClient 扩展 envelope

Orchestrator 发起每次 query 时增加稳定审计元数据，例如：

```text
X-AIOps-Tool-Run-ID: <uuid>
X-AIOps-Tool-ID: query_metrics.v1
X-AIOps-Lease-Epoch: <n>
```

或将等价字段加入 versioned internal envelope。字段必须被 TrustedRequestContext 请求哈希覆盖，不能成为未签名旁路参数。

Query API 不相信 caller 的 tenant/cluster；继续从 TrustedRequestContext / Run 解析。

### 11.5 两个短事务包住实际查询

```text
TX1
  lock Run
  validate active lease + state_version if required
  idempotency check tool_run_id
  insert/update ai_tool_runs status=running
  append TOOL_STARTED
COMMIT

actual typed repository call

TX2
  lock Run -> tool_run
  store result/error/duration
  if same valid lease generation and Run non-terminal:
      eligible_for_evidence=true
      append TOOL_SUCCEEDED/FAILED
  else:
      eligible_for_evidence=false
      append TOOL_RESULT_LATE
COMMIT
```

### 11.6 Tool retry

同 `tool_run_id + args_hash`：

```text
success/failed/timeout → 返回已持久化结果
running → 202/in-progress
hash mismatch → IDEMPOTENCY_KEY_REUSED
```

Planner 想做一次新的真实查询必须生成新的 `tool_run_id`。

### 11.7 Evidence consumption

Runtime Commit 消费 ToolResult 时必须：

```text
eligible_for_evidence=true
AND evidence_consumed_at IS NULL
AND tool_run belongs to same run/tenant/cluster
```

成功 Commit 同事务设置 `evidence_consumed_at`，防止同一个事实结果被两个并发 Commit 重复转成 Evidence。

### 11.8 统一 ToolResult Envelope：禁止“数据源失败 = 空数据”

A-C 所有 `/internal/v1/query/*` 统一返回 versioned `ToolResultEnvelope`，生命周期 `ai_tool_runs.status` 与结果质量分离：

```text
tool_run_id
status                 # pending|running|success|failed|timeout
result_quality         # complete|partial|none
complete               # bool
truncated              # bool
observed_at            # Query API 完成观测的 DB/server time
query_window_start     # RFC3339 absolute
query_window_end       # RFC3339 absolute
result_count
result_digest_sha256
data
source_errors[]
  - source
  - code
  - retryable
  - safe_message
```

判定固定为：

```text
所有必要数据源成功       -> status=success, result_quality=complete
部分子查询成功           -> status=success, result_quality=partial
必要数据源全部失败       -> status=failed,  result_quality=none
超时                     -> status=timeout, result_quality=none|partial
空数据但查询成功         -> status=success, complete=true, data=[]
```

因此 Kubernetes 三个子查询不得再静默吞错。若 Pods 成功、Nodes 失败，必须返回 `partial + source_errors`；如果都失败则 HTTP 使用 typed query error，同时 `ai_tool_runs` 记录 failed。Planner/RCA 可以消费 partial Evidence，但必须把完整性写入 Evidence metadata，不能把缺失部分解释成“无异常”。

### 11.9 Investigation 时间窗必须在 Run 创建时冻结

Run 创建由 Query API 在事务中取得 `DB_NOW`。对“最近 N 分钟/小时”的请求立即解析：

```text
time_range_end   = DB_NOW
time_range_start = DB_NOW - requested_duration
created_at        = same DB_NOW
```

若用户给定绝对时间窗则原样校验并保存。Investigation 的 metrics/logs/traces/alerts/changes Tool 请求统一携带绝对 `query_window_start/query_window_end`；生产 Investigation 不允许用 `minutes/hours/since` 在每次 retry 时重新以当前时间展开。

同 `tool_run_id + args_hash` 的 transport retry 必须读取第一次持久化的绝对窗口并返回/恢复同一执行；Planner 真正要求“刷新当前数据”时必须创建新的 `tool_run_id` 和新的绝对窗口。

### 11.10 ToolResult 上限固定，不在 A-C 引入 MinIO 作为前置依赖

A-C 固定生产上限：

```text
TOOL_RESULT_INLINE_MAX_BYTES = 262144
metrics max points           = 5000
logs max records             = 1000
traces max records           = 200
alerts max records           = 500
changes max records          = 500
knowledge max top_k          = 50
kubernetes max objects       = 5000
topology max nodes           = 5000
topology max edges           = 10000
```

所有 limit 必须由 Query API 服务端 clamp；Orchestrator 传更大值也不能突破。超过记录上限且 endpoint 可确定性分页/截断时返回 `truncated=true + original_count/returned_count`；超过 256 KiB 且无法安全确定性缩减时返回 `RESULT_TOO_LARGE`，不得把任意截断字节当合法 JSON。

A-C **不把 MinIO 作为必需组件**。只有后续明确增加 StorageAdapter 时才允许 Object First + MySQL Reference；在此之前，大结果问题通过服务端上限、分页和 deterministic truncation 解决。

### 11.11 Browser 只读 API 的 canonical allowlist 收敛

Agent A-C 主链只使用 `/internal/v1/query/*`，不能因为公共 API 当前存在契约断点就绕回 legacy handler。Browser API 另行原位整改。

当前 `AuthMiddleware` 的行为是：

```text
/api/v1/*
→ RequestAuthorizationContext(JWT + session + canonical tenant membership)
→ isCanonicalProtectedRoute(path)
→ 不在 allowlist => 403 permission_denied
```

因此“main.go 注册了 handler”不等于“产品当前可访问”。至少对下列产品入口逐个做路由合同测试：

```text
GET /api/v1/metrics/query
GET /api/v1/metrics/query_range
GET /api/v1/traces/{trace_id}
GET /api/v1/traces/{trace_id}/context
GET /api/v1/topology/node/{name}
GET /api/v1/infrastructure/nodes|pods|deployments|namespaces|hpa|vms
GET /api/v1/ai/runs/{run_id}
```

整改规则：

1. 先确认前端/产品是否仍需要该 route；不需要则删 handler/client 调用，不扩大 allowlist。
2. 需要保留时，handler 必须使用 `requestAuthorizationContext` 的 canonical tenant，不再依赖 `extractTenantID()` 的 `default` 或未验证 header/query hint。
3. cluster-scoped route 必须通过 Cluster Registry / tenant membership / resource resolver 验证 concrete `cluster_id`；不能仅把 `all` 当空过滤条件。
4. 原始 PromQL 代理若无法可靠注入 tenant/cluster matcher，则生产 profile 关闭该 passthrough，改走 typed metrics contract；不能仅因为登录成功就放开任意 PromQL。
5. `TraceContext` 中硬编码 VictoriaLogs URL 的直接请求必须移入 `LogRepository`/配置化 reader，并带 tenant/cluster scope；alert 关联也必须通过有 scope 的 repository，不读取无 tenant/cluster 过滤的全局内存集合。
6. 完成 handler scope 修复后，才把**精确 route/prefix**加入 `isCanonicalProtectedRoute()`；新增 contract test 证明未授权 tenant/cluster 为 403、合法请求为 200/typed no_data。
7. `/api/v1/ai/runs/{run_id}` 详情必须与现有 SSE/cancel 使用同一 tenant/run ownership 校验，不得重新代理到 Orchestrator 内存 RunStore。

本节是 Browser 产品可用性与隔离整改，不改变 `/internal/v1/query/*` 作为 Agent canonical read-only execution plane 的结论。

## 12. Evidence 与大结果存储：先复用现有 ai_tool_runs / ai_evidence，MinIO 不得伪装为已接线

当前代码已经有 `ai_tool_runs` 与 `ai_evidence` 表结构：`ai_tool_runs` 保存 Tool 的 `input_json/result_json/error_*`，`ai_evidence` 保存 `raw_ref/raw_digest_sha256/summary/metadata_json/provenance_fingerprint`。当前仓库没有发现已经完成生产接线的 MinIO Client/Storage Adapter，因此本阶段不得把 MinIO 写成“现有能力”或强依赖。

A-C 默认实现路径：

```text
/internal/v1/query/* 执行真实只读查询
→ Query API 在 ai_tool_runs 记录 ToolRun
→ 返回 bounded normalized ToolResult
→ Orchestrator 做 Interpretation / Hypothesis / RCA
→ Runtime Commit 引用 source_tool_run_id
→ Query API 校验 ToolRun 归属与 fencing
→ 同事务写 ai_evidence + evidence_consumed_at
```

每个 Runtime Evidence 至少保存：

```text
evidence_id
run_id
tenant_id
cluster_id
evidence_type / source_type
source_tool_run_id
tool_name
query_parameters / time_range（可放 metadata_json）
resource_identity（可放 metadata_json）
raw_ref
raw_digest_sha256
summary / normalized result
provenance_fingerprint
created_at
```

`source_tool_run_id` 必须指向当前 Run、Tenant、Cluster 下真实完成且仍允许消费的 `ai_tool_runs.tool_run_id`。`Runtime Commit` 不接受 Orchestrator 任意构造的 Tool 执行事实；Query API 必须反查 `ai_tool_runs`，核对 `run_id/tenant_id/cluster_id/status/result hash` 后再形成 Evidence。

本阶段统一采用“一个 ToolRun 对应一个主 Evidence Envelope”，多条 normalized facts 可以放在该 Envelope 的结构化 metadata/result 中。为避免 Recovery 或并发 Commit 重复落 Evidence，`0004_runtime_convergence.sql` 应增加：

```text
ai_tool_runs.evidence_consumed_at DATETIME(3) NULL
ai_evidence.source_tool_run_id CHAR(36) NULL
UNIQUE(run_id, source_tool_run_id)
```

Runtime Commit 消费条件：

```text
tool_run.status in terminal readable result states
AND tool_run.run_id = commit.run_id
AND tool_run.tenant_id = run.tenant_id
AND tool_run.cluster_id is authorized by run scope
AND evidence_consumed_at IS NULL
AND current execution lease is still valid
```

成功 Commit 与 `evidence_consumed_at` 更新必须处于同一个 MySQL 事务中。晚到 ToolResult、旧 `lease_epoch`、Cancel 后结果或不属于该 Run 的 ToolRun 一律不得转为 Evidence。

### 12.1 A-C 不引入 Object Storage 依赖；后续 StorageAdapter 必须独立验收

A-C 已在 11.10 固定 ToolResult 服务端上限，因此**结果超限不会触发隐式 MinIO 上传**。当前仓库没有生产可用的 MinIO Client/Storage Adapter，A-C 生产候选不得把 MinIO endpoint/access key 作为 Query API/Orchestrator 必需配置。

后续确需保存超过 inline 合同的原始 Evidence 时，必须先完整实现 StorageAdapter：

```text
Query API StorageAdapter
→ SHA-256
→ immutable object write
→ opaque object_ref
→ MySQL transactional reference
→ GC / retention
→ tenant/run authorization
→ integration + fault-injection tests
```

后续 StorageAdapter 启用后，对象键固定为：

```text
evidence/{tenant_id}/{sha256}
```

但 Orchestrator 只能看到 opaque `object_ref`，不得持有 MinIO endpoint/access key，也不得直接通过 object_ref 绕过 Query API 读取。

后续启用 Object Storage 时固定采用：

```text
Object First + Transactional Reference
```

允许“对象成功、MySQL 回滚”形成可 GC 孤儿对象；禁止“先提交 MySQL 引用、再上传对象”。

在 Storage Adapter 未实际实现、部署、测试前：

```text
raw_ref 可为空或表示现有受控来源；
不得把 MinIO 写成 A-C 的上线前置依赖；
不得在 Helm/NetworkPolicy 中凭空增加未被代码使用的 MinIO 权限。
```

LLM 输出只能作为 Interpretation / Hypothesis / RCA，不得伪造并标记指标值、日志行、Trace Span、时间戳或资源身份为事实 Evidence。

---

## 13. Recovery：复用现有 Snapshot，增加 Lease 驱动恢复

### 13.1 已实现基线

Query API 已有 `/internal/v1/control-plane/recovery/snapshot?run_id=...`，在一个 DB read transaction 中加载 Run、Plan/Step、ToolRun、Action、Approval 与 `last_event_sequence`。后续必须扩展这个 snapshot，而不是新建第二套 recovery API 数据模型。

### 13.2 Recovery Scanner

Scanner 只发现候选，不自行复制状态机规则：

```text
non-terminal
AND no active execution lease
AND status in auto-recoverable set
AND (runtime_wait_kind != retry OR DB_NOW >= retry_not_before)
```

A-C 自动恢复主要针对：

```text
created / planning / investigating
```

`awaiting_confirmation` 需要用户控制事件；`awaiting_approval/executing/verifying` 由 Stage D 规则处理。

候选发现后仍必须调用同一个 Claim API；Scanner 不直接 UPDATE lease。

### 13.3 Recovery 全局扫描使用独立 system capability

当前 `/internal/v1/control-plane/runs/unfinished` 不能继续复用普通 tenant-scoped `control_plane.runs.recover` 后直接 `ScanUnfinished()`。目标合同固定为：

```text
GET /internal/v1/control-plane/runs/unfinished?limit=200&cursor=...
capability = control_plane.runs.recover.global
principal_type = system
issuer = ai-orchestrator
tenant_id = empty
cluster_id = empty
```

只有专门的 Orchestrator Recovery Worker service identity 可使用该 capability。Query API 只返回 `run_id/tenant_id/primary_cluster_id/status/state_version/lease_epoch/lease_expires_at/runtime_wait_kind/retry_not_before`，必须分页，最大单页 `limit=200`，禁止返回全库 Artifact。

拿到候选后，Recovery Worker 为具体 Run 使用该 `tenant_id` 签发 tenant-bound Claim/Snapshot Context；`/recovery/snapshot?run_id=...` 继续执行 run tenant 校验。tenant-scoped Context 请求 `/runs/unfinished` 必须 403。Global scan 只能发现候选，最终 eligibility/ownership 仍只通过第 9 章 Claim API。

### 13.4 Tool 等待恢复

当：

```text
status=investigating
runtime_wait_kind=tool
pending_tool_run_ids=[...]
```

恢复器读取现有 `ai_tool_runs`：

- running 且 deadline 未到：等待/查询状态，不重复真实 I/O；
- running 且 deadline 到：Reconciler 收敛 timeout/failed；
- terminal + eligible + 未消费：可由新的 Runtime Commit 消费；
- terminal + ineligible：只保留审计；
- 根本没有对应 tool_run：说明旧 executor 可能在持久化计划后、真实调用前崩溃；可用**同一 tool_run_id**重新进入 Query API 的幂等入口，由服务端决定是否第一次执行。

### 13.5 LLM 等待恢复

LLM 不承诺 exactly-once。进入 LLM await 前至少持久化：

```text
runtime_wait_kind=llm
llm_call_id
prompt/input digest
provider/model
attempt
```

进程崩溃后不能伪造旧 Provider 调用结果；按预算和 retry policy 以新 provider attempt 继续，并记录原 call unknown/abandoned。

## 14. Agent 与产品入口：按当前代码收敛

### 14.1 普通 Chat：当前源码事实

当前 canonical Browser Chat 主链实际是：

```text
AiChat.tsx
→ POST /api/v1/ai/chat
→ Query API ProxyChat
   - JWT + MySQL session/tenant authorization
   - concrete canonical cluster resolution
   - capability=ai.chat
   - sign RunInvocationContext
→ Orchestrator /internal/v1/chat
   - verify user principal + ai.chat + tenant/cluster
   - 不创建 Investigation Run
→ brain.stream_sync(mode="chat")
→ chat_graph
```

“**不创建 Investigation Run**”这一点当前代码是成立的；但“普通 Chat 不采集实时数据”这一点**不成立**。当前 `build_graph(mode="chat")` 固定：

```text
entry = collect
collect -> clean
clean(light query) -> summarize
clean(non-light)   -> rca -> rag -> crewai -> summarize
```

也就是说，light query 只是在 **collect 之后**跳过深度诊断，并不会跳过实时采集。

当前 `node_collect()` 的真实行为如下：

| 数据/能力 | 当前 Chat 是否固定尝试 | 实际入口 | 当前问题 |
|---|---|---|---|
| 服务概览 | 是，每轮 | `get_service_list()` | 先尝试 `kg_graph._load_graph()` direct MySQL；失败后走 `/api/v1/services` |
| K8s 节点/Pod | 是，每轮 | `get_infrastructure()` | 走 `/internal/v1/query/kubernetes`，但在 helper 内自行把 Chat 语义重签为 `kubernetes.resources.read` system context，绕过 `InternalQueryClient` Tool gate |
| 告警事件/规则 | 是，每轮 | `_collect_alerts()` | 走 `/api/v1/alerts/events`、`/api/v1/alerts/rules` legacy/browser 查询面 |
| 日志 | 是，每轮 | `query_logs()` | 走 `/api/v1/logs/query`，不是 canonical internal Tool path |
| RED 指标 | 有具体 service 时固定尝试 | `query_metrics()` | 调 `/api/v1/services/{service}`；当前 canonical allowlist 未放行该详情 path，真实请求可 403 后被静默吞掉 |
| Trace | 有具体 service 时固定尝试 | `query_traces()` | 调 `/api/v1/traces?limit=5`，当前实现没有传 `service`，因此不是严格 service-scoped Trace |
| K8sGPT | 否 | `k8sgpt_diagnose()` | 仅显式请求，或 `intent=diagnosis` 且非 light query 时调用 |
| RAG/知识 | 非 light query 后进入 | `node_rag()` | 默认直接本地 ChromaDB；显式知识工具也不是 Runtime ToolRun 主链 |
| RCA | 非 light query 后进入 | `full_rca_analysis()` | 会再次查询拓扑/服务/指标/基础设施，其中仍有 legacy `/api/v1/*` 路径 |

因此当前 Chat 的准确描述应是：

> **非 Run，但不是“纯问答”。它是一个带固定实时观测采集的轻量诊断图，只是没有 Run Lease、ai_tool_runs/Evidence 消费闭环，也没有使用完整运维图的 wait_approval/execute/verify 节点。**

当前 `stream_sync()` 在 Chat 图结束后还会根据分析文本和异常证据生成 `suggestion` SSE 卡片；但 canonical Browser 主链并没有把旧 `/api/v1/ai/suggestion/execute` 纳入受支持的 Query API 入口，因此现阶段不能把这张卡片等同于可用的生产执行能力。

另外，`node_collect()` 写入、`node_crewai()` 读取 `logs_data`，但 `AgentState` 当前没有声明该字段。整改时必须明确加入 state schema，或在移除 Chat 固定 collector 时同时删除该遗留 channel，不能继续依赖 LangGraph 对未知 state key 的隐式行为。

#### 14.1.1 Chat 专项源码核对最终判定

本轮沿真实调用链完成了闭环核对：

```text
observability-frontend/src/pages/ai/AiChat.tsx
  -> POST /api/v1/ai/chat
ai-apm-query-go/internal/api/settings.go::ProxyChat
  -> signed RunInvocationContext(capability=ai.chat)
ai-orchestrator/main.py::internal_chat
  -> brain.stream_sync(mode=chat)
ai-orchestrator/orchestrator.py::build_graph(mode=chat)
  -> collect -> clean -> ...
ai-orchestrator/orchestrator.py::node_collect
  -> tools.py / rca.py realtime reads
```

最终结论必须按“**当前事实**”与“**整改目标**”分别理解：

| 项目 | 当前源码事实 | A-C 目标合同 |
|---|---|---|
| Chat 是否创建 Investigation Run | 否 | 否；只有显式“开始调查”才创建 Run |
| Chat 是否每轮固定采集实时数据 | **是** | **否，删除固定 collector** |
| light query 是否能避免实时采集 | 否；light 分流发生在 `collect` 之后 | 是；普通 Chat 在任何 live query 前完成意图分类 |
| 服务/K8s/告警/日志 | 当前每轮固定尝试 | 普通 Chat 不自动读取 |
| RED/Trace | 当前识别到 service 后固定尝试 | 迁入 Investigation ToolRun |
| K8sGPT | 条件调用 | 仅 Investigation/显式受控工具 |
| RCA | 非 light Chat 会进入并二次查询实时事实 | 从普通 Chat 移除，迁入 Investigation |
| 实时查询审计 | 未统一形成 `ai_tool_runs -> Evidence` | 必须由 Run Lease + InternalQueryClient + ai_tool_runs + Evidence 闭环 |
| 数据面边界 | 存在 public `/api/v1/*`、direct KG MySQL、capability 重签旁路 | Orchestrator Runtime 只经 canonical `/internal/v1/query/*` |
| 执行 | chat_graph 无 execute node，但可生成 suggestion，另有 legacy execute endpoint | 普通 Chat 不生成可执行 Action；Stage D 独立处理 |
| 会话状态 | AsyncSqliteSaver/本地 session checkpoint | 仅 UX 会话状态，不成为 Run SoT/Recovery 权威 |

因此，后续 AI 编码不得把“保留 Chat 的固定采集、仅优化 collector”作为可接受实现。**必须先在 Chat 图入口完成分类，再决定纯对话；凡需要当前运行事实、跨数据源关联或根因分析，进入显式 Investigation Run。**

还必须修复一个当前真实 state schema 缺陷：`logs_data` 被 `node_collect()` 产生、被 `_deterministic_diagnosis()`/`node_crewai()` 消费，但 `AgentState` 与 `_run_dag()/stream_sync()` 初始 state 均未声明/初始化该字段。阶段 B 改造时：

- 如果 `node_collect` 迁入 Investigation/full graph 后仍保留日志证据，必须在 `AgentState` 和初始 state 中显式增加 `logs_data: str`；
- 普通 Chat 删除固定日志采集后，不得为了兼容旧 Chat 再保留一个无审计的 `logs_data` live-query 旁路；
- 增加 checkpoint round-trip 测试，证明日志证据在需要的图中不会因 state schema 丢失。

### 14.2 A-C 目标：普通 Chat 删除固定实时观测采集

为了让 Persistence Ownership、Run Lease、ToolRun、Evidence 与授权边界真正只有一套，A-C 不保留当前“Chat 先固定采集一轮实时数据”的设计。目标图必须先分类、后决定是否创建调查，不能再由 `collect` 先读数据再判断用户问题类型：

```text
/internal/v1/chat
  -> classify_chat_intent
      -> GENERAL_CHAT / KNOWLEDGE_ONLY
           -> LLM / static knowledge / conversation history
           -> no live observability query
      -> LIVE_FACT_OR_DIAGNOSIS_REQUIRED
           -> investigation_required + CTA
           -> user explicitly starts investigation
           -> Query API creates Run
```

目标定义如下：

1. **普通 Chat 仍是非 Run、非执行入口**，可以使用 LLM、历史会话上下文、静态知识/已授权知识内容；
2. `build_graph(mode="chat")` 不再以 `node_collect` 为入口，不再自动调用 services/logs/metrics/traces/alerts/K8s/topology/changes；
3. Chat 不再进入 `node_rca` 以及任何会二次读取实时运行数据的路径；
4. 用户问题一旦需要“当前/最近/实时”的集群、服务、指标、日志、Trace、告警、K8s 状态或根因证据，返回明确的 `investigation_required`/CTA，由用户显式触发 `createRun()`；
5. Investigation Run 才允许：

```text
Run Lease
→ Planner/Tool Registry
→ InternalQueryClient
→ /internal/v1/query/*
→ ai_tool_runs
→ Evidence
→ RCA/Hypothesis
→ Run Event/SSE
```

6. 不允许 Orchestrator 在 Chat 内自行把 `ai.chat` 提升/重签为 `observability.*.read`、`kubernetes.resources.read` 等事实查询 capability；
7. 不允许 Chat 再通过 `signed_query_api_request()` 访问 `/api/v1/services`、`/api/v1/logs/query`、`/api/v1/traces`、`/api/v1/alerts/*` 等 legacy/browser 数据接口；
8. `get_service_list()` 在 Chat 主链中的 `kg_graph._load_graph()` direct-MySQL 快路径必须删除；知识图谱读取若仍属于 Investigation 工具，应经 Query API canonical repository/tool boundary；
9. Chat 不创建 `ai_actions`，不执行 shell/K8s，不产生“可直接执行”的处置卡；实时诊断后的 Action Preview 属于 Investigation Run；
10. 当前跨服务 Context 不新增第四种 `ChatQueryContext`。A-C 保留现有 trusted `/internal/v1/chat` ingress；由于现有 `RunInvocationContext.cluster_scope` 契约要求具体 cluster，**当前阶段普通 Chat 也必须选择 concrete canonical cluster**。如果未来产品要求真正 cluster-independent Chat，再单独做契约版本演进，不能继续由前端发送 `all` 绕过。

当前 `InternalQueryClient` 通过 `context_ref` 派生 synthetic run_id 的历史做法，不得被当作生产 Run identity；生产实时调查必须使用 Query API 创建的真实 `run_id`。

### 14.2.1 当前 AiChat 必修项

当前 `AiChat.tsx` 仍：

```text
clusterId default = "all"
POST /ai/chat with cluster_id=all
executeSuggestion -> /ai/suggestion/execute
finalReport -> /ai/final_report
```

而 Query API `ProxyChat` 当前会对 missing/`all` cluster fail-closed，因此必须修改：

1. 普通 Chat 与 Investigation 在发送前都要求 concrete canonical cluster；当前阶段不再保留“知识 Chat 可用 all”的描述，因为真实跨服务 Context 仍要求 cluster scope；
2. 对需要实时事实的问题，Chat 不再偷偷采集数据，而是展示“开始调查”入口；用户确认后调用 `createRun()`；
3. 移除/隐藏 legacy `executeSuggestion`、`finalReport` 生产调用；调查报告统一由 Run detail/report 数据读取；
4. Chat 不再根据 `stream_sync()` 的分析结果生成可执行 suggestion；若短期为兼容仍收到该事件，前端只忽略/展示迁移提示，不显示“确认执行”；
5. `exec_result` 不再作为 Chat“执行后继续深入分析”的生产主链输入；该闭环迁入 Action/Verification/Run Event。

### 14.3 显式“开始调查”

```text
Frontend createRun
→ Query API AuthZ + concrete cluster
→ CreateWithOutbox transaction
   - ai_runs(status=created)
   - RUN_CREATED event
   - ai_run_outbox pending invocation
→ dispatcher → /internal/v1/run-invocations
→ Orchestrator verifies RunInvocationContext
→ Claim execution lease
→ refresh/recovery snapshot
→ planning/investigating
→ InternalQueryClient → /internal/v1/query/*
→ ai_tool_runs → Evidence → Hypothesis/RCA
→ success/partial/failed
→ SSE DB replay/tail
```

### 14.4 Planner Budget

预算至少持久化在 Runtime metadata/snapshot：

```text
max_steps
max_tool_calls
max_parallel_tools
max_wall_time
max_llm_calls
max_evidence_items
max_followup_rounds
used_*
```

计数在真正产生对应副作用**之前**原子预留，不能仅存 Python 内存，也不能因为 timeout/5xx 把已经发出的调用“退款”：

```text
Tool: TX1 创建 running ToolRun 时 used_tool_calls++；请求已发到数据源后无论成功/失败/超时都消耗预算。
LLM: 调用 Provider 前先 Runtime Commit 持久化 runtime_wait_kind=llm + llm_call_id + used_llm_calls++；Provider 调用失败也不回退 used_llm_calls。
```

`max_parallel_tools` 不能只靠 Python Semaphore；Query API 在 ToolRun TX1 中按同一 `run_id + active lease_epoch` 统计 running ToolRun 并拒绝超过预算的启动。预算耗尽统一形成 `partial/BUDGET_EXHAUSTED`。

## 15. Stage D：复用现有 Action/Approval/Verification，不造 ActionV2

当前数据库与 Query API 已有：

```text
ai_actions
ai_approval_decisions
ai_verifications
```

并且当前控制面 Action API 明确仍禁止真实执行。Stage D 应在这些模型上增强，不新建另一套 Action 状态机表。

### 15.1 Run 与 Action 的关系

为兼容当前 V9.2 RunStatus，保留 Run 的高层执行状态：

```text
awaiting_approval
executing
verifying
```

具体每一个变更动作的幂等、审批、resourceVersion、执行、回滚和验证事实由 `ai_actions`/approval/verification 持久化。

这样既不破坏现有跨语言 RunStatus，也避免把每个动作细节塞进 Run row。

### 15.2 A-C 期间的硬门禁

```text
EXECUTION_AFTER_APPROVAL = 0
K8s write RBAC = disabled / grantK8sWrite=false
execute_k8s / execute_shell tool = execution_state=disabled
```

A-C 可生成 Action Preview，但不得进入真实外部 mutation。

### 15.3 Stage D 必补字段

在现有 `ai_actions` / `ai_approval_decisions` 原位增加或确认：

```text
requester
approver
action_hash
preview_hash
resource_id
resource_version
expires_at
idempotency_key
execution_started_at
execution_finished_at
rollback_ref
verification_id
```

R3/R4 必须 requester != approver。执行前重新获取 resourceVersion；变化则 `REQUIRE_RECONFIRMATION`。

现有 `AIActionDAO.Create()` 的 duplicate-key 行为不能直接作为 Stage D 完整幂等。创建/执行 Action 时必须反查既有记录：

```text
same run + idempotency_key + same action_hash/action_type/cluster/resourceVersion/payload
→ idempotent replay，返回同一 action

same run + idempotency_key but semantic payload differs
→ 409 IDEMPOTENCY_KEY_REUSED
```

审批同样必须验证 `approval_id/action_id/action_hash/approver/decision` 语义一致，不能把任意 duplicate key 静默吞掉。

### 15.4 Stage D 必须物理隔离 Action Executor

当前 `ai-orchestrator/execution_adapter.py` 与 `credential_broker.py` 只允许保留为领域原型/单测参考。Stage D 生产实现固定新增独立 Deployment：

```text
ai-action-executor
  - no LLM / Planner / RCA
  - no Run state authority
  - no direct MySQL
  - only ActionExecutionContext verification
  - only approved action types
  - scoped credential broker client
  - target UID/resourceVersion precondition
  - execute / rollback primitive
  - result returned to Query API
```

生产网络/RBAC 固定为：

```text
ai-orchestrator    -> Kubernetes/OpenStack/Shell WRITE = DENY
query-api role=api  -> production mutation credential       = DENY
ai-action-executor -> approved target scope only            = ALLOW
```

Query API 继续是 `ai_actions/ai_approval_decisions/ai_verifications` 的唯一持久化 Owner。Action Executor 不保存第二套 Action SoT。外部 mutation 已发生但响应丢失时，Action 进入 `execution_unknown`，必须先 Reconcile 目标实际状态，再决定确认成功/回滚/重新执行；禁止对未知写操作盲目 retry。当前内存 `_executed` map 不能作为生产幂等依据。

### 15.5 执行模式迁移

最终统一为：

```text
EXECUTION_MODE=disabled | manual | approved
```

迁移期若旧 `EXECUTION_AFTER_APPROVAL` 仍存在：

- `EXECUTION_MODE` 为唯一新权威；
- 未设置新变量时，为兼容只映射到 `disabled`，不要让旧 `1` 自动打开 approved；
- 完成迁移后删除旧变量和分支。

## 16. Trace 数据链整改：固定 ClickHouse `trace_spans` 为平台 Trace SoT

当前 ingest 主入口使用 `nil SpanSink`，旧 ClickHouse Span writer 已删除。因此 OTLP/DeepFlow Span 即使被解析，也不会经当前 ingest 主链写入 `trace_spans`。本版不再保留二选一，最终职责固定为：

```text
Trace Persistent SoT = ClickHouse trace_spans
Trace Schema Owner    = platform schema/init migration
Trace Write Owner     = ingest ClickHouseSpanSink
Trace Read Owner      = Query API traceRepo
DeepFlow              = Span input/enrichment source，不是持久化权威
```

唯一主链：

```text
OTLP / DeepFlow Span
→ normalize
→ ClickHouseSpanSink
→ ClickHouse trace_spans
→ Query API traceRepo
→ /internal/v1/query/traces
→ ai_tool_runs
→ Evidence
→ RCA
```

`SpanSink=nil` 只能存在于显式 unit-test fixture；production/candidate ingest 未配置 ClickHouseSpanSink 必须 readiness fail-closed，禁止“接收成功但静默丢 Span”。

### 16.1 Trace 投递与去重语义

Trace 固定为 **At-Least-Once ingestion**。Normalize 后至少形成 `tenant_id/cluster_id/trace_id/span_id/start_time_ns/source/span_dedup_key`。`span_dedup_key` 固定为：

```text
SHA256(tenant_id || cluster_id || source || trace_id || span_id || start_time_ns)
```

ClickHouse 写入或 Query Adapter 必须实现确定性去重。若表引擎依赖后台 merge，Query API fresh trace 查询仍必须按 `span_dedup_key` collapse/argMax，确保同一逻辑 Span 只返回一条事实。

阶段 C 必须测试：重复投递同一 Span 2 次只形成一个逻辑 Trace/Evidence；SpanSink unavailable 时 readiness=false/写入 fail-closed，不允许 silent drop。

---

## 17. 网络与凭据边界

### 17.1 Orchestrator

Orchestrator 允许的网络目标原则上只有：

```text
Query API
DNS
LLM Egress Path
必要且明确批准的内部 Knowledge Provider（若存在）
```

禁止直接访问：

```text
MySQL
VictoriaMetrics
VictoriaLogs
ClickHouse
MinIO
Kubernetes API
```

Orchestrator 不得持有这些数据面的访问凭据。完成阶段 B cutover 后，如果 Orchestrator 不需要直接调用 Kubernetes API，应设置：

```text
automountServiceAccountToken: false
```

或使用无数据面权限的最小 ServiceAccount。

### 17.2 Query API

Query API 按当前职责可访问：

```text
MySQL
VictoriaMetrics
VictoriaLogs
ClickHouse
Kubernetes API
```

只有在第 12.1 节 Object Storage Adapter 真正实现后，才允许按最小权限新增 MinIO/Object Storage 出站和凭据；当前不得为未接线能力提前授予。

必须使用最小权限凭据和 tenant/cluster/resource scope 校验。跨集群 Kubernetes 凭据只能由 Query API 的受控 credential resolver 按 `cluster_id` 解析，不得由 Orchestrator 传 kubeconfig/token。

当前 Helm 的 `queryApi.k8sInsecureSkipVerify` 为 `"true"`，只允许保留在本地单节点/自签验证 profile。生产 profile 必须：

```text
K8S_INSECURE_SKIP_VERIFY=false
cluster credential/registration 提供受信 CA bundle 或等价 server identity 验证材料
API endpoint host/SAN 与注册身份匹配
证书校验失败 => fail closed，不降级到 insecure
```

若多集群的 CA/endpoint 各不相同，应由现有 cluster credential resolver 随 `cluster_id` 一起返回 TLS 配置，而不是只靠一个全局 `K8S_INSECURE_SKIP_VERIFY` 开关。

### 17.3 LLM Egress：固定走集群内专用 Egress Proxy

`egressDefaultDeny` 当前默认 false，应作为 P1 上线门禁灰度开启。标准 Kubernetes NetworkPolicy 不能可靠表达公网 FQDN 动态白名单，因此本合同不再保留 CNI FQDN Policy 作为并列生产方案。生产固定主链：

```text
AI Orchestrator
→ in-cluster ai-llm-egress-proxy
→ External LLM Provider
```

Orchestrator NetworkPolicy 只放行 Query API、`ai-llm-egress-proxy` 与 CoreDNS/kube-dns UDP/TCP 53；不能直连公网 Provider。NetworkPolicy 使用 `namespaceSelector + podSelector` 选择 Proxy Pod，不依赖固定 ClusterIP。

Egress Proxy 必须是 LLM provider 专用出口：

- provider host/SNI allowlist；
- 上游 TLS 证书校验；
- 请求大小、超时、并发限制；
- 禁止访问 RFC1918/集群网段/metadata endpoint；
- access log 不记录 prompt 全文、Authorization/API key；
- Provider API key 最终由 Proxy 持有；Orchestrator 只持访问 Proxy 的内部服务凭据。阶段 C 结束时删除明文 Provider Key 下发链。

### 17.3.1 LLM Secret 迁移边界

当前 `/api/v1/settings/llm/internal` 不能按现状进入生产候选：它虽然要求 `X-Internal-Token`，但返回的是解密后的 API Key，认证强度却低于 `/internal/v1/query/*`。

Stage C 迁移固定分两步，最终状态没有二选一：

**迁移桥接期：**

```text
保留现有 TrustedRequestContext 类型
固定 capability = llm.config.read
Orchestrator 每次拉取时新签 TrustedRequestContext
Query API 同时验证 service token + signature + issuer/audience + capability + shared replay
endpoint 固定迁入 /internal/v1/settings/llm
旧 /api/v1/settings/llm/internal 只允许做 internal-only 兼容转发，不执行第二套 Secret 读取逻辑
公共 Ingress 不路由两个 internal 配置 path
全链路 TLS，日志/Trace 永不记录 API key
```

**Stage C 完成态：**

```text
External Provider API Key Owner = ai-llm-egress-proxy
Orchestrator = 仅持 Proxy workload credential
Query API -> Orchestrator Provider API Key 下发 = disabled/removed
/api/v1/settings/llm/internal = removed
/internal/v1/settings/llm = 不返回 Provider Secret；仅允许返回经授权的非敏感模型/路由元数据（确有调用方时）
```

因此 `llm.config.read` 只是迁移桥接 capability，不得成为长期的 Secret 分发机制。Stage C 发布门禁必须验证 Orchestrator Pod 环境、Secret volume、API 响应和日志中均不存在外部 Provider API Key。

同时修复 Browser Admin LLM 工具：`ModelsLLM` 对任何 caller-supplied/default `base_url` 都必须调用与 `SaveLLMSettings/TestLLMConnection` 相同的 `validateLLMBaseURL()`；Provider 请求统一封装，避免以后再出现某个 endpoint 遗漏 SSRF 校验。

### 17.4 内部传输安全

Lease token、Trusted Context、内部服务凭据都属于敏感材料。服务身份和签名只能证明“谁发的/内容是否被改”，不能提供传输机密性。

因此 Orchestrator ↔ Query API、Query API ↔ Egress Proxy 等携带敏感内部头/body 的链路必须使用经过验证的 TLS：

```text
优先：mTLS / service mesh workload identity
或：内部 HTTPS + 双向/服务端证书校验 + 独立服务身份认证
```

禁止以明文 HTTP 跨可被其他工作负载监听的网络传递 `lease_token`、Authorization 或 Trusted Context。日志、中间件 access log、APM Trace 必须显式 redact 这些字段。

`/internal/v1/*` Runtime/Lease/Tool/Security 接口必须只通过内部 Service 暴露，公共 Ingress/NodePort 不得路由这些 path。NetworkPolicy 只允许明确的 Orchestrator/内部组件访问；浏览器 JWT 即使有效，也不能直接调用 internal API。

---

## 18. Schema 与迁移治理：从当前 0001~0003b 继续

当前仓库已有 versioned migrations：

```text
0001_control_plane_baseline.sql
0002_ai_runtime.sql
0003_ai_runtime_v2.sql
0003_platform_audit.sql
0003b_ai_runtime_recovery.sql
```

`schema-migrator` 已是唯一 DDL Owner。这部分是**已实现基线**。

### 18.1 强制迁移策略

1. 不修改已经发布/可能已应用的 0001~0003b 内容来“就地重写历史”；
2. 本轮新增统一使用后续 migration，例如：

```text
0004_runtime_convergence.sql
```

3. `0004` 原位 ALTER 当前表，新增 Lease、Runtime metadata、Commit idempotency、Replay guard、ToolRun 幂等字段；
4. backfill 必须显式、可重复、不会把已有 Run 错判为可自动恢复；
5. migration 先 expand，再部署兼容代码，再启用新行为；不在一个滚动窗口内同时删旧列/改 enum wire 值。

### 18.2 0004 必须包含的 Runtime Convergence 内容

```text
ALTER ai_runs: lease_*, runtime_wait_kind, retry_*, last_failure_code, runtime_metadata_json
CREATE ai_runtime_commits
CREATE ai_run_claims
CREATE ai_context_replay_guard
ALTER ai_control_commands: payload_hash, response_json, completed_at
ALTER ai_tool_runs: args_hash, lease_epoch_at_start, executor_id, deadline_at,
                    eligible_for_evidence, evidence_consumed_at
```

如现有 migration 已有同义列，以真实 schema 为准，不重复创建。

### 18.3 readiness 与滚动升级：复用当前 required-set 语义

当前 `migrations.RequireCurrentVersion()` 的真实语义是：

```text
对当前二进制 embed 的每一个 required migration：
  DB 必须存在同 migration_id
  checksum 必须一致
DB 中存在当前二进制不知道的“后续 migration”不会被判错
```

因此当前实现已经不是“DB version 必须精确等于二进制最新 version”，无需再新增另一套 compatibility-range 机制。正确发布顺序是：

```text
1. 0004 必须保持 additive/backward-compatible
2. schema-migrator 先应用 0004
3. 旧 Query API 仍只校验 0001~0003b，可继续运行
4. 滚动部署新 Query API；新二进制开始要求 0004
5. 等所有旧二进制退出、回滚窗口关闭后，未来独立 migration 才允许 contract/delete
```

Query API runtime 继续只调用只读 `RequireCurrent/RequireCurrentVersion`，不得运行 DDL。任何新 migration 如果会让旧二进制在“数据库先升级、应用后升级”的窗口内无法运行，就不满足本轮 expand 要求，必须拆成多个发布。

## 19. HA、RWO PVC、Checkpointer、WAL 与当前部署边界

### 19.1 Run correctness 与 Orchestrator PVC 分离

当前 ai-orchestrator Deployment 挂载 `orchestrator-data` RWO PVC，承载 ChromaDB/SQLite/checkpoint；注释也明确多副本需先外部化存储。

因此：

- `orchestratorReplicas=1` 仍是当前部署安全基线；
- Run 的权威恢复不得依赖 AsyncSqliteSaver、RWO PVC 或本地 Run fallback；
- Run persistence/lease/event/tool facts 全部在 Query API/MySQL 后，即使 Orchestrator 本地 checkpoint 丢失，也应能由 recovery snapshot 重建调查；
- 本地 SQLite 可以继续作为 Chat/session checkpoint，但必须在文档和代码里明确“非 Run SoT”。

### 19.2 本机如何验证双 Executor

单节点不强行把 Deployment 扩成两个同时挂 RWO PVC 的生产 Pod。阶段 A 可以用：

- 两个无共享 PVC 的测试进程；或
- 专用测试 Deployment，禁用 Chroma/session persistence；或
- Go/Python integration harness 同时模拟两个 executor_id。

验证 DB Lease/Fencing 即可。真正 Orchestrator 多副本运行仍在外部化 session/knowledge state 后单独验收。

### 19.3 Event Collector

当前 Event Collector 已支持 K8s Lease leader election、ClickHouse health、retry queue、WAL backlog metrics。单节点环境只能验证 leader-election 逻辑/进程切换，不能声称完成真实多节点 DaemonSet failover；相应结果标记 `BLOCKED_BY_ENV`。

### 19.4 投递语义

```text
WAL = durability
Run outbox = RunInvocation eventual dispatch
business idempotency = dedup/retry safety
delivery = at-least-once
```

禁止声称 HTTP/SSE/Outbox/Tool 是 exactly-once。

### 19.5 Query API 后台角色拆分与扩缩容

生产阶段 C 必须完成第 2.1 节角色拆分：`role=api` 可水平扩容；`run-dispatch` 多副本依赖 DB dispatch fencing；`alert-eval` 多副本依赖 Kubernetes Lease，仅 Leader 执行。固定启动行为：

```text
role=api          -> 禁止启动 RunDispatchLoop / StartAlertEvaluation
role=run-dispatch -> 只运行 dispatch worker + health
role=alert-eval   -> 只运行 leader election + alert evaluation + health
unknown role      -> startup fail
```

### 19.6 Alert Evaluation Leader 与持久状态

Stage C migration 新增：

```sql
CREATE TABLE alert_rule_runtime_state (
  rule_id VARCHAR(128) NOT NULL,
  last_triggered_at DATETIME(3) NULL,
  breach_streak INT NOT NULL DEFAULT 0,
  last_eval_state VARCHAR(32) NOT NULL DEFAULT 'unknown',
  state_version BIGINT NOT NULL DEFAULT 0,
  updated_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  PRIMARY KEY(rule_id)
);
```

`alert-worker` 使用 Kubernetes Lease `aiops-alert-evaluator`：`leaseDuration=30s`、`renewDeadline=20s`、`retryPeriod=5s`。丢失 Leadership 后立即取消当前 evaluation context，禁止生成新 Alert/Webhook。每轮从 MySQL 恢复/更新 `breach_streak/last_triggered_at`；进程内 map 只能作本轮缓存。Webhook 使用稳定 `alert_event_id` 作为 Idempotency-Key，语义为 at-least-once。

### 19.7 MySQL 控制面 HA/DR 合同

生产候选 MySQL 必须满足以下语义合同，具体 HA 产品不由本仓库虚构：

```text
engine            = MySQL 8.4 / InnoDB
write endpoint    = single writable primary semantics
runtime authority = primary read/write endpoint only
DB time           = CURRENT_TIMESTAMP(3) on authority primary
ACK durability    = 已返回成功的 Runtime Commit/Lease/Approval/Action 在正常 failover 后不丢失
PITR              = enabled
backup restore    = verified
schema checksum   = schema-migrator enforced
```

Runtime/Lease/Replay/Approval/Action 权威请求不得发往异步只读副本。Stage D 前必须在候选环境执行 primary failover，验证已 ACK 的 Runtime Commit/Approval/Action 记录存在且 version/sequence 不回退；这些控制记录目标为 **RPO=0 at acknowledged transaction boundary**。本机单节点对此标记 `BLOCKED_BY_ENV`。

### 19.8 Control Plane 自观测

至少暴露：

```text
aiops_runtime_active_runs
aiops_runtime_commit_total{result}
aiops_runtime_commit_duration_seconds
aiops_run_lease_claim_total{result}
aiops_run_lease_renew_total{result}
aiops_run_lease_lost_total
aiops_run_fencing_reject_total{reason}
aiops_outbox_pending
aiops_outbox_claimed
aiops_outbox_oldest_pending_seconds
aiops_outbox_reclaim_total
aiops_tool_runs_active
aiops_tool_runs_total{status,result_quality}
aiops_tool_duration_seconds{tool}
aiops_tool_result_truncated_total{tool}
aiops_recovery_candidates
aiops_recovery_total{result}
aiops_replay_guard_total{result}
aiops_sse_connections
aiops_sse_replay_events_total
aiops_llm_calls_total{result}
aiops_llm_duration_seconds
aiops_alert_worker_leader
aiops_alert_evaluation_total{result}
aiops_action_execute_total{result}
```

日志/Trace correlation 固定携带可用时的 `run_id/tool_run_id/commit_id/event_id/action_id/lease_epoch/trace_id`；禁止记录 lease/dispatch token、Authorization、Provider API key、K8s credential 或完整敏感 prompt。阶段 C 必须配置 Outbox oldest age、Lease lost 激增、Recovery backlog、Tool partial/timeout、SSE replay lag、LLM egress failure 告警。

## 20. Python、依赖与工程基线

Python 运行基线统一为容器使用的 Python 3.12，并与 `requirements` 中 LangGraph 1.x 及其他依赖声明一致。

必须使用锁文件和统一 Docker/CI 环境，避免开发机 Python 3.9、LangGraph 0.2.x 等环境造成测试无法收集却被误判为代码失败。

禁止：

- 开发机依赖版本作为生产事实；
- 未锁定的浮动大版本依赖；
- “本机 import 成功”替代容器验证；
- 测试文件存在即宣称测试通过。

### 20.1 LLM 运行模式门禁

现有配置语境统一使用：

```text
LLM_MOCK = true | false
```

本轮不再新增第二个 `LLM_MODE` 配置，避免两个开关同时存在并产生冲突。

Mock 只允许单元测试、开发演示和明确标记的本机测试 profile。进入 `CONTROLLED_AI_INVESTIGATION_CANDIDATE` 前必须满足：

- 实际运行 Pod 中 `LLM_MOCK=false`；
- Provider credential/endpoint/model 均已配置并通过 readiness 检查；
- LLM timeout/retry 受 Planner Budget 约束；
- Mock 返回不得进入候选/生产 RCA 历史并被误认为真实模型判断；
- readiness/启动日志必须明确显示当前是否为 Mock，但不得打印 API key。

如果 Python 源码直接启动仍存在 Mock 默认值，生产 Helm/manifest 必须显式覆盖为 false，并以**运行实例实际环境/健康检查**证明最终值；不能只因为 values 文件写了 false 就判定验收通过。

如果未来确实要迁移为枚举式 `LLM_MODE=mock|real`，必须在一个明确兼容窗口内完成旧 `LLM_MOCK` 的弃用和删除；两个配置同时存在且值冲突时必须 fail-closed，不能定义隐式优先级。

---

## 21. 测试状态、代码基线验证与新增测试

### 21.1 当前仓库验证记录

本轮无法把 GitHub 源码 clone 到执行容器，因此**没有在本轮执行当前 HEAD 的仓库测试**。仓库最近提交记录包含 Go/Python/Frontend/部署验证结果以及 `run_persistence=remote` 的真实环境修复记录，但这些只能作为历史证据，不能替代当前整改目标提交的重新验证。

因此本报告的代码事实结论来自当前 HEAD 源码、迁移、契约和 Helm 配置的逐项复核；任何“PASS”必须在后续实现目标提交中重新执行对应命令后才能填写。最终实现完成时必须在**目标提交**重新运行全部测试，不能用历史提交的绿灯替代。

### 21.2 新增/强化测试

必须新增：

1. production remote cache miss + Query API unavailable → fail closed，绝不返回 fallback Run；
2. cancel client 必须发送 expected_version + command_id；重复 command 返回相同结果；
3. Runtime Commit 响应丢失 → 同 commit_id 返回原结果；
4. 双 executor claim → 单 owner；old epoch commit fenced；
5. `runtime_wait_kind=retry` 在 retry_not_before 前不可 claim；
6. Event AppendTx 与 Runtime Commit 回滚不留下孤立 event/sequence；
7. `/internal/v1/query/*` 同 tool_run_id 不重复真实查询；
8. tool result late/old epoch → audit only，不能生成 Evidence；
9. tool result 只能被一个 Commit consume；
10. SSE reconnect/Last-Event-ID/retention/多 query-api 实例 DB tail；
11. Chat 专项契约测试：`你好/解释术语` => 0 次 live InternalQuery；`有哪些服务/查看实时状态/诊断根因` => 普通 Chat 不先采集，返回 `investigation_required`；显式“开始调查”后才创建且只创建 1 个 Run；
12. AiChat 默认 `all` 被移除：未选择 concrete cluster 时前端禁用发送并提示选择集群；绕过前端直接发送 `cluster_id=all` 仍由 Query API fail-closed；
13. capability 防提升：以 `ai.chat` 进入 Orchestrator 后，任何 helper 尝试自行签成 `kubernetes.resources.read`/`observability.*.read` 必须失败；只有 Query API 授权的 Investigation/Tool context 可调用对应 internal query；
14. Runtime 数据面旁路测试：Chat/Investigation Runtime 禁止直接 MySQL/ClickHouse/VM/VLogs/K8s；`get_service_list -> kg_graph._load_graph` 不得出现在 Runtime 调用链；
15. `logs_data` state schema/checkpoint round-trip：Investigation/full graph 需要日志证据时字段必须显式声明、初始化并可恢复；普通 Chat 不产生该 live evidence；
16. legacy `executeSuggestion/finalReport` 不再作为 Browser 生产主链可达，chat_graph 不包含 execute/verify 节点；
17. migration 0004 在真实 MySQL 8.4 上 fresh + upgrade 两条路径通过；
18. outbox dispatcher：claim 后模拟进程崩溃，lease 到期后另一 dispatcher 能原子 reclaim 并成功 deliver；并发 reclaim 只能一个成功；旧 dispatch epoch/token 的 Deliver/Retry 必须被拒绝；
19. Recovery global scope：tenant-scoped recovery 调 `/runs/unfinished` 必须 403，只有 `control_plane.runs.recover.global` system identity 可分页扫描；
20. Kubernetes internal query：部分子查询失败 => partial；全部失败 => failed；成功空集合 => complete success；
21. absolute window：同 tool_run_id transport retry 的 `query_window_start/end` 完全一致，refresh 使用新 tool_run_id；
22. ToolResult limits：服务端 clamp、deterministic truncation、`RESULT_TOO_LARGE`；
23. Query API role：`role=api` 不启动 worker；run-dispatch 多副本 fencing；alert 两副本仅 Leader 评估且切换后从 MySQL 恢复状态；
24. Trace：SpanSink unavailable 时 candidate readiness fail；重复 span 只形成一个逻辑 Trace/Evidence；
25. MySQL candidate failover：已 ACK Runtime Commit/Approval/Action 不丢失；本机标 `BLOCKED_BY_ENV`；
26. Stage D：Orchestrator 无 write credential；`execution_unknown` 不盲重试；
27. Control-plane metrics smoke：outbox oldest age、lease lost、tool partial/timeout、recovery backlog、SSE replay、LLM egress failure 可采集。

测试状态统一：`PASS / FAIL / BLOCKED_BY_ENV / NOT_RUN / NOT_APPLICABLE`。

## 22. 按当前代码的实施顺序与验收门禁

### 阶段 A0：先修当前真实缺陷，不改架构

1. 修 `PersistentRunRepository.cancel -> ControlPlaneClient.cancel`：传 `expected_version + command_id`；
2. 把现有 transition/cancel 的 `ai_control_commands` 收敛为 payload-hash + stored-response + 单事务幂等；
3. production `run_persistence=remote` 下删除 `PersistentRunStateStore.get()` 的 fallback read；remote unavailable 必须 fail closed；
4. 修 `ai_run_outbox` stale claimed 无法 reclaim，并把 dispatch lease/retry due 统一改为 DB time；
5. 审计 Browser canonical route allowlist：优先修 `/api/v1/ai/runs/{id}`、Trace detail/context、前端实际使用的 metrics/infrastructure route；先补 scope 再放行；
6. 强化 LLM secret internal endpoint：shared token-only → token + signed TrustedRequestContext + dedicated capability，并修 `ModelsLLM` BaseURL 校验；
7. 为上述问题增加 focused concurrency/crash/auth-route/security tests；
8. 修正注释中仍写 “In-memory MVP/真实持久化后续” 等已过时描述，避免后续 AI 误判现状；
9. 修 `/runs/unfinished` global recovery scope 与 Kubernetes internal query silent-empty。

**A0 验收后再做 Lease/Commit。**

### 阶段 A1：现有 Control Plane 增加并发权威

1. 新 migration `0004_runtime_convergence.sql`；
2. `ai_runs` 增 Lease/runtime wait/retry 字段；
3. 新增 claim/renew endpoints 到 `/internal/v1/control-plane/runs/*`；
4. 增 DB-time lease、epoch/token fencing；
5. 增 `ai_runtime_commits`、Runtime Commit；
6. Event DAO 增 `AppendTx`；
7. 扩 recovery snapshot；
8. 墓碑/终态/Cancel fencing tests。

验收：两个 executor 竞争只一个 owner；old epoch、cancel 后 commit、response-lost retry 全部符合合同。

### 阶段 A2：Replay 与恢复

1. Query API 对 TrustedRequestContext 使用共享 replay guard；
2. RunInvocation/RunControl 保留本地签名验证 + 业务 idempotency + lease，按生产要求增加 issuer-owned shared nonce consume；
3. Recovery Scanner 复用 Claim API；
4. runtime_wait_kind/retry_not_before 接入恢复。

### 阶段 B1：现有 `/internal/v1/query/*` 增强 ToolRun

1. 不新建 generic tool endpoint；
2. `ai_tool_runs` ALTER；
3. InternalQueryClient 传 tool_run/lease 审计信息；
4. 8 个 internal query handlers 统一 wrapper：Start ToolRun → repository → Finish ToolRun；
5. real Tool events 由 Query API 产生；
6. Runtime Commit 一次消费 ToolResult → Evidence；
7. 统一 ToolResultEnvelope、absolute query window、partial/source_errors、result digest 与固定结果上限。

### 阶段 B2：调查主链与前端

1. RunInvocation 收到后 Claim Lease；
2. Planner → ToolRegistry → InternalQueryClient → canonical query；
3. 移除节点进入即伪造 tool event；
4. 重构 Chat：`build_graph(mode="chat")` 入口从 `collect` 改为 `classify_chat_intent`，普通 Chat 删除 services/K8s/alerts/logs/metrics/traces/RCA 固定实时采集；
5. `node_collect` 改为 Investigation/full graph 专属采集节点，并补齐 `logs_data` 显式 state schema；
6. 删除 Runtime `get_service_list -> kg_graph._load_graph` direct-MySQL 快路径，RCA/tools 的 public `/api/v1/*` 实时查询迁到 `InternalQueryClient -> /internal/v1/query/*`；
7. 删除 `get_infrastructure()` 在 `ai.chat` 内自行重签 `kubernetes.resources.read` system context 的 capability 提升逻辑；
8. 修复 AiChat cluster all：无 concrete cluster 不发送，Query API 继续 fail-closed；
9. 实时事实/诊断请求由 Chat 返回 `investigation_required`，用户显式确认后 `createRun()`；
10. 移除 legacy suggestion execution/final report Browser 生产路径；
11. A-C 调查允许 investigating -> success/partial。

### 阶段 C：数据与运行环境

1. 实现唯一 `ClickHouseSpanSink -> trace_spans` 写链；
2. 拆 Query API `api/run-dispatch/alert-eval` 三个运行角色；
3. Alert Worker 增 Kubernetes Lease + MySQL runtime state；
4. 部署专用 LLM Egress Proxy，Provider key 迁入 Proxy；
5. network egress deny 灰度；
6. 生产 K8s API TLS 校验（禁止 insecure skip verify）；
7. 生产 `LLM_MOCK=false`；
8. real MySQL/VM/VLogs/ClickHouse/K8s integration；
9. Control Plane metrics/alerts；
10. local fault injection；
11. MySQL/多节点真正 failover 无法本地证明项明确 BLOCKED_BY_ENV。

### 阶段 D：受控执行

仅 A-C 全过后，原位增强 `ai_actions/approval/verifications` 并新增独立 `ai-action-executor`。Orchestrator/Query API API Pod 均不得获得生产 mutation credential；在 Action Executor、Credential Broker、TOCTOU、execution_unknown reconcile、rollback/verification 未闭环前保持所有真实 mutation 关闭。

## 23. 发布禁止条件

### 23.1 A-C 阶段生产候选禁止条件

出现下列任意情况，不得进入“受控 AI 运维调查”生产候选验证：

- production 模式仍可能在远端 Query API 读取失败后回退本地 `RunStateStore`，形成第二事实源；
- `PersistentRunRepository.cancel()` / `ControlPlaneClient.cancel()` 尚未把 `expected_version + command_id` 端到端传入 Query API，Cancel 仍缺调用侧 CAS/幂等；
- Orchestrator 仍直接写 Runtime MySQL，或持有 MySQL、VictoriaMetrics、VictoriaLogs、ClickHouse、MinIO、Kubernetes 数据面凭据；
- Orchestrator 的 Run correctness / Recovery 仍依赖 Pod 本地 Checkpointer、RWO PVC 或本地文件，而不是 Query API + MySQL 的权威快照；
- Run 没有共享 execution Lease、`lease_epoch` fencing、`lease_token` 或 Runtime Commit 幂等；
- Commit 成功但响应丢失后，相同 `commit_id` 无法返回首次提交结果；
- 当前 V9.2 `RunStatus` 被另建 `QUEUED/RUNNING/WAITING_*` 平行状态机替代，而没有完成正式跨语言 breaking-contract migration；
- retry/backoff 只存在进程内计时，未通过 `runtime_wait_kind=retry + retry_not_before + retry_attempt` 持久化并由 DB time 判断；
- Query API 对 Orchestrator→Query API 的 TrustedRequestContext 重放保护仍仅依赖单进程内存，无法覆盖 Query API 多 Pod/重启；
- 新建 `/internal/v1/runtime/*` 或 Generic Tool Gateway，与现有 `/internal/v1/control-plane/*`、`/internal/v1/query/*` 形成平行执行面；
- 新建 `ai_tool_invocations` 与现有 `ai_tool_runs` 形成第二 Tool I/O 权威；
- `ai_run_outbox` 被改造成 Event Outbox，破坏其现有 RunInvocation dispatch 语义；
- SSE 不以 `ai_run_events.sequence` 为游标，或多副本实时流依赖单 Pod 内存 subscriber；
- ToolEvent 仍由 LangGraph 节点状态推断，或同一 `ai_tool_runs` 结果可被多个 Runtime Commit 重复转成 Evidence；
- `AiChat.tsx` 仍默认 `cluster_id=all` 而 `ProxyChat` 要求 concrete cluster，或普通 Chat 仍固定执行 `node_collect`/RCA 实时查询，或遗留 `/ai/suggestion/execute`、`/ai/final_report` 被继续当作生产处置主链；
- Chat 仍可通过 `signed_query_api_request()` 调 legacy `/api/v1/services|logs|traces|alerts|infrastructure` 获取实时事实，或 `get_infrastructure()` 仍可把 `ai.chat` 隐式重签为 `kubernetes.resources.read`，或 `get_service_list()` 仍保留 direct-MySQL KG 快路径；
- 前端验收所需 Browser 只读路由仍因 `isCanonicalProtectedRoute()` 未覆盖而固定 403，或通过“整段 prefix 放行”绕过 canonical tenant/cluster/resource scope；
- Query API `role=api` 仍无条件启动 RunDispatchLoop/AlertEvaluation，或 Alert 多副本没有 Kubernetes Lease + MySQL runtime state；
- `/internal/v1/control-plane/runs/unfinished` 仍允许 tenant-scoped recovery context 触发全库 `ScanUnfinished()`；
- Internal Kubernetes query 仍把 repository 错误吞成 200 空数组，或 ToolResult 无法区分 complete/partial/failed；
- Investigation 相对时间窗仍在 retry 时重新展开，或同 tool_run_id 可得到不同 query window；
- ToolResult 没有服务端硬上限/`truncated`/digest；
- Trace 未固定 `ClickHouse trace_spans` 为平台 SoT，或当前 `nil SpanSink` 导致 fresh trace 无法端到端进入查询/Evidence；
- `ai-llm-egress-proxy` 未部署/未限制 Provider host，Orchestrator 仍能直接访问公网 LLM，或 `egressDefaultDeny` 后真实 Provider 路径未验证；
- 生产 Query API 仍以 `K8S_INSECURE_SKIP_VERIFY=true` 访问 Kubernetes API，或没有对每个 cluster_id 验证 CA/server identity；
- LLM 解密 Key 仍可通过只校验共享 `X-Internal-Token` 的 `/api/v1/settings/llm/internal` 获取，或该 endpoint 可经公共 Ingress 访问；
- `ModelsLLM` 等 Provider 请求路径仍可绕过统一 BaseURL/SSRF 校验；
- 生产候选实例无法证明 `LLM_MOCK=false`；
- Orchestrator ↔ Query API 的内部服务调用未完成网络隔离和生产级 TLS/mTLS 验证；
- Schema 仍由 Runtime 隐式 DDL 修改，或新增 runtime schema 不是通过当前 schema-migrator 的后续 migration 原位演进；
- 生产运行依赖、镜像、schema 不可追溯；
- MySQL 控制面备份/PITR/候选 failover 合同未验证，或已 ACK Runtime 控制事务可能在正常 failover 后丢失；
- Runtime/Lease/Outbox/Tool/Recovery/SSE/LLM 关键自观测指标缺失；
- L5 Platform E2E 缺失。

### 23.2 Stage D 真实执行禁止条件

Stage D 必须复用现有 `ai_actions`、`ai_approval_decisions`、`ai_verifications`，并允许现有 V9.2 RunStatus 在高层表示 `awaiting_approval/executing/verifying`。禁止的是把资源变更的细粒度生命周期继续全部塞进 Run，而不是禁止这些已冻结的 RunStatus。

除满足 A-C 外，出现下列任意情况不得开启 `EXECUTION_MODE=approved`：

- Approval 未绑定 `tenant/cluster/requester/approver/action_hash/resourceVersion/expiry`；
- R3/R4 requester/approver 分离规则未实现；
- `ai_actions.idempotency_key`、动作 payload hash 或重复执行保护未被真实执行适配器使用；
- Action 自身没有明确 `proposed/approved/executing/succeeded/failed/...` 等可恢复生命周期，导致仅依赖 RunStatus 判断某个资源变更是否已经执行；
- resourceVersion/TOCTOU 防护未实现；
- 最小 RBAC、Credential Broker/凭据边界未验证；
- 独立 `ai-action-executor` 尚未建立，或 Orchestrator/Query API API Pod 仍直接持有生产 mutation credential；
- Preview、Rollback、Verification、Audit 未闭环；
- 旧 `EXECUTION_AFTER_APPROVAL=1` 能绕过新的 `EXECUTION_MODE`/Action Gate；
- `execution_unknown` 没有 reconcile-before-retry 规则；
- L6 Remediation E2E 缺失。

---

## 24. 本机单节点验收范围

本机环境可以作为阶段 A、B 和 C 大部分控制面能力的强验证环境，包括：

- 多 Orchestrator Pod Claim 竞争；
- Lease Renew/Lost；
- epoch/token fencing；
- Commit idempotency；
- Shared Replay 跨 Pod；
- Query API `api/run-dispatch/alert-eval` 三角色同节点多 Pod；
- Alert Leader 进程切换与 MySQL cooldown/streak 恢复；
- SSE 重连；
- Outbox reclaim + dispatch fencing；
- `/internal/v1/query/*` ToolRegistry/capability/scope 权限；
- Runtime Recovery；
- 数据源真实查询；
- Trace `ClickHouseSpanSink` 新写入路径与去重；
- ToolResult partial/truncated/absolute-window；
- Recovery global capability 隔离；
- Control Plane metrics；
- NetworkPolicy；
- LLM egress；
- 服务重启恢复。

不能由单节点环境充分证明：

- Kubernetes Node 故障；
- 跨节点 RWO/PVC 故障恢复；
- DaemonSet 真正跨节点 Leader Failover；
- 多机房/多可用区故障；
- 真实生产网络抖动与长时间分区；
- MySQL 真正多节点主库故障切换与 RPO=0 验证。

上述项目必须标记为 `BLOCKED_BY_ENV` 或在后续多节点候选环境验证，不得影响本地阶段结论的真实性。

---

## 25. 后续 AI 编码任务约束

后续交给 AI 编码时，每个任务必须明确：

```text
目标
允许修改目录
禁止修改目录
输入接口
输出接口
数据库迁移
兼容性要求
错误码
幂等键
并发规则
测试级别
验收命令
回滚方式
```

AI 不得自行：

- 新建平行 Run Store；
- 新建第二套状态机；
- 在 Orchestrator 恢复 MySQL/K8s 直连；
- 使用内存缓存替代共享权威；
- 为绕过接口限制添加 Generic Shell/SQL/HTTP Tool；
- 把 Stage D 写执行提前混入 A-C；
- 在测试受环境阻塞时伪造 PASS；
- 为“方便”跳过 migration、Lease、version、Replay 或 Authorization。

每个阶段完成后先执行代码审查、迁移审查、Contract Test 和阶段 E2E，再进入下一阶段。

---

## 26. 最终交付判断

本报告经本轮全面复审后，架构方向调整为：

```text
Orchestrator 负责推理和选择
Query API 负责权威状态、事务、执行数据访问和凭据
MySQL 负责 Runtime 事实
ai_run_events 负责事件历史
ai_run_outbox 负责 RunInvocation 可靠派发
DB Lease + epoch + token 负责并发执行权
Shared Replay Guard 负责 Context 防重放
业务 Idempotency 负责业务重试
```

完成阶段 A-C 并通过相应验证后，平台可以进入：

```text
CONTROLLED_AI_INVESTIGATION_CANDIDATE
```

此时允许 AI：

- 收集只读事实证据；
- 执行受控调查；
- 形成 Hypothesis/RCA；
- 生成 Action Preview；
- 输出调查报告；
- 保留完整审计链。

但不得自动修改生产资源。

真实处置执行必须在阶段 D 作为独立安全上线决策完成，不能因为 Agent、RCA、Approval UI 或 Chat 已存在就直接打开执行开关。

在上述整改全部写入代码并完成测试前，当前状态应视为：

```text
ARCHITECTURE_APPROVED_WITH_REQUIRED_AMENDMENTS
NOT_PRODUCTION_READY
```

---

## 27. 具体实施规格（按当前仓库文件直接落地）

### 27.1 第一批必须修改的现有文件

#### Query API

```text
ai-apm-query-go/internal/store/ai_runs.go
ai-apm-query-go/internal/store/ai_run_events.go
ai-apm-query-go/internal/store/ai_run_outbox.go          # dispatch 语义 + dispatch fencing
ai-apm-query-go/internal/store/ai_tool_runs.go
ai-apm-query-go/internal/store/alerts.go                    # alert runtime state DAO
ai-apm-query-go/internal/store/ai_control_commands.go
ai-apm-query-go/internal/api/control_plane_runs.go
ai-apm-query-go/internal/api/control_plane_events.go
ai-apm-query-go/internal/api/control_plane_recovery.go
ai-apm-query-go/internal/api/internal_query.go
ai-apm-query-go/internal/api/sse_proxy.go                 # 主要补测试，不重写架构
ai-apm-query-go/internal/api/run_dispatch.go              # dispatch fencing + worker role
ai-apm-query-go/internal/api/alert_engine.go               # leader-only + DB state
ai-apm-query-go/cmd/api/main.go                             # --role=api|run-dispatch|alert-eval
ai-apm-query-go/internal/store/migrations/versions/0004_runtime_convergence.sql
ai-apm-query-go/internal/store/migrations/versions/0005_alert_worker_state.sql
```

#### Orchestrator

```text
ai-orchestrator/persistent_run_repository.py
ai-orchestrator/persistent_run_state_store.py
ai-orchestrator/control_plane_client.py
ai-orchestrator/run_state_machine.py
ai-orchestrator/contracts.py                              # 仅最小 transition/字段演进
ai-orchestrator/internal_query_client.py
ai-orchestrator/tool_registry.py
ai-orchestrator/trusted_context.py
ai-orchestrator/main.py
ai-orchestrator/orchestrator.py
```

#### Frontend

```text
observability-frontend/src/pages/ai/AiChat.tsx
observability-frontend/src/api/client.ts
observability-frontend/src/api/contracts.ts（如 contract 字段演进）
Investigation/Run detail 页面相关文件
```

#### Deploy

```text
deploy/helm/aiops/values.yaml
deploy/helm/aiops/templates/ai-orchestrator/deployment.yaml
deploy/helm/aiops/templates/ai-orchestrator/rbac.yaml
deploy/helm/aiops/templates/query-api/deployment.yaml
deploy/helm/aiops/templates/run-dispatch-worker/deployment.yaml
deploy/helm/aiops/templates/alert-worker/deployment.yaml
deploy/helm/aiops/templates/alert-worker/rbac.yaml
deploy/helm/aiops/templates/llm-egress-proxy/deployment.yaml
deploy/helm/aiops/templates/networkpolicy.yaml
# Stage D:
ai-action-executor/**
```

### 27.2 A0-1：修复 Cancel CAS，并补齐现有 Control Command 真正幂等

当前客户端问题：

```python
PersistentRunRepository.cancel(... expected_version, command_id ...)
    -> self._client.cancel(run_id, tenant_id)   # 两参数丢失
```

先修为：

```python
ControlPlaneClient.cancel(
    run_id,
    tenant_id,
    expected_version,
    command_id,
)
```

HTTP body：

```json
{
  "expected_version": 7,
  "command_id": "uuid"
}
```

同时修复服务端已有 `ai_control_commands` 的不完整幂等语义。当前实现的问题是：

```text
recordControlCommand() 不保存真实 payload_json
done replay 只比较 operation
不比较 target / expected_version / payload hash
不保存第一次 response
command row 与 Run CAS/state mutation 不是同一事务
```

`0004_runtime_convergence.sql` 在原表增加：

```sql
ALTER TABLE ai_control_commands
  ADD COLUMN payload_hash CHAR(64) NULL,
  ADD COLUMN response_json JSON NULL,
  ADD COLUMN completed_at DATETIME(3) NULL;
```

对历史记录只做兼容 backfill，不伪造 response。新代码启用后所有新 control command 必须有 canonical payload hash。hash 至少覆盖：

```text
run_id
operation
expected_version
target（transition）
其他影响业务语义的 control payload
```

不包含 Authorization、Trusted Context nonce、HTTP 时间戳。

Transition 与 Cancel 均走一个事务化 helper，例如：

```text
ApplyRunControlCommandTx(command_id, operation, payload_hash, expected_version, mutate_fn)
```

事务：

```text
BEGIN
lookup command_id
  existing + same semantic payload + done => return stored response_json
  existing + different payload          => IDEMPOTENCY_KEY_REUSED
lock ai_runs
CAS expected_version
validate transition
apply Run mutation / lease fence if cancel
AppendTx business event
write command payload/hash/status/response
COMMIT
```

这样覆盖两个现实故障窗：

1. Run mutation 成功但 `MarkDone` 前进程崩溃；
2. 响应丢失后重试同 command_id，此时 Run 已被后续步骤推进。

重试必须返回该 command 首次成功的 committed response，而不是简单返回“此刻 current Run”。

测试：

```text
cancel client passes expected_version + command_id
transition/cancel missing expected_version => 400/422
same command + same payload => identical semantic response
same command + different target/body => 409 IDEMPOTENCY_KEY_REUSED
stale expected_version => 409 RUN_STATE_CONFLICT
crash window / transaction rollback => no half-done command
terminal => illegal transition
```

### 27.3 A0-2：production remote 读取必须 fail closed

当前 `PersistentRunStateStore.get()` 远端 refresh 失败后仍可能：

```python
return self._fallback.get(run_id)
```

改造：

```python
if self._repo is not None:
    cached = cache.get(run_id)
    if cached is not None:
        return cached
    # tenant 必须来自 invocation/recovery scope，而不是 UUID(0) 猜测
    return repo.refresh(run_id=run_id, tenant_id=required_tenant_id)
```

生产 remote 模式：

```text
remote 404 -> not found
remote 403 -> permission denied
remote 503/timeout -> unavailable
任何情况不得回退本地 RunStateStore
```

接口签名应逐步把 `tenant_id` 显式传入 `get()`，删除 `_fallback_tenant()` 的 UUID(0) 猜测逻辑。

开发 `memory` 模式仍可使用 fallback，但必须由 factory 在启动时决定 backend，不在一次 `get()` 内动态跨 SoT。

### 27.3.1 A0-3：修复 RunInvocation Outbox stale-claim，并增加 dispatch fencing

当前真实逻辑：

```text
ScanPending(): pending due + stale claimed
Claim():       only WHERE status='pending'
Deliver/Retry: only WHERE invocation_id
```

必须同时解决 stale reclaim 与旧 worker 晚到覆盖。`AIRunOutboxDAO.Claim` 接收 `invocation_id/dispatcher_id/dispatch_token/lease_duration`，其中 token 为 >=128-bit random，DB 只存 SHA-256。Claim 原子允许 pending due 或 stale claimed，并执行 `dispatch_epoch++`、写 owner/token/`dispatch_expires_at=DB_NOW+30s`。

`Deliver/Retry` 必须匹配 `invocation_id + dispatch_owner_id + dispatch_epoch + dispatch_token_hash`；`Deliver` 还要求 `dispatch_expires_at > DB_NOW`。旧 epoch/token 返回 `DISPATCH_LEASE_LOST`。`Retry` 只由当前 Claim 设置 `status=pending + next_retry_at=DB_NOW+backoff` 并清 claim 字段。

故障测试固定覆盖：claim 后进程崩溃可 reclaim；旧 Deliver/Retry 晚到被拒；两个 worker 并发 reclaim 只有一个成功；POST 已成功但 Deliver 失败后再次派发同 invocation_id，由 Orchestrator 业务幂等返回首次结果。该 dispatch lease 不复用 Run execution lease。

### 27.4 0004 migration

`0004_runtime_convergence.sql` 固定包含：

```sql
ALTER TABLE ai_runs
  ADD COLUMN lease_owner_id VARCHAR(128) NULL,
  ADD COLUMN lease_epoch BIGINT NOT NULL DEFAULT 0,
  ADD COLUMN lease_claim_id CHAR(36) NULL,
  ADD COLUMN lease_token_hash CHAR(64) NULL,
  ADD COLUMN lease_expires_at DATETIME(3) NULL,
  ADD COLUMN heartbeat_at DATETIME(3) NULL,
  ADD COLUMN runtime_wait_kind VARCHAR(32) NOT NULL DEFAULT 'none',
  ADD COLUMN retry_not_before DATETIME(3) NULL,
  ADD COLUMN retry_attempt INT NOT NULL DEFAULT 0,
  ADD COLUMN last_failure_code VARCHAR(64) NULL,
  ADD COLUMN runtime_metadata_json JSON NULL;
```

约束不强依赖 MySQL CHECK（历史版本/兼容性可能不同）；业务 enum 由 Go validation + migration test 保证。

Commit：

```sql
CREATE TABLE ai_runtime_commits (
  run_id CHAR(36) NOT NULL,
  commit_id CHAR(36) NOT NULL,
  payload_hash CHAR(64) NOT NULL,
  committed_state_version BIGINT NOT NULL,
  result_status VARCHAR(32) NOT NULL,
  first_event_sequence BIGINT NULL,
  last_event_sequence BIGINT NULL,
  response_json JSON NOT NULL,
  created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  PRIMARY KEY(run_id, commit_id)
);
```

Claim：

```sql
CREATE TABLE ai_run_claims (
  run_id CHAR(36) NOT NULL,
  claim_id CHAR(36) NOT NULL,
  executor_id VARCHAR(128) NOT NULL,
  lease_epoch BIGINT NOT NULL,
  lease_token_hash CHAR(64) NOT NULL,
  created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  PRIMARY KEY(run_id, claim_id)
);
```

Replay：

```sql
CREATE TABLE ai_context_replay_guard (
  issuer VARCHAR(128) NOT NULL,
  audience VARCHAR(128) NOT NULL,
  nonce CHAR(36) NOT NULL,
  request_hash CHAR(64) NULL,
  consumed_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  expires_at DATETIME(3) NOT NULL,
  PRIMARY KEY(issuer, audience, nonce),
  INDEX idx_replay_expiry(expires_at)
);
```

Control command hardening：

```sql
ALTER TABLE ai_control_commands
  ADD COLUMN payload_hash CHAR(64) NULL,
  ADD COLUMN response_json JSON NULL,
  ADD COLUMN completed_at DATETIME(3) NULL;
```

Tool：

```sql
ALTER TABLE ai_tool_runs
  ADD COLUMN args_hash CHAR(64) NULL,
  ADD COLUMN executor_id VARCHAR(128) NULL,
  ADD COLUMN lease_epoch_at_start BIGINT NULL,
  ADD COLUMN deadline_at DATETIME(3) NULL,
  ADD COLUMN observed_at DATETIME(3) NULL,
  ADD COLUMN query_window_start DATETIME(3) NULL,
  ADD COLUMN query_window_end DATETIME(3) NULL,
  ADD COLUMN result_quality VARCHAR(16) NOT NULL DEFAULT 'none',
  ADD COLUMN result_complete TINYINT NOT NULL DEFAULT 0,
  ADD COLUMN result_truncated TINYINT NOT NULL DEFAULT 0,
  ADD COLUMN result_count BIGINT NOT NULL DEFAULT 0,
  ADD COLUMN result_digest_sha256 CHAR(64) NULL,
  ADD COLUMN eligible_for_evidence TINYINT NOT NULL DEFAULT 0,
  ADD COLUMN evidence_consumed_at DATETIME(3) NULL;

ALTER TABLE ai_run_outbox
  ADD COLUMN dispatch_owner_id VARCHAR(128) NULL,
  ADD COLUMN dispatch_epoch BIGINT NOT NULL DEFAULT 0,
  ADD COLUMN dispatch_token_hash CHAR(64) NULL,
  ADD COLUMN dispatch_expires_at DATETIME(3) NULL,
  ADD COLUMN delivered_at DATETIME(3) NULL,
  ADD INDEX idx_run_outbox_dispatch(status, next_retry_at, dispatch_expires_at, created_at);
```

不要创建 `ai_tool_invocations`。`next_retry_at` 只表示下一次派发时间，`dispatch_expires_at` 才是 dispatcher claim lease。

### 27.5 DB-time Lease DAO

Claim transaction：

```text
BEGIN
SELECT ai_runs WHERE run_id=? FOR UPDATE
SELECT ai_run_claims WHERE run_id=? AND claim_id=?
if exact valid replay => return current lease metadata
if claim id collision => conflict
if terminal => reject
SELECT CURRENT_TIMESTAMP(3) AS db_now
if lease_expires_at > db_now => RUN_ALREADY_CLAIMED
if runtime_wait_kind=retry and retry_not_before>db_now => RETRY_NOT_DUE
if status awaiting_confirmation/awaiting_approval => NOT_AUTO_CLAIMABLE
new_epoch=lease_epoch+1
UPDATE ai_runs SET owner/token_hash/expiry=db_now+TTL...
INSERT ai_run_claims
COMMIT
```

Token：客户端生成 >=256-bit random，服务端只存 SHA-256；恒时比较。

Renew：

```text
BEGIN
SELECT run FOR UPDATE
DB_NOW
non-terminal + owner + epoch + token hash + not expired
UPDATE expiry/heartbeat using DB_NOW
COMMIT
return server_now/expiry/remaining_ms
```

### 27.6 Runtime Commit DAO/API

新增 handler 与 DAO/service，路径：

```text
POST /internal/v1/control-plane/runs/{run_id}/commit
```

Runtime Commit 逻辑必须从巨大 HTTP handler 中抽成领域 service，固定新增/复用：

```text
internal/runtimecommit/service.go
internal/store/ai_runtime_commits.go
```

HTTP 适配层放 `internal/api/control_plane_commit.go`；事务编排放 `internal/runtimecommit/service.go`，DAO 放 `internal/store/ai_runtime_commits.go`。状态 validation 必须复用唯一 control-plane transition contract，不得在 handler 内再定义第二套。

事务伪代码：

```go
func Commit(ctx context.Context, req CommitRequest) (CommitResult, error) {
    if old := findCommit(req.RunID, req.CommitID); old != nil {
        return replayOrConflict(old, semanticHash(req))
    }
    tx := BeginTx()
    run := lockRun(tx, req.RunID)
    if old := findCommitTx(tx, req.RunID, req.CommitID); old != nil { ... }
    validateLeaseDBTime(tx, run, req)
    requireVersion(run.StateVersion, req.ExpectedVersion)
    validateTargetStatus(run.Status, req.TargetStatus)
    toolResults := lockAndValidateToolConsumptions(tx, req.ToolResultConsumptions)
    persistArtifacts(tx, req, toolResults)
    updateRun(tx, req)
    seqs := appendEventsTx(tx, req.Events)
    persistCommitResult(tx, ...)
    tx.Commit()
}
```

### 27.7 Event DAO refactor

当前 `AIRunEventDAO.Append()` 已有正确的 event_id 幂等 + sequence owner 逻辑，但自行 Begin/Commit。

改为：

```go
func AppendTx(tx *sql.Tx, ev AIRunEvent) (...)
func Append(ev AIRunEvent) (...) {
    tx := Begin()
    out := AppendTx(tx, ev)
    Commit()
    return out
}
```

Runtime Commit 与 Run CreateWithOutbox 使用 `AppendTx`。

`CreateWithOutbox` 新顺序：

```text
BEGIN
insert ai_runs(status=created, state_version=0)
AppendTx RUN_CREATED
insert ai_run_outbox(pending RunInvocation)
COMMIT
```

### 27.8 Runtime wait metadata

不要修改 wire enum 增加 WAITING 状态。Runtime Commit 更新：

```json
{
  "target_status": null,
  "runtime_metadata": {
    "runtime_wait_kind": "tool",
    "pending_tool_run_ids": ["..."],
    "retry_not_before": null
  }
}
```

Retry：

```json
{
  "target_status": null,
  "runtime_metadata": {
    "runtime_wait_kind": "retry",
    "retry_attempt": 2,
    "retry_not_before": "DB-derived future instant",
    "last_failure_code": "LLM_UNAVAILABLE"
  }
}
```

Query API 校验 retry attempt 单调，不让 caller 回退计数。

### 27.9 Python/Go 状态机同步

现有状态表分别在：

```text
ai-orchestrator/run_state_machine.py
ai-apm-query-go/internal/api/control_plane_runs.go
```

不要再增加第三份 Runtime 状态机实现。新增唯一 machine-readable 测试合同：

```text
docs/contracts/run_state_transitions.json
```

Go/Python/Frontend contract tests 都读取同一 fixture 校验 enum/transition；运行时权威仍是 Query API 的 `validRunTransition()`，Python 只做本地预检。A-C 只增加：

```text
investigating -> success | partial
awaiting_confirmation -> success | partial（若产品允许用户确认“仅结束调查”）
```

所有变更同步：

```text
contracts.py
Go contract/binding
frontend api/contracts.ts
shared fixtures
state transition tests
```

### 27.10 Shared replay 实施

#### Orchestrator -> Query API

每个 ControlPlaneClient/InternalQueryClient 请求继续**新签** TrustedRequestContext；nonce 在 Query API MySQL guard 中一次消费。

HTTP transport retry：

```text
new signed context/nonce
same business idempotency key
```

例如 Commit 始终复用同 `commit_id`，Tool 始终复用同 `tool_run_id`。

#### Query API -> Orchestrator

RunInvocationContext/RunControlContext 仍先本地验证签名/lifetime。生产 profile 随后必须通过 Query API issuer-owned shared nonce consume endpoint 完成跨 Pod 一次性消费，再按 invocation/control id 做业务幂等并 Claim Lease；consume 不可用时 fail closed。不要给 Orchestrator MySQL 凭据。

### 27.11 `/internal/v1/query/*` ToolRun wrapper

InternalQueryClient 生成：

```python
tool_run_id = uuid4()
args_hash = canonical_hash(operation, params)
```

在请求内携带 signed/audited metadata。

Query API 每个 handler 共用 wrapper：

```go
func executeInternalTool(ctx, meta, fn) {
    // auth/scope already canonical
    startOrReplayToolRun(meta)
    result, err := fn()
    finishToolRun(meta, result, err)
}
```

`startOrReplayToolRun`：

```text
same tool_run_id + same args hash + terminal => return saved
same id + different hash => 409
same id + running => 202/in-progress
new => validate Run Lease and insert running
```

### 27.12 Tool result late/fencing

Finish ToolRun 时：

```text
lock run -> tool_run
if run terminal OR run.lease_epoch != tool.lease_epoch_at_start:
    store actual result
    eligible_for_evidence=false
    event TOOL_RESULT_LATE
else:
    store result
    eligible_for_evidence=true
```

即使外部 query 已返回，也不能绕过 epoch fencing进入 Evidence。

### 27.13 Tool Reconciler

查询候选时不加反向锁：

```text
SELECT running tool_runs WHERE deadline_at < DB_NOW LIMIT N
```

逐条收敛：

```text
BEGIN
lock Run
lock ToolRun
recheck still running/deadline
status=timeout/failed_unknown
eligible=false
append event
COMMIT
```

统一锁序 Run -> ToolRun，避免与 Commit/Finish 相反。

### 27.14 Evidence 一次消费

Runtime Commit 锁 ToolRun：

```text
status terminal successful/partial/no_data as allowed
eligible_for_evidence=true
evidence_consumed_at IS NULL
same run/tenant/cluster
```

创建 `ai_evidence` 后同事务设置 `evidence_consumed_at=DB_NOW`。

A-C 超过 11.10 固定上限时，只允许服务端 deterministic truncation 或返回 `RESULT_TOO_LARGE`；不得隐式上传 MinIO/Object Storage。`Object First + MySQL Ref` 仅在后续独立 StorageAdapter 阶段完成代码、权限、GC、授权与故障注入验收后启用。

### 27.15 Recovery Scanner

全局恢复发现只使用第 13.3 节已经冻结的唯一接口，不再新增 `recoverable` 第二路径：

```text
GET /internal/v1/control-plane/runs/unfinished?limit=200&cursor=...
capability = control_plane.runs.recover.global
principal_type = system
tenant_id / cluster_id = empty
```

SQL 只做候选筛选并分页返回最小字段：

```text
non-terminal
lease missing/expired
status in created/planning/investigating
retry due if runtime_wait_kind=retry
```

最终 Eligibility 由 Claim transaction 再校验。

### 27.16 Orchestrator main loop

```text
RunInvocation
  -> verify context
  -> idempotency by invocation_id
  -> refresh authoritative Run/snapshot
  -> claim lease
  -> start independent renew task
  -> if created: commit planning
  -> planner
  -> commit investigating + runtime_wait_kind=tool + pending tool_run ids
  -> InternalQueryClient canonical typed calls
  -> commit consume ToolResults + Evidence
  -> LLM/RCA
  -> commit success/partial/failed
  -> release/clear lease as part terminal commit
```

每个外部 await 期间 renew task 独立运行；Lease uncertain 禁止新增副作用。

### 27.17 Chat 主链具体修改

#### Orchestrator

`main.py /internal/v1/chat`：

- 保留现有 `RunInvocationContext` 验签、`principal_type=user`、`capability=ai.chat`、tenant/cluster body match；
- 保留“不创建 Investigation Run”的语义；
- 删除 `exec_result -> iteration=2` 作为生产处置闭环；
- 对需要实时观测事实的问题返回结构化 `investigation_required` 事件/结果，不进入实时 collector；
- 不再从 Chat 流发送可执行 `suggestion`；兼容期可保留事件解析但不得产生执行入口。

`orchestrator.py`：

- `build_graph(mode="chat")` 不再 `set_entry_point("collect")`；
- Chat 图删除 `node_collect`、`node_rca` 以及其他会读取实时运行数据的节点；
- 普通知识/对话图只处理 history/knowledge/LLM/summarize；
- `node_collect` 继续只服务 Investigation/full runtime 时，必须逐项迁到 `InternalQueryClient`/ToolRun 主线，不能继续让 Chat/Agent 直接调用 `tools.py` 的 `/api/v1/*` helpers；
- 删除 Chat 对 `_fallback_script()`、`_extract_script()`、`_sanitize_script_placeholders()` 的处置卡生成依赖；这些只允许在 Stage D Action Proposal 中复用，且必须由 Action policy 重新生成/验证；
- `logs_data` 若仍在 Investigation state 使用，显式加入 state contract；如果随 Chat collector 删除而无其他调用者，则一起删除，禁止未声明 state key。

`tools.py / rca.py / kg_graph.py`：

- Chat 主链不得调用 `get_service_list/query_logs/query_metrics/query_traces/_collect_alerts/get_infrastructure/full_rca_analysis`；
- 删除 `get_service_list()` 对 `kg_graph._load_graph()` 的 Chat direct-MySQL 快路径；
- `get_infrastructure()` 不再自行把 `ai.chat` 重签为 `kubernetes.resources.read` system context；
- services/logs/traces/alerts/topology 等实时读取由 Investigation 的 `InternalQueryClient` + Tool Registry 发起；
- RCA 内 legacy `/api/v1/infrastructure/*`、`/api/v1/services`、`/api/v1/topology/global` 等查询逐步迁移到 canonical internal query repositories，不能因为 Chat 不再调用就保留成未来 Agent 旁路。

#### Frontend

`AiChat.tsx`：

- 删除 `clusterId='all'` 作为请求默认值；
- 当前未选 concrete cluster 时禁止发送 canonical Chat，并明确提示选择集群；
- 识别“实时/当前/最近/排查/根因/告警/日志/Trace/K8s 状态”等调查意图时，Chat 只展示“开始调查”，不先发起隐藏 collector；
- 点击“开始调查”调用 `createRun()`，然后导航到 Run detail/Investigation 页面；
- 不再渲染执行按钮调用 `executeSuggestion`；
- 不再通过 `finalReport` 旧端点生成 Run 报告；
- 删除“执行后把 `exec_result` 回填 Chat 继续分析”的生产闭环；
- Chat 纯知识问答与 Run 调查在 UI 上明确区分。

`client.ts`：

- 删除或 deprecate `executeSuggestion` / `finalReport` 的生产调用者；
- `createRun` 强制 concrete cluster 参数；
- Run SSE 使用现有 sequence contract；
- 不新增一个浏览器“实时 Chat 查询”API 来绕过 Investigation Run。

#### Query API Browser route

- 保留 `ProxyChat` 对 JWT/session/tenant/concrete cluster/`ai.chat` 的当前严格校验；
- 不因为 Chat 原固定 collector 需要 `/api/v1/services`、`/logs/query` 等就扩大 Browser allowlist；Chat collector 应删除，而不是靠放宽公共路由维持；
- `isCanonicalProtectedRoute()` 只增加经过 scope 修复且产品本身仍需要的精确 Browser 路由；
- 优先保证 `GET /api/v1/ai/runs/{run_id}` 详情可在 tenant ownership 校验后访问；
- Trace detail/context 不再硬编码 VLogs endpoint，不再读取无 scope 全局 alert 集合；
- raw PromQL passthrough 未完成 tenant/cluster 隔离前保持 fail-closed；
- infrastructure Browser route 逐个接 canonical cluster authorization 后再放行。

#### Chat 验收断言

必须增加自动化测试证明：

```text
普通知识 Chat
  -> 不调用 /internal/v1/query/metrics
  -> 不调用 /internal/v1/query/logs
  -> 不调用 /internal/v1/query/traces
  -> 不调用 /internal/v1/query/alerts
  -> 不调用 /internal/v1/query/kubernetes
  -> 不调用 legacy /api/v1/services|logs|traces|alerts|infrastructure
  -> 不创建 ai_runs/ai_tool_runs/ai_actions

实时诊断问题
  -> Chat 返回 investigation_required
  -> 用户显式 createRun 后才产生 ai_runs
  -> Run 内 Tool 才进入 InternalQueryClient + ai_tool_runs + Evidence
```

### 27.18 Trace

当前代码事实必须原样保留：`OTLP/DeepFlow span parser exists`、`main.go passes nil SpanSink`、`ingest does not persist spans through current main path`。阶段 C 直接实现 `ClickHouseSpanSink -> trace_spans -> query-api traceRepo -> /internal/v1/query/traces`；production/candidate profile `SpanSink=nil` 启动失败。DeepFlow 只能进入同一 SpanSink 或作为非权威补充源。增加 `span_dedup_key` 与 query-side dedup 测试。

### 27.19 NetworkPolicy/credentials

当前 Helm：

```text
networkPolicy.enabled=true
egressDefaultDeny=false
allowOrchestratorDbAccess=false
orchestrator injectDbCredentials 默认 false
orchestrator egress canary 只放 query-api + DNS
```

因此阶段 C：

1. 部署专用 `ai-llm-egress-proxy`，Provider API key 迁入 Proxy；
2. Orchestrator egress 只允许 Query API + Proxy + DNS，并把 ai-orchestrator 加入 egress canary；
3. 验证不需要 DB/CH/VM/VLogs/K8s 直连；
4. production values/profile 将 `queryApi.k8sInsecureSkipVerify=false`，并验证每个已注册 cluster 的 CA/server identity；本机自签 profile 如需 true 必须显式标记且不得复用为生产 values；
5. 最后全局 egressDefaultDeny=true。

注意部署模板虽然默认不注入密码，仍设置 MYSQL_HOST/USER/DB、CLICKHOUSE_HOST/USER 等非秘密环境变量；不要把“环境变量存在”误判为“可成功直连”，但应在清理后删掉无用途配置以减少误导。

LLM secret endpoint 同步纳入网络策略：只允许 ai-orchestrator（或后续 LLM egress proxy）访问，不允许 frontend/Ingress namespace 访问；鉴权必须使用 signed TrustedRequestContext，不能只依赖 NetworkPolicy。

### 27.20 RWO/Checkpointer

当前 Docker/Deployment 明确：

```text
Python 3.12
LangGraph >=1,<2
langgraph-checkpoint-sqlite >=3,<4
RWO orchestrator-data PVC
```

这些与最新代码一致。不要删除 AsyncSqliteSaver 只为“看起来无状态”；它可以继续服务 Chat/session。强约束是：Run correctness/recovery 不依赖它。

多副本 Orchestrator 生产化前需单独解决 Chroma/session checkpoint 的共享/分片/粘性策略。

### 27.21 Stage D

复用 `ai_actions/ai_approval_decisions/ai_verifications`，并新增唯一生产执行边界 `ai-action-executor`。当前 Orchestrator 内的 `execution_adapter.py`、`credential_broker.py`、真实 K8s adapter 不得获得生产写权限。

固定链路：`Action persisted -> approval/action_hash/precondition -> Query API execution attempt -> signed ActionExecutionContext -> ai-action-executor re-read UID/resourceVersion -> scoped credential -> mutation -> Query API result -> verification -> terminal/rollback/execution_unknown`。`execution_unknown` 必须 reconcile-before-retry。

### 27.22 编码任务固定拆分

```text
A0-01 Cancel + control-command idempotency transaction convergence
A0-02 Production remote read fail-closed
A0-03 RunInvocation outbox stale-claim + dispatch fencing + DB-time
A0-04 Browser canonical route/scope convergence
A0-05 Recovery global-scope + Kubernetes silent-empty correction
A1-01 0004 runtime convergence migration
A1-02 Lease DAO + claim/renew + claim history
A1-03 Runtime Commit + AppendTx + commit-result retention
A1-04 Recovery snapshot/runtime metadata
A2-01 Shared TrustedRequest replay guard + /internal/v1/security/replay/consume
A2-02 Recovery Scanner + fencing/response-loss tests
B1-01 ai_tool_runs data-quality/time-window/result-limit expansion
B1-02 InternalQuery ToolRun wrapper + ToolResultEnvelope
B1-03 ToolResult -> Evidence atomic consume
B2-01 Orchestrator lease-aware main loop + persisted budget
B2-02 Remove inferred tool events
B2-03 AiChat/Investigation split
C-01 ClickHouseSpanSink + trace_spans dedup/fresh-query chain
C-02 Query API api/run-dispatch/alert runtime role split
C-03 Alert Kubernetes Lease + alert_rule_runtime_state
C-04 ai-llm-egress-proxy + Provider Key migration + default-deny NetworkPolicy
C-05 K8s API TLS verification + internal TLS
C-06 Control-plane metrics/correlation + local platform E2E/fault injection
C-07 MySQL backup/PITR/failover/RPO=0 candidate verification（本机 BLOCKED_BY_ENV）
D-01 Independent ai-action-executor deployment/boundary
D-02 Approval/action idempotency + UID/resourceVersion TOCTOU + scoped credential
D-03 execution_unknown reconciliation + rollback + verification + audit
```

任务依赖顺序固定为 `A0 -> A1 -> A2 -> B1 -> B2 -> C -> D`。同一阶段内部只有明确无共享 schema/API/状态依赖的任务才能并行；不得为并行开发复制 Run Store、State Machine、Replay Guard、Tool API 或 Action authority。

### 27.23 每个 AI 编码任务输入模板

每个任务必须写：

```text
Code baseline SHA
Current files/functions being changed
Existing authoritative model reused
Forbidden parallel model/API/table
Migration impact
Backward compatibility
Unit tests
Contract tests
Integration tests
Failure injection
Observed result: PASS/FAIL/BLOCKED_BY_ENV
```

AI 不得因为实现困难另起 `V2Store/NewRuntime/ToolGateway2`。

## 28. 实施完成判定

### 28.1 当前文档判定

```text
ARCHITECTURE_APPROVED_WITH_REQUIRED_AMENDMENTS
NOT_PRODUCTION_READY
```

这意味着：方案已经按当前 `main` 代码主线重新校准，可以指导后续编码；不意味着源码已经完成 Lease/Commit/Shared Replay/ToolRun 增强，也不意味着生产验收通过。

### 28.2 A-C 完成必须同时满足

- current remote Run persistence 不再有 production fallback read；
- Cancel/Transition/Runtime Commit 使用统一事务服务，command/commit 响应丢失可返回首次成功结果；
- Run execution Lease/epoch/token/claim history 经真实 MySQL 并发验证，Commit 在最终权威更新前重新校验 DB-time Lease；
- Shared Replay 使用 MySQL + `/internal/v1/security/replay/consume`，`consumer_service` 只能来自认证服务身份；
- Event Store/SSE 继续使用现有 `ai_run_events` DB sequence/tail 且无回归；
- `ai_run_outbox` 只承担 RunInvocation dispatch，并完成 stale-claim reclaim、dispatch epoch/token fencing 和 DB-time；
- Query API `api/run-dispatch/alert-eval` 三运行角色已拆开，`api` 角色不再隐式启动 Worker；
- Alert Evaluation 只有 Kubernetes Lease Leader 执行，cooldown/dampening 从 `alert_rule_runtime_state` 恢复；
- `/runs/unfinished` 只有 `control_plane.runs.recover.global` system identity 可分页扫描，tenant-scoped context 必须 403；
- canonical `/internal/v1/query/*` 形成 ToolRun 审计/幂等/Lease 边界，并统一 `complete/partial/failed` 数据质量语义；数据源错误不得伪装为空数据；
- Investigation 相对时间窗在 Run 创建时冻结为绝对时间，同一 ToolRun retry 不发生窗口漂移；
- ToolResult 服务端上限、`truncated`、digest 与 `RESULT_TOO_LARGE` 均通过边界测试，A-C 不隐式依赖 MinIO；
- ToolResult 不能跨 epoch/终态进入 Evidence，Evidence 保留来源、观测时间窗、完整性和 digest；
- Orchestrator 无 Runtime MySQL、VictoriaMetrics、VictoriaLogs、ClickHouse、MinIO、Kubernetes 数据面凭据；
- Chat 与 Investigation 边界清晰：`cluster_id=all` 不再破坏 canonical Chat；普通 Chat 不读取实时观测事实，实时问题只在显式 createRun 后进入 Investigation Tool/Evidence 主链；
- Trace 固定使用 `ClickHouseSpanSink -> trace_spans -> Query API -> ToolRun -> Evidence`，candidate profile 不允许 `SpanSink=nil`；
- `ai-llm-egress-proxy` 是唯一公网 LLM 出口，Stage C 结束时外部 Provider API Key 不再下发到 Orchestrator；
- `LLM_MOCK=false`、default-deny egress、内部 TLS、K8s TLS、schema/image/SBOM 都通过目标部署验证；
- Runtime/Lease/Outbox/Tool/Recovery/Replay/SSE/LLM/Alert control-plane metrics 与 correlation IDs 可查询且不泄露 secret；
- MySQL backup/PITR/restore contract 已落实；真实主库 failover 与 ACK 控制事务 RPO=0 在本机单节点环境标记 `BLOCKED_BY_ENV`，不得伪报 PASS；
- 所有目标提交测试重新执行，不引用历史绿灯替代当前验证。

### 28.3 Stage D

只有 28.2 全部达到生产候选门禁后，Stage D 才允许单独开启。Stage D 必须先部署独立 `ai-action-executor`，确认 Orchestrator 与普通 Query API 角色没有生产 mutation credential，再完成 Approval/Action 幂等、UID/resourceVersion TOCTOU、short-lived scoped credential、`execution_unknown` reconcile-before-retry、Rollback、Verification 与 Audit。完成前 `EXECUTION_MODE=disabled`；自动/无人值守处置不属于本合同默认上线范围。

