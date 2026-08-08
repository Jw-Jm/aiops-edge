# 数据归属治理：存储职责重构

**日期**: 2026-08-08
**范围**: 日志归属、配置类迁移、死表停用、报告/拓扑/向量库冗余收敛
**驱动**: 用户要求"重新考虑所有数据应该归属的数据库"，消除双写、死表、放错引擎
**决策**: 核心三项 + 冗余收敛（更彻底）

---

## 1. 数据归属全景（梳理结论）

| 数据项 | 当前存储 | 本质 | **最优归属** | 动作 |
|---|---|---|---|---|
| 服务 RED（QPS/错误率/延迟 sum+count）| CH `metric_service_red` + ingest→VM | 时序指标 | **VM** | 停用 CH 死表 |
| OS/硬件/组件指标 | VM | 时序 | **VM** ✅ | 保留 |
| 延迟分位数 P50/P95/P99 | CH `trace_spans` | 明细分析 | **CH** ✅ | 保留 |
| 链路 trace | CH `trace_spans` | 事件/血缘 | **CH** ✅ | 保留 |
| **日志** | VL(pod)+CH `log_records` 双写，前端默认 CH | 全文检索 | **VL** | 前端默认切 VL |
| 拓扑边 | CH `service_topology`（3 处写入）| 关系聚合 | **CH** ✅ | 收敛写入源 |
| 配置类（llm_providers/platform_settings/llm_config_history）| **CH** | 业务配置 KV | **MySQL** | 迁移 |
| 用户/目录/设备/集群/拓扑资产 | MySQL | 业务/资产 | **MySQL** ✅ | 保留 |
| SNMP/IPMI/部件健康 | MySQL（orchestrator 写）| 资产/状态 | **MySQL** ✅ | 治理写读断层 |
| 审批/审计/Agent/报告元数据/知识库/规则 | MySQL | 业务 | **MySQL** ✅ | 保留 |
| 巡检报告对象 | MinIO+CH+MySQL 三写 | 对象 | **MinIO**（余留元数据）| 收敛三写 |
| RAG 运维案例向量 | orchestrator /tmp + 闲置 chromadb Server | 向量 | 归一 | 删闲置 chromadb |
| 任务队列/缓存 | Redis(arq)+内存死代码 | 易失 | Redis 保留 | 清死代码 |
| LangGraph 会话/Flow | SQLite | 会话 | SQLite ✅ | 保留 |

---

## 2. 核心治理项

### 2.1 日志归属 → VL（前端默认切 VL）

**现状**：VL(pod 日志, log-shipper 写) + CH `log_records`(ingest/deepflow 应用日志) 双写双查，前端 `/logs` 默认 `backend='clickhouse'`。

**改造**：
- 前端 `Logs/index.tsx` 默认 `backend='victorialogs'`（读 VL）
- CH `log_records` 保留仅作 trace 血缘关联（TraceContext 按 trace_id 查），不作为日志主查询源
- 收敛双写：业务日志若语义被 VL 覆盖，停写 CH 通用日志；仅保留 trace 关联场景

### 2.2 停用 metric_service_red 死表

**现状**：ingest MetricsWriter 写 CH `metric_service_red`（`metrics_writer.go:234`），无读方。

**改造**：
- ingest `insertMetrics` 停止写 CH `metric_service_red`（服务 RED 已由 ingest /metrics → VM 承担）
- `init_clickhouse.sql` 移除该表 DDL（或保留废弃不建）

### 2.3 配置类数据迁 MySQL

**现状**：`llm_providers`/`llm_config_history`/`platform_settings` 在 CH（时序库），是业务配置。

**改造**：
- query-api 在 MySQL 建 `platform_settings`/`llm_providers` 表（EnsureSchema）
- `settings.go`/`llm_providers.go` 改为读写 MySQL（复用 store 层）
- CH 中对应表废弃

---

## 3. 冗余收敛项

### 3.1 巡检报告三写收敛

**现状**：报告 markdown 写 MinIO `ops-reports` + CH `inspection_reports` + MySQL `reports` 三处。

**改造**：
- MinIO 留对象本体
- MySQL `reports` 留元数据 + `file_key` 引用
- 停写 CH `inspection_reports`（只留 MySQL 元数据）

### 3.2 删闲置 chromadb Server

**现状**：部署了独立 chromadb Server（8000 端口），但 orchestrator RAG 用本地 PersistentClient（`/tmp/ops-cases`），Server 未接线。

**改造**：
- 删除 `templates/chromadb/deployment.yaml`（闲置组件）
- 保留 orchestrator 本地 PersistentClient（或后续归一 HttpClient）

### 3.3 收敛拓扑多写

**现状**：`service_topology` 由 ingest MetricsWriter + deepflow_sync + query-api K8s sync 三处写入。

**改造**：
- 明确主源为 deepflow（真实拓扑），K8s sync 作为补充
- 去重/防重复边（按 source/target/bucket 唯一性治理）

---

## 4. 实施顺序（低风险→高风险）

1. 停用 metric_service_red（纯删除，零影响）
2. 删闲置 chromadb（纯删除，零影响）
3. 日志前端默认切 VL（前端改动）
4. 配置类迁 MySQL（query-api 存储层迁移）
5. 报告三写收敛（orchestrator 写入逻辑）
6. 拓扑多写收敛（写入源治理）

---

## 5. 风险

| 风险 | 影响 | 缓解 |
|------|------|------|
| 日志切 VL 后 TraceContext 关联丢失 | 中 | CH log_records 保留 trace 血缘 |
| 配置类迁 MySQL 破坏现有 LLM 配置 | 中 | EnsureSchema 幂等 + 迁移保留旧值 |
| 报告三写收敛丢内容 | 低 | MinIO 保留对象，MySQL 留 file_key |
| 删 chromadb 影响 RAG | 低 | orchestrator 用本地 PersistentClient，不依赖 Server |

---

## 6. 自审
- [x] 无 TBD/TODO
- [x] 覆盖核心三项 + 冗余收敛全部
- [x] 每项有明确动作与风险
- [x] 归属矩阵完整（14 类数据）
