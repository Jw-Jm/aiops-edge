# P2a：WebShell + 用户管理 + 报告中心

**日期**: 2026-08-08
**范围**: ai-orchestrator（Python WebShell）+ query-api（Go 用户管理/鉴权）+ observability-frontend（React 三页面）

## 1. 范围与决策（已确认）

| 子项 | 决策 |
|------|------|
| WebShell | **WebSocket + xterm.js**，orchestrator 加 ws 端点 |
| WebShell 安全 | 复用 `shell_policy.py`（黑白名单 + 危险命令）；只读命令直接执行，写命令需审批；会话并发/超时限制 |
| 用户管理 | **完整改造**：users 表 + 用户 CRUD + 角色(admin/user) + 真实 JWT(HS256) + 收紧鉴权 |
| users 存储 | **query-api 连 MySQL**（`database/sql` + `go-sql-driver/mysql`，轻量 DAO，无 ORM）|
| MySQL 降级 | query-api MySQL 不可达时降级 admin 凭据（不阻塞登录）|
| 鉴权收紧 | **去掉 IP 白名单免鉴权**，保留 `X-Internal-Token` 内部通道（orchestrator 已带）|
| 报告中心 | 独立前端页面（后端 ReportStore 已就绪）|

---

## 2. 用户管理设计（query-api, Go）

### 2.1 users 表（MySQL，新增迁移）

```sql
CREATE TABLE IF NOT EXISTS users (
    id BIGINT PRIMARY KEY AUTO_INCREMENT,
    username VARCHAR(64) NOT NULL UNIQUE,
    password_hash VARCHAR(255) NOT NULL,   -- bcrypt
    display_name VARCHAR(128) DEFAULT '',
    role ENUM('admin','user') NOT NULL DEFAULT 'user',
    email VARCHAR(128) DEFAULT '',
    status TINYINT DEFAULT 1,              -- 1 启用 / 0 禁用
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP
);
```

### 2.2 鉴权中间件（收紧）

```go
// AuthMiddleware 改造：
// 1. 内部服务间调用：保留 X-Internal-Token 校验（orchestrator 等）
// 2. 其余所有请求：必须带合法 JWT（HS256，标准 jwt/v5）
// 3. 移除 isInternalRequest 的 IP 白名单免鉴权
// 4. 公开端点白名单：/api/v1/login、/health 等
```

### 2.3 真实 JWT（HS256）

```go
// 用 golang-jwt/jwt/v5 生成标准 JWT
claims := jwt.MapClaims{
    "sub": username,
    "role": role,
    "exp": time.Now().Add(24 * time.Hour).Unix(),
}
token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
signed, _ := token.SignedString([]byte(jwtSecret))
```

### 2.4 登录（改造 Login handler）

```go
// 1. 从 MySQL users 表查用户（bcrypt 校验密码）
// 2. MySQL 不可达时降级：校验内置 admin/admin123（仅降级路径）
// 3. 签发 HS256 JWT 返回
```

### 2.5 用户 CRUD 路由（新增）

| 端点 | 方法 | 权限 | 逻辑 |
|------|------|------|------|
| `/api/v1/users` | GET | admin | 用户列表（分页）|
| `/api/v1/users` | POST | admin | 创建用户（bcrypt 哈希）|
| `/api/v1/users/{id}` | PUT | admin | 更新用户（角色/状态/密码）|
| `/api/v1/users/{id}` | DELETE | admin | 删除用户 |
| `/api/v1/me` | GET | 登录用户 | 当前用户信息 |

### 2.6 前端
- **Login 页**：登录后存 token（现有 localStorage）
- **用户管理页** `/users`（admin）：用户列表 + 增删改 + 角色切换 + 状态开关

---

## 3. WebShell 设计（ai-orchestrator, Python）

### 3.1 WebSocket 端点

```python
@app.websocket("/api/v1/shell/ws")
async def shell_ws(ws: WebSocket):
    await ws.accept()
    # 会话限制：并发数、空闲超时
    async for message in ws.iter_text():
        # 1. shell_policy.check() 危险命令拦截
        # 2. is_whitelisted_for_execute() 判断只读/写
        # 3. 写操作要求预先审批（状态标记）
        # 4. subprocess 执行，stdout/stderr 实时回传
```

### 3.2 会话限制

- 最大并发会话数（env `SHELL_MAX_SESSIONS`，默认 5）
- 单命令超时（env `SHELL_TIMEOUT`，默认 30s）
- 空闲超时断开（默认 5min）

### 3.3 前端 xterm 页 `/shell`

- 依赖：`@xterm/xterm` + `@xterm/addon-fit`（npm 新增）
- WebSocket 连接，xterm.js 渲染，输入回传 + 输出显示
- 命令只读优先：危险命令前端灰化提示

---

## 4. 报告中心（前端独立页）

后端已就绪（`/ops/reports/history`、`/ops/reports/trend`、ReportStore）。前端新建 `/reports` 页：
- 报告列表（服务/类型/风险分/摘要/时间）
- 风险分布图（echarts 环形）
- 趋势图（echarts 折线，复用 trend）
- 下载入口（复用现有 MinIO 下载）

---

## 5. 部署

- query-api：go.mod 加 `go-sql-driver/mysql`；deployment 注入 `MYSQL_HOST/PORT/USER/PASSWORD/DB`
- ai-orchestrator：无新增后端依赖（subprocess + WebSocket 均标准库/已有）；前端加 xterm 依赖
- Helm：query-api 加 MySQL env；前端镜像含新页面

---

## 6. 测试

- **Go**：`auth_test.go`（JWT HS256 签发/校验、bcrypt 校验）、`user_crud_test.go`、鉴权中间件（拒绝无 token、拒绝伪造 token、内部 token 放行）
- **Python**：`test_shell_policy.py`（复用）+ 新 ws 逻辑单测（白名单、写审批）
- **前端**：`tsc --noEmit` + `npm run build`
- **冒烟**：登录获取 JWT → 用户 CRUD → WebShell ws 连接 → 报告中心数据

---

## 7. 风险

| 风险 | 概率 | 影响 | 缓解 |
|------|------|------|------|
| 收紧鉴权破坏现有集成 | 中 | 高 | 保留 X-Internal-Token 通道；回归测试 |
| MySQL 不可达影响登录 | 低 | 高 | 降级 admin 凭据 |
| WebSocket 安全（命令注入）| 中 | 高 | shell_policy 双重校验 + 写审批 |
| bcrypt 密码泄露 | 低 | 高 | bcrypt 成本因子 + 不落明文 |
| 前端 xterm 回归 | 低 | 中 | 独立验证 |

---

## 8. 自审

- [x] 无 TBD/TODO
- [x] 范围聚焦：三块完整交付
- [x] 复用 shell_policy / X-Internal-Token / ReportStore，无重复造轮子
- [x] 轻量哲学：Go 用 database/sql（无 ORM），与 P1b 一致
- [x] 鉴权收紧路径明确（去 IP 白名单，留内部 token）
