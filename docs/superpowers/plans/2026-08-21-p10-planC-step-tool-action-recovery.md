# Plan C — Plan/Step/Tool/Action 持久化与重启恢复（P10 闭环 3/4）

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 补齐 Plan/Step/Tool/Action/ControlCommand 持久化结构（0003 追加迁移），实现 AIPlanStepDAO/AIToolRunDAO/AIActionDAO/AIControlCommandDAO + 恢复端点（一致性快照），并验证真实进程重启不重复 Tool/Action。

**Architecture:** query-api 是持久化 owner；恢复端点以统一快照事务读取 Run + Plan/Step + ToolRun + Action + ControlCommand；orchestrator 重启后经 `/internal/v1/control-plane/runs/unfinished` + 恢复端点重建 runtime state。

**Tech Stack:** Go（query-go：store + internal/api）、MySQL（migrations + 真实集成测试）。

**设计依据：** `docs/superpowers/specs/2026-08-21-p10-full-closed-loop-design.md` §R3 + §5.1。

## Global Constraints

- `ai_plan_steps` 补 `depends_on`（JSON 数组）/`parameters`（JSON）/`attempt`/`outcome`/`result_ref`。
- `ai_tool_runs` 补 `idempotency_key` + `UNIQUE(run_id, idempotency_key)`。
- `ai_actions` 补 `UNIQUE(run_id, idempotency_key)`。
- 新增 `ai_control_commands` 表（command_id PK、run_id、operation、payload_json、status、idempotency_key、created_at）。
- 恢复读取走一致性快照事务（Run + Step + Tool + Action 同一快照），证明重启不重复 Tool/Action。
- 真实 MySQL 集成测试证明进程销毁后持久性；sqlmock 仅用于 DAO 单测。
- orchestrator 不直连 DB；红线 F1-F5 保持。

---

### Task C1: 0003b 追加迁移（step/tool/action/control command 结构）

**Files:**
- Create: `ai-apm-query-go/internal/store/migrations/versions/0003b_ai_runtime_recovery.sql`
- Modify: `ai-apm-query-go/internal/store/migrations/`（manifest/coverage 登记）

**Interfaces:**
- Produces: `ai_plan_steps` 增列、`ai_tool_runs.idempotency_key` + UNIQUE、`ai_actions` UNIQUE、`ai_control_commands` 表。

- [ ] **Step 1: 写迁移 SQL**

```sql
-- 0003b-ai-runtime-recovery
-- P10 Plan C：Plan/Step/Tool/Action 恢复结构。

ALTER TABLE ai_plan_steps
  ADD COLUMN depends_on JSON NULL,
  ADD COLUMN parameters JSON NULL,
  ADD COLUMN attempt INT NOT NULL DEFAULT 0,
  ADD COLUMN outcome VARCHAR(32) NULL,
  ADD COLUMN result_ref VARCHAR(512) NULL;
-- statement-breakpoint

ALTER TABLE ai_tool_runs
  ADD COLUMN idempotency_key VARCHAR(255) NOT NULL DEFAULT '',
  ADD UNIQUE KEY uq_ai_tool_runs_idem (run_id, idempotency_key);
-- statement-breakpoint

ALTER TABLE ai_actions
  ADD UNIQUE KEY uq_ai_actions_idem (run_id, idempotency_key);
-- statement-breakpoint

CREATE TABLE IF NOT EXISTS ai_control_commands (
  command_id CHAR(36) PRIMARY KEY,
  run_id CHAR(36) NOT NULL,
  operation VARCHAR(64) NOT NULL,
  payload_json JSON NULL,
  status VARCHAR(16) NOT NULL DEFAULT 'pending',
  idempotency_key VARCHAR(255) NOT NULL DEFAULT '',
  created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  UNIQUE KEY uq_ai_control_commands_idem (run_id, idempotency_key),
  INDEX idx_ai_control_commands_run (run_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
```

- [ ] **Step 2: schema manifest/coverage 校验**

Run: `cd ai-apm-query-go && go test ./internal/store/migrations/ -v`
Expected: PASS（登记新 migration）。

- [ ] **Step 3: 提交**

```bash
cd /Users/mssc/Documents/Code/agent/aiops/ai-apm-query-go
git add internal/store/migrations/
git commit -m "feat(migrations): 0003b recovery structure (plan/tool/action/control-command)"
```

---

### Task C2: AIPlanStepDAO

**Files:**
- Create: `ai-apm-query-go/internal/store/ai_plan_steps.go`
- Create: `ai-apm-query-go/internal/store/ai_plan_steps_test.go`

**Interfaces:**
- Consumes: `GetDB()/SetDB()`、`0003b` 迁移。
- Produces: `AIPlanStep{StepID, RunID, ParentStepID, Seq, StepType, Status, ClusterID, Description, BudgetUsed, DependsOn, Parameters, Attempt, Outcome, ResultRef, StartedAt, CompletedAt, CreatedAt, UpdatedAt}`；`AIPlanStepDAO{Create, Update, ListByRun}`。

- [ ] **Step 1: 写失败测试（Create + ListByRun + Update 幂等）**

```go
func TestAIPlanStepCreateAndListByRun(t *testing.T) { ... }
```

- [ ] **Step 2: 运行测试确认失败**

Run: `cd ai-apm-query-go && go test ./internal/store/ -run TestAIPlanStep -v`
Expected: FAIL。

- [ ] **Step 3: 最小实现**

`Create`：`INSERT INTO ai_plan_steps (step_id, run_id, parent_step_id, seq, step_type, status, cluster_id, description, budget_used, depends_on, parameters, attempt, outcome, result_ref, started_at, completed_at, created_at, updated_at) VALUES (...)`（depends_on/parameters 序列化 JSON）。`Update`：`UPDATE ... SET status=?, outcome=?, attempt=?, result_ref=?, completed_at=? WHERE step_id=?`。`ListByRun`：`SELECT ... WHERE run_id=? ORDER BY seq`。

- [ ] **Step 4: 运行测试确认通过**

Run: `cd ai-apm-query-go && go test ./internal/store/ -run TestAIPlanStep -v`
Expected: PASS。

- [ ] **Step 5: 提交**

```bash
cd /Users/mssc/Documents/Code/agent/aiops/ai-apm-query-go
git add internal/store/ai_plan_steps.go internal/store/ai_plan_steps_test.go
git commit -m "feat(store): ai_plan_steps DAO (DAG + runtime state)"
```

---

### Task C3: AIToolRunDAO + AIActionDAO

**Files:**
- Create: `ai-apm-query-go/internal/store/ai_tool_runs.go`
- Create: `ai-apm-query-go/internal/store/ai_tool_runs_test.go`
- Create: `ai-apm-query-go/internal/store/ai_actions.go`
- Create: `ai-apm-query-go/internal/store/ai_actions_test.go`

**Interfaces:**
- Consumes: `0003b` 迁移（idempotency_key UNIQUE）。
- Produces: `AIToolRun{...}`/`AIToolRunDAO{Create, UpdateByIdemKey}`；`AIAction{...}`/`AIActionDAO{Create, UpdateByIdemKey}`。两者 `Create` 幂等（重复 idempotency_key → existing）。

- [ ] **Step 1: 写失败测试（幂等 key）**

```go
func TestAIToolRunCreateIdempotentByKey(t *testing.T) {
	// 同 (run_id, idempotency_key) 重复 → existing(!ok)
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `cd ai-apm-query-go && go test ./internal/store/ -run TestAIToolRun -v`
Expected: FAIL。

- [ ] **Step 3: 最小实现**

`AIToolRunDAO.Create`：`INSERT INTO ai_tool_runs (tool_run_id, run_id, step_id, tenant_id, cluster_id, tool_name, status, input_json, result_json, error_code, error_message, duration_ms, started_at, completed_at, created_at, idempotency_key) VALUES (...)`；重复键（1062）返回 `(false, nil)`。`AIActionDAO.Create` 类似（含 action_hash/authoritative_risk/dry_run/params_json）。`UpdateByIdemKey` 更新 status/result。

- [ ] **Step 4: 运行测试确认通过**

Run: `cd ai-apm-query-go && go test ./internal/store/ -run "TestAIToolRun|TestAIAction" -v`
Expected: PASS。

- [ ] **Step 5: 提交**

```bash
cd /Users/mssc/Documents/Code/agent/aiops/ai-apm-query-go
git add internal/store/ai_tool_runs.go internal/store/ai_tool_runs_test.go internal/store/ai_actions.go internal/store/ai_actions_test.go
git commit -m "feat(store): ai_tool_runs + ai_actions DAOs (idempotency key)"
```

---

### Task C4: AIControlCommandDAO

**Files:**
- Create: `ai-apm-query-go/internal/store/ai_control_commands.go`
- Create: `ai-apm-query-go/internal/store/ai_control_commands_test.go`

**Interfaces:**
- Produces: `AIControlCommand{CommandID, RunID, Operation, Payload, Status, IdempotencyKey, CreatedAt}`；`AIControlCommandDAO{Create, Get}`。

- [ ] **Step 1: 写失败测试（幂等 key）**

```go
func TestAIControlCommandCreateIdempotent(t *testing.T) { ... }
```

- [ ] **Step 2: 运行测试确认失败**

Run: `cd ai-apm-query-go && go test ./internal/store/ -run TestAIControlCommand -v`
Expected: FAIL。

- [ ] **Step 3: 最小实现**

`Create`：`INSERT INTO ai_control_commands (command_id, run_id, operation, payload_json, status, idempotency_key, created_at) VALUES (...)`；重复键返回 existing。`Get` 按 command_id。

- [ ] **Step 4: 运行测试确认通过**

Run: `cd ai-apm-query-go && go test ./internal/store/ -run TestAIControlCommand -v`
Expected: PASS。

- [ ] **Step 5: 提交**

```bash
cd /Users/mssc/Documents/Code/agent/aiops/ai-apm-query-go
git add internal/store/ai_control_commands.go internal/store/ai_control_commands_test.go
git commit -m "feat(store): ai_control_commands DAO (command idempotency)"
```

---

### Task C5: control-plane 恢复端点（一致性快照）

**Files:**
- Create: `ai-apm-query-go/internal/api/control_plane_recovery.go`
- Create: `ai-apm-query-go/internal/api/control_plane_recovery_test.go`
- Modify: `ai-apm-query-go/internal/api/handler.go`（Handler 加 planDAO/toolDAO/actionDAO/cmdDAO）
- Modify: `ai-apm-query-go/cmd/api/main.go`

**Interfaces:**
- Consumes: C2/C3/C4 DAO + `AIRunDAO.ScanUnfinished`。
- Produces: `InternalControlPlaneRecovery`（capability=`control-plane.runs.recover`）：`GET /internal/v1/control-plane/recovery/snapshot?run_id=` 返回 {run, plan_steps[], tool_runs[], actions[], control_commands[], last_event_sequence} 一致性快照。

- [ ] **Step 1: 写失败测试（快照事务一致性）**

```go
func TestControlPlaneRecoverySnapshot(t *testing.T) {
	// GET .../recovery/snapshot?run_id=x → 200 {run, plan_steps, tool_runs, actions, control_commands, last_event_sequence}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `cd ai-apm-query-go && go test ./internal/api/ -run TestControlPlaneRecovery -v`
Expected: FAIL。

- [ ] **Step 3: 最小实现**

`InternalControlPlaneRecovery`：`authorizeInternalControlPlane(r, "control-plane.runs.recover", ...)`；在同一 DB 事务内读取 run + `planDAO.ListByRun` + `toolDAO.ListByRun` + `actionDAO.ListByRun` + `cmdDAO.ListByRun` + `eventDAO.LastSequence`，打包返回。

- [ ] **Step 4: 运行测试确认通过**

Run: `cd ai-apm-query-go && go test ./internal/api/ -run TestControlPlaneRecovery -v`
Expected: PASS。

- [ ] **Step 5: 提交**

```bash
cd /Users/mssc/Documents/Code/agent/aiops/ai-apm-query-go
git add internal/api/control_plane_recovery.go internal/api/control_plane_recovery_test.go internal/api/handler.go cmd/api/main.go
git commit -m "feat(api): control-plane recovery snapshot (consistent tx)"
```

---

### Task C6: 真实 MySQL + 进程重启集成测试（评审 P1-3）

**Files:**
- Create: `ai-apm-query-go/internal/api/recovery_integration_test.go`（`//go:build integration`）

**Interfaces:**
- Consumes: 真实 MySQL（env `TEST_MYSQL_DSN`）、`0002/0003/0003b` 迁移、`AIRunDAO`+`AIRunEventDAO`+C2/C3/C4 DAO。
- Produces: 证明进程销毁后 Run/Event/Plan/Tool/Action 持久化 + ScanUnfinished 恢复 + 不重复 Tool/Action。

- [ ] **Step 1: 写集成测试**

```go
//go:build integration
func TestProcessRestartRecoveryIntegration(t *testing.T) {
	// 1. 连接真实 MySQL，跑迁移
	// 2. Create Run（state_version=0）→ transition to planning → append event → insert plan_step + tool_run
	// 3. 模拟进程销毁：新建 DAO 实例（新连接，等价重启）
	// 4. ScanUnfinished → 找到 planning Run
	// 5. 用 recovery snapshot 恢复 → 断言 plan_step/tool_run 均存在
	// 6. 再次执行同一 tool_run（同 idempotency_key）→ 返回 existing，不重复执行
}
```

- [ ] **Step 2: 运行集成测试（需真实 MySQL）**

Run: `cd ai-apm-query-go && TEST_MYSQL_DSN="user:pass@tcp(127.0.0.1:3306)/aiops?parseTime=true" go test -tags integration ./internal/api/ -run TestProcessRestartRecoveryIntegration -v`
Expected: PASS（若本机无 MySQL 或 DSN 不可达，记录环境限制，属后续真实环境 Integration Gate）。

- [ ] **Step 3: 提交**

```bash
cd /Users/mssc/Documents/Code/agent/aiops/ai-apm-query-go
git add internal/api/recovery_integration_test.go
git commit -m "test(integration): real MySQL process-restart recovery"
```

---

### Task C7: Plan C 验收 + 全量回归

- [ ] **Step 1: Go 全量测试**

Run: `cd ai-apm-query-go && go test ./... 2>&1 | tail -30`
Expected: 全 PASS（integration 标签除外，默认跳过）。

- [ ] **Step 2: 提交验收**

```bash
cd /Users/mssc/Documents/Code/agent/aiops
git add docs/V9.2_V9.3_P0_P9_IMPLEMENTATION_EVIDENCE.md
git commit -m "docs: P10 Plan C (step/tool/action recovery + process restart) done"
```
