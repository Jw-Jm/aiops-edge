# Phase B · 告警 incident 状态机 + IncidentDetail Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 为告警事件引入 incident 状态机（firing → acknowledged → resolved + 事件时间线），并提供 IncidentDetail 详情页（复用 RCA）。这是 Phase B 的告警地基（IncidentDetail 依赖它）。

**Architecture:** 后端 query-api `alerts.go`：`AlertEvent` 加状态字段 + 内存存储（`alertEvents` 已有 + 文件持久化）；新增 ack/resolve/详情接口。前端新增 `/alerts/incidents/:id` 详情页（状态时间线 + RCA 分析按钮复用 `rcaAlertAnalysis`）。

**Tech Stack:** Go1.24 / AntD5 / React18；`AlertEvent` 现为 `[]AlertEvent` + `alertEventsMu sync.RWMutex` + JSON 文件持久化（`ALERT_EVENTS_FILE`，默认 `/tmp/observability-alert-events.json`）。

## Global Constraints

- 后端：`ai-apm-query-go/internal/api/alerts.go`（`package api`）、路由在 `cmd/api/main.go`。
- **存储不变**：仍用内存 `alertEvents` + JSON 文件（不引入 MySQL，保持增量）。
- `AlertEvent` 现有字段：ID/RuleID/RuleName/Service/Severity/Message/Value/Threshold/Timestamp/Count/FirstTimestamp/LastTimestamp。
- 状态字段命名：`Status`（`firing`/`acknowledged`/`resolved`）、`AcknowledgedAt`、`AcknowledgedBy`、`ResolvedAt`、`ResolvedBy`。
- 合规：状态机为通用告警模式（AlertManager 类似），独立实现，不复刻 ongrid 代码。
- 基线：`github.com/Jw-Jm/aiops-edge` main=`459cc05`，每任务提交。

---

## Task 1: AlertEvent 增加状态字段（TDD）

**Files:**
- Modify: `aiops/ai-apm-query-go/internal/api/alerts.go`
- Test: `aiops/ai-apm-query-go/internal/api/alerts_test.go`（新建）

**Interfaces:**
- Consumes: `AlertEvent` struct（line 36-49）。
- Produces: 状态字段 + 辅助函数 `transitionStatus(ev *AlertEvent, to, by string) bool`（合法迁移：firing→acknowledged→resolved，可 firing→resolved；非法迁移返回 false）。

- [ ] **Step 1: 写失败测试**

```go
// aiops/ai-apm-query-go/internal/api/alerts_test.go
package api

import "testing"

func TestTransitionStatus_FiringToAck(t *testing.T) {
	ev := &AlertEvent{Status: "firing"}
	if !transitionStatus(ev, "acknowledged", "admin") {
		t.Fatal("expected firing->acknowledged allowed")
	}
	if ev.Status != "acknowledged" || ev.AcknowledgedBy != "admin" {
		t.Fatalf("status=%s by=%s", ev.Status, ev.AcknowledgedBy)
	}
}

func TestTransitionStatus_AckToResolved(t *testing.T) {
	ev := &AlertEvent{Status: "acknowledged"}
	if !transitionStatus(ev, "resolved", "admin") {
		t.Fatal("expected acknowledged->resolved allowed")
	}
	if ev.Status != "resolved" || ev.ResolvedBy != "admin" {
		t.Fatalf("status=%s by=%s", ev.Status, ev.ResolvedBy)
	}
}

func TestTransitionStatus_FiringToResolved(t *testing.T) {
	ev := &AlertEvent{Status: "firing"}
	if !transitionStatus(ev, "resolved", "admin") {
		t.Fatal("expected firing->resolved allowed")
	}
}

func TestTransitionStatus_Illegal(t *testing.T) {
	ev := &AlertEvent{Status: "resolved"}
	if transitionStatus(ev, "acknowledged", "admin") {
		t.Fatal("resolved->acknowledged should be illegal")
	}
	if transitionStatus(ev, "firing", "admin") {
		t.Fatal("resolved->firing should be illegal")
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `cd aiops/ai-apm-query-go && go test ./internal/api/ -run TestTransitionStatus -v`
Expected: FAIL（`transitionStatus`/`Status` 字段未定义）。

- [ ] **Step 3: 实现**

在 `AlertEvent` struct 加字段：
```go
	Status           string `json:"status"`              // firing/acknowledged/resolved
	AcknowledgedAt   string `json:"acknowledged_at,omitempty"`
	AcknowledgedBy   string `json:"acknowledged_by,omitempty"`
	ResolvedAt       string `json:"resolved_at,omitempty"`
	ResolvedBy       string `json:"resolved_by,omitempty"`
```
加辅助函数（文件末尾）：
```go
// transitionStatus 执行告警状态迁移；非法迁移返回 false 且不修改。
func transitionStatus(ev *AlertEvent, to, by string) bool {
	now := time.Now().Format(time.RFC3339)
	switch to {
	case "acknowledged":
		if ev.Status != "firing" {
			return false
		}
		ev.Status = to; ev.AcknowledgedAt = now; ev.AcknowledgedBy = by
	case "resolved":
		if ev.Status == "resolved" {
			return false
		}
		ev.Status = to; ev.ResolvedAt = now; ev.ResolvedBy = by
	default:
		return false
	}
	return true
}
```
新建事件时默认 `Status: "firing"`（`evaluateAlerts` 内 append event 处，line ~635 前加）。

- [ ] **Step 4: 运行测试确认通过**

Run: `cd aiops/ai-apm-query-go && go test ./internal/api/ -run TestTransitionStatus -v`
Expected: PASS（4 tests）。

- [ ] **Step 5: 提交**

```bash
cd /Users/mssc/Documents/Code/agent/aiops
git add ai-apm-query-go/internal/api/alerts.go ai-apm-query-go/internal/api/alerts_test.go
git commit -m "feat(query-api): alert incident status fields + transition (TDD)"
```

---

## Task 2: ack / resolve / 详情接口

**Files:**
- Modify: `aiops/ai-apm-query-go/internal/api/alerts.go`
- Modify: `aiops/ai-apm-query-go/cmd/api/main.go`

**Interfaces:**
- Consumes: `transitionStatus`（Task 1）、`alertEvents`/`alertEventsMu`。
- Produces:
  - `POST /api/v1/alerts/events/{id}/ack` → 置 acknowledged
  - `POST /api/v1/alerts/events/{id}/resolve` → 置 resolved
  - `GET /api/v1/alerts/events/{id}` → 单个事件详情（含状态）

- [ ] **Step 1: 实现三个 handler**

```go
// 在 AlertEvents 后新增
func (h *Handler) AlertEventAck(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	by := extractTenantID(r) // 或 r.Header 用户名；本机用 "admin"
	alertEventsMu.Lock()
	defer alertEventsMu.Unlock()
	for i := range alertEvents {
		if alertEvents[i].ID == id {
			if !transitionStatus(&alertEvents[i], "acknowledged", by) {
				respondError(w, http.StatusConflict, "cannot acknowledge from current status")
				return
			}
			saveAlertEvents()
			respondJSON(w, http.StatusOK, alertEvents[i])
			return
		}
	}
	respondError(w, http.StatusNotFound, "event not found")
}

func (h *Handler) AlertEventResolve(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	by := extractTenantID(r)
	alertEventsMu.Lock()
	defer alertEventsMu.Unlock()
	for i := range alertEvents {
		if alertEvents[i].ID == id {
			if !transitionStatus(&alertEvents[i], "resolved", by) {
				respondError(w, http.StatusConflict, "cannot resolve from current status")
				return
			}
			saveAlertEvents()
			respondJSON(w, http.StatusOK, alertEvents[i])
			return
		}
	}
	respondError(w, http.StatusNotFound, "event not found")
}

func (h *Handler) AlertEventByID(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	alertEventsMu.RLock()
	defer alertEventsMu.RUnlock()
	for _, ev := range alertEvents {
		if ev.ID == id {
			respondJSON(w, http.StatusOK, ev)
			return
		}
	}
	respondError(w, http.StatusNotFound, "event not found")
}
```
> 注：若项目 Go 版本路由不支持 `r.PathValue`（Go 1.22+ 支持），改为用 `strings.TrimPrefix(r.URL.Path, "/api/v1/alerts/events/")` 提取 id。需确认 main.go 用 `http.ServeMux` 及 Go 版本（go.mod 为 1.24，支持 PathValue）。

- [ ] **Step 2: main.go 注册路由**

```go
mux.HandleFunc("GET /api/v1/alerts/events/{id}", handler.AlertEventByID)
mux.HandleFunc("POST /api/v1/alerts/events/{id}/ack", handler.AlertEventAck)
mux.HandleFunc("POST /api/v1/alerts/events/{id}/resolve", handler.AlertEventResolve)
```
> 需确认现有 main.go 路由是否已用带 method 的 `ServeMux` 语法（Go 1.22+）。若现有全为 `mux.HandleFunc("/path", ...)`（无 method），则 `{id}` 路径参数语法仍可用，但 method 限定需确认。为兼容，用不带 method 的路由 + 在 handler 内判断 `r.Method`。

- [ ] **Step 3: 编译 + 冒烟**

Run: `cd aiops/ai-apm-query-go && go build ./...`
Expected: 通过。

- [ ] **Step 4: 提交**

```bash
cd /Users/mssc/Documents/Code/agent/aiops
git add ai-apm-query-go/internal/api/alerts.go ai-apm-query-go/cmd/api/main.go
git commit -m "feat(query-api): alert ack/resolve/detail endpoints"
```

---

## Task 3: 前端 IncidentDetail 页

**Files:**
- Create: `aiops/observability-frontend/src/pages/Alerts/IncidentDetail.tsx`
- Modify: `aiops/observability-frontend/src/pages/Alerts/index.tsx`（行点击跳详情）
- Modify: `aiops/observability-frontend/src/App.tsx`（注册 `/alerts/incidents/:id`）

**Interfaces:**
- Consumes: `getAlertEventByID`/`ackAlertEvent`/`resolveAlertEvent`（前端 api 新增）、`rcaAlertAnalysis`（已有）。
- Produces: `/alerts/incidents/:id` 详情页：状态徽章 + 字段 + 时间线（firing→ack→resolved）+ Ack/Resolve 按钮 + "AI 根因分析"按钮（复用 rcaAlertAnalysis）。

- [ ] **Step 1: 前端 api 层加 3 个方法**

在 `src/api/client.ts` 加：
```ts
export const getAlertEventByID = (id: string) => api.get(`/alerts/events/${id}`)
export const ackAlertEvent = (id: string) => api.post(`/alerts/events/${id}/ack`)
export const resolveAlertEvent = (id: string) => api.post(`/alerts/events/${id}/resolve`)
```
> 确认 client.ts 的 `api.post` 是否存在（现有登录用 axios.post）。若无，用 `api.request({method:'POST', url})`。

- [ ] **Step 2: 创建 IncidentDetail.tsx**

```tsx
// src/pages/Alerts/IncidentDetail.tsx
import React, { useEffect, useState } from 'react'
import { useParams, useNavigate } from 'react-router-dom'
import { Card, Descriptions, Tag, Button, Spin, Timeline, Space, message } from 'antd'
import { getAlertEventByID, ackAlertEvent, resolveAlertEvent, rcaAlertAnalysis } from '../../api/client'
import { fmtLocalTime } from '../../utils/date'

const STATUS_COLOR: Record<string, string> = { firing: 'red', acknowledged: 'orange', resolved: 'green' }

const IncidentDetail: React.FC = () => {
  const { id } = useParams()
  const navigate = useNavigate()
  const [ev, setEv] = useState<any>(null)
  const [rca, setRca] = useState('')
  const [loading, setLoading] = useState(true)

  const load = async () => {
    setLoading(true)
    try {
      const r = await getAlertEventByID(id!)
      setEv(r?.data)
    } catch { message.error('加载告警失败') } finally { setLoading(false) }
  }
  useEffect(() => { load() }, [id])

  const onAck = async () => { await ackAlertEvent(id!); message.success('已确认'); load() }
  const onResolve = async () => { await resolveAlertEvent(id!); message.success('已解决'); load() }
  const onRCA = async () => { setRca('分析中...'); const r = await rcaAlertAnalysis(ev?.service); setRca(r?.data?.analysis || '无分析结果') }

  if (loading) return <Spin />
  if (!ev) return <div>未找到告警</div>
  return (
    <Card title={`告警详情 · ${ev.rule_name}`} style={{ background: 'var(--surface)', borderColor: 'var(--border)', borderRadius: 10 }}>
      <Space style={{ marginBottom: 16 }}>
        <Tag color={STATUS_COLOR[ev.status] || 'blue'}>{ev.status}</Tag>
        <Button size="small" onClick={onAck} disabled={ev.status !== 'firing'}>确认</Button>
        <Button size="small" onClick={onResolve} disabled={ev.status === 'resolved'}>解决</Button>
        <Button size="small" onClick={onRCA} type="primary">AI 根因分析</Button>
        <Button size="small" onClick={() => navigate('/alerts')}>返回</Button>
      </Space>
      <Descriptions column={2} size="small">
        <Descriptions.Item label="服务">{ev.service}</Descriptions.Item>
        <Descriptions.Item label="严重级别">{ev.severity}</Descriptions.Item>
        <Descriptions.Item label="触发次数">{ev.count}</Descriptions.Item>
        <Descriptions.Item label="首次触发">{fmtLocalTime(ev.first_timestamp)}</Descriptions.Item>
        <Descriptions.Item label="最近触发">{fmtLocalTime(ev.last_timestamp)}</Descriptions.Item>
        <Descriptions.Item label="消息">{ev.message}</Descriptions.Item>
      </Descriptions>
      <Timeline style={{ marginTop: 16 }}>
        <Timeline.Item color="red">firing · {fmtLocalTime(ev.first_timestamp)}</Timeline.Item>
        {ev.acknowledged_at && <Timeline.Item color="orange">acknowledged by {ev.acknowledged_by} · {fmtLocalTime(ev.acknowledged_at)}</Timeline.Item>}
        {ev.resolved_at && <Timeline.Item color="green">resolved by {ev.resolved_by} · {fmtLocalTime(ev.resolved_at)}</Timeline.Item>}
      </Timeline>
      {rca && <Card size="small" style={{ marginTop: 12, background: 'var(--surface-2)' }}><pre style={{ color: 'var(--text)', whiteSpace: 'pre-wrap' }}>{rca}</pre></Card>}
    </Card>
  )
}
export default IncidentDetail
```
> `rcaAlertAnalysis` 签名需确认（Alerts/index.tsx 里用过，传 service 或 event）。若签名不同，按现有用法适配。

- [ ] **Step 3: App.tsx 注册路由**

```tsx
import IncidentDetail from './pages/Alerts/IncidentDetail'
<Route path="/alerts/incidents/:id" element={<IncidentDetail />} />
```

- [ ] **Step 4: Alerts/index.tsx 行点击跳详情**

将告警表格某列（如 rule_name）改为可点击链接跳 `/alerts/incidents/{id}`。

- [ ] **Step 5: 构建验证**

Run: `cd aiops/observability-frontend && npm run build 2>&1 | tail -3`
Expected: 构建成功。

- [ ] **Step 6: 提交**

```bash
cd /Users/mssc/Documents/Code/agent/aiops
git add observability-frontend/src/pages/Alerts/IncidentDetail.tsx observability-frontend/src/pages/Alerts/index.tsx observability-frontend/src/api/client.ts observability-frontend/src/App.tsx
git commit -m "feat(frontend): incident detail page (status timeline + ack/resolve + RCA)"
```

---

## Task 4: 本机部署验证 + 推送

**Files:**
- 无新代码；构建 + 部署 + 验证 + 推送。

**Interfaces:**
- Consumes: Task 1-3。

- [ ] **Step 1: 重建镜像**

Run: `cd /Users/mssc/Documents/Code/agent/aiops && docker build -t query-api:latest ai-apm-query-go && docker tag query-api:latest docker.io/library/query-api:latest && docker build -t observability-frontend:latest observability-frontend && docker tag observability-frontend:latest docker.io/library/observability-frontend:latest`
Expected: 构建成功（arm64）。

- [ ] **Step 2: 升级部署 + 触发滚动更新**

Run: `cd /Users/mssc/Documents/Code/agent/aiops && helm upgrade aiops deploy/helm/aiops --namespace observability --set deepflow.enabled=false --set secrets.jwtSecret="dev-jwt-secret-change-me" --set secrets.internalToken="dev-internal-token" --set secrets.ingestApiKey="dev-ingest-key" --set secrets.clickhousePassword="dev-ch-pass" --set secrets.redisPassword="dev-redis-pass" --set secrets.minioAccessKey="minioadmin" --set secrets.minioSecretKey="minioadmin123" --set secrets.mysqlRootPassword="dev-mysql-pass" && kubectl -n observability rollout restart deploy/query-api deploy/frontend`
Expected: deployed + 滚动更新。

- [ ] **Step 3: 验证 ack/resolve/详情**

Run（带 JWT）：
```bash
JWT=$(curl -s -X POST http://localhost:30253/api/v1/auth/login -H "Content-Type: application/json" -d '{"username":"admin","password":"admin123"}' | grep -o '"token":"[^"]*"' | cut -d'"' -f4)
# 取一个 event id
EID=$(curl -s "http://localhost:30253/api/v1/alerts/events?limit=1" -H "Authorization: Bearer $JWT" | grep -o '"id":"[^"]*"' | head -1 | cut -d'"' -f4)
echo "event=$EID"
# 详情
curl -s "http://localhost:30253/api/v1/alerts/events/$EID" -H "Authorization: Bearer $JWT" | head -c 200
```
Expected: 返回事件含 `status`。
```bash
# ack
curl -s -X POST "http://localhost:30253/api/v1/alerts/events/$EID/ack" -H "Authorization: Bearer $JWT" | head -c 200
```
Expected: 返回 `status: acknowledged`。

- [ ] **Step 4: 验证前端页**

Run: `curl -s -o /dev/null -w "%{http_code}\n" http://localhost:30253/alerts`
Expected: 200（Alerts 页）。
（IncidentDetail 页需浏览器点击进入，验证路由注册与构建。）

- [ ] **Step 5: 提交验证通过（如有修复）**

```bash
cd /Users/mssc/Documents/Code/agent/aiops
git add -A
git commit -m "fix: deployment verification fixes" || echo "无改动"
```

- [ ] **Step 6: 推送**

```bash
cd /Users/mssc/Documents/Code/agent/aiops
git push origin main
```
Expected: 推送成功。
