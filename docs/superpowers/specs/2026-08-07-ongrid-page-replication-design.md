# ongrid 页面完整复刻方案（含对应功能）

> 日期：2026-08-07 ｜ 状态：代码层面深度审计定稿
> 范围：**页面 + 页面所依赖的后端功能** 一并复刻（不只 UI 壳）
> 依据：ongrid-ref 前端 `web/src/pages/` + `web/src/api/` + 后端 `internal/manager/server/` × 自研 4 仓库代码实据

---

## 〇、核心结论

**页面完整复刻 ≈ 40 页 + 全新后端子系统，是超大工程（非"能力复刻"可比）。** 页面缺口的 60% 成本在后端功能，尤其：
- **通用 PromQL `query_range` 端点缺失**（Monitor/Devices/Dashboard 地基，自研完全没有）
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
| 10 | Approvals `/approvals` | 收件箱/通过/拒绝 | ops/tasks approve/reject ✅ | ✅/🔶 |
| 11 | MCP `/mcp` | server 注册/测试/工具 | 硬编码工具；无注册 CRUD | 🔶 |
| 12 | Knowledge `/knowledge` | RAG 检索/文档/代码索引 | RAG✅；文档/代码索引❌ | 🔶 |
| 13 | Reports `/pages`,`/reports` | 报告 CRUD/渲染/导出/分享/调度 | 生成/历史✅；调度/分享❌ | 🔶 |
| 14 | Monitor `/monitor` | 9 面板/面板 CRUD/PromQL | ❌ | ❌ |
| 15 | Settings 子页 | LLM/集成/通知/通道/密钥/升级 | 仅 LLM 配置 | 🔶 |
| 16 | Admin `/admin/*` | 用户/组织/审计 CRUD | 仅 tenant，无 RBAC | ❌ |

---

## 二、自研已备（页面可直接对接功能）

Chat SSE 流式、Chat 会话列表、任务审批 approve/reject、告警规则基础 CRUD、告警静默、**AI 根因分析 `/ops/rca/alert`**、RAG 检索 `/ops/cases/search`、MCP 工具调用、报告生成/历史、LLM 配置、K8s 节点/Pod/Deployment 只读、全局服务拓扑只读。

---

## 三、后端功能缺口（复刻必须先补，按地基性排序）

### T0 可观测性地基（Monitor/Devices/Dashboard 共同依赖）
- **通用 PromQL `query_range` 端点**：自研完全没有。需 query-api 接 VictoriaMetrics，提供 `GET /api/v1/metrics/query_range?query=&start=&end=&step=`。这是 Monitor、Devices 实时指标、Dashboard 的地基。

### T1 业务实体子系统（Devices/Clusters/Topology/Admin 依赖）
- **设备/主机管理**：Devices CRUD + 在线状态 + 进程/网络。
- **集群注册与运维**：Clusters 注册/bootstrap token/健康/事件/升级。
- **Topology 目录体系**：`topology_node`/`node_type`/`relation_type` 表 + CRUD 接口 + 语义化（见拓扑专项）。
- **RBAC/用户/组织/审计**：Admin 三页。

### T2 AI 运维功能（Chat/Agents/Skills/Workflows 依赖）
- **SSE 工具事件**：`tool_start/tool_end` + `assistant` 结构化消息 + `approval_pending` 内联审批。
- **Agent/Persona CRUD API**：Agents 页。
- **Skills 目录/安装/执行 HTTP API**：Skills 页。
- **Workflows CRUD + 运行 + 节点配置 API**：FlowEditor 页。

### T3 增强项
- 告警 incident 状态机（ack/resolve/事件时间线）、规则 preview/enable。
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
1. **通用 PromQL `query_range` 端点**（T0，地基）
2. **布局壳重构**：Sidebar 8 区段 + ⌘K⌘P + 深色 zinc（纯前端）
3. **Dashboard**（Overview 升级 + 聚合接口 `/dashboard/stats` + 会话/告警统计）
4. **Monitor**（9 面板，依赖 PromQL）
5. **IncidentDetail**（告警 incident 状态机 + ack/resolve + 复用 rcaAlertAnalysis）

### Phase B — AI 运维功能（Chat/Agents/Skills/Workflows）
6. **ChatThread**：SSE 补 `tool_start/tool_end` + `assistant` + `approval_pending` + stop
7. **Agents**：Persona CRUD API + 页
8. **Skills**：目录/安装/执行 HTTP API + 页
9. **Workflows**：flow CRUD + 运行 + FlowEditor（React Flow 或封装 LangGraph）
10. **Approvals/MCP 前端页**（后端已备，纯前端）

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

1. **PromQL 端点是前端功能地基**：Monitor/Devices/Dashboard 全依赖，自研无此能力，需先建。
2. **设备/主机管理是全新域**：自研无设备模型，Devices 是"从 0 建数据模型 + 采集 + WebSSH"三重工程。
3. **拓扑是数据模型改造**：非前端换库，需动 ClickHouse + query-api。
4. **40 页完整复刻规模远超能力复刻**：建议按 Phase A-D 分批，每批独立验收，避免一次性大重构风险。
