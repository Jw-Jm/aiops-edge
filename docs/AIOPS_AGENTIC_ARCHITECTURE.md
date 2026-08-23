# AIOps — Agentic Architecture (V9.3 reconciled)

Status: **IMPLEMENTED**（Phase 21 P21.1 Architecture Reconciliation，与真实运行代码一致）
Date: 2026-08-23
GIT_ACTION: NONE

本文档是 AIOps 平台最终架构，与真实运行代码一致。覆盖 V9.2（P1-P6）+ V9.3（P7-P21）当前实现状态。

## 产品端态（Journey A/B）

- **Journey A（问题→根因）**：user/alert/page → tenant → canonical cluster → resource → time range → Intent → Planner → Investigation DAG → Domain Agents → Tools → ToolResult → Evidence → Hypothesis → Contradiction → Missing Evidence → Follow-up → Root Cause Ranking → Confidence → Unknowns。
- **Journey B（根因→恢复）**：Root Cause → Structured OpsAction → Authoritative Risk → Current Authorization → Confirmation/Approval → Precheck → Execute → Observation Window → Verification → success/partial/failed/regressed → Incident Candidate。

用户从不手动选择 K8sGPT/RAG/Tool/Agent/Workflow/LangGraph/MCP。

## 单一生产主链（真实实现）

```text
Browser → JWT Identity → query-api → MySQL Real-time Authorization
→ Tenant Scope → Canonical Cluster Resolution → RunInvocationContext
→ ai-orchestrator → Intent → Planner → Investigation DAG → Domain Agents
→ Tool Registry → TrustedRequestContext → query-api Trusted Data Boundary
→ ToolResult → Evidence Hub → Hypothesis RCA → Missing Evidence Investigation
→ Root Cause Ranking → Structured OpsAction → Authoritative Risk Evaluation
→ MySQL Real-time Authorization → Confirmation/Approval → Execution Adapter
→ Verification → Incident Candidate / Learning
```

**不得共存**：旧 AI Chat 主路径、prompt-only RCA、旧 Tool Router、旧 Workflow investigation、旧 Session/Checkpoint、旧 Schema Adapter、default tenant/cluster fallback、JWT role/scope authority、client tenant/cluster authorization。

## 控制面职责（真实实现）

| 组件 | 职责（当前实现） |
|---|---|
| **query-api** | 外部信任边界；JWT 身份 + MySQL 实时授权；canonical cluster/resource 解析；AI 代理（RunInvocationContext/RunControlContext）；Trusted Data Boundary；Execution Policy Engine；MySQL 持久化 owner（ai_runs/ai_actions/ai_evidence 等）；SSE 公共代理/replay；浏览器唯一入口；`/internal/v1/query/*` 内部边界（internalScopeAuthorized 校验 cluster 属 tenant） |
| **ai-orchestrator** | Run 状态机 owner；SSE event 语义 owner；sequence allocator；Intent/Planner/Agents/Tool Registry；Evidence Hub；Hypothesis RCA（RcaEngine 单一编排）；remediation（approval/execution 链）；**不持有 kubeconfig/credential 内容** |
| **ingest / event-collector** | 写 new-schema 可观测 + 资源事件（VM/VLogs/CH）；TelemetryWriterMode=new |
| **frontend** | 任务驱动 IA；调查工作台；evidence deep links；无直接 orchestrator 访问 |

## 三个内部上下文（真实实现）

- `RunInvocationContext`（query-api → orchestrator）：create Run。
- `RunControlContext`（query-api → orchestrator）：control 既有 Run。
- `TrustedRequestContext`（orchestrator → query-api）：tool/data 访问，scope_kind cluster 或 run。

**服务身份 — EdDSA**：每方向独立 Ed25519 keypair；verifier 只持对向公钥。JWS Compact Serialization，alg=EdDSA，typ=AIOPS-CONTEXT，kid=key-id。Replay 保护（nonce+replay cache）统一三上下文。Service Credential（X-Internal-Token）与 context signing 分离。

## V9.3 关键组件（真实实现）

### 可信分析（P7）
Tool Registry → InternalQueryClient → TrustedRequestContext → query-api `/internal/v1/query/*`（唯一事实路径，禁 direct DB/K8s）。

### 七类 Agent + Resource Graph（P8）
Observability/Log/Trace/K8s/Change/Knowledge/Infra 七类 Agent，统一 AgentRuntimeFramework 执行 PlanStep；Agent=Evidence collection+Insight generation，**≠ Execution**。Resource Graph V1 经 query-api 采集 typed graph。

### 执行基础设施（Execution Infra + Production Enablement）
ExecutionContract（一次性许可证）+ ExecutionIdentity + ExecutionAdapter（restricted_shell/patch allowlist）+ Policy Engine（Context 来源冻结）+ ExecutionPreview + Approval Signature（Ed25519）+ Credential Broker + Rollback + AuthorizationSoTProvider + RBAC mapper + GrayRollout + ProductionApproval。

### Phase 9-11
- P9 Hypothesis RCA：RcaEngine 单一编排（Snapshot→Hypothesis→Support→Contradiction→Missing→Scoring→Ranker→Timeline→Unknown-safe）；跨 cluster EvidenceScopeMismatch 阻断。
- P10 Run Persistence/SSE/Recovery：PersistentRunRepository 远端提交优先（HTTP 失败 fail-closed 不推进）；SSE tenant 校验；进程重启恢复。
- P11 Remediation：ApprovalService 接 query-api 权威 SoT；verification 用 SLI 非 exit code；regression stop；rollback 新 action。

### Phase 13-20
- P13 服务端安全加固：AuthorizationMatrix fail-closed + role tamper 服务端忽略。
- P18 部署：5 镜像（query-api/ingest/collector/orchestrator/frontend）。
- P19 真实环境 + 多集群：kind-02 接入（Cluster Registry + 中央凭据 + 受控 OTLP generator + 隔离 Gate `internalScopeAuthorized`）。
- P20 缺陷收口 + 最终构建：`v1.2.0-p20-24b157a0`（source_tree_hash `24b157a08a02f6b469dffa3bdc0008264c2f72cdbb95f0adf0abb32361d3b866`）。

## 存储 SoT（真实实现）

- **MySQL**（`aiops`）：授权/审计/配置/catalog/Run/Approval 权威 SoT（52 表）。
- **VictoriaMetrics**（VM）：metrics 权威 SoT（new writer ACTIVE）。
- **VictoriaLogs**（VLogs）：logs 权威 SoT（new writer ACTIVE）。
- **ClickHouse**（`observability`）：k8s_events/alert_events/log_records/trace_spans/service_topology（legacy 数据保留，只停流量不删数据）。
- **Chroma**：orchestrator 知识库（ops-cases，2 collections）。

## 多集群架构（Phase 19 真实接入）

- 中心数据面（VM/VLogs/CH 共享），kind-02 不建第二套 SoT。
- Cluster Registry：kind-02 `84f7e5a3`（identity_uid `ea994341`）注册 ready；canonical cluster `91771a6e`。
- 隔离 Gate：`/internal/v1/query/*` 错误 tenant/cluster → 403；已授权空 → no_data；跨 cluster RCA `EvidenceScopeMismatch` 阻断。

## 边界与红线（当前保持）

- **红线 F1-F5**：F1 Human signature / F2 三+一身份 / F3 Secret 不落 Evidence / F4 Planner 不直连执行 / F5 不触发真实业务执行。
- **Execution Production Execution = NOT YET APPROVED**（Phase 11 只验证审批/执行链路 dry-run，不落地真实 K8s/OpenStack 变更）。
- **Agent ≠ Execution**：Agent 无 execute/self_execute/credential/kubeconfig。
- **不得把未实现的 Incident/Detection/Edge Autonomy/Autonomy Level 写成正式架构**（未实现）。
- GIT_ACTION = NONE；Phase 21 后状态 = `WAITING_USER_AUTHORIZATION_FOR_GIT_COMMIT_AND_PUSH`。

## 实现状态总览

```text
V9.2 Phase 1-6:  COMPLETE（P6 cutover 完成，new SoT ACTIVE，legacy 只停流量）
V9.3 Phase 7:    COMPLETE（可信分析 10 组件）
V9.3 Phase 8:    COMPLETE（七类 Agent + Resource Graph + ARI）
V9.3 Phase 9-11: COMPLETE（Hypothesis RCA / Run Persistence / Remediation）
V9.3 Phase 12-17: COMPLETE（前端收敛 / 权限 / 删旧 / 依赖 / 测试 / 数据重置）
V9.3 Phase 18-19: COMPLETE（构建部署 / 真实环境 + 多集群）
V9.3 Phase 20:   COMPLETE（缺陷收口 + 最终构建 v1.2.0-p20）
V9.3 Phase 21:   IN_PROGRESS（文档 + Git 准备）
Execution Production Execution: NOT YET APPROVED
```
