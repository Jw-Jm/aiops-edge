# AIOps 代码库理解与导读

> 目的：帮助新加入的开发者从代码而非产品宣传理解本仓库：请求从哪里进入、数据如何流动、每个服务负责什么、写操作如何被保护，以及应按什么顺序阅读。
>
> 本文基于仓库当前实现整理，侧重运行时边界。它不是部署操作手册；部署参数请另见 `docs/deploy/`。

## 1. 项目一句话

这是一个部署在 Kubernetes 上的 AIOps 平台。它把 OTLP 与 DeepFlow 采集到的日志、链路、指标和拓扑数据沉淀到可观测性存储；由 Go 查询服务向 Web 控制台提供查询、告警和配置能力；由 Python AI 编排服务完成诊断、RCA、知识检索、流程编排和受控运维动作。

核心闭环是：**采集 → 关联查询/告警 → AI 分析 → 人工审批 → 受限执行 → 验证、报告与审计**。

## 2. 运行时全景

```text
浏览器
  │
  ▼
frontend（React 静态资源 + nginx，唯一对外 Web 入口）
  │  /api/v1/*
  ▼
query-api（Go，JWT/RBAC、查询、告警、配置、AI 代理）
  ├──► ClickHouse：链路、日志、调用拓扑、告警事件
  ├──► VictoriaMetrics / VictoriaLogs：指标、日志查询
  ├──► MySQL：用户、配置、目录、拓扑、审批、审计等业务数据
  └──► ai-orchestrator（Python，内部 token 认证）
           ├──► query-api：回读指标、日志、Trace、K8s 等证据
           ├──► ChromaDB：RAG 知识库
           ├──► LLM（OpenAI 兼容接口；本地默认 mock）
           └──► kubectl / Shell：经策略、审批和预检后执行

应用 / OpenTelemetry SDK ──OTLP──► ingest（Go）──► ClickHouse + VictoriaMetrics
DeepFlow eBPF ──► DeepFlow 独立栈 ──► ingest 增量同步
Kubernetes Events / IPMI SEL ──► event-collector（Go）──► ClickHouse
```

仓库 README 中将其概括为四个自研服务，但当前 Helm Chart 还包含 `ai-event-collector`。它是事件采集辅助服务，不承担用户请求。

## 3. 目录与职责地图

- `observability-frontend/`：React 18 + TypeScript + Vite 控制台；产物由 nginx 托管。
- `ai-apm-query-go/`：Go HTTP API。它是前端唯一业务 API、告警评估器、MySQL 配置/目录服务，也是 AI 服务的安全代理。
- `ai-apm-ingest-go/`：Go OTLP 与 DeepFlow 数据入口，负责写 Trace、日志、聚合的 RED 指标和服务调用边。
- `ai-orchestrator/`：FastAPI AI 服务；包含 LangGraph 诊断图、RAG、工作流引擎、MCP、报告、审批与硬件采集领域逻辑。
- `ai-event-collector/`：Kubernetes Event Watch 与 IPMI SEL 采集，批量写 ClickHouse。
- `ipmi-exporter/`：IPMI Prometheus exporter 构建与部署资源。
- `deploy/helm/aiops/`：应用与中间件的 Helm Chart；`files/clickhouse/init_clickhouse.sql` 是 ClickHouse 初始化表结构。
- `deploy/scripts/`：镜像构建、安装、卸载、初始化和多集群演示脚本。
- `docs/deploy/`：部署、生产配置、使用和运维说明。
- `docs/SCHEMA_OWNERSHIP.md`：数据库单表单主写者契约，是跨服务开发必须遵守的边界。

## 4. 前端：页面、状态与请求边界

入口是 `observability-frontend/src/main.tsx`：挂载 React、Ant Design 中文 locale、主题和 `BrowserRouter`。应用壳在 `src/App.tsx`，其中定义侧栏信息架构、路由、登录保护、集群切换、告警角标与懒加载页面。

主要页面按领域分组：

- `pages/Overview/`：工作台首页与整体健康。
- `pages/observability/`：服务全景、Trace、日志指标、虚拟机、Grafana。
- `pages/alerts/`：规则与告警事件。
- `pages/ai/`：AI 对话、工具、知识库、知识图谱、工作流编辑器。
- `pages/infra/`：Kubernetes 运维、硬件健康、变更时间线。
- `pages/admin/`、`pages/slo/`、`pages/capacity/`、`pages/report/`：治理和运营能力。

### 4.1 请求层

`src/api/client.ts` 创建 Axios 实例，基地址固定为 `/api/v1`。其职责不只是封装 API：

1. 从 `localStorage` 读取 JWT 并注入 `Authorization: Bearer ...`；收到 401 时清除 token 并跳转登录页。
2. 注入 `X-Tenant-ID`，默认租户为 `default`。
3. 从持久化 UI store 读取当前 `cluster_id`；对于非全局接口自动加入查询参数，完成多集群过滤。
4. 按领域暴露前端可用函数。例如 `getServices`、`getAlertEvents`、`chatWithAI`、`runFlowAsync`、`nl2sqlTranslate`。

这意味着页面一般不直接知道后端地址，也不应绕过 `client.ts` 自行拼接请求。新增 API 时应先判定其是否与具体集群有关，并相应更新全局路径白名单。

### 4.2 nginx 的真实路由

`observability-frontend/nginx.conf` 将静态 SPA、Grafana 和 API 放在同一个域名下：

- `/`：SPA fallback 到 `index.html`。
- `/grafana/`：代理 DeepFlow namespace 的 Grafana，并重写其中资源路径。
- `/api/v1/ai/*`、`/api/v1/ops/*`：仍先到 query-api，不直连 orchestrator。
- 其余 `/api/*`：到 query-api；WebSocket Upgrade 头也在这里透传。

这个“统一先到 query-api”的设计是安全边界：query-api 完成客户端 JWT 验证后才为 AI 请求追加内部身份与共享 token。

## 5. query-api：查询、治理和可信代理

启动入口为 `ai-apm-query-go/cmd/api/main.go`。启动时会读取 ClickHouse/VictoriaMetrics 配置、调用 `store.EnsureSchema()`、创建 Handler、初始化告警 ClickHouse 写入通道、启动告警评估器、创建初始 admin、播种拓扑类型，然后注册路由和中间件。

内部结构大致分为：

- `internal/api/`：HTTP handler、中间件、告警、查询、认证、代理和基础设施 API。
- `internal/store/`：MySQL DAO 与幂等建表。
- `internal/biz/`：可复用业务聚合，例如 Dashboard。

### 5.1 主要职责

**可观测查询。** 服务、Trace、日志、指标、容量、Dashboard、拓扑与 DeepFlow 查询都在该服务暴露。它读取 ClickHouse、VictoriaMetrics/VictoriaLogs，并把结果转换为控制台需要的形状。

**业务配置。** 用户、租户、服务目录、设备、集群、拓扑目录、看板、SLO、LLM 设置和告警规则以 MySQL 为主存储。

**告警。** `internal/api/alert_engine.go` 在服务启动后运行评估器；规则保存在 MySQL，产生的 `alert_events` 写 ClickHouse。事件可确认、解决、聚合和静默。

**认证授权。** `internal/api/auth.go` 实现 JWT（HS256）和基于角色的包装器。登录、健康检查等为公开端点；其他 API 默认要求 JWT。读取目录资源与写入资源的角色要求也有所区分。

**AI 代理。** `ProxyAI` 位于 `internal/api/settings.go`。它不会简单地转发请求：会限制请求/响应体大小，删除客户端伪造的内部头，判定高危路径角色，加入请求者、审批人、角色与 `X-Internal-Token` 后，再转发到 orchestrator。LLM 配置也通过内部受保护路径供 orchestrator 获取，避免从浏览器直接交付真实密钥。

因此，面向浏览器的新 AI、MCP、硬件、SNMP 或 `/ops` 路径必须同时满足：在 `main.go` 显式注册代理、通过 JWT 中间件、且必要时列入高危代理路径规则。不能把不受保护的通配路径接到代理上。

## 6. ingest：从 OTLP Span 推导可观测模型

入口在 `ai-apm-ingest-go/cmd/ingest/main.go`，对外接口包括：

- `POST /v1/traces`：接收 OTLP JSON Trace。
- `POST /v1/logs`：接收 OTLP JSON Log。
- `POST /v1/deepflow`：接收 DeepFlow 原生数据。
- `/health`、`/metrics`：健康与 Prometheus 指标。

`INGEST_API_KEY` 配置后，采集入口要求 `X-Api-Key`；还可通过 `INGEST_RPS` 限制速率和通过请求体上限防止过大负载。服务实例从 `CLUSTER_ID` 获取所属集群，未配置时为 `default`。

### 6.1 Trace 处理流程

核心实现是 `internal/pipeline/ingest.go` 的 `Pipeline.ProcessOTLPTraces`：

1. 反序列化 OTLP ResourceSpans，提取 `service.name` 和资源属性。
2. 每个 span 被规范化为内部 `model.Span`，送入 ClickHouse writer。
3. 在同一请求内建立 `spanID → service` 映射，通过父 span 找到调用方服务。
4. 按租户、服务、调用方和分钟窗口累计调用量、错误量与时延。
5. 对有父子关系的 span 生成服务拓扑边。
6. 后台 flush loop 每 10 秒将聚合指标与边异步写出；关闭时做最终 flush。

这解释了平台的服务拓扑并非独立人工维护：主要调用边可从分布式 Trace 自动得出。日志路径同样会提取 resource/log attributes、服务名、Trace/Span ID，并写入 `log_records`。

### 6.2 可靠性与 DeepFlow

三个 writer（span、日志、指标/边）采用批处理，并可使用 `INGEST_WAL_DIR` 提供本地 WAL 重试；未挂载目录时退化为内存重试。健康检查同时验证 ClickHouse 可用性及重试队列水位。

DeepFlow 同步器可以用单一 `DEEPFLOW_CH_HOST`，也可用 `DEEPFLOW_CH_ENDPOINTS` 配置多个 `cluster@host:port`。它把 DeepFlow ClickHouse 中的应用层数据转写为本平台的 Trace、日志、拓扑和 RED 指标，因而 DeepFlow 是补充数据源，而非前端直接读取的唯一数据源。

## 7. event-collector 与硬件事件

`ai-event-collector/main.go` 启动 `EventWriter`，按配置可并行启动：

- Kubernetes Watcher：读取 Kubernetes Event。
- SEL Collector：读取服务器 IPMI System Event Log。

两类事件都批量写入 ClickHouse；服务暴露 `/health` 与 `/metrics`，健康状态会考虑 ClickHouse Ping 和重试队列水位。它与 ingest 的差异是：ingest 接收应用遥测，event-collector 接收集群与硬件事件。

在 AI 服务中，`ipmi_ingest.py`、`node_health.py`、`hardware_tools.py` 处理 IPMI 传感器、SEL 事件与节点组件健康聚合。相关 MySQL 表由 orchestrator 的迁移维护。

## 8. AI 编排服务：从问题到报告

`ai-orchestrator/main.py` 创建 FastAPI 应用，挂载 `flow_api` 和 `kg_api`，并在 lifespan 中完成：MySQL 迁移、知识播种、persona 注册、K8s 工具注册、异常扫描、告警转案例、节点健康聚合、工作流 cron 与知识图谱定时重建等后台任务。

它的 API 覆盖以下域：

- `/api/v1/ai/chat`：对话诊断。
- `/api/v1/ai/skills`、`agents`、`marketplace`：能力目录与 Agent 管理。
- `/api/v1/ai/workflows`、`flows`：自研工作流与内置 DAG 描述。
- `/api/v1/ops/*`：任务、审批、恢复策略、RCA、异常、案例、报告、审计和变更。
- `/api/v1/ai/knowledge/*`：知识库、playbook、RAG 与案例沉淀。
- `/api/v1/ai/nl2sql/*`：自然语言 SQL 的生成与执行。
- `/api/v1/mcp/*`：MCP 工具目录和调用。
- `/api/v1/ipmi/*`、`/node/*`、`/snmp/*`：硬件和网络设备领域。

### 8.1 LangGraph 诊断图

`orchestrator.py` 定义 `AgentState`，把一次请求贯穿的意图、服务、集群、采集证据、相似案例、计划、脚本、风险、审批、执行输出、验证结果、RCA 和报告放在统一状态中。

它的设计不是“让 LLM 直接执行命令”，而是将过程拆开：

```text
意图/服务识别
  → 采集服务、基础设施、指标、日志、Trace 等确定性证据
  → 检索 RAG 相似案例
  → 分析与多 Agent 子任务/审阅
  → 生成处置计划和风险判断
  → 若有写动作则中断等待人工审批
  → 受策略约束地执行
  → 比较前后指标、生成 RCA 和最终报告
```

信息查询会走轻量分流，避免无意义地进入完整诊断链路。LLM 调用使用共享的并发信号量和固定大小线程池，以限制超时残留调用的资源上界。

### 8.2 LLM、RAG、技能和人员角色

- `rag.py`、`playbook_loader.py`、`knowledge_seed.py`：ChromaDB 向量知识库与内置 playbook 的加载。
- `skill_registry.py`、`skills/`：工具定义、分类、审批标记和内置领域技能。
- `persona_registry.py`、`personas/`、`agent_tool.py`：专家 persona 及后台 worker。
- `dual_agent.py`：子任务拆分、并行处理与审阅合并。
- `llm_mock.py`：本地 mock 响应；`main.py` 默认令 `LLM_MOCK=true`，因此演示环境的 AI 结论不应被当作真实模型推理结果。

真实 LLM 密钥不放进 LangGraph state/checkpoint，而是在进程内存 holder 中保存；状态中只保留不含密钥的模型配置，以降低 checkpoint 与日志泄露风险。

### 8.3 自研工作流与知识图谱

`flow_engine/` 是独立于 LangGraph 诊断图的可视化工作流运行时：`graph.py` 管图结构，`noderegistry.py` 注册节点，`engine.py` 执行，`store.py` 持久化，`trigger_scheduler.py` 处理 cron，`flow_alert_dispatch.py` 把告警转为触发器。前端 Flow Editor 使用 `/ai/workflows` 进行 CRUD、运行和审批恢复。

`kg_graph.py`、`kg_tools.py`、`kg_api.py` 建立并暴露知识图谱。其定时构建器默认每 60 秒运行一次（可用 `KG_BUILD_INTERVAL_SECONDS=0` 关闭）。

## 9. 安全模型：三层防线

### 9.1 入口与身份

前端只持有 JWT；query-api 验证 JWT、角色和租户上下文。只有 query-api 才能向 orchestrator 注入受信的内部 token、操作者与审批人身份。orchestrator 的中间件会校验内部调用，从而避免用户绕开 Go API 伪造内部头。

### 9.2 工具分级与审批

`execution_gate.py` 根据 `ToolDef` 的类别和 `requires_approval` 决定工具是否能运行：普通 safe 只读工具可执行；`mutating`、`dangerous` 或显式要求审批的工具必须先获得批准。审批任务和审计日志由 orchestrator 维护。

### 9.3 Shell 与 Kubernetes 动作

`shell_policy.py` 对命令实施整段白名单、元字符检查、敏感路径限制和额外黑名单；不是只按命令前缀放行。`k8s_actions.py` 将动作限制为明确 schema，例如滚动重启、扩缩容、删除/驱逐 Pod、节点 cordon/drain。

高风险 K8s 动作还需经历：

1. 由 `preflight` 读取目标资源版本并生成绑定参数、五分钟有效的 HMAC token。
2. 写操作携带 token 与预期 `resourceVersion`。
3. 如果资源已变化，返回冲突并要求重新预检。
4. destructive 动作必须关联已批准任务；结果写入审计。

这是审批、参数绑定、乐观锁和命令白名单的叠加防护，任何一层不满足都不应执行。

## 10. 存储边界与表所有权

`docs/SCHEMA_OWNERSHIP.md` 的“单表单主写者”是重要架构规则。

### 10.1 ClickHouse

`deploy/helm/aiops/files/clickhouse/init_clickhouse.sql` 定义主要表：

- `trace_spans`、`log_records`、`service_topology`：唯一主写者是 ingest。
- `alert_events`：唯一主写者是 query-api 告警引擎，使用 ReplacingMergeTree 和 TTL。

query-api 读前面三类表，但不应写入或 `TRUNCATE` 它们。新写入方不应绕过这个约束。

### 10.2 MySQL

query-api 的 `internal/store` 拥有用户/会话、租户、服务目录、设备、集群、拓扑、平台设置、LLM 设置、告警/SLO 和 Dashboard 表。orchestrator 的 `migrations/*.sql` 拥有审批、审计、Agent、报告、知识库、治理规则、SNMP/IPMI 和节点组件健康表。

新增 MySQL 表时，应先判断它属于“查询/配置/治理”还是“AI 编排/审批/报告/硬件”，并只在对应主写者中做 DDL；跨服务需求走读接口或共享读模型，而不是两个服务同时迁移同一张表。

### 10.3 其他存储

- VictoriaMetrics：指标时间序列，采集层写入、查询层读取。
- VictoriaLogs：日志查询后端，query-api 代理查询。
- ChromaDB：AI RAG 向量知识库。
- Redis：AI 服务的 ARQ/异步任务相关能力。
- MinIO：报告、诊断产物和备份等对象。

## 11. 部署与配置读取顺序

Helm 根目录为 `deploy/helm/aiops/`：

- `Chart.yaml`：Chart 元数据。
- `values.yaml`：开发/默认配置；`values-prod.yaml` 与 `values-deepflow.yaml` 为变体。
- `templates/`：frontend、query-api、ingest、orchestrator、event-collector 和中间件工作负载、RBAC、Secret、NetworkPolicy、初始化 Job。
- `files/`：ClickHouse 与 MySQL 初始化资产。

默认 Web 入口为前端 NodePort；内部服务通常均监听 8080。敏感值应由 Helm Secret 与环境变量注入，包括 JWT 密钥、数据库密码、`INTERNAL_TOKEN`、采集 API key、LLM 连接配置和外部服务 token。不要把它们放入前端构建变量、代码常量或工作流定义。

## 12. 推荐阅读路径

对第一次接手项目的人，推荐按以下顺序，而不是直接进入超长的 `main.py`：

1. 根目录 `README.md` 与 `docs/deploy/01-architecture.md`：建立产品和服务全貌。
2. `docs/SCHEMA_OWNERSHIP.md`：先理解哪些服务可写哪些表。
3. 前端 `src/App.tsx` 与 `src/api/client.ts`：知道页面入口、请求与集群上下文。
4. `ai-apm-query-go/cmd/api/main.go`：查看所有 HTTP 边界及中间件顺序。
5. ingest 的 `cmd/ingest/main.go`、`internal/pipeline/ingest.go`：理解数据是怎样从 span 变成可查询模型的。
6. orchestrator 的 `main.py`、`orchestrator.py`、`tools.py`：理解 AI 请求和后台任务。
7. `execution_gate.py`、`shell_policy.py`、`k8s_actions.py`：在改任何执行相关代码前先读安全链路。
8. 具体功能再从前端 API 函数反查 query-api handler，或从 orchestrator 路由反查领域模块和测试。

## 13. 开发时最容易踩的边界

- **不要绕过 query-api 直连 orchestrator。** 这会绕过 JWT 到内部身份转换和高危路径控制。
- **不要让 LLM 的输出直接变成 shell。** 必须经过工具注册、执行闸门、审批和 ShellPolicy。
- **不要跨服务写同一张表。** 先查看 schema ownership。
- **不要忽略 `cluster_id` 与 `tenant_id`。** 前端会自动注入集群筛选；采集端也必须打上租户/集群标签。
- **不要将默认 Mock 当生产 AI。** 真实部署需显式关闭 `LLM_MOCK` 并配置可用的模型提供方。
- **不要把“能返回 200”视为健康。** ingest 和 event-collector 的健康检查还依赖 ClickHouse 连接和重试水位；新增依赖也应定义相应健康语义。

## 14. 结论

本项目的核心不是单个 AI 聊天接口，而是一个有明确服务边界的闭环系统：Go 采集/查询层负责可信数据面和访问控制，Python 编排层负责推理与流程，前端负责操作体验，而审批、审计、白名单和存储主写者规则共同约束自动化风险。

阅读或修改任何功能时，先回答四个问题：数据由谁写入？请求经谁认证？是否跨租户/跨集群？是否会造成真实世界的变更？这四个问题可以覆盖大多数架构与安全错误。
