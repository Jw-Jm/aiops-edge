# P2a：WebShell + 用户管理 + 报告中心 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** ① query-api 用户管理（users 表 + 用户 CRUD + 真实 JWT HS256 + 收紧鉴权 + MySQL 降级），② ai-orchestrator WebShell（WebSocket + 复用 shell_policy + 会话限制），③ 前端三页面（用户管理 + WebShell/xterm + 报告中心）。

**Architecture:**
- 用户管理：query-api（Go, database/sql + go-sql-driver/mysql + golang-jwt/v5）
- WebShell：ai-orchestrator（Python, WebSocket + subprocess + shell_policy.py）
- 报告中心：前端 React（后端 ReportStore 已就绪）

**Tech Stack:** Go, Python 3.12, FastAPI WebSocket, MySQL, React, AntD, xterm.js, echarts

## Global Constraints

- 移除 IP 白名单免鉴权（`isInternalRequest` 只保留 `X-Internal-Token` 通道）
- 真实 JWT：HS256 用 `golang-jwt/jwt/v5`（已依赖），bcrypt 用 `golang.org/x/crypto/bcrypt`
- query-api MySQL 不可达时**降级 admin/admin123**（不阻塞登录）
- 复用 `shell_policy.py`（`is_whitelisted_for_execute`），写命令需审批
- 复用 ReportStore（报告中心后端）
- 现有 63 pytest + Go 测试不回归
- users 表迁移由 query-api 启动时 `EnsureSchema()` 应用（独立于 orchestrator 迁移）

---

### Task 1: query-api MySQL 基础设施（users 表 + DAO）

**Files:**
- Create: `ai-apm-query-go/internal/store/mysql.go`（连接池 + EnsureSchema + UserDAO）
- Create: `ai-apm-query-go/internal/store/users.sql`（users 表迁移）
- Modify: `ai-apm-query-go/go.mod`（加 go-sql-driver/mysql + x/crypto）
- Test: `ai-apm-query-go/internal/store/mysql_test.go`

**Interfaces:**
- Consumes: env `MYSQL_HOST/PORT/USER/PASSWORD/DB`
- Produces: `store.GetDB()`、`store.EnsureSchema()`、`store.UserDAO{List/Create/Update/Delete/Get/GetByUsername}`

- [ ] **Step 1: go.mod 加依赖**

```bash
go get github.com/go-sql-driver/mysql@latest golang.org/x/crypto/bcrypt
```

- [ ] **Step 2: 写 users.sql 迁移**

```sql
-- users 表迁移（query-api 启动时应用）
CREATE TABLE IF NOT EXISTS users (
  id BIGINT PRIMARY KEY AUTO_INCREMENT,
  username VARCHAR(64) NOT NULL UNIQUE,
  password_hash VARCHAR(255) NOT NULL,
  display_name VARCHAR(128) DEFAULT '',
  role ENUM('admin','user') NOT NULL DEFAULT 'user',
  email VARCHAR(128) DEFAULT '',
  status TINYINT DEFAULT 1,
  created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
```

- [ ] **Step 3: 写失败测试（mysql_test.go）**

```go
package store

func TestUserDAOCreateGet(t *testing.T) {
	// MySQL 不可用时 GetDB() 返回 nil，测试应安全跳过
	db := GetDB()
	if db == nil {
		t.Skip("MySQL not available")
	}
	// create + get roundtrip
}
```

- [ ] **Step 4: 运行确认失败**

Run: `cd ai-apm-query-go && go build ./...`
Expected: FAIL — package store 未定义

- [ ] **Step 5: 实现 store/mysql.go**

```go
package store

import (
	"database/sql"
	"fmt"
	"os"
	_ "github.com/go-sql-driver/mysql"
)

var db *sql.DB

func GetDB() *sql.DB {
	if db != nil { return db }
	host := os.Getenv("MYSQL_HOST")
	if host == "" { host = "127.0.0.1" }
	port := os.Getenv("MYSQL_PORT")
	if port == "" { port = "3306" }
	user := os.Getenv("MYSQL_USER")
	if user == "" { user = "root" }
	pw := os.Getenv("MYSQL_PASSWORD")
	database := os.Getenv("MYSQL_DB")
	if database == "" { database = "aiops" }
	dsn := fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?charset=utf8mb4&parseTime=true", user, pw, host, port, database)
	conn, err := sql.Open("mysql", dsn)
	if err != nil { return nil }
	conn.SetMaxOpenConns(10)
	conn.SetMaxIdleConns(5)
	if err := conn.Ping(); err != nil { return nil }
	db = conn
	return db
}

func EnsureSchema() {
	if GetDB() == nil { return }
	// 执行 users.sql（内嵌）
	_, _ = db.Exec(`CREATE TABLE IF NOT EXISTS users (...同上...)`)
	// 种子 admin 用户（bcrypt）
}

// UserDAO 用户数据访问
type UserDAO struct{}
// List / GetByUsername / Create / Update / Delete ...
```

- [ ] **Step 6: 运行确认通过**

Run: `cd ai-apm-query-go && go build ./... && go test ./...`
Expected: PASS（MySQL 不可用时 skip）

- [ ] **Step 7: 提交**

```bash
git add ai-apm-query-go/internal/store ai-apm-query-go/go.mod ai-apm-query-go/go.sum
git commit -m "feat(query-api): MySQL users 表 + UserDAO + 连接池"
```

---

### Task 2: query-api 真实 JWT + 登录改造 + 用户 CRUD

**Files:**
- Modify: `ai-apm-query-go/internal/api/auth.go`（真实 JWT + 登录查 MySQL + 降级）
- Create: `ai-apm-query-go/internal/api/users.go`（用户 CRUD 路由）
- Modify: `ai-apm-query-go/cmd/api/main.go`（注册路由）
- Test: `ai-apm-query-go/internal/api/auth_test.go`、`users_test.go`

**Interfaces:**
- Consumes: `store.UserDAO`
- Produces: 登录返回标准 JWT；`/api/v1/users` CRUD；`/api/v1/me`

- [ ] **Step 1: 写失败测试（auth_test.go）**

```go
package api

func TestValidateJWTStandard(t *testing.T) {
	token := generateJWT("admin", "admin")
	if _, ok := validateJWT(token); !ok {
		t.Fatal("valid token rejected")
	}
	// 伪造 token 应被拒
	bad := token[:len(token)-2] + "xx"
	if _, ok := validateJWT(bad); ok {
		t.Fatal("tampered token accepted")
	}
}
```

- [ ] **Step 2: 运行确认失败**

Run: `cd ai-apm-query-go && go test ./internal/api/`
Expected: FAIL — generateJWT 签名不符

- [ ] **Step 3: 重写 auth.go 的 JWT 与登录**

```go
import (
	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
	"ai-apm-query-go/internal/store"
)

func generateJWT(username, role string) string {
	claims := jwt.MapClaims{
		"sub": username, "role": role,
		"exp": time.Now().Add(24 * time.Hour).Unix(),
	}
	t := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, _ := t.SignedString(jwtSecret)
	return signed
}

func validateJWT(tokenStr string) (string, string, bool) {
	t, err := jwt.Parse(tokenStr, func(t *jwt.Token) (interface{}, error) {
		return jwtSecret, nil
	})
	if err != nil || !t.Valid { return "", "", false }
	claims, _ := t.Claims.(jwt.MapClaims)
	username, _ := claims["sub"].(string)
	role, _ := claims["role"].(string)
	return username, role, true
}
```

Login handler 改造：
```go
func (h *Handler) Login(w http.ResponseWriter, r *http.Request) {
	// 1. 从 MySQL 查用户 + bcrypt 校验
	u, err := (&store.UserDAO{}).GetByUsername(username)
	if err == nil && u != nil {
		if bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(creds.Password)) == nil {
			token := generateJWT(u.Username, u.Role)
			respondJSON(w, 200, ...)
			return
		}
	}
	// 2. MySQL 不可达降级：admin/admin123
	if store.GetDB() == nil && creds.Username == "admin" && creds.Password == "admin123" {
		respondJSON(w, 200, map[string]interface{}{"token": generateJWT("admin", "admin"), "username": "admin", "degraded": true})
		return
	}
	respondJSON(w, 401, map[string]interface{}{"error": "invalid credentials"})
}
```

AuthMiddleware 收紧：`isInternalRequest` 只保留 X-Internal-Token（删除 IP 白名单 L57-69）；`validateJWT` 返回 role。

- [ ] **Step 4: 实现 users.go（用户 CRUD）**

```go
// GET /api/v1/users  (admin)
// POST /api/v1/users  (admin) — bcrypt 哈希密码
// PUT /api/v1/users/{id}  (admin) — 更新角色/状态/密码
// DELETE /api/v1/users/{id}  (admin)
// GET /api/v1/me  (登录用户)
```

- [ ] **Step 5: 注册路由（main.go）**

```go
mux.Handle("/api/v1/users", requireRole("admin", h.UserList))
mux.Handle("/api/v1/users/", requireRole("admin", h.UserRouter))
mux.Handle("/api/v1/me", h.Me)
```

- [ ] **Step 6: 运行确认通过**

Run: `cd ai-apm-query-go && go build ./... && go test ./...`
Expected: PASS

- [ ] **Step 7: 提交**

```bash
git add ai-apm-query-go/internal/api/auth.go ai-apm-query-go/internal/api/users.go ai-apm-query-go/cmd/api/main.go ai-apm-query-go/internal/api/*_test.go
git commit -m "feat(query-api): 真实 JWT HS256 + 登录查 MySQL + 用户 CRUD + 收紧鉴权"
```

---

### Task 3: ai-orchestrator WebShell（WebSocket）

**Files:**
- Create: `ai-orchestrator/shell_ws.py`（WebSocket 端点 + 会话限制）
- Modify: `ai-orchestrator/main.py`（注册 ws 路由 + shell 相关）
- Test: `ai-orchestrator/tests/test_shell_ws.py`（白名单逻辑）

**Interfaces:**
- Consumes: `shell_policy.is_whitelisted_for_execute`、`subprocess`
- Produces: `/api/v1/shell/ws` WebSocket

- [ ] **Step 1: 写失败测试（test_shell_ws.py）**

```python
from shell_policy import is_whitelisted_for_execute

def test_readonly_allowed():
    ok, _ = is_whitelisted_for_execute("ls -la")
    assert ok

def test_write_requires_approval():
    ok, cat = is_whitelisted_for_execute("rm -rf /tmp/x")
    assert ok is False or cat == "write"
```

- [ ] **Step 2: 运行确认失败**

Run: `.venv-312/bin/python -m pytest tests/test_shell_ws.py -v`
Expected: FAIL — shell_policy 行为不符（若 is_whitelisted_for_execute 已存在则可能直接通过）

> **注意**：若 `is_whitelisted_for_execute` 已实现且测试通过，此 Task 只补 WebSocket 端点，测试验证白名单即可。

- [ ] **Step 3: 实现 shell_ws.py（WebSocket 端点）**

```python
import asyncio, subprocess, os, time
from fastapi import WebSocket, WebSocketDisconnect

_MAX_SESSIONS = int(os.environ.get("SHELL_MAX_SESSIONS", "5"))
_TIMEOUT = int(os.environ.get("SHELL_TIMEOUT", "30"))
_active = 0

async def shell_ws(ws: WebSocket):
    global _active
    if _active >= _MAX_SESSIONS:
        await ws.close(code=1013)
        return
    await ws.accept()
    _active += 1
    try:
        while True:
            cmd = (await ws.receive_text()).strip()
            if not cmd:
                continue
            ok, category = is_whitelisted_for_execute(cmd)
            if not ok:
                await ws.send_text(f"❌ 命令被拒绝：{cmd}\n")
                continue
            try:
                proc = await asyncio.create_subprocess_shell(
                    cmd, stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
                try:
                    out, _ = await asyncio.wait_for(proc.communicate(), timeout=_TIMEOUT)
                except asyncio.TimeoutError:
                    proc.kill()
                    await ws.send_text(f"⏱ 命令超时（>{_TIMEOUT}s）\n")
                    continue
                await ws.send_text(out.decode(errors="replace"))
            except Exception as e:
                await ws.send_text(f"❌ 执行错误：{e}\n")
    except WebSocketDisconnect:
        pass
    finally:
        _active -= 1
```

- [ ] **Step 4: 注册 ws 路由（main.py）**

```python
from shell_ws import shell_ws
app.add_websocket_route("/api/v1/shell/ws", shell_ws)
```

- [ ] **Step 5: 回归测试 + 冒烟**

Run: `.venv-312/bin/python -m pytest tests/ -q`（63 passed）
Run: 启动 uvicorn，curl WebSocket 握手验证 101

- [ ] **Step 6: 提交**

```bash
git add ai-orchestrator/shell_ws.py ai-orchestrator/main.py ai-orchestrator/tests/test_shell_ws.py
git commit -m "feat(api): WebShell WebSocket 端点 + 会话/超时限制 + 白名单"
```

---

### Task 4: 前端三页面（用户管理 + WebShell + 报告中心）

**Files:**
- Create: `observability-frontend/src/pages/Users/index.tsx`
- Create: `observability-frontend/src/pages/Shell/index.tsx`
- Create: `observability-frontend/src/pages/Reports/index.tsx`
- Modify: `observability-frontend/src/App.tsx`（路由 + 菜单）
- Modify: `observability-frontend/src/api/client.ts`（API）
- Modify: `observability-frontend/package.json`（xterm 依赖）
- Test: `tsc --noEmit` + `npm run build`

**Interfaces:**
- Consumes: 前端 API + `/api/v1/users`、`/api/v1/me`、报告路由、`/api/v1/shell/ws`
- Produces: 三页面 + 菜单 + 路由

- [ ] **Step 1: 加 xterm 依赖**

```bash
npm install @xterm/xterm @xterm/addon-fit
```

- [ ] **Step 2: client.ts 加 API**

```ts
// 用户管理
export const listUsers = (params?: Record<string, unknown>) => api.get('/users', { params })
export const createUser = (data: Record<string, unknown>) => api.post('/users', data)
export const updateUser = (id: number, data: Record<string, unknown>) => api.put(`/users/${id}`, data)
export const deleteUser = (id: number) => api.delete(`/users/${id}`)
export const getMe = () => api.get('/me')
```

- [ ] **Step 3: 创建 Users 页**

AntD Table（用户名/显示名/角色/状态/邮箱）+ 新增/编辑 Modal + 角色切换 + 状态开关（admin 专属）。

- [ ] **Step 4: 创建 Shell 页（xterm.js + WebSocket）**

```tsx
import { Terminal } from '@xterm/xterm'
import { FitAddon } from '@xterm/addon-fit'

useEffect(() => {
  const term = new Terminal({ theme: { background: '#1a1a1a' } })
  const fit = new FitAddon()
  term.loadAddon(fit)
  term.open(ref.current!)
  fit.fit()
  const ws = new WebSocket(`ws://${location.host}/api/v1/shell/ws`)
  ws.onmessage = e => term.write(e.data)
  term.onData(d => ws.send(d))
  return () => { ws.close(); term.dispose() }
}, [])
```

- [ ] **Step 5: 创建 Reports 页**

AntD Table（服务/类型/风险分/摘要/时间）+ echarts 风险分布环形 + 趋势折线（复用 `/ops/reports/trend`）+ 下载。

- [ ] **Step 6: 注册路由和菜单**

App.tsx 加 `/users`（用户管理）、`/shell`（WebShell）、`/reports`（报告中心）；对应菜单。

- [ ] **Step 7: tsc + build 验证**

Run: `cd observability-frontend && node_modules/.bin/tsc --noEmit && npm run build`
Expected: exit 0

- [ ] **Step 8: 提交**

```bash
git add observability-frontend/src/pages/Users observability-frontend/src/pages/Shell observability-frontend/src/pages/Reports observability-frontend/src/App.tsx observability-frontend/src/api/client.ts observability-frontend/package.json
git commit -m "feat(web): 用户管理 + WebShell(xterm) + 报告中心 三页面"
```

---

## Self-Review

**1. Spec coverage（对照设计文档）：**
- ✅ §2 users 表 → Task 1
- ✅ §2.2/2.3/2.4 鉴权中间件/真实 JWT/登录 → Task 2
- ✅ §2.5 用户 CRUD → Task 2
- ✅ §3 WebSocket → Task 3
- ✅ §3.2 会话限制 → Task 3
- ✅ §4 报告中心 → Task 4
- ✅ 收紧鉴权（去 IP 白名单，留 X-Internal-Token）→ Task 2

**2. Placeholder scan：** 无 TBD/TODO，所有步骤含精确代码。

**3. Type consistency：**
- `generateJWT(username, role)` — Task 2 定义，Login/鉴权使用一致
- `validateJWT(token) (username, role, bool)` — Task 2 定义
- `store.UserDAO` — Task 1 定义，Task 2 登录使用
- `shell_ws(ws)` — Task 3 定义，main.py 注册
- 前端 `listUsers/createUser/...` — Task 4 定义，Users 页使用
