# 存储所有权契约（Schema Ownership）

本文是 Phase 1 的 P0 约束。它区分“业务域所有者”和“物理数据库写入者”：

- 业务域所有者决定语义、状态机和 API 契约。
- 物理写入者是唯一可以执行该表 DDL/DML 的服务。
- orchestrator 不得直接连接 MySQL；需要持久化时调用 query-api 的版本化内部控制面接口。

## 全局规则

1. **单表单物理写入者**：一张表只有一个服务负责生产 DDL/DML。
2. **MySQL 是动态授权唯一权威来源**：用户、角色、权限、Tenant、Cluster 关系及账号状态不得从 JWT、缓存或 orchestrator 内存推断。
3. **所有运行数据显式携带 `tenant_id` 和 canonical UUID `cluster_id`**；不得使用 `NULL` 表示 default tenant，不得使用 slug/name 作为外键。
4. **禁止新旧双主路径**：切换 Phase 开始后，旧 writer/reader 必须停止；不做长期双写兼容层。
5. **跨服务写入必须通过内部 `/internal/v1/...` API**，使用服务身份认证和短时签名 `TrustedRequestContext`；上下文不是授权结论，接收方仍需查询 MySQL 当前授权。
6. **Kubernetes 凭据只在可信 Kubernetes Access Boundary 解析**；orchestrator、Agent、Tool 不得读取 Secret 或创建 Kubernetes client。
7. **历史运行数据不迁移**。清理仅按 Phase 0 生成的 manifest，在本机验收环境执行；未知资产不得删除。

## MySQL 目标所有权矩阵

| 数据域/表 | 业务域所有者 | 物理写入者 | 访问边界 | Phase 1 状态 |
|---|---|---|---|---|
| `users`, `roles`, `permissions`, `tenants` | Identity/RBAC | query-api | MySQL 当前状态 | 目标冻结 |
| `user_tenants`, `user_roles`, `tenant_roles`, `scope_assignments` | Identity/RBAC | query-api | tenant/cluster/namespace/resource/action | 目标冻结 |
| `user_sessions`, token version、账号状态 | Identity/RBAC | query-api | JWT 只引用 user/session | 目标冻结 |
| `clusters`, `cluster_credentials` 元数据 | Cluster Registry | query-api | UUID canonical identity；仅存 `credential_ref` | 目标冻结 |
| `cluster_nodes`, `resource_scopes` | Cluster Registry | query-api | 仅存标准化身份/授权元数据 | 目标冻结 |
| `ai_runs` | AI Runtime | query-api Control Plane Persistence | orchestrator 通过内部 API 创建/推进 | 目标冻结 |
| `ai_plan_steps` | AI Runtime | query-api Control Plane Persistence | DAG step 与 budget 必须可审计 | 目标冻结 |
| `ai_tool_runs` | AI Runtime | query-api Control Plane Persistence | ToolResult/Error 原样结构化保存 | 目标冻结 |
| `ai_evidence` | Evidence/RCA | query-api Control Plane Persistence | 仅引用 `cluster_id` 和 evidence source | 目标冻结 |
| `approval_tasks`, `ops_actions`, `verification_runs` | Risk/Execution | query-api Control Plane Persistence | 写操作必须结构化、可审批、可回滚 | 目标冻结 |
| `audit_logs` | AI Runtime/Execution | query-api Control Plane Persistence | 记录 run、plan、tool、approval、execution 业务审计 | 目标冻结 |
| `platform_audit_logs` | Platform Security | query-api | 认证、授权、Cluster Access、Secret access 审计 | 目标冻结 |
| `platform_settings`, `llm_settings`, `llm_models` | Platform Configuration | query-api | 管理面授权 | 已有域，需补 tenant 约束 |
| `knowledge_base`, `rules`, `reports` | Knowledge/AI Runtime | query-api Control Plane Persistence | orchestrator 只通过内部 API | 现有 writer 需收敛 |

### `audit_logs` 与 `platform_audit_logs` 的边界

- `platform_audit_logs`：谁以什么身份访问了什么平台资源，以及授权结果；例如登录、权限拒绝、cluster resolver、credential access、Kubernetes read/write。
- `audit_logs`：AI Run 内发生了什么业务动作；例如计划生成、Tool 调用、证据采集、审批、执行、验证、回滚。
- 两者都必须保存 `request_id`、`run_id`（适用时）、`tenant_id`、canonical `cluster_id`（适用时）、`user_id`、`service_identity`、时间和结果。
- 不能把一个表当作另一个表的兼容别名，也不能由两个服务共同写同一张表。

## ClickHouse / VictoriaLogs 目标所有权矩阵

| 存储 | 主写者 | 读者 | 目标职责 |
|---|---|---|---|
| VictoriaLogs 原始日志 | ingest/collector | query-api、orchestrator Tool | 原始日志主存储；按 tenant/cluster/resource 过滤 |
| ClickHouse `log_records` | ingest/derived pipeline | query-api | 派生索引/聚合，不是原始日志权威 |
| ClickHouse `trace_spans` | ingest/derived pipeline | query-api | Trace 查询与聚合 |
| ClickHouse `service_topology` | ingest/derived pipeline | query-api | 派生拓扑边；canonical identity 中包含 UUID cluster |
| ClickHouse `alert_events` | alert/event pipeline | query-api、orchestrator Tool | 统一 Event 查询；必须显式 tenant/cluster |
| VictoriaMetrics | metrics ingest | query-api、orchestrator Tool | 指标时序主查询路径；旧数据不迁移 |

query-api 不能写 ingest 所拥有的 ClickHouse 表；任何重建、TTL 或清理必须由该存储域的主写者执行。

## 迁移与实施规则

- 新增表必须在本矩阵登记业务域所有者、物理写入者、DDL 位置和内部 API。
- query-api 使用 Go migration/EnsureSchema 体系；orchestrator 使用 SQL migration 仅作为历史实现，Phase 1 后不得继续成为 MySQL 直连 writer。
- 在 cutover 前必须同时列出旧 writer、旧 reader 和删除点；只停止旧 writer 不足以完成迁移。
- 任何 schema 变更都必须有 typed contract fixture、错误语义和跨语言 contract test。
- 发现当前实现与本矩阵冲突时，记录为 Gate blocker，不得默默兼容。

## 当前已知冲突（不在 Phase 1 偷改）

1. 当前 `clusters` 仍有 `kubeconfig` 字段和 Go 读写路径；目标改为 `credential_ref`，在 Cluster Registry / Kubernetes Access Phase 处理。
2. 当前 query-api 与 orchestrator 都存在历史 `audit_logs` 建表/写入逻辑；Phase 1 只冻结边界，后续通过 Control Plane Persistence 收敛。
3. 当前部分 observability schema 使用字符串 `cluster_id` 默认值 `default`；新 schema 切换 Phase 处理 UUID 与 tenant 隔离。
4. 当前 orchestrator 存在直接 migration 资产；在控制面 writer 收敛前不得声明目标所有权已落地。
