# 存储所有权契约（Schema Ownership）

**原则：单表单主写者（Single Writer Per Table）。** 每个数据库表只有一个服务负责写入/DDL，其他服务只能读。违反此契约会导致数据竞争、全量重建误删、schema 冲突。

## MySQL 库归属

### ai-apm-query-go（Go，`internal/store` EnsureSchema 幂等建表）
| 表 | 用途 |
|---|---|
| `users` / `user_tenants` / `user_sessions` | 认证与会话 |
| `service_catalog` | 服务目录 |
| `devices` | 设备清单 |
| `clusters` / `cluster_nodes` | 集群/节点 |
| `topology_nodes` / `topology_edges` | 拓扑 |
| `platform_settings` | 平台设置 |
| `llm_settings` / `llm_models` | LLM 配置 |
| `alert_rules` / `alert_silences` | 告警规则/静默 |
| `slo_targets` | SLO 目标 |
| `dashboard_panels` | 看板面板 |
| `tenants` | 租户 |

### ai-orchestrator（Python，`migrations/*.sql`）
| 表 | 用途 |
|---|---|
| `approval_tasks` | 审批任务 |
| `audit_logs` | 审计日志 |
| `agents` | Agent 定义 |
| `reports` | 报告 |
| `knowledge_base` | 知识库 |
| `rules` | 治理规则 |
| `snmp_*` / `network_interfaces` / `ipmi_*` / `node_component_health` | 设备监控数据 |

## ClickHouse 归属（`deploy/helm/aiops/files/clickhouse/init_clickhouse.sql`）
| 表 | 主写者 |
|---|---|
| `trace_spans` / `log_records` / `service_topology` | **ai-apm-ingest-go**（唯一主写者；query-api 只读，禁止 TRUNCATE/INSERT） |
| `alert_events` | **ai-apm-query-go**（告警评估引擎写入，ReplacingMergeTree + TTL） |

## 迁移约定
- **新增 MySQL 表**：按功能域选归属——查询侧/配置/治理 → query-api（Go EnsureSchema）；AI 编排/审批/审计/报告 → ai-orchestrator（migrations SQL）。
- **跨服务迁移**：只允许在单表单主写者的服务内做 DDL；若他服务需要该表，走读路径。
- **禁止**：query-api 写 `alert_events` 之外的 CH 表；任何服务 TRUNCATE 其他服务的表（如 SyncDataFromK8s 曾 TRUNCATE trace_spans，已废弃）。
- **告警事件演进**：已从 MySQL（全量 ReplaceAll 写放大）迁至 ClickHouse（ReplacingMergeTree + TTL 30 天）；生产扩容时无需迁移存储，query-api 可横向扩展（告警查询读内存态缓存 + CH 持久）。
