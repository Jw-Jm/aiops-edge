# AIOps Action 工作流发布与回滚手册

本手册对应 canonical Action V2、Query API transactional outbox、Action Executor
真实状态 reconcile 和独立 Verification。任何生产 mutation 都必须按顺序通过 G0-G5；
未通过时保持 `EXECUTION_MODE=disabled`。

## 发布前门禁

1. G0 Run correctness：创建、派发、恢复和终态均在 Query API/MySQL，确认同一
   `run_id` 全链路一致。
2. G1 Evidence correctness：数据源调用均有 ToolRun/Evidence，RCA 引用可回查。
3. G2 Recovery：验证 dispatcher lease 过期、响应丢失、数据库短暂不可用不会重复
   mutation；`execution_unknown` 只能走真实 GET reconcile。
4. G3 Boundary convergence：浏览器只调用 Query API，Orchestrator 无写 RBAC，
   Action Executor 是唯一 mutation 边界。
5. G4 UI truth：审批页只读 `/api/v1/ai/actions`，SSE 断线后按 `after_sequence`
   恢复，不从本地状态推断成功。
6. G5 Controlled action：先 fake/canary executor dry-run，再做单目标 canary。

建议在 CI 与发布流水线执行：

```bash
make test-workflow-all
helm lint deploy/helm/aiops
./deploy/scripts/verify-aiops-workflow-gates.sh
```

### 本机验证记录（2026-08-25）

在当前工作区执行了以下本机门禁，均以退出码 0 完成：

- `make test-workflow-all`：Go、4 个 workflow contract、Action Executor、Python `1167 passed/1 skipped`、前端 `4 tests passed` 和生产构建。
- `./deploy/scripts/verify-aiops-workflow-gates.sh`：Helm lint/render、RBAC 和生产安全开关检查通过。
- 本机代码门禁阶段保持 `EXECUTION_MODE=disabled`、`realMutation=false`，没有修改生产 Helm 配置。

随后在 OrbStack 本地集群中完成了一次隔离真实 mutation 验证：

- 新建 `action-test` namespace 和一次性 `aiops-local-canary` Deployment，使用当前 arm64 编译的 Executor 镜像及一次性 Ed25519 签名公钥。
- Executor 临时以 `EXECUTION_MODE=approved`、`POD_SA_ACCESS=true` 启动；专用 ServiceAccount 仅有 `deployments get/patch` 权限，`delete` 权限为 `no`。
- 签名 annotation patch 返回 HTTP 200 `success`，目标 UID 保持一致，annotation `aio-action-executor/local-validation=passed` 已真实写入。
- 使用旧 resourceVersion 重放同一请求返回 HTTP 409 `TOCTOU drift`，没有发生第二次 mutation。
- 签名 reconcile 返回 HTTP 200 `applied`，并明确返回“不重试”。
- 验证证据采集后删除了整个一次性 `action-test` namespace，确认本地没有残留 approved Executor 或测试工作负载。

这证明本机真实 Kubernetes patch、TOCTOU 防护、RBAC 限制和 reconcile 链路成立；仍不替代生产环境人工变更审批、生产目标 canary 和观察窗口。

## 阶段化发布

### 1. Schema + shadow

先应用 `0009_action_workflow_closure.sql`，确认 migrator 完成且旧 migration checksum
不变。部署 Query API/Orchestrator/Executor，但保持：

```text
EXECUTION_MODE=disabled
LEGACY_APPROVAL_COMPAT=0 (production)
LEGACY_DIRECT_MUTATIONS_ENABLED=0
LEGACY_FLOW_RUNTIME_ENABLED=0
```

检查 action preflight 只生成 Deployment `patch|scale`，且 action hash、UID、resourceVersion
与 MySQL 记录一致。

### 2. Canonical UI

启用审批中心 canonical Action 查询，观察 `ACTION_PREFLIGHT_FAILED`、hash conflict、
outbox backlog、lease fencing 和 `execution_unknown` 指标。旧 `/ops/tasks` 仅保留
兼容读/遥测，不作为生产写入源。

### 3. Fake/canary Executor

使用无写入的 fake/canary Executor 验证：批准事务同时写 approval、Action outbox 并将
Run 推进到 `executing`；dispatcher 重启或响应丢失只生成一个 Attempt；reconcile 读取
到目标不匹配时返回 `reconcile_required`，不能自动 retry。

### 4. 单目标 mutation

仅当 G0-G4 连续通过且审批人确认后，将单个测试 namespace、单个 Deployment 置于：

```text
EXECUTION_MODE=approved
POD_SA_ACCESS=true
ai-action-executor.realMutation=true
```

确保 Executor 验签公钥、限定 Role（仅目标 namespace 的 Deployment get/patch）、
LLM proxy readiness、worker/query-api readiness 均为 green。先执行 `scale`，再执行
受控 annotation `patch`；不开放 `restart`、任意 shell 或跨 namespace。

## 监控与自动回滚

以下任一条件立即回滚到 `EXECUTION_MODE=disabled` 并暂停审批：

- 同一 action_id 出现多个 approved decision、多个 outbox command 或多个 running Attempt；
- `execution_unknown` 无法在两个 reconcile 周期内收敛；
- UID/resourceVersion drift、tenant/cluster/hash conflict 非预期增长；
- Run stuck 在 `executing|verifying` 超过恢复周期两倍；
- Verification 为 `regressed` 或 `inconclusive` 的比例超过 canary 基线；
- Executor readiness、签名校验或 LLM egress proxy readiness 失败。

回滚顺序：停止新审批 → 将 Executor 设为 disabled → 保留 outbox/Attempt/Verification
用于审计与人工 reconcile → 恢复上一版本 Query API/worker → 确认所有未终态 Run 都有
可恢复 owner 或显式 `cancelled` 终态。不要删除 outbox、Attempt、reconcile 或事件记录。

## 兼容路径退出

一个稳定版本且无 legacy 写入后，删除 `/ops/tasks/{id}/approve|reject`、同步
`/ai/actions/{id}/execute` 的实际执行逻辑和旧 Flow runtime；保留返回 410 的明确错误，
避免客户端静默回退。最后更新本手册中的实际指标、canary 目标和回滚演练日期。
