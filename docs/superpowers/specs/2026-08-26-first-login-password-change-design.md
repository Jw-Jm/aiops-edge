# 首次登录强制改密设计

## 目标

本地/验收环境在全新数据库初始化时提供可知的首个登录凭据 `admin/admin123`，首次登录后必须先修改密码，修改完成前不得访问任何业务 API。已有数据库升级不得被 Helm 重置密码；当前本机环境执行一次显式迁移，使验收可以从该流程开始。

## 约束与不变量

- `admin/admin123` 只用于没有 `admin` 用户的首次种子流程；`SeedAdmin` 必须幂等，已有用户的密码不能被启动或升级覆盖。
- 初始密码不能通过登录响应、前端源码、日志或接口回显；登录页只提供用户名和密码输入。
- 数据库保存 `users.must_change_password`，首次种子为 `1`，普通创建用户默认为 `0`。
- 登录成功仍创建正常 JWT 会话，但响应增加 `must_change_password`；强制改密状态下，后端只允许改密相关认证端点和健康检查，业务 API 返回 `403` 与稳定错误码 `password_change_required`。
- 改密接口必须重新校验当前密码，新密码至少 8 个字符且两次输入一致；成功后写入 bcrypt 哈希、清除强制标记，并签发新会话令牌。
- 显式 `ADMIN_PASSWORD`/`ADMIN_INITIAL_PASSWORD` 仍可覆盖首次种子密码，方便非本地部署；没有覆盖值时本地默认值为 `admin123`。
- 当前本机已有 `admin` 账号不在启动时自动覆盖；仅在本次验收部署中执行有记录的一次性 admin 密码重置并置为强制改密。

## 后端设计

### 数据库

新增版本化迁移 `mysql/0010_auth_password_bootstrap`，给 `users` 增加 `must_change_password TINYINT NOT NULL DEFAULT 0`。查询和写入用户实体时都显式投影该列，避免依赖 `SELECT *`。

### 认证接口

- `POST /api/v1/auth/login`：验证用户名和密码，响应保留现有字段并增加 `must_change_password: boolean`。
- `POST /api/v1/auth/change-password`：从已验证 JWT 的用户身份读取账号，接收 `{current_password,new_password,confirm_password}`；仅允许当前用户修改自己的密码，成功返回新 token 和 `must_change_password:false`。
- `GET /api/v1/me`：补充返回 `must_change_password`，用于刷新页面后的路由判断。

`AuthMiddleware` 在完成 JWT、会话、租户授权后读取权威用户状态。浏览器请求在强制改密时，除 `/api/v1/auth/change-password` 和 `/api/v1/me` 外统一返回 `403 password_change_required`；带独立内部服务身份的请求不走浏览器强制改密分支，避免后台 worker 因 admin 的交互式引导状态被阻断。

### 会话安全

改密成功后撤销该用户当前活动会话，创建一个新的活动会话并返回新 JWT；旧 token 立即失效。密码修改失败不改变会话和用户状态。

## 前端设计

- auth store 持久化 `must_change_password`，登录响应写入该状态。
- 新增 `/change-password` 页面和 API 客户端方法；页面提供当前密码、新密码、确认密码，提交成功后保存新 token 并进入 `/overview`。
- `RequireAuth` 在已有 token 且 `must_change_password=true` 时仅允许 `/change-password`，其他路径重定向到该页面。
- 登录成功后按标志直接进入 `/change-password`，不短暂加载 Overview；401/403 错误仍按现有会话失效逻辑处理。
- 改密页面不显示默认密码，不把密码写入 URL、日志、localStorage 或错误信息。

## 验证

- Go：迁移列、首次 admin 种子幂等性、登录标志、强制改密拦截、错误密码/弱密码/确认不一致、成功换 token 和旧 token 失效。
- 前端：登录标志路由、改密表单校验、强制改密路由守卫、成功后进入 Overview；完整现有测试和生产构建。
- 本机真实环境：应用迁移后用 `admin/admin123` 登录，确认业务 API 在未改密时返回 `403 password_change_required`，提交新密码后旧密码失败、新密码成功、刷新后可访问 Overview。
- 发布一致性：提交每个修复，构建对应镜像，Helm 部署，执行 API/浏览器真实验证，并确认 `HEAD == origin/main`。
