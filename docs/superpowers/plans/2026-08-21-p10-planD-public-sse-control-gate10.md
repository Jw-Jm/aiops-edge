# Plan D — Public Control/SSE、授权重放及完整 Gate 10（P10 闭环 4/4）

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 实现 query-api 公共 SSE proxy（JWT 授权 + Last-Event-ID + retention + heartbeat + live-tail）、公共 Control 入口，并完成 Gate 10 完整断言（含真实 MySQL + 进程重启）。同时收紧 orchestrator 侧 `ai_runs_api.py`（移除公共 create）。

**Architecture:** query-api 是 `ai_run_events` 持久化 + replay owner，公共 SSE 由 query-api 直接从持久化事件 replay + live-tail（不回到 orchestrator）；Browser 只连 query-api。

**Tech Stack:** Go（query-go：internal/api + cmd/api）、Python（orchestrator：ai_runs_api.py 收敛）。

**设计依据：** `docs/superpowers/specs/2026-08-21-p10-full-closed-loop-design.md` §R1/P1-2 + §R5 + §7。

## Global Constraints

- SSE 所有权：query-api 直接 replay + live-tail，不回 orchestrator。
- 公共 SSE：JWT + Run tenant/cluster/resource 授权；每次重连重新鉴权；`Last-Event-ID`（sequence）；retention 越界明确错误或完整 reload；heartbeat 10-15s；禁止 Browser 直接连 orchestrator。
- `ai_runs_api.py` 移除公共 create 路由（返回不可用），不留第二公共入口。
- orchestrator 不直连 DB；红线 F1-F5 保持。
- Gate 10 完整通过需真实 MySQL + 进程重启集成测试。

---

### Task D1: 收敛 orchestrator ai_runs_api.py（移除公共 create）

**Files:**
- Modify: `ai-orchestrator/ai_runs_api.py`
- Modify: `ai-orchestrator/tests/test_ai_runs_api.py`（若有）

**Interfaces:**
- Consumes: 现有 `ai_runs_api.py`。
- Produces: `POST /api/v1/ai/runs` 返回 `410 GONE`（"public run creation moved to query-api"）；保留只读 `GET /api/v1/ai/runs` 列表/详情（转 query-api 数据源，Plan B/D 接 control-plane）。

- [ ] **Step 1: 写失败测试（POST 返回 410）**

```python
def test_public_create_run_moved():
    client = TestClient(app)
    resp = client.post("/api/v1/ai/runs", json={"tenant_id": str(TENANT)})
    assert resp.status_code == 410
```

- [ ] **Step 2: 运行测试确认失败**

Run: `cd ai-orchestrator && python -m pytest tests/test_ai_runs_api.py -v`
Expected: FAIL（当前 200）。

- [ ] **Step 3: 最小实现**

将 `create_run` handler 改为抛 `HTTPException(410, detail="RUN_CREATION_MOVED_TO_QUERY_API")`，或移除该路由。保留 list/get。

- [ ] **Step 4: 运行测试确认通过**

Run: `cd ai-orchestrator && python -m pytest tests/test_ai_runs_api.py -v`
Expected: PASS。

- [ ] **Step 5: 提交**

```bash
cd /Users/mssc/Documents/Code/agent/aiops/ai-orchestrator
git add ai_runs_api.py tests/test_ai_runs_api.py
git commit -m "fix(orchestrator): remove public run creation route (moved to query-api)"
```

---

### Task D2: query-api 公共 SSE proxy（replay + live-tail + 授权 + heartbeat）

**Files:**
- Create: `ai-apm-query-go/internal/api/sse_proxy.go`
- Create: `ai-apm-query-go/internal/api/sse_proxy_test.go`
- Modify: `ai-apm-query-go/internal/api/handler.go`（Handler 加 `eventDAO`）
- Modify: `ai-apm-query-go/cmd/api/main.go`（注册 `GET /api/v1/ai/runs/{id}/events`）

**Interfaces:**
- Consumes: `AIRunEventDAO.ReplayAfter/LastSequence`（Plan B）、AuthMiddleware（JWT）。
- Produces: `handler.StreamRunEvents(w,r)`：JWT 授权 + Run tenant 校验；`Last-Event-ID` header/query → `after_sequence`；heartbeat 每 12s 发 `: ping`；replay 已持久化事件后 live-tail（内部事件通知 + DB 轮询兜底）；retention 越界 → `SSE_RETENTION_EXCEEDED`。

- [ ] **Step 1: 写失败测试（httptest + httptest.NewServer 流式读）**

```go
func TestStreamRunEvents(t *testing.T) {
	// JWT 授权 → 200，Content-Type text/event-stream
	// 先 append 2 事件 → 流读到 event: e1\nevent: e2
	// Last-Event-ID:1 → 只读 sequence>1
	// 跨租户 → 403
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `cd ai-apm-query-go && go test ./internal/api/ -run TestStreamRunEvents -v`
Expected: FAIL。

- [ ] **Step 3: 最小实现**

`StreamRunEvents`：AuthMiddleware 后取 JWT tenant；`eventDAO` 校验 Run 归属 tenant（`AIRunDAO.Get`）；`after_sequence` 从 `Last-Event-ID` header（或 `after_sequence` query）取；超 retention → SSE 错误帧；循环：replay `ReplayAfter(runID, afterSeq)` 写 `data: <json>\n\n`，随后 ticker 12s heartbeat `: ping\n\n`；每 tick 重新 `LastSequence` 检查新增（live-tail）。`Flush` 每帧。

- [ ] **Step 4: 运行测试确认通过**

Run: `cd ai-apm-query-go && go test ./internal/api/ -run TestStreamRunEvents -v`
Expected: PASS。

- [ ] **Step 5: 提交**

```bash
cd /Users/mssc/Documents/Code/agent/aiops/ai-apm-query-go
git add internal/api/sse_proxy.go internal/api/sse_proxy_test.go internal/api/handler.go cmd/api/main.go
git commit -m "feat(api): public SSE proxy (replay + live-tail + heartbeat + retention)"
```

---

### Task D3: 公共 Control 入口（Browser → query-api cancel 等）

**Files:**
- Create: `ai-apm-query-go/internal/api/runs_control.go`
- Create: `ai-apm-query-go/internal/api/runs_control_test.go`
- Modify: `ai-apm-query-go/cmd/api/main.go`

**Interfaces:**
- Consumes: `AIRunDAO.Transition/Cancel`、AuthMiddleware、AuthorizationMatrix（capability=ai.investigate/control）。
- Produces: `POST /api/v1/ai/runs/{id}/cancel`（JWT + capability 校验 → runDAO transition to cancelled → 写 control command 幂等）；响应 200 committed。

- [ ] **Step 1: 写失败测试（cancel 授权 + 幂等 command）**

```go
func TestPublicCancelRun(t *testing.T) {
	// JWT 授权 + capability → 200 cancelled
	// 同 command_id 重复 → 200 首次结果（幂等）
	// 跨租户 → 403
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `cd ai-apm-query-go && go test ./internal/api/ -run TestPublicCancel -v`
Expected: FAIL。

- [ ] **Step 3: 最小实现**

`PublicCancelRun`：JWT tenant 校验 Run 归属；`runDAO.Transition(runID, "cancelled", expectedVersion, now)`；写 `ai_control_commands`（command_id 幂等）；返回 committed Run。

- [ ] **Step 4: 运行测试确认通过**

Run: `cd ai-apm-query-go && go test ./internal/api/ -run TestPublicCancel -v`
Expected: PASS。

- [ ] **Step 5: 提交**

```bash
cd /Users/mssc/Documents/Code/agent/aiops/ai-apm-query-go
git add internal/api/runs_control.go internal/api/runs_control_test.go cmd/api/main.go
git commit -m "feat(api): public run control (cancel) with command idempotency"
```

---

### Task D4: Gate 10 完整断言 + 真实 MySQL/进程重启集成测试

**Files:**
- Create: `ai-apm-query-go/internal/api/gate10_integration_test.go`（`//go:build integration`）
- Create: `ai-orchestrator/tests/test_p10_gate10_full.py`（orchestrator 侧 Gate 10 追加断言）

**Interfaces:**
- Consumes: 真实 MySQL、Plan A-C 全部 DAO/端点。
- Produces: Gate 10 全部断言 PASS（orchestrator restart recovery / duplicate request_id idempotent / illegal transition 409 / cancel works / SSE reconnect replay / event sequence monotonic / unauthorized replay rejected / Run relationships survive restart / CAS conflict deterministic / recovery does not duplicate Tool/Action / no parallel Incident persistence）。

- [ ] **Step 1: 写 Gate 10 集成测试**

```go
//go:build integration
func TestGate10Full(t *testing.T) {
	// 真实 MySQL：Create run → duplicate request_id → existing
	// illegal transition 409；cancel；event sequence monotonic
	// 进程重启 → ScanUnfinished 恢复 → 同 idempotency_key tool_run 不重复
	// SSE replay preserves sequence + unauthorized rejected
}
```

- [ ] **Step 2: 运行集成测试（需真实 MySQL）**

Run: `cd ai-apm-query-go && TEST_MYSQL_DSN="..." go test -tags integration ./internal/api/ -run TestGate10Full -v`
Expected: PASS（环境受限则记录为后续真实环境 Integration Gate）。

- [ ] **Step 3: orchestrator Gate 10 追加断言**

在 `test_p10_gate10_full.py` 加 `test_gate10_no_parallel_incident_persistence`（沿用既有 `run_persistence` 断言）+ `test_gate10_remote_commit_no_double_write`（PersistentRunRepository HTTP 失败不推进缓存）。

Run: `cd ai-orchestrator && python -m pytest tests/test_p10_gate10_full.py -v`
Expected: PASS。

- [ ] **Step 4: 提交**

```bash
cd /Users/mssc/Documents/Code/agent/aiops/ai-apm-query-go
git add internal/api/gate10_integration_test.go
git commit -m "test(integration): Gate 10 full assertions (real MySQL + restart)"
cd /Users/mssc/Documents/Code/agent/aiops/ai-orchestrator
git add tests/test_p10_gate10_full.py
git commit -m "test(orchestrator): Gate 10 assertions"
```

---

### Task D5: Plan D 验收 + 全量回归 + Evidence

- [ ] **Step 1: Go 全量测试**

Run: `cd ai-apm-query-go && go test ./... 2>&1 | tail -30`
Expected: 全 PASS（integration 标签除外）。

- [ ] **Step 2: orchestrator 全量测试**

Run: `cd ai-orchestrator && python -m pytest tests/test_p10_gate10_full.py tests/test_p10_run_persistence.py tests/test_b5_run_state_machine_cache.py tests/test_b6_persistent_repo.py tests/test_ai_runs_api.py -v`
Expected: 全 PASS，无回归。

- [ ] **Step 3: 红线隔离检查**

Run: `cd aiops && grep -rn "execute\|credential\|kubeconfig\|adapter" ai-orchestrator/control_plane_client.py ai-orchestrator/persistent_run_repository.py ai-orchestrator/run_cache.py 2>/dev/null | grep -v ".pyc" || echo "0 match"`
Expected: 0 match（Agent≠Execution 隔离保持）。

- [ ] **Step 4: 追加总 Evidence + 提交**

```bash
cd /Users/mssc/Documents/Code/agent/aiops
git add docs/V9.2_V9.3_P0_P9_IMPLEMENTATION_EVIDENCE.md
git commit -m "docs: P10 full closed loop complete (Plans A-D) + Gate 10"
```
