# Phase A 剩余 · Logs/Traces UI 风格对齐 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 对齐 Logs / Traces 页 UI 风格到 ongrid 深色 zinc 基调（功能已完备，仅做风格层对齐），不改变已有查询逻辑/接口。

**Architecture:** 纯前端。自研 `Logs/index.tsx`、`Traces/index.tsx` 功能已完善（Logs 含 VictoriaLogs+ClickHouse 双后端、LogsQL、severity 过滤；Traces 列表）。仅将硬编码颜色/背景替换为 zinc 语义变量（`--surface`/`--surface-2`/`--border`/`--text-muted`，已在 index.css 定义），统一 Card 圆角/边框。

**Tech Stack:** React18 / AntD5 / CSS 变量（zinc，已定义）。

## Global Constraints

- 前端：`observability-frontend/src/pages/Logs/index.tsx`、`Traces/index.tsx`。
- **只做 UI 风格对齐，不改功能逻辑**（查询/接口/状态管理保持）。
- zinc 变量已在 `src/index.css` 定义（`--bg`/`--surface`/`--surface-2`/`--border`/`--text`/`--text-muted`）。
- 合规：对齐 ongrid **风格基调**（深色 zinc），从自身组件用 CSS 变量实现，不复刻 ongrid 代码。
- 基线：`github.com/Jw-Jm/aiops-edge` main=`e81eee4`，每任务提交。

---

## Task 1: Logs 页 UI 对齐 zinc

**Files:**
- Modify: `aiops/observability-frontend/src/pages/Logs/index.tsx`

**Interfaces:**
- Consumes: 现有查询逻辑（`fetchLogs`/`checkVLStatus`）、`var(--surface)` 等 zinc 变量。
- Produces: 搜索卡片/结果卡片统一 zinc 背景 + 圆角；时间/服务列用 zinc muted 色。

- [ ] **Step 1: 对齐 Card 与文本颜色**

将两处 `<Card size='small' style={{ marginBottom: 12 }}>` 与结果 Card 加统一风格：
```tsx
// 搜索卡
<Card size='small' style={{ marginBottom: 12, background: 'var(--surface)', borderColor: 'var(--border)', borderRadius: 10 }}>
// 结果卡
<Card size='small' title={...} style={{ background: 'var(--surface)', borderColor: 'var(--border)', borderRadius: 10 }}>
```
时间列 render 用 muted 色 + 保留 monospace：
```tsx
render: (v: string) => <Text style={{ fontSize: 12, fontFamily: 'monospace', whiteSpace: 'nowrap', color: 'var(--text-muted)' }}>{fmtLocalTime(v, '-', 'MM-DD HH:mm:ss')}</Text>
```
LogsQL 提示行 `<Text type='secondary'>` 改为显式 muted：
```tsx
<Text style={{ fontSize: 11, color: 'var(--text-muted)' }}>
```

- [ ] **Step 2: 构建验证**

Run: `cd aiops/observability-frontend && npm run build 2>&1 | tail -3`
Expected: 构建成功。

- [ ] **Step 3: 提交**

```bash
cd /Users/mssc/Documents/Code/agent/aiops
git add observability-frontend/src/pages/Logs/index.tsx
git commit -m "style(frontend): align Logs page to zinc palette"
```

---

## Task 2: Traces 页 UI 对齐 zinc

**Files:**
- Modify: `aiops/observability-frontend/src/pages/Traces/index.tsx`

**Interfaces:**
- Consumes: 现有 `getTraces`/`fmtLocalTime`。
- Produces: 列表 Card + 搜索框对齐 zinc；Trace ID 链接用 zinc 文本色。

- [ ] **Step 1: 对齐 Card 与搜索框**

```tsx
<Card style={{ background: 'var(--surface)', borderColor: 'var(--border)', borderRadius: 10 }}>
  <Input prefix={<SearchOutlined />} placeholder="搜索 Trace ID..." value={search} onChange={e => setSearch(e.target.value)} style={{ width: 400, marginBottom: 16, background: 'var(--surface-2)', borderColor: 'var(--border)', color: 'var(--text)' }} />
```
Trace ID 链接色：
```tsx
render: (id: string) => <a onClick={() => navigate(`/traces/${id}`)} style={{ fontFamily: 'monospace', color: '#60a5fa' }}>...
```

- [ ] **Step 2: 构建验证**

Run: `cd aiops/observability-frontend && npm run build 2>&1 | tail -3`
Expected: 构建成功。

- [ ] **Step 3: 提交**

```bash
cd /Users/mssc/Documents/Code/agent/aiops
git add observability-frontend/src/pages/Traces/index.tsx
git commit -m "style(frontend): align Traces page to zinc palette"
```

---

## Task 3: 本机部署验证 + 推送

**Files:**
- 无新代码；构建 + 部署 + 验证 + 推送。

**Interfaces:**
- Consumes: Task 1-2。

- [ ] **Step 1: 重建前端镜像**

Run: `cd /Users/mssc/Documents/Code/agent/aiops && docker build -t observability-frontend:latest observability-frontend && docker tag observability-frontend:latest docker.io/library/observability-frontend:latest`
Expected: 构建成功（arm64）。

- [ ] **Step 2: 升级部署 + 触发 frontend 滚动更新**

Run: `cd /Users/mssc/Documents/Code/agent/aiops && helm upgrade aiops deploy/helm/aiops --namespace observability --set deepflow.enabled=false --set secrets.jwtSecret="dev-jwt-secret-change-me" --set secrets.internalToken="dev-internal-token" --set secrets.ingestApiKey="dev-ingest-key" --set secrets.clickhousePassword="dev-ch-pass" --set secrets.redisPassword="dev-redis-pass" --set secrets.minioAccessKey="minioadmin" --set secrets.minioSecretKey="minioadmin123" --set secrets.mysqlRootPassword="dev-mysql-pass" && kubectl -n observability rollout restart deploy/frontend`
Expected: deployed + frontend 重启。

- [ ] **Step 3: 验证前端**

Run: `sleep 30 && curl -s -o /dev/null -w "%{http_code}\n" http://localhost:30253/`
Expected: 200。
Run: `curl -s -o /dev/null -w "%{http_code}\n" http://localhost:30253/logs`
Expected: 200（Logs 路由）。
Run: `curl -s -o /dev/null -w "%{http_code}\n" http://localhost:30253/traces`
Expected: 200（Traces 路由）。

- [ ] **Step 4: 提交验证通过（如有修复）**

```bash
cd /Users/mssc/Documents/Code/agent/aiops
git add -A
git commit -m "fix(frontend): deployment verification fixes" || echo "无改动"
```

- [ ] **Step 5: 推送**

```bash
cd /Users/mssc/Documents/Code/agent/aiops
git push origin main
```
Expected: 推送成功。
