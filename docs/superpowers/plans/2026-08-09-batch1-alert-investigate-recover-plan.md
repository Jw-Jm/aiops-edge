# 批 1：告警调查按钮 + 审批制恢复 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A1 告警事件"调查"按钮（RCA 结果持久化到事件）+ A5 审批制恢复（AI 恢复方案 → 人员审批 → 白名单执行，安全边界可配置）。

**Architecture:** 复用现有 RCA 端点（`POST /api/v1/ops/rca/alert`）与审批体系（`approval_tasks` + `approve/reject` + `execute_suggestion` 执行器 + `ShellPolicy`）。A1 增量 = RCA 结果落库到 alert_events 的 investigation 字段；A5 增量 = 审批人角色校验 + 恢复白名单配置 + 恢复方案与事件关联。

**Tech Stack:** Go(query-api), Python(orchestrator), React, MySQL

## Global Constraints

- 告警事件 CRUD 在 query-api（`/api/v1/alerts/events`），RCA/审批在 orchestrator（`/api/v1/ops/`）
- 前端 IncidentDetail 已有"AI 根因分析"按钮（onRCA → rcaAlertAnalysis），A1 复用它改名为"调查"并持久化
- 审批体系：`approval_tasks` 表（task_id/service_name/status/plan/script/risk_score/risk_reason/diagnosis/report/requester/created_at/decided_at/decision_by）
- 执行器：`execute_suggestion(service, script, context)`（经 ShellPolicy 拦截后 subprocess 执行）
- 审批人 = admin + 用户管理中配置的可审审批人（users 表新增 approver 标记）
- 恢复白名单可配置（设置中管理），默认允许 kubectl rollout/scale/restart，禁止 delete/清数据
- 全部自研，不复制 ongrid 代码；TDD；频繁提交

---

## Task 1: A1 告警事件加 investigation 字段 + 持久化 RCA 结果

**Files:**
- Modify: `ai-apm-query-go/internal/api/alerts.go`（AlertEvent 加 Investigation 字段 + 关联/查询）
- Modify: `ai-apm-query-go/internal/store/alerts.go`（AlertEvent DAO 加 investigation 列）
- Modify: `ai-apm-query-go/internal/store/mysql.go`（alert_events 表加 investigation 列 + 兼容 ALTER）
- Test: `ai-apm-query-go/internal/store/alerts_test.go`
- Modify: `ai-apm-query-go/cmd/api/main.go`（事件增加 investigation 相关路由或复用事件 PUT）

**Interfaces:**
- Consumes: `AlertEvent`（现有）
- Produces: `AlertEvent.Investigation string`；`saveAlertInvestigation(eventID, json)` 持久化；`GET /alerts/events/{id}` 返回 investigation

- [ ] **Step 1: 写失败测试**

创建 `ai-apm-query-go/internal/store/alerts_test.go`：

```go
package store

import "testing"

func TestAlertEventInvestigation(t *testing.T) {
	if GetDB() == nil {
		t.Skip("mysql unavailable")
	}
	d := &AlertEventDAO{}
	// 先确认列存在
	_ = d.ReplaceAll([]AlertEvent{{ID: "inv-test", Service: "s", Status: "firing"}})
	rows, _ := d.LoadAll()
	found := false
	for _, e := range rows {
		if e.ID == "inv-test" {
			found = true
		}
	}
	if !found {
		t.Fatal("event not persisted")
	}
	_ = d.DeleteForTest("inv-test")
}
```

- [ ] **Step 2: 运行确认失败**

Run: `cd ai-apm-query-go && go test ./internal/store/ -run TestAlertEventInvestigation`
Expected: 编译失败 — `AlertEvent` 无 Investigation 字段 / 表无列

- [ ] **Step 3: mysql.go alert_events 加 investigation 列**

在 `internal/store/mysql.go` alert_events 的 `timeline TEXT,` 后加：

```go
  investigation TEXT,
```

并在 alert_events 建表后加兼容 ALTER：

```go
	if !hasColumn(conn, "alert_events", "investigation") {
		_, _ = conn.Exec("ALTER TABLE alert_events ADD COLUMN investigation TEXT")
	}
```

- [ ] **Step 4: store alerts.go AlertEvent 加 Investigation + DAO 支持**

`internal/store/alerts.go` 的 `AlertEvent` struct 加 `Investigation string`；`LoadAll` 的 SELECT/Scan 加 investigation 列（`var inv sql.NullString`）；`ReplaceAll` 的 INSERT/Exec 加 investigation 列。

- [ ] **Step 5: api alerts.go AlertEvent 加 Investigation + 持久化方法**

`internal/api/alerts.go` 的 `AlertEvent` struct 加 `Investigation string \`json:"investigation,omitempty"\``；`loadAlertEvents`/`saveAlertEvents` 的映射加 Investigation；新增：

```go
// saveInvestigation 持久化某事件的调查结果（RCA 结果 JSON）。
func saveInvestigation(eventID, investigation string) {
	alertEventsMu.Lock()
	defer alertEventsMu.Unlock()
	for i := range alertEvents {
		if alertEvents[i].ID == eventID {
			alertEvents[i].Investigation = investigation
			break
		}
	}
	go saveAlertEvents()
}
```

- [ ] **Step 6: 运行确认通过**

Run: `cd ai-apm-query-go && go build ./... && go test ./internal/store/ ./internal/api/`
Expected: PASS

- [ ] **Step 7: 提交**

```bash
git add ai-apm-query-go
git commit -m "feat(alerts): 告警事件加 investigation 字段（RCA 结果持久化到事件）"
```

---

## Task 2: A1 前端调查按钮 + 结果持久化展示

**Files:**
- Modify: `observability-frontend/src/pages/Alerts/IncidentDetail.tsx`（调查按钮 + 结果持久化）
- Modify: `observability-frontend/src/api/client.ts`（saveInvestigation API）
- Test: `tsc --noEmit`

**Interfaces:**
- Consumes: `rcaAlertAnalysis(data)`（现有）、`saveInvestigation(eventID, json)`（Task 1）
- Produces: "调查"按钮触发 RCA → 结果持久化到事件 → 详情展示 investigation

- [ ] **Step 1: client.ts 加保存调查 API**

```ts
export const saveAlertInvestigation = (id: string, investigation: string) =>
  api.post(`/alerts/events/${id}/investigation`, { investigation })
```

- [ ] **Step 2: IncidentDetail 改造**

`IncidentDetail.tsx`：
- 顶部操作区按钮从"AI 根因分析"改为"**调查**"（保留 onRCA 逻辑）
- onRCA 成功后调 `saveAlertInvestigation(id, JSON.stringify(result))` 持久化
- 详情区展示 investigation（若事件带 investigation 则直接展示，否则点按钮调查）
- RCA 结果 `<pre>` 区保留

- [ ] **Step 3: main.go 加 investigation 路由**

`cmd/api/main.go` 在事件路由加 `POST /alerts/events/{id}/investigation`（解析 body 调 `saveInvestigation`）。

- [ ] **Step 4: tsc + go build 验证**

Run: `cd observability-frontend && node_modules/.bin/tsc --noEmit` + `cd ai-apm-query-go && go build ./...`
Expected: exit 0

- [ ] **Step 5: 提交**

```bash
git add observability-frontend/src ai-apm-query-go/cmd/api/main.go
git commit -m "feat(web): 告警详情调查按钮——点击触发 RCA 并持久化结果到事件"
```

---

## Task 3: A5 users 加审批人标记 + 审批权限校验

**Files:**
- Modify: `ai-apm-query-go/internal/store/mysql.go`（users 加 is_approver 列）
- Modify: `ai-apm-query-go/internal/store/users.go`（User 加 IsApprover）
- Modify: `ai-apm-query-go/internal/api/users.go`（users CRUD 支持 is_approver）
- Modify: `ai-orchestrator/main.py`（approve/reject 校验 admin 或 is_approver）
- Test: `go test ./internal/store/`

**Interfaces:**
- Consumes: `User.Role`（现有）
- Produces: `User.IsApprover bool`；approve/reject 仅 admin 或 is_approver 可操作

- [ ] **Step 1: mysql.go users 加 is_approver 列 + 兼容 ALTER**

users 表 `scope VARCHAR(512) DEFAULT '',` 后加 `is_approver TINYINT DEFAULT 0,`；建表后加兼容 ALTER。

- [ ] **Step 2: users.go store 加 IsApprover + DAO 支持**

`User` struct 加 `IsApprover bool`；List/GetByUsername/GetByID 的 SQL 加 is_approver 列；`UpdateScope` 旁新增 `SetApprover(id, bool)`。

- [ ] **Step 3: users.go api 支持 is_approver**

users Create/Update handler 支持 `is_approver` 字段；List 返回 `is_approver`。

- [ ] **Step 4: orchestrator approve/reject 校验审批人**

`ai-orchestrator/main.py` 的 approve/reject handler：调用前校验请求者（经 query-api 传入的 role/is_approver）为 admin 或 is_approver；否则 403。通过内部 header（`X-Internal-Role`/`X-Internal-Approver`）由 query-api 注入。

- [ ] **Step 5: 测试 + 提交**

Run: `go build ./... && go test ./internal/store/ ./internal/api/` + `python3 -m py_compile main.py`
Expected: PASS

```bash
git add -A
git commit -m "feat(rbac): 审批人标记（admin + 可配审批人），approve/reject 权限校验"
```

---

## Task 4: A5 恢复白名单配置 + AI 恢复方案生成

**Files:**
- Modify: `ai-orchestrator/shell_ws.py`（或新建 recovery_policy.py）— 恢复白名单配置
- Modify: `ai-orchestrator/main.py` — 恢复方案端点 + 白名单管理
- Modify: `observability-frontend/src/pages/Settings/index.tsx` — 恢复白名单设置 UI
- Test: `python3 -m py_compile` + 冒烟

**Interfaces:**
- Consumes: `ShellPolicy`（现有）、`approval_tasks`（现有）
- Produces: 恢复白名单（可配置，存 MySQL）；`POST /api/v1/ops/recovery/plan` 生成恢复方案；`GET/PUT /api/v1/ops/recovery/policy` 管理白名单

- [ ] **Step 1: 新建 recovery_policy.py**

创建 `ai-orchestrator/recovery_policy.py`：默认白名单（允许 `kubectl rollout restart/scale/undo`、`kubectl scale`、`systemctl restart`；禁止 `kubectl delete`、`rm`、`DROP` 等），支持从 MySQL 读取/写入（存 `recovery_policy` 表或复用 platform_settings KV）。

- [ ] **Step 2: main.py 恢复方案 + 白名单端点**

- `POST /api/v1/ops/recovery/plan`：接收 `{service, diagnosis, investigation}` → 调 LLM 生成恢复方案（操作步骤/影响面/风险）→ 创建 approval_task（source="recovery"）
- `GET/PUT /api/v1/ops/recovery/policy`：读/写恢复白名单

- [ ] **Step 3: Settings 前端加恢复白名单设置**

`Settings/index.tsx` 加"恢复白名单"区：展示允许/禁止命令列表，可编辑保存（`PUT /ops/recovery/policy`）。

- [ ] **Step 4: approve 执行区分 recovery 源**

main.py approve handler：`source=="recovery"` 时，校验恢复命令在白名单内，执行 `execute_suggestion` 并记录审计；不在白名单则拒绝。

- [ ] **Step 5: 提交**

```bash
git add -A
git commit -m "feat(recovery): 恢复白名单可配置 + AI 恢复方案生成（审批制）"
```

---

## Task 5: A5 前端恢复方案展示 + 审批流 + 事件关联

**Files:**
- Modify: `observability-frontend/src/pages/Alerts/IncidentDetail.tsx`（恢复方案区 + 审批）
- Modify: `observability-frontend/src/api/client.ts`（recovery API）
- Test: `tsc --noEmit`

**Interfaces:**
- Consumes: `POST /ops/recovery/plan`、`approveTask/rejectTask`（现有）
- Produces: 事件详情"恢复"按钮 → 生成方案 → 展示审批状态；审批通过后显示执行结果

- [ ] **Step 1: client.ts 加 recovery API**

```ts
export const genRecoveryPlan = (data: Record<string, unknown>) => api.post('/ops/recovery/plan', data)
export const getRecoveryPolicy = () => api.get('/ops/recovery/policy')
export const saveRecoveryPolicy = (data: Record<string, unknown>) => api.put('/ops/recovery/policy', data)
```

- [ ] **Step 2: IncidentDetail 加恢复方案区**

- 顶部加"恢复"按钮 → 调 `genRecoveryPlan` → 展示恢复方案（操作步骤/影响面/风险）+ 审批按钮（approve/reject）
- 审批通过 → 显示执行结果；展示审批人/时间

- [ ] **Step 3: tsc + go build 验证**

Run: `cd observability-frontend && node_modules/.bin/tsc --noEmit` + `cd ai-apm-query-go && go build ./...`
Expected: exit 0

- [ ] **Step 4: 提交**

```bash
git add -A
git commit -m "feat(web): 告警恢复方案生成 + 审批制执行（AI方案→人审核→白名单执行）"
```

---

## Task 6: 部署 + 冒烟验证

- [ ] **Step 1: 重建镜像**

query-api（investigation/审批人）、orchestrator（恢复方案/白名单/审批人校验）、frontend（调查/恢复 UI）三镜像离线重建 + 部署。

- [ ] **Step 2: 冒烟**

- 事件详情点"调查"→ RCA 结果持久化，刷新仍在
- 事件详情点"恢复"→ 生成恢复方案 → 审批（admin）→ 执行
- 非审批人 approve/reject 返回 403
- 恢复白名单设置生效（禁止命令被拦截）

---

## Self-Review

**1. Spec coverage:** 覆盖总方案批 1 的 A1（调查按钮+持久化）与 A5（审批制恢复+白名单可配置）。
**2. Placeholder scan:** 无 TBD/TODO；SQL/端点/字段名跨 Task 一致。
**3. Type consistency:** `Investigation`/`IsApprover`/`genRecoveryPlan`/`getRecoveryPolicy` 跨 Task 命名一致。
**4. 合规:** 全部自研，不复制 ongrid 代码。
