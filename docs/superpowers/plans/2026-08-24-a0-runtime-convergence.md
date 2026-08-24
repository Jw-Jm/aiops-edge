# A0 批次实施计划：生产收敛（先修真实缺陷）

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在现有 Run/Control Plane 主线上修复报告 F-01/F-02/F-09/F-11/F-18/F-19 六类真实代码缺陷（Cancel CAS、control-command 幂等、remote fallback、outbox stale-claim、recovery 全局 scope、K8s silent-empty），不改架构。

**Architecture:** 复用现有 `ai_runs`/`ai_control_commands`/`ai_run_outbox`/`/internal/v1/control-plane/*`/`/internal/v1/query/*`。A0 批次一次性创建完整 `0004_runtime_convergence.sql`（含报告 27.4 全部列，checksum 冻结），但 A0 仅使用其中 control_commands 相关列；lease/commit/replay/tool 字段留待 A1/B1 使用。

**Tech Stack:** Go (ai-apm-query-go, sqlmock 单测)、Python (ai-orchestrator, pytest)。

## Global Constraints

- GIT_ACTION=NONE（本轮不 commit、不 push）。
- 不新建平行 Run Store/State Machine/Replay Guard/Tool API/Action authority。
- 不改已应用的 0001~0003b；0004 一次成型不改内容。
- migration 0004 必须 additive/backward-compatible（新二进制要求 0004，旧二进制只要求 0001~0003b 仍可运行）。
- 真实执行 NOT APPROVED；不触真实 K8s/OpenStack/Shell mutation。
- `runTransitions` 状态机与 orchestrator `RunStateMachine` 保持一致。

---

### Task A0-01: Cancel + control-command 幂等事务收敛

**Files:**
- Create: `ai-apm-query-go/internal/store/migrations/versions/0004_runtime_convergence.sql`
- Modify: `ai-apm-query-go/internal/store/ai_control_commands.go`（AIControlCommand 加 PayloadHash/ResponseJSON/CompletedAt；新增 tx 事务化 helper）
- Modify: `ai-apm-query-go/internal/store/ai_runs.go`（新增 `TransitionTx`/`CancelTx` 事务版 + GetTx 复用）
- Modify: `ai-apm-query-go/internal/api/control_plane_runs.go`（Cancel 用 `*int64` expected_version；transition/cancel 统一事务化 helper `ApplyRunControlCommandTx`；payload hash 语义）
- Modify: `ai-orchestrator/control_plane_client.py`（cancel 传 expected_version + command_id）
- Modify: `ai-orchestrator/persistent_run_repository.py`（cancel 调用传参）
- Test: `ai-apm-query-go/internal/api/control_plane_runs_test.go`（新增 cancel/transition 幂等测试）
- Test: `ai-orchestrator/tests/test_b6_persistent_repo.py`（新增 cancel 传参测试）

**Interfaces:**
- Consumes: 现有 `AIRunDAO.Transition/Cancel/Get`、`AIControlCommandDAO.Create/MarkDone/Get`、`recordControlCommand`。
- Produces: `AIControlCommand` 扩展字段（PayloadHash/ResponseJSON/CompletedAt）；事务化 `ApplyRunControlCommandTx(commandID, operation, payloadHash, expectedVersion, mutateFn)`。

- [ ] **Step 1: 写完整 0004 migration**（含报告 27.4 全部内容）
- [ ] **Step 2: 扩展 ai_control_commands.go**（字段 + Tx helper）
- [ ] **Step 3: 扩展 ai_runs.go**（TransitionTx/CancelTx）
- [ ] **Step 4: 重构 control_plane_runs.go**（cancel *int64 + 统一事务 helper + payload hash）
- [ ] **Step 5: 修 orchestrator cancel 客户端传参**
- [ ] **Step 6: 补测试并运行验证**

### Task A0-02: Production remote read fail-closed

**Files:**
- Modify: `ai-orchestrator/persistent_run_state_store.py`（get() 移除 fallback；tenant 显式传入；`_fallback_tenant` UUID(0) 猜测删除）
- Test: `ai-orchestrator/tests/test_persistent_run_state_store.py`

### Task A0-03: RunInvocation outbox stale-claim + dispatch fencing

**Files:**
- Modify: `ai-apm-query-go/internal/store/ai_run_outbox.go`（Claim 允许 stale claimed + dispatch_epoch++ + owner/token/expiry；Deliver/Retry 匹配 owner+epoch+token）
- Modify: `ai-apm-query-go/internal/api/run_dispatch.go`（传 dispatch 字段；DB-time）
- Test: 新增 outbox reclaim 测试

### Task A0-04: Browser canonical route/scope convergence

**Files:**
- Modify: `ai-apm-query-go/internal/api/auth.go`（isCanonicalProtectedRoute 精确路由补 scope）
- Test: route contract 测试（403 vs 200/no_data）

### Task A0-05: Recovery global-scope + K8s silent-empty

**Files:**
- Modify: `ai-apm-query-go/internal/api/control_plane_recovery.go`（unfinished 要求 `control_plane.runs.recover.global` system identity + 分页）
- Modify: `ai-apm-query-go/internal/api/internal_query.go`（K8s 子查询错误不吞成 200 空数组，改 partial/failed）
- Test: 新增 global-scope + K8s partial/failed 测试

---

## Implementation Status (2026-08-24)

**A0 批次全部完成并验证**（GIT_ACTION=NONE）：

- [x] **A0-01 Cancel + control-command 幂等事务收敛**：新增 `0004_runtime_convergence.sql`（一次成型含 A1 全部列）；`AIControlCommandDAO` 加 PayloadHash/ResponseJSON/CompletedAt + NULL 安全 scan；新增 `ApplyRunControlCommandTx`（command 幂等重放/hash mismatch + Run CAS 同事务）+ `UpsertDoneTx`；`AIRunDAO` 加 `TransitionTx`/`CancelTx`；cancel/transition handler 用 `*int64` expected_version（缺省 400 fail-closed，不再"读当前 version 当 caller expected"）+ payload hash 语义化幂等；orchestrator `cancel()` 端到端传 expected_version + command_id。测试：store `ai_control_commands_test.go`（4）+ api cancel/transition/missing-version/CAS-conflict + orchestrator cancel 传参。
- [x] **A0-02 Production remote read fail-closed**：`PersistentRunStateStore.get()` 远端 refresh 失败不再 `return self._fallback.get(run_id)`；`_tenant_for` 显式解析 tenant，无法确定 → `RUN_TENANT_UNKNOWN` fail-closed（删除 UUID(0) 猜测）。测试：cache-hit / cache-miss remote-error fail-closed / unknown-tenant / with-tenant success。
- [x] **A0-03 RunInvocation outbox dispatch fencing**：`ai_run_outbox.go` 加 dispatch_owner_id/epoch/token_hash/expires_at（DB-time 判定 stale claimed 回收），`Claim` 原子抢占 pending/stale + 返回 fence，`Deliver`/`Retry` 带 fence（owner+epoch+token 匹配防旧 owner 误交付）；`run_dispatch.go` dispatchOne 用 fence。测试：outbox InsertAndClaim/Deliver/Retry/ScanPending + dispatch Delivers/Retries。
- [x] **A0-04 Browser canonical route/scope convergence**：query-api 关闭 `/metrics/query` 任意 PromQL passthrough（`METRICS_PROMQL_PASSTHROUGH_DISABLED`）与 `/metrics/query_range`（`METRICS_PROMQL_RANGE_DISABLED`）；`QueryMetrics` 改 canonical tenant + concrete cluster（拒 `all`/缺省）；allowlist 补 `/api/v1/ai/runs/{runID}` 单段详情（GetRunPublic 已有 ownership）+ `/api/v1/metrics/query`；前端 AiChat 移除默认 `cluster_id=all`，无 concrete cluster 禁用发送。测试：metrics passthrough 关闭/rejects-all-cluster + allowlist contract。
- [x] **A0-05 Recovery global-scope + K8s silent-empty**：unfinished 扫描要求 `control_plane.runs.recover.global`（独立 system capability，与单 Run recover 分离）+ `ScanUnfinishedLimit` 分页；`InternalQueryKubernetes` K8s 子查询错误不吞 200 空数组——部分失败 → partial+errors，全部失败 → 503 unavailable。测试：unfinished capability 403/200 + K8s partial/all-fail。

验证：query-go `go test ./...` 10 包全 PASS + `go vet` 通过；orchestrator 81 passed（test_b6/persistent/p10/p11/p13）；前端 `npx tsc --noEmit` exit 0 + `vite build` 成功；红线 grep execute/credential/kubeconfig 0 match；orchestrator py 语法 AST 解析通过。

## Real-Environment Verification (2026-08-24, orbstack acceptance)

用户授权"本机有真实环境可以验证，严格按照文档步骤来"。真实环境 = orbstack context observability ns 全栈 Running。

**1) 0004 migration 应用到生产 MySQL（报告 18.3 顺序）**
- 构建 schema-migrator 镜像 `schema-migrator:a0-runtime-20260824`（本机交叉编译 Linux/arm64 含 0004 embed，基于现有运行镜像 COPY 二进制，避免拉取 golang base）
- 一次性 Job `migrate-a0-runtime` 运行 `migrations.Run` → `schema-migrator: migrations applied successfully`
- 验证：`aiops_schema_migrations` 新增 `mysql/0004_runtime_convergence`；`ai_control_commands` 有 `payload_hash/response_json/completed_at`；`ai_run_outbox` 有 `dispatch_owner_id/dispatch_epoch/dispatch_token_hash/dispatch_expires_at/delivered_at`
- Job 已删除

**2) 部署新 query-api `query-api:a0-runtime-20260824`（digest sha256:77c41426...）**
- 交叉编译含 A0 改动的 Linux/arm64 二进制，基于 `query-api:v1.2.0-p20-24b157a0` COPY /api
- `kubectl set image` 滚动部署 → rollout 成功，pod Ready 1/1，启动日志无 schema fail-closed，RequireCurrent 通过（0004 已应用）

**3) A0-01 control-command 幂等（签名 internal 请求，TrustedRequestContext V2 system principal）**
- transition 缺 `expected_version` → **400 MISSING_EXPECTED_VERSION**（不再读当前 version 当 caller expected）
- cancel 带 `expected_version=0 + command_id` → **200**，真实 CAS 迁移 run `8bf2befd` created→cancelled（state_version 0→1）
- 同 command_id + 不同 payload → **409 IDEMPOTENCY_KEY_REUSED**
- 终态再操作 → **409 RUN_STATE_CONFLICT**
- 真实 `ai_control_commands` 记录：status=done + payload_hash=a4fd953b... + response_json 含 run.status="cancelled"（首次成功响应已存储）
- 注：cancel 的 run `8bf2befd`（P19 遗留 created run）被合理清理为终态

**4) A0-04 metrics fail-closed（公共 /api/v1/metrics/query，admin JWT + canonical tenant）**
- 纯 PromQL（无 service）→ **400 METRICS_PROMQL_PASSTHROUGH_DISABLED**（"任意 PromQL 直通已关闭"）
- `cluster_id=all` → **400 MISSING_CONCRETE_CLUSTER**（"必须指定 concrete canonical cluster_id"）
- 带 service + concrete cluster → **200** typed RED（data 空因该 service 无数据，主链未破坏）
- internal `/internal/v1/query/metrics` concrete canonical cluster → **200 NO_DATA**（正常）

**5) A0-05 unfinished capability 区分（签名 internal 请求）**
- 错误 capability `control_plane.runs.recover` → **403 TENANT_ACCESS_DENIED "unauthorized capability"**
- 正确 capability `control_plane.runs.recover.global` → **200** 返回真实 1 个 unfinished Run（`8bf2befd` status=created）
- internal `/internal/v1/query/kubernetes` canonical cluster → **200** total_nodes=1 total_pods=33（K8s 正常路径，非空数组）

**诚实边界（A0 真实验证未覆盖）**：
- A0-02（orchestrator `PersistentRunStateStore.get()` remote fail-closed）与 A0-03（outbox dispatch fencing 真实 dispatch）**未做运行时真实验证**——当前 orchestrator 仍是旧镜像（未重建，不含 A0-02 改动），outbox 无新 dispatch 事件；二者靠单元测试覆盖（test_persistent_run_state_store.py + ai_run_outbox_test.go）
- K8s 失败路径（partial/all-fail）未在真实环境制造故障（风险高），靠单测 `TestInternalQueryKubernetesPartial/AllFail` 覆盖；K8s 正常路径已真实验证
- `completed_at` 字段在真实 cancel 记录中为 NULL（JSON 序列化 NULL，非逻辑缺陷）
- 生产部署的镜像为本机交叉编译 + 基于旧镜像 COPY，非标准 Dockerfile 多阶段构建（因 base image 网络拉取失败）

**边界**：真实 MySQL/K8s Integration Gate（gate10/recovery_integration）单测未执行（无本地测试库）；`0004` 已应用生产库；Execution Production Execution=NOT YET APPROVED；红线 F1-F5 保持；GIT_ACTION=NONE（未 commit）。

## Self-Review

- Spec 覆盖：A0 9 项 → 本 plan 合并为 5 个任务，覆盖 F-01/F-02/F-09/F-11/F-18/F-19，A0 其余（LLM secret endpoint、注释清理）归入 A0-04/A0-05 后续。
- Placeholder：无 TBD/TODO，每任务有明确文件与改动。
- Type consistency：AIControlCommand 新字段、`ApplyRunControlCommandTx`、cancel 签名（expected_version + command_id）在客户端/服务端一致。
