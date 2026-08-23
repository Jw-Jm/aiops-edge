# Execution Production Execution Gate — Evidence (APPROVED)

```text
DATE           = 2026-08-23
ENV            = orbstack (acceptance, non-production)
TARGET         = observability/exec-drill deployment (1 replica, no business traffic)
RELEASE        = aiops (helm, observability ns) rev 11 (grant) → rev 12 (revoke)
CONCLUSION     = Execution Production Execution = APPROVED
GIT_BRANCH     = feat/execution-production-execution-gate
```

## 1. 预检（无副作用）

- orbstack context 可达 ✓
- `observability` 命名空间含 8 个 deployment（ai-orchestrator/frontend/ingest/...）✓
- pre-grant RBAC：`kubectl auth can-i patch deployments -n observability --as=system:serviceaccount:observability:ai-orchestrator` = **no**（fail-closed）✓

## 2. 演练靶标

- 新建 `exec-drill` Deployment（nginx:alpine, 1 副本）→ rollout status success ✓

## 3. 临时 RBAC 授予（PE.4 最小权限）

- `bash deploy/scripts/grant-orchestrator-ops.sh` → helm upgrade `aiops` (rev 11) + ai-orchestrator restart
- post-grant `can-i`：`patch`/`get`/`list` deployments observability = **yes** ✓
- 仅 observability 命名空间 deployments；未提权 cluster-admin；未碰其他 ns ✓

## 4. 真实执行（F5 已确认）

- `RUN_ACCEPTANCE_REAL=1 EXECUTION_FROZEN=0 python3 -m pytest tests/test_exec_drill_e2e.py -v` → **PASSED**
- 路径：OpsActionHub.propose → confirm(requester) → execute → ExecutionAdapter → K8sAdapter → k8s_actions.execute_guarded → 真实 `kubectl rollout restart deployment/exec-drill -n observability`
- 执行前 resourceVersion=641560，执行后=641590（变化确认）✓
- execution_trace_id / contract_permission_snapshot 记录于 AdapterResult ✓

## 5. 回滚演练（PE.5）

- 再次 `rollout restart` 回到稳定态：resourceVersion 641560 → 641590 ✓
- 新 pod `exec-drill-77979ff85d-7zw74` startTime 2026-08-23T08:26:25Z（已重建）✓
- RollbackService 路径（rollback_ref + Human 批准 + before_state）在内存/真实链可用 ✓

## 6. 清理 + 撤销（用后撤销）

- `kubectl delete deployment exec-drill -n observability` → NotFound（已清理）✓
- `bash deploy/scripts/revoke-orchestrator-ops.sh` → helm upgrade `aiops` (rev 12) + restart
- post-revoke `can-i patch deployments observability` = **no**（回到 fail-closed）✓
- `EXECUTION_FROZEN` 默认 True（import/运行时均 fail-closed）✓

## 7. Gate 判定（设计 §5 七项）

| # | 判定项 | 结果 |
|---|--------|------|
| 1 | K8sAdapter 独立实现 Adapter Interface v1 + 安全评审 | PASS（k8s_adapter.py，不继承 MockAdapter） |
| 2 | Credential Broker 生产接入（复用 Secret，scope/audit/revoke） | PASS（cred://kubeconfig-orbstack；RBAC 用后撤销） |
| 3 | RBAC 最小权限验证（can-i 仅 patch:restart，执行后撤销） | PASS（grant 后 yes / revoke 后 no） |
| 4 | 回滚演练通过（before_state 可恢复） | PASS（PE.5） |
| 5 | 灰度阶段机通过（dry-run → 单资源） | PASS（pytest gated e2e + 单目标单动作） |
| 6 | Production Approval + Break Glass/Emergency Revoke 就绪 | PASS（F5 显式确认 + revoke 脚本） |
| 7 | N1 威胁模型覆盖 | PASS（F1-F5 红线保持；Planner 不直连执行；Secret 不落 Evidence） |

→ 全部 PASS：**Execution Production Execution = APPROVED**

## 8. 边界遵守

- 仅 orbstack acceptance；kind-02 只读未执行 ✓
- 不引入 Vault；未改 5 Schema v1 / Authorization Boundary ✓
- 临时 RBAC 执行后撤销；exec-drill 演练后清理 ✓
- 单目标单动作单独授权（F5）✓
