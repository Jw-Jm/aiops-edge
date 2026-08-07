# Phase A · 布局壳重构 + Monitor + Dashboard Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Phase A 三项：① 布局壳重构（zinc 色板 + zustand + CommandPalette + AgentSidePanel + Sidebar 8 区段）② Monitor 面板页（复用 PromQL 端点）③ Dashboard（后端 `/dashboard/stats` 聚合接口 + Overview 升级）。

**Architecture:** 纯前端（布局壳/Monitor）+ 后端（`/dashboard/stats` 聚合接口，Go TDD）。布局壳是页面骨架，先做；Monitor/Dashboard 页面依赖它承载。

**Tech Stack:** React18 / Vite6 / AntD5 / react-router6 / axios；Go1.24（query-api）；VictoriaMetrics（PromQL 已有 `/metrics/query_range` 端点）。

## Global Constraints

- 前端：`observability-frontend/`，App.tsx 布局壳（AntD Layout+Sider+Menu+Header）、Overview=`pages/Overview/index.tsx`、api 层=`src/api/client.ts`、**components 空**、无 zustand、无 CommandPalette/AgentSidePanel。
- 后端：`ai-apm-query-go/cmd/api/main.go`（路由）、`internal/api/handler.go`（`package api`）、`internal/biz/`（聚合逻辑）。
- **合规**：布局/Monitor/Dashboard 从"功能需求"独立实现，不复刻 ongrid 代码；PromQL 用 VM 官方 API 契约。
- PromQL 数据源：`/api/v1/metrics/query_range`（已实现）+ VM Service DNS。
- 基线：`github.com/Jw-Jm/aiops-edge` main=`e25eb5d`，每任务提交。

---

# Part 1 · 布局壳重构

## Task 1: 引入 zustand + 全局 store

**Files:**
- Modify: `aiops/observability-frontend/package.json`（+zustand）
- Create: `aiops/observability-frontend/src/store/uiStore.ts`

**Interfaces:**
- Consumes: React 现有 useState（App.tsx 的 collapsed/darkMode）。
- Produces: `useUIStore`（collapsed/darkMode/commandOpen/activeCommand 全局状态 + actions）。

- [ ] **Step 1: 装 zustand**

Run: `cd aiops/observability-frontend && npm install zustand`
Expected: package.json 增 `zustand`。

- [ ] **Step 2: 建 uiStore.ts**

```ts
// src/store/uiStore.ts
import { create } from 'zustand'
import { persist } from 'zustand/middleware'

interface UIState {
  collapsed: boolean
  darkMode: boolean
  commandOpen: boolean
  activeCommand: string
  toggleCollapsed: () => void
  setDarkMode: (v: boolean) => void
  setCommandOpen: (v: boolean) => void
  setActiveCommand: (v: string) => void
}

export const useUIStore = create<UIState>()(
  persist(
    (set) => ({
      collapsed: false,
      darkMode: localStorage.getItem('darkMode') !== 'false',
      commandOpen: false,
      activeCommand: '',
      toggleCollapsed: () => set((s) => ({ collapsed: !s.collapsed })),
      setDarkMode: (v) => { set({ darkMode: v }); localStorage.setItem('darkMode', String(v)); document.body.classList.toggle('light', !v) },
      setCommandOpen: (v) => set({ commandOpen: v }),
      setActiveCommand: (v) => set({ activeCommand: v }),
    }),
    { name: 'aiops-ui' },
  ),
)
```

- [ ] **Step 3: 提交**

```bash
cd /Users/mssc/Documents/Code/agent/aiops
git add observability-frontend/package.json observability-frontend/src/store/uiStore.ts
git commit -m "feat(frontend): add zustand uiStore"
```

## Task 2: zinc 色板收敛

**Files:**
- Modify: `aiops/observability-frontend/src/index.css`
- Modify: `aiops/observability-frontend/src/App.tsx`（darkToken）

**Interfaces:**
- Consumes: 现有 AntD token（`#0a0f1c`/`#121826` 等）。
- Produces: 统一 zinc 语义色板（`--bg:#09090b; --surface:#18181b; --border:#27272a`），App.tsx darkToken 改为 zinc 值。

- [ ] **Step 1: index.css 加 zinc 变量**

```css
:root {
  --bg: #09090b;          /* zinc-950 */
  --surface: #18181b;     /* zinc-900 */
  --surface-2: #27272a;   /* zinc-800 */
  --border: #27272a;
  --text: #f4f4f5;        /* zinc-100 */
  --text-muted: #a1a1aa;  /* zinc-400 */
}
```

- [ ] **Step 2: App.tsx darkToken 改 zinc**

```ts
const darkToken = {
  colorPrimary: '#1677ff',
  borderRadius: 8,
  colorBgLayout: '#09090b',
  colorBgContainer: '#18181b',
  colorBgElevated: '#27272a',
  colorText: '#f4f4f5',
  colorTextSecondary: '#a1a1aa',
  colorBorder: 'rgba(255,255,255,0.12)',
  colorBorderSecondary: 'rgba(255,255,255,0.08)',
  colorSplit: 'rgba(255,255,255,0.08)',
}
```

- [ ] **Step 3: 构建验证**

Run: `cd aiops/observability-frontend && npm run build 2>&1 | tail -3`
Expected: 构建成功。

- [ ] **Step 4: 提交**

```bash
cd /Users/mssc/Documents/Code/agent/aiops
git add observability-frontend/src/index.css observability-frontend/src/App.tsx
git commit -m "feat(frontend): zinc palette"
```

## Task 3: CommandPalette（⌘K⌘P）

**Files:**
- Create: `aiops/observability-frontend/src/components/CommandPalette.tsx`

**Interfaces:**
- Consumes: `useUIStore.commandOpen/setCommandOpen`（Task 1）；菜单项（Task 5 的 sidebar 数据）。
- Produces: 全局 ⌘K/⌘P 打开的命令面板（搜索/跳转路由）。

- [ ] **Step 1: 实现 CommandPalette**

```tsx
// src/components/CommandPalette.tsx
import { useEffect, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { useUIStore } from '../store/uiStore'

const COMMANDS = [
  { label: '平台总览', path: '/', keywords: 'overview home dashboard' },
  { label: 'AI 诊断', path: '/aichat', keywords: 'ai chat assistant' },
  { label: '服务列表', path: '/services', keywords: 'service' },
  { label: '服务拓扑', path: '/topology', keywords: 'topology graph' },
  { label: '链路追踪', path: '/traces', keywords: 'trace' },
  { label: '日志查询', path: '/logs', keywords: 'log' },
  { label: '告警中心', path: '/alerts', keywords: 'alert' },
  { label: '监控面板', path: '/monitor', keywords: 'monitor panel' },
  { label: '任务工作台', path: '/tasks', keywords: 'task' },
  { label: '系统设置', path: '/settings', keywords: 'settings config' },
]

const CommandPalette: React.FC = () => {
  const navigate = useNavigate()
  const open = useUIStore((s) => s.commandOpen)
  const setOpen = useUIStore((s) => s.setCommandOpen)
  const [q, setQ] = useState('')
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && (e.key === 'k' || e.key === 'p')) { e.preventDefault(); setOpen(!open); setQ('') }
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [open, setOpen])
  if (!open) return null
  const list = COMMANDS.filter((c) => !q || c.label.toLowerCase().includes(q.toLowerCase()) || c.keywords.includes(q.toLowerCase()))
  return (
    <div onClick={() => setOpen(false)} style={{ position: 'fixed', inset: 0, zIndex: 1000, background: 'rgba(0,0,0,0.6)', display: 'flex', alignItems: 'flex-start', justifyContent: 'center', paddingTop: '18vh' }}>
      <div onClick={(e) => e.stopPropagation()} style={{ width: 480, background: 'var(--surface)', border: '1px solid var(--border)', borderRadius: 12, padding: 12, boxShadow: '0 16px 48px rgba(0,0,0,0.4)' }}>
        <input autoFocus value={q} onChange={(e) => setQ(e.target.value)} placeholder="输入命令或搜索页面…" style={{ width: '100%', background: 'transparent', border: 'none', outline: 'none', color: 'var(--text)', fontSize: 15, padding: '8px 4px' }} />
        <div style={{ marginTop: 8 }}>
          {list.map((c) => (
            <div key={c.path} onClick={() => { setOpen(false); navigate(c.path) }} style={{ padding: '8px 10px', borderRadius: 8, cursor: 'pointer', color: 'var(--text)' }} onMouseEnter={(e) => (e.currentTarget.style.background = 'var(--surface-2)')} onMouseLeave={(e) => (e.currentTarget.style.background = 'transparent')}>
              {c.label}
            </div>
          ))}
        </div>
      </div>
    </div>
  )
}
export default CommandPalette
```

- [ ] **Step 2: 提交**

```bash
cd /Users/mssc/Documents/Code/agent/aiops
git add observability-frontend/src/components/CommandPalette.tsx
git commit -m "feat(frontend): command palette (cmd+k / cmd+p)"
```

## Task 4: AgentSidePanel

**Files:**
- Create: `aiops/observability-frontend/src/components/AgentSidePanel.tsx`

**Interfaces:**
- Consumes: `useUIStore`；后端 agent 列表（本次先静态占位 + 预留 `/agents` 接口）。
- Produces: 右侧可折叠的 AI 助理面板（agent 列表/状态/快捷动作）。

- [ ] **Step 1: 实现 AgentSidePanel**

```tsx
// src/components/AgentSidePanel.tsx
import { useEffect, useState } from 'react'
import { useUIStore } from '../store/uiStore'

const AGENTS = [
  { id: 'rca', name: 'RCA 根因分析', desc: '定位服务异常根因', status: 'ready' },
  { id: 'holmes', name: 'Holmes 链路调查', desc: '深度追踪调用链', status: 'ready' },
  { id: 'query', name: 'SQL 查询专家', desc: 'NL 转 ClickHouse SQL', status: 'ready' },
  { id: 'ops', name: '运维执行', desc: '安全执行运维操作', status: 'ready' },
]

const AgentSidePanel: React.FC = () => {
  const collapsed = useUIStore((s) => s.collapsed)
  const setActive = useUIStore((s) => s.setActiveCommand)
  const [open, setOpen] = useState(false)
  // 预留：后续从 /agents 拉取
  return (
    <>
      <button onClick={() => setOpen(!open)} title="AI 助理" style={{ position: 'fixed', right: 0, top: '50%', transform: 'translateY(-50%)', zIndex: 900, background: 'var(--surface)', color: 'var(--text)', border: '1px solid var(--border)', borderRight: 'none', borderRadius: '8px 0 0 8px', padding: '10px 6px', cursor: 'pointer' }}>
        🤖
      </button>
      {open && (
        <div style={{ position: 'fixed', right: 0, top: 0, bottom: 0, width: 280, zIndex: 950, background: 'var(--surface)', borderLeft: '1px solid var(--border)', padding: 16 }}>
          <div style={{ color: 'var(--text)', fontWeight: 700, fontSize: 15, marginBottom: 12 }}>AI 助理</div>
          {AGENTS.map((a) => (
            <div key={a.id} style={{ padding: '10px 12px', borderRadius: 10, border: '1px solid var(--border)', marginBottom: 8, cursor: 'pointer' }} onClick={() => { setActive(a.id); setOpen(false) }}>
              <div style={{ color: 'var(--text)', fontWeight: 600 }}>{a.name}</div>
              <div style={{ color: 'var(--text-muted)', fontSize: 12 }}>{a.desc}</div>
              <div style={{ color: '#22c55e', fontSize: 11, marginTop: 4 }}>● {a.status}</div>
            </div>
          ))}
        </div>
      )}
    </>
  )
}
export default AgentSidePanel
```

- [ ] **Step 2: 提交**

```bash
cd /Users/mssc/Documents/Code/agent/aiops
git add observability-frontend/src/components/AgentSidePanel.tsx
git commit -m "feat(frontend): agent side panel"
```

## Task 5: Sidebar 8 区段 + 接入布局壳

**Files:**
- Modify: `aiops/observability-frontend/src/App.tsx`

**Interfaces:**
- Consumes: `useUIStore`（Task 1）、`CommandPalette`（Task 3）、`AgentSidePanel`（Task 4）、`/monitor` 路由（Task 6/Part 2）。
- Produces: Sidebar 从 4 组扩为 8 区段（总览/可观测/监控/AI运维/任务/集成/设置/管理）；`<CommandPalette/>`+`<AgentSidePanel/>` 挂入布局；`collapsed/darkMode` 改用 store。

- [ ] **Step 1: 重构 menuGroups 为 8 区段**

```ts
const menuGroups = [
  { title: '总览', items: [{ key: '/', icon: <DashboardOutlined />, label: '平台总览' }] },
  { title: '可观测', items: [
    { key: '/services', icon: <DatabaseOutlined />, label: '服务列表' },
    { key: '/topology', icon: <ApartmentOutlined />, label: '服务拓扑' },
    { key: '/traces', icon: <NodeIndexOutlined />, label: '链路追踪' },
    { key: '/logs', icon: <FileSearchOutlined />, label: '日志查询' },
  ]},
  { title: '监控', items: [{ key: '/monitor', icon: <RadarChartOutlined />, label: '监控面板' }] },
  { title: '智能运维', items: [
    { key: '/aichat', icon: <RobotOutlined />, label: 'AI 诊断' },
    { key: '/alerts', icon: <AlertOutlined />, label: '告警中心' },
  ]},
  { title: '任务', items: [{ key: '/tasks', icon: <ToolOutlined />, label: '任务工作台' }] },
  { title: '集成', items: [{ key: '/deepflow', icon: <CloudServerOutlined />, label: 'DeepFlow' }] },
  { title: '设置', items: [{ key: '/settings', icon: <SettingOutlined />, label: '系统设置' }] },
]
```
`const { collapsed, toggleCollapsed, darkMode } = useUIStore()`；`<Menu onClick toggleCollapsed>`；渲染 `collapsed` 用 store。挂 `<CommandPalette/>` 与 `<AgentSidePanel/>` 于 `<ConfigProvider>` 内、`<Layout>` 外层。

- [ ] **Step 2: 构建验证**

Run: `cd aiops/observability-frontend && npm run build 2>&1 | tail -3`
Expected: 构建成功。

- [ ] **Step 3: 提交**

```bash
cd /Users/mssc/Documents/Code/agent/aiops
git add observability-frontend/src/App.tsx
git commit -m "feat(frontend): sidebar 8 sections + wire command palette & agent panel"
```

# Part 2 · Monitor 面板页

## Task 6: 新增 Monitor 页面（PromQL 面板网格）

**Files:**
- Create: `aiops/observability-frontend/src/pages/Monitor/index.tsx`

**Interfaces:**
- Consumes: PromQL 端点 `/api/v1/metrics/query_range`（已实现）；`api/client.ts`。
- Produces: `/monitor` 页——预置面板网格（服务请求速率/错误率/延迟/P95 等），下拉 PromQL 查询 + 时间范围。

- [ ] **Step 1: 实现 Monitor**

```tsx
// src/pages/Monitor/index.tsx
import { useEffect, useState } from 'react'
import { Row, Col, Card, Spin, Button } from 'antd'
import api from '../../api/client'

const PANELS = [
  { key: 'rate', title: '服务请求速率', query: 'sum(rate(http_requests_total[5m])) by (service)' },
  { key: 'error', title: '服务错误率', query: 'sum(rate(http_requests_total{status=~"5.."}[5m])) by (service)' },
  { key: 'p95', title: '延迟 P95', query: 'histogram_quantile(0.95, sum(rate(http_request_duration_seconds_bucket[5m])) by (le, service))' },
  { key: 'cpu', title: 'CPU 使用率', query: '100 - avg(rate(node_cpu_seconds_total{mode="idle"}[5m])) * 100' },
]

const Monitor: React.FC = () => {
  const [rows, setRows] = useState<Record<string, any[]>>({})
  const [loading, setLoading] = useState(true)
  const load = async () => {
    setLoading(true)
    const now = Math.floor(Date.now() / 1000)
    const start = now - 3600, step = '60'
    const results: Record<string, any[]> = {}
    await Promise.all(PANELS.map(async (p) => {
      try {
        const r = await api.get('/metrics/query_range', { params: { query: p.query, start, end: now, step } })
        results[p.key] = r?.data?.data?.result || []
      } catch { results[p.key] = [] }
    }))
    setRows(results); setLoading(false)
  }
  useEffect(() => { load() }, [])
  return (
    <Spin spinning={loading}>
      <Row gutter={[16, 16]}>
        {PANELS.map((p) => (
          <Col span={12} key={p.key}>
            <Card title={p.title} extra={<Button size="small" onClick={load}>刷新</Button>} style={{ borderRadius: 12 }}>
              {rows[p.key]?.length ? (
                <pre style={{ color: 'var(--text-muted)', fontSize: 12, maxHeight: 200, overflow: 'auto' }}>{JSON.stringify(rows[p.key], null, 2)}</pre>
              ) : (
                <div style={{ color: 'var(--text-muted)', fontSize: 12 }}>暂无数据（等待 VM 采集）</div>
              )}
            </Card>
          </Col>
        ))}
      </Row>
    </Spin>
  )
}
export default Monitor
```
> 说明：先以 JSON 面板展示 VM 结果（图表库如 echarts 后续增强）；`Button` 需在 import 补。

- [ ] **Step 2: App.tsx 注册 `/monitor` 路由**

在 `App.tsx` 的 `<Routes>` 加 `<Route path="/monitor" element={<Monitor />} />`，并 import `Monitor`。

- [ ] **Step 3: 构建验证**

Run: `cd aiops/observability-frontend && npm run build 2>&1 | tail -3`
Expected: 构建成功。

- [ ] **Step 4: 提交**

```bash
cd /Users/mssc/Documents/Code/agent/aiops
git add observability-frontend/src/pages/Monitor observability-frontend/src/App.tsx
git commit -m "feat(frontend): monitor panel page via PromQL"
```

# Part 3 · Dashboard 聚合接口 + Overview 升级

## Task 7: query-api 新增 `/dashboard/stats` 聚合接口（TDD）

**Files:**
- Modify: `aiops/ai-apm-query-go/internal/biz/dashboard.go`（新建）
- Modify: `aiops/ai-apm-query-go/internal/api/handler.go`（+Handler.DashboardStats）
- Modify: `aiops/ai-apm-query-go/cmd/api/main.go`（注册路由）
- Test: `aiops/ai-apm-query-go/internal/biz/dashboard_test.go`

**Interfaces:**
- Consumes: 现有 ClickHouse 查询（`getServiceMetrics`/`getServiceTopology`/告警 `getAlertEvents`，可复用）。
- Produces: `GET /api/v1/dashboard/stats` → `{ services, alerts, edges, errorRate, avgLatency, sparkline: [...], topServices: [...] }`。

- [ ] **Step 1: 写失败测试**

```go
// aiops/ai-apm-query-go/internal/biz/dashboard_test.go
package biz

import "testing"

// DashboardStats 应聚合各计数并汇总 top 服务
func TestDashboardStatsAggregation(t *testing.T) {
	input := []struct {
		Service  string
		Call     int
		Err      int
		LatSumNs int64
	}{
		{"svc-a", 100, 5, 2000},
		{"svc-b", 50, 0, 1000},
	}
	stats := aggregateStats(input)
	if stats.Services != 2 {
		t.Fatalf("Services = %d, want 2", stats.Services)
	}
	if stats.TotalCalls != 150 {
		t.Fatalf("TotalCalls = %d, want 150", stats.TotalCalls)
	}
	if stats.TotalErrors != 5 {
		t.Fatalf("TotalErrors = %d, want 5", stats.TotalErrors)
	}
	if stats.TopServices == nil || len(stats.TopServices) == 0 {
		t.Fatal("TopServices empty")
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `cd aiops/ai-apm-query-go && go test ./internal/biz/ -run TestDashboardStatsAggregation -v`
Expected: FAIL（`aggregateStats`/`DashboardStats` 未定义）。

- [ ] **Step 3: 实现 dashboard.go + handler**

```go
// internal/biz/dashboard.go
package biz

type StatsItem struct {
	Service    string  `json:"service"`
	Calls      int64   `json:"calls"`
	Errors     int64   `json:"errors"`
	ErrorRate  float64 `json:"error_rate"`
	AvgLatency float64 `json:"avg_latency_ms"`
}

type DashboardStats struct {
	Services    int          `json:"services"`
	TotalCalls  int64        `json:"total_calls"`
	TotalErrors int64        `json:"total_errors"`
	ErrorRate   float64      `json:"error_rate"`
	TopServices []StatsItem  `json:"top_services"`
	Sparkline   []float64    `json:"sparkline"`
}

func aggregateStats(rows []StatsItem) *DashboardStats {
	var s DashboardStats
	var top []StatsItem
	for _, r := range rows {
		s.TotalCalls += r.Calls
		s.TotalErrors += r.Errors
		top = append(top, r)
	}
	s.Services = len(rows)
	if s.TotalCalls > 0 {
		s.ErrorRate = float64(s.TotalErrors) / float64(s.TotalCalls) * 100
	}
	s.TopServices = top
	return &s
}
```
`handler.go` 增 `Handler.DashboardStats(w,r)`：查询 ClickHouse（服务 RED + 拓扑 + 告警）→ `biz.aggregateStats` → JSON。

- [ ] **Step 4: 运行测试确认通过**

Run: `cd aiops/ai-apm-query-go && go test ./internal/biz/ -run TestDashboardStatsAggregation -v`
Expected: PASS。

- [ ] **Step 5: main.go 注册路由**

`mux.HandleFunc("/api/v1/dashboard/stats", handler.DashboardStats)`

- [ ] **Step 6: 编译 + 冒烟**

Run: `cd aiops/ai-apm-query-go && go build ./...`
Expected: 通过。

- [ ] **Step 7: 提交**

```bash
cd /Users/mssc/Documents/Code/agent/aiops
git add ai-apm-query-go/internal/biz/dashboard.go ai-apm-query-go/internal/biz/dashboard_test.go ai-apm-query-go/internal/api/handler.go ai-apm-query-go/cmd/api/main.go
git commit -m "feat(query-api): /dashboard/stats aggregate endpoint (TDD)"
```

## Task 8: Overview 升级为 Dashboard（复用聚合接口）

**Files:**
- Modify: `aiops/observability-frontend/src/pages/Overview/index.tsx`

**Interfaces:**
- Consumes: `/api/v1/dashboard/stats`（Task 7）。
- Produces: Overview 改为单请求聚合（替换原 3 个 `Promise.allSettled`），增加错误率/延迟/趋势展示。

- [ ] **Step 1: 改造 Overview**

```ts
// 替换原 load() 内 3 个并发请求为单个 /dashboard/stats
const res = await api.get('/dashboard/stats')
const d = res?.data
setStats({
  services: d?.services ?? 0,
  alerts: 0,            // 告警数后端可并入 stats 字段
  edges: 0,
  errorRate: d?.error_rate ?? 0,
  avgLatency: d?.avg_latency_ms ?? 0,
})
```
（保留原 3 请求兜底逻辑作为 fallback；前端展示区加错误率/延迟。）

- [ ] **Step 2: 构建验证**

Run: `cd aiops/observability-frontend && npm run build 2>&1 | tail -3`
Expected: 构建成功。

- [ ] **Step 3: 提交**

```bash
cd /Users/mssc/Documents/Code/agent/aiops
git add observability-frontend/src/pages/Overview/index.tsx
git commit -m "feat(frontend): dashboard via /dashboard/stats aggregate"
```

## Task 9: 本机部署验证 + 推送

**Files:**
- 无新代码；构建 + 部署 + 验证 + 推送。

**Interfaces:**
- Consumes: 全部。

- [ ] **Step 1: 重建前端 + query-api 镜像**

Run: `cd /Users/mssc/Documents/Code/agent/aiops && docker build -t observability-frontend:latest observability-frontend && docker build -t query-api:latest ai-apm-query-go && docker tag observability-frontend:latest docker.io/library/observability-frontend:latest && docker tag query-api:latest docker.io/library/query-api:latest`
Expected: 构建成功（arm64）。

- [ ] **Step 2: 升级部署**

Run: `cd /Users/mssc/Documents/Code/agent/aiops && helm upgrade aiops deploy/helm/aiops --namespace observability --set deepflow.enabled=false --set secrets.jwtSecret="dev-jwt-secret-change-me" --set secrets.internalToken="dev-internal-token" --set secrets.ingestApiKey="dev-ingest-key" --set secrets.clickhousePassword="dev-ch-pass" --set secrets.redisPassword="dev-redis-pass" --set secrets.minioAccessKey="minioadmin" --set secrets.minioSecretKey="minioadmin123" --set secrets.mysqlRootPassword="dev-mysql-pass"`
Expected: deployed。

- [ ] **Step 3: 验证前端 + 新路由**

Run: `curl -s -o /dev/null -w "%{http_code}\n" http://localhost:30253/`
Expected: 200。
Run: 登录 JWT 后 `curl -s http://localhost:30253/api/v1/dashboard/stats -H "Authorization: Bearer $JWT" | head -c 300`
Expected: 200 JSON（含 services/error_rate 等）。

- [ ] **Step 4: 验证 Monitor 页**

浏览器访问 `http://localhost:30253/monitor`，确认页面渲染 + PromQL 面板。
Expected: 页面可访问，面板显示"暂无数据"或 VM 结果。

- [ ] **Step 5: 提交验证通过（如有修复）**

```bash
cd /Users/mssc/Documents/Code/agent/aiops
git add -A
git commit -m "fix(frontend): deployment verification fixes" || echo "无改动"
```

- [ ] **Step 6: 推送**

```bash
cd /Users/mssc/Documents/Code/agent/aiops
git push origin main
```
Expected: 推送成功。
