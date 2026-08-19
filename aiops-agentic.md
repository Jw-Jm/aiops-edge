# AIOps Agentic 智能运维平台全面重构——Luna 强约束执行规格书 V9.0

> 文件位置：`/Users/mssc/Documents/Code/agent/aiops/aiops-agentic.md`
>
> 适用仓库：`/Users/mssc/Documents/Code/agent/aiops/`
>
> 基线代码：生成本文时仓库 `main` 分支 `2ce8df5`；正式实施必须在 Phase 0 重新记录实际 SHA，本文中的 SHA 只用于说明本次扫描来源。
>
> 文档性质：本文件是后续 Luna 实施该仓库全面重构的文件级执行合同。本次只生成规格书，不在本次任务中实施重构。

---

# 0. 使用规则与最高约束

## 0.1 指令优先级

实施时按以下优先级解决冲突：

1. 用户在实施会话中的最新明确指令；
2. 本文件的最终目标、阶段顺序、数据保护和安全约束；
3. 仓库中的真实代码、测试、运行时和部署事实；
4. 既有设计、审计和测试报告。

代码事实与本文文件路径发生偏差时，Luna 必须先在 Phase 0 更新 Code Map，再以最小必要偏差实现同一目标；不得因路径变化改成另一套架构。所有偏差必须写入最终实施报告。

## 0.2 范围

本次重构覆盖整个项目，不得缩成 AI Chat 子模块：

- `observability-frontend/`
- `ai-apm-query-go/`
- `ai-apm-ingest-go/`
- `ai-event-collector/`
- `ai-orchestrator/`
- `ipmi-exporter/`
- `deploy/helm/aiops/`
- `deploy/scripts/`
- ClickHouse、VictoriaMetrics、VictoriaLogs、MySQL、ChromaDB、MinIO 与 Kubernetes 集成边界
- API、SSE、RBAC、依赖、镜像、测试、部署、真实 LLM 和浏览器 E2E

`ongrid-ref/` 是被 `.gitignore` 排除的外部参考副本，只能用于阅读对标，不得成为生产依赖或被提交。

## 0.3 历史数据与配置保护

以下历史运行数据全部不迁移，允许在正式切换阶段清理并由新链路重新产生：

```text
Metrics
Logs
Trace / Span
Topology 派生数据
Alert / Event 历史
AI Session / Chat
LangGraph checkpoint
Workflow Run / Tool Run
RCA / Evidence / Hypothesis
Action / Verification / Report 历史
容量预测和异常检测派生历史
```

以下当前有效配置和资产不得误删、不得明文导出、不得写进 Git：

```text
用户、角色、审批人标记、租户与授权范围
JWT/内部 Token/采集 API Key/数据库密码
Kubernetes Secret、证书、API Token
当前 LLM Provider、模型和加密配置
当前数据源、集群和 Helm 关键配置
有效知识文档、Runbook、Playbook
仍被当前系统使用的服务目录和治理配置
```

运行历史可以清空；配置和凭据必须保护。清理前必须按表、PVC、对象存储前缀和 namespace 列出精确目标，禁止对工作区根目录、数据库实例或 namespace 做模糊递归删除。

## 0.4 不允许的最终状态

最终不得保留以下双主路径：

```text
旧 AI Chat 主流程 + 新 Investigation 主流程
旧 if/else Tool Router + 新 Tool Registry
旧 AgentState 自由文本字段 + 新结构化 AIOpsState
旧 Prompt-only RCA + Hypothesis RCA
旧 Session/Checkpoint 业务模型 + 新 Run Model
旧 observability schema + 新 clean schema
旧独立 Workflow 调查入口 + Planner DAG 调查入口
旧图谱主产品入口 + 嵌入式 Resource Graph
```

迁移中的短暂并存只能存在于同一 Phase 的红绿切换期间；该 Phase Gate 前必须完成生产调用切换和旧路径删除。

## 0.5 实施纪律

每个 Task 必须执行同一闭环：

1. 读取本 Task 列出的真实文件和调用方；
2. 写失败测试并确认失败原因正确；
3. 实现最小可用改动；
4. 运行聚焦测试；
5. 运行相邻模块测试；
6. 自审数据隔离、安全和错误语义；
7. 删除被替代且已无生产调用的旧代码；
8. 再次运行聚焦、相邻和 Gate 测试；
9. 记录文件、API、表、依赖和镜像变化；
10. Gate 未通过不得进入下一 Phase。

禁止先重写全项目、最后一次性补测试。

---

# 1. 当前仓库 Code Map（基于真实代码）

## 1.1 服务与入口

### observability-frontend

- 语言/框架：React 18、TypeScript、Vite、Ant Design、Zustand。
- 入口：`observability-frontend/src/main.tsx`。
- 应用壳、导航和路由：`observability-frontend/src/App.tsx`。
- 统一 API 客户端：`observability-frontend/src/api/client.ts`。
- 集群 UI 状态：`observability-frontend/src/store/uiStore.ts`、`src/components/ClusterSwitcher.tsx`。
- 身份状态：`observability-frontend/src/store/authStore.ts`、`src/components/RequireAuth.tsx`。
- AI 浮动入口：`observability-frontend/src/components/AiDock.tsx`。
- 运行边界：`observability-frontend/nginx.conf` 统一把 API 先代理到 query-api。
- 生产镜像：`observability-frontend/Dockerfile`，构建后由 nginx 提供 `dist`。

当前页面文件：

```text
pages/Overview/index.tsx
pages/observability/ServiceObservability.tsx
pages/observability/Trace.tsx
pages/observability/LogMetrics.tsx
pages/observability/VirtualMachines.tsx
pages/observability/Grafana.tsx
pages/alerts/AlertEvents.tsx
pages/alerts/AlertRules.tsx
pages/ai/AiChat.tsx
pages/ai/AiTools.tsx
pages/ai/Knowledge.tsx
pages/ai/KnowledgeGraph.tsx
pages/ai/Workflows/index.tsx
pages/ai/Workflows/Editor.tsx
pages/ai/Workflows/Detail.tsx
pages/capacity/Capacity.tsx
pages/infra/K8sActions.tsx
pages/infra/Hardware.tsx
pages/infra/Changes.tsx
pages/slo/SLO.tsx
pages/report/Report.tsx
pages/admin/Approvals.tsx
pages/admin/AdminUsers.tsx
pages/admin/AdminSettings.tsx
```

当前一级产品问题：`AI 对话`、`图谱视图`、`AI 工具`、`工作流`、`报告中心`、`变更时间线`、`容量预测` 和独立 Grafana 仍在导航中直接暴露技术能力，未收敛为任务驱动信息架构。

### ai-apm-query-go

- 入口：`ai-apm-query-go/cmd/api/main.go`。
- 启动顺序：`store.EnsureSchema()` → `api.NewHandler()` → ClickHouse 告警写入/告警引擎 → admin 与拓扑类型种子 → `http.ServeMux` → CORS/Auth。
- HTTP 路由集中注册在 `cmd/api/main.go`；`internal/api/router.go` 只注册 Grafana 路由。
- 查询/代理处理器：`ai-apm-query-go/internal/api/`。
- MySQL DAO：`ai-apm-query-go/internal/store/`。
- 业务聚合：`ai-apm-query-go/internal/biz/dashboard.go`；当前没有统一 Resource Resolver/use-case 层。
- AI 安全代理：`internal/api/settings.go` 的 `ProxyAI`。
- JWT/RBAC：`internal/api/auth.go`。
- 集群注册与 kubeconfig 查询：`internal/api/clusters.go`、`internal/store/clusters.go`。
- 生产镜像：`ai-apm-query-go/Dockerfile`，多阶段构建并携带运行所需 `kubectl`。

当前 API 域：

```text
/api/v1/auth、/users、/me
/api/v1/clusters、/catalog、/devices、/tenants
/api/v1/services、/traces、/metrics、/logs、/topology
/api/v1/infrastructure、/capacity、/dashboard、/grafana
/api/v1/alerts、/slo、/system、/settings
/api/v1/ai、/ops、/mcp、/ipmi、/node、/shell（代理到 orchestrator）
```

已确认缺口：

- `cluster_id` 目前主要是客户端可传过滤器；JWT scope 未统一强制应用于 ClickHouse、VictoriaMetrics、K8s 和 AI 代理请求。
- `tenant_id` 可来自客户端 `X-Tenant-ID`/query，而 JWT 无可信 tenant claim。
- `clusters.name`、ClickHouse `cluster_id`、`default` 和数据库数值 ID 并存，没有 canonical resolver。
- `alert_events` 缺 `tenant_id`。
- `clusters.kubeconfig` 当前存于 MySQL 文本列，必须按敏感凭据重新设计为 `credential_ref`。
- query-api 仍代理 `/snmp/*`，而 orchestrator 已删除 SNMP 端点，存在死路由。

### ai-apm-ingest-go

- 入口：`ai-apm-ingest-go/cmd/ingest/main.go`。
- 测试/演示流量：`ai-apm-ingest-go/cmd/loadgen/main.go`。
- 数据模型：`internal/model/span.go`、`log.go`、`metrics.go`。
- OTLP 处理与聚合：`internal/pipeline/ingest.go`。
- DeepFlow：`internal/pipeline/deepflow.go`、`deepflow_sync.go`。
- ClickHouse 写入：`internal/clickhouse/writer.go`、`log_writer.go`、`metrics_writer.go`。
- WAL：`internal/clickhouse/wal.go`，属于当前可靠性机制，必须保留并适配新 schema。
- 生产镜像：`ai-apm-ingest-go/Dockerfile`。

当前写入所有权：

```text
trace_spans       → ingest
log_records       → ingest
service_topology  → ingest
VictoriaMetrics   → ingest 聚合指标
```

### ai-event-collector

- 入口：`ai-event-collector/main.go`。
- 配置：`config.go`。
- Kubernetes Event：`k8s_events.go`。
- IPMI SEL：`sel_events.go`。
- ClickHouse writer：`clickhouse.go`。
- 当前自建并写入 `observability.k8s_events`；该表尚未进入 Helm 的统一 ClickHouse 初始化文件，必须收敛表所有权和初始化方式。
- 生产镜像：`ai-event-collector/Dockerfile`，运行时保留 `ipmitool`。

### ai-orchestrator

- FastAPI 入口和大量路由：`ai-orchestrator/main.py`。
- 当前 LangGraph 主图：`ai-orchestrator/orchestrator.py`。
- 当前状态 `AgentState` 同时混合自由文本采集结果、RAG、计划、脚本、执行、验证、RCA 和报告字段。
- 工具实现：`ai-orchestrator/tools.py`、`function_calling.py`、`mcp_server.py`。
- Tool/Skill/Expert 注册：`skill_registry.py`、`skill_loader.py`、`skills/`、`persona_registry.py`。
- RCA：`rca.py`、`investigator.py`、`detector.py`。
- 审批与审计：`db_approval.py`、`db_audit.py`、`execution_gate.py`。
- K8s 写动作：`k8s_actions.py`；Shell 约束：`shell_policy.py`、`shell_ws.py`。
- RAG/知识：`rag.py`、`playbook_loader.py`、`knowledge_seed.py`、`data/playbooks/`。
- 图谱：`kg_graph.py`、`kg_tools.py`、`kg_api.py`。
- 独立 Flow Engine：`flow_api.py`、`flow_engine/`。
- MySQL 迁移：`migrations/0001_business_tables.sql` 至 `0004_change_events.sql`。
- 生产依赖：`requirements.txt`。
- 生产镜像：`Dockerfile`；当前通过 `bin/*.tar.gz` 离线注入完整 site-packages、模型缓存和二进制，是镜像体积审计重点。

已确认双主路径/兼容负担：

```text
main.py /ai/chat + /ops/rca* + /ai/flows/*/run-legacy
orchestrator.py 自由文本 AgentState
session_store.py + LangGraph SQLite checkpoint
main.py 对旧 checkpoint/session 卡片的 fallback/兼容读取
tools.py、skill_registry.py、function_calling.py、MCP 多套调用包装
rca.py、investigator.py、LangGraph 内部 RCA 多入口
Flow Engine 调查能力与 LangGraph 调查能力并存
```

这些只能在替代契约和测试生效后删除；不得因关键词匹配直接删除当前仍被生产调用的恢复、安全或业务能力。

### 部署与存储

- Helm Chart：`deploy/helm/aiops/`。
- ClickHouse 初始化：`deploy/helm/aiops/files/clickhouse/init_clickhouse.sql`。
- MySQL 初始化/迁移 Job：`deploy/helm/aiops/files/mysql/migrations/0001_init.sql` 与相关 templates。
- 主要 values：`values.yaml`、`values-prod.yaml`、`values-deepflow.yaml`。
- 镜像构建：`deploy/scripts/build-images.sh`。
- 部署：`deploy/scripts/apply.sh`，默认构建最新源码并复用既有 Secret。
- 版本：`deploy/scripts/version.sh`。
- 多集群演示：`deploy/scripts/multicluster-demo.sh`。
- 默认部署测试：`deploy/scripts/test-default-deploy.sh`。

当前 ClickHouse 主表：

```text
observability.log_records
observability.trace_spans
observability.service_topology
observability.alert_events
observability.k8s_events（event-collector 自建，需纳入统一初始化）
```

当前 MySQL 存在一个必须解决的所有权冲突：query-api 的 `internal/store/mysql.go` 与 orchestrator 的 `migrations/0001_business_tables.sql` 都创建 `audit_logs`，列宽和索引定义不同。Phase 1 必须指定唯一主写者和唯一 DDL。

## 1.2 当前真实调用链

```text
Browser
  → frontend nginx
  → query-api JWT/RBAC/tenant/cluster 边界
  → ClickHouse / VictoriaMetrics / VictoriaLogs / MySQL / K8s
  → query-api ProxyAI
  → ai-orchestrator LangGraph / RAG / K8sGPT / Flow / approval
  → query-api 回读可观测事实
  → SSE / JSON 返回 frontend

OTLP / DeepFlow
  → ai-apm-ingest-go
  → ClickHouse + VictoriaMetrics

Kubernetes Event / IPMI SEL
  → ai-event-collector
  → ClickHouse k8s_events
```

不得让浏览器绕过 query-api 直连 orchestrator；不得让 orchestrator 绕过 query-api 的可信 scope 读取任意租户/集群数据。

---

# 2. 最终平台形态（产品级强约束）

## 2.1 一句话产品定义

最终平台必须是：

> 面向多 Kubernetes 集群和云原生业务的、任务驱动、Evidence 驱动、人在回路中的 Agentic AIOps 智能运维平台。

它不是“可观测页面 + AI Chat + Tool/Workflow/图谱入口”的功能集合，而是一个统一运维控制面：用户从问题、告警或专业页面进入后，平台自动规划调查、调用真实数据、形成可审计根因、生成结构化处置方案、执行风险与审批控制，并验证业务是否真正恢复。

最终产品价值固定为：

```text
更快发现真实问题
自动完成跨指标/日志/Trace/K8s/变更/知识的调查
用 Evidence 和反证降低错误 RCA
让高风险动作始终经过审批和审计
用业务恢复验证结束闭环，而不是以命令成功结束
在多个集群之间保持严格数据与权限隔离
```

## 2.2 产品边界

最终平台固定包含：

```text
一个统一 Web 控制台
一个外部信任入口 query-api
一个 Agentic AI 控制面 ai-orchestrator
一个统一可观测数据面
多个受管 Kubernetes 集群
一套统一 Resource Identity
一条调查—处置—验证主链
```

最终平台不得呈现为：

```text
AI 聊天机器人
Tool Marketplace 产品
Workflow 编辑器产品
知识图谱浏览器产品
Grafana 链接集合
多个互不关联的监控页面
自动执行任意 Shell 的机器人
```

Tool Registry、MCP、K8sGPT、RAG、Resource Graph、LangGraph、Flow Engine 和模型配置是后台能力或管理员能力，不是普通运维用户的主要产品对象。

## 2.3 最终导航信息架构

最终普通用户导航固定为：

```text
总览

智能运维
├─ 智能调查
├─ 告警与事件
└─ 审批任务

可观测
├─ 服务
├─ 链路
└─ 日志与指标

资源
├─ Kubernetes
├─ 主机与虚机
└─ 容量与硬件

治理
├─ 知识与 Runbook
├─ 变更与审计
└─ SLO

系统管理
├─ 用户与权限
└─ 设置
```

以下不得继续作为普通用户一级导航：

```text
AI 工具
图谱视图
Workflow
K8sGPT
RAG
Planner
Agent
报告中心
独立变更时间线
独立容量预测
独立 Grafana
```

这些能力的最终位置固定为：

- AI Tool/Agent/模型状态：`系统管理 → 设置 → AI / Agent`。
- Workflow：仅保留非调查自动化，在高级管理中配置；普通调查页只显示调查步骤。
- Resource Graph：嵌入服务详情、资源详情和 RCA Evidence Graph。
- K8sGPT：Kubernetes Agent 的只读 Tool。
- RAG/知识检索：Knowledge Agent 的能力；知识文档维护位于治理。
- 报告：作为调查详情的产物和历史视图，不设独立一级产品中心。
- 变更：嵌入调查时间轴，并在治理中提供审计视图。
- 容量：并入资源域；Grafana 作为专业深挖入口嵌入相关页面。

## 2.4 用户与角色的最终体验

### 普通运维用户

可以：

- 查看被授权 tenant/cluster 的总览和专业可观测数据；
- 从告警、服务、日志、Trace、K8s 或自然语言发起智能调查；
- 查看计划、Tool 状态、Evidence、Hypothesis、根因、未知项和处置建议；
- 对 R2 用户确认动作进行确认；
- 查看自己有权访问的 run、审批状态和验证结果。

不可以：

- 修改用户、Secret、LLM、集群凭据或全局策略；
- 批准 R3/R4 动作；
- 通过修改浏览器 localStorage 扩大权限；
- 让 LLM 直接执行命令。

### 审批人

在普通用户能力基础上，可以审批其 tenant/cluster/resource scope 内的 R2/R3 动作。审批界面必须显示根因置信度、关键 Evidence、目标、影响范围、具体参数、rollback、action digest、resourceVersion 和有效期。

审批人不能审批超出其授权 scope 的动作，不能审批已变化的 Action，也不能复用过期审批。

### 管理员

可以管理用户与权限、Cluster Registry/credential_ref、LLM Provider、Tool/Agent 状态、非调查 Workflow、平台配置和系统健康。管理员仍不能绕过 Evidence、Risk、Approval、审计和 Verification 合同。

## 2.5 最终核心页面合同

### 总览

必须回答：

```text
当前哪些集群/服务正在异常？
哪些 SLO 正在燃烧？
有哪些高优先级告警和进行中的调查？
有哪些待审批动作？
最近变更是否与异常相关？
数据源或 Agent 能力是否不可用？
```

总览卡片必须可进入告警、服务、调查或审批，不得只展示无法追溯的聚合数字。无数据、不可用和权限不足必须分开显示。

### 智能调查

这是最终平台的核心工作台，主体固定包含：

```text
调查对象与 canonical cluster/namespace/time range
用户问题或触发告警
调查计划与依赖关系
当前进度与 heartbeat
每个 Tool 的真实状态和耗时
关键 Evidence 与原始数据跳转
根因候选、支持证据、反证和缺失证据
二次调查过程
最终根因与 confidence breakdown
Unknowns
结构化处置方案与 rollback
Risk、影响范围与审批状态
执行结果
Before/After 恢复验证
调查历史和审计链
```

完整 LLM Markdown 只能出现在“详细分析”区域，不能替代上述结构化组件。

### 告警与事件

必须展示告警生命周期、聚合、静默、确认、解决、关联资源、关联变更和调查状态。任一告警可以一键创建带完整上下文的调查。Alert 只能作为问题信号，不能默认显示为根因。

### 审批任务

必须展示审批原因、Evidence、Root Cause confidence、Action、目标、参数、blast radius、环境、rollback、请求人、审批人、过期时间和审计。审批页不得只显示 shell 文本。

### 服务

必须统一显示 RED、SLO、上下游、运行资源、近期异常、相关日志/Trace/变更，并可带当前服务和时间范围发起调查。服务列表和拓扑入口必须打开同一份完整服务详情，不能出现一个入口数据完整、另一个入口数据缺失。

### 链路

必须支持 Trace 查询、错误/慢 Trace、瀑布图、Critical Span、上下游和正常/异常 Trace 差异；可把选中 Trace 直接交给调查。

### 日志与指标

必须至少包含“原始日志”和“异常模式”两个主视图。异常模式显示 Pattern、Baseline、Current、Growth、First Seen，并能跳原始日志、Trace、Pod、Service 和智能调查。指标视图必须显示资源 identity、时间范围、数据源和空/错状态。

### Kubernetes

必须展示集群、namespace、Node、Deployment、Pod、Events、requests/limits、Probe、Scheduling、PVC 和动作入口。读取与诊断默认只读；任何写动作进入统一 Action/Risk/Approval/Execution/Verification 链。

### 主机、虚机、容量与硬件

必须只展示当前真实接入的数据源。没有 BMC、SMART、NIC CRC、SNMP 或硬件事件时显示 `UNKNOWN/未接入`，不得生成模拟健康结论。容量结论必须标明指标来源、当前值、趋势、时间窗和置信度。

### 知识、变更、审计与 SLO

- 知识与 Runbook：维护有效文档、案例和审核后的 Incident Candidate；自动生成内容不得未经审核进入正式知识库。
- 变更与审计：统一展示发布、镜像、ConfigMap、K8s Event、平台操作、审批、执行与验证记录。
- SLO：定义目标、窗口、burn rate 和关联服务；SLO 状态可进入调查和验证。

### 系统管理

必须包含用户/角色/scope、Cluster Registry、credential_ref、LLM Provider、Tool/Agent status、延迟/失败率、平台组件健康和高级 Workflow。普通用户不能访问或调用管理写能力。

## 2.6 两条固定用户主旅程

### 旅程 A：从问题到根因

```text
用户在服务页看到 orders 错误率上升
→ 点击“交给 AI 调查”
→ 平台冻结 tenant/cluster/service/time range
→ Intent 识别 RCA
→ Planner 先查 Metrics 定位异常窗口
→ Logs/Trace/K8s/Change 并行调查
→ Knowledge 补充 Runbook/历史案例
→ Evidence Hub 建立事实链
→ Hypothesis RCA 产生、反证、补证并排序
→ 页面显示最终根因、confidence、unknowns 和所有跳转
```

用户不需要选择 Tool、Agent、K8sGPT、RAG 或 Workflow。

### 旅程 B：从根因到恢复

```text
Root Cause confirmed/supported
→ 生成结构化 OpsAction
→ Risk Engine 计算动作风险、置信度、blast radius 和环境风险
→ R2 用户确认 / R3 审批 / R4 严格审批或禁止
→ Execution Adapter 执行已批准且未变化的 Action
→ 观察窗口
→ 对比 Before/After 指标、可用性、Ready、重启和 SLO
→ success/partial/failed/regressed/unknown
→ regressed 停止并生成 rollback 建议
→ 成功调查生成待审核 Incident Candidate
```

## 2.7 最终后台能力关系

```text
Frontend
  → Query API（JWT、tenant、cluster、RBAC、Resource Resolver、事实查询、AI Proxy）
    → ClickHouse / VictoriaMetrics / VictoriaLogs / MySQL / Kubernetes
    → AI Orchestrator
       → Intent Engine
       → Planner / LangGraph Investigation DAG
       → Domain Agents
       → Tool Registry
       → Evidence Hub / Resource Graph
       → Hypothesis RCA
       → Remediation / Risk / Approval / Execution / Verification

OTLP / DeepFlow → Ingest → 新 Trace / Log / Metric / Topology
K8s Event / IPMI SEL → Event Collector → 新 Resource Event
```

Flow Engine 只服务非调查型自动化；不能与 Planner 共同控制调查。知识图谱能力收敛为 Resource Graph；不能与 Resource Resolver 建立第二套资源身份。

## 2.8 最终体验和质量硬约束

- 页面任何“成功、健康、无异常”结论必须有数据源状态和时间范围。
- 任一 Root Cause、Evidence、Action、Approval、Execution、Verification 都必须有稳定 ID 和审计链接。
- 任一 API 失败都必须给用户可见反馈；禁止 console-only 错误和 silent catch。
- 任一长调查都必须有进度、heartbeat、取消和断线恢复。
- 任一页面都必须显示当前 tenant/cluster；调查 run 的 cluster 一经创建不可被全局切换器改变。
- 任一 Evidence deep link 必须回到产生该 Evidence 的 cluster、resource 和 time range。
- 普通用户不得被要求理解 LangGraph、MCP、K8sGPT、RAG 或 Tool Registry 才能完成调查。
- 产品默认语言为中文；关键状态使用稳定中文文案并保留机器状态码。
- 桌面端主工作台不得出现横向溢出；关键 Drawer/Modal 在 1280px 宽度可完整操作。
- 空状态必须区分：尚无数据、筛选无结果、数据源未配置、数据源不可达、权限不足、工具不可用。

## 2.9 本轮明确不做的最终产品能力

```text
Neo4j
独立大型 ML 日志平台
新独立向量数据库
全自动 L4/L5 运维
无限制 Shell
全部硬件厂家适配
历史运行数据迁移和恢复
跨租户资源关系
未经审核自动写入正式知识库
长期保留两套调查主流程
```

## 2.10 Product End-State Gate

在最终验收前必须逐项证明：

- 用户不进入技术能力页即可完成“发现问题→调查→根因→处置→审批→执行→验证”。
- 总览、专业页面和智能调查共享同一 tenant/cluster/resource/time context。
- 智能调查展示的是结构化过程和证据，不是长 Markdown 聊天。
- 专业页面能验证 AI，AI Evidence 能回专业页面。
- 普通用户、审批人、管理员三类角色的产品体验和服务端权限一致。
- 无数据、不可用、权限不足和健康在所有核心页面语义一致。
- 最终导航与 2.3 完全一致；被降级能力不再作为普通一级入口。
- 两条固定用户主旅程在真实浏览器和真实数据上通过。

Product End-State Gate 未通过，即使后端模块测试全部通过，也不得宣称平台重构完成。

---

# 3. 目标架构与固定契约

## 3.1 唯一生产主链

```text
User / Alert / Page Context
  → Trusted Tenant + Cluster Resolver
  → OpsIntent
  → Planner
  → Investigation DAG
  → Domain Agents
  → Tool Registry
  → ToolResult
  → Evidence Hub
  → Hypothesis RCA
  → Root Cause Ranking
  → Structured Remediation
  → Risk Engine
  → Approval
  → Execution Adapter
  → Before/After Verification
  → Incident Candidate
```

LLM 可以规划、解释和形成假设，但现场事实只能来自 Evidence；LLM 不得直接执行 raw shell，不得把 `no_data`、`unavailable`、`permission_denied` 或 `timeout` 攢成“系统健康”。

## 3.2 Canonical Context

新增统一上下文，所有 Query API、Agent、Tool、Evidence、Action 和 SSE 事件必须携带：

```python
@dataclass(frozen=True)
class RequestContext:
    tenant_id: str
    user_id: str
    roles: list[str]
    allowed_cluster_ids: list[str]
    cluster_id: str
    namespace: str | None
    request_id: str
```

约束：

- `tenant_id`、`user_id`、roles 和 allowed clusters 由 query-api 从 JWT/数据库派生，客户端不得覆盖。
- `cluster_id` 必须是 Cluster Registry 中的 canonical slug/UUID；`all` 只允许具有跨集群权限的只读聚合请求。
- 每次 AI 调查必须冻结 resolved context，后续步骤不得静默切换集群。
- 非默认集群无凭据时返回 `unavailable`，不得回退当前 kubectl context。

## 3.3 Resource Identity

统一格式：

```text
urn:aiops:{tenant_id}:{cluster_id}:{resource_type}:{namespace-or-_}:{name}
```

第一阶段固定 `resource_type`：

```text
cluster namespace node deployment pod service container trace span alert event device vm
```

新增 query-api `ResourceResolver`，负责把 OTLP `service.name`、K8s labels、日志字段、Trace 字段、DeepFlow 字段和目录元数据映射到 canonical resource ID。Resolver 不做 LLM 推理。

## 3.4 ToolDefinition 与 ToolResult

在 `ai-orchestrator/contracts.py` 定义唯一契约，在 `ai-orchestrator/tool_registry.py` 实现唯一注册与调用入口：

```python
@dataclass
class ToolDefinition:
    name: str
    category: str
    description: str
    read_only: bool
    risk_level: int
    capabilities: list[str]
    available: bool
    timeout_seconds: int

@dataclass
class ToolResult:
    tool_name: str
    success: bool
    status: str
    summary: str
    data: Any
    error_code: str | None
    error_message: str | None
    evidence_ids: list[str]
    started_at: datetime
    finished_at: datetime
```

`status` 只能是：

```text
success partial no_data failed timeout unavailable permission_denied
```

所有现有工具必须通过 adapter 返回该契约；禁止 `[]`、`None`、异常字符串或自然语言承担状态语义。

最低注册项：

```text
query_metrics query_logs query_traces query_alerts query_topology
query_k8s k8sgpt_diagnose knowledge_search query_changes
execute_k8s execute_shell
```

## 3.5 Evidence、Hypothesis、Action 与 Verification

统一数据模型写入 `ai-orchestrator/contracts.py`，持久化模型写入新的 investigation 表：

```python
@dataclass
class Evidence:
    id: str
    run_id: str
    source: str
    evidence_type: str
    resource_id: str | None
    cluster_id: str
    namespace: str | None
    timestamp: datetime | None
    start_time: datetime | None
    end_time: datetime | None
    fact: str
    confidence: float
    severity: str | None
    trace_id: str | None
    raw_data: Any
    metadata: dict

@dataclass
class Hypothesis:
    id: str
    title: str
    description: str
    supporting_evidence: list[str]
    contradicting_evidence: list[str]
    missing_evidence: list[str]
    confidence: float
    status: str

@dataclass
class OpsAction:
    type: str
    target: dict
    parameters: dict
    risk_level: int
    expected_effect: str
    rollback: dict | None

@dataclass
class VerificationResult:
    status: str
    before_snapshot: dict
    after_snapshot: dict
    observation_window_seconds: int
    checks: list[dict]
    summary: str
```

Evidence 类型第一阶段固定：

```text
metric_anomaly log_pattern log_error trace_anomaly k8s_state k8s_event
alert change knowledge_case topology_relation resource_state capacity_anomaly hardware_event
```

Hypothesis 状态固定：`candidate supported rejected unknown confirmed`。

Verification 状态固定：`success partial failed regressed unknown`。`regressed` 必须停止自动链并建议 rollback。

## 3.6 Intent、Plan 与 State

```python
@dataclass
class OpsIntent:
    intent: str
    target_type: str | None
    target_name: str | None
    resource_id: str | None
    cluster_id: str
    namespace: str | None
    time_range: dict
    requested_capabilities: list[str]
    action_mode: str

@dataclass
class InvestigationPlan:
    id: str
    goal: str
    target: dict
    steps: list[PlanStep]

@dataclass
class PlanStep:
    id: str
    agent: str
    action: str
    parameters: dict
    depends_on: list[str]
    required: bool
    status: str
```

LangGraph state 必须收敛为结构化 `AIOpsState`：

```text
request_context, user_message, intent, plan,
tool_results, evidence, hypotheses, root_causes,
remediation_plan, approval_state, execution_results,
verification, report, errors
```

现有 `AgentState` 中 `services_data`、`infra_data`、`red_metrics`、`trace_data`、`k8sgpt_raw`、`similar_cases`、`rca_evidence` 等自由文本字段不得继续作为跨节点主契约。

## 3.7 SSE 固定协议

唯一事件集合：

```text
session_start
intent
plan_start plan_step_start plan_step_end
tool_start tool_end
evidence
hypothesis root_cause
remediation
approval_required
execution_start execution_end
verification
report
heartbeat
error
done
```

每个事件必须包含 `run_id`、`sequence`、`timestamp`、`cluster_id` 和结构化 `payload`。长任务每 10 至 15 秒发送 heartbeat。错误码至少包含：

```text
connection_lost llm_timeout tool_timeout backend_error permission_denied user_abort
invalid_context cluster_unavailable no_data
```

前端不得解析 Markdown 或自然语言猜测 Tool、审批、执行或验证状态。

---

# 4. 文件级处置总表

## 4.1 KEEP（保留并通过新契约复用）

- `ai-apm-query-go/internal/api/auth.go`：JWT、用户禁用回查、admin/approver 门禁；补 tenant/cluster claim 与 scope 强制执行。
- `ai-apm-query-go/internal/api/settings.go`：保留 query-api 作为 AI 信任代理；改为注入可信 RequestContext。
- `ai-apm-query-go/internal/api/clusters.go`、`internal/store/clusters.go`：保留 Cluster Registry 基础；改 credential_ref、canonical ID 和 scope。
- `ai-apm-ingest-go/internal/clickhouse/wal.go`：保留故障恢复语义并适配新记录格式。
- `ai-apm-ingest-go/internal/pipeline/deepflow*.go`：保留 DeepFlow 数据源，统一 identity/schema。
- `ai-event-collector/k8s_events.go`、`sel_events.go`：保留真实事件来源，统一 schema。
- `ai-orchestrator/k8s_actions.py`、`shell_policy.py`、`execution_gate.py`：保留安全边界并接入 OpsAction/Risk/Approval。
- `ai-orchestrator/rag.py`、`playbook_loader.py`、`data/playbooks/`：保留知识资产和检索实现，由 Knowledge Agent 调用。
- `ai-orchestrator/flow_engine/`：只保留非调查型用户自动化工作流；调查控制权转给 Planner。
- `ai-orchestrator/kg_graph.py`、`kg_tools.py`：保留关系计算能力，重构为 Resource Graph 服务。
- `deploy/scripts/apply.sh` 的 Secret 复用和默认 clean build 行为。
- 专业页面：服务、日志与指标、Trace、K8s、告警、审批、用户和设置。

## 4.2 MODIFY

- `observability-frontend/src/App.tsx`：按任务型 IA 重写导航和路由。
- `observability-frontend/src/api/client.ts`：停止信任客户端 tenant/cluster，增加 investigation/run API 与 SSE client。
- `observability-frontend/src/store/uiStore.ts`：cluster 选择改为 canonical ID；受限用户不出现越权 `all`。
- `observability-frontend/src/components/AiDock.tsx`：发送完整页面上下文，跳转/嵌入智能调查。
- `observability-frontend/src/pages/ai/AiChat.tsx`：由新 Investigation 页面替代后删除。
- `observability-frontend/src/pages/observability/LogMetrics.tsx`：增加原始日志/异常模式双视图与 Evidence 跳转。
- `observability-frontend/src/pages/observability/ServiceObservability.tsx`、`Trace.tsx`、`infra/K8sActions.tsx`、`alerts/AlertEvents.tsx`：增加“交给 AI 调查”和反向 Evidence 定位。
- `ai-apm-query-go/cmd/api/main.go`：注册统一 investigation/resource API，删除切换完成后的死代理路由。
- `ai-apm-query-go/internal/api/handler.go`：拆分超长通用查询逻辑，复用底层查询，统一 RequestContext/ResourceResolver。
- `ai-apm-query-go/internal/store/mysql.go`：停止内联兼容 DDL 扩散，冻结配置表并解决 audit_logs 双 DDL。
- `ai-apm-ingest-go/internal/model/*.go`、`internal/pipeline/*.go`、`internal/clickhouse/*.go`：统一 resource/tenant/cluster/schema。
- `ai-event-collector/clickhouse.go`：不再运行服务自有 DDL；使用 Helm 统一 schema。
- `ai-orchestrator/main.py`：拆分路由与 lifespan，最终只保留新 run API 和仍有业务价值的管理 API。
- `ai-orchestrator/orchestrator.py`：替换自由文本 State 和 15-node 旧主图。
- `ai-orchestrator/skill_registry.py`、`tools.py`、`function_calling.py`、`mcp_server.py`：收敛到唯一 Tool Registry，管理 API 可保留 registry 视图。
- `ai-orchestrator/rca.py`、`investigator.py`：逻辑迁入 Hypothesis RCA 后删除旧生产入口。
- `ai-orchestrator/session_store.py` 与 checkpoint：只保留新 run recovery 所需实现，不兼容旧历史。
- `deploy/helm/aiops/files/clickhouse/init_clickhouse.sql`、MySQL migrations、所有 deployment templates 和 Dockerfile。

## 4.3 ADD（精确新增路径）

```text
ai-apm-query-go/internal/api/investigation.go
ai-apm-query-go/internal/api/resource.go
ai-apm-query-go/internal/biz/investigation.go
ai-apm-query-go/internal/biz/resource_resolver.go
ai-apm-query-go/internal/store/investigation.go

ai-apm-ingest-go/internal/model/resource.go
ai-apm-ingest-go/internal/pipeline/resource_resolver.go

ai-orchestrator/contracts.py
ai-orchestrator/request_context.py
ai-orchestrator/tool_registry.py
ai-orchestrator/intent_engine.py
ai-orchestrator/planner.py
ai-orchestrator/evidence_hub.py
ai-orchestrator/resource_graph.py
ai-orchestrator/hypothesis_rca.py
ai-orchestrator/remediation.py
ai-orchestrator/risk_engine.py
ai-orchestrator/verification.py
ai-orchestrator/investigation_store.py
ai-orchestrator/investigation_api.py
ai-orchestrator/investigation_agents/__init__.py
ai-orchestrator/investigation_agents/observability.py
ai-orchestrator/investigation_agents/log.py
ai-orchestrator/investigation_agents/trace.py
ai-orchestrator/investigation_agents/kubernetes.py
ai-orchestrator/investigation_agents/change.py
ai-orchestrator/investigation_agents/knowledge.py
ai-orchestrator/investigation_agents/infrastructure.py

observability-frontend/src/api/investigations.ts
observability-frontend/src/pages/ai/Investigation/index.tsx
observability-frontend/src/components/investigation/PlanPanel.tsx
observability-frontend/src/components/investigation/EvidenceList.tsx
observability-frontend/src/components/investigation/HypothesisPanel.tsx
observability-frontend/src/components/investigation/RemediationPanel.tsx
observability-frontend/src/components/investigation/VerificationPanel.tsx
```

对应测试文件必须与模块同 Phase 添加；不得先创建空壳文件。

## 4.4 DELETE（仅在替代 Gate 通过且调用图为零后执行）

明确删除候选：

```text
ai-orchestrator/main.py 中 /api/v1/ai/flows/{key}/run-legacy
query-api 中无后端实现的 /api/v1/snmp* 代理
旧 /api/v1/ai/chat 与 session 兼容 endpoint（新 run API 切换后）
main.py 的旧 checkpoint/session 卡片兼容 fallback
旧 Prompt-only RCA 入口 /ops/rca、/ops/rca/deep、/ops/rca/alert（新 RCA API 覆盖后）
普通用户的独立 /ai/tools、/kg、/ai/workflows 主路由
仅被上述页面使用的 API wrapper、state、CSS 和依赖
observability-frontend/src_legacy_v2/ 本地旧备份（它已被 gitignore；只在用户确认后从磁盘清理）
```

`backup-pre-reset-20260816/`、`backup-pre-p0-20260816.bundle`、`1.md`、`2.md` 和未跟踪二进制属于现有用户文件；实施期间不得擅自删除。最终 GitHub 同步只需确保它们未被提交；若用户明确要求清理，再做可恢复移动或精确删除。

---

# 5. 严格实施阶段

# Phase 0：冻结基线与生成真实地图

## Task 0.1 基线记录

**Preconditions / 输入**

- 当前仓库、当前分支、当前运行环境、当前 Kubernetes context。
- 不修改源码、不删除文件、不调整依赖、不重建数据。

**Existing Code Inspection**

- 根目录、五个自研服务、`ipmi-exporter/`、`deploy/`、`docs/`。
- `git status --short --branch`、`git rev-parse HEAD`、最近提交。
- 所有 Dockerfile、依赖清单、Helm values/templates、SQL、路由、页面和测试。

**Modification Scope / ADD / MODIFY / DELETE**

- ADD：`BEFORE_BASELINE.md`。
- ADD：`docs/luna/phase0-code-map.md`、`phase0-api-map.md`、`phase0-data-map.md`、`phase0-page-map.md`、`phase0-dependency-map.md`。
- MODIFY：无。
- DELETE：无。

**Data Input / Processing / Output**

- 输入：Git、依赖清单、schema、构建产物、当前镜像和部署状态。
- 处理：记录而不修复；成功和失败都必须保留命令、退出码和失败原因。
- 输出：Git SHA、工作区状态、测试结果、API/页面/表清单、镜像大小、直接依赖和当前 P0/P1。

**API Input / Output**

- 仅枚举当前注册路由及已知调用方；不得调用写 API。

**Error Semantics**

- 工具缺失、Docker daemon 不可用、K8s 不可达分别记录 `unavailable`；不得记为通过或零数据。

**Multi-cluster Requirements**

- 记录 Cluster Registry、JWT scope、前端 `currentClusterId`、所有数据源实际 cluster filter 和默认回退。

**Security / RBAC Requirements**

- 不读取或打印 Secret 明文、kubeconfig 内容、LLM API Key、Token、数据库密码。
- 只记录 Secret 名、key 名、存在性和来源。

**Unit / Integration / E2E Tests**

```bash
cd ai-orchestrator && .venv-312/bin/python -m pytest tests -q
cd ai-orchestrator && .venv-312/bin/python -m compileall -q .
cd ai-orchestrator && .venv-312/bin/python -m pip check
cd ai-apm-query-go && go test ./...
cd ai-apm-ingest-go && go test ./...
cd ai-event-collector && go test ./...
cd observability-frontend && npm run build
helm lint deploy/helm/aiops
git diff --check
```

若 venv、npm、helm 或 Docker 环境不同，先记录实际可执行路径；不得悄悄换成较弱验证。

**Acceptance Criteria / Gate 0**

- 能回答当前服务、入口、页面、API、数据链、AI 链、存储所有权、依赖和镜像大小。
- `BEFORE_BASELINE.md` 与五份 Code Map 完整，失败项有明确原因。
- 未修改生产代码、配置或运行数据。

**禁止事项**

- 以已有审计报告替代当前基线。
- 因测试失败直接修改代码；Phase 0 只记录。

---

# Phase 1：目标架构、所有权与契约冻结

## Task 1.1 架构决策和 schema 所有权

**Preconditions / 输入**

- Gate 0 通过。
- Phase 0 的 API、数据和调用图已确认。

**Existing Code Inspection**

- `docs/SCHEMA_OWNERSHIP.md`。
- `ai-apm-query-go/internal/store/mysql.go`。
- `ai-orchestrator/migrations/*.sql`。
- `deploy/helm/aiops/files/clickhouse/init_clickhouse.sql`。
- `ai-event-collector/clickhouse.go`。

**Modification Scope / ADD / MODIFY / DELETE**

- ADD：`docs/AIOPS_AGENTIC_ARCHITECTURE.md`、`docs/AIOPS_DATA_MODEL_REDESIGN.md`。
- MODIFY：`docs/SCHEMA_OWNERSHIP.md`，指定唯一 DDL/写者。
- MODIFY：`audit_logs` 唯一所有者固定为 orchestrator，用于 AI、审批、执行和验证审计；query-api 的平台操作审计改写独立 `platform_audit_logs`，由 query-api 唯一建表和写入。两个服务不得创建或写入对方审计表。
- DELETE：本 Task 不删表；只冻结废弃列表和 cutover 顺序。

**Data Input / Processing / Output**

- 输入：现有表、DAO、writer、读者、备份清单。
- 处理：按单表单主写者分类配置数据、可观测事实、AI 控制面和审计数据。
- 输出：旧表废弃列表、新表列表、owner、writer、reader、TTL、初始化方式、清理和回滚窗口。

**API Input / Output**

- 冻结 query-api 为浏览器唯一业务入口。
- 冻结 orchestrator 只接受 query-api 注入的可信内部上下文。

**Error Semantics**

- schema/version 不匹配必须使服务 readiness 失败，禁止静默降级到旧表。

**Multi-cluster Requirements**

- 所有新运行数据表和 AI run 表必须包含 `tenant_id`、canonical `cluster_id`。
- 跨集群聚合必须由服务端授权，不以客户端 `all` 作为授权依据。

**Security / RBAC Requirements**

- `clusters.kubeconfig` 从业务表明文迁出，改存 `credential_ref`；本仓库部署固定由 Kubernetes Secret 提供凭据。
- 数据库和采集器运行账号不再持有无关 DDL 权限。

**Unit / Integration / E2E Tests**

- schema ownership 静态检查：同名表不得被两个服务 DDL。
- Helm template 中初始化 Job 必须先于写者 readiness。
- 各表 owner 与代码 writer 逐项核对。

**Acceptance Criteria / Gate 1A**

- 无同表双 DDL、双主写者和未知 owner。
- 新旧表、清理范围、配置保护范围和回滚策略明确。

**禁止事项**

- 用“沿用现状”跳过 `audit_logs`、`k8s_events` 或凭据存储冲突。

## Task 1.2 控制面契约测试骨架

**Preconditions / 输入**

- Gate 1A 通过，目标 contracts 已冻结。

**Existing Code Inspection**

- `ai-orchestrator/models.py`、`skill_registry.py`、`orchestrator.py`。
- `ai-apm-query-go/internal/api/auth.go`、`handler.go`。

**Modification Scope / ADD / MODIFY / DELETE**

- ADD：`ai-orchestrator/contracts.py` 及 `tests/test_contracts.py`。
- ADD：query-api RequestContext/ResourceRef Go types 与 contract tests。
- MODIFY：本 Task 只增加契约和 serialization tests，不切生产路径。
- DELETE：无。

**Data Input / Processing / Output**

- 输入：JSON 请求、ToolResult、Evidence、Plan、Hypothesis、Action、Verification、SSE envelope。
- 处理：严格校验枚举、必填字段、时间和 ID。
- 输出：跨 Python/Go/TypeScript 一致 JSON schema 样例，保存到 `docs/contracts/`。

**API Input / Output**

- 生成有效/无效 payload fixture；无效状态、缺 cluster、缺 evidence 引用必须拒绝。

**Error Semantics**

- validation error 使用稳定 `error_code` 和字段路径，不返回 Python/Go 内部异常堆栈。

**Multi-cluster Requirements**

- fixture 必须包含同名 service 位于两个 cluster 的场景。

**Security / RBAC Requirements**

- RequestContext 中不允许客户端提交 roles/allowed clusters。

**Tests**

- Python Pydantic/dataclass contract tests。
- Go JSON marshal/unmarshal tests。
- 前端 TypeScript fixture type-check。

**Acceptance Criteria / Gate 1**

- contracts 和 ownership 文档通过自审；所有契约测试通过。

**禁止事项**

- 在契约未通过时开始 Agent、RCA 或 UI 主路径实现。

---

# Phase 2：多集群信任边界与 Resource Identity

## Task 2.1 Trusted RequestContext 与 Cluster Resolver

**Preconditions / 输入**

- Gate 1 通过。

**Existing Code Inspection**

- `ai-apm-query-go/internal/api/auth.go`、`clusters.go`、`settings.go`、`handler.go`。
- `ai-apm-query-go/internal/store/clusters.go`、`users.go`。
- `observability-frontend/src/store/uiStore.ts`、`api/client.ts`。

**Modification Scope / ADD / MODIFY / DELETE**

- ADD：`internal/biz/resource_resolver.go`、`internal/api/resource.go`。
- MODIFY：JWT/DB 派生 tenant、roles、allowed clusters；所有 handler 使用统一 context。
- MODIFY：Cluster 表使用稳定 canonical ID、slug、environment、region、credential_ref、status、labels。
- MODIFY：`ProxyAI` 删除客户端伪造的内部上下文 header，再注入签名/可信 context。
- DELETE：Gate 通过后删除 `id=1 → default`、任意 `X-Tenant-ID` 和受限用户 `all` 的隐式兼容。

**Data Input / Processing / Output**

- 输入：JWT、用户 scope、Cluster Registry、资源引用。
- 处理：授权 → canonical cluster resolve → resource resolve → context freeze。
- 输出：RequestContext、ResourceRef、明确的 allowed/denied 结果。

**API Input / Output**

```text
GET  /api/v1/resources/resolve?type=&name=&cluster_id=&namespace=
GET  /api/v1/clusters
POST /api/v1/clusters
PUT  /api/v1/clusters/{id}
```

- `/resources/resolve` 只返回调用者有权访问的 canonical resource。
- cluster create/update 只接受 `credential_ref`，不回传 kubeconfig。

**Error Semantics**

```text
invalid_context 400
permission_denied 403
resource_not_found 404
cluster_unavailable 503
ambiguous_resource 409
```

**Multi-cluster Requirements**

- `cluster-a/orders` 与 `cluster-b/orders` 必须得到不同 resource ID。
- 无权访问 `cluster-b` 的用户传 `all` 或 cluster-b 必须 403，不得返回空列表伪装成功。

**Security / RBAC Requirements**

- 前端 localStorage role 只用于展示，服务端每次裁决。
- 非默认集群无 credential_ref 不回退当前 context。
- `GET /settings/llm` 不得匿名泄露配置；公开端点只可返回 health/status 的最小信息。

**Tests**

- JWT tenant/cluster claim 和数据库 scope tests。
- CH/VM/K8s/AI proxy 的强制 scope tests。
- 伪造 `X-Tenant-ID`、internal header、localStorage role 的越权 tests。

**Acceptance Criteria / Gate 2A**

- 所有数据源和 AI proxy 共享同一授权后 context。
- 多集群同名资源不冲突、不串数据。

**禁止事项**

- 让前端 Axios 拦截器继续承担授权。
- 通过 SQL 字符串零散拼接复制 scope 逻辑。

## Task 2.2 Resource Resolver 贯穿数据模型

**Preconditions / 输入**

- Gate 2A 通过。

**Existing Code Inspection**

- ingest `internal/model/*.go`、`pipeline/ingest.go`、`deepflow_sync.go`。
- event collector `k8s_events.go`、`sel_events.go`。
- query-api topology/service handlers。

**Modification Scope / ADD / MODIFY / DELETE**

- ADD：`ai-apm-ingest-go/internal/model/resource.go`、`pipeline/resource_resolver.go`。
- MODIFY：Span、Log、Topology、Event 写入 canonical IDs。
- MODIFY：DeepFlow server span 的 instance identity 使用目标服务；持久化 source/target namespace。
- DELETE：切换后删除仅按 service name、pod name 或 `default` 关联的旧 reader/formatter。

**Data Input / Processing / Output**

- 输入：OTLP attributes、DeepFlow fields、K8s metadata、目录元数据。
- 处理：规范 tenant/cluster/namespace/type/name，生成 resource ID，保存原始来源 metadata。
- 输出：统一 ResourceRef 和 topology relations。

**API Input / Output**

- query-api 服务、日志、Trace、K8s、Topology 输出均包含 `resource_id`。

**Error Semantics**

- identity 缺失但信号仍可保存时标记 `partial` 并记录 missing fields；禁止猜 cluster/namespace。

**Multi-cluster Requirements**

- DeepFlow endpoint 配置必须同时携带 tenant 和 canonical cluster，不允许 tenant 固定 `default`。

**Security / RBAC Requirements**

- 不把 kubeconfig、Token 或敏感 labels 写入 raw_data。

**Tests**

- OTLP/DeepFlow/K8s/SEL resource mapping fixtures。
- 两集群同名 service/pod 不冲突。

**Acceptance Criteria / Gate 2**

- 所有新写入信号均可解析到 canonical identity 或明确 partial/unknown。

**禁止事项**

- 使用 LLM 解析 Resource ID。

---

# Phase 3：Clean Data Model 与统一初始化

## Task 3.1 ClickHouse / Metrics / Logs / Trace / Event schema

**Preconditions / 输入**

- Gate 2 通过；已确认不迁移历史运行数据。

**Existing Code Inspection**

- `deploy/helm/aiops/files/clickhouse/init_clickhouse.sql`。
- ingest 三个 writer 的 TabSeparated 列序。
- event collector `chDDL` 和 writer。
- query-api 所有 CH SQL。

**Modification Scope / ADD / MODIFY / DELETE**

- MODIFY：统一 Helm ClickHouse 初始化文件，纳入 trace、log、topology、alert、resource event。
- MODIFY：collector 不再运行 DDL；运行账号移除 DDL 权限。
- ADD：版本化 schema 记录和初始化 Job 验证。
- DELETE：旧表只在 Phase 16 精确清理；本 Task 不清数据。

**Data Input / Processing / Output**

新 Trace 至少包含：

```text
tenant_id cluster_id resource_id trace_id span_id parent_span_id
service pod namespace node operation start_time duration_ns status/error attributes source
```

新 Log 至少包含：

```text
tenant_id cluster_id resource_id timestamp namespace service pod container node
level message trace_id span_id source attributes
```

Topology 至少包含 source/target resource ID、namespace、relation type、window、calls/errors/latency、source。

Resource Event 至少包含 event UID、resourceVersion、count、reason、type、reporting source、involved resource ID、first/last time、tenant/cluster。

Alert Event 必须增加 tenant、resource ID 和调查关联字段。

Metrics 使用 VictoriaMetrics，统一 labels：`tenant_id`、`cluster_id`、`resource_id`、`namespace`、`service`、`pod`、`node`；禁止依赖互不兼容的 `job/app/deployment` 作为唯一身份。

**API Input / Output**

- query-api 新旧 SQL 不并行保留；在 Gate 前一次切换到新列。

**Error Semantics**

- writer 列序/schema version 不匹配必须 readiness fail；不得写入错位数据。

**Multi-cluster Requirements**

- ORDER BY、partition、索引和查询条件以 tenant/cluster/time/resource 为核心。

**Security / RBAC Requirements**

- raw attributes 经过敏感字段过滤；禁止写 Token、Authorization、Secret data。

**Tests**

- DDL/serializer 列序 contract tests。
- ClickHouse 容器集成：写入、查询、TTL、ReplacingMergeTree 去重。
- VictoriaMetrics label 隔离 tests。

**Acceptance Criteria / Gate 3A**

- 初始化可从空存储创建完整 schema；全部 writer/reader 使用同一版本。
- `k8s_events` 纳入初始化、备份和恢复清单。

**禁止事项**

- 写旧表升级/历史行转换脚本。
- 让长期运行服务自行建表。

## Task 3.2 AI Run schema

**Preconditions / 输入**

- Gate 3A 通过，控制面 contracts 已冻结。

**Existing Code Inspection**

- orchestrator migrations、`db.py`、`store.py`、`db_approval.py`、`session_store.py`、Flow store。

**Modification Scope / ADD / MODIFY / DELETE**

- ADD：新 migration 创建 `ai_runs`、`ai_plan_steps`、`ai_tool_runs`、`ai_evidence`、`ai_hypotheses`、`ai_actions`、`ai_verifications`、`ai_approval_decisions`。
- MODIFY：审批/审计表关联 `run_id`、tenant、cluster、action digest。
- DELETE：新路径生效后删除旧 AI 历史 reader 和旧 checkpoint 业务兼容；不迁移旧行。

**Data Input / Processing / Output**

- 每表主键、run_id、tenant/cluster、状态、created/updated time、版本和 provenance 明确。
- Evidence raw_data 使用受控 JSON；大对象写 MinIO，仅保存 immutable reference/digest。

**API Input / Output**

- run 查询返回结构化步骤、tool、evidence、hypothesis、action、approval、verification。

**Error Semantics**

- 持久化失败不得把内存任务标记成功；返回 `backend_error` 并可安全重试。

**Multi-cluster Requirements**

- 一个 run 默认只绑定一个 canonical cluster；跨集群健康检查拆成 parent run + per-cluster child run。

**Security / RBAC Requirements**

- 不持久化 LLM API Key、kubeconfig、approval token 明文。

**Tests**

- 空库 migration、重复 migration、事务回滚、权限、run recovery tests。

**Acceptance Criteria / Gate 3**

- 数据面和 AI 控制面均可从空库初始化；配置表保持不受清理影响。

**禁止事项**

- 让 LangGraph checkpoint 继续充当唯一业务数据库。

---

# Phase 4：采集链与 Event Collector 重构

## Task 4.1 OTLP / DeepFlow ingest 切换

**Preconditions / 输入**

- Gate 3 通过；新 schema 可从空存储初始化。

**Existing Code Inspection**

- `ai-apm-ingest-go/cmd/ingest/main.go`。
- `internal/pipeline/ingest.go`、`deepflow.go`、`deepflow_sync.go`。
- `internal/clickhouse/writer.go`、`log_writer.go`、`metrics_writer.go`、`wal.go`。

**Modification Scope / ADD / MODIFY / DELETE**

- MODIFY：OTLP Trace/Log 和 DeepFlow 映射写新 schema/identity。
- MODIFY：DeepFlow tenant 不再硬编码 `default`；endpoint 配置绑定 canonical cluster。
- MODIFY：Topology 写入 source/target namespace/resource ID。
- KEEP：span/log/edge 独立 WAL、写前 append、ack、replay、compact、有界重试、health 和优雅退出。
- KEEP：`POST /v1/deepflow` 在无真实协议实现时继续明确返回 501。
- DELETE：新链验证后删除旧 writer/字段转换和死 `metric_service_red` reader/DDL。

**Data Input / Processing / Output**

- 输入：OTLP JSON、DeepFlow application_map 和 l7_flow_log。
- 处理：校验认证 → 解析 → Resource Resolver → 敏感字段过滤 → WAL → batch writer。
- 输出：新 Trace、Log、Topology 与 VictoriaMetrics labels。

**API Input / Output**

- `/v1/traces`、`/v1/logs` 成功只表示已可靠接收，不代表所有后端查询立即可见。
- 响应包含 accepted/rejected count、request_id 和 schema_version。

**Error Semantics**

- invalid payload=400；unauthorized=401；backpressure=429；storage unavailable 且 WAL 可接收=202/partial；WAL 不可写=503。

**Multi-cluster Requirements**

- cluster 来自受信部署配置/API key 映射，不接受 payload 任意覆写。
- 同一 ingest 处理多集群时每条记录带 canonical context；WAL record 保留该 context。

**Security / RBAC Requirements**

- 保留 `INGEST_API_KEY`、body size、rate limit；日志不打印 payload 中的敏感值。

**Tests**

- HTTP handler、OTLP mapping、DeepFlow mapping、WAL replay、schema integration、跨集群隔离、错误语义。
- `go test ./...`、`go vet ./...`；race 工具链不可用必须记录环境原因。

**Acceptance Criteria / Gate 4A**

- 正常/错误/慢请求和日志可经真实入口写入新表并查询；重启 replay 不串 cluster、不丢 context。

**禁止事项**

- 删除 WAL 以简化 schema。
- 把 DeepFlow synthetic trace 冒充原始分布式 Trace；必须标记 `source=deepflow` 和 synthetic 属性。

## Task 4.2 Kubernetes Event / IPMI SEL collector

**Preconditions / 输入**

- Gate 4A 通过；统一 Resource Event 表已初始化。

**Existing Code Inspection**

- `ai-event-collector/main.go`、`config.go`、`k8s_events.go`、`sel_events.go`、`clickhouse.go`。
- Helm event-collector Deployment/RBAC 和 backup ConfigMap。

**Modification Scope / ADD / MODIFY / DELETE**

- MODIFY：K8s watcher 保存 UID、resourceVersion、count、reporting source 和 canonical resource ID。
- MODIFY：K8s watch 使用单 leader/单 Deployment；IPMI SEL 继续按节点 DaemonSet 或独立采集单元，避免每节点重复 watch 全集群。
- ADD：EventWriter WAL 或持久 outbox，checkpoint key 为 tenant+cluster+source。
- MODIFY：移除服务启动 DDL，使用 Helm schema。
- DELETE：替代验证后删除内存-only retry 作为唯一可靠性路径。

**Data Input / Processing / Output**

- 输入：K8s LIST/WATCH Warning/Error，IPMI SEL。
- 处理：UID 去重、410 relist、乱序窗口、resource resolve、WAL/outbox、batch write。
- 输出：统一 Resource Event。

**API Input / Output**

- collector 只暴露 health/metrics；无用户写 API。

**Error Semantics**

- API forbidden=permission_denied；watch timeout=timeout 并重连；ClickHouse down 时保持 backlog 并反映 degraded。

**Multi-cluster Requirements**

- 任何 checkpoint/query 都必须同时过滤 tenant 和 cluster；不得只用 source+cluster。

**Security / RBAC Requirements**

- K8s watcher 只需 get/list/watch Events 与读取必要 metadata；不得赋写权限。
- IPMI 凭据仅来自 Secret。

**Tests**

- fake K8s API：list/watch、410、UID 去重、RBAC denied。
- SEL parser、重启去重、WAL replay、ClickHouse integration。

**Acceptance Criteria / Gate 4**

- event-collector 不自建表；重启/短时 CH 故障不丢已接收事件；重复率和 backlog 可观测。

**禁止事项**

- 把 permission denied 转成“当前无事件”。

---

# Phase 5：Query API 与统一调查查询层

## Task 5.1 Investigation Resource APIs

**Preconditions / 输入**

- Gate 4 通过，新数据链可写入事实。

**Existing Code Inspection**

- `cmd/api/main.go` 路由。
- `internal/api/handler.go`、`metrics.go`、`infrastructure.go`、`topology_graph.go`、`alerts.go`。
- `internal/biz/dashboard.go`、`internal/store/*`。

**Modification Scope / ADD / MODIFY / DELETE**

- ADD：`internal/api/investigation.go`、`resource.go`、`internal/biz/investigation.go`、`resource_resolver.go`。
- MODIFY：底层现有查询函数抽成可复用 repository/use-case；调查 API 不复制 SQL。
- MODIFY：所有现有专业 API 同样使用 Trusted RequestContext。
- DELETE：新 API 覆盖且调用图为零后删除重复 handler/DTO/旧 schema adapter。

**Data Input / Processing / Output**

- 输入：RequestContext、ResourceRef、time range、query-specific options。
- 处理：授权 → resolve → 执行真实数据查询 → 统一状态/分页/provenance。
- 输出：结构化事实，供专业页面和 Agent 共用。

**API Input / Output**

```text
GET /api/v1/investigation/resource
GET /api/v1/investigation/metrics
GET /api/v1/investigation/logs
GET /api/v1/investigation/traces
GET /api/v1/investigation/k8s
GET /api/v1/investigation/changes
GET /api/v1/investigation/topology
GET /api/v1/investigation/alerts
```

统一响应：

```json
{
  "status": "success|partial|no_data|failed|timeout|unavailable|permission_denied",
  "summary": "orders 在 2026-08-19T09:30:00Z 至 2026-08-19T10:00:00Z 的错误率为 12.4%",
  "data": {},
  "provenance": {
    "source": "victoria-metrics",
    "query_id": "qry_01HZX7QK2Q5B7H9R2X4W6N8M0P",
    "start": "2026-08-19T09:30:00Z",
    "end": "2026-08-19T10:00:00Z"
  },
  "error": null
}
```

**Error Semantics**

- 无数据 `200/no_data`；无权限 `403/permission_denied`；后端不可达 `503/unavailable`；查询超时 `504/timeout`。

**Multi-cluster Requirements**

- 每个查询先验证 cluster scope；跨集群聚合结果按 cluster 分组并保留 provenance。

**Security / RBAC Requirements**

- SQL/PromQL/LogSQL 参数化或白名单；限制 time range、limit、body size 和并发。

**Tests**

- 每个 endpoint contract tests；CH/VM/VLogs/K8s/MySQL error semantics；tenant/cluster isolation；pagination/time range。

**Acceptance Criteria / Gate 5A**

- Agent 不需要自行拼接存储 URL 或复制 SQL；专业页面与 AI 查询相同底层事实。

**禁止事项**

- Agent 直连 ClickHouse/MySQL 规避 query-api 授权。

## Task 5.2 AI Proxy 可信上下文

**Preconditions / 输入**

- Gate 5A 通过。

**Existing Code Inspection**

- `internal/api/settings.go` 的 `ProxyAI`、高风险路径判定、quota 和 headers。
- `observability-frontend/nginx.conf`。

**Modification Scope / ADD / MODIFY / DELETE**

- MODIFY：ProxyAI 解析并签发可信 RequestContext；向 orchestrator 传 tenant/user/roles/allowed cluster/run request ID。
- MODIFY：禁止原样透传客户端伪造内部 header、tenant 或超范围 cluster。
- DELETE：orchestrator 已无实现的 `/snmp/*` 代理。
- DELETE：新 API 切换后旧 `/ai/chat`、session、RCA 代理项按调用图逐项移除。

**Data Input / Processing / Output**

- 输入：JWT + 用户请求。
- 处理：鉴权、scope resolve、高风险角色判断、request signing、限流、stream proxy。
- 输出：安全转发的 run API/SSE。

**API Input / Output**

- 浏览器只访问 query-api；内部 token 和可信 context 不回传浏览器。

**Error Semantics**

- orchestrator 超时、断流、拒绝、下游错误必须保留分类，不统一成 network error。

**Multi-cluster Requirements**

- proxy 必须冻结单 run cluster；不得在 query string 与 body 中出现冲突 cluster。

**Security / RBAC Requirements**

- `X-Internal-Token` 只在集群内部网络可达；NetworkPolicy 限制来源。

**Tests**

- header stripping/signing、quota、body limit、stream flush、disconnect、RBAC、scope propagation。

**Acceptance Criteria / Gate 5**

- 从浏览器到 orchestrator 的身份、tenant、cluster 和权限链可审计且不可由前端伪造。

**禁止事项**

- 让 nginx 直接代理浏览器到 orchestrator。

---

# Phase 6：可信 AI Core

## Task 6.1 Tool Registry / ToolResult / Evidence Hub

**Preconditions / 输入**

- Gate 5 通过；调查查询 API 稳定。

**Existing Code Inspection**

- `skill_registry.py`、`tools.py`、`function_calling.py`、`mcp_server.py`、`flow_engine/nodes_aiops.py`。
- `orchestrator.py` 中显式 k8sgpt/knowledge 特殊路径。

**Modification Scope / ADD / MODIFY / DELETE**

- ADD：`contracts.py`、`tool_registry.py`、`evidence_hub.py`。
- MODIFY：现有 ToolDef/registry 保留管理元数据，但执行统一经新 registry adapter。
- MODIFY：所有工具返回 ToolResult 并由 Evidence Hub 转换事实。
- DELETE：Gate 通过后删除各调用点对字符串、任意 dict、空数组和异常字符串的特殊判断。

**Data Input / Processing / Output**

- 输入：RequestContext、ToolDefinition、parameters。
- 处理：availability → parameter validation → risk/read-only gate → timeout → invoke → normalize → evidence persist。
- 输出：ToolResult + evidence IDs。

**API Input / Output**

- `/api/v1/mcp/tools` 与 AI Tool 管理页只展示 registry 视图；MCP call 也必须返回 ToolResult。

**Error Semantics**

- K8sGPT success+empty=no_data；binary missing=unavailable；RBAC denied=permission_denied。
- RAG success+empty=no_data；Chroma unavailable=unavailable。

**Multi-cluster Requirements**

- tool parameters 不接受与 RequestContext 冲突的 cluster；所有 Evidence 固定 cluster。

**Security / RBAC Requirements**

- read_only 与 risk_level 由代码注册，LLM 不得修改。

**Tests**

- 每个状态 contract tests；timeout/cancel；Evidence provenance；权限和空结果语义。

**Acceptance Criteria / Gate 6A**

- 全部生产 Tool 经唯一 registry；Tool 状态与 LLM 报告不冲突；Evidence 可回原始数据。

**禁止事项**

- 一个 Tool 包装成一个 Agent。

## Task 6.2 Intent Engine / Planner / AIOpsState

**Preconditions / 输入**

- Gate 6A 通过。

**Existing Code Inspection**

- `models.py` ChatRequest/TaskRequest。
- `orchestrator.py` AgentState、路由、graph build、stream_sync。
- `skill_registry.py` keyword match 与 `dual_agent.py`。

**Modification Scope / ADD / MODIFY / DELETE**

- ADD：`intent_engine.py`、`planner.py`、新结构化 graph state。
- MODIFY：LangGraph 成为唯一 Investigation DAG runtime；Planner 是唯一调查控制器。
- MODIFY：页面上下文和 Alert Context 进入 OpsIntent。
- DELETE：切换后删除多个独立 if/else 直接控制调查数据源的生产路由。

**Data Input / Processing / Output**

- 输入：用户问题/告警 + RequestContext + page context。
- 处理：intent parse → resource resolve → candidate capabilities → DAG plan → dependency scheduling。
- 输出：OpsIntent、InvestigationPlan、结构化 AIOpsState。

**API Input / Output**

- intent/plan 通过 SSE 明确事件输出；前端展示步骤和依赖。

**Error Semantics**

- 目标歧义返回 `ambiguous_resource` 并要求用户选择；缺关键证据生成 missing_evidence，不强行确定根因。

**Multi-cluster Requirements**

- 单调查单 cluster；跨集群请求拆 child plans。

**Security / RBAC Requirements**

- Planner 只能选择 registry 中且当前 context 可用的 Tool；不能通过 prompt 生成新执行能力。

**Tests**

- health/query/diagnose/RCA/remediation/verify intent。
- OOM、error rate、P99 的默认数据源。
- DAG 并行、依赖、required step、补充调查和取消。

**Acceptance Criteria / Gate 6**

- ToolDefinition、ToolResult、Evidence、Intent、Planner、AIOpsState contract tests 全通过。

**禁止事项**

- 继续扩散临时字符串 state 字段。

---

# Phase 7：Domain Agents 与 Resource Graph

## Task 7.1 七类 Domain Agents

**Preconditions / 输入**

- Gate 6 通过。

**Existing Code Inspection**

- `agents.py`、`agent_tool.py`、`dual_agent.py`、`skills/*.py`、personas。
- query-api investigation endpoints。

**Modification Scope / ADD / MODIFY / DELETE**

- ADD：`investigation_agents/` 下 Observability、Log、Trace、Kubernetes、Change、Knowledge、Infrastructure 七个模块。
- MODIFY：现有 skills/personas 可作为内部策略和提示词资源，不作为第二编排器。
- DELETE：新 Agent 覆盖后删除重复 Tool-as-Agent、重复 CrewAI 路由和未使用 persona。

**Data Input / Processing / Output**

- 所有 Agent 输入：PlanStep + RequestContext + 已有 Evidence。
- 所有 Agent 输出：ToolResult 列表、Evidence 列表、missing evidence；不得直接输出最终 Root Cause。
- Observability：异常窗口、RED/USE/SLI/SLO。
- Log：normalize/template/hash、baseline/current、growth、first seen、trace/pod/service correlation。
- Trace：error/slow trace、critical span、normal vs abnormal diff、downstream anomaly。
- Kubernetes：Pod、restart/lastState/OOM、Deployment、Events、requests/limits、Node Condition、Probe、Scheduling、PVC；K8sGPT 只是 tool。
- Change：Deployment revision、image、ConfigMap、K8s Event、平台变更/Workflow execution。
- Knowledge：Runbook/SOP/Incident/RCA/Architecture/Product docs，保留 source/document/version/similarity/applicability。
- Infrastructure：仅分析现有 IPMI/节点/VM 数据；缺 BMC/SMART/NIC CRC 时输出 unknown。

**API Input / Output**

- Agent 仅使用 investigation API 和 registry，不直连前端。

**Error Semantics**

- 每个 Agent 保留 partial/no_data/unavailable/permission_denied，不能汇总成 success。

**Multi-cluster Requirements**

- KG、Flow nodes、RAG metadata 当前存在 `default`/无 scope 断点，必须全部接入 RequestContext。

**Security / RBAC Requirements**

- 七类 Agent 默认只读；写操作只能输出 OpsAction 草案。

**Tests**

- 各 Agent contract/scenario tests；Log pattern baseline；Trace diff；K8s denied/no_data；Knowledge empty/unavailable；Infrastructure unknown。

**Acceptance Criteria / Gate 7A**

- 七 Agent 都可独立测试、并行执行、输出可审计 Evidence。

**禁止事项**

- 第一阶段新增大型 ML 日志平台或 Neo4j。

## Task 7.2 Resource Graph 与 First Bad Event 时间轴输入

**Preconditions / 输入**

- Gate 7A 通过。

**Existing Code Inspection**

- `kg_graph.py`、`kg_tools.py`、`kg_api.py`。
- query-api topology store/handlers、ingest topology、change_events、k8s_events。

**Modification Scope / ADD / MODIFY / DELETE**

- ADD/MODIFY：`resource_graph.py` 统一 K8s、Trace、DeepFlow、service metadata 关系。
- MODIFY：KG 定时重建不再硬编码 `default`；cluster 为正式列/上下文而非仅 props JSON。
- MODIFY：接口固定 `get_upstream/get_downstream/get_runtime_resources/get_owner/get_dependencies`。
- DELETE：新图谱覆盖后删除仅服务独立 KG 页面而无 Planner/RCA 消费的 adapter。

**Data Input / Processing / Output**

- 节点：Cluster、Node、Deployment、Pod、Service；关系：runs_on、managed_by、belongs_to、calls、depends_on。
- 输出供 Planner、RCA 和前端嵌入；不是独立主入口。

**API Input / Output**

- Resource Graph API 返回 canonical resource IDs 和 Evidence provenance。

**Error Semantics**

- 图关系不完整返回 partial + missing relation source；不得凭 LLM 补边。

**Multi-cluster Requirements**

- 跨 cluster relation 默认禁止；确有多集群调用时必须携带两个完整 resource ID 和来源证据。

**Security / RBAC Requirements**

- 图遍历每一步执行 scope filter，避免通过边泄露未授权资源。

**Tests**

- upstream/downstream/runtime/owner/dependencies；双 cluster 同名节点；图传播；无证据边拒绝。

**Acceptance Criteria / Gate 7**

- Planner 能从 orders 自动解析 Pods、Nodes、上下游；专业页面可按权限嵌入图。

**禁止事项**

- 把现有 Knowledge Graph 改名但不解决 identity、scope 和 provenance。

---

# Phase 8：Hypothesis RCA、补充调查与排序

## Task 8.1 Hypothesis Engine

**Preconditions / 输入**

- Gate 7 通过；Agents 和 Resource Graph 可提供 Evidence。

**Existing Code Inspection**

- `rca.py`、`investigator.py`、`detector.py`、`orchestrator.py` node_rca。
- 保留的确定性拓扑、Granger、变更关联和 kubectl 假设证伪逻辑。

**Modification Scope / ADD / MODIFY / DELETE**

- ADD：`hypothesis_rca.py`。
- MODIFY：把现有确定性 RCA 能力改成 Hypothesis generation/support/contradiction provider。
- MODIFY：Planner 根据 missing_evidence 添加二次调查步骤并重新评分。
- DELETE：Gate 通过后删除旧 Prompt-only 和直接 root cause 生产入口。

**Data Input / Processing / Output**

```text
Evidence + Timeline + Resource Graph
→ Generate hypotheses
→ Support / Contradict / Missing
→ Additional Investigation
→ Score / Rank
→ Root Cause candidates + Unknowns
```

**API Input / Output**

- Hypothesis SSE 事件包含 evidence IDs、missing IDs/queries、confidence components 和 status。
- Root Cause 输出必须含 supporting_evidence、contradictions、unknowns。

**Error Semantics**

- 关键证据缺失时 root cause status=unknown，禁止转成 confirmed。

**Multi-cluster Requirements**

- hypothesis 不得引用其他 cluster Evidence；若跨集群 parent run 聚合，只能汇总 child root causes。

**Security / RBAC Requirements**

- LLM 训练知识只能标记 Knowledge/Inference，不能成为现场 Fact。

**Tests**

- 支持、反证、missing、二次调查、循环上限、unknown、Evidence 删除/越权引用拒绝。

**Acceptance Criteria / Gate 8A**

- 任一 Root Cause 可从 ID 回溯所有事实、反证和未知项。

**禁止事项**

- LLM 一次调用直接返回最终根因作为核心实现。

## Task 8.2 Confidence / Ranking / First Bad Event

**Preconditions / 输入**

- Gate 8A 通过。

**Existing Code Inspection**

- `rca.py` 现有 confidence、变更关联、时间逻辑。
- Alerts、change_events、k8s_events、日志、Trace、Metrics 时间字段。

**Modification Scope / ADD / MODIFY / DELETE**

- MODIFY：confidence 由规则与 LLM 联合产生；LLM 权重不得超过 50%。
- ADD：统一时间轴和 First Bad Event detector。
- DELETE：删除仅由 LLM 自评 confidence 的路径。

**Data Input / Processing / Output**

固定权重：LLM reasoning 35%、Evidence support 30%、source reliability 20%、temporal relation 15%，contradiction penalty 单独扣减。

时间轴合并 Change、K8s Event、Log Pattern、Trace anomaly、Metric anomaly、Alert；输出第一异常事件及其 Evidence ID。Alert 不得默认当根因。

**API Input / Output**

- 输出 confidence breakdown 和排序原因；低于 0.60 不得进入自动修复。

**Error Semantics**

- 时间戳不一致或 clock skew 超阈值时标记 timeline partial/unknown。

**Multi-cluster Requirements**

- 时间轴按 tenant+cluster 过滤，统一 UTC 存储并带原始时区 metadata。

**Security / RBAC Requirements**

- 未授权 Evidence 不参与评分。

**Tests**

- 变更先于 Pod restart/日志/指标/告警；反证降权；低置信阻断；clock skew。

**Acceptance Criteria / Gate 8**

- 固定 RCA 场景在单测层完整验证 Intent→Plan→Tool→Evidence→Hypothesis→Missing→Additional→Root Cause→Confidence→Unknowns。

**禁止事项**

- 只测试最终文字。

---

# Phase 9：Run Persistence、SSE 与恢复

## Task 9.1 ControlPlaneRun 与唯一状态机

**Preconditions / 输入**

- Gate 8 通过；AI run schema 已存在。

**Existing Code Inspection**

- `main.py` `_TaskStore`、task endpoints、session endpoints。
- `orchestrator.py` checkpoint、interrupt/resume。
- `flow_engine/store.py`、`usecase.py`、`engine.py`。

**Modification Scope / ADD / MODIFY / DELETE**

- ADD：`investigation_store.py`、`investigation_api.py`。
- MODIFY：Task、LangGraph 和 Investigation 使用一个持久状态机；checkpoint 只用于 runtime resume，不作为业务历史真源。
- MODIFY：Flow resume 当前从入口重跑整图；保留的业务 Flow 必须从安全 checkpoint 恢复或保证幂等，不重复写动作。
- DELETE：新 run API 切换后删除内存 `_TaskStore` 主存、旧 SessionStore/旧 checkpoint compatibility。

**Data Input / Processing / Output**

固定 run 状态：

```text
created planning investigating awaiting_approval executing verifying
succeeded partial failed regressed canceled
```

- 状态转换必须事务化、可审计、幂等。

**API Input / Output**

```text
POST /api/v1/investigations
GET  /api/v1/investigations
GET  /api/v1/investigations/{run_id}
POST /api/v1/investigations/{run_id}/cancel
GET  /api/v1/investigations/{run_id}/events
```

**Error Semantics**

- 重复 request_id 返回原 run；非法状态转换=409；持久化失败=503；已取消 run 不可继续执行。

**Multi-cluster Requirements**

- run context immutable；更换 cluster 创建新 run。

**Security / RBAC Requirements**

- run 查询/取消按 tenant、owner、role、cluster scope 授权。

**Tests**

- 状态机、重复请求、进程重启恢复、取消、并发更新、approval wait、Flow 不重复执行。

**Acceptance Criteria / Gate 9A**

- 重启后可恢复未完成 run；历史查询不依赖旧 checkpoint。

**禁止事项**

- 为旧 AI 历史编写转换器。

## Task 9.2 SSE v2

**Preconditions / 输入**

- Gate 9A 通过。

**Existing Code Inspection**

- `main.py` Chat SSE generator。
- `orchestrator.py` stream_sync events。
- frontend `AiChat.tsx` event parser、nginx proxy timeout/buffering。

**Modification Scope / ADD / MODIFY / DELETE**

- MODIFY：实现固定事件协议、monotonic sequence、heartbeat、structured error、done。
- ADD：断线后 `Last-Event-ID`/sequence 恢复与 run event replay。
- MODIFY：query-api/nginx 禁用 SSE buffering，设置长任务超时和 flush。
- DELETE：前端切换后删除旧 progress/chunk/suggestion 自由形事件解析。

**Data Input / Processing / Output**

- 输入：persisted run events。
- 处理：序列化、flush、10–15 秒 heartbeat、断线不取消后台 run（用户明确 cancel 除外）。
- 输出：固定 SSE envelope。

**API Input / Output**

- `GET /investigations/{run_id}/events` 支持 `Last-Event-ID`。

**Error Semantics**

- connection_lost 仅表示客户端连接；run 实际状态需重新读取。
- llm_timeout/tool_timeout/backend_error/permission_denied/user_abort 不得统一为 network error。

**Multi-cluster Requirements**

- 每个事件携带 run 固定 cluster ID。

**Security / RBAC Requirements**

- event replay 重新鉴权；不得通过猜 run_id 读取他人 Evidence。

**Tests**

- 30 分钟长流、heartbeat、proxy flush、断线重连、duplicate sequence、取消、后端异常。

**Acceptance Criteria / Gate 9**

- 长调查无静默断流；前端断线可恢复；错误分类准确。

**禁止事项**

- 用增大单一 timeout 掩盖无 heartbeat/无恢复问题。

---

# Phase 10：Structured Remediation、Risk、Approval、Execution、Verification

## Task 10.1 OpsAction / Risk Engine / Approval

**Preconditions / 输入**

- Gate 9 通过；Root Cause 与 run 可持久化。

**Existing Code Inspection**

- `execution_gate.py`、`k8s_actions.py`、`shell_policy.py`、`db_approval.py`。
- `main.py` suggestion execute、task approve/reject、K8s endpoints。
- Flow `_execute/_verify` 与 LangGraph execute/verify。

**Modification Scope / ADD / MODIFY / DELETE**

- ADD：`remediation.py`、`risk_engine.py`。
- KEEP：K8s `preflight → HMAC 参数绑定 → resourceVersion → execute → audit`。
- MODIFY：所有写动作先生成 OpsAction；Risk 同时考虑 action、RCA confidence、blast radius、environment。
- MODIFY：ApprovalDecision 绑定 run_id、action digest、target、cluster、resourceVersion、expiry、approver。
- DELETE：新入口生效后删除客户端 `approved: true`、Chat suggestion 直接执行、Flow/LangGraph 重复执行器。

**Data Input / Processing / Output**

风险固定：R0 read；R1 diagnostic；R2 restart/scale-up；R3 config/resource/deployment modification；R4 destructive/storage/network/delete。

行为：R0 自动；R1 自动+审计；R2 用户确认；R3 审批；R4 严格审批或禁止。

**API Input / Output**

```text
POST /investigations/{run_id}/actions
POST /investigations/{run_id}/actions/{action_id}/approve
POST /investigations/{run_id}/actions/{action_id}/reject
POST /investigations/{run_id}/actions/{action_id}/execute
```

**Error Semantics**

- 低 confidence、过期审批、digest 不同、resourceVersion 变化、scope 变化均拒绝执行并返回稳定错误码。

**Multi-cluster Requirements**

- 写 Action 必须单 cluster、精确资源；禁止 `all`。

**Security / RBAC Requirements**

- LLM 输出不得直接成为 shell；adapter 只接受结构化参数。
- ShellPolicy、命令白名单、敏感路径和审计继续生效。

**Tests**

- R0–R4、blast radius 上调、低 confidence、approval binding/expiry/replay、普通用户越权、resourceVersion 冲突。

**Acceptance Criteria / Gate 10A**

- 任意写动作无法绕过 risk 和 approval；批准内容不可被替换。

**禁止事项**

- 只按命令字符串判风险。

## Task 10.2 Execution Adapter 与强制 Verification

**Preconditions / 输入**

- Gate 10A 通过。

**Existing Code Inspection**

- `k8s_actions.py` execute、`orchestrator.py` before/after metrics、Flow `_verify`。

**Modification Scope / ADD / MODIFY / DELETE**

- ADD：`verification.py`。
- MODIFY：K8s API/kubectl/shell/HTTP adapter 统一执行结果。
- MODIFY：所有写动作强制 Before Snapshot → Execute → Observation Window → After Snapshot → Compare → Verdict。
- DELETE：Flow `_verify` 恒 pass；命令 exit 0 直接标成功；重复 verify 路径。

**Data Input / Processing / Output**

- 输入：approved OpsAction、before checks、expected effect、rollback。
- 输出：execution result + VerificationResult。
- 检查 Error Rate、P99、Availability、Ready Replica、Restart Rate、SLO 等与动作目标相关的真实效果。

**API Input / Output**

- execution_end 只说明命令执行；verification 单独输出业务 verdict。

**Error Semantics**

- `regressed` 停止后续自动操作、提示风险、建议 rollback；不得说修复成功。

**Multi-cluster Requirements**

- before/after 使用与 action 完全相同的 context 和资源。

**Security / RBAC Requirements**

- rollback 也是新 OpsAction，重新风险和审批；不得自动执行 R3/R4 rollback。

**Tests**

- exit 0 但业务未恢复、partial、failed、regressed、unknown、观测窗口超时、rollback suggestion。

**Acceptance Criteria / Gate 10**

- 写动作没有 Verification 不可进入 succeeded；regressed 能阻断。

**禁止事项**

- 把 kubectl/shell exit code 当业务恢复。

---

# Phase 11：前端产品收敛与 Evidence 双向跳转

## Task 11.1 信息架构与智能调查页

**Preconditions / 输入**

- Gate 10 通过；run/SSE/action API 稳定。

**Existing Code Inspection**

- `src/App.tsx` 全路由和 NAV_GROUPS。
- `pages/ai/AiChat.tsx`、`AiTools.tsx`、Workflows、KnowledgeGraph。
- `components/AiDock.tsx`、`api/client.ts`、`store/uiStore.ts`。

**Modification Scope / ADD / MODIFY / DELETE**

- ADD：`pages/ai/Investigation/index.tsx`、`api/investigations.ts`、五个 investigation components。
- MODIFY：`App.tsx` 采用固定 IA：总览；智能运维（智能调查、告警与事件、审批任务）；可观测（服务、链路、日志与指标）；资源（K8s、主机与虚机、容量与硬件）；治理（知识与 Runbook、变更与审计、SLO）；系统管理（用户、设置）。
- MODIFY：AiDock 和专业页面传 ResourceRef、cluster、namespace、time range。
- DELETE：切换后删除 `/ai/chat` 旧页面、普通用户 `/ai/tools`、`/kg`、独立 `/ai/workflows` 主入口及独占代码。
- KEEP：Flow Engine 管理能力迁入系统设置/高级管理；Knowledge、Graph、Tools 以能力面嵌入。

**Data Input / Processing / Output**

- 输入：run snapshot + SSE events。
- 展示：目标、范围、计划、Tool 状态、Evidence、Hypothesis、Root Cause、Unknown、Remediation、Risk、Approval、Execution、Verification。
- Markdown 只作为详细分析，不承担状态。

**API Input / Output**

- 前端使用具名 `investigations.ts`；禁止页面内散落直接 `api.get/post`。

**Error Semantics**

- 明确展示 no_data/unavailable/permission_denied/timeout/partial；断线后从 run 恢复。

**Multi-cluster Requirements**

- URL 和 run 显示 canonical cluster；受限用户不显示 `all`；同一 run 不随全局切换器变更。

**Security / RBAC Requirements**

- ADD `RequireRole` 只改善 UX；服务端仍是决定性边界。
- 客户端不能发送 `approved: true` 作为授权。

**Tests**

- 组件状态机、SSE sequence、断线恢复、角色显示、路由、scope；Playwright 关键链路。

**Acceptance Criteria / Gate 11A**

- 用户主要体验是运维任务而非技术能力；调查可刷新/分享受控 URL 并恢复。
- 最终导航、角色体验、调查工作台结构和状态语义逐项符合第 2 章；任一偏离都阻断 Gate 11A。

**禁止事项**

- 只隐藏菜单但保留无业务价值的页面、route、API wrapper 和依赖。

## Task 11.2 专业页面与 Log Pattern

**Preconditions / 输入**

- Gate 11A 通过。

**Existing Code Inspection**

- ServiceObservability、Trace、LogMetrics、K8sActions、AlertEvents、Changes、Capacity、Hardware。

**Modification Scope / ADD / MODIFY / DELETE**

- MODIFY：LogMetrics 增加“原始日志 / 异常模式”，列出 Pattern、Baseline、Current、Growth、First Seen，并跳原始日志/Trace/Pod/AI。
- MODIFY：Service/Trace/K8s/Alert/Resource 增加“交给 AI 调查”。
- MODIFY：Evidence 支持 Log→日志、Trace→Trace、Pod→K8s、Service→服务、Change→变更。
- MODIFY：图谱嵌入服务详情、RCA、资源详情三种模式。
- DELETE：仅支持旧入口的 formatter/hook/state。

**Data Input / Processing / Output**

- 页面上下文必须含 resource_type/name/id、cluster、namespace、time range。

**API Input / Output**

- Evidence deep link 使用 canonical resource/query parameters，不传 raw SQL。

**Error Semantics**

- 页面 catch 不得把 API error 静默显示成空列表。

**Multi-cluster Requirements**

- deep link 复原原 Evidence cluster，不用当前全局 cluster 覆盖。

**Security / RBAC Requirements**

- 跳转后仍重新鉴权 Evidence 与专业数据。

**Tests**

- Pattern baseline/current；deep link；scope；empty/error/permission；窄屏 Drawer/Modal。

**Acceptance Criteria / Gate 11**

- AI 与专业页面双向可追踪；日志异常模式自动运行；主导航收敛。
- 第 2.5 节定义的每个核心页面都能回答其固定问题，且两条主旅程没有要求普通用户选择 Tool、Agent、Workflow、K8sGPT 或 RAG。

**禁止事项**

- 用前端 mock 生成 Pattern 或 Evidence。

---

# Phase 12：多集群 RBAC 与安全加固

## Task 12.1 权限矩阵和逐请求裁决

**Preconditions / 输入**

- Gate 11 通过。

**Existing Code Inspection**

- query-api auth、ProxyAI、高风险路径、users/scope。
- frontend RequireAuth/authStore/admin pages。
- orchestrator internal middleware、execution/approval endpoints。

**Modification Scope / ADD / MODIFY / DELETE**

- ADD：角色/能力/tenant/cluster/namespace/resource/action 权限矩阵文档和 tests。
- MODIFY：JWT 或数据库会话绑定 tenant；allowed clusters 服务端派生。
- MODIFY：admin、approver、user 的读/写/审批/执行边界。
- DELETE：匿名 LLM settings、客户端 tenant 信任、localStorage role 授权语义。

**Data Input / Processing / Output**

- 输入：authenticated principal + resource/action。
- 处理：tenant → cluster → namespace/resource → capability → risk/action 裁决。
- 输出：allow/deny + audit reason。

**API Input / Output**

- 403 返回稳定 code，不泄露未授权资源是否存在。

**Error Semantics**

- unauthenticated=401；authenticated but denied=403；不得用 200+empty 隐藏越权。

**Multi-cluster Requirements**

- 同用户在 cluster-A 可读，不代表 cluster-B 可读/执行。

**Security / RBAC Requirements**

- query-api 是外部信任边界；orchestrator 只信签名内部 context。
- NetworkPolicy 限制 orchestrator、MySQL、ClickHouse、K8s API 的调用来源。

**Tests**

- 普通用户篡改 role/tenant/cluster；审批人跨 cluster；admin；内部 header 伪造；run/evidence ID 猜测。

**Acceptance Criteria / Gate 12**

- 前端、query-api、orchestrator 的权限行为一致；服务端可独立阻止所有越权。

**禁止事项**

- 把前端按钮隐藏算作 RBAC 完成。

---

# Phase 13：删除旧代码、旧 API、旧页面与双主路径

## Task 13.1 调用图驱动删除

**Preconditions / 输入**

- Gate 12 通过；新生产链、专业页面和安全链均运行。

**Existing Code Inspection**

- 搜索：`legacy|compat|fallback|deprecated|old|migration|checkpoint|sidecar|run-legacy|/ai/chat|/ops/rca`。
- `rg` 前端 import/route/API call、Go route/handler、Python route/call graph、Helm refs、测试。

**Modification Scope / ADD / MODIFY / DELETE**

- DELETE：调用图为零的旧 AI Session、checkpoint compatibility、旧 RCA、旧 Tool routing、死 route/handler/page/API wrapper/state/CSS/test。
- DELETE：query-api `/snmp` 死代理。
- DELETE：前端 `src/hooks/useAsyncData.ts`（再次确认无调用后）。
- MODIFY：Flow Engine 只保留非调查业务自动化；删除其重复调查 collect/RCA/execute/恒 pass verify。
- ADD：`docs/AIOPS_CODE_AND_DEPENDENCY_CLEANUP.md` 逐项记录删除。

**Data Input / Processing / Output**

- 输入：新旧调用图、route map、bundle、test coverage。
- 输出：deleted files/LOC/routes/handlers/compatibility paths/tests 和理由。

**API Input / Output**

- 删除 endpoint 前前端、Agent、Webhook、文档和外部调用均为零；否则先提供同 Phase adapter 并在 Gate 前移除 adapter。

**Error Semantics**

- 旧 endpoint 切换完成后返回 404/410，不静默代理到不等价新 API。

**Multi-cluster Requirements**

- 删除所有 `default/id=1/all` 兼容不得破坏 canonical scope tests。

**Security / RBAC Requirements**

- 保留 WAL、当前恢复、ShellPolicy、K8s preflight、审计、Secret 复用等仍有价值代码。

**Tests**

- 全仓 compile/test/build；route snapshot；无引用检查；`git diff --check`。

**Acceptance Criteria / Gate 13**

- 无新旧双主路径；无明显死代码/API/页面；删除报告可量化。

**禁止事项**

- 仅凭文件名包含 fallback/old 删除。
- 删除现有用户未跟踪文件或备份。

---

# Phase 14：依赖、Docker Context 与镜像精简

## Task 14.1 依赖审计与锁定

**Preconditions / 输入**

- Gate 13 通过；死代码和页面已删除。

**Existing Code Inspection**

- `ai-orchestrator/requirements.txt`、直接 import、依赖树、容器启动、测试。
- 三个 Go `go.mod`/vendor。
- frontend `package.json`、lockfile、源码 import 和构建产物。

**Modification Scope / ADD / MODIFY / DELETE**

- MODIFY：Python runtime/dev/test 分离并锁定可重复版本；保留实际运行需要的间接依赖。
- MODIFY：Go 执行 `go mod tidy`，query-api 重新生成/验证 vendor。
- MODIFY：Frontend 删除确认无引用依赖并更新 lockfile。
- DELETE 候选：前端静态扫描未见引用的 `@antv/g6`、`@dagrejs/dagre`、`@xterm/*`、`dayjs`、`echarts-for-react`、`html2canvas`、`react-grid-layout` 及 types；必须先验证 runtime/plugin/新设计用途。
- KEEP：源码实际使用的 `echarts`、`@xyflow/react` 等。

**Data Input / Processing / Output**

- 输入：direct deps、import graph、dependency tree、镜像运行测试。
- 输出：每服务 Before/After direct dependencies、Added、Removed、Reason。

**API Input / Output**

- 无 API 变更；依赖删除不得改变合同。

**Error Semantics**

- `pip check`、npm build 或 Go test 失败即回滚该依赖删除；不得增加 fallback 包掩盖。

**Multi-cluster Requirements**

- 不得删除 kubeconfig/K8s/OTLP/DeepFlow 多集群运行必需依赖。

**Security / RBAC Requirements**

- 新依赖必须维护活跃、锁定版本、可离线缓存、许可证可接受、无已知高危漏洞或有明确缓解。

**Tests**

- Python full tests/compileall/pip check；Go tidy/test/vet；frontend install/build/tests；离线安装。

**Acceptance Criteria / Gate 14A**

- 无明显无用直接依赖；生产 runtime 不含 pytest/coverage/notebook/开发工具，除非有运行时证据。

**禁止事项**

- 只 grep import 就删除间接依赖。

## Task 14.2 Docker Context 与镜像优化

**Preconditions / 输入**

- Gate 14A 通过；Before 镜像大小已记录。

**Existing Code Inspection**

- 所有 Dockerfile、`.dockerignore`、`build-images.sh`、离线缓存、运行时依赖。
- 特别检查 orchestrator `bin/*.tar.gz`、HF/Chroma 模型、kubectl/k8sgpt；frontend 当前无 `.dockerignore`。

**Modification Scope / ADD / MODIFY / DELETE**

- ADD：各服务精确 `.dockerignore`；排除 `.git`、venv、node_modules、tests、coverage、docs、backup、archive、tmp、旧 dist、本地 cache。
- MODIFY：Go build 使用 `-trimpath -ldflags '-s -w'`，runtime 不含 compiler/source/tests/module cache。
- MODIFY：frontend 只允许 `npm ci`，runtime 只含 dist+nginx；替换固定旧镜像的 `Dockerfile.offline` 为可重复 artifact/digest 流程。
- MODIFY：orchestrator 使用 multi-stage/分层依赖和最小 runtime；离线资产逐项证明需要，避免 `COPY . .` 携带源码测试和重复 tarball。
- DELETE：重复模型缓存、开发工具、无用系统包；CA/timezone/health/shell 等必要运行依赖有证据才保留或删除。

**Data Input / Processing / Output**

- 输入：每镜像 compressed/virtual size、layer history、SBOM、启动和功能测试。
- 输出：`docs/AIOPS_IMAGE_SIZE_REPORT.md`，逐服务 Before/After/Reduction/Removed Files/Deps/Base Image Changes，及 Total Before/After/Overall。

**API Input / Output**

- 镜像优化不得改变服务端口、health、API/SSE 行为。

**Error Semantics**

- 离线资产缺失、架构不匹配、模型缓存未命中必须 build/start fail-fast。

**Multi-cluster Requirements**

- kubectl/k8sgpt 目标架构和多集群凭据加载必须在镜像验收。

**Security / RBAC Requirements**

- 所有自研容器使用 non-root 用户；frontend、query-api、ingest 和 event-collector 使用只读 rootfs。orchestrator 只允许 `AIOPS_DATA_DIR`、模型缓存和临时目录对应的显式 volume 可写。任何无法满足项必须在镜像报告中给出复现命令和阻塞原因，且不得把 Secret/kubeconfig 写入镜像层；所有镜像生成 SBOM 和漏洞报告。

**Tests**

- clean docker build、offline build、container start/health、关键 tool、RAG 模型命中、read-only runtime、镜像扫描。

**Acceptance Criteria / Gate 14**

- 所有自研运行镜像总大小至少降低 20%，且不高于基线；单镜像增长有必要性证据。
- 功能、安全、稳定性和离线构建未受损。

**禁止事项**

- 为减镜像删除 CA、timezone、必要系统库、健康检查或当前可靠性工具。

---

# Phase 15：全量自动化测试与固定场景

## Task 15.1 全仓测试门禁

**Preconditions / 输入**

- Gate 14 通过；代码、依赖和镜像均完成。

**Existing Code Inspection**

- 全部测试、CI/本地脚本、Helm、build scripts。

**Modification Scope / ADD / MODIFY / DELETE**

- ADD：缺失的 event-collector tests、frontend component/API/E2E tests、跨服务 integration tests。
- MODIFY：`deploy/scripts/test-default-deploy.sh` 覆盖新 schema/run/SSE。
- DELETE：只验证旧 endpoint/旧 schema 的测试；必须以新合同替代，不得只删。

**Data Input / Processing / Output**

- 输入：空数据库、fixture、真实依赖容器、测试 K8s。
- 输出：`docs/AIOPS_AGENTIC_TEST_REPORT.md`，含命令、版本、通过/失败/跳过和证据。

**API Input / Output**

- REST、SSE、内部 proxy、执行/审批/验证全部有 contract tests。

**Error Semantics**

- 跳过必须有原因和 owner；P0/P1 场景不得跳过。

**Multi-cluster Requirements**

- 同名资源双集群、受限用户、跨集群 parent run、错误 cluster 必测。

**Security / RBAC Requirements**

- 认证、越权、header 伪造、审批 replay、action tamper、Secret 泄露扫描。

**Tests**

```text
Python full test + compileall + pip check
Go all test + vet；可用环境执行 race
Frontend typecheck + unit/component + production build
Helm lint + template（仅使用临时非真实 Secret）
Docker clean build + start
git diff --check
secret/binary/backup/runtime-data scan
```

**Acceptance Criteria / Gate 15A**

- 所有自动化门禁通过；无 P0/P1 跳过。

**禁止事项**

- 用 mock 通过最终真实验收；mock 仅用于单元测试。

## Task 15.2 十二个 RCA Scenario

**Preconditions / 输入**

- Gate 15A 通过。

**Existing Code Inspection**

- loadgen、测试 playbooks、K8s manifests、现有故障场景资产。

**Modification Scope / ADD / MODIFY / DELETE**

- ADD：可重复 scenario harness 和结果断言。
- MODIFY：无生产功能绕过。
- DELETE：无。

**Data Input / Processing / Output**

固定场景：

```text
1 OOMKilled
2 CrashLoopBackOff
3 Error Rate Increase
4 P99 Increase
5 Redis Timeout
6 Deployment Unavailable
7 Node Pressure
8 Change-caused Failure
9 Similar Knowledge Case
10 RBAC Denied
11 Tool Timeout
12 No Data
```

每个场景检查：Intent、Plan、Tool Calls、Evidence、Hypotheses、Contradictions、Missing Evidence、Additional Investigation、Root Cause、Confidence、Unknowns。

**API Input / Output**

- scenario 通过 run API 注入问题，不直接写 UI mock。

**Error Semantics**

- RBAC denied/tool timeout/no data 必须得到对应状态，不产生健康假结论。

**Multi-cluster Requirements**

- 至少四个场景在两个集群同名 service 上验证隔离。

**Security / RBAC Requirements**

- 写场景仅在隔离测试 namespace，经审批执行。

**Tests**

- scenario harness 自动断言全链 JSON/SSE，而非自然语言关键词。

**Acceptance Criteria / Gate 15**

- 12/12 场景链路通过；失败有可复现证据。

**禁止事项**

- 仅人工阅读最终报告判通过。

---

# Phase 16：精确重置历史运行数据

## Task 16.1 Clean Runtime Data

**Preconditions / 输入**

- Gate 15 通过。
- 用户已确认执行本地/验收环境的数据清理窗口。
- 已导出配置清单和可恢复备份；备份不进入 Git。

**Existing Code Inspection**

- 新旧表清单、PVC、Chroma collections、MinIO prefixes、checkpoint/Flow stores、Helm Secret。

**Modification Scope / ADD / MODIFY / DELETE**

- DELETE：仅清理明确列出的历史 Metrics/Logs/Trace/Topology/Alert/Event/AI Run/RCA/Evidence/Workflow Run/checkpoint。
- KEEP：users、RBAC、tenants、clusters/credential_ref、platform/LLM/data-source config、Secret、有效 knowledge/Runbook。
- ADD：清理前后审计记录和验证清单。

**Data Input / Processing / Output**

- 输入：精确数据库/表/PVC/object prefix。
- 处理：先停止写者/排空 WAL → 清理 → 初始化新 schema → 启动写者。
- 输出：清理对象、未清理配置、恢复点、行数/PVC 状态。

**API Input / Output**

- 清理期间写 API 进入维护态；读 API 返回 maintenance，不伪装 no_data。

**Error Semantics**

- 任一目标不明确立即停止；部分失败不得继续部署。

**Multi-cluster Requirements**

- 按 tenant+cluster 精确确认；验收环境可全清，但不得误清外部/共享集群数据。

**Security / RBAC Requirements**

- 不打印 Secret；备份加密和访问受控。

**Tests**

- 清理后配置/用户登录/LLM 配置存在性；运行历史为空；新 schema version 正确。

**Acceptance Criteria / Gate 16**

- 旧历史清理、配置资产完整、新空数据面可启动。

**禁止事项**

- 未获清理窗口授权执行 destructive 数据操作。
- 清空整个 MySQL、namespace 或工作区。

---

# Phase 17：最新源码 Clean Build 与部署

## Task 17.1 正式 patch、全镜像构建和 Helm 部署

**Preconditions / 输入**

- Gate 16 通过；工作区是拟交付源码；全量测试新鲜通过。

**Existing Code Inspection**

- `version.sh`、`build-images.sh`、`apply.sh`、Chart appVersion/global.imageTag、所有 Deployment image。

**Modification Scope / ADD / MODIFY / DELETE**

- MODIFY：递增正式 patch；同一 tag 构建所有自研镜像。
- MODIFY：Helm 部署新 schema/init Job 和最新镜像。
- DELETE：不复用旧 tag/旧 image；不使用 dirty 临时镜像作为最终交付。

**Data Input / Processing / Output**

```text
正式源码 → 全量测试 → patch → clean build all → image inspect
→ Helm deploy → rollout → health/readiness → image digest 对账
```

**API Input / Output**

- health/readiness 必须验证 schema、存储和关键内部依赖，不只进程存活。

**Error Semantics**

- init/build/rollout 任一步失败即停止；不得继续真实验收。

**Multi-cluster Requirements**

- 部署至少配置两个测试 cluster registry entry；凭据来自 Secret reference。

**Security / RBAC Requirements**

- 首次 Secret 由环境注入；升级复用既有值；日志不显示值。

**Tests**

- Pod image digest=本次 build；rollout complete；health；schema version；NetworkPolicy；服务账号权限。

**Acceptance Criteria / Gate 17**

- 所有 Pod 运行本次正式 tag/digest；无旧镜像；新存储空态正常。

**禁止事项**

- `SKIP_IMAGE_BUILD=1` 用于最终部署。

---

# Phase 18：真实新数据、多集群、LLM 与 Browser E2E

## Task 18.1 新数据链和多集群验收

**Preconditions / 输入**

- Gate 17 通过；部署后无历史运行数据。

**Existing Code Inspection**

- ingest/loadgen、DeepFlow、event collector、query-api、UI、Agent run。

**Modification Scope / ADD / MODIFY / DELETE**

- ADD：仅测试流量和故障场景资产；标记“测试产生的真实采集数据”。
- MODIFY：发现问题进入 Phase 19 修复。
- DELETE：无生产数据 mock。

**Data Input / Processing / Output**

必须验证：

```text
OTLP / DeepFlow → ingest → new Trace/Logs/Metrics/Topology
K8s Event/IPMI → event collector → Resource Event
storage → query-api → professional UI → Agent Tool → Evidence
```

**API Input / Output**

- 每层记录 request/query/evidence ID，能端到端关联。

**Error Semantics**

- 无数据必须区分流量未产生、采集不可用、权限不足、查询错误。

**Multi-cluster Requirements**

- cluster-A/cluster-B 生成同名 orders；查询、Evidence、RCA、SSE、审批不得串。

**Security / RBAC Requirements**

- 多集群测试用户验证 read/deny/action scope。

**Tests**

- 正常、错误、慢请求、日志错误、K8s 事件；WAL/restart；跨集群隔离。

**Acceptance Criteria / Gate 18A**

- 全部验收依据部署后新采集数据；端到端可追踪。

**禁止事项**

- 直接向 UI/数据库插入假成功结果。

## Task 18.2 真实 LLM 验收

**Preconditions / 输入**

- Gate 18A 通过；管理员已配置真实 LLM；不得记录 key。

**Existing Code Inspection**

- provider 状态、LLM mode、Tool/SSE/audit。

**Modification Scope / ADD / MODIFY / DELETE**

- 无代码修改；问题转 Phase 19。

**Data Input / Processing / Output**

固定问题：

```text
请检查当前 Kubernetes 集群健康状况，并给出真实证据。
为什么 orders 服务错误率上涨？
请自动定位 orders 最近出现的异常日志模式。
请定位 OOMKilled 的真实根因，不要只解释 OOMKilled。
当前问题是否与最近变更有关？
请使用 K8sGPT 参与调查，并准确说明实际执行结果。
是否存在历史相似 Runbook 或案例？
当前证据还缺什么？
给出处置方案，但不要执行。
修复后如何确认系统真正恢复？
```

**API Input / Output**

- 记录 provider/model（不含 key）、run ID、SSE events、ToolResult、Evidence、Hypothesis、report。

**Error Semantics**

- LLM 未实际调用、降级 mock、Tool unavailable 或断流必须判失败/部分，不得算真实 LLM 通过。

**Multi-cluster Requirements**

- orders 问题必须明确 cluster；歧义时要求选择。

**Security / RBAC Requirements**

- prompt/response 不含 Secret；Action 不自动执行。

**Tests**

- 10 个问题逐项 contract 验收和事实核对。

**Acceptance Criteria / Gate 18B**

- LLM 事实全部有 Evidence；状态语义准确；无现场编造；长 SSE 完整。

**禁止事项**

- 用 `LLM_MOCK=true` 或测试桩作最终验收。

## Task 18.3 Browser E2E

**Preconditions / 输入**

- Gate 18B 通过。

**Existing Code Inspection**

- 当前全部路由、Tab、Modal、Drawer、API errors、console、network。

**Modification Scope / ADD / MODIFY / DELETE**

- ADD：Playwright E2E 和证据目录；问题转 Phase 19。

**Data Input / Processing / Output**

- 覆盖：总览、智能调查、告警、服务、Trace、日志与指标、K8s、资源、治理、审批、设置。
- 交互：Tab、Modal、Drawer、Evidence jump、AI Analyze、Approval、Execution、Verification、Run history、SSE long request。

**API Input / Output**

- 保存关键 response status、run/evidence IDs 和截图，不保存 Token/Secret。

**Error Semantics**

- console error、silent catch、空态误导和 network error 均记录缺陷。

**Multi-cluster Requirements**

- 切换 cluster 后专业页面与新 run scope 一致；Evidence deep link 不被全局选择覆盖。

**Security / RBAC Requirements**

- user/admin/approver 三角色 E2E；localStorage role 篡改不能提权。

**Tests**

- 新自动化 + 真实浏览器逐页冒烟。

**Acceptance Criteria / Gate 18**

- Browser E2E 全通过；无主页面不可用、断流、错误跳转或越权。
- 第 2.10 节 Product End-State Gate 的每一项都有真实浏览器、真实角色和真实数据证据；缺少任一证据即 Gate 18 失败。

**禁止事项**

- 只看页面加载 200，不操作 Tab/Modal/Drawer/闭环。

---

# Phase 19：修复 P0/P1、最终 Clean Production Build

## Task 19.1 缺陷收口

**Preconditions / 输入**

- Phase 18 产生完整缺陷清单。

**Existing Code Inspection**

- 每项缺陷的最小复现、日志、ToolResult、Evidence、network、代码调用链。

**Modification Scope / ADD / MODIFY / DELETE**

- 每个缺陷按测试先行修复；同步删除被替代旧路径。

**Data Input / Processing / Output**

- P0：事实冲突/编造、Evidence 不可追踪、RCA 无证据、Log Agent 不运行、SSE 断流、权限误义、no_data 健康、绕审批、执行即恢复、Verify 错误、旧镜像、主页面不可用。
- P1：时间/cluster filter、Evidence 跳转、变更/Trace/Log/Pod 关联、confidence、菜单未收敛、旧主路径、明显死代码/依赖。

**API Input / Output**

- 修复必须保留合同兼容；若合同变更，三端和文档同 commit 更新。

**Error Semantics**

- 不降低错误级别来让测试通过。

**Multi-cluster Requirements**

- 每个数据正确性修复复测双集群。

**Security / RBAC Requirements**

- 安全修复必须包含负向测试。

**Tests**

- 聚焦→相邻→全量→真实场景→浏览器回归。

**Acceptance Criteria / Gate 19A**

- P0=0、P1=0。

**禁止事项**

- 带任何已知 P0/P1 宣称完成。

## Task 19.2 最终 clean build/deploy/smoke

**Preconditions / 输入**

- Gate 19A 通过；工作区只含正式版本改动。

**Existing Code Inspection**

- 本次拟发布 diff、全部测试报告、Phase 17/18 的构建部署脚本和最终修复后的镜像配置。

**Modification Scope / ADD / MODIFY / DELETE**

- 重新全量测试、递增正式 patch、构建所有镜像、部署、重新采集新数据、LLM 冒烟、浏览器冒烟。

**Data Input / Processing / Output**

- 输出最终 SHA、tag、digest、Helm revision、Pod images、smoke run IDs。

**API Input / Output**

- 最小冒烟覆盖 health、run 创建、SSE、Evidence 查询和专业页面 API，输出最终 run/evidence IDs。

**Error Semantics**

- 任一 build、rollout、采集、LLM 或浏览器冒烟失败立即退回 Task 19.1，不允许标记部分完成。

**Multi-cluster Requirements**

- 最终冒烟至少验证 cluster-A 和 cluster-B 的同名资源隔离。

**Security / RBAC Requirements**

- 继续执行 Phase 17/18 的 Secret 保护、镜像来源、user/admin/approver 和 action scope 验证。

**Tests**

- 全量门禁 + 最小真实 LLM + 关键浏览器闭环。

**Acceptance Criteria / Gate 19**

- 正式 Pod 运行最终镜像；P0/P1 仍为零；无 dirty 临时镜像。

**禁止事项**

- 复用 Phase 18 的旧验证结果替代最终 build 后冒烟。

---

# Phase 20：最终文档、审计与 GitHub 同步

## Task 20.1 最终交付文档

**Preconditions / 输入**

- Gate 19 通过。

**Existing Code Inspection**

- 本规格书、Phase 0 基线、全部 commit、测试/镜像/部署/真实验收证据和偏差记录。

**Modification Scope / ADD / MODIFY / DELETE**

最终至少输出并更新：

```text
docs/AIOPS_AGENTIC_ARCHITECTURE.md
docs/AIOPS_AGENTIC_IMPLEMENTATION_REPORT.md
docs/AIOPS_AGENTIC_TEST_REPORT.md
docs/AIOPS_DATA_MODEL_REDESIGN.md
docs/AIOPS_CODE_AND_DEPENDENCY_CLEANUP.md
docs/AIOPS_IMAGE_SIZE_REPORT.md
docs/AIOPS_FINAL_ACCEPTANCE_REPORT.md
```

**Data Input / Processing / Output**

- 实施报告记录 commit/phase/偏差。
- Data Model 记录不迁移数据、旧表、新表、identity/timestamp/tenant/cluster。
- Cleanup 量化 deleted files/LOC/routes/handlers/compat paths/deps。
- Image 报告逐服务和总计。
- Acceptance 记录真实数据、12 RCA、多集群、LLM、Browser、P0/P1、最终 SHA/digest。

**API Input / Output**

- 文档记录最终 API/SSE 清单、废弃 endpoint 和调用方迁移结果；每项验收链接到 run/evidence/test 证据。

**Error Semantics**

- 未通过或未执行项必须标记 failed/blocked/not-run 和原因，不得写成 success。

**Multi-cluster Requirements**

- 架构、数据模型、测试和验收报告都必须单列 tenant/cluster 隔离证据。

**Security / RBAC Requirements**

- 文档不得含 Secret、真实 kubeconfig、Token、数据库 dump、日志 dump 中的敏感内容。

**Tests**

- 链接/路径、占位符、前后矛盾、报告证据、自研镜像总量计算检查。

**Acceptance Criteria / Gate 20A**

- 七份文档完整、可复核、无“已优化”式空结论。

## Task 20.2 GitHub 同步

**Preconditions / 输入**

- Gate 20A 通过；用户已授权 commit/push/PR 或同步方式。

**Existing Code Inspection**

- `git status`、diff、tracked/untracked、`.gitignore`、secret scan、binary/backup/runtime data。

**Modification Scope / ADD / MODIFY / DELETE**

- 提交正式源码、测试和文档。
- 不提交 `.cursor/`、`.workbuddy/`、backup、bundle、binary、venv、node_modules、dist、runtime data、dump、logs、Secret。
- 未跟踪用户文件保持不动，除非用户另行授权清理。

**Data Input / Processing / Output**

- 输出 final local SHA、remote branch SHA、PR/commit 链接或同步证据。

**API Input / Output**

- 同步完成后读取 GitHub remote branch/PR 的 SHA 和文件清单，与本地提交对账。

**Error Semantics**

- push/PR 失败必须报告实际 remote 状态，不得把本地 commit 视为已同步。

**Multi-cluster Requirements**

- 提交中必须包含多集群 contract、测试和文档；不得包含任何集群凭据或本地 kubeconfig。

**Security / RBAC Requirements**

- push 前 secret/binary/backup scan 必须通过；发现疑似 Secret 立即停止。

**Tests**

- 最终 `git diff --check`；提交内容复读；remote SHA readback。

**Acceptance Criteria / Gate 20**

- GitHub 与最终本地正式 SHA 一致；无敏感/备份/二进制/运行数据进入提交。

**禁止事项**

- 未获外部写授权自动 push。
- 为工作区干净而删除用户未跟踪文件。

---

# 6. 阶段提交与审查规则

- 每个 Task 使用独立、可回滚 commit；commit 不混入其他 Task 或用户原有改动。
- 每个 Phase Gate 前执行一次需求覆盖自审、代码自审和新鲜验证。
- 数据模型、API contract、前端 type 和测试 fixture 必须在同一 Phase 同步更新。
- 新模块进入生产调用的同一 Phase 内删除被替代主路径；不得推迟到“以后清理”。
- 任何安全、数据清理、部署、真实 LLM、浏览器或 GitHub 外部写操作，只在对应 Preconditions 和授权满足后执行。
- 如果运行环境缺少 Docker/K8s/LLM/浏览器/外网，记录为明确未完成 Gate，不得用静态检查代替真实验收。

默认 commit 序列固定为；若现有仓库事实要求合并或拆分，必须在实施报告中逐项记录偏差：

```text
docs(baseline)
docs(architecture/contracts)
feat(context-resource)
feat(clean-schema)
feat(ingest-events)
feat(investigation-api)
feat(ai-contracts-planner)
feat(agents-resource-graph)
feat(hypothesis-rca)
feat(run-sse)
feat(action-risk-verify)
feat(frontend-investigation)
security(multicluster-rbac)
refactor(remove-legacy)
chore(deps-images)
test(full-scenarios-e2e)
docs(final-reports)
```

---

# 7. 最终 Definition of Done

只有以下全部满足，才能报告“本方案全部完成”：

## 数据与多集群

- [ ] 所有历史 Metrics/Logs/Trace/Topology/Alert/Event/AI 历史均不迁移，新系统不依赖旧格式。
- [ ] 用户、RBAC、tenant、Secret、LLM 配置、集群凭据引用、有效知识资产完整。
- [ ] ClickHouse、VictoriaMetrics/VictoriaLogs identity、Resource Event 和 AI Run 新 schema 生效。
- [ ] schema 单表单主写者，无 `audit_logs` 双 DDL，无 collector 自建表。
- [ ] tenant/cluster/resource/time identity 统一。
- [ ] Cluster Registry 使用 canonical ID 和 credential_ref。
- [ ] JWT/数据库派生 Trusted RequestContext，客户端不能伪造 tenant/cluster/role。
- [ ] 两集群同名资源不串数据、Evidence、RCA、审批或执行。

## Agentic 控制面

- [ ] Tool Registry 是唯一生产调用入口。
- [ ] ToolResult 状态统一，empty/unavailable/denied/timeout 语义准确。
- [ ] Evidence 是现场事实唯一来源且可回原始专业页面。
- [ ] OpsIntent 和页面/告警上下文生效。
- [ ] Planner 是唯一调查控制器，支持 DAG 并行和 missing evidence 二次调查。
- [ ] AIOpsState 结构化，无自由文本临时字段主契约。
- [ ] Observability、Log、Trace、Kubernetes、Change、Knowledge、Infrastructure Agent 生效。
- [ ] Resource Graph 供 Planner/RCA/UI 使用，无 Neo4j。
- [ ] K8sGPT 是 Kubernetes Tool；RAG 是 Knowledge Agent 能力。
- [ ] Log Agent 自动定位新 Pattern、增长、first seen、Trace/Pod/Service 关联。
- [ ] Hypothesis RCA 处理支持、反证、缺证、补充调查、排序和 First Bad Event。
- [ ] Confidence 有可见分解，LLM 权重不超过 50%，低于 0.60 不自动修复。
- [ ] Root Cause 有 supporting Evidence、contradictions、unknowns。

## Run、SSE 与处置闭环

- [ ] AI Session 主模型已替换为持久 ControlPlaneRun，不依赖旧 checkpoint 历史。
- [ ] SSE 使用固定事件、sequence、heartbeat、structured error、断线恢复。
- [ ] 长调查不再静默 network error。
- [ ] OpsAction、Risk Engine、ApprovalDecision 和 action digest 生效。
- [ ] 写动作无法绕过审批、scope、preflight、resourceVersion 和审计。
- [ ] Execution success 与 Verification success 分离。
- [ ] 所有写动作有 Before/After/Observation/Compare。
- [ ] `regressed` 阻断后续自动操作并建议 rollback。

## 产品与专业工作台

- [ ] 第 2.10 节 Product End-State Gate 全部通过，且证据可从最终报告追溯。
- [ ] 普通用户在真实浏览器中不进入技术能力页即可完成“发现问题→调查→根因→处置→审批→执行→验证”。
- [ ] “从问题到根因”和“从根因到恢复”两条固定主旅程均以真实数据通过。
- [ ] AI Chat 已重构为智能调查，主体不是聊天气泡。
- [ ] 菜单严格按第 2.3 节的总览/智能运维/可观测/资源/治理/系统管理及其子项收敛。
- [ ] AI Tool、Workflow、图谱、报告、变更、容量、Grafana 不再作为不必要的普通一级技术入口。
- [ ] 第 2.5 节全部核心页面能回答各自固定问题，且健康、无数据、不可用和权限不足语义不混淆。
- [ ] 服务、日志与指标、Trace、K8s、告警、审批专业页面保留。
- [ ] 专业页面可带完整 context 发起调查。
- [ ] Evidence 可回专业页面，deep link 不被当前全局 cluster 覆盖。
- [ ] 日志页有原始日志/异常模式双视图。
- [ ] user/admin/approver 的前端展示与服务端权限一致，篡改 localStorage 不提权。

## 清理与工程质量

- [ ] 旧 AI Session/Prompt RCA/Tool routing/schema adapter/死 endpoint 已删除。
- [ ] Flow 与 LangGraph 不再重复控制同一调查/审批/执行/验证。
- [ ] 无明显死文件、route、handler、API wrapper、state、hook、CSS、测试和依赖。
- [ ] 删除文件、LOC、route、handler、compatibility path、依赖均量化。
- [ ] Python/Go/frontend 直接依赖 Before/After 有依据。
- [ ] Docker context 不含 Git、backup、venv、node_modules、tests、旧 dist、runtime data。
- [ ] runtime 镜像不含无关 compiler/source/test/cache。
- [ ] 自研运行镜像总大小至少降低 20%，功能、安全、稳定性和离线运行未受损。

## 测试、部署与同步

- [ ] Python full test、compileall、pip check 通过。
- [ ] 全部 Go test、vet 通过；race 在可用工具链执行或明确记录环境阻塞，最终发布环境必须补跑。
- [ ] Frontend typecheck、unit/component、production build 通过。
- [ ] Helm lint/template、clean Docker build/start 通过。
- [ ] 新数据采集链依据部署后真实数据通过。
- [ ] 12 个 RCA scenario 全链通过。
- [ ] 真实多集群隔离通过。
- [ ] 真实 LLM 10 个固定问题通过，未使用 mock。
- [ ] Browser E2E 全路由和关键交互通过。
- [ ] P0=0、P1=0。
- [ ] 最终 Pod 运行最终正式镜像 tag/digest。
- [ ] 七份最终文档完整且证据可核验。
- [ ] Git 提交不含 Secret、backup、binary、venv、node_modules、runtime data、dump 或日志。
- [ ] 获得同步授权后，GitHub remote SHA 与最终本地 SHA 一致。

任意一项未满足：

> 不得宣称“本方案全部完成”；必须明确列出未完成项、证据和下一步。

---

# 8. Luna 最终行为约束

Luna 必须：

```text
先读真实代码和当前测试，再修改
先写失败测试，再实现
新主路径生效后立即删除旧主路径
保留当前有效可靠性、安全和配置资产
使用真实采集数据、真实 Tool、真实 LLM、真实 Browser 验收
持续量化代码、依赖、Docker context 和镜像变化
每个 Gate 用新鲜命令输出证明
```

Luna 不得：

```text
为了历史数据保留旧架构
新增一套新架构但不删除旧生产路径
只隐藏菜单不删除无价值页面和独占代码
只加 Tool 不统一 ToolResult
只让 LLM 总结日志而不实现 Log Agent
输出无 Evidence 的现场事实或 Root Cause
将 permission_denied/unavailable/no_data 解释成健康
让 LLM 或客户端绕过 Risk/Approval/Execution Gate
把命令成功当业务恢复
删除 WAL、preflight、ShellPolicy、Secret 复用等当前有效机制以追求简化
只单测不部署，或只部署不做真实 LLM/Browser 验收
忽略 P0/P1 后声称完成
擅自删除用户未跟踪文件、备份或配置
未获授权自动清理运行数据或同步 GitHub
```

最终工程结果必须收敛为：

```text
Trusted Context
  → Intent
  → Planner
  → Agents
  → ToolResult
  → Evidence
  → Hypothesis RCA
  → Structured Action
  → Risk / Approval
  → Execution
  → Verification
```

前台聚焦运维任务，后台复用复杂能力；历史运行数据全部舍弃，从统一的新数据面和可信 AI 控制面重新开始。
