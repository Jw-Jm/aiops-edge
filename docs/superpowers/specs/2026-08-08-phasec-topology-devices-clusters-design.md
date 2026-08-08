# Phase C：拓扑专项（目录+真实聚合）+ Devices + Clusters + Knowledge

**日期**: 2026-08-08
**范围**: query-api（Go, MySQL + K8s 自动发现 + ClickHouse trace 聚合）+ observability-frontend（React）

## 1. 范围与决策（已确认）

| 块 | 决策 |
|----|------|
| 拓扑专项 | **服务目录（catalog）+ 真实 trace 聚合**（从 trace_spans 提取服务调用，替换硬编码依赖表）|
| Devices | **存 MySQL，query-api 管理**，人工录入 + 列表/详情/编辑 |
| Clusters | **存 MySQL，query-api 管理，K8s 自动发现**（节点/命名空间）|
| Knowledge | P1b 已完整覆盖（确认），无增量 |

**统一数据归属**：Devices/Clusters/服务目录 存 MySQL（`aiops` 库），query-api 用 `database/sql`（复用 P2a 的 `internal/store` 模式）。

---

## 2. 数据库表设计（query-api 迁移，复用 store.EnsureSchema）

### 2.1 `service_catalog` — 服务目录

```sql
CREATE TABLE IF NOT EXISTS service_catalog (
  id BIGINT PRIMARY KEY AUTO_INCREMENT,
  service_name VARCHAR(128) NOT NULL UNIQUE,   -- 关联 trace_spans.service_name
  display_name VARCHAR(128) DEFAULT '',
  description TEXT,
  owner VARCHAR(128) DEFAULT '',
  team VARCHAR(128) DEFAULT '',
  tags VARCHAR(255) DEFAULT '',
  status ENUM('active','maintenance','deprecated') DEFAULT 'active',
  created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
```

### 2.2 `devices` — 设备

```sql
CREATE TABLE IF NOT EXISTS devices (
  id BIGINT PRIMARY KEY AUTO_INCREMENT,
  hostname VARCHAR(128) NOT NULL UNIQUE,
  ip VARCHAR(64) DEFAULT '',
  os VARCHAR(64) DEFAULT '',
  cpu_cores INT DEFAULT 0,
  memory_mb BIGINT DEFAULT 0,
  status ENUM('online','offline','maintenance') DEFAULT 'online',
  role VARCHAR(64) DEFAULT '',                -- node/worker/edge
  location VARCHAR(128) DEFAULT '',
  tags VARCHAR(255) DEFAULT '',
  created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
```

### 2.3 `clusters` — 集群

```sql
CREATE TABLE IF NOT EXISTS clusters (
  id BIGINT PRIMARY KEY AUTO_INCREMENT,
  name VARCHAR(128) NOT NULL UNIQUE,
  provider VARCHAR(64) DEFAULT '',            -- onprem/aws/aliyun/orbstack
  region VARCHAR(64) DEFAULT '',
  version VARCHAR(64) DEFAULT '',             -- k8s version
  node_count INT DEFAULT 0,
  status ENUM('active','degraded','down') DEFAULT 'active',
  api_server VARCHAR(255) DEFAULT '',
  created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
```

---

## 3. 后端接口（query-api, Go）

### 3.1 服务目录 CRUD

| 端点 | 方法 | 逻辑 |
|------|------|------|
| `/api/v1/catalog/services` | GET | 服务目录列表（含从 trace 聚合的实时指标：调用量/错误率/P95）|
| `/api/v1/catalog/services` | POST | 新增目录项 |
| `/api/v1/catalog/services/{id}` | PUT/DELETE | 编辑/删除 |

### 3.2 真实 trace 聚合拓扑（替换硬编码依赖表）

现有拓扑 handler 从硬编码 K8s 依赖表聚合，改为从 `trace_spans` 提取服务调用边：

```go
// 从 trace_spans 聚合服务调用关系（source→target 边）
// 通过 parent_span_id 关联：span 的 service 调用其 parent span 的 service
SELECT
  s2.service_name AS source,
  s1.service_name AS target,
  count() AS calls,
  countIf(s1.is_error=1) AS errors
FROM observability.trace_spans s1
JOIN observability.trace_spans s2 ON s1.parent_span_id = s2.span_id
WHERE s1.tenant_id=? AND s1.date >= today()-1
GROUP BY source, target
```

- 生成 `{nodes, edges}`，nodes 含服务名 + 实时指标，edges 含调用次数/错误率/P95
- 保留 `service_topology` 表作为归档，但 Dashboard/拓扑页主数据源改为 trace 实时聚合

### 3.3 Devices CRUD

| 端点 | 方法 | 逻辑 |
|------|------|------|
| `/api/v1/devices` | GET | 设备列表（分页）|
| `/api/v1/devices` | POST | 新增设备 |
| `/api/v1/devices/{id}` | PUT/DELETE | 编辑/删除 |

### 3.4 Clusters（K8s 自动发现 + CRUD）

| 端点 | 方法 | 逻辑 |
|------|------|------|
| `/api/v1/clusters` | GET | 集群列表 |
| `/api/v1/clusters/sync` | POST | **从 K8s 自动发现**：读当前 K8s 集群信息 → upsert clusters 表 |
| `/api/v1/clusters/{id}` | PUT/DELETE | 编辑/删除 |
| `/api/v1/clusters/{id}/nodes` | GET | 集群节点列表（K8s node 信息）|

**K8s 自动发现**（用 client-go 或 K8s REST API）：
- 读 `cluster-info`、节点列表（Node 资源）、命名空间
- upsert 到 `clusters` + `devices`（节点作为 device 的 role=node）

---

## 4. 前端页面

| 路由 | 页面 | 功能 |
|------|------|------|
| `/catalog` | 服务目录 | 目录列表（服务/owner/team/状态 + 实时指标），增删改 |
| `/devices` | 设备管理 | 设备列表（主机/IP/OS/状态），增删改 |
| `/clusters` | 集群管理 | 集群列表 + "从 K8s 同步"按钮 + 节点查看 |
| `/topology` | 拓扑（增强）| 现有 G6 图改为真实 trace 聚合数据 |

菜单挂到现有分组（"智能运维" 或新"基础设施"分组）。

---

## 5. 依赖

- query-api：`k8s.io/client-go`（或轻量用 K8s REST API + serviceaccount token）用于集群发现
- 前端：复用现有 G6（拓扑）/ echarts / AntD

---

## 6. 测试

- **Go**：`catalog_test.go`、`devices_test.go`、`clusters_test.go`（MySQL CRUD）、`topology_trace_test.go`（trace 聚合 SQL 逻辑，mock）
- **前端**：`tsc --noEmit` + `npm run build`
- **冒烟**：服务目录 CRUD、Devices CRUD、Clusters 从 K8s 同步、拓扑返回 trace 聚合边

---

## 7. 风险

| 风险 | 概率 | 影响 | 缓解 |
|------|------|------|------|
| K8s 发现权限不足 | 中 | 中 | client-go 用 in-cluster config；无权限降级人工录入 |
| trace 聚合性能 | 中 | 中 | 限制时间窗口 + LIMIT + 缓存 |
| 硬编码依赖表替换影响现有拓扑 | 中 | 高 | 保留 service_topology 归档，主源切换可回退 |
| MySQL 不可达 | 低 | 中 | 复用降级模式（内存 fallback）|

---

## 8. 自审

- [x] 无 TBD/TODO
- [x] 范围聚焦：四块完整交付
- [x] 统一数据归属：query-api 管 MySQL（与 P2a 一致）
- [x] 复用 store.EnsureSchema / database/sql / G6，无重复造轮子
- [x] 拓扑真实 trace 聚合明确（parent_span 关联）
