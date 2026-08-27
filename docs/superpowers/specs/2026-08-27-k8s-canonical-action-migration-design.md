# K8s 运维动作统一 Canonical Action 设计

## 背景与目标

当前 K8s 运维页仍调用已废弃的 `POST /api/v1/ops/k8s/preflight` 和
`POST /api/v1/ops/k8s/execute`。这两个路径已经由 `ai-orchestrator` 返回
410，而统一 Action 控制面已经具备 Action 持久化、审批、幂等和执行器派发能力，
造成前端契约与后端权威边界分裂。

本设计将 K8s 运维页迁移到 query-api 持有的 Canonical Action 边界，覆盖当前页面
暴露的七类动作，并保留当前安全约束：

- DeepFlow Agent 熔断器继续保持临时关闭，不在本变更中开启。
- `ai-action-executor` 继续使用 `EXECUTION_MODE=disabled`，真实 K8s mutation
  必须由服务端拒绝，验收不得把 dry-run 或拒绝响应伪报为成功。
- query-api/orchestrator 不新增 Kubernetes client；所有目标身份读取继续经
  `k8sboundary.ClusterClientManager`。
- DeepFlow ClickHouse 与平台 ClickHouse 的职责边界不变；本变更不读取或修改
  DeepFlow ClickHouse。

## 方案选择

采用“公共 Canonical Action proposal + 统一 decision/execute”方案：

1. 浏览器向 query-api `POST /api/v1/ai/actions` 提交人工动作候选。
2. query-api 校验 tenant/cluster、动作白名单和参数，通过 K8s 只读边界重新读取
   target UID/resourceVersion，计算不可变 action hash。
3. query-api 在单事务内创建 `ai_runs(status=awaiting_approval, action_mode=manual)`
   与 `ai_actions(status=proposed, dry_run=0)`；不写 run outbox，避免未审批动作被
   自动派发。
4. 审批中心继续调用 `POST /api/v1/ai/actions/{id}/decision`，审批决定和 action
   状态在同一事务中更新；批准时才写 `ai_action_outbox`。
5. 执行入口继续使用 `POST /api/v1/ai/actions/{id}/execute`。dispatcher 只向
   executor 发送签名上下文；当前禁用执行器返回明确 rejected，不发生真实 mutation。

相比恢复旧 `/ops/k8s/*` 路径，该方案只有一套 Action SoT，审批、审计、TOCTOU、
幂等和执行状态不会在两条链路间分叉。

## 公共 proposal 契约

请求：

```json
{
  "idempotency_key": "browser-generated-retry-key",
  "cluster_id": "canonical-cluster-uuid",
  "resource_type": "deployment",
  "namespace": "aiops-canary",
  "target_name": "aiops-mutation-canary",
  "operation": "scale",
  "params": {"replicas": 1}
}
```

规则：

- `idempotency_key` 必须为非空、长度受限的客户端重试键；同 tenant + key 重放时
  返回原 action/run；同 key 对应不同 hash 返回冲突。
- `cluster_id` 必须是 canonical UUID，且属于当前 JWT tenant。
- `resource_type`、`operation`、namespace/name 与参数必须通过统一白名单校验。
- `action_type` 固定为 `kubernetes`，risk 由服务端根据动作计算，不能由浏览器覆盖。
- target UID/resourceVersion 只能从 K8s 只读边界取得，不能信任浏览器传入值。
- 响应至少包含 `action_id`、`run_id`、`status=proposed`、`action_version`、
  `action_hash`、target UID/resourceVersion 和 `execution_status=proposed`。

## 动作模型

页面动作名保留用于用户界面，Canonical Action 的 operation 使用下表值；
`target_resource_type` 使用实际 Kubernetes kind 的小写形式。

| 页面动作 | 允许资源 | Canonical operation | 参数 | 风险与执行语义 |
| --- | --- | --- | --- | --- |
| `rollout_restart` | deployment/statefulset/daemonset | `rollout_restart` | 无 | 写操作；通过受控 `restartedAt` annotation 表达 |
| `scale` | deployment/statefulset | `scale` | `replicas`，0–10000 整数 | 写操作；修改 `spec.replicas` |
| `delete_pod` | pod | `delete_pod` | `grace_period_seconds`，0–600 整数 | 破坏性；删除目标 Pod |
| `evict_pod` | pod | `evict_pod` | `grace_period_seconds`，0–600 整数 | 破坏性；通过 eviction 子资源 |
| `cordon` | node | `cordon` | 无 | 破坏性运维动作；设置 `spec.unschedulable=true` |
| `uncordon` | node | `uncordon` | 无 | 恢复动作；清除 `spec.unschedulable` |
| `drain` | node | `drain` | `drain_timeout`，1–3600 整数 | 破坏性；先驱逐可驱逐 Pod 再 cordon |

预检必须为上述动作生成完整的 `target_spec`。对于 annotation，key 仅允许
`aiops.observability.io/` 或 `aio-action-executor/` 前缀；不得接受任意 JSON Patch。
所有动作均绑定 UID/RV 并纳入 hash，审批后再次做 TOCTOU 校验。

## 事务与状态

新增 store 方法 `CreateManualAction`，在同一 MySQL transaction 中：

1. 插入 canonical `ai_runs`，状态 `awaiting_approval`、版本 `0`、动作模式 `manual`。
2. 插入 canonical `ai_actions`，状态 `proposed`、`dry_run=0`、预检状态 `passed`。
3. 以 tenant + idempotency key 做幂等重放；不产生 `ai_run_outbox`。
4. 任一插入失败均回滚，禁止留下无 action 的审批 Run 或无 Run 的 Action。

已有 decision 事务保持不变，并增强 canonical hash 校验使用 Action 自身的
`target_resource_type`，不再硬编码 deployment。批准后 action outbox 才出现；
拒绝后 Run 进入 `cancelled`，Action 进入 `rejected`。

## K8s 访问边界扩展

扩展现有窄接口，不在 handler 或 executor 中加载 kubeconfig：

- `GetWorkloadIdentity(resourceType, namespace, name)`：deployment/statefulset/
  daemonset，返回 UID、RV、namespace、name 与受控 observed 摘要。
- `GetPodIdentity(namespace, name)`：返回 Pod UID/RV。
- `GetNodeIdentity(name)`：返回 Node UID/RV。

生产实现继续由 `k8sboundary.Client` 通过 kubectl/已校验凭据读取；query repository
负责 cluster identity 解析和统一错误映射。测试 fake 必须覆盖每种资源及身份漂移。

## 执行器契约

扩展 `ai-action-executor` 的内部 operation 白名单、目标类型读取、受控 patch/delete/
eviction/node 操作与 reconcile 判断，但不改变模式门禁：

- `disabled`：任何签名 Action 均返回 HTTP 403 + `status=rejected`，不得访问写 API。
- `manual`：只允许预览/干跑，不改变目标状态。
- `approved`：仅未来在显式环境变更后使用；必须有签名上下文、K8s client、UID/RV
  校验，并按 resource type 走固定 API 路径。

本轮真实环境保持 disabled，因此生产验收的执行项应验证“审批可持久化、派发被拒绝、
目标对象未变化、拒绝状态可查询”，而不是打开执行器。

## 前端迁移

`observability-frontend/src/api/k8s.ts` 删除旧 preflight/execute 数据契约，新增：

- `createK8sActionProposal()` → `POST /ai/actions`
- `decideAiAction()` → `POST /ai/actions/{id}/decision`
- `executeAiAction()` → `POST /ai/actions/{id}/execute`
- `getAiAction()` → `GET /ai/actions/{id}`

`K8sActions.tsx` 流程改为：预检即 proposal；展示 action hash、目标 UID/RV 和待审批
状态；不再加载或提交 `approval_task_id`；执行按钮对所有动作先显示 Canonical Action
确认信息，再调用 execute。对当前 executor disabled 的 403/状态 rejected 显示
“执行器已禁用，未发生真实变更”，并刷新 Action 状态。列表资源仍为只读。

审批页面读取 `ai_actions` 的 canonical 字段，不能把旧 `approval_task_id` 当作
Action 审批事实。

## 测试与验收

- Go 单元测试：七种动作白名单/参数、每种资源身份、canonical hash、tenant 隔离、
  幂等重放、事务回滚、审批版本冲突、disabled executor 不写 K8s。
- 前端测试：七种动作选择、proposal 请求体、proposal 状态展示、execute 只携带
  action_id、旧 `/ops/k8s/*` 不再被调用、disabled 错误文案。
- 部署后真实测试：使用现有非生产 canary Deployment 只创建 proposal，验证 GET
  action、tenant 隔离、审批前 execute 不能执行；不点击真实批准/执行，不改变 K8s
  对象。需要做审批写操作时，必须在动作发生前获得用户的明确确认。
- 验收证据记录 API HTTP 状态、响应中的非敏感状态字段、Pod 日志与目标资源前后
  resourceVersion/replicas；不记录 JWT、数据库密码、LLM key 或 kubeconfig。
