# Phase C：拓扑专项（目录+真实聚合）+ Devices + Clusters Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** ① 服务目录（catalog）+ 真实 trace 聚合拓扑，② Devices CRUD，③ Clusters（K8s 自动发现）+ CRUD，④ Knowledge 确认。全部由 query-api（Go, MySQL database/sql）管理。

**Architecture:** query-api 扩展 store.EnsureSchema（新 3 表）+ 新 DAO + 新 handler；K8s 发现用 `kubectl` 命令解析（项目无 client-go，保持轻量）；前端新增 4 页。

**Tech Stack:** Go, MySQL, ClickHouse, kubectl, React, AntD, echarts/G6

## Global Constraints

- 复用 `internal/store`（P2a 的 EnsureSchema/GetDB/UserDAO 模式）与 `internal/api` 的 auth/RequireRole
- 新表 `service_catalog`/`devices`/`clusters` 由 EnsureSchema 建表（幂等）
- Clusters 发现用 `kubectl` 命令（`kubectl get nodes -o json`），不引入 client-go
- 拓扑主数据源改为 trace_spans 实时聚合（parent_span_id 关联），`service_topology` 保留归档
- 复用 `database/sql`（无 ORM）、RequireRole("admin") 保护写操作
- 现有 68 pytest + Go test 不回归

---

### Task 1: query-api 三表迁移 + DAO（service_catalog/devices/clusters）

**Files:**
- Modify: `ai-apm-query-go/internal/store/mysql.go`（EnsureSchema 加 3 表）
- Create: `ai-apm-query-go/internal/store/catalog.go`、`devices.go`、`clusters.go`（DAO）
- Test: `ai-apm-query-go/internal/store/*_test.go`

**Interfaces:**
- Consumes: `GetDB()`
- Produces: `CatalogDAO`、`DeviceDAO`、`ClusterDAO`

- [ ] **Step 1: EnsureSchema 加 3 张表**

在 `mysql.go` 的 EnsureSchema 追加 `service_catalog`/`devices`/`clusters` 建表 SQL（见设计文档 §2）。

- [ ] **Step 2: 写失败测试**

```go
// store/catalog_test.go
func TestCatalogDAO(t *testing.T) {
	if GetDB() == nil { t.Skip("MySQL not available") }
	d := &CatalogDAO{}
	id, _ := d.Create(&ServiceCatalog{ServiceName: "svc-c", Owner: "ops"})
	if id <= 0 { t.Fatal("create failed") }
}
```

- [ ] **Step 3: 运行确认失败**

Run: `cd ai-apm-query-go && go build ./...`
Expected: FAIL — CatalogDAO 未定义

- [ ] **Step 4: 实现三个 DAO**

复用 UserDAO 模式（List/Create/Update/Delete/Get），字段对齐设计文档。

- [ ] **Step 5: 运行确认通过**

Run: `cd ai-apm-query-go && go build ./... && go test ./internal/store/`
Expected: PASS（MySQL 不可达 skip）

- [ ] **Step 6: 提交**

```bash
git add ai-apm-query-go/internal/store
git commit -m "feat(query-api): service_catalog/devices/clusters 表 + DAO"
```

---

### Task 2: 服务目录 + Devices + Clusters 路由（handler + 注册）

**Files:**
- Create: `ai-apm-query-go/internal/api/catalog.go`、`devices.go`、`clusters.go`
- Modify: `ai-apm-query-go/cmd/api/main.go`（注册路由）
- Test: `ai-apm-query-go/internal/api/*_test.go`

**Interfaces:**
- Consumes: 各 DAO + RequireRole
- Produces: `/api/v1/catalog/services`、`/api/v1/devices`、`/api/v1/clusters` 及 `/sync`、`/nodes`

- [ ] **Step 1: 实现 catalog.go**

```go
// GET /api/v1/catalog/services — 目录列表 + 从 trace 聚合实时指标
// POST/PUT/DELETE — 目录 CRUD（admin）
func (h *Handler) CatalogList(w, r) { ... }
func (h *Handler) CatalogRouter(w, r) { ... }
```

- [ ] **Step 2: 实现 devices.go（CRUD，admin 保护）**

- [ ] **Step 3: 实现 clusters.go**

```go
// GET /api/v1/clusters — 列表
// POST /api/v1/clusters/sync — 从 kubectl 发现并 upsert
// GET /api/v1/clusters/{id}/nodes — 节点列表（kubectl get nodes）
// PUT/DELETE — CRUD（admin）
func (h *Handler) ClusterSync(w, r) {
	// kubectl get nodes -o json 解析 node_count/version/status
	// upsert 到 clusters
}
```

- [ ] **Step 4: 注册路由（main.go）**

```go
mux.HandleFunc("/api/v1/catalog/services", h.CatalogRouter)
mux.HandleFunc("/api/v1/catalog/services/", h.CatalogRouter)
mux.HandleFunc("/api/v1/devices", h.DeviceRouter)
mux.HandleFunc("/api/v1/devices/", h.DeviceRouter)
mux.HandleFunc("/api/v1/clusters", h.ClusterRouter)
mux.HandleFunc("/api/v1/clusters/", h.ClusterRouter)
```

- [ ] **Step 5: 运行确认通过**

Run: `cd ai-apm-query-go && go build ./... && go test ./...`
Expected: PASS

- [ ] **Step 6: 提交**

```bash
git add ai-apm-query-go/internal/api ai-apm-query-go/cmd/api/main.go
git commit -m "feat(query-api): 服务目录 + Devices + Clusters 路由（含 kubectl 自动发现）"
```

---

### Task 3: 真实 trace 聚合拓扑（替换硬编码依赖表）

**Files:**
- Modify: `ai-apm-query-go/internal/api/handler.go`（Topology 数据源改 trace 聚合）
- Test: `ai-apm-query-go/internal/api/topology_test.go`

**Interfaces:**
- Consumes: `queryClickHouse`
- Produces: 拓扑 `{nodes, edges}`（真实 trace 调用边）

- [ ] **Step 1: 写失败测试（topology_test.go）**

```go
func TestBuildTopologyFromTrace(t *testing.T) {
	// 解析 mock trace 聚合行，验证节点/边构建
}
```

- [ ] **Step 2: 实现 trace 聚合**

```go
// 从 trace_spans 聚合服务调用边
sql := `
SELECT s2.service_name AS source, s1.service_name AS target,
  count() AS calls, countIf(s1.is_error=1) AS errors,
  round(quantile(0.95)(s1.duration_ns)/1000000, 2) AS p95
FROM observability.trace_spans s1
JOIN observability.trace_spans s2 ON s1.parent_span_id = s2.span_id
WHERE s1.tenant_id=? AND s1.date >= today()-1
GROUP BY source, target ORDER BY calls DESC LIMIT 200`
// 构建 nodes（含聚合指标）+ edges
```

- [ ] **Step 3: 替换 Topology handler 数据源**

保留 `service_topology` 归档表，主源切到 trace 聚合。

- [ ] **Step 4: 运行确认通过**

Run: `cd ai-apm-query-go && go build ./... && go test ./...`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add ai-apm-query-go/internal/api/handler.go ai-apm-query-go/internal/api/topology_test.go
git commit -m "feat(query-api): 拓扑改真实 trace 聚合（parent_span 关联服务调用边）"
```

---

### Task 4: 前端四页（目录/设备/集群/拓扑增强）

**Files:**
- Create: `observability-frontend/src/pages/Catalog/index.tsx`
- Create: `observability-frontend/src/pages/Devices/index.tsx`
- Create: `observability-frontend/src/pages/Clusters/index.tsx`
- Modify: `observability-frontend/src/pages/Topology/index.tsx`（数据源对接）
- Modify: `observability-frontend/src/App.tsx`（路由+菜单）
- Modify: `observability-frontend/src/api/client.ts`（API）
- Test: `tsc --noEmit` + `npm run build`

**Interfaces:**
- Consumes: 前端 API
- Produces: 4 页面 + 菜单

- [ ] **Step 1: client.ts 加 API**

```ts
export const listCatalog = (params?) => api.get('/catalog/services', { params })
export const createCatalog = (data) => api.post('/catalog/services', data)
export const updateCatalog = (id, data) => api.put(`/catalog/services/${id}`, data)
export const deleteCatalog = (id) => api.delete(`/catalog/services/${id}`)
export const listDevices = (params?) => api.get('/devices', { params })
export const createDevice = (data) => api.post('/devices', data)
export const updateDevice = (id, data) => api.put(`/devices/${id}`, data)
export const deleteDevice = (id) => api.delete(`/devices/${id}`)
export const listClusters = (params?) => api.get('/clusters', { params })
export const syncClusters = () => api.post('/clusters/sync')
export const listClusterNodes = (id) => api.get(`/clusters/${id}/nodes`)
```

- [ ] **Step 2: 创建 Catalog 页（目录 + 实时指标表格）**

- [ ] **Step 3: 创建 Devices 页（设备 CRUD）**

- [ ] **Step 4: 创建 Clusters 页（列表 + 同步按钮 + 节点展开）**

- [ ] **Step 5: 增强 Topology 页（对接真实 trace 聚合数据）**

- [ ] **Step 6: 注册路由和菜单**

App.tsx 加 `/catalog`、`/devices`、`/clusters`；新建"基础设施"菜单分组。

- [ ] **Step 7: tsc + build 验证**

Run: `cd observability-frontend && node_modules/.bin/tsc --noEmit && npm run build`
Expected: exit 0

- [ ] **Step 8: 提交**

```bash
git add observability-frontend/src
git commit -m "feat(web): 服务目录 + 设备管理 + 集群管理 + 拓扑增强"
```

---

## Self-Review

**1. Spec coverage（对照设计文档）：**
- ✅ §2 三表 → Task 1
- ✅ §3.1/3.3/3.4 服务目录/Devices/Clusters 路由 → Task 2
- ✅ §3.2 真实 trace 聚合 → Task 3
- ✅ §4 前端 4 页 → Task 4
- ✅ Knowledge 确认 → 无增量（P1b 已覆盖）

**2. Placeholder scan：** 无 TBD/TODO。

**3. Type consistency：**
- `CatalogDAO`/`DeviceDAO`/`ClusterDAO` — Task 1 定义，Task 2 使用
- `ClusterSync` — Task 2 定义，注册 `/clusters/sync`
- 前端 `listCatalog/listDevices/listClusters/syncClusters` — Task 4 定义，页面使用一致
