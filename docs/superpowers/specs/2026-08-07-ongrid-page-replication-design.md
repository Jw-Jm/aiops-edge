# ongrid 页面完整复刻方案（含对应功能）

> 日期：2026-08-07 ｜ 状态：代码深度审计 + requesting-code-review 独立审核修正
> 范围：**页面 + 页面所依赖的后端功能** 一并复刻（不只 UI 壳）
> 依据：ongrid-ref 前端 `web/src/pages/` + `web/src/api/` + 后端 `internal/manager/server/` × 自研 4 仓库代码实据

---

## 〇、核心结论

**页面完整复刻 ≈ 48 页 + 全新后端子系统，是超大工程（非"能力复刻"可比）。** 页面缺口的 60% 成本在后端功能，尤其：
- **通用 PromQL `query_range` 端点缺失**（Monitor/Devices/Dashboard 地基；且 **VictoriaMetrics 时序库本身尚未接入**，需从零建，非"接上"）
- **告警 incident 状态机缺失**（ack/resolve/事件时间线；IncidentDetail 页地基，须优先于页面）
- **SSE 事件协议不兼容**（自研 `progress/chunk` vs ongrid `assistant/tool_*`，是跨前后端契约重构，ChatThread 及所有 agent 型页面的共同地基）
- **设备/主机管理、WebSSH 全缺**（Devices）
- **集群注册/健康/事件/升级缺**（Clusters/K8s）
- **Topology 目录 CRUD 缺**（拓扑专项）
- **Agent/Skills/Workflows HTTP 接口缺**（AI 运维三页）
- **Admin RBAC 缺**（用户/组织/审计）

---

## 一、页面 → 功能 → 后端现状 映射总表

> ✅=自研已备可对接 ｜ 🔶=需补/增强 ｜ ❌=缺失

| # | 页面 | 核心后端功能需求 | 自研现状 | 判定 |
|---|---|---|---|---|
| 1 | ChatThread `/chat/:id` | SSE 流式、工具事件、会话管理、审批联动、stop/mentions/query-translate | SSE✅；工具/assistant 事件、stop、mentions、query-translate ❌ | 🔶 |
| 2 | Dashboard `/dashboard` | KPI/趋势/告警环/资源top/会话统计/用量 | 聚合🔶；PromQL/设备/token 统计 ❌ | 🔶 |
| 3 | Devices/EdgeDetail `/devices` | 设备 CRUD、PromQL 实时指标、进程/网络、WebSSH | 全部 ❌ | ❌ |
| 4 | Clusters/K8s `/clusters`,`/kubernetes` | 集群注册/节点/Pod/Deployment/事件/升级 | 仅只读节点/Pod/Deployment | 🔶 |
| 5 | Topology `/topology` | 节点/关系/类型目录 CRUD | 只读全局拓扑，无目录 | 🔶 |
| 6 | Alerts+Incident `/alerts`,`/incidents/:id` | 规则 CRUD/事件/ack/静默/解决/AI 根因 | 规则🔶+静默✅+rca✅；状态机/事件线/preview ❌ | 🔶 |
| 7 | Agents `/agents` | persona CRUD+工具绑定 | ❌ | ❌ |
| 8 | Skills `/skills` | 目录/安装/执行 | 仅内部代码，无 HTTP API | ❌ |
| 9 | Workflows `/workflows` | flow CRUD/运行/节点配置 | 仅 LangGraph 内部 DAG | ❌ |
| 10 | Approvals `/approvals` | 收件箱/通过/拒绝/待办数 | ops/tasks approve/reject 有，但**数据模型不匹配**（ongrid 是 `kind/source/status` 实体，自研是 task 模型），需适配层 | 🔶 |
| 11 | MCP `/mcp` | server 注册/测试/工具 | 硬编码工具；无注册 CRUD | 🔶 |
| 12 | Knowledge `/knowledge` | RAG 检索/文档/代码索引 | RAG✅；文档/代码索引❌ | 🔶 |
| 13 | Reports `/pages`,`/reports` | 报告 CRUD/渲染/导出/分享/调度 | 生成/历史✅；调度/分享❌ | 🔶 |
| 14 | Monitor `/monitor` | 9 面板/面板 CRUD/PromQL | ❌ | ❌ |
| 15 | Settings 子页 | LLM/集成/通知/通道/密钥/升级 | 仅 LLM 配置 | 🔶 |
| 16 | Admin `/admin/*` | 用户/组织/审计 CRUD | 仅 tenant，无 RBAC | ❌ |
| 17 | Logs `/logs` | LogQL 查询/过滤 | 自研已备（VictoriaLogs 代理+过滤） | ✅ |
| 18 | Traces `/traces`,`/traces/:id` | 链路列表/详情 | 自研已备（Span 瀑布+关联证据） | ✅ |
| 19 | Tasks `/tasks` | 报告调度/任务工作台 | 自研已备（运维任务+报告历史，语义略不同） | ✅/🔶 |
| 20 | Home `/` | 聚合门户/AI 聊天入口 | 自研 Overview+`/aichat` 对应 | 🔶 |
| 21 | AlertRules `/alerts/rules` | 规则 CRUD+promql 预览/试算 | 规则 CRUD 有；preview/试算缺 | 🔶 |
| 22 | PageView `/pages/:id/view` | 全屏 artifact 宿主页 | ❌（报告中心缺 artifact 渲染） | 🔶 |

> 注：ongrid 实计 **~48 页**（含 11 Settings 子页 + 4 Admin 子页）；上表为 22 个核心页映射，Settings/Admin 子页按组归并。

---

## 二、自研已备（页面可直接对接功能）

Chat SSE 流式、Chat 会话列表、任务审批 approve/reject、告警规则基础 CRUD、告警静默、**AI 根因分析 `/ops/rca/alert`**、RAG 检索 `/ops/cases/search`、MCP 工具调用、报告生成/历史、LLM 配置、K8s 节点/Pod/Deployment 只读、全局服务拓扑只读。

> ⚠️ **预期管理（勿误读为"拿来即用"）**：
> - **Chat SSE 事件协议不兼容**：自研是 `progress/chunk/suggestion/done/error`，ongrid 是 `assistant/tool_start/tool_end/approval_pending/done/error`——需前后端契约重构，非直接对接。
> - **MCP 仅"工具调用"**：自研有 `/mcp/tools`+`/mcp/call`，但**无 server 注册/测试/credential CRUD**，复刻 MCP 页需补后端。
> - **告警"规则 CRUD"无 preview/enable、无 incident 状态机**，仅基础 CRUD。

---

## 三、后端功能缺口（复刻必须先补，按地基性排序）

### T0 可观测性地基（Monitor/Devices/Dashboard 共同依赖）
- **VictoriaMetrics 时序库从零接入**：当前代码**只有 victoria-logs 在用，VictoriaMetrics 时序库本身未接入**（指标从 trace_spans 聚合）。需先建 VM 接入（连接/写入/采集），再谈 PromQL。
- **通用 PromQL `query_range` 端点**：query-api 提供 `GET /api/v1/metrics/query_range?query=&start=&end=&step=`。这是 Monitor、Devices 实时指标、Dashboard 的地基。

### T1 业务实体子系统 + 告警状态机
- **告警 incident 状态机**（ack/resolve/事件时间线）：IncidentDetail 页的地基，**须先于页面**（审核修正：从 T3 上调）。
- **设备/主机管理**：Devices CRUD + 在线状态 + 进程/网络。
- **集群注册与运维**：Clusters 注册/bootstrap token/健康/事件/升级。
- **Topology 目录体系**：`topology_node`/`node_type`/`relation_type` 表 + CRUD 接口 + 语义化（见拓扑专项）。
- **RBAC/用户/组织/审计**：Admin 三页。

### T2 AI 运维功能 + 契约层（Chat/Agents/Skills/Workflows 依赖）
- **SSE 事件协议契约重构**（跨前后端，ChatThread 及所有 agent 型页面共同地基）：自研 `progress/chunk` → 对齐 ongrid `assistant/tool_start/tool_end/approval_pending`。
- **Agent/Persona CRUD API**：Agents 页。
- **Skills 目录/安装/执行 HTTP API**：Skills 页。
- **Workflows CRUD + 运行 + 节点配置 API**：FlowEditor 页。

### T3 增强项
- 告警规则 preview/enable。
- 报告调度 CRUD、分享 token、页面 artifact。
- Knowledge 文档管理 CRUD、代码仓库索引。
- 通用 system-settings 键值、secret 凭据库、集成测试端点。

---

## 四、拓扑图专项（含功能）

核心判断：**问题在数据模型，不在渲染库**（G6 与 React Flow 能力等价，保留 G6）。

| 工作项 | 后端功能 | 工作量 |
|---|---|---|
| 数据模型 | 新增 `topology_node`/`node_type`/`relation_type` 表；`service_topology` 加 `relation_type`/`semantics_tag`/`propagates_failure` | 高 3-5 人日 |
| query-api | 节点/关系/类型目录 CRUD + 语义化接口 | 高 3-4 人日 |
| 前端渲染 | tier 分层带 + 类型着色 + 语义边样式（G6 自定义） | 中 2-3 人日 |
| 前端交互 | 聚焦 BFS / 类型显隐 / 孤立开关 / CRUD 面板 | 中 2-3 人日 |
| 详情抽屉 | 邻居关系 + props + 编辑 | 中低 1-2 人日 |
| 故障传播 | `propagates_failure` 影响面推理 + AIOps 联动 | 显著上调 |

**合计**：仅外观+交互 5-8 人日；含数据模型+后端目录 10-15 人日；含故障传播联动显著上调。

---

## 五、分阶段复刻路线（页面 + 功能成组交付）

### Phase A — 可观测地基 + 已有能力页面（低风险，先做）
1. **VictoriaMetrics 从零接入 + 通用 PromQL `query_range` 端点**（T0，地基）
2. **布局壳重构**：Sidebar 8 区段 + ⌘K⌘P + 深色 zinc（纯前端）
3. **Logs / Traces**（自研已备，UI 对齐 ongrid，低成本项）
4. **Dashboard**（Overview 升级 + 聚合接口 `/dashboard/stats` + 会话/告警统计）
5. **Monitor**（9 面板，依赖 PromQL）

### Phase B — 告警状态机 + AI 运维功能
6. **告警 incident 状态机**（ack/resolve/事件时间线，T1 地基）+ **IncidentDetail 页**（复用 rcaAlertAnalysis）
7. **SSE 事件协议契约重构**（`progress/chunk` → `assistant/tool_start/tool_end/approval_pending`，跨前后端）+ **ChatThread**
8. **Agents**：Persona CRUD API + 页
9. **Skills**：目录/安装/执行 HTTP API + 页
10. **Workflows**：flow CRUD + 运行 + FlowEditor（React Flow 或封装 LangGraph）
11. **Approvals/MCP 前端页**（需数据适配层，非纯前端）

### Phase C — 基础设施/拓扑/知识库（大工程）
11. **Topology**（数据模型 + 目录 + 语义 + 交互，10-15 人日专项）
12. **Devices**（设备管理 + PromQL + WebSSH xterm）
13. **Clusters/K8s**（注册/健康/事件/升级）
14. **Knowledge**（文档 CRUD + 代码索引）

### Phase D — 治理与设置（独立立项）
15. **Admin**（RBAC/用户/组织/审计）
16. **Settings 子页**（集成/通知/通道/密钥/升级）
17. **Reports**（调度/分享/artifact）

---

## 六、风险

1. **VictoriaMetrics 从零接入是前提**：当前 VM 时序库未接入（仅 VL 在用），Monitor/Devices/Dashboard 全依赖，需先建，非"接上"。
2. **SSE 事件协议是契约重构**：自研 `progress/chunk` 与 ongrid `assistant/tool_*` 不兼容，涉及前后端联动改造，是 ChatThread 及 agent 型页面的共同地基。
3. **设备/主机管理是全新域**：自研无设备模型，Devices 是"从 0 建数据模型 + 采集 + WebSSH"三重工程。
4. **拓扑是数据模型改造**：非前端换库，需动 ClickHouse + query-api。
5. **48 页完整复刻规模远超能力复刻**：建议按 Phase A-D 分批，每批独立验收，避免一次性大重构风险。

---

## 七、授权风险与合规红线（最高优先级）

> **核心原则**：复刻"能力/设计"不侵权；复刻"代码/数据结构表达"侵权。AGPL 传染性仅在复制 ongrid 代码时触发。

### 7.1 许可证现状

| 项目 | 许可证 | 传染性 |
|---|---|---|
| 自研 `aiops` | **Apache 2.0** | 宽松，不传染 |
| ongrid（参考） | **AGPL v3** | 强传染（GPL 家族最强） |

**规则**：若"传播"了"基于 ongrid 代码的衍生作品"，整个作品须 AGPL 开源——包括自研 Apache 部分。

### 7.2 风险分级

| 复刻项 | 风险 | 结论 |
|---|---|---|
| 深色 UI / 布局 / 交互范式 | 低 | 可做，独立实现（思想不属版权保护） |
| 功能清单（有拓扑/告警状态机） | 低 | 可做 |
| `topology_node`/`node_type`/`relation_type` 表结构 | **中-高** | 换自研命名，只保留能力 |
| `semantics_tag` 枚举 / `tier` 分层 | **中-高** | 换自研枚举/字段名 |
| SSE 事件 `tool_start/tool_end/approval_pending` | **中-高** | 换名或自定义协议 |
| `/approvals/{id}/approve` 等 API 路径/字段 | 中 | 换自研 API 设计 |
| 前端 TSX / CSS / Tailwind 组件 | **高** | **严禁复制**，从功能需求重写 |

### 7.3 合规红线（实施时强制执行）

1. **Clean-room 独立实现**：复刻每个 ongrid 对应功能时，从"功能需求"出发重写，不逐行照抄 ongrid 源码。
2. **不复制数据结构表达**：表结构/枚举/事件类型/API 契约**改用自研命名**（如 `semantics_tag`→`impact_semantic`、`tier`→`layer`），只保留能力等价。
3. **不引入 ongrid 依赖/组件**：不 copy `Graph.tsx`/`topology.ts`/`semantics` 等模块。
4. **隔离**：`ongrid-ref/` 继续被 `.gitignore` 排除、不入库、不参与构建。
5. **留存合规证据**：每个复刻功能记录"功能需求 vs 自研实现"对照表，防未来被质疑。
6. **开源策略**：自研保持 Apache 2.0，确保所有复刻功能为独立创作；不混入 ongrid 代码。

### 7.4 现状核查（已做对）

- `ongrid-ref/` 已 `.gitignore` 隔离、**未入库**（仓库 main 无 ongrid 代码）。
- 已部署的自研 4 服务为独立编写，**低风险**。
