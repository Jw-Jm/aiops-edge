# Admin + RBAC + kubeconfig 多集群 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 新增 Admin 管理门户 + 用户 scope 权限过滤 + kubeconfig 多集群注册/查询。

**Architecture:** query-api（Go）改 users 加 scope、clusters 加 kubeconfig、JWT 加 scope claim、RequireScope 过滤、clientcmd 按集群查节点/事件；前端新增 `/admin` 单页多 Tab。

**Tech Stack:** Go, MySQL, k8s.io/client-go, React, AntD

## Global Constraints

- **砍掉角色三级**：保留 `admin`/`user` 两级，每用户直接配 scope
- scope 格式：`{"services":[],"clusters":[],"devices":[]}`，admin 或空 scope = 全量
- 多集群用 **kubeconfig 注册**（clusters 加 kubeconfig 字段），按集群用 clientcmd 查节点/命名空间/事件
- client-go 仅用 `clientcmd` + `kubernetes`，不用完整发现
- 换自研命名，合规
- 现有 Go test / 前端 tsc+build 不回归

---

### Task 1: users 表加 scope + User struct/DAO

**Files:**
- Modify: `ai-apm-query-go/internal/store/mysql.go`（users 表加 scope 列）
- Modify: `ai-apm-query-go/internal/store/users.go`（User struct + DAO）
- Test: `ai-apm-query-go/internal/store/users_scope_test.go`

**Interfaces:**
- Consumes: `GetDB()`
- Produces: `User.Scope string`；`UserDAO` 的 List/Get/Create/Update 支持 scope

- [ ] **Step 1: 写失败测试**

创建 `ai-apm-query-go/internal/store/users_scope_test.go`：

```go
package store

import "testing"

func TestUserScopeCRUD(t *testing.T) {
	if GetDB() == nil {
		t.Skip("mysql unavailable")
	}
	d := &UserDAO{}
	id, err := d.Create(&User{Username: "scopetest", PasswordHash: "x", Role: "user", Scope: `{"services":["a"]}`})
	if err != nil || id <= 0 {
		t.Fatalf("create failed: %v", err)
	}
	u, _ := d.GetByID(id)
	if u.Scope == "" {
		t.Fatal("scope not persisted")
	}
	_ = d.Delete(id)
}
```

- [ ] **Step 2: 运行确认失败**

Run: `cd ai-apm-query-go && go test ./internal/store/ -run TestUserScopeCRUD`
Expected: FAIL — `User.Scope` 字段不存在 / Create 无 scope 参数

- [ ] **Step 3: mysql.go users 表加 scope 列**

在 `ai-apm-query-go/internal/store/mysql.go` EnsureSchema 的 users 表 DDL 中 `status TINYINT DEFAULT 1,` 后加：

```sql
  scope VARCHAR(512) DEFAULT '',
```

并对已存在的表做兼容（在 EnsureSchema 的 users CREATE 之后追加）：

```go
	// 兼容已存在表：补 scope 列（幂等）
	rows, _ := conn.Query("SELECT COLUMN_NAME FROM information_schema.COLUMNS WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME='users' AND COLUMN_NAME='scope'")
	hasScope := rows.Next()
	rows.Close()
	if !hasScope {
		_, _ = conn.Exec("ALTER TABLE users ADD COLUMN scope VARCHAR(512) DEFAULT ''")
	}
```

- [ ] **Step 4: users.go 加 Scope 字段 + DAO 支持**

在 `User` struct 的 `Status int` 后加：

```go
	Scope        string    `json:"scope"`
```

修改所有 SQL 列（List L39、GetByUsername L64、GetByID L84、Create L104）：
- 查询 SELECT 加 `scope` 列，Scan 加 `&u.Scope`
- Create 的 INSERT 加 `scope` 列和 `u.Scope` 值
- Update（L113-129）加 scope 更新

- [ ] **Step 5: 运行确认通过**

Run: `cd ai-apm-query-go && go test ./internal/store/ -run TestUserScopeCRUD`
Expected: PASS

- [ ] **Step 6: 提交**

```bash
git add ai-apm-query-go/internal/store
git commit -m "feat(query-api): users 表加 scope 字段 + DAO 支持"
```

---

### Task 2: JWT scope claim + RequireScope + /me

**Files:**
- Modify: `ai-apm-query-go/internal/api/auth.go`
- Modify: `ai-apm-query-go/internal/api/users.go`
- Test: `ai-apm-query-go/internal/api/users_scope_test.go`

**Interfaces:**
- Consumes: `User.Scope`（Task 1）
- Produces: `generateJWT` 携带 scope；`validateJWT` 返回 scope；`RequireScope(...)` 中间件；`/me` 返回 scope；users PUT 支持 scope

- [ ] **Step 1: 写失败测试**

创建 `ai-apm-query-go/internal/api/users_scope_test.go`：

```go
package api

import "testing"

func TestScopeParsing(t *testing.T) {
	scope := parseScope(`{"services":["a","b"],"clusters":[],"devices":[]}`)
	if !scope.ContainsService("a") || scope.ContainsService("c") {
		t.Fatal("service scope parse wrong")
	}
	if scope.IsFull() {
		t.Fatal("non-empty scope should not be full")
	}
	if !parseScope("").IsFull() {
		t.Fatal("empty scope should be full")
	}
}
```

- [ ] **Step 2: 运行确认失败**

Run: `cd ai-apm-query-go && go test ./internal/api/ -run TestScopeParsing`
Expected: FAIL — `parseScope` 未定义

- [ ] **Step 3: auth.go 实现 scope 解析 + JWT claim + RequireScope**

在 `ai-apm-query-go/internal/api/auth.go` 加：

```go
import "encoding/json"

// Scope 数据范围（三维）。
type Scope struct {
	Services []string `json:"services"`
	Clusters []string `json:"clusters"`
	Devices  []string `json:"devices"`
}

func (s *Scope) IsFull() bool {
	return s == nil || (len(s.Services) == 0 && len(s.Clusters) == 0 && len(s.Devices) == 0)
}
func (s *Scope) ContainsService(name string) bool {
	if s == nil || s.IsFull() { return true }
	for _, x := range s.Services { if x == name { return true } }
	return false
}
func (s *Scope) ContainsCluster(name string) bool {
	if s == nil || s.IsFull() { return true }
	for _, x := range s.Clusters { if x == name { return true } }
	return false
}
func (s *Scope) ContainsDevice(name string) bool {
	if s == nil || s.IsFull() { return true }
	for _, x := range s.Devices { if x == name { return true } }
	return false
}

func parseScope(raw string) *Scope {
	sc := &Scope{}
	if raw == "" { return sc }
	_ = json.Unmarshal([]byte(raw), sc)
	return sc
}
```

修改 `generateJWT` 增加 scope 参数和 claim；`validateJWT` 返回 scope：

```go
func generateJWT(username, role, scope string) string {
	claims := jwt.MapClaims{
		"sub": username, "role": role, "scope": scope,
		"exp": time.Now().Add(24 * time.Hour).Unix(),
	}
	...
}
func validateJWT(tokenStr string) (string, string, string, bool) { // 返回 username, role, scope, ok
	...
	scope, _ := claims["scope"].(string)
	return username, role, scope, true
}
```

更新 `Login`（L73、L85）传 `u.Scope`；`RequireRole`（L102）适配新签名。

新增 `RequireScope`（按服务名过滤的读接口辅助，返回当前用户 scope）：

```go
func currentScope(r *http.Request) *Scope {
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	_, _, scope, ok := validateJWT(token)
	if !ok { return &Scope{} }
	return parseScope(scope)
}
```

- [ ] **Step 4: users.go /me 返回 scope + PUT 编辑 scope**

在 `ai-apm-query-go/internal/api/users.go` 的 `/me` handler 返回 `scope`；users Update 请求体加 `scope` 字段并调用 `UserDAO.Update` 持久化 scope。

- [ ] **Step 5: 运行确认通过**

Run: `cd ai-apm-query-go && go build ./... && go test ./internal/api/`
Expected: PASS

- [ ] **Step 6: 提交**

```bash
git add ai-apm-query-go/internal/api
git commit -m "feat(query-api): JWT scope claim + RequireScope + /me scope"
```

---

### Task 3: 读接口按 scope 过滤

**Files:**
- Modify: `ai-apm-query-go/internal/api/catalog.go`
- Modify: `ai-apm-query-go/internal/api/devices.go`
- Modify: `ai-apm-query-go/internal/api/clusters.go`
- Modify: `ai-apm-query-go/internal/api/alerts.go`
- Test: `ai-apm-query-go/internal/api/users_scope_test.go`

**Interfaces:**
- Consumes: `currentScope(r)`（Task 2）
- Produces: user 角色的 catalog/devices/clusters/alerts 读接口按 scope 过滤

- [ ] **Step 1: 写失败测试（scope 过滤逻辑）**

在 `users_scope_test.go` 追加：

```go
func TestScopeFilterService(t *testing.T) {
	sc := parseScope(`{"services":["a"]}`)
	if !sc.ContainsService("a") || sc.ContainsService("b") {
		t.Fatal("service filter wrong")
	}
	if !sc.ContainsCluster("any") { // clusters 未限定 => 全通过
		t.Fatal("unscoped dimension should pass")
	}
}
```

- [ ] **Step 2: catalog.go 服务列表按 scope 过滤**

在 `/api/v1/catalog/services` GET handler 中，取 `sc := currentScope(r)`，若非全量则只返回 `sc.ContainsService(service_name)` 的行。

- [ ] **Step 3: devices.go / clusters.go 过滤**

- devices：非全量 scope 时只返回 `sc.ContainsDevice(hostname)`
- clusters：非全量 scope 时只返回 `sc.ContainsCluster(name)`

- [ ] **Step 4: alerts.go 事件列表过滤**

- 告警事件列表：非全量 scope 时只返回 `sc.ContainsService(service)`

- [ ] **Step 5: 运行确认通过**

Run: `cd ai-apm-query-go && go build ./... && go test ./internal/api/`
Expected: PASS

- [ ] **Step 6: 提交**

```bash
git add ai-apm-query-go/internal/api
git commit -m "feat(query-api): catalog/devices/clusters/alerts 按用户 scope 过滤"
```

---

### Task 4: clusters 加 kubeconfig + 多集群 CRUD

**Files:**
- Modify: `ai-apm-query-go/internal/store/mysql.go`（clusters 加 kubeconfig/api_server 兼容）
- Modify: `ai-apm-query-go/internal/store/clusters.go`（Cluster struct + DAO）
- Modify: `ai-apm-query-go/internal/api/clusters.go`
- Test: `ai-apm-query-go/internal/store/clusters_kubeconfig_test.go`

**Interfaces:**
- Consumes: `GetDB()`
- Produces: `Cluster.Kubeconfig string`；clusters CRUD 支持 kubeconfig

- [ ] **Step 1: 写失败测试**

创建 `ai-apm-query-go/internal/store/clusters_kubeconfig_test.go`：

```go
package store

import "testing"

func TestClusterKubeconfig(t *testing.T) {
	if GetDB() == nil {
		t.Skip("mysql unavailable")
	}
	d := &ClusterDAO{}
	id, err := d.Upsert(&Cluster{Name: "kube-test", Provider: "onprem", Kubeconfig: "dummy"})
	if err != nil || id <= 0 {
		t.Fatalf("upsert failed: %v", err)
	}
	cl, _ := d.GetByName("kube-test")
	if cl == nil || cl.Kubeconfig == "" {
		t.Fatal("kubeconfig not persisted")
	}
	_ = d.Delete(id)
}
```

- [ ] **Step 2: 运行确认失败**

Run: `cd ai-apm-query-go && go test ./internal/store/ -run TestClusterKubeconfig`
Expected: FAIL — `Cluster.Kubeconfig` 不存在

- [ ] **Step 3: mysql.go clusters 表加 kubeconfig 列**

在 clusters DDL 中 `api_server VARCHAR(255) DEFAULT '',` 后加 `kubeconfig TEXT,`，并对已存在表加兼容 ALTER（同 Task 1 Step 3 模式，表名 clusters，列 kubeconfig）。

- [ ] **Step 4: clusters.go 加 Kubeconfig 字段 + DAO**

`Cluster` struct 加 `Kubeconfig string \`json:"kubeconfig"\``；List/GetByName/Upsert/Update 的 SQL 加 kubeconfig 列（kubeconfig 敏感，List 可返回但不回显全量——为简单，List 返回 kubeconfig 字段但前端脱敏）。

- [ ] **Step 5: clusters.go API 支持 kubeconfig 读写**

clusters POST/PUT 请求体加 `kubeconfig` 字段；响应含 `kubeconfig`（前端脱敏显示）。

- [ ] **Step 6: 运行确认通过**

Run: `cd ai-apm-query-go && go build ./... && go test ./internal/store/`
Expected: PASS

- [ ] **Step 7: 提交**

```bash
git add ai-apm-query-go/internal/store ai-apm-query-go/internal/api
git commit -m "feat(query-api): clusters 加 kubeconfig 字段 + 多集群 CRUD"
```

---

### Task 5: 按集群查节点/命名空间/事件（clientcmd）

**Files:**
- Modify: `ai-apm-query-go/go.mod`（加 k8s.io/client-go）
- Modify: `ai-apm-query-go/internal/api/clusters.go`
- Create: `ai-apm-query-go/internal/api/cluster_k8s.go`
- Test: `ai-apm-query-go/internal/api/cluster_k8s_test.go`

**Interfaces:**
- Consumes: `Cluster.Kubeconfig`（Task 4）
- Produces: `/clusters/{id}/nodes`、`/clusters/{id}/namespaces`、`/clusters/{id}/events`

- [ ] **Step 1: 加 client-go 依赖**

Run: `cd ai-apm-query-go && go get k8s.io/client-go@latest k8s.io/api@latest k8s.io/apimachinery@latest`

- [ ] **Step 2: 写失败测试**

创建 `ai-apm-query-go/internal/api/cluster_k8s_test.go`：

```go
package api

import "testing"

func TestBuildK8sClient(t *testing.T) {
	if _, err := newClusterClient("not-a-valid-kubeconfig"); err == nil {
		t.Fatal("invalid kubeconfig should error")
	}
}
```

- [ ] **Step 3: 实现 kubeconfig 客户端**

创建 `ai-apm-query-go/internal/api/cluster_k8s.go`：

```go
package api

import (
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

// newClusterClient 用 kubeconfig 内容构建 K8s clientset。
func newClusterClient(kubeconfig string) (*kubernetes.Clientset, error) {
	cfg, err := clientcmd.RESTConfigFromKubeConfig([]byte(kubeconfig))
	if err != nil {
		return nil, err
	}
	return kubernetes.NewForConfig(cfg)
}
```

- [ ] **Step 4: 实现 /clusters/{id}/nodes**

用 `clientset.CoreV1().Nodes().List()` 返回节点列表（name/status/ip/os）。

- [ ] **Step 5: 实现 /clusters/{id}/namespaces**

用 `clientset.CoreV1().Namespaces().List()`。

- [ ] **Step 6: 实现 /clusters/{id}/events**

用 `clientset.CoreV1().Events("").List()` 按 `LastTimestamp` 排序返回异常事件。

- [ ] **Step 7: 路由注册**

在 `cmd/api/main.go` 注册 `/clusters/{id}/nodes|namespaces|events` 路由（admin + 登录用户均可读，按 scope 过滤集群）。

- [ ] **Step 8: 运行确认通过**

Run: `cd ai-apm-query-go && go build ./... && go test ./internal/api/`
Expected: PASS

- [ ] **Step 9: 提交**

```bash
git add ai-apm-query-go
git commit -m "feat(query-api): kubeconfig 按集群查节点/命名空间/事件"
```

---

### Task 6: 前端 Admin 页（单页多 Tab）

**Files:**
- Modify: `observability-frontend/src/api/client.ts`
- Create: `observability-frontend/src/pages/Admin/index.tsx`
- Modify: `observability-frontend/src/App.tsx`
- Test: `tsc --noEmit` + `npm run build`

**Interfaces:**
- Consumes: Task 2-5 的 API
- Produces: `/admin` 路由（用户管理含 scope / 审计日志 / 集群管理含 kubeconfig）

- [ ] **Step 1: client.ts 加 API**

```ts
// ===== Admin =====
export const getMe = () => api.get('/me')
export const updateUserScope = (id: number, data: Record<string, any>) => api.put(`/users/${id}`, data)
export const createCluster = (data: Record<string, any>) => api.post('/clusters', data)
export const getClusterNodes = (id: number) => api.get(`/clusters/${id}/nodes`)
export const getClusterNamespaces = (id: number) => api.get(`/clusters/${id}/namespaces`)
export const getClusterEvents = (id: number) => api.get(`/clusters/${id}/events`)
```

- [ ] **Step 2: 创建 Admin 页面**

创建 `observability-frontend/src/pages/Admin/index.tsx`：AntD Tabs，三个 Tab（用户管理 / 审计日志 / 集群管理）。用户 Tab 复用用户 CRUD + scope 编辑（服务/集群/设备三个多选）；审计 Tab 调 `/ops/audit-logs`；集群 Tab 集群 CRUD + kubeconfig 录入 + 查节点/事件。

- [ ] **Step 3: 注册路由**

`App.tsx` 加 `import Admin from './pages/Admin'` + `<Route path="/admin" element={<Admin />} />` + 侧边栏"管理"分组加菜单项。

- [ ] **Step 4: tsc + build 验证**

Run: `cd observability-frontend && node_modules/.bin/tsc --noEmit && npm run build`
Expected: exit 0

- [ ] **Step 5: 提交**

```bash
git add observability-frontend/src
git commit -m "feat(web): Admin 管理门户（用户 scope + 审计 + 集群 kubeconfig）"
```

---

### Task 7: 部署 + 冒烟验证

- [ ] **Step 1: 重建 query-api + 前端镜像并部署**

query-api 需重新 build（新增 client-go 依赖）；前端用离线方式重建。

- [ ] **Step 2: 冒烟**

- 创建 user 配 scope → 登录该 user 查 catalog 只看到 scope 内服务；admin 全量
- `/me` 返回 scope
- 上传 kubeconfig 注册集群 → 查节点/事件
- `/admin` 页三个 Tab 正常

---

## Self-Review

**1. Spec coverage:** 覆盖 admin-rbac-multicluster spec 全部（scope / RequireScope / kubeconfig 多集群 / Admin 页）。
**2. Placeholder scan:** 无 TBD/TODO；kubeconfig 脱敏、compat ALTER 均明确。
**3. Type consistency:** `parseScope`/`Scope.Contains*`/`newClusterClient`/`getMe` 等跨 Task 命名一致。
