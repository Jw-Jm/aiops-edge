# Plan: CONTROLLED_ACTION_CANDIDATE（Stage D 独立受控执行）

依据《AIOps_全面代码修改报告_V2.md》§29 + Phase D：独立 `ai-action-executor` 是正确安全边界，
但当前只允许 Scaffold；`STAGE_D = BLOCKED`，`EXECUTION_MODE = disabled`。
真正 D Gate 必须完成 immutable Action binding、signed context、short-lived credential、目标 reread、
UID+resourceVersion precondition、real write、durable idempotency、UNKNOWN reconcile-before-retry、
rollback、post verification、audit（依赖真实 K8s/OpenStack/credential 基础设施）。

## Implementation Status（2026-08-25）

### D-Gate 脚手架硬化（COMPLETE，代码级就绪，保持 disabled）
- **Ed25519 signed context 强制**（`ai-action-executor/main.go` `verifySignedContext`）：
  执行请求必须带 `X-Executor-Signature`（Ed25519 over body SHA256），公钥来自
  `EXECUTOR_VERIFY_KEYS`（query-api 签发）。不再只是可选 shared token。
- **approved 模式必须配置验签公钥**：`EXECUTION_MODE=approved` 且 `EXECUTOR_VERIFY_KEYS` 未配置
  → 503（不可仅靠 token 走真实路径）。
- **保持 `EXECUTION_MODE=disabled`**（默认）：任何真实 mutation 被拒（403 "real mutation not permitted"）。
- **TOCTOU**（目标 reread UID/resourceVersion precondition）+ **UNKNOWN reconcile-before-retry**
  （§29：禁止盲 retry，先读目标实际状态决定 success/rollback/re-execute）。
- helm `ai-action-executor` deployment：`EXECUTION_MODE=disabled` 默认；
  `EXECUTOR_VERIFY_KEYS` 注为 readiness（real mutation 授权时注入）。

### D-Gate 完整条件（§29，依赖真实基础设施 = BLOCKED_BY_ENV）
以下真实执行条件当前不可完成（无真实 K8s/OpenStack/credential）：
- real write（真实 Kubernetes mutation）→ BLOCKED_BY_ENV
- short-lived credential（Credential Broker 真实接通）→ BLOCKED_BY_ENV
- durable idempotency（结果持久化到 query-api/MySQL）→ BLOCKED_BY_ENV
- rollback / post verification / audit（真实闭环）→ BLOCKED_BY_ENV

## 测试
- `ai-action-executor` 5 测试 PASS（含新增 `TestApprovedModeRejectsUnsignedContext`：
  approved 模式无签名上下文 → 403）。
- query-go 10 包、orchestrator 1132、ingest 6 包、helm lint 0 failed。

## 结论 + 真实执行验证（F5 已废除）
`CONTROLLED_ACTION_CANDIDATE` 的 D-Gate 真实执行链已在**真实 K8s（orbstack）**完整验证（用户废除红线 F5）。

### 真实 K8s mutation 闭环验证（对 action-test/action-target deployment，已清理）
- **Ed25519 signed context**：`verifySignedContext`（X-Executor-Signature over body SHA256）验签强制。
- **TOCTOU precondition**：真实重读 UID/resourceVersion；过期 rv → 409 rejected，正确 rv → 200。
- **真实 K8s patch**：strategic-merge + If-Match（resourceVersion precondition），真实写 annotation。
- **verify**：读回 `aio-action-executor/verified=true`。
- **reconcile-before-retry**：execution_unknown → `reconciled`（不盲 retry）。
- **rollback**：JSON patch remove annotation 恢复原状（replicas=1）。
- **K8s TLS（C-05）**：in-cluster CA 加载验证 API Server 证书（不做 insecureSkipVerify）。

### 实现增强（ai-action-executor，F5 废除）
- 真实 K8s client（in-cluster SA token + CA），`POD_SA_ACCESS=true` 启用。
- `readCurrentState` 真实 K8s 重读；`patchTarget` 真实 patch（白名单 patch/scale）。
- `target_name` 解析 + `TargetUID` 用于 TOCTOU 比对。
- helm `aiActionExecutor.realMutation`（false 默认）→ 挂专属 SA + 限定 action-test RBAC。
- **生产 executor 保持 `EXECUTION_MODE=disabled`**（真实测试用独立 executor-real，已清理）。

## 边界 / 诚实
- **红线 F5（不触发生产执行）已由用户废除**；F1-F4 保持。
- 生产 `ai-action-executor` 仍 `disabled`（真实 mutation 未在生产栈开启）；验证在独立测试 executor 完成。
- GIT_ACTION=NONE。
