# K8s Canonical Action Migration Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 将 K8s 运维页的七类动作完整迁移到 query-api Canonical Action proposal/approval/execute 链路，并在执行器保持 disabled 时完成真实、可审计的拒绝验收。

**Architecture:** 浏览器只读资源列表并向 query-api 创建 Canonical Action proposal；query-api 经既有 Kubernetes Access Boundary 读取目标 UID/RV，在单事务内创建 awaiting-approval Run 与 proposed Action；approval/execute 继续使用统一 Action API 和 durable outbox。执行器扩展七类动作的受控语义，但部署配置继续 `EXECUTION_MODE=disabled`，真实 mutation 不开启。

**Tech Stack:** Go、net/http、MySQL、Kubernetes Access Boundary、React 18、TypeScript、Vitest、Docker/Helm、kubectl。

**Spec:** `docs/superpowers/specs/2026-08-27-k8s-canonical-action-migration-design.md`

## Global Constraints

- 不修改 DeepFlow 源代码，不让 AIOps runtime 读取或修改 DeepFlow ClickHouse。
- DeepFlow Agent 熔断器保持当前临时关闭配置，除非用户明确要求，不得开启。
- `ai-action-executor` 保持 `EXECUTION_MODE=disabled`、`POD_SA_ACCESS=false`、`realMutation=false`。
- K8s 凭据和客户端只能通过 `k8sboundary.ClusterClientManager`；handler、orchestrator、executor 不加载 kubeconfig。
- 所有 action 的 UID、resourceVersion、params、operation 和 policy version 必须进入 canonical hash。
- 不触碰未跟踪的 `AIOps前端全功能及真实性验收测试方案_细化排查版_最终版.md` 与 `部署验证.md`。
- 修复必须先有失败测试；每个故障修复后部署并真实回归；按用户约定累计五个故障后再同步 GitHub。

---

### Task 1: 固化 Canonical Action 公共 proposal 契约

**Files:**
- Create: `ai-apm-query-go/internal/api/action_proposal.go`
- Modify: `ai-apm-query-go/cmd/api/main.go:374-376`
- Test: `ai-apm-query-go/internal/api/action_proposal_test.go`

**Interfaces:**
- Consumes: `requestAuthorizationContext`, `store.AIRunDAO`, `store.AIActionDAO`, `ActionPreflightService`。
- Produces: `POST /api/v1/ai/actions`；`createActionProposalRequest`；`ActionProposalHandler`。

- [ ] **Step 1: Write the failing tests**

测试覆盖：未登录返回 403；缺少 cluster/target/operation 返回 422；proposal 使用
预检返回的 UID/RV/hash 创建 `awaiting_approval` Run + `proposed` Action；同一
idempotency key 重放同一 action；不同 hash 返回 409；proposal 不写 run outbox。

- [ ] **Step 2: Run the focused tests to verify RED**

Run: `cd ai-apm-query-go && go test ./internal/api -run 'TestActionProposal' -count=1`

Expected: FAIL because `ActionProposalHandler` and public POST routing do not exist.

- [ ] **Step 3: Add the minimal handler and route**

实现严格 JSON 解码、tenant/cluster ownership 校验、`PreflightInput` 调用和
统一 JSON 响应；动作风险由服务端映射。先把 handler 接到明确的 proposal 持久化
接口，避免直接写 SQL；该接口在 Task 3 由 Run + Action 原子事务实现。

- [ ] **Step 4: Run the focused tests to verify the contract**

Run: `cd ai-apm-query-go && go test ./internal/api -run 'TestActionProposal' -count=1`

Expected: handler validation and response contract tests pass; persistence tests remain
skipped until Task 2 seam is connected.

- [ ] **Step 5: Commit locally**

```bash
git add ai-apm-query-go/internal/api/action_proposal.go ai-apm-query-go/internal/api/action_proposal_test.go ai-apm-query-go/cmd/api/main.go
git commit -m "feat: add canonical k8s action proposal endpoint"
```

### Task 2: 扩展 K8s Access Boundary 的目标身份读取

**Files:**
- Modify: `ai-apm-query-go/internal/k8sboundary/k8sboundary.go`
- Modify: `ai-apm-query-go/internal/query/kubernetes.go`
- Modify: `ai-apm-query-go/internal/query/kubernetes_test.go`
- Modify: `ai-apm-query-go/internal/api/action_preflight.go`
- Modify: `ai-apm-query-go/internal/api/action_preflight_test.go`

**Interfaces:**
- Consumes: 既有 `KubeClient` 与 `KubernetesRepository`。
- Produces: `GetWorkloadIdentity(resourceType, namespace, name)`、`GetPodIdentity`、
  `GetNodeIdentity`；七类 action 的统一参数校验与 hash 输入。

- [ ] **Step 1: Write failing identity and matrix tests**

为 deployment/statefulset/daemonset/pod/node 各增加 fake identity；为七个 operation
增加允许/拒绝测试；测试 scale、grace period、drain timeout 的边界值；测试 UID/RV
缺失和非法 resource type 失败。

- [ ] **Step 2: Run RED**

Run: `cd ai-apm-query-go && go test ./internal/query ./internal/api -run '(KubeRepo|ActionPreflight)' -count=1`

Expected: FAIL because the fake/client interfaces only expose Deployment identity and
preflight only accepts deployment patch/scale.

- [ ] **Step 3: Implement boundary methods and preflight matrix**

边界中的 kubectl read 只返回 metadata UID/RV/namespace/name；repository 按 kind
选择边界方法并把错误映射为已有 QueryError。preflight 为每种 operation 生成固定
结构化 params/target spec，严格限制数值和 annotation；hash 使用实际 resource type。

- [ ] **Step 4: Run focused tests**

Run: `cd ai-apm-query-go && go test ./internal/query ./internal/api -run '(KubeRepo|ActionPreflight)' -count=1`

Expected: all identity, validation, and hash tests pass.

- [ ] **Step 5: Commit locally**

```bash
git add ai-apm-query-go/internal/k8sboundary/k8sboundary.go ai-apm-query-go/internal/query/kubernetes.go ai-apm-query-go/internal/query/kubernetes_test.go ai-apm-query-go/internal/api/action_preflight.go ai-apm-query-go/internal/api/action_preflight_test.go
git commit -m "feat: support canonical k8s action target identities"
```

### Task 3: 增加 Run + Action 原子持久化

**Files:**
- Modify: `ai-apm-query-go/internal/store/ai_runs.go`
- Modify: `ai-apm-query-go/internal/store/ai_actions.go`
- Create: `ai-apm-query-go/internal/store/ai_manual_actions.go`
- Test: `ai-apm-query-go/internal/store/ai_manual_actions_test.go`
- Modify: `ai-apm-query-go/internal/api/action_proposal.go`

**Interfaces:**
- Consumes: validated `ActionPreflightResult` and current authenticated principal。
- Produces: `CreateManualAction(ctx, AIRun, AIAction) (created bool, err error)` with
  same-transaction Run/Action insert, idempotent replay, and no Run outbox.

- [ ] **Step 1: Write the failing sqlmock tests**

测试事务顺序：BEGIN → insert `ai_runs` status awaiting_approval/action_mode manual
→ insert `ai_actions` proposed/dry_run 0 → COMMIT；action insert failure causes
ROLLBACK；duplicate request reloads existing Run/Action；different hash conflicts。

- [ ] **Step 2: Run RED**

Run: `cd ai-apm-query-go && go test ./internal/store -run TestCreateManualAction -count=1`

Expected: FAIL because `CreateManualAction` is undefined.

- [ ] **Step 3: Implement transaction and idempotency**

在一个事务内插入两张表，使用 tenant/request idempotency lookup；不调用
`CreateWithOutbox`。对 duplicate key 读取既有 Run/Action 并比较 canonical hash，
不一致返回 `ErrIdempotencyPayloadMismatch`。

- [ ] **Step 4: Run tests**

Run: `cd ai-apm-query-go && go test ./internal/store -run TestCreateManualAction -count=1`

Expected: PASS，且 rollback/重放测试均通过。

- [ ] **Step 5: Connect handler and run API tests**

Run: `cd ai-apm-query-go && go test ./internal/api ./internal/store -run '(ActionProposal|CreateManualAction)' -count=1`

Expected: proposal 返回稳定 action/run projection，错误码按 409/422/503 映射。

- [ ] **Step 6: Commit locally**

```bash
git add ai-apm-query-go/internal/store/ai_runs.go ai-apm-query-go/internal/store/ai_actions.go ai-apm-query-go/internal/store/ai_manual_actions.go ai-apm-query-go/internal/store/ai_manual_actions_test.go ai-apm-query-go/internal/api/action_proposal.go
git commit -m "feat: persist manual canonical actions atomically"
```

### Task 4: 对齐审批 hash 与 action 资源类型

**Files:**
- Modify: `ai-apm-query-go/internal/api/action_decision.go`
- Modify: `ai-apm-query-go/internal/api/action_decision_test.go`
- Modify: `ai-apm-query-go/internal/api/action_control.go`

**Interfaces:**
- Consumes: Task 3 canonical Action rows。
- Produces: decision 对任意支持的 K8s resource type 校验相同 hash，保持审批版本和
  tenant/run CAS；GET action 返回完整 canonical 字段。

- [ ] **Step 1: Add failing tests**

为 pod/node/workload action 增加审批通过测试；篡改 resource type、operation、
params、UID/RV 的 hash mismatch 测试；同提议人审批、错误 tenant、版本冲突测试。

- [ ] **Step 2: Run RED**

Run: `cd ai-apm-query-go && go test ./internal/api -run 'TestActionDecision' -count=1`

Expected: non-deployment action fails because decision currently hashes resource type as
deployment.

- [ ] **Step 3: Implement canonical resource-type lookup**

从 action row 的 `target_resource_type` 进入 `CanonicalActionPayloadV2`；拒绝空值、
未知值、hash mismatch；不改变 approved 才写 action outbox 的状态机。

- [ ] **Step 4: Run tests**

Run: `cd ai-apm-query-go && go test ./internal/api -run 'TestActionDecision' -count=1`

Expected: all decision tests pass。

- [ ] **Step 5: Commit locally**

```bash
git add ai-apm-query-go/internal/api/action_decision.go ai-apm-query-go/internal/api/action_decision_test.go ai-apm-query-go/internal/api/action_control.go
git commit -m "fix: validate canonical action resource types on approval"
```

### Task 5: 扩展 executor 的受控动作语义并保持 disabled 门禁

**Files:**
- Modify: `ai-action-executor/main.go`
- Modify: `ai-action-executor/main_test.go`
- Modify: `ai-action-executor/fault_injection_test.go`

**Interfaces:**
- Consumes: signed `ActionExecutionContext` with resource type encoded in the target
  spec/context contract。
- Produces: operation/resource allowlist, TOCTOU reads, fixed K8s API path and reconcile
  predicates for seven operations；`ModeDisabled` 仍在任何 K8s read/write 前拒绝。

- [ ] **Step 1: Write failing unit tests**

测试 `buildPatchPayload`/operation builder 对 restart/scale/cordon/uncordon；pod delete
和 eviction 的请求形态；node drain 顺序/参数；未知 operation/resource type 拒绝；
`handleExecute` 在 disabled 时返回 403 且 read/patch seam 均未调用。

- [ ] **Step 2: Run RED**

Run: `cd ai-action-executor && go test ./... -run '(PatchPayload|Execute|Reconcile|Disabled)' -count=1`

Expected: new operation tests fail as only patch/scale are supported。

- [ ] **Step 3: Implement fixed operation dispatch**

将资源类型显式加入执行上下文或 target spec；统一先做 mode gate，再做签名/目标
身份校验。只允许固定 API URL、固定 verb、固定参数；不接受任意 URL、JSON Patch、
客户端 credential_ref。disabled/manual 路径不能调用 mutation client。

- [ ] **Step 4: Run executor tests**

Run: `cd ai-action-executor && go test ./...`

Expected: all existing tests and seven-operation tests pass，且 disabled guard test
证明没有调用写 seam。

- [ ] **Step 5: Commit locally**

```bash
git add ai-action-executor/main.go ai-action-executor/main_test.go ai-action-executor/fault_injection_test.go
git commit -m "feat: model all canonical k8s action operations"
```

### Task 6: 迁移前端 K8s 运维页到 Canonical Action API

**Files:**
- Modify: `observability-frontend/src/api/k8s.ts`
- Modify: `observability-frontend/src/pages/infra/K8sActions.tsx`
- Create: `observability-frontend/src/pages/infra/K8sActions.test.tsx`
- Modify: `observability-frontend/src/pages/admin/Approvals.tsx` if it renders legacy task ids

**Interfaces:**
- Consumes: Task 1 proposal response and existing decision/execute APIs。
- Produces: UI 预检→proposal→canonical approval/execute status flow；旧 `/ops/k8s/*`
  client functions no longer used。

- [ ] **Step 1: Write failing frontend tests**

mock API and render page；验证选择 deployment scale 提交 `/ai/actions`，请求包含
canonical cluster/resource/params；七动作均可选择；响应展示 action id/hash/UID/RV；
execute 只使用 action id；旧 `k8sPreflight`/`k8sExecute` 不被调用；403 rejected
显示“执行器已禁用，未发生真实变更”。

- [ ] **Step 2: Run RED**

Run: `cd observability-frontend && npm run test:run -- src/pages/infra/K8sActions.test.tsx`

Expected: FAIL because current page calls `/ops/k8s/preflight` and requires legacy
`approval_task_id`。

- [ ] **Step 3: Implement API client and UI state**

用 `createK8sActionProposal` 替换预检；保存 action projection；移除 approval task
列表/字段；execute 调用 `/ai/actions/{id}/execute`，将 rejected/403 映射成真实
未变更提示；保持资源列表只读和七种动作表单。

- [ ] **Step 4: Run frontend tests and build**

Run: `cd observability-frontend && npm run test:run -- src/pages/infra/K8sActions.test.tsx && npm run build`

Expected: tests PASS and TypeScript/Vite build PASS。

- [ ] **Step 5: Commit locally**

```bash
git add observability-frontend/src/api/k8s.ts observability-frontend/src/pages/infra/K8sActions.tsx observability-frontend/src/pages/infra/K8sActions.test.tsx observability-frontend/src/pages/admin/Approvals.tsx
git commit -m "feat: migrate k8s actions page to canonical action api"
```

### Task 7: 全链路部署与真实回归

**Files:**
- Modify only deployment values/manifests required by the implementation。
- Do not modify: `AIOps前端全功能及真实性验收测试方案_细化排查版_最终版.md`, `部署验证.md`。

**Interfaces:**
- Consumes: all tested query-api/frontend/executor artifacts。
- Produces: deployed revisions, HTTP evidence, target object unchanged evidence, and
  acceptance notes for the current fault batch。

- [ ] **Step 1: Run full local tests**

Run:

```bash
cd ai-apm-query-go && go test ./...
cd ../ai-action-executor && go test ./...
cd ../ai-orchestrator && pytest -q
cd ../observability-frontend && npm run test:run && npm run build
```

Expected: all suites PASS。

- [ ] **Step 2: Build and deploy query-api/frontend/executor**

使用项目现有本机构建/加载脚本，部署新镜像；Helm values 必须保留
`EXECUTION_MODE=disabled`、`POD_SA_ACCESS=false`，不改变 DeepFlow breaker 配置。

- [ ] **Step 3: Verify deployment invariants**

Run read-only `kubectl` checks for image revisions, pod readiness, env mode, DeepFlow
breaker config and Helm release status；失败则停止，不做真实 mutation。

- [ ] **Step 4: Real proposal verification**

使用 `admin/admin1234` 登录取得会话后，针对现有 canary Deployment 创建一个唯一
proposal；验证 201/200、GET action、run status awaiting_approval、action status
proposed、tenant isolation、无 run outbox；不审批、不执行。

- [ ] **Step 5: Real disabled execution verification**

只对测试 action 调用 execute 前先确认 action 未批准时为 422；不进行真实批准点击。
通过单元/集成测试验证 approved + executor disabled 返回 rejected；线上只读检查
目标 Deployment 的 replicas/resourceVersion 未因验收改变。

- [ ] **Step 6: Record evidence without secrets**

追加非敏感 HTTP 状态、action/run 状态、镜像 tag、Pod 日志摘要和测试命令到受控记录
文件；不碰两个用户未跟踪文档。当前五故障批次规则未满足时不 push GitHub。

- [ ] **Step 7: Commit locally and report batch count**

```bash
git status --short
git log -1 --oneline
git diff origin/main --stat
```

如果本轮累计达到五个新故障，才执行 fetch、push，并核对 local/cluster/origin
三方 commit；否则保留本地 commit，明确 GitHub 尚未同步。
