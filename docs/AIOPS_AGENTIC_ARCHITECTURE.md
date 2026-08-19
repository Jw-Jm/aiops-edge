# AIOps Agentic 目标架构（Phase 1 冻结）

本文将 `aiops-agentic.md` 中已经确认的决策落到可实现的代码边界。它是实施约束，不是对当前代码状态的描述；当前状态以 Phase 0 maps 和 `BEFORE_BASELINE.md` 为准。

## 请求与信任边界

```text
Browser / Alert
      │ JWT: user_id + session_id + short-lived claims
      ▼
query-api
  ├─ MySQL: current user/session/tenant/cluster/action authorization
  ├─ Cluster Resolver: UUID or slug → immutable canonical cluster_id
  ├─ Kubernetes Read Access Boundary
  └─ Control Plane Persistence
          ▲
          │ /internal/v1 + Service Credential + signed TrustedRequestContext
          ▼
ai-orchestrator
  ├─ Intent / Planner / Investigation DAG
  ├─ Tool Registry / Agents / RCA
  └─ Risk / Approval / structured OpsAction request
```

orchestrator 只能持有 `user_id`、`session_id`、`tenant_id`、canonical `cluster_id`、`run_id` 和 capability 上下文，不得持有 Kubernetes credential、kubeconfig、Secret 内容，也不得直连 MySQL。

## 身份、认证与授权

- MySQL 是用户、角色、权限、Tenant、Cluster 授权和 session 状态的唯一动态权威。
- JWT 证明“谁”和“哪个 session”，不承载 roles、permissions、tenant_ids、cluster_ids 或管理员结论。
- 服务间认证由独立 Service Credential 完成；委托上下文由短时签名 `TrustedRequestContext` 完成。两者密钥分离、可轮换、必须校验 issuer/audience/iat/exp/nonce/replay。
- TrustedRequestContext 不是授权凭证。query-api 每次接收内部请求仍需按 MySQL 当前状态验证 user/session/tenant/cluster/action。
- Tenant、Cluster、Namespace、Resource、Action 是授权 scope 层级；拒绝或缺少具体授权不得因 broad role 自动扩权。

## Cluster Registry 与 Kubernetes Access Boundary

```text
cluster_ref (UUID or slug)
        │
        ▼
Cluster Registry → immutable UUID cluster_id
        │ credential_ref only
        ▼
Kubernetes Access Boundary → Secret/Vault → per-cluster client
```

- `cluster_id` 是不可变 UUID；`slug` 唯一可读且可受控重命名；`name` 只展示。
- 所有持久化 observability、Evidence、Run、Audit、Execution 和权限数据只以 UUID 关联。
- 每个 cluster 使用独立 Secret；MySQL 只存 `credential_ref`。
- Kubernetes client/cache 必须以 canonical UUID 为 key，凭据轮换必须失效旧 client。
- 读取路径经过 query-api 的 Kubernetes Read Adapter；写入路径经过 Execution Adapter。
- LLM、Planner、Agent、Tool 不得生成或提交裸 `kubectl`/shell 作为 canonical action。

## Read Plane / Write Plane

Read Plane 允许标准化 `get/list/watch/describe/logs/events/status` 等只读 capability，返回带 `tenant_id`、`cluster_id`、resource identity、observed_at 和结构化错误的结果。

Write Plane 只接收结构化 `OpsAction`，执行顺序固定为：

```text
Intent → Plan → Risk → current Authorization → Approval → Execution Adapter → Verification
```

回滚是新的 OpsAction；R3/R4 回滚在 V1 必须重新授权和审批，并重新绑定目标 `resourceVersion`。R4 shell 仅作为人工显式、严格 allow/deny、审计和超时限制的应急通道。

## AI Runtime 与控制面持久化

- orchestrator 负责 Intent、Planner、DAG、Tool 调度、RCA 和运行状态机的业务决策。
- query-api Control Plane Persistence 是 MySQL 的唯一物理写入边界；orchestrator 通过内部 API 写 `ai_runs`、`ai_plan_steps`、`ai_tool_runs`、`ai_evidence`、approval/execution/audit 等控制面记录。
- SSE 的 sequence、持久化和 replay 由 orchestrator 负责生成/消费语义，但落库仍经控制面接口；事件必须可按 `run_id + sequence` 恢复。
- Tool 返回统一 `ToolResult`/`StructuredError`；失败必须可区分权限拒绝、参数错误、超时、上游不可用、证据为空和内部错误。

## 存储职责

- VictoriaLogs 是 raw logs 主存储。
- ClickHouse 保存 logs/traces/topology/events 的派生查询模型。
- VictoriaMetrics 保存 metrics 时序查询路径。
- MySQL 保存身份、授权、Cluster Registry、配置、AI Runtime、审批、执行、审计和有效知识资产。
- 旧运行历史不迁移，Phase 16 只按 manifest 在本机验收环境清理，UNKNOWN 资产不得删除。

## Phase 1 边界

Phase 1 只冻结 ownership、数据模型和跨语言契约，新增类型、fixture 和验证；不切换生产路由、不启用新 writer、不删除旧 schema/页面/依赖、不接触真实凭据。后续 Phase 必须以本文和 contract fixture 为输入。

