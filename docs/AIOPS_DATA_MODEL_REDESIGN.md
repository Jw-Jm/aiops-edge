# AIOps Agentic 数据模型重设计（Phase 1 冻结）

## 标识与隔离

所有受 tenant 或 cluster 影响的表必须显式保存：

```text
tenant_id       UUID / canonical tenant key
cluster_id      immutable UUID, never slug/name
```

资源身份必须使用 canonical cluster UUID：

```text
<kind>:<cluster_uuid>:<namespace>:<name>
```

Cluster Registry 的最低字段：

```text
cluster_id       immutable UUID primary key
slug             globally unique, lowercase DNS-like reference
name             mutable display name
tenant_id        owning tenant
credential_ref   Secret/Vault reference only
status           registered/ready/degraded/disabled/deleted
created_at
updated_at
deleted_at       lifecycle tombstone; UUID never reused
```

外部可传 UUID 或 slug，但在 API 边界立即 canonicalize；内部 DTO 使用 `ClusterRef` 与 `ClusterID` 两个不同概念。

## 控制面核心实体

### `ai_runs`

保存一次调查/诊断运行的生命周期：`run_id`、`tenant_id`、`cluster_id`、`user_id`、intent、action_mode、status、budget、started_at、ended_at、failure_code、correlation fields。状态只能由 Run 状态机推进，所有变更可审计。

### `ai_plan_steps`

保存 Investigation DAG 的节点、依赖、attempt、tool capability、status、timeout、started/ended time 和 structured error。必须禁止跨 run 依赖和隐式 cluster。

### `ai_tool_runs`

保存 Tool 输入摘要、schema version、`ToolResult`、错误、证据引用、耗时和调用者上下文。不得保存 Kubernetes 凭据或 Secret 内容；原始敏感字段必须脱敏/哈希。

### `ai_evidence`

保存标准证据引用：`evidence_id`、tenant/cluster、run/step、source_type、resource_ref、observed_at、content_ref/content_hash、reliability、supports/contradicts hypothesis。Evidence 不直接假设 slug 永久不变。

### `ops_actions` / `approval_tasks` / `verification_runs`

`OpsAction` 是结构化写操作的唯一 canonical contract；审批与执行必须记录 policy/risk/approver/resourceVersion/verification/rollback relation。rollback 使用新的 action id，不能覆盖原动作。

## 观测数据模型

统一的 Metrics、Logs、Traces、Events、Topology 记录必须包含：

```text
tenant_id
cluster_id
resource_identity
observed_at / timestamp
source
correlation_id（可选但必须能回溯）
```

raw logs 写入 VictoriaLogs；ClickHouse 的 `log_records`、`trace_spans`、`service_topology`、`alert_events` 是派生查询模型。所有数据源必须在 ingest/adapter 边界补齐 canonical UUID，缺失或无法解析时拒绝写入并返回结构化错误；不得退回字符串 `default`。

## 授权模型

```text
Principal(user/service)
  → Role
  → Permission(action)
  → ScopeAssignment(tenant/cluster/namespace/resource)
```

授权决策按最具体 scope 匹配，权限不足即拒绝。JWT 和 TrustedRequestContext 不保存授权事实；它们只提供需要重新验证的身份/委托上下文。

## 生命周期与迁移

- Phase 1 不做历史运行数据迁移。
- 新 schema 上线时同一 Phase 停止旧 writer/reader；不建立长期双写。
- 删除/重建 cluster 产生新 UUID；旧数据保留生命周期归属，slug 可复用但不改变历史关联。
- 数据清理必须由 Phase 0 manifest 驱动，目标路径逐项确认，UNKNOWN 不删除。

