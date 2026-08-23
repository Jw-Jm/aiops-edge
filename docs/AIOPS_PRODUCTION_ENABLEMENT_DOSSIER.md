# AIOps — Execution Production Enablement Dossier（生产执行准入审查与无副作用预检）

Status: **PREFLIGHT**（只做准入审查与无副作用预检，**不执行任何真实 K8s/OpenStack 变更**）
Date: 2026-08-23
GIT_ACTION: COMMIT (本分支收口，仅代码+文档，不触发真实执行)
红线: **Execution Production = NOT APPROVED**（保持，直到单目标单动作单独授权）

> 本 dossier 是生产执行准入审查的档案。所有项当前为**预检/规划**，不授权执行。
> 约束：只运行 `can-i`、连通性、`dry-run`、审批链和回滚演练；**不执行 `patch/scale/restart/delete`**。

## 1. 目标环境

| 项 | 值 |
|----|-----|
| 环境 | orbstack acceptance（非生产） |
| 命名空间 | observability |
| Cluster | canonical `91771a6e-9c2d-11f1-8271-bea176fe9f9f` |
| 第二集群 | kind-02 `84f7e5a3`（只读接入，不做执行） |
| 主要服务 | query-api/orchestrator/ingest/event-collector/frontend |

## 2. 允许动作白名单（当前）

- 执行准入的**动作白名单**来自 ExecutionContract `allowed_actions`（白名单缺省拒绝）。
- 当前 ExecutionAdapter 二次校验 `request.action ∈ contract.allowed_actions`（fail-closed）。
- **本预检阶段允许验证的动作**（仅 dry-run/回滚演练，不真实执行）：
  - `restart`（受控目标，仅 dry-run）
  - `scale`（受控目标，仅 dry-run）
  - `patch_resource`（受控目标，仅 dry-run）
- **明确禁止**（F5 红线）：`delete`、`evacuate`、`create`（RBAC mapper ForbiddenAction，PE.4）。
- 真实执行（patch/scale/restart）**待单目标单动作单独授权**，本 dossier 不授权。

## 3. 最小 RBAC（当前）

- orchestrator ClusterRole **只读**（pods/replicasets/events/pods-log get/list/watch）。
- 实测：`kubectl auth can-i patch deployment` → **no**（无写权限）；`get deployment` → **yes**（只读）。
- **真实执行需要临时最小写 RBAC**（如受限 namespace 的 patch:restart/scale），待授权时单独评估授予，用后撤销。

## 4. Vault/凭据引用（当前）

- 凭据经 **Credential Broker**（EX.2）获取，不直接持明文凭据。
- credential_ref 只引用（k8s-secret://），Secret 内容不落 Evidence（F3）。
- kubeconfig-orbstack/kubeconfig-kind-02 Secret + ADMIN_KUBECONFIG env（只读）。
- **无 Vault 后端**：当前用 K8s Secret 承载，Vault 接入列为后续专项（PE.3 安全评审）。

## 5. 审批人

- ApprovalService：requester != approver（SELF_APPROVAL 拒，admin 也不例外）。
- 跨 cluster approval 拒绝（CROSS_CLUSTER_APPROVAL）。
- 审批绑定 action hash/version/target/risk/resourceVersion（mutation 失效需重批）。
- **审批链为预检项**：验证流程可用，不触发真实执行。

## 6. 维护窗口

- 建议维护窗口：低峰时段（如工作日晚间 / 周末）。
- **当前未设定具体维护窗口**——待单目标单动作授权时由用户指定。

## 7. 资源版本

- ExecutionPreview 绑定 `resource_version/cluster_id/namespace`（防漂移）。
- 执行前校验当前版本 == Preview 记录（verify_no_drift，PreviewDrift 拒绝）。

## 8. 幂等键

- R4.1：`idempotency_key` 防网络 timeout retry 导致执行两次。
- 同 `idempotency_key` 二次 → 返回已执行结果（不重复执行）。
- execution_request_id + idempotency_key 双重防护。

## 9. 回滚方案

- RollbackService（EX.4）：rollback 生成新 action_id/version/hash（不复用原 contract）。
- 需 Human 批准 + 无 before_state 拒绝。
- 回滚目标 = before_state。
- **本预检只做回滚演练（dry-run），不触发真实回滚**。

## 10. 停止阈值

- **regressed**（verification 用 SLI 判定 after<before）→ REGRESSION_STOP 停止后续自动 action。
- **partial**（after>=before 未达标）→ 不推进。
- **failed**（exit_code!=0 只是 fact，SLI 判定）→ 停止。
- 连通性失败 / 写失败率超阈值 → 立即停止并回滚。

## 11. 无副作用预检清单（本阶段执行，不真实变更）

| 预检项 | 状态 |
|--------|------|
| can-i（orchestrator 只读，patch=no） | PASS（确认 F5 保持）|
| 连通性（query-api/orchestrator/VM/VLogs/CH） | 待执行 |
| ExecutionAdapter dry-run | 待执行（In-memory 模拟）|
| 审批链演练（ApprovalService） | 待执行（In-memory）|
| 回滚演练（RollbackService dry-run） | 待执行（In-memory）|
| Credential Broker 引用解析 | 待执行（只读）|
| 资源版本校验（verify_no_drift） | 待执行（In-memory）|

## 12. 结论与下一步

- **本 dossier 是准入审查与无副作用预检**，**不授权任何真实执行**。
- 预检完成后，**针对某一个明确目标、某一种明确动作**，请求一次单独的真实执行授权。
- 在收到该单项授权之前，**Execution Production = NOT APPROVED** 保持不变。
- 红线 F1-F5 保持；GIT_ACTION=NONE。
