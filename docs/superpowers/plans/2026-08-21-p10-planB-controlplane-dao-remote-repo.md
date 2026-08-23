# Plan B — control-plane 鉴权 + Run/Event DAO + orchestrator 远端提交仓储（P10 闭环 2/4）

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 固化独立 `control-plane.*` capability 域 + `authorizeInternalControlPlane` 专用鉴权器；实现 `AIRunEventDAO` 与 `/internal/v1/control-plane/*` 端点（transition/cancel/get/list/unfinished + events append/replay，**不含业务 create**）；orchestrator 侧实现远端提交优先 `PersistentRunRepository` + `RunCache` + `RunStateMachine`。

**Architecture:** orchestrator（system principal）经 `/internal/v1/control-plane/*` 让 query-api（persistence owner）CAS + 持久化 Run/Event；orchestrator 内存仅作已提交缓存。

**Tech Stack:** Go（query-go：store + internal/api）、Python（orchestrator：run_state_machine/persistent_run_repository/run_cache/control_plane_client）。

**设计依据：** `docs/superpowers/specs/2026-08-21-p10-full-closed-loop-design.md` §D1/D2/D3 + §R2/R4 + §6。

## Global Constraints

- `control-plane.*` capability 不进 `tool_registry.KNOWN_CAPABILITIES`。
- control-plane 端点校验：`principal_type==system`、`principal_id==ai-orchestrator`、精确 capability、issuer、method/path→action、scope 一致。
- **control-plane 不含业务 Run 创建**（transition/cancel/unfinished/get/list 仅针对已存在 Run）。
- 远端提交优先：HTTP 失败不推进内存缓存；响应丢失用同 `command_id` 重试返回首次结果。
- event append：先锁 Run sequence owner → 查 event_id → 分配 sequence → 插入（幂等）。
- `partial` 是终态；Go ScanUnfinished 排除 partial。
- orchestrator 不直连 DB。

---

### Task B1: 抽取公共验签底座 + authorizeInternalControlPlane

**Files:**
- Create: `ai-apm-query-go/internal/api/control_plane.go`
- Create: `ai-apm-query-go/internal/api/control_plane_test.go`

**Interfaces:**
- Consumes: `internalQueryEnvelope.go` 的 `internalVerifier`、`internalQueryError`、`mapTrustedContextVerifyError`、`respondInternalQueryError`、`contract` 错误码。
- Produces: `authorizeInternalControlPlane(r *http.Request, capability string, principalID string, expectedIssuer string) (*internalQueryCtx, error)`。

- [ ] **Step 1: 写失败测试**

```go
func TestAuthorizeControlPlaneRejectsNonSystem(t *testing.T) {
	// 构造签名 context（principal_type=user）→ 403
	// 构造签名 context（principal_type=system, principal_id=ai-orchestrator, capability=control-plane.runs.mutate）→ 通过
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `cd ai-apm-query-go && go test ./internal/api/ -run TestAuthorizeControlPlane -v`
Expected: FAIL（函数未定义）。

- [ ] **Step 3: 最小实现**

`authorizeInternalControlPlane`：复用 `internalVerifier`（X-Internal-Token + TrustedRequestContext V2 验签，不要求 cluster scope，允许 multi-cluster）；校验 `ctx.PrincipalType == "system"`、`ctx.PrincipalID == principalID`（`ai-orchestrator`）、`ctx.Issuer == expectedIssuer`（`ai-orchestrator`，即调用方向 orchestrator→query-api）、`ctx.Capability == capability`；失败映射为 `internalQueryError`（403/401）。

- [ ] **Step 4: 运行测试确认通过**

Run: `cd ai-apm-query-go && go test ./internal/api/ -run TestAuthorizeControlPlane -v`
Expected: PASS。

- [ ] **Step 5: 提交**

```bash
cd /Users/mssc/Documents/Code/agent/aiops/ai-apm-query-go
git add internal/api/control_plane.go internal/api/control_plane_test.go
git commit -m "feat(api): authorizeInternalControlPlane dedicated authorizer"
```

---

### Task B2: AIRunEventDAO（锁 owner→查 event_id→分配 sequence）

**Files:**
- Create: `ai-apm-query-go/internal/store/ai_run_events.go`
- Create: `ai-apm-query-go/internal/store/ai_run_events_test.go`

**Interfaces:**
- Consumes: `GetDB()/SetDB()`、`0003` 迁移（`ai_run_events.event_id` + `ai_runs.last_event_sequence`）。
- Produces: `AIRunEvent{EventID, RunID, Sequence, EventType, Payload, CreatedAt}`；`AIRunEventDAO{Append, ReplayAfter, LastSequence}`。

- [ ] **Step 1: 写失败测试（事务顺序：锁→查→插；重复 event_id 返回既有）**

```go
func TestAIRunEventAppendIdempotent(t *testing.T) {
	mock, cleanup := setupAIRunsDB(t)
	defer cleanup()
	// 期望 SQL 顺序：UPDATE ai_runs SET last_event_sequence=... WHERE run_id=?（先锁 owner）
	// → SELECT event_id FROM ai_run_events WHERE run_id=? AND event_id=?（查幂等）
	// → INSERT INTO ai_run_events ...
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `cd ai-apm-query-go && go test ./internal/store/ -run TestAIRunEventAppend -v`
Expected: FAIL。

- [ ] **Step 3: 最小实现**

`Append(ctx, ev AIRunEvent) (AIRunEvent, bool, error)`：
1. `tx, _ := conn.BeginTx(...)`；`defer tx.Rollback()`。
2. `UPDATE ai_runs SET last_event_sequence = last_event_sequence + 1 WHERE run_id = ?`（行锁 owner）。
3. `SELECT event_id FROM ai_run_events WHERE run_id = ? AND event_id = ?`——已存在 → 返回既有事件 `(ev, false, nil)`（幂等，**不**再插入）。
4. `INSERT INTO ai_run_events (run_id, sequence, event_id, event_type, payload_json, created_at) VALUES (?, ?, ?, ?, ?, ?)`，sequence = `last_event_sequence`。
5. `tx.Commit()` → `(ev, true, nil)`。
`ReplayAfter(runID, afterSeq)`：`SELECT ... WHERE run_id=? AND sequence > ? ORDER BY sequence ASC`。
`LastSequence(runID)`：`SELECT last_event_sequence FROM ai_runs WHERE run_id=?`。

- [ ] **Step 4: 运行测试确认通过**

Run: `cd ai-apm-query-go && go test ./internal/store/ -run TestAIRunEvent -v`
Expected: PASS。

- [ ] **Step 5: 提交**

```bash
cd /Users/mssc/Documents/Code/agent/aiops/ai-apm-query-go
git add internal/store/ai_run_events.go internal/store/ai_run_events_test.go
git commit -m "feat(store): ai_run_events DAO (lock owner + event_id idempotency + sequence)"
```

---

### Task B3: control-plane runs 端点（transition/cancel/get/list/unfinished）

**Files:**
- Create: `ai-apm-query-go/internal/api/control_plane_runs.go`
- Create: `ai-apm-query-go/internal/api/control_plane_runs_test.go`
- Modify: `ai-apm-query-go/internal/api/handler.go`（Handler 加 `runDAO *store.AIRunDAO`）
- Modify: `ai-apm-query-go/cmd/api/main.go`（注册 `/internal/v1/control-plane/...`）

**Interfaces:**
- Consumes: `authorizeInternalControlPlane`（B1）、`AIRunDAO.Transition/Get/List/ScanUnfinished`。
- Produces: `InternalControlPlaneRunTransition/Cancel/Get/List/Unfinished`。`Transition` body：`{command_id, expected_version, target}`；冲突 → 409 `RUN_STATE_CONFLICT`。

- [ ] **Step 1: 写失败测试（httptest，system principal + capability=control-plane.runs.mutate）**

```go
func TestControlPlaneRunTransitionCAS(t *testing.T) {
	// POST /internal/v1/control-plane/runs/{id}/transition
	// body {expected_version:0, target:"planning"} → runDAO.Transition ok → 200
	// expected_version 不符 → 409 RUN_STATE_CONFLICT
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `cd ai-apm-query-go && go test ./internal/api/ -run TestControlPlaneRunTransition -v`
Expected: FAIL。

- [ ] **Step 3: 最小实现**

`InternalControlPlaneRunTransition`：`authorizeInternalControlPlane(r, "control-plane.runs.mutate", "ai-orchestrator", "ai-orchestrator")`；decode body；`runDAO.Transition(runID, target, expectedVersion, now)`；RowsAffected==0 → 409 `RUN_STATE_CONFLICT`；成功返回 200 + committed Run。`Cancel` 类似（target=cancelled）。`Get/List/Unfinished` 用 `control-plane.runs.recover`（List/Unfinished）与 `control-plane.runs.mutate`（Get 读，但 recover 亦可读）——固定：Get 用 `control-plane.runs.recover`。

- [ ] **Step 4: 运行测试确认通过**

Run: `cd ai-apm-query-go && go test ./internal/api/ -run TestControlPlaneRun -v`
Expected: PASS。

- [ ] **Step 5: main.go 注册 + 提交**

```go
mux.HandleFunc("/internal/v1/control-plane/runs/", handler.InternalControlPlaneRunRouter)
mux.HandleFunc("/internal/v1/control-plane/runs", handler.InternalControlPlaneRunRouter)
```

```bash
cd /Users/mssc/Documents/Code/agent/aiops/ai-apm-query-go
git add internal/api/control_plane_runs.go internal/api/control_plane_runs_test.go internal/api/handler.go cmd/api/main.go
git commit -m "feat(api): control-plane runs endpoints (transition/cancel/get/list/unfinished)"
```

---

### Task B4: control-plane events 端点（append/replay）

**Files:**
- Create: `ai-apm-query-go/internal/api/control_plane_events.go`
- Create: `ai-apm-query-go/internal/api/control_plane_events_test.go`
- Modify: `ai-apm-query-go/internal/api/handler.go`（Handler 加 `eventDAO *store.AIRunEventDAO`）
- Modify: `ai-apm-query-go/cmd/api/main.go`

**Interfaces:**
- Consumes: `AIRunEventDAO.Append/ReplayAfter/LastSequence`（B2）。
- Produces: `InternalControlPlaneEventAppend`（capability=control-plane.events.append）、`InternalControlPlaneEventReplay`（capability=control-plane.events.replay）。

- [ ] **Step 1: 写失败测试（append 幂等 + replay after_sequence）**

```go
func TestControlPlaneEventAppendIdempotent(t *testing.T) {
	// POST .../events {event_id, event_type, payload} → 200 {sequence:1, created:true}
	// 重复 event_id → 200 {sequence:1, created:false}（返回既有，不追加）
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `cd ai-apm-query-go && go test ./internal/api/ -run TestControlPlaneEvent -v`
Expected: FAIL。

- [ ] **Step 3: 最小实现**

`InternalControlPlaneEventAppend`：`authorizeInternalControlPlane(r, "control-plane.events.append", ...)`；decode body；`eventDAO.Append(ctx, ev)`；返回 `{sequence, created}`。`InternalControlPlaneEventReplay`：`authorizeInternalControlPlane(r, "control-plane.events.replay", ...)`；`after_sequence` query param；返回事件数组。

- [ ] **Step 4: 运行测试确认通过**

Run: `cd ai-apm-query-go && go test ./internal/api/ -run TestControlPlaneEvent -v`
Expected: PASS。

- [ ] **Step 5: 提交**

```bash
cd /Users/mssc/Documents/Code/agent/aiops/ai-apm-query-go
git add internal/api/control_plane_events.go internal/api/control_plane_events_test.go internal/api/handler.go cmd/api/main.go
git commit -m "feat(api): control-plane events endpoints (append idempotent + replay)"
```

---

### Task B5: orchestrator RunStateMachine + RunCache（纯内存，可测）

**Files:**
- Create: `ai-orchestrator/run_state_machine.py`
- Create: `ai-orchestrator/run_cache.py`
- Create: `ai-orchestrator/tests/test_b5_run_state_machine_cache.py`

**Interfaces:**
- Consumes: `contracts.Run`/`RunStatus`/`RunScopeKind`。
- Produces: `RunStateMachine.validate_transition(current, target) -> None`（抛 `RunPersistenceError("ILLEGAL_RUN_TRANSITION")`）、`RunStateMachine.terminal_statuses() -> frozenset`；`RunCache`：`get/put/invalidate`，只存已提交 Run。

- [ ] **Step 1: 写失败测试**

```python
def test_state_machine_rejects_terminal_migration():
    sm = RunStateMachine()
    with pytest.raises(RunPersistenceError) as ex:
        sm.validate_transition("success", "planning")
    assert ex.value.error_code == "ILLEGAL_RUN_TRANSITION"

def test_run_cache_failed_put_does_not_commit():
    cache = RunCache()
    cache.put(run)  # 正常
    with pytest.raises(RuntimeError):
        cache.put_with_check(run_conflict)  # 冲突时不推进
    assert cache.get(run_id) == run
```

- [ ] **Step 2: 运行测试确认失败**

Run: `cd ai-orchestrator && python -m pytest tests/test_b5_run_state_machine_cache.py -v`
Expected: FAIL（ModuleNotFoundError）。

- [ ] **Step 3: 最小实现**

从 `run_persistence.py` 提取 `_TERMINAL`/`_RUN_TRANSITIONS`/`_validate_transition` 到 `RunStateMachine`（保持语义一致）。`RunCache` 用 `dict[UUID, contracts.Run]`，`put_with_check` 在收到冲突信号时抛异常且不写入。

- [ ] **Step 4: 运行测试确认通过**

Run: `cd ai-orchestrator && python -m pytest tests/test_b5_run_state_machine_cache.py -v`
Expected: PASS。

- [ ] **Step 5: 提交**

```bash
cd /Users/mssc/Documents/Code/agent/aiops/ai-orchestrator
git add run_state_machine.py run_cache.py tests/test_b5_run_state_machine_cache.py
git commit -m "feat(orchestrator): RunStateMachine + RunCache (pure in-memory)"
```

---

### Task B6: orchestrator PersistentRunRepository（远端提交优先 + command_id 幂等）

**Files:**
- Create: `ai-orchestrator/control_plane_client.py`
- Create: `ai-orchestrator/persistent_run_repository.py`
- Create: `ai-orchestrator/tests/test_b6_persistent_repo.py`

**Interfaces:**
- Consumes: `contracts.Run`、`TrustedContextIssuer`（签发 system principal + control-plane capability）、`_default_http`（`internal_query_client.py`）。
- Produces: `ControlPlaneClient`：`transition(run_id, target, expected_version, command_id)`、`cancel(...)`、`get/list/unfinished`、`append_event(...)`、`replay(...)`，capability 从固定映射取（`control-plane.runs.mutate/recover/events.append/replay`），principal_id=`ai-orchestrator`。`PersistentRunRepository`：`commit(run_id, expected_version, target, command_id) -> contracts.Run`（HTTP 成功才更新 RunCache；失败抛 `PersistError`；响应丢失用同 command_id 重试）。

- [ ] **Step 1: 写失败测试（fake http：成功/失败/响应丢失重试）**

```python
def test_commit_success_updates_cache():
    repo = PersistentRunRepository(client=fake_ok, cache=RunCache())
    run = repo.commit(run_id, 0, "planning", command_id="c1")
    assert cache.get(run_id).status == "planning"

def test_commit_http_failure_does_not_update_cache():
    repo = PersistentRunRepository(client=fake_500, cache=RunCache())
    with pytest.raises(PersistError):
        repo.commit(run_id, 0, "planning", command_id="c1")
    assert cache.get(run_id).status == "created"  # 不推进

def test_commit_response_loss_retry_same_command_id():
    # 第一次超时，第二次同 command_id 返回首次 committed result
    assert repo.commit(...) 返回同一 committed Run，不重复
```

- [ ] **Step 2: 运行测试确认失败**

Run: `cd ai-orchestrator && python -m pytest tests/test_b6_persistent_repo.py -v`
Expected: FAIL。

- [ ] **Step 3: 最小实现**

`ControlPlaneClient` 组装 claims（`principal_type=system, principal_id=ai-orchestrator, capability=<固定>, issuer=ai-orchestrator`）+ `command_id` 进 body；`PersistentRunRepository.commit` 远端提交成功才 `cache.put`，失败抛 `PersistError`，响应丢失用同 `command_id` 重试（幂等返回首次结果）。

- [ ] **Step 4: 运行测试确认通过**

Run: `cd ai-orchestrator && python -m pytest tests/test_b6_persistent_repo.py -v`
Expected: PASS。

- [ ] **Step 5: 提交**

```bash
cd /Users/mssc/Documents/Code/agent/aiops/ai-orchestrator
git add control_plane_client.py persistent_run_repository.py tests/test_b6_persistent_repo.py
git commit -m "feat(orchestrator): PersistentRunRepository (remote-commit-first, command_id idempotent)"
```

---

### Task B7: Plan B 验收 + 全量回归

- [ ] **Step 1: Go 全量测试**

Run: `cd ai-apm-query-go && go test ./... 2>&1 | tail -30`
Expected: 全 PASS，`go vet ./...` ok。

- [ ] **Step 2: orchestrator 全量测试**

Run: `cd ai-orchestrator && python -m pytest tests/test_b5_run_state_machine_cache.py tests/test_b6_persistent_repo.py tests/test_p10_run_persistence.py -v`
Expected: 全 PASS（既有 `test_p10_run_persistence.py` 19 用例无回归）。

- [ ] **Step 3: 提交验收**

```bash
cd /Users/mssc/Documents/Code/agent/aiops
git add docs/V9.2_V9.3_P0_P9_IMPLEMENTATION_EVIDENCE.md
git commit -m "docs: P10 Plan B (control-plane auth + DAOs + remote-commit repo) done"
```
