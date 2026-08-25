# Deployment Validation Closure Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 将《部署验证.md》中的 B1～B8 部署阻断项、两阶段 Fresh Install、最小 mutation 边界和可重复本机验证流程落地到当前代码库。

**Architecture:** Helm 以 `global.imageTag` 作为唯一自研版本来源，bootstrap profile 只启动基础设施并按 hook 顺序完成账号/schema 初始化，runtime profile 再启动 Query API、Worker、Proxy、采集和 Executor。Executor 默认 fail-closed，真实写入只在显式 `approved + realMutation=true` 且 target namespace 白名单命中时启用；验证故障注入只存在于测试 harness，不进入生产默认行为。

**Tech Stack:** Helm 3 templates, Bash deployment scripts, Go 1.23 services/tests, Python pytest workflow gates, Vitest/Vite frontend build, kubectl/OrbStack validation.

**Spec:** `部署验证.md`, `docs/DEPLOYMENT_AND_VERIFY.md`, `docs/runbooks/aiops-action-rollout.md`

## Global Constraints

- Fresh Install 从空 `observability`、`deepflow`、`aiops-canary` namespace、PVC 和 Secret 开始；本轮不实现历史数据迁移或在线升级兼容。
- 所有自研镜像必须使用同一个 `RELEASE_TAG=git-<12位SHA>`，禁止 `latest` 和历史固定 tag。
- Query API 使用 `aiops_app`，schema-migrator 使用 `aiops_migrator`，root 只供 users-init Job 使用。
- `EXECUTION_MODE=disabled`、`realMutation=false` 是默认安全状态；真实 mutation 只允许 `aiops-canary` 下 Deployment 的 `get/patch`。
- Provider key 只能进入 LLM egress proxy；Worker/Orchestrator 只持 proxy token 和 URL。
- 每个行为改动先写会失败的测试/门禁，再写最小实现；不修改用户未跟踪的验收文档。
- 无法由本机证明的多节点、PITR/failover、Credential Broker 和真实生产 provider 条件必须标记 `BLOCKED_BY_ENV`。

---

### Task 1: 建立 Helm/部署合同测试（RED）

**Files:**
- Create: `deploy/scripts/test-deployment-contracts.sh`
- Modify: `deploy/scripts/verify-aiops-workflow-gates.sh`

**Interfaces:**
- Produces: shell-level render assertions reusable by CI and local validation.

- [ ] **Step 1: Write failing render-contract checks**

  Assert that a render using the local validation values rejects empty required secrets, renders every self-built image with one tag, omits Executor resources when disabled, and renders only canary Deployment get/patch RBAC when enabled.

- [ ] **Step 2: Run the contract script and verify it fails on the current chart**

  Run: `bash deploy/scripts/test-deployment-contracts.sh`

  Expected: FAIL because current values contain fixed tags, missing MySQL account keys, unconditional Executor resources, and `action-test` RBAC.

- [ ] **Step 3: Add the contract script to the workflow gate entrypoint**

  The existing gate must call this script after Helm lint/render and preserve a non-zero exit code on any mismatch.

- [ ] **Step 4: Commit the red tests/gates**

  Run: `git add deploy/scripts/test-deployment-contracts.sh deploy/scripts/verify-aiops-workflow-gates.sh && git commit -m "test: add deployment contract gates"`

---

### Task 2: Secret closure and canonical identity generator

**Files:**
- Create: `deploy/scripts/generate-local-secrets.sh`
- Create: `deploy/scripts/secret-format-test.sh`
- Modify: `deploy/helm/aiops/values.yaml`
- Modify: `deploy/helm/aiops/templates/secrets.yaml`
- Modify: `deploy/helm/aiops/templates/_helpers.tpl`
- Modify: `deploy/helm/aiops/templates/query-api/deployment.yaml`
- Modify: `deploy/helm/aiops/templates/ai-orchestrator/deployment.yaml`
- Modify: `deploy/helm/aiops/templates/ai-llm-egress-proxy/deployment.yaml`
- Modify: `deploy/helm/aiops/templates/ai-action-executor/deployment.yaml`

**Interfaces:**
- Produces: `generate-local-secrets.sh` writes a shell-sourceable env file with independent 64-byte Ed25519 private keys encoded as base64url without padding and matching public keys.
- Consumes: Go `auth.DecodePrivateKey` and Python service identity parsers.

- [ ] **Step 1: Add failing key-format and Helm secret tests**

  Verify three keypairs are different, private keys decode to `ed25519.PrivateKeySize`, public keys verify a cross-language fixture, MySQL app/migrator passwords are present and distinct, and missing/`CHANGE_ME` values fail Helm rendering.

- [ ] **Step 2: Run the tests to confirm the current format fails**

  Run: `bash deploy/scripts/secret-format-test.sh` and the Task 1 contract script.

- [ ] **Step 3: Implement secret values and fail-closed rendering**

  Add `mysqlAppPassword`, `mysqlMigratorPassword`, service tokens, provider keys, and Stage D signing values. Require the three MySQL passwords and all enabled service identities when their component is enabled. Update comments from 32-byte raw keys to 64-byte Ed25519 private key + raw URL base64 contract.

- [ ] **Step 4: Implement deterministic local secret generation**

  Generate independent keypairs with `openssl`/a small Go helper, emit only the env file path requested by the caller, never log provider keys, and support `--output` plus `--force` refusal for existing files.

- [ ] **Step 5: Re-run secret tests and Helm renders**

  Run: `bash deploy/scripts/secret-format-test.sh` and `bash deploy/scripts/test-deployment-contracts.sh`.

- [ ] **Step 6: Commit**

  Run: `git add deploy/scripts deploy/helm/aiops && git commit -m "feat: close deployment secret and identity contract"`

---

### Task 3: Image build and version convergence

**Files:**
- Create: `ai-action-executor/Dockerfile`
- Create: `ai-llm-egress-proxy/Dockerfile`
- Modify: `deploy/scripts/build-images.sh`
- Modify: `deploy/scripts/version.sh`
- Modify: `deploy/helm/aiops/values.yaml`
- Modify: `deploy/helm/aiops/templates/mysql/init-job.yaml`
- Modify: service Deployment templates that currently embed fixed image tags.

**Interfaces:**
- Produces: `IMAGE_TAG=git-<SHA>` builds frontend, query-api, ingest, orchestrator, worker, event-collector, executor, proxy, schema-migrator and ipmi images; Helm references `.Values.global.imageTag` for all self-built images.

- [ ] **Step 1: Add a failing build inventory test**

  Add a dry-run mode to `build-images.sh` and test that `all` expands to the complete nine-image inventory and that every rendered self-built image contains the requested tag.

- [ ] **Step 2: Run the inventory test against the current script**

  Run: `IMAGE_TAG=git-test BUILD_IMAGES_DRY_RUN=1 ./deploy/scripts/build-images.sh all`.

  Expected: FAIL because executor, proxy and schema-migrator are missing.

- [ ] **Step 3: Add minimal Dockerfiles**

  Build static Go binaries for executor/proxy in a pinned Go builder image and run them from a CA-enabled non-root Alpine runtime. Use the existing schema-migrator Dockerfile unchanged except for tag convergence.

- [ ] **Step 4: Extend the image inventory and tag resolution**

  Make `RELEASE_TAG`/`IMAGE_TAG` explicit, default to `git-<12 SHA>` for clean trees, reject `latest` and historical tags in validation mode, and include all nine services in `all`.

- [ ] **Step 5: Re-run dry-run and Helm image tests**

  Run: `IMAGE_TAG=git-test BUILD_IMAGES_DRY_RUN=1 ./deploy/scripts/build-images.sh all` and `helm template ... --set global.imageTag=git-test`.

- [ ] **Step 6: Commit**

  Run: `git add ai-action-executor/Dockerfile ai-llm-egress-proxy/Dockerfile deploy/scripts deploy/helm/aiops && git commit -m "build: converge all deployment images on release tag"`

---

### Task 4: Helm profiles, conditional resources and two-stage bootstrap

**Files:**
- Create: `deploy/helm/aiops/values-local-bootstrap.yaml`
- Create: `deploy/helm/aiops/values-local-validation.yaml`
- Modify: `deploy/helm/aiops/templates/ai-action-executor/deployment.yaml`
- Modify: `deploy/helm/aiops/templates/ai-action-executor/rbac.yaml` (or split from deployment template)
- Modify: `deploy/helm/aiops/templates/ai-orchestrator/rbac.yaml`
- Modify: `deploy/helm/aiops/templates/mysql/users-init-job.yaml`
- Modify: `deploy/helm/aiops/templates/mysql/init-job.yaml`
- Modify: `deploy/helm/aiops/templates/networkpolicy.yaml`

**Interfaces:**
- Produces: bootstrap profile with only stateful dependencies and migration hooks; validation profile with canonical Worker (2 replicas), Proxy and disabled Executor; approved overlay with target namespace allowlist.

- [ ] **Step 1: Add failing profile/render tests**

  Assert bootstrap disables all runtime Deployments, validation enables Worker/Proxy and legacy switches are zero, disabled Executor renders no Deployment/Service/SA/RBAC, and approved Executor renders only canary Role/RoleBinding.

- [ ] **Step 2: Implement profile files and global image references**

  Keep local validation independent of `values-prod.yaml`; set `k8sInsecureSkipVerify=true` only in the explicit local profile, keep production false, and set canonical runtime switches exactly as specified.

- [ ] **Step 3: Make Executor resources conditional and namespace-scoped**

  Wrap every Executor resource in `if .Values.aiActionExecutor.enabled`; add `targetNamespaces`; generate one Role/RoleBinding per target namespace with only `apps/deployments get,patch`, no delete or cluster-wide writes.

- [ ] **Step 4: Make Worker/Orchestrator/Proxy identities and NetworkPolicy match the profiles**

  Ensure Worker and Orchestrator use the same image, proxy is the only provider-key holder, and egress rules are rendered only when the component is enabled.

- [ ] **Step 5: Run profile tests**

  Run: `bash deploy/scripts/test-deployment-contracts.sh` and `./deploy/scripts/verify-aiops-workflow-gates.sh`.

- [ ] **Step 6: Commit**

  Run: `git add deploy/helm/aiops && git commit -m "feat: add canonical bootstrap and validation profiles"`

---

### Task 5: Deployment entrypoint and fresh-install verification

**Files:**
- Create: `deploy/scripts/local-validation.sh`
- Modify: `deploy/scripts/apply.sh`
- Modify: `deploy/scripts/build-images.sh`
- Modify: `docs/DEPLOYMENT_AND_VERIFY.md`

**Interfaces:**
- Produces: a local command that performs cleanup (only with explicit `--destroy`), generates/loads secrets, builds images, renders/lints, installs bootstrap, waits users-init and schema-migrator, upgrades runtime, then optionally deploys DeepFlow.

- [ ] **Step 1: Add failing shell integration checks**

  Test dry-run output order: generate → build → lint/render → bootstrap → users-init → schema-migrator → runtime; require explicit confirmation for namespace/PVC deletion and refuse to delete production-looking namespaces.

- [ ] **Step 2: Implement two-phase local validation entrypoint**

  Use `RELEASE_SHA`/`RELEASE_TAG`, a caller-provided secret env file, `helm upgrade --install` bootstrap values, explicit Job waits, then runtime upgrade values. Keep DeepFlow opt-in and report `BLOCKED_BY_ENV` if not Ready.

- [ ] **Step 3: Make `apply.sh` delegate safely**

  Preserve existing upgrade behavior for non-destructive deployments, but route a fresh local validation request to the two-phase path and pass all new Secret keys.

- [ ] **Step 4: Run shell dry-run and render checks**

  Run: `deploy/scripts/local-validation.sh --dry-run` and `./deploy/scripts/verify-aiops-workflow-gates.sh`.

- [ ] **Step 5: Commit**

  Run: `git add deploy/scripts docs/DEPLOYMENT_AND_VERIFY.md && git commit -m "feat: add two-phase local deployment validation"`

---

### Task 6: Deterministic Stage D fault-injection tests

**Files:**
- Modify: `ai-action-executor/main.go`
- Modify: `ai-action-executor/main_test.go`
- Create: `ai-action-executor/fault_injection_test.go`
- Modify: `ai-apm-query-go/internal/api/action_executor_client.go`
- Modify: `ai-apm-query-go/internal/api/action_dispatch_test.go`

**Interfaces:**
- Produces: test-only dispatch gate and response-loss-after-apply seams bound to an explicit Action ID and disabled unless a local validation flag is set; production defaults unchanged.

- [ ] **Step 1: Add failing tests for gate and response loss**

  Prove an approved request waits at the gate until released; prove a patch can be applied once while the HTTP response is dropped; prove Query API records unknown and calls reconcile rather than re-dispatching.

- [ ] **Step 2: Run the focused tests and verify red**

  Run: `go test ./... -run 'Fault|ResponseLoss|DispatchGate'` in executor and query-api packages.

- [ ] **Step 3: Implement test-only seams**

  Add environment-gated hooks (`LOCAL_VALIDATION_FAULT_ACTION_ID`, `LOCAL_VALIDATION_DISPATCH_GATE`, `LOCAL_VALIDATION_DROP_RESPONSE_AFTER_APPLY`) that cannot activate when `EXECUTION_MODE=disabled` and are ignored unless the test harness explicitly enables them.

- [ ] **Step 4: Run focused and package tests**

  Run: `go test ./...` in both Go modules.

- [ ] **Step 5: Commit**

  Run: `git add ai-action-executor ai-apm-query-go/internal/api && git commit -m "test: make Stage D fault validation deterministic"`

---

### Task 7: Full local verification and evidence capture

**Files:**
- Create: `deploy/scripts/validate-local-stack.sh`
- Modify: `deploy/scripts/verify-aiops-workflow-gates.sh`
- Modify: `docs/runbooks/aiops-action-rollout.md`
- Modify: `部署验证.md`

**Interfaces:**
- Produces: a read-only validator for health, image/tag consistency, schema version, RBAC, disabled-stage safety and canary mutation evidence; it never enables production mutation.

- [ ] **Step 1: Add failing checks for each mandatory matrix row**

  Check pod readiness, `/readyz`, schema migrations 0001–0009, app/migrator grants, real data markers, LLM proxy readiness, canonical worker switches, RBAC, and absence of Executor resources in disabled mode.

- [ ] **Step 2: Implement the validator with explicit environment skips**

  Use `BLOCKED_BY_ENV` for unavailable provider, DeepFlow, multi-node, PITR/failover or Credential Broker conditions; never mark skipped checks as passed.

- [ ] **Step 3: Run all repository gates and the validator**

  Run: `make test-workflow-all`, `./deploy/scripts/verify-aiops-workflow-gates.sh`, `bash deploy/scripts/test-deployment-contracts.sh`, `bash deploy/scripts/validate-local-stack.sh --offline`.

- [ ] **Step 4: Update the evidence docs with exact SHA and outputs**

  Record only observed results, distinguish local canary mutation from production readiness, and retain the three allowed conclusion labels.

- [ ] **Step 5: Commit**

  Run: `git add deploy/scripts docs/runbooks/aiops-action-rollout.md 部署验证.md && git commit -m "docs: record complete deployment validation evidence"`

---

### Task 8: Integrated review and release verification

**Files:**
- Modify only files already listed above if fixes are required.

- [ ] **Step 1: Re-read this plan and the source document line by line**
- [ ] **Step 2: Run the complete test/build/render suite on the merged tree**

  Run: `make test-workflow-all`, `./deploy/scripts/verify-aiops-workflow-gates.sh`, `bash deploy/scripts/test-deployment-contracts.sh`, `helm lint deploy/helm/aiops`, and `git diff --check`.

- [ ] **Step 3: Verify current branch, commit and untracked user files**

  Do not stage the user-owned untracked Chinese acceptance documents.

- [ ] **Step 4: Commit any final fixes and report evidence**

  Only claim completion for checks whose command output is fresh and successful; report all `BLOCKED_BY_ENV` items explicitly.

