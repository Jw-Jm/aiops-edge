# Plan A — 权威 Run 映射 + 0003 迁移 + 公共创建 + 可靠派发（P10 闭环 1/4）

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 建立权威 `contracts.Run ↔ AIRun` 字段映射，落地 0003 迁移（request_id/event_id 幂等 + sequence counter + partial 终态 + 空值回填），实现 query-api 公共 `POST/GET /api/v1/ai/runs` 鉴权创建 + durable outbox 可靠派发 RunInvocation 给 orchestrator。

**Architecture:** query-api 是 Run 持久化 owner；Browser 经公共 `POST /api/v1/ai/runs`（JWT + ai.investigate + ManualBoundary）创建并持久化 Run（权威），随后写 `ai_run_outbox` 经 dispatcher 可靠派发可信 RunInvocation 给 orchestrator `/internal/v1/run-invocations`。

**Tech Stack:** Go（query-go：store + internal/api + cmd/api）、MySQL（migrations）、Python（orchestrator 消费侧在 Plan B/D 接入）。

**设计依据：** `docs/superpowers/specs/2026-08-21-p10-full-closed-loop-design.md` §R1/R2/R6 + §6。

## Global Constraints

- orchestrator 不直连 DB（P0-3）；Run 持久化仅经 query-api。
- `ai_runs` 权威状态起点为 `created`（对齐 Python `contracts.RunStatus.CREATED`），非 DB 默认 `pending`。
- 不可变字段（run_id/request_id/tenant_id/principal_type/principal_id/session_id/scope_kind/primary_cluster_id/intent/action_mode/target_type/target_resource_id/time_range_start/time_range_end/parent_run_id/created_at）Create 后不得改写；相同 run_id 重复 create（不同不可变参数）fail-closed。
- **禁止** `ON DUPLICATE KEY UPDATE run_id = VALUES(run_id)` 改写原 run_id。
- `UNIQUE(tenant_id, request_id)` 迁移前必须先回填历史空 `request_id`。
- 红线 F1-F5 保持；Execution Production Execution = NOT YET APPROVED。
- `control-plane.*` capability 不进用户 Tool Registry。

---

### Task A1: 修正 AIRunDAO（字段补全 + 幂等返回 + partial 终态）

**Files:**
- Modify: `ai-apm-query-go/internal/store/ai_runs.go`
- Modify: `ai-apm-query-go/internal/store/ai_runs_test.go`

**Interfaces:**
- Consumes: 现有 `AIRun` 结构、`GetDB()/SetDB()`（`internal/store/mysql.go`）。
- Produces: `AIRun` 增加字段 `PrincipalType, SessionID, TargetType, TargetResourceID, TimeRangeStart, TimeRangeEnd, FinishedAt *time.Time`（与现有 `RunID/RequestID/TenantID/Principal/ScopeKind/PrimaryClusterID/Intent/ActionMode/Status/StateVersion/ParentRunID/CreatedAt/UpdatedAt` 并存）。`Create` 改为 `Create(r AIRun) (bool, error)` 返回 `created(ok)/existing(!ok)`。`ScanUnfinished` 排除列表含 `partial`。

- [ ] **Step 1: 写失败测试（AIRun 结构含新字段 + Create 返回 created/existing）**

```go
func TestAIRunCreateReturnsExistingOnDuplicate(t *testing.T) {
	mock, cleanup := setupAIRunsDB(t)
	defer cleanup()
	// 唯一键冲突 → ErrDuplicateKey → 返回 existing(!ok)
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO ai_runs")).
		WillReturnError(&mysql.MySQLError{Number: 1062})
	d := &AIRunDAO{}
	created, err := d.Create(AIRun{RunID: "a", RequestID: "r", TenantID: "t", Status: "created"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created {
		t.Fatalf("expected existing (!ok) on duplicate")
	}
}
```

> 若 `github.com/go-sql-driver/mysql` 未引入，用 `errors.New` + 类型断言模拟；见 Task A3 依赖确认。

- [ ] **Step 2: 运行测试确认失败**

Run: `cd ai-apm-query-go && go test ./internal/store/ -run TestAIRunCreateReturnsExistingOnDuplicate -v`
Expected: FAIL（`Create` 当前返回 `error`，非 `(bool, error)`）。

- [ ] **Step 3: 最小实现**

在 `ai_runs.go` 中：
- `AIRun` 增加字段 `PrincipalType, SessionID, TargetType, TargetResourceID string`、`TimeRangeStart, TimeRangeEnd, FinishedAt *time.Time`。
- `Create` 签名改为 `func (d *AIRunDAO) Create(r AIRun) (bool, error)`：执行 `INSERT INTO ai_runs (run_id, request_id, tenant_id, principal, principal_type, session_id, scope_kind, primary_cluster_id, intent, action_mode, target_type, target_resource_id, time_range_start, time_range_end, status, state_version, parent_run_id, created_at, updated_at) VALUES (...)`（无 ON DUPLICATE）。判断重复键（MySQL 1062）返回 `(false, nil)`；其它错误 `(false, err)`；成功 `(true, nil)`。
- `ScanUnfinished` 的 `WHERE status NOT IN` 改为 `('success','partial','failed','regressed','cancelled')`。
- `Get/List/ScanUnfinished/scanAIRun` 同步 select/scan 新列。

- [ ] **Step 4: 运行测试确认通过**

Run: `cd ai-apm-query-go && go test ./internal/store/ -v`
Expected: PASS（含既有 `TestAIRunDAOCreate` 适配新签名）。

- [ ] **Step 5: 提交**

```bash
cd /Users/mssc/Documents/Code/agent/aiops/ai-apm-query-go
git add internal/store/ai_runs.go internal/store/ai_runs_test.go
git commit -m "fix(store): AIRun field mapping + create idempotent result + partial terminal"
```

---

### Task A2: 0003 迁移（request_id UNIQUE + 空值回填 + sequence counter + 恢复列）

**Files:**
- Create: `ai-apm-query-go/internal/store/migrations/versions/0003_ai_runtime_v2.sql`
- Modify: `ai-apm-query-go/internal/store/migrations/`（manifest/coverage 若需登记）

**Interfaces:**
- Consumes: `0002_ai_runtime.sql` 既有表。
- Produces: 迁移后 `ai_runs` 含 `UNIQUE(tenant_id, request_id)`、`last_event_sequence BIGINT NOT NULL DEFAULT 0`、`principal_type/session_id/target_type/target_resource_id/time_range_start/time_range_end/finished_at` 列、状态默认 `created`；`ai_run_events` 含 `event_id CHAR(36)` + `UNIQUE(run_id, event_id)`；`ai_run_outbox` 表（见 D2）。

- [ ] **Step 1: 写迁移 SQL**

```sql
-- 0003-ai-runtime-v2
-- P10 闭环：幂等/序列/恢复/派发结构（query-api Control Plane Persistence owner）。

-- 1) 历史空 request_id 回填（避免 UNIQUE(tenant_id,request_id) 同租户重复空值失败）
UPDATE ai_runs SET request_id = CONCAT('legacy-', run_id) WHERE request_id = '' OR request_id IS NULL;
-- statement-breakpoint

-- 2) ai_runs：补权威 Run 字段 + 幂等约束 + sequence counter
ALTER TABLE ai_runs
  ADD COLUMN principal_type VARCHAR(32) NOT NULL DEFAULT 'user',
  ADD COLUMN session_id CHAR(36) NULL,
  ADD COLUMN target_type VARCHAR(32) NULL,
  ADD COLUMN target_resource_id VARCHAR(512) NULL,
  ADD COLUMN time_range_start DATETIME(3) NULL,
  ADD COLUMN time_range_end DATETIME(3) NULL,
  ADD COLUMN finished_at DATETIME(3) NULL,
  ADD COLUMN last_event_sequence BIGINT NOT NULL DEFAULT 0,
  ADD UNIQUE KEY uq_ai_runs_tenant_request (tenant_id, request_id);
-- statement-breakpoint

-- 状态起点统一为 created（对齐 Python contracts.RunStatus.CREATED）
UPDATE ai_runs SET status = 'created' WHERE status = 'pending';
-- statement-breakpoint

-- 3) ai_run_events：event_id + 幂等
ALTER TABLE ai_run_events
  ADD COLUMN event_id CHAR(36) NOT NULL DEFAULT '',
  ADD UNIQUE KEY uq_ai_run_events (run_id, event_id);
-- statement-breakpoint

-- 4) ai_run_outbox：创建后可靠派发（durable outbox）
CREATE TABLE IF NOT EXISTS ai_run_outbox (
  invocation_id CHAR(36) PRIMARY KEY,
  run_id CHAR(36) NOT NULL,
  status VARCHAR(16) NOT NULL DEFAULT 'pending', -- pending|claimed|delivered|expired
  dispatch_count INT NOT NULL DEFAULT 0,
  next_retry_at DATETIME(3) NULL,
  created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  INDEX idx_ai_run_outbox_pending (status, next_retry_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
```

> 注：Plan C 会追加 `ai_control_commands` 表与 `ai_plan_steps`/`ai_tool_runs`/`ai_actions` 补列；本迁移只做 Plan A/B 需要的结构。若 `0002` 尚无某列/表，先确认现状再定 ALTER（本任务以 `0002_ai_runtime.sql` 为准）。

- [ ] **Step 2: 运行 schema manifest/coverage 校验**

Run: `cd ai-apm-query-go && go test ./internal/store/migrations/ -v`
Expected: PASS（manifest/coverage 需登记新 migration，按既有模式在 `schema_manifest_test.go` 更新清单与 checksum）。

- [ ] **Step 3: 提交**

```bash
cd /Users/mssc/Documents/Code/agent/aiops/ai-apm-query-go
git add internal/store/migrations/
git commit -m "feat(migrations): 0003 ai_runtime_v2 (idempotency + sequence + outbox)"
```

---

### Task A3: 新增 AIRunOutboxDAO

**Files:**
- Create: `ai-apm-query-go/internal/store/ai_run_outbox.go`
- Create: `ai-apm-query-go/internal/store/ai_run_outbox_test.go`

**Interfaces:**
- Consumes: `GetDB()/SetDB()`。
- Produces: `AIRunOutbox{InvocationID, RunID, Status, DispatchCount, NextRetryAt, CreatedAt, UpdatedAt}`；`AIRunOutboxDAO{Insert, Claim, Deliver, ScanPending}`。

- [ ] **Step 1: 写失败测试**

```go
func TestAIRunOutboxInsertAndClaim(t *testing.T) {
	mock, cleanup := setupAIRunsDB(t)
	defer cleanup()
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO ai_run_outbox")).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec(regexp.QuoteMeta("UPDATE ai_run_outbox SET status")).
		WillReturnResult(sqlmock.NewResult(0, 1))
	d := &AIRunOutboxDAO{}
	if err := d.Insert(AIRunOutbox{InvocationID: "i", RunID: "r", Status: "pending"}); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	ok, err := d.Claim("i")
	if err != nil || !ok {
		t.Fatalf("Claim: ok=%v err=%v", ok, err)
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `cd ai-apm-query-go && go test ./internal/store/ -run TestAIRunOutboxInsertAndClaim -v`
Expected: FAIL（`AIRunOutboxDAO` 未定义）。

- [ ] **Step 3: 最小实现**

`Insert`：`INSERT INTO ai_run_outbox (invocation_id, run_id, status, dispatch_count, created_at, updated_at) VALUES (?, ?, 'pending', 0, ?, ?)` 幂等（重复 invocation_id 返回 nil）。
`Claim`：`UPDATE ai_run_outbox SET status='claimed', dispatch_count=dispatch_count+1, next_retry_at=NULL, updated_at=? WHERE invocation_id=? AND status IN ('pending','claimed')`，RowsAffected==1。
`Deliver`：`UPDATE ai_run_outbox SET status='delivered', updated_at=? WHERE invocation_id=?`。
`ScanPending`：`SELECT ... FROM ai_run_outbox WHERE status='pending' AND (next_retry_at IS NULL OR next_retry_at <= ?) LIMIT 50`。

- [ ] **Step 4: 运行测试确认通过**

Run: `cd ai-apm-query-go && go test ./internal/store/ -run TestAIRunOutbox -v`
Expected: PASS。

- [ ] **Step 5: 提交**

```bash
cd /Users/mssc/Documents/Code/agent/aiops/ai-apm-query-go
git add internal/store/ai_run_outbox.go internal/store/ai_run_outbox_test.go
git commit -m "feat(store): ai_run_outbox durable dispatch DAO"
```

---

### Task A4: query-api 公共 POST/GET /api/v1/ai/runs 鉴权创建 + 写 outbox

**Files:**
- Create: `ai-apm-query-go/internal/api/runs_public.go`
- Create: `ai-apm-query-go/internal/api/runs_public_test.go`
- Modify: `ai-apm-query-go/internal/api/handler.go`（Handler 加 `runDAO *store.AIRunDAO`、`outboxDAO *store.AIRunOutboxDAO`）
- Modify: `ai-apm-query-go/cmd/api/main.go`（注册 `POST/GET /api/v1/ai/runs`）

**Interfaces:**
- Consumes: `AIRunDAO`（Task A1）、`AIRunOutboxDAO`（Task A3）、既有 AuthMiddleware（JWT + tenant）、`store.AIRun`。
- Produces: `handler.CreateRunPublic(w,r)`、`handler.ListRunsPublic(w,r)`。POST body：`{tenant_id, cluster_id, intent, action_mode, service, message}`；响应 `{run_id, request_id, status:"created", created_at}`。capability=`ai.investigate`；ManualBoundary 校验（复用 query-go 侧已有 ManualBoundary 语义或由 orchestrator 校验——见 Step）。

- [ ] **Step 1: 写失败测试（httptest 验证鉴权 + 创建 + outbox 写入）**

```go
func TestCreateRunPublic(t *testing.T) {
	// 构造 handler，注入 runDAO/outboxDAO（sqlmock）；AuthMiddleware 已放行 JWT。
	// POST /api/v1/ai/runs → 200 {run_id, status:"created"}
	// 断言 runDAO.Create 被调 + outboxDAO.Insert 被调（同 invocation_id）
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `cd ai-apm-query-go && go test ./internal/api/ -run TestCreateRunPublic -v`
Expected: FAIL。

- [ ] **Step 3: 最小实现**

`CreateRunPublic`：从 JWT context 取 `tenant_id/principal_id/principal_type`（经 RequestAuthorizationContext，参考 `handler.go`/`auth.go` 既有取法）；校验 body tenant/cluster 与 JWT 一致；`service`/`message` 记录 intent；构造 `store.AIRun{Status:"created", StateVersion:0, ...}` 调 `runDAO.Create`（existing 则返回既有，幂等）；生成 `invocationID=uuid`；`outboxDAO.Insert(AIRunOutbox{InvocationID: invocationID, RunID: runID, Status:"pending"})`；返回 JSON。ManualBoundary：查询侧无独立 ManualBoundary，`ai.investigate` capability + JWT 已含人工显式语义；`principal_type != user` 拒绝（403）。

- [ ] **Step 4: 运行测试确认通过**

Run: `cd ai-apm-query-go && go test ./internal/api/ -run TestCreateRunPublic -v`
Expected: PASS。

- [ ] **Step 5: main.go 注册路由 + 提交**

```go
mux.HandleFunc("/api/v1/ai/runs", handler.CreateRunPublic)
mux.HandleFunc("/api/v1/ai/runs/", handler.ListRunsPublic)
```

```bash
cd /Users/mssc/Documents/Code/agent/aiops/ai-apm-query-go
git add internal/api/runs_public.go internal/api/runs_public_test.go internal/api/handler.go cmd/api/main.go
git commit -m "feat(api): public run creation with JWT auth + outbox dispatch"
```

---

### Task A5: outbox dispatcher 可靠派发 RunInvocation 给 orchestrator

**Files:**
- Create: `ai-apm-query-go/internal/api/run_dispatch.go`
- Create: `ai-apm-query-go/internal/api/run_dispatch_test.go`
- Modify: `ai-apm-query-go/cmd/api/main.go`（启动 dispatcher goroutine）

**Interfaces:**
- Consumes: `AIRunOutboxDAO.ScanPending/Claim/Deliver`、`store.GetDB()`、签名 RunInvocationContext（复用 `internal/auth` 签发：orchestrator 侧 `verify_run_invocation_context` 对 `context_type=run_invocation`，issuer=query-api、audience=ai-orchestrator）。
- Produces: `handler.RunDispatchLoop(ctx)` 周期扫描 pending → POST `/internal/v1/run-invocations`（orchestrator URL 来自 env `ORCHESTRATOR_URL`）→ 成功 `Deliver`；失败保留 pending 指数退避（`next_retry_at`）。

- [ ] **Step 1: 写失败测试（扫描 → claim → 派发成功 → deliver）**

```go
func TestRunDispatchDelivers(t *testing.T) {
	// 注入 fake http client（200）+ ScanPending 返回 1 行 → Claim ok → Deliver 被调
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `cd ai-apm-query-go && go test ./internal/api/ -run TestRunDispatchDelivers -v`
Expected: FAIL。

- [ ] **Step 3: 最小实现**

`RunDispatchLoop`：`for { rows := outboxDAO.ScanPending(now); for each: Claim; build RunInvocationContext JWS（issuer=query-api, audience=ai-orchestrator, context_type=run_invocation, tenant_id, cluster_id, principal_type=user, principal_id, invocation_id, nonce）；POST ORCHESTRATOR_URL + "/internal/v1/run-invocations"，header X-Internal-Token + X-Trusted-Request-Context；200→Deliver；非 200/超时→保留 pending，`next_retry_at = now + 2^dispatch_count * 1s`；sleep 1s }`。

- [ ] **Step 4: 运行测试确认通过**

Run: `cd ai-apm-query-go && go test ./internal/api/ -run TestRunDispatch -v`
Expected: PASS。

- [ ] **Step 5: 提交**

```bash
cd /Users/mssc/Documents/Code/agent/aiops/ai-apm-query-go
git add internal/api/run_dispatch.go internal/api/run_dispatch_test.go cmd/api/main.go
git commit -m "feat(api): durable outbox dispatcher for RunInvocation dispatch"
```

---

### Task A6: Plan A 验收 + 全量回归

**Files:**
- Verify: 全部 Plan A 新增/修改文件。

- [ ] **Step 1: 全量 Go 测试**

Run: `cd ai-apm-query-go && go test ./... 2>&1 | tail -30`
Expected: 全 PASS，无 lint（`go vet ./...` ok）。

- [ ] **Step 2: 映射自检**

在 `runs_public.go` 确认 `store.AIRun` 新字段均被 Create 填充；`request_id` 唯一域 `(tenant_id, request_id)`；无 `ON DUPLICATE KEY UPDATE run_id`。

- [ ] **Step 3: 提交验收记录（追加总 Evidence 文档占位）**

```bash
cd /Users/mssc/Documents/Code/agent/aiops
git add docs/V9.2_V9.3_P0_P9_IMPLEMENTATION_EVIDENCE.md
git commit -m "docs: P10 Plan A (run mapping + migration + public create + dispatch) done"
```
