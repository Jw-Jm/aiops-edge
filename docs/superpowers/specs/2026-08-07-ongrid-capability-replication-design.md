# ongrid 能力复刻可行性方案（修正版）

> 日期：2026-08-07 ｜ 状态：代码审计 + 独立 skill 审核后定稿
> 依据：ongrid 官方 README_ZH.md 特性清单 × 自研 `aiops/` 代码实据（4 仓库 + ongrid-ref 参考）
> 方法：code-explorer 并行审计四维（界面/功能/框架/架构）→ requesting-code-review 独立审核 → 修正定稿

---

## 〇、核心结论

**ongrid 能力面约 7 成可复刻，3 成不可/不建议。** 复刻目标是**能力面**（自研技术栈实现等价能力），**不是技术栈**（不引入 Qdrant/Prometheus-Loki-Tempo/Edge 代理）。

三档定性：
- ✅ **可复刻**：能力等价，自研已备或需前端页/小改
- 🔶 **需大工程**：能力可实现但为全新子系统/大模块
- ❌ **不建议**：深度绑定 ongrid 架构，复刻成本远高于价值或方向相反

---

## 一、能力清单可行性总表（对照 ongrid README）

### 1. AI Agent 能力

| ongrid | 自研现状 | 审核后判定 |
|---|---|---|
| Coordinator+Specialist 双层 Agent | 4 专家（agents.py + experts.py）+ `match_intent` 单专家路由；**无派活/协调层** | 🔶 部分：需在 DAG 顶部加 Coordinator 路由 |
| 告警自动调查 Investigator | webhook **只登记不自动**；但 DAG 有 `node_holmes`（Trace 调查引擎，用户触发时运行） | 🔶 可复刻：补自动触发循环 |
| RCA 根因分析 | `rca.py` 确定性 3 层 + 假设引擎 4 步证伪 + 告警联动 | ✅ 已具备且强 |
| 自带任意模型 + 热路由 | `llm_providers.go` 多 provider + AES 加密 + 版本回滚 | ✅ 已具备（provider 面可扩） |
| Reviewer Agent | 无独立审查角色 | 🔶 可复刻：LangGraph 加审查节点 |

### 2. 可观测性

| ongrid | 自研现状 | 判定 |
|---|---|---|
| 指标（Prometheus） | VictoriaMetrics **假接入**（query-api 从不查 VM，`metric_service_red` 只写不读） | 🔶 需补真 |
| 日志（Loki） | VictoriaLogs 真接入（Proxy + pod 增量采集 shipper） | ✅ 已具备 |
| 链路（Tempo） | ClickHouse `trace_spans` + DeepFlowSyncer | ✅ 已具备 |
| Grafana 面板 | deepflow-grafana | ✅ 已具备 |
| 拓扑影响面 | `service_topology` + G6 | ✅ 已具备 |
| 告警 | threshold/mutation + 每 60s 评估 + vmalert | ✅ 已具备 |
| 网络设备管理（SNMP） | 无 | 🔶 新模块 |

### 3. 远程执行与安全

| ongrid | 自研现状 | 判定 |
|---|---|---|
| 零入站端口（Edge 外联） | DeepFlow eBPF 被动采集，无外联隧道 | ❌ 不建议（方向相反） |
| 浏览器 SSH（双向） | 仅一次性 `subprocess.run`，无 pty/WS 双向流 | 🔶 需新子系统（pty+WS） |
| 只读巡检 26+ 工具 | shell_policy（readonly/write）+ 8/16 工具 | 🔶 补工具 + dangerous 分级 |
| 审批/写入闸门 | 任务审批（approve/reject）+ ShellPolicy | ✅ 已具备 |

### 4. K8s 生命周期

| ongrid | 自研现状 | 判定 |
|---|---|---|
| 注册集群/查工作负载/事件 | `infrastructure.go` 接 K8s API + Infra 页 | ✅ 已具备 |
| 集群升级管理 | 无 | 🔶 新模块 |
| K8s→拓扑映射 | DeepFlowSyncer + service_topology | ✅ 已具备 |

### 5. 平台功能模块

| ongrid | 自研现状 | 审核后判定 |
|---|---|---|
| **工作流编排**（HLD-016 + FlowEditor `/workflows`） | **自研 LangGraph 是固定 AI 诊断管道，非用户可编排引擎**；无 FlowEditor | 🔶 **需新建可视化编排层**（审核修正：≠ 现成 flow） |
| 技能目录 `/skills` | skill_registry（16 工具）后端已备，**无前端页** | ✅ 已具备（缺前端页） |
| MCP 服务器 `/mcp` | `mcp_server.py`（8 工具）已备，**无管理页** | ✅ 已具备（缺前端页） |
| 知识库 `/knowledge`（含 `/repos` 代码索引） | **案例级 RAG**（ChromaDB + 反馈/衰减闭环）已备；**代码/文档索引缺失** | 🔶 部分（审核区分：案例 RAG ✓ / 代码 RAG ✗） |
| 产物中心 `/pages` | 报告存 MinIO+CH，内嵌任务台，无独立页 | 🔶 需前端独立页 |
| 监控工作台 | 服务/日志/链路/告警多页 | ✅ 已具备 |
| 拓扑图 | G6 | ✅ 已具备 |

### 6. 通信与集成

| ongrid | 自研现状 | 判定 |
|---|---|---|
| Slack/Telegram/飞书/钉钉/企微/Webhook | **完全缺失**（仅告警 webhook 入口） | 🔶 6 通道大模块 |

### 7. 治理（审核补充）

| ongrid | 自研现状 | 判定 |
|---|---|---|
| 独立审批中心 `/approvals` | 审批融入任务台，无独立页 | 🔶 需独立页 |
| RBAC/组织/审计 `/admin/users|orgs|audit` | 仅单账号 admin/admin123 登录，**无 RBAC** | 🔶 需新模块 |
| Agent 管理 `/agents` | 无 | 🔶 需前端页 |
| 升级管理 `/settings/upgrade`、多集群 `/clusters` | 无 | 🔶 新模块 |

### 8. 架构（不建议复刻）

| ongrid | 自研 | 判定 |
|---|---|---|
| Go 后端 + React 前端 | Go + React | ✅ 栈一致 |
| Qdrant 向量库 | ChromaDB | ❌ 保留自研 |
| Prometheus/Loki/Tempo | ClickHouse 统一 + Victoria | ❌ 保留自研（数据仓更强） |
| Edge 代理 + 零入站 | DeepFlow eBPF | ❌ 方向相反 |
| GORM+MySQL 业务库 | **query-api 零数据库驱动**（go.mod 仅 jwt） | 🔶 全新子系统 |

---

## 二、审核修正采纳记录

| # | 修正 | 影响 |
|---|---|---|
| M1 | **flow≠LangGraph**：自研 LangGraph 是固定 AI 诊断管道，ongrid flow 是用户可编排引擎（FlowEditor） | P1c 仍需做可视化编排层，非"0 改动" |
| M2 | **区分案例 RAG 与代码 RAG**：自研有案例级 RAG（反馈/衰减），缺 `/knowledge/repos` 代码索引 | 知识库可行性细化为"部分" |
| M3 | 补充 ongrid 差距点：独立审批中心、RBAC/审计、Agent 管理页、升级/集群管理 | 治理维度纳入对照 |
| M4 | 认可自研已备：K8sGPT、Cohen's d 执行后验证、审计日志写 VL、报告导出、多 provider 回滚 | 使可行性结论更公允 |

---

## 三、自研已具备（无需复刻，直接为能力底座）

- LangGraph 15 节点 DAG（collect→clean→rca→rag→crewai/holmes→plan→risk→approval→execute→verify→report→memorize→summarize）
- RCA 引擎（确定性 + 假设证伪）、Cohen's d 执行后验证 + 副作用检测
- 多 LLM provider + AES 加密 + 版本回滚
- 案例级 RAG + 反馈闭环、K8sGPT、审计日志写 VL
- ClickHouse 8 表（数据仓）、DeepFlowSyncer 增量同步（可配置间隔+水位+容错）
- 告警评估、Infra、日志 shipper、报告导出（Markdown/PDF）

---

## 四、复刻落地路线（按价值/成本优先）

### Phase 0 — 界面基因（纯前端，最快）
1. zinc 色板收敛 + 清 AIChat 硬编码浅色块
2. 引入 Zustand（store/ui, auth, theme, chatSessions）
3. 新建 StatusPill/StatusRow/Sparkline 轻组件
4. 首页改大输入框+PromptCard 第一入口
5. CommandPalette + AgentSidePanel（⌘K/⌘P）

### Phase 1 — 工具可视化 + 治理（后端+前端）
6. orchestrator 补 `tool_start/tool_end` SSE + 前端 ToolCard
7. 统一工具注册表（MCP 8 并入 skill_registry 16，ToolDef 自动派生 JSON Schema）
8. 技能目录页 `/skills`、MCP 管理页 `/mcp`、告警详情页 `/alerts/:id`
9. 独立审批中心 `/approvals`

### Phase 2 — 编排与自动调查（成熟地基上的增量）
10. **可视化工作流编排**（审核修正 M1）：基于现有 DAG 封装 FlowEditor `/workflows`（用户可编排 DAG）
11. Investigator 自动调查循环（webhook"只登记"→"按需自动"，带冷却/去重）
12. WebShell 双向流（pty + WS，复用 shell_policy）
13. 审批分级（write 拆 dangerous 三级 + 多级审批）
14. 代码/文档 RAG 索引（`/knowledge/repos`）

### Phase 3 — 独立大工程（单独立项）
15. **MySQL 业务状态库**（query-api 引 gorm/mysql + 迁移内存/文件/SQLite 业务状态）
16. IM 通道（飞书/钉钉/企微/Slack/Telegram/Webhook + 语言匹配）
17. RBAC/组织/审计、升级管理、SNMP 设备管理

### 明确不做（❌）
- Edge 代理 + 零入站端口（自研 eBPF 方向相反）
- Qdrant 替换 ChromaDB、Prometheus-Loki-Tempo 替换 ClickHouse+Victoria

---

## 五、风险与建议

1. **MySQL 是工程量主基准**：query-api 零 DB 驱动，业务状态散落内存/`/tmp` JSON/SQLite/CH，需从零建存储层。
2. **界面基因是"从零"非"改"**：G4/G5 前后端都无，工作被低估。
3. **工具 schema 两套并存需统一**：MCP(8) vs skill_registry(16)，schema 手工硬编码。
4. **方案引用接口不存在**：`tool_start/tool_end`、`/dashboard/stats`、`/alerts/:id` 均为规划项，非现状。

**一句话建议**：复刻 ongrid **能力面**（7 成可复刻，核心 AI/可观测几乎全覆盖），**不复制技术栈**；按 Phase 0-3 重排 `ONGRID_REPLICATION_PLAN.md`，将 MySQL、界面基因自研、IM/RBAC 如实纳入排期。
