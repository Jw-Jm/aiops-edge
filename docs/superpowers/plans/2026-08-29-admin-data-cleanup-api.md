# Plan: 后台历史数据清理 API

**Goal:** 在 query-api 增加管理员专用的预览/二次确认/异步执行清理接口，并通过受保护的内部契约清理 ai-orchestrator 的历史会话；支持 `ai_sessions`、`alert_events` 和固定白名单 ClickHouse telemetry 范围。

**Architecture:** query-api 是唯一的浏览器入口和清理操作 owner。MySQL 持久化 preview/operation 状态与审计元数据；ClickHouse 由 query-api 直接执行白名单 SQL；ai-orchestrator 仅提供内部的按截止时间清理会话接口。图谱、控制面权威表、VictoriaMetrics/VictoriaLogs 和前端不改动。

## 1. 建立清理操作持久化模型

Files:
- Add: `ai-apm-query-go/internal/store/migrations/versions/0012-data-cleanup.sql`
- Add: `ai-apm-query-go/internal/store/data_cleanup.go`
- Add: `ai-apm-query-go/internal/store/data_cleanup_test.go`

Steps:

1. 先写 DAO 失败测试：创建 preview、按 operation ID 读取、幂等键冲突、确认 token 只保存 hash、状态从 queued/running 到 succeeded/failed 的更新，以及部分失败结果持久化。
2. 运行 `go test ./internal/store -run DataCleanup`，确认测试因缺少 DAO/表结构失败。
3. 增加 `data_cleanup_operations` 表和 DAO 类型，字段覆盖 operation/preview ID、tenant/user、canonical request JSON、digest、confirmation hash、expires_at、status、result JSON、timestamps、idempotency key；唯一键保护 `(tenant_id, idempotency_key)` 和 `preview_id`。
4. 实现创建、读取、原子消费确认、启动、完成和失败更新方法；token 用 SHA-256 hash 比对，DAO 不返回明文 token。
5. 重新运行目标测试，确认通过；再运行 `go test ./internal/store/...`。

## 2. 实现清理规划器和 ClickHouse 白名单执行器

Files:
- Add: `ai-apm-query-go/internal/api/data_cleanup.go`
- Add: `ai-apm-query-go/internal/api/data_cleanup_test.go`

Steps:

1. 先写纯单元测试：请求规范化与 digest 稳定性；空/未知 scope、未来 cutoff、缺失幂等键、非法租户/集群过滤的拒绝；所有表名和时间列只能来自白名单；告警查询必须包含 `status='resolved'`；cutoff 统一为 UTC 且条件为严格 `<`。
2. 运行 `go test ./internal/api -run DataCleanup`，确认因缺少规划器失败。
3. 定义请求/响应模型、scope 规格和 `CleanupBackend` 接口；实现 canonical JSON/digest、随机 preview/confirmation token、10 分钟过期和确认错误码。
4. 为 `alert_events` 与七类 telemetry 表生成固定的 count SQL 和 delete SQL；只把服务端生成的安全字符串字面量用于 tenant/cluster/cutoff，不接受客户端表名或 SQL。
5. 以 `query.ClickHouseRepo.Query` 作为 ClickHouse seam，解析 count 结果并记录 mutation 请求；将无数据计数规范化为 0。
6. 重新运行目标测试，确认通过；再运行 `go test ./internal/api -run 'DataCleanup|Alert'`。

## 3. 接入 query-api HTTP 两阶段接口和异步任务

Files:
- Modify: `ai-apm-query-go/internal/api/data_cleanup.go`
- Modify: `ai-apm-query-go/internal/api/handler.go`
- Modify: `ai-apm-query-go/cmd/api/main.go`
- Add: `ai-apm-query-go/internal/api/data_cleanup_http_test.go`

Steps:

1. 先写 HTTP 合约失败测试：preview/execute/status 路径、非 admin 拒绝且不访问后端、预览返回 digest/token/数量、错误 token/过期 token/摘要不匹配/重复 execute 的稳定状态码，以及 execute 返回 `202`。
2. 运行 `go test ./internal/api -run 'DataCleanupHTTP|DataCleanupRoute'`，确认因路由和 handler 缺失失败。
3. 将清理服务注入 `Handler`，注册：
   - `POST /api/v1/admin/data-cleanups/preview`
   - `POST /api/v1/admin/data-cleanups/execute`
   - `GET /api/v1/admin/data-cleanups/`
   并用现有 `RequireRole("admin", ...)` 保护全部路径。
4. preview 校验 canonical admin tenant 上下文，读取各后端预计数量，持久化 preview，并返回 `preview_id`、`request_digest`、`confirmation_token`、过期时间和明细。
5. execute 原子消费确认信息，写入 queued operation 后启动后台 goroutine；每个 scope 独立执行并持久化结果，ClickHouse 返回 mutation ID，orchestrator 失败不掩盖已完成 scope。
6. status 只允许同租户管理员读取，并返回 queued/running/succeeded/failed 及逐项结果；对不存在或跨租户 operation 不泄露信息。
7. 重新运行 HTTP 目标测试，确认通过；再运行 `go test ./internal/api ./internal/store`。

## 4. 增加 ai-orchestrator 内部会话清理契约

Files:
- Modify: `ai-orchestrator/session_store.py`
- Modify: `ai-orchestrator/main.py`
- Add: `ai-orchestrator/data_cleanup_api.py`
- Add: `ai-orchestrator/tests/test_data_cleanup_api.py`

Steps:

1. 先写 Python 失败测试：内部 token 缺失/错误拒绝；请求摘要和 operation ID 缺失拒绝；cutoff 过滤 session sidecar 的 `updated_at`，并同步清理对应 checkpoint/writes；只删除严格早于 cutoff 的会话；返回 deleted counts。
2. 运行 `pytest -q tests/test_data_cleanup_api.py`，确认因新路由/SessionStore 方法缺失失败。
3. 在 `SessionStore` 增加按 epoch cutoff 查询/删除的方法，使用同一 SQLite 连接和事务，保证 sidecar 删除与 checkpoint/writes 的候选 session 集合一致。
4. 新增内部路由 `POST /internal/v1/data-cleanups/ai-sessions`，复用 orchestrator 现有内部认证中间件，并强制校验 `X-Cleanup-Operation-Id`、`X-Cleanup-Request-Digest` 和 cutoff；不暴露给浏览器。
5. 将路由纳入 `main.py`，使用 query-api 方向性 token；完成 Python 目标测试及现有 session 测试。

## 5. 接通跨服务执行、审计与迁移契约

Files:
- Modify: `ai-apm-query-go/internal/api/data_cleanup.go`
- Modify: `ai-apm-query-go/internal/api/handler.go`
- Add or modify: `ai-apm-query-go/internal/store/platform_audit.go` (only if the existing audit writer cannot record cleanup events)
- Add: `ai-apm-query-go/internal/api/data_cleanup_integration_test.go`
- Modify: `ai-apm-query-go/internal/store/migrations/schema_manifest_test.go` if required by the migration contract

Steps:

1. 先写跨服务失败测试：query-api 向 orchestrator 发送固定内部路径、方向 token、operation ID 和 digest；错误响应映射为该 scope 的 failed；重试 execute 不重复调用。
2. 运行对应 Go 测试，确认因 adapter/审计接线缺失失败。
3. 实现 orchestrator HTTP adapter，限制 body/timeout，禁止转发浏览器 Authorization；把 operator/tenant/canonical request 写入平台审计事件。
4. 确认 `0012-data-cleanup.sql` 被 embed migrator 纳入，并在 schema manifest 中覆盖新表；runtime 只读 readiness 不执行 DDL。
5. 运行 Go/Python 目标测试和迁移测试，确认通过。

## 6. 完整验证与交付

Commands:

1. `gofmt -w` 本次新增/修改 Go 文件；`python -m compileall` 本次新增 Python 文件。
2. `go test ./internal/api ./internal/store/... ./internal/query/...`。
3. `pytest -q ai-orchestrator/tests/test_data_cleanup_api.py ai-orchestrator/tests/test_session_store.py`（若目标文件不存在则运行现有 session 相关测试集合）。
4. 检查 `git diff --check`，确认只包含本功能文件；不暂存用户已有的密码、部署、图谱门禁等未提交改动。
5. 运行必要的本机 HTTP 合约 smoke test；不执行真实删除，除非用户另行明确要求。

## 风险与处理

- ClickHouse `ALTER TABLE ... DELETE` 是异步 mutation，接口只报告 mutation ID 和状态，不假装已物理完成。
- query-api 重启后已持久化 operation 仍可查询；未完成操作由状态标为 failed/unknown，不能自动重放删除。
- trace summary 是派生表，按自身时间列清理；本期不尝试根据 trace_spans 重新计算图谱或摘要。
- 现有 dirty worktree 含用户未提交改动，提交时只按明确文件路径选择本功能文件。
