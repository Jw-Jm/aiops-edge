# Execution Production Execution Gate — Design Spec

```text
CONTRACT        = V9.3 DEEPSEEK EXECUTION R4 (Phase 8+ Manual-Triggered)
BASE           = V9.3_EXECUTION_PRODUCTION_ENABLEMENT_GATE_DESIGN.md (PE.1-PE.7)
               + V9.3_PRODUCTION_ENABLEMENT_IMPLEMENTATION_EVIDENCE.md (PE.3/4/6/7, 291 passed)
               + AIOPS_PRODUCTION_ENABLEMENT_DOSSIER.md (orbstack acceptance 预检)
VERSION         = v0.1
STATUS          = DESIGN APPROVED (2026-08-23, 自审核后执行)
GIT_ACTION      = COMMIT (本分支收口阶段提交)
DATE            = 2026-08-23
```

本 Spec 将 Execution Production Execution Gate 端到端打通：补齐 PE.1/PE.2（真实
Adapter 安全评审）+ PE.5（回滚演练），解除 `EXECUTION_FROZEN`，并在 orbstack
acceptance 做第一次真实单动作演练，使 Production Execution 从 NOT APPROVED 走到
APPROVED。

---

## 0. 范围与红线

- 真实执行目标：`exec-drill` Deployment（1 副本，无业务流量，observability 命名空间）
  + `rollout restart`（单目标单动作，F5）。
- 环境：orbstack acceptance（非生产）。kind-02 第二集群仅只读接入，不做执行。
- 允许动作白名单：restart / scale / patch_resource（dry-run 合法）；delete / evacuate /
  create 明确禁止（PE.4 ForbiddenAction）。
- 红线保持（F1-F5）：Human 签名 / 三+一身份 / Secret 不落 Evidence / Planner 不直连执行 /
  单目标单动作单独授权。
- **真实 `kubectl rollout restart` 执行前，执行 Agent 必须停下向用户做最后一次显式确认
  （F5 硬约束 + dossier §12）。自审核通过不等于静默触发真实变更。**

---

## 1. 真实执行链架构

```text
PlanProposal(awaiting_approval)
  → Human Approval（Ed25519 签名，EX.1，requester ≠ approver）
  → ExecutionContract(active, signed)
  → Policy ALLOW（真实 SoT，EX.6）
  → ExecutionPreview approved（resource_version 绑定，EX.5 verify_no_drift）
  → Credential Broker short-lived（EX.2，复用 kubeconfig-orbstack Secret）
  → ExecutionAdapter.execute（安全链：scope 二次校验 + 签名 + 幂等 R4.1 + 权限快照 R4.2）
       └─ 真实模式委托 K8sAdapter（PE.1，独立实现 Adapter Interface v1）
            └─ k8s_actions.execute_guarded（preflight token + 审批门 + 资源版本乐观锁 + 白名单）
                 └─ kubectl（真实系统）
  → success → executed | 失败/regressed → RollbackService（Human 批准，EX.4）
```

- `ExecutionAdapter`（execution_adapter.py）已是内存 MVP 安全链，**不连真实 K8s**，保留。
- 新增 `K8sAdapter`：**独立实现 `Adapter Interface v1`**（N2：不继承 MockAdapter），内部
  复用 `k8s_actions.execute_guarded` 作为真实执行引擎。
- `ExecutionAdapter` 增加真实模式开关：配置 `K8sAdapter` 后，`execute` 在 scope/签名/幂等
  校验通过后委托给 `K8sAdapter`，而非内存模拟。
- `ops_action_hub.py`：`EXECUTION_FROZEN` 改为由配置/环境变量控制（默认冻结，演练时显式
  解冻），解冻路径接入 `ExecutionAdapter` → `K8sAdapter`。

## 2. 真实 RBAC（最小权限，执行后撤销）

- orchestrator 当前 ClusterRole 只读（实测 `kubectl auth can-i patch deployment` = no）。
- 真实执行前：经 helm 授予 `observability` 命名空间**仅** `patch` subresource
  `deployments/restart` 的 RoleBinding（PE.4 映射：restart → get/list/patch-restart）。
- 执行完毕后立即撤销该临时 RBAC（dossier §3"用后撤销"）。
- 不提升为 cluster-admin；不碰其他命名空间；禁止 delete/create/evacuate。

## 3. 凭据与红线

- 凭据复用既有 `kubeconfig-orbstack` Secret（k8sboundary SA，已存在），经 Broker 引用，
  **不落 Evidence**（F3）。
- 无 Vault 后端（PE.3 安全评审列为后续专项，本 Spec 不引入 Vault）。
- F1 Human 签名 / F2 三+一身份 / F4 Planner 不直连执行 全部保持。

## 4. 演练剧本（PE.5 回滚 + PE.6 灰度）

1. 新建 `exec-drill` Deployment（1 副本，无业务流量，observability 命名空间）。
2. Stage0 dry-run：`K8sAdapter.dry_run` + `k8s_actions.preflight`（生成 token，无副作用）。
3. Stage1 单资源真实执行：`rollout restart exec-drill`（真实 kubectl）。
4. 验证：读 `exec-drill` 的 `resourceVersion` / pod 重启时间，确认 before→after 变化。
5. 回滚演练：`rollout restart` 本身可逆，再次 restart 即回到稳定态；验证 RollbackService
   路径（rollback_contract_id + Human 批准 + before_state）在内存/真实均可用。
6. 清理：删除 `exec-drill` Deployment（演练产物，非生产数据）。

## 5. Gate 判定标准（PASS 条件）

1. K8sAdapter 独立实现 + 安全评审通过（N2）
2. Credential Broker 生产接入（复用 Secret，TTL/scope/audit/revoke）
3. RBAC 最小权限验证（can-i 仅 patch:restart，执行后撤销）
4. 回滚演练通过（acceptance，before_state 可恢复）
5. 灰度阶段机通过（dry-run → 单资源）
6. Production Approval + Break Glass/Emergency Revoke 就绪
7. N1 威胁模型覆盖

→ 全部 PASS 后：`Execution Production Execution = APPROVED`

## 6. 边界与最后确认

- 真实执行前一次显式确认（F5）；预检（can-i / 连通性 / dry-run / 审批链 / 回滚演练）本轮全做。
- 不引入 Vault；不碰 kind-02 执行；不修改 5 Schema v1 / Authorization Boundary。
- 临时 RBAC 执行后撤销；`exec-drill` 演练后清理。

## 7. 交付物

- 代码：新增 `k8s_adapter.py`（K8sAdapter）；增强 `execution_adapter.py`（真实模式委托）；
  增强 `ops_action_hub.py`（可配置解冻）；helm 临时 RBAC 模板/脚本。
- 测试：K8sAdapter 单测（scope/dry_run/委托 execute）；端到端演练测试（dry-run→真实→回滚，
  acceptance 标记，默认跳过真实执行）。
- 文档：本 Spec + 执行后 Evidence（含预检结果、真实执行记录、回滚验证、Gate 判定）。
- Git：本 Spec 与代码随 writing-plans 阶段提交。
