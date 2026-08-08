# Admin 管理门户 + 用户 scope + kubeconfig 多集群

**日期**: 2026-08-08
**范围**: query-api（Go, MySQL scope/kubeconfig + 鉴权过滤 + 按集群查询）+ observability-frontend（React Admin 页）
**驱动**: 仿照 ongrid Admin/RBAC + 用户 scope + 后续多集群需求
**定位**: 轻量化——砍掉角色三级，保留 admin/user 两级 + 用户级 scope；多集群用 kubeconfig 注册（非 client-go 全量发现）

---

## 1. 决策汇总（已与用户确认）

| 项 | 决策 |
|----|------|
| RBAC 模型 | **砍掉角色三级**。保留 `admin`/`user` 两级角色 + **每用户直接配 scope** |
| scope 粒度 | 三维数组 `{"services":[],"clusters":[],"devices":[]}`，**换自研命名**（合规），admin 空 scope = 全量 |
| 多集群 | **kubeconfig 注册**：clusters 表加 `kubeconfig` 字段，用户上传 kubeconfig 注册集群；按集群用该 kubeconfig 查节点/命名空间/事件 |
| K8s 客户端 | **clientcmd 轻量加载 kubeconfig + REST**，不引入完整 client-go 发现 |
| Admin 页 | 单页多 Tab：用户管理(含 scope) / 审计日志 / 集群管理(多集群注册) |
| 不含 | 组织树、细粒度资源级 RBAC、角色权限矩阵 |

---

## 2. 数据模型（query-api MySQL，复用 store.EnsureSchema）

### 2.1 `users` 表加 `scope` 列

```sql
ALTER TABLE users ADD COLUMN scope VARCHAR(512) DEFAULT '';  -- {"services":[],"clusters":[],"devices":[]}
```

- `User` struct 加 `Scope string` 字段；`UserDAO.Create/Update` 支持 scope。
- admin 角色 scope 为空字符串 = 全量（不过滤）。

### 2.2 `clusters` 表加连接字段

```sql
ALTER TABLE clusters
  ADD COLUMN kubeconfig TEXT,          -- kubeconfig 内容（base64 或原文本）
  ADD COLUMN api_server VARCHAR(255) DEFAULT '',
  ADD COLUMN token VARCHAR(255) DEFAULT '';  -- 可选：若 kubeconfig 内无 token
```

- 现有 `clusters` 表已有 name/provider/region/version/node_count/status/api_server。
- `Cluster` struct 加 `Kubeconfig/ApiServer/Token` 字段；DAO CRUD 支持。

---

## 3. scope 权限过滤（query-api Go）

### 3.1 JWT 增加 scope claim

```go
// generateJWT 增加 scope
claims := jwt.MapClaims{
    "sub": username, "role": role,
    "scope": scope,   // 新增：JSON 字符串
    "exp":  time.Now().Add(24 * time.Hour).Unix(),
}
```

### 3.2 `RequireScope` 过滤中间件

```go
// 从 JWT 取 scope；admin(role==admin 或 scope=="") 不过滤
// 读接口按 scope 过滤：catalog(services) / devices / clusters / alerts
```

- 语义：user 角色的查询接口，若 `scope.services` 非空 → 只返回在列表内的 service；admin/空 scope → 全量。
- 涉及读接口：`/api/v1/catalog/services`、`/api/v1/devices`、`/api/v1/clusters`、`/api/v1/alerts/events`。

### 3.3 `/api/v1/me` 返回 scope；users PUT 支持编辑 scope

- `/api/v1/me` 返回 `{username, role, scope}`
- `/api/v1/users/{id}` PUT 支持 `scope` 字段（admin 可编辑）

---

## 4. 多集群：kubeconfig 注册 + 按集群查询

### 4.1 多集群 CRUD

| 端点 | 方法 | 权限 | 逻辑 |
|------|------|------|------|
| `/api/v1/clusters` | GET | 登录 | 集群列表（含多集群）|
| `/api/v1/clusters` | POST | admin | 新增集群（含 kubeconfig/api_server）|
| `/api/v1/clusters/{id}` | PUT/DELETE | admin | 编辑（更新 kubeconfig）/ 删除 |
| `/api/v1/clusters/{id}/nodes` | GET | 登录 | 用该集群 kubeconfig 查节点 |
| `/api/v1/clusters/{id}/namespaces` | GET | 登录 | 用该集群 kubeconfig 查命名空间 |
| `/api/v1/clusters/{id}/events` | GET | 登录 | 用该集群 kubeconfig 查事件 |

### 4.2 kubeconfig 加载（轻量，clientcmd）

```go
import "k8s.io/client-go/tools/clientcmd"

// 按集群 id 取 kubeconfig 内容 → clientcmd 构建 rest.Config
cfg, err := clientcmd.RESTConfigFromKubeConfig([]byte(cluster.Kubeconfig))
clientset, err := kubernetes.NewForConfig(cfg)
// 查 Node / Namespace / Event
```

- 不引入完整 client-go 自动发现；按需 `kubernetes.NewForConfig` 查询。
- 若 `Kubeconfig` 为空但 `ApiServer+Token` 有值 → 用 `rest.Config{Host, BearerToken, TLSClientConfig.Insecure}` 构造。
- **事件查询**：`clientset.CoreV1().Events("").List(...)` 按 `lastTimestamp` 排序返回异常事件。

---

## 5. 前端 Admin 页

### 5.1 路由与页面

- 新增 `/admin` 路由 + `pages/Admin/index.tsx`
- **单页多 Tab**（AntD Tabs）：

| Tab | 内容 |
|-----|------|
| **用户管理** | 用户列表 + CRUD + 角色切换(admin/user) + **scope 编辑**（服务/集群/设备 三个多选输入）|
| **审计日志** | 审计列表 + 按操作者/动作/服务过滤（复用 `/ops/audit-logs`）|
| **集群管理** | 集群列表 + **kubeconfig 上传/录入** + 连接测试 + 按集群查节点/命名空间/事件 |

### 5.2 client.ts 新增 API

- 用户：`updateUserScope`、`getMe`（scope）
- 集群：`createCluster`（含 kubeconfig）、`getClusterNodes`、`getClusterNamespaces`、`getClusterEvents`

---

## 6. 部署

- query-api：go.mod 加 `k8s.io/client-go` + `k8s.io/api` + `k8s.io/apimachinery`
- Helm：无新组件（复用现有 mysql）；query-api 保持当前 env

---

## 7. 测试

- **Go**：`users_scope_test.go`（scope 过滤：user 只返回 scope 内 service；admin 全量）、`clusters_kubeconfig_test.go`（kubeconfig 注册 + nodes/events，mock）
- **前端**：`tsc --noEmit` + `npm run build`
- **冒烟**：创建 user 配 scope → 登录用该 user 查 catalog 只看到 scope 内服务；admin 全量；上传 kubeconfig 注册集群 → 查节点

---

## 8. 风险

| 风险 | 概率 | 影响 | 缓解 |
|------|------|------|------|
| kubeconfig 泄露 | 中 | 高 | kubeconfig 字段仅在 admin 可读写；敏感信息脱敏显示 |
| scope 过滤破坏现有 admin | 低 | 高 | admin 不受限；user 默认空 scope = 无数据可见（需配置）|
| client-go 依赖引入编译负担 | 低 | 中 | 仅用 clientcmd + kubernetes 包，最小依赖 |
| 事件查询权限不足 | 中 | 中 | kubeconfig 需有 list events 权限；无权限降级提示 |

---

## 9. 自审

- [x] 无 TBD/TODO
- [x] 轻量化：砍角色三级（保留 admin/user + 用户级 scope）
- [x] 多集群用 kubeconfig 注册（非 client-go 全量发现）
- [x] 换自研命名，合规
- [x] 复用 store.EnsureSchema / RequireRole / clientcmd
