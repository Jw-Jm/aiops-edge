# P0 Runtime Closure Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 修复当前提交中会阻断 Investigation 主链的租约上下文、恢复协议、终态判定、持久化幂等和 Action 接线问题。

**Architecture:** Query API/MySQL 继续作为控制面权威；orchestrator 只保存非敏感的业务 checkpoint，lease token 通过 worker 的临时执行上下文传递；read-only Investigation 不再无条件成功，Action Executor 仍是唯一 mutation 边界。

**Tech Stack:** Python 3.9/pytest/asyncio/LangGraph、Go net/http/MySQL/sqlmock、现有 Helm 与 React 合约。

**Spec:** `docs/superpowers/specs/2026-08-25-aiops-workflow-convergence-design.md`

## Global Constraints

- 不把 `lease_token` 写入 LangGraph checkpoint 或前端响应。
- `success` 只表示完整调查/验证成功；错误、空数据和审批暂停必须使用明确的非成功状态。
- Action Executor 保持 `EXECUTION_MODE=disabled`，本轮不启用生产 mutation。
- 每个行为修复先写一个会失败的回归测试，再修改生产代码。
- 不修改已应用 migration；必要时优先复用现有表和字段。

### Task 1: Lease context survives graph execution without persisting secrets

**Files:**
- Modify: `ai-orchestrator/invocation_scope.py`
- Modify: `ai-orchestrator/orchestrator.py`
- Modify: `ai-orchestrator/tools.py`
- Test: `ai-orchestrator/tests/test_invocation_scope.py`
- Test: `ai-orchestrator/tests/test_tools_investigation_path.py`

**Interfaces:** `ScopeViewSnapshot` keeps `invocation_id` and a non-secret lease reference (`executor_id`, `lease_epoch`) in memory; `lease_token` is injected through an ephemeral context binding before graph nodes run. Tool construction must receive a complete `ToolExecutionContext` and fail before HTTP if the binding is absent.

- [ ] Add a failing projection/tool-context test that restores an Investigation scope and asserts invocation and lease identity are available while the token is not serialized.
- [ ] Run the focused Python tests and observe the missing `invocation_id`/lease failure.
- [ ] Implement the smallest ephemeral binding and ensure each worker binds/unbinds it around `brain.investigate`.
- [ ] Run the two focused tests and the existing tool execution context tests.

### Task 2: Durable-compatible recovery payload and retry loop

**Files:**
- Modify: `ai-apm-query-go/internal/api/control_plane_runs.go`
- Modify: `ai-apm-query-go/internal/store/ai_run_lease.go`
- Modify: `ai-orchestrator/control_plane_client.py`
- Modify: `ai-orchestrator/main.py`
- Test: `ai-apm-query-go/internal/api/control_plane_runs_test.go`
- Test: `ai-orchestrator/tests/test_investigation_recovery_startup.py`

**Interfaces:** Recovery candidates return the complete Run identity needed to reconstruct an invocation: `run_id`, `request_id`, `invocation_id`, `tenant_id`, `cluster_id`, `status`, intent, target and action mode. Recovery is a periodic bounded scan, not a single startup attempt; active leases remain excluded by query-api.

- [ ] Add a failing Go response test asserting complete candidate fields.
- [ ] Add a failing Python test proving a candidate is reconstructed with its original invocation ID and tenant.
- [ ] Run both tests and confirm the current response is incomplete.
- [ ] Return the joined Run + lease candidate DTO and add a bounded recovery loop with retry/backoff.
- [ ] Run focused Go/Python recovery tests.

### Task 3: Truthful Investigation outcomes and durable progress events

**Files:**
- Modify: `ai-orchestrator/investigation_runtime.py`
- Modify: `ai-orchestrator/main.py`
- Modify: `ai-orchestrator/orchestrator.py`
- Test: `ai-orchestrator/tests/test_investigation_runtime.py`
- Test: `ai-orchestrator/tests/test_investigation_dispatcher.py`

**Interfaces:** Brain adapter returns `InvestigationOutcome(status, events, error_code, report)`; runtime maps `success|partial|failed|awaiting_approval` to legal Run transitions and commits all durable progress events. Stream errors are not converted into a successful outcome.

- [ ] Add failing tests for error events, empty/no-data result and approval interruption.
- [ ] Run them and confirm the current runtime always commits success.
- [ ] Implement outcome normalization and legal transition selection; preserve lease fencing on commit.
- [ ] Persist deterministic event IDs derived from invocation and event index so retries are idempotent.
- [ ] Run focused runtime/dispatcher tests.

### Task 4: Run and ToolRun idempotency boundaries

**Files:**
- Modify: `ai-orchestrator/investigation_dispatcher.py`
- Modify: `ai-apm-query-go/internal/api/internal_query.go`
- Modify: `ai-apm-query-go/internal/api/toolrun_wrapper.go`
- Modify: `ai-apm-query-go/internal/store/ai_tool_runs.go`
- Test: `ai-orchestrator/tests/test_investigation_dispatcher.py`
- Test: `ai-apm-query-go/internal/api/internal_query_test.go`

**Interfaces:** Accepted invocation identity is durable/idempotent across process restarts; ToolRun `running` replays do not execute data-source I/O, terminal replays return the stored envelope, and argument mismatches return 409.

- [ ] Add failing tests for a duplicate after worker completion and for a ToolRun terminal replay returning stored data.
- [ ] Run focused tests and observe the current in-memory/flag-only behavior.
- [ ] Implement response replay and fail-closed handling for duplicate/running insert races without bypassing ToolRun fencing.
- [ ] Run Go and Python idempotency tests.

### Task 5: Wire Action proposal fields and verification safety

**Files:**
- Modify: `ai-apm-query-go/internal/api/control_plane_actions.go`
- Modify: `ai-apm-query-go/internal/store/ai_actions.go`
- Modify: `ai-orchestrator/control_plane_client.py`
- Modify: `ai-orchestrator/orchestrator.py`
- Modify: `ai-apm-query-go/internal/api/action_executor_client.go`
- Modify: `ai-action-executor/main.go`
- Test: `ai-apm-query-go/internal/api/action_executor_client_test.go`
- Test: `ai-action-executor/main_test.go`

**Interfaces:** Persisted Action includes target UID/name/resourceVersion/namespace/operation and immutable target spec. Investigation can only create a proposal; approval and execution require the persisted Action hash. Reconcile reads the target and records an explicit outcome; it never returns a synthetic success.

- [ ] Add failing tests for missing target fields and synthetic reconcile success.
- [ ] Run focused Go tests and observe current acceptance of incomplete Action / simulated reconcile.
- [ ] Extend the action payload and DAO mapping, then make reconcile fail closed when no real observer is available.
- [ ] Run action and executor tests; keep production execution disabled.

### Task 6: Verification and delivery gate

- [ ] Run all focused Python tests for scopes, runtime, dispatcher, recovery and tools.
- [ ] Run Go tests for control-plane runs, ToolRun, actions, executor and SSE.
- [ ] Run frontend typecheck/build if dependencies are available.
- [ ] Inspect `git diff`, `git status`, and changed-file scope.
- [ ] Do not claim completion until every command has fresh passing output; report any environment-blocked checks explicitly.
