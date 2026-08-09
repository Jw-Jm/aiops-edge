# 01 架构与组件

> 本文档说明 AIOps 平台的系统架构、各服务职责、数据流与中间件清单。
> 面向部署/运维工程师，帮助你理解"要部署什么、各组件怎么协作"。

---

## 1. 系统架构总览

```
浏览器
  │  NodePort 30253 (HTTP/HTTPS)
  ▼
┌────────────────────────────────────────────────────────────┐
│ frontend (React + nginx:80)                                 │
│   /                    → 前端静态页面                        │
│   /api/v1/ai/*, /ops/* → ProxyAI 代理 → ai-orchestrator:8080│
│   /api/v1/*            → query-api:8080                     │
│   /grafana/            → deepflow-grafana (deepflow ns)      │
└────────────────────────────────────────────────────────────┘
          │                          │
          ▼                          ▼
┌──────────────────┐      ┌───────────────────────────────┐
│ query-api (Go:8080)│      │ ai-orchestrator (Python:8080) │
│ 查询/告警/设置/代理 │      │ AI 编排/诊断/审批/报告/SNMP/   │
│ ClickHouse 查询    │      │ IPMI/流程引擎/NL2SQL/MCP       │
│ VictoriaMetrics   │      └───────────────────────────────┘
│ VictoriaLogs      │
│ MySQL (业务状态)   │      ┌───────────────────────────────┐
└──────────────────┘      │ ingest (Go:8080)               │
          │               │ OTLP 采集/日志/指标/DeepFlow同步 │
          ▼               └───────────────────────────────┘
┌────────────────────────────────────────────────────────────┐
│ 数据底座：ClickHouse / VictoriaMetrics / VictoriaLogs /     │
│           MySQL / Redis / MinIO / ChromaDB                  │
└────────────────────────────────────────────────────────────┘
          │
          ▼
┌────────────────────────────────────────────────────────────┐
│ deepflow（独立 namespace，完整 eBPF 采集）                  │
│ agent(DaemonSet) → server → clickhouse / mysql / grafana   │
└────────────────────────────────────────────────────────────┘
```

- **命名空间 `observability`**：4 个自研服务 + 8 个中间件
- **命名空间 `deepflow`**：deepflow 完整采集栈（agent/server/clickhouse/mysql/grafana）

---

## 2. 自研服务（4 个）

| 服务 | 语言 | 端口 | 职责 |
|------|------|------|------|
| **frontend** | React + Vite + nginx | 80（NodePort 30253） | Web 控制台；反向代理 `/api`、`/grafana` |
| **query-api** | Go | 8080 | 查询/告警/设置/用户/租户/拓扑/容量；AI 请求代理到 orchestrator |
| **ingest** | Go | 8080 | OTLP traces/logs 采集；指标；DeepFlow 增量同步 |
| **ai-orchestrator** | Python (FastAPI) | 8080 | AI 编排（LangGraph DAG）、诊断、审批、报告、NL2SQL、MCP、SNMP/IPMI |

### 2.1 query-api 职责
- 查询 API：服务/拓扑/链路/日志/指标/容量预测（ClickHouse + VictoriaMetrics）
- 告警引擎：内置 `evaluateAlerts`（阈值/突变/异常/预测/烧毁率），`alert_events` 落 ClickHouse
- 业务状态：MySQL（用户/租户/规则/审批/审计/SLO）
- **ProxyAI**：把 `/api/v1/ai/*`、`/api/v1/ops/*`、`/api/v1/mcp/*` 代理到 ai-orchestrator，并注入内部 token 与角色
- 认证：JWT（HS256），`JWT_SECRET` 强注入（≥32 字符）

### 2.2 ingest 职责
- OTLP HTTP：`/v1/traces`、`/v1/logs` 接收（X-Api-Key 鉴权）
- 写 ClickHouse：`trace_spans` / `log_records` / `service_topology`（批量 + WAL 兜底重试）
- 指标：RED 指标进 VictoriaMetrics
- DeepFlow 同步：从 `deepflow-clickhouse` 增量拉取 application_map / l7_flow_log 转写

### 2.3 ai-orchestrator 职责
- AI 编排：LangGraph DAG（采集 → 分析 → 计划 → 审批 → 执行 → 验证 → RCA）
- 安全执行：execution_gate（危险工具需审批）、ShellPolicy（命令白名单）、WebShell
- LLM 接入：OpenAI 兼容 API（配置存 query-api 加密）、mock 模式
- 业务：审批/审计/报告/知识库/NL2SQL/技能/MCP/工作流
- 采集：SNMP（交换机）、IPMI（服务器硬件）

---

## 3. 中间件（8 个）

| 中间件 | 用途 | 存储 | 高可用现状 |
|--------|------|------|-----------|
| **ClickHouse** | trace/log/topology/alert 时序存储 | PVC | 单写（可 ReplicatedMergeTree 多副本） |
| **MySQL** | 业务状态（用户/规则/审批/审计/SLO） | PVC | 单写（生产加主从） |
| **VictoriaMetrics** | 指标时序库 | PVC | 单写（可写对象存储） |
| **VictoriaLogs** | 日志（查询端） | PVC | 单写 |
| **Redis** | ai-orchestrator ARQ 任务队列 | PVC | 单写（生产加 Sentinel） |
| **MinIO** | 对象存储（报告/产物/备份） | PVC | 单写（生产分布式 EC） |
| **ChromaDB** | AI 知识库向量库 | PVC | 单写 |
| **vmalert** | 已关闭（告警用 query-api 内置引擎） | — | — |

> **HA 说明**：单写组件 `replicas:1` 是"单写安全"兜底。生产 HA 方案见《04 生产配置》"高可用"章节。

---

## 4. 数据流

### 4.1 可观测性数据（采集 → 存储 → 展示）
```
应用/服务 ──OTLP──▶ ingest ──▶ ClickHouse (trace/log/topology)
                    │          └─▶ VictoriaMetrics (RED 指标)
                    ▼
              DeepFlow agent ──▶ deepflow-server ──▶ deepflow-clickhouse
                    │                                      │
                    └──ingest DeepFlowSyncer 增量拉取──────┘
                                                          ▼
frontend ──/api/v1──▶ query-api ──▶ ClickHouse / VictoriaMetrics / VictoriaLogs
```

### 4.2 AI 诊断数据流（前端 → orchestrator）
```
frontend /aichat ──▶ query-api (JWT 鉴权 + ProxyAI 注入 token)
                        │
                        ▼
                   ai-orchestrator /api/v1/ai/chat
                        │  LangGraph DAG
                        ├─ 采集: 调 query-api 读指标/日志/链路/K8s
                        ├─ 分析: LLM 生成诊断/计划/脚本
                        ├─ 审批: 写操作需人工审批（human_approved）
                        ├─ 执行: execute_shell（白名单 + execution_gate）
                        └─ 结果: 回传前端流式输出
```

### 4.3 告警数据流
```
query-api evaluateAlerts（60s 轮询规则）──▶ alert_events（ClickHouse，TTL 30 天）
   │
   └─▶ webhook 通知 + 前端 /alerts 展示 + 可触发 AI 诊断（RCA）
```

---

## 5. 端口清单

| 端口 | 用途 | 暴露 |
|------|------|------|
| 30253 | 前端 NodePort（主入口） | 集群外 |
| 8080 | query-api / ai-orchestrator / ingest 内部 Service | 集群内 |
| 8123 / 9000 | ClickHouse HTTP / native | 集群内（需认证） |
| 3306 | MySQL | 集群内 |
| 6379 | Redis | 集群内（requirepass） |
| 9000 | MinIO | 集群内 |
| 9100 | node-exporter hostPort（hostNetwork） | 集群内 |
| 8428 / 9428 | VictoriaMetrics / VictoriaLogs | 集群内 |

---

## 6. 关键设计约定

- **密钥全部走环境变量/Secret**，代码无硬编码凭据/IP
- **可移植**：镜像 registry/tag、storageClass、中间件地址均可通过 `values.yaml` 覆盖
- **单写原则**：有状态组件固定单副本，避免共享 RWO PVC 数据损坏
- **安全边界**：WebShell/命令执行受白名单与审批门控；ClickHouse/Redis/MySQL 均启用认证

---

> 下一章：《[02 环境准备与前置](./02-prerequisites.md)》
