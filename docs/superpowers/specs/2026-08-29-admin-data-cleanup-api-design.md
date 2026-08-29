# 后台历史数据清理 API 设计

## 状态

已获用户确认，作为实现约束。

## 目标

为本地测试环境和后续受控运维提供统一的后台历史数据清理入口。接口必须支持明确范围、截止时间、只读预览和独立二次确认，不能把现有的“清空全部会话”或按规则删除告警接口直接暴露为通用清理能力。

## 非目标

- 不清理知识图谱投影、图谱 Schema、同步状态或图谱重建数据。
- 不清理 MySQL 中的用户、权限、集群配置、Schema 迁移、Outbox 等权威控制面数据。
- 不新增前端管理页面；本期只提供 REST API。
- 不修改 VictoriaMetrics/VictoriaLogs 的 retention 配置；本接口只清理可精确按时间范围定位的存储。

## API 合约

所有接口位于 query-api：

### `POST /api/v1/admin/data-cleanups/preview`

请求体：

```json
{
  "scopes": ["ai_sessions", "alert_events", "clickhouse_telemetry"],
  "cutoff_at": "2026-08-01T00:00:00Z",
  "tenant_id": "optional-tenant-id",
  "cluster_id": "optional-cluster-id",
  "idempotency_key": "client-generated-key"
}
```

语义：删除严格早于 `cutoff_at` 的数据；时间必须是带时区的 RFC3339，服务端规范化为 UTC。`scopes` 只能使用白名单值，不能为空；截止时间不能晚于当前时间。租户和集群过滤条件为空表示当前管理员授权范围内的全部对象。

响应至少包含：`preview_id`、规范化请求摘要 `request_digest`、一次性 `confirmation_token`、过期时间、按 scope/table 分组的预计数量、ClickHouse mutation 提示和风险告警。预览不产生删除副作用。

### `POST /api/v1/admin/data-cleanups/execute`

请求体：

```json
{
  "preview_id": "...",
  "request_digest": "...",
  "confirmation_token": "...",
  "idempotency_key": "..."
}
```

服务端只接受未过期、未消费且摘要完全一致的预览；确认令牌单次消费，重复执行返回同一个操作结果，不重复删除。成功后返回 `202 Accepted` 和 `operation_id`，清理在后台执行。

### `GET /api/v1/admin/data-cleanups/{operation_id}`

返回 `queued`、`running`、`succeeded` 或 `failed` 状态，以及每个 scope/table 的实际结果、ClickHouse mutation ID、错误信息和审计时间戳。

## Scope 边界

- `ai_sessions`：由 ai-orchestrator 删除 SQLite 中严格早于截止时间的 session sidecar、checkpoint 和 writes；必须由 query-api 通过受保护的内部接口调用，不能跨服务直接读写 SQLite 文件。
- `alert_events`：ClickHouse `observability.alert_events`，默认只处理已恢复事件，并按 `last_timestamp` 过滤；禁止复用现有按 `rule_id` 删除的路径。
- `clickhouse_telemetry`：只允许固定表和固定时间列：`log_records.timestamp`、`service_topology.time_bucket`、`change_records.start_time`、`trace_spans.start_time`、`trace_summary_state.date`、`trace_summary_index.time_bucket`、`k8s_events.time_bucket`。所有 SQL 使用服务端生成的表名和参数化/安全字面量，客户端不能传表名或 SQL。

## 权限、持久化与并发

- preview、execute、status 均要求有效 JWT、租户上下文和 MySQL 权威 `admin` 角色；JWT 中的 role 不作为权限来源。
- query-api MySQL 增加 `data_cleanup_operations` 表，持久化预览/执行状态、摘要、操作人、范围、截止时间、数量、mutation ID 和错误；确认 token 只保存哈希。
- 使用摘要和幂等键防止 TOCTOU、重复提交和跨请求篡改；执行时重新校验预览未过期且请求摘要一致。
- 多表清理采用后台任务；ClickHouse 删除是异步 mutation，状态接口必须反映未完成状态。
- 失败时保留已完成 scope 的结果和未完成 scope 的错误，不回报“整体成功”。

## 安全护栏

- 拒绝未来截止时间、空范围、未知 scope、空确认字段、过期/错误/已消费 token。
- 默认不删除 active/firing 告警，不触碰系统元数据和控制面表。
- 预览和执行均写入平台审计事件；响应不返回 token 明文以外的数据库凭据或原始会话内容。
- 内部 ai-orchestrator 清理端点只接受 query-api 的方向性内部 token，并要求请求摘要/操作 ID，不能被浏览器直接调用。

## 验收标准

1. 非 admin、缺少租户上下文或未认证请求返回 `403`，且不访问清理存储。
2. 非法 scope、未来时间、缺少幂等键、摘要不一致、确认失败和重复消费均有稳定错误码。
3. 预览只读并返回可审计的数量和摘要；执行只接受该预览的二次确认。
4. ClickHouse SQL 只使用白名单表/时间列，告警仅清理已恢复历史数据。
5. ai-sessions 日期过滤同时覆盖 session sidecar、checkpoint 和 writes，并有内部鉴权。
6. 重复 execute 不重复发起 mutation；状态接口可查询成功、部分失败和失败。
7. 单元测试、HTTP 合约测试和现有 Go/Python 测试通过。
