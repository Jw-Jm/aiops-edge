# AIOps 平台 实施成果汇总

> 日期：2026-08-07 ｜ 基线：`github.com/Jw-Jm/aiops-edge` main=`7f6537e`（71 commits）
> 部署：本机 OrbStack K8s（arm64），Helm Chart，REVISION 17
> 方法：superpowers 流程（writing-plans → subagent-driven-development → 端到端验证 → 推送）

---

## 一、项目定位

自研云原生 AIOps 平台（ongrid 风格），**复刻 ongrid 能力面 + 页面**，**不复制其技术栈**（Qdrant/Prometheus-Loki-Tempo/Edge 均不用自研替代）。全链路自研：采集 → 存储 → AI 编排 → 可视化。合规红线：ongrid 为 AGPL-3.0，仅对标不复制（`ongrid-ref/` 已隔离不入库）。

## 二、当前运行环境

| 项 | 值 |
|---|---|
| 运行环境 | 本机 macOS(arm64) + OrbStack K8s 单节点 |
| 部署方式 | Helm Chart（`deploy/helm/aiops`），REVISION 17 |
| 访问入口 | http://localhost:30253 |
| 存储类 | local-path（集群默认） |
| 镜像 | arm64 原生 |

### 运行中的服务（全部健康）
**observability（14）**：frontend / query-api / ingest / ai-orchestrator + ClickHouse / VictoriaMetrics / VictoriaLogs / Redis / ChromaDB / MinIO / MySQL / vmalert / (init Jobs Completed)
**deepflow（6）**：deepflow-agent / server / clickhouse / mysql / grafana / app

## 三、已交付成果（按 Phase）

### Phase A — 界面地基 + 可观测
| 交付 | 说明 |
|---|---|
| **布局壳** | Sidebar 8 区段 + zinc 色板 + Zustand 全局状态 + ⌘K⌘P 命令面板 + AI 助理侧栏 |
| **Dashboard** | 后端 `/dashboard/stats` 聚合接口（TDD）+ 前端 Overview 升级（错误率/延迟） |
| **Monitor** | PromQL 面板页（4 面板，复用 `/metrics/query_range`） |
| **Logs / Traces** | UI 对齐 zinc 基调 |
| **VictoriaMetrics 接入** | 从零接入 VM + 通用 PromQL `query_range` 端点（TDD） |

### Phase B — AI 运维功能
| 交付 | 说明 |
|---|---|
| **告警 incident 状态机** | `AlertEvent` 状态字段（firing/ack/resolved）+ `transitionStatus`（TDD 4 case）+ ack/resolve/详情接口 |
| **IncidentDetail** | `/alerts/incidents/:id` 详情页（状态时间线 + ack/resolve + AI RCA） |
| **SSE 事件协议** | 标准 `event:` 帧 + `tool_start/tool_end`/`done` 结构化 + 前端按事件渲染工具卡片 |
| **ChatThread** | `/chat/:sessionId` 独立会话线程页（历史回放 + 流式发送） |
| **Skills** | `/skills` 目录页（详情 + 执行表单）+ `/ai/skills` + `execute_skill` |
| **Agents** | `/agents` 卡片页 + `/ai/agents` CRUD + 自定义专家持久化 |
| **Workflows** | `/workflows` 页（只读 DAG 图 + 运行）+ `/ai/flows` |
| **工具 params 全量** | 16 工具全量参数 schema（Skills 页所有工具可渲染执行表单） |

### 文档（spec + plan）
- **spec（3）**：本机 K8s 部署设计、ongrid 能力复刻可行性、ongrid 页面复刻设计（含授权合规红线）
- **plan（11）**：每阶段实施计划（见 `docs/superpowers/plans/`）

## 四、端到端验证成果

```
前端 http://localhost:30253 → 200
/dashboard/stats → {services:14, total_calls:33345, error_rate:0}
/ai/skills → 7 skills（observability/infra/rca/rag_cases/vm_ops/alert_ops/automation）
/ai/agents → 4 experts + CRUD（创建/更新/删除，内置受保护）
/ai/flows → 2 workflows（full 13 节点 + chat 6 节点）+ run 完整 DAG 诊断
SSE chat → event:progress → tool_start/tool_end(真实RCA) → done(结构化)
ClickHouse → 8 表自动建表，无历史数据
DeepFlowSyncer → interval=10s 增量同步 deepflow 拓扑
```

## 五、技术要点与关键决策

1. **Helm 可插拔**：每个中间件 `enabled`/`external` 开关，可复用外部实例（其他环境）
2. **数据模型**：ClickHouse 统一数据仓（trace_spans/log_records/service_topology 等 8 表），VictoriaLogs 日志 + VictoriaMetrics 指标
3. **AI 编排**：LangGraph 固定 DAG（full/chat 两套）+ RCA 引擎 + 多专家 + RAG 案例库 + MCP 工具
4. **实时同步**：DeepFlowSyncer 可配置间隔 + 增量水位（`DEEPFLOW_SYNC_INTERVAL`）
5. **合规**：ongrid-ref 隔离不入库；复刻能力不复制代码；表结构/枚举用自研命名

## 六、已解决的关键部署问题

- PVC Pending → storageClass 条件渲染
- 镜像拉取失败 → imagePullPolicy: IfNotPresent
- ClickHouse 远程访问 → 挂载 remote-config 允许集群内
- SSE 协议不兼容 → 重构为标准 event 帧
- 告警事件无状态 → 状态机 + rule_id 匹配

## 七、后续路线

| 批次 | 内容 | 状态 |
|---|---|---|
| Phase B 剩余 | approval 审批卡（内联 SSE）、FlowEditor（可编辑引擎，独立大工程） | 待办 |
| Phase C | 拓扑专项（数据模型+目录）、Devices、Clusters、Knowledge | 待办 |
| Phase D | Admin/RBAC、Reports、MySQL 业务库 | 待办 |
| 二期强化 | IM 通道、SNMP、agent 工具全量 | 待办 |

---

**一句话总结**：AIOps 平台本机已完整部署运行（14 服务 + deepflow），Phase A 全部 + Phase B 大部交付（界面地基 + AI 运维功能 8 大项），71 commits 全量推送，可随时回滚。剩余 Phase B 边缘 + Phase C/D 为后续路线。
