# 数据与接口所有权

| 数据/接口 | 唯一 owner | 写入路径 | 读取路径 |
|---|---|---|---|
| users/roles/tenants/clusters/auth_sessions/scope | query-api + MySQL migrator（DDL） | Query API DML | Query API 授权层 |
| ai_runs/outbox/leases/events | query-api | Run/dispatcher transaction | Query API/Worker |
| ai_chat_sessions/messages | query-api | `/api/v1/ai/chat` 或内部 append | Query API SSE/list |
| ai_actions/attempts/approvals | query-api | Action API/dispatcher | Query API/Executor 回报 |
| observability traces/events/logs | unified ingest | `/v1/*` + OTLP | Query readers |
| observability.service_topology（Trace 父子关系派生边） | unified ingest | 接受 Span 后按租户/集群/分钟聚合，经鉴权 ClickHouse sink 批量写入 | Query topology/panorama/RCA readers |
| graph projection | graph projector | 版本化 outbox projection | Graph API（只读） |
| LLM provider secrets | egress proxy | Secret/registry 管理 | Proxy 内部 |
| Kubernetes credentials | credential broker | pre-registered profile | Action Executor 短时 lease |

任何服务新增表、直连 ClickHouse/MySQL/Kubernetes 或建立本地权威状态，都必须先更新本表、
架构 ADR 和 contract 测试；否则视为架构违规。
