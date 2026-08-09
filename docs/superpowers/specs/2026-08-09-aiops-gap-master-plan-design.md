# AIOps 平台差距补齐总体方案（资深架构师审查）

**日期**: 2026-08-09
**性质**: 全面架构审查 + 差距分析 + 新方案（不考虑旧文档，参考 ongrid 能力面，基于当前代码实际）
**目标**: 补齐自研 AIOps 与 ongrid 的差异化能力差距，强化"AI 运维"定位
**执行方式**: 分批实施（本 spec 为总纲，每批独立 spec/plan）

---

## 1. 现状分析（已实现能力，成熟区）

### 1.1 前端（38 页面，观测 + AI 运维 + 管理治理成熟）
- **可观测**：总览/监控/服务/服务详情/拓扑（typed graph）/拓扑目录/链路/链路详情/日志/DeepFlow
- **AI 运维**：AIChat（多专家）/ChatThread/Skills/Agents/Workflows/WorkflowEditor/WorkflowDetail/Tasks/Approvals/NL2SQL
- **管理治理**：Users/Admin（scope+审计+集群）/Settings/Knowledge/Reports/Shell/Audit/Rules/Snmp/Hardware/Mcp/Catalog/Devices/Clusters

### 1.2 后端（query-api Go + ingest Go + orchestrator Python）
- **可观测底座**：ingest OTLP → CH(trace/log) + VM(指标) + VL(日志)
- **告警基础**：规则 CRUD（threshold/mutation/anomaly/forecast/burn_rate/metric_raw）+ 事件状态机（firing/ack/resolve/timeline）+ 静默 + 时间聚合 + 规则级 webhook
- **AI 编排**：多专家聊天/技能（9 skill）/Agent/Flow 引擎/RCA（确定性+假设双引擎）/审批/审计/WebShell/RAG/报告
- **治理**：用户 scope + RequireScope + kubeconfig 多集群 + 审计

### 1.3 数据架构
- **VM** = 指标（node-exporter + 服务RED + orchestrator/ingest）
- **CH** = trace + log + service_topology
- **MySQL** = 配置 + 告警 + 用户/资产 + 业务（审批/审计/Agent/报告，orchestrator 写）
- **VL** = 日志

---

## 2. ongrid 差距对照（架构师视角核心差距）

### 差距 A：AI 智能内核薄弱（支撑"AI"定位的最短板）

| ongrid | 自研现状 | 差距 |
|---|---|---|
| 告警自动调查 investigator（告警触发自动 RCA）| 仅手动触发 | 🔴 |
| 40+ 确定性工具（correlate_incident/expand_topology/rank_edges/query_change_events）| ~18 工具 | 🔴 |
| 双层 Agent（Coordinator + 子 Agent + Reviewer SOP）| 单层 LangGraph | 🔴 |
| LLM function-calling 工具循环 | 一次性采集 | 🔴 |
| 异常检测统计模型（zscore/MAD）| 简单均值偏离 | 🟡 |
| 告警自动恢复（谓词清除 auto-resolve）| 人工 /resolve | 🔴 |

### 差距 B：告警运维闭环不完整

| ongrid | 自研现状 | 差距 |
|---|---|---|
| 4 重降噪栈（dedupe/cooldown/inhibit/dampening）| 仅时间窗口聚合 | 🔴 |
| 8 种规则类型（含 log/trace_latency/burn_rate）| threshold/mutation 为主 | 🟡 |
| SLO 多窗口烧毁率（metric_burn_rate）| 无 | 🔴 |
| 监控看板（Monitor 10 面板 + 自定义）| Monitor 半成品（裸 JSON）| 🔴 |

### 差距 C：治理/值班/运维闭环

| ongrid | 自研现状 | 差距 |
|---|---|---|
| 审批闸门（safe/mutating/dangerous 工具分级 + 审批中心）| 仅 shell_policy + risk_score | 🔴 |
| K8s 生命周期（注册/升级/资源→拓扑/动作审计）| 有集群/kubectl | 🟡 |
| Infrastructure 页面挂载 | 孤儿（有功能无入口）| 🟡 |
| 成本分析（FinOps）| 无 | 🔴 |
| 细粒度 RBAC（角色/权限矩阵/菜单级）| 仅用户级 scope | 🔴 |

---

## 3. 总体方案（三阶段，分批实施）

### Phase A：AI 智能内核升级（差异 A）

**A1. 告警自动调查（investigator）**
- 告警事件触发 → 自动后台触发 RCA 分析（不阻塞告警流程）
- 分析结果（根因假设/证据链）关联到事件，前端 incident 详情展示
- 复用现有确定性 + 假设 RCA 引擎，改为告警触发自动调度

**A2. LLM function-calling 工具循环**
- Agent 推理时主动调用工具（probe/topology/logs/metrics）获取新证据，再继续推理
- 替代"一次性采集喂 LLM"模式，实现迭代式调查
- 新增循环控制（max_steps、工具白名单、成本护栏）

**A3. 双层 Agent 架构**
- Coordinator Agent：理解用户意图 → 派发任务给子 Agent
- 子 Agent（诊断/处置/RAG/巡检）：执行具体任务
- Reviewer：审查子 Agent 输出，SOP 校验
- 基于 LangGraph 多节点图实现

**A4. 异常检测统计模型升级**
- 实现 zscore / MAD（中位数绝对偏差）统计检测，替代简单均值偏离
- 规则类型 `anomaly` 升级为真实统计检测

**A5. 告警自动恢复**
- 规则支持 `clear_condition`（谓词清除）：当指标恢复到正常阈值时自动 resolve 事件
- 替代人工 /resolve

### Phase B：告警运维闭环（差异 B）

**B1. 4 重降噪栈**
- dedupe（按 service+rule+signature 去重）、cooldown（触发冷却）、inhibit（抑制静默）、dampening（波动抑制）
- 替代单一时间窗口聚合

**B2. 规则类型补齐**
- 支持 `log`（日志关键词/错误率）、`trace_latency`（链路延迟）、`trace_error_rate`、`burn_rate`

**B3. SLO 多窗口烧毁率**
- SLO 目标管理（availability/latency）+ 多窗口烧毁率评估（metric_burn_rate）

**B4. Monitor 监控看板完成**
- 完整图表渲染（ECharts/antd charts），10 面板数据化
- 支持自定义面板（选指标/选图表类型）

### Phase C：治理与闭环（差异 C）

**C1. 审批闸门体系**
- 工具分级（safe/mutating/dangerous）+ 危险操作自动进审批中心
- 审批中心完善（approve/reject/超时策略）

**C2. Infrastructure 挂载 + 审计补强**
- Infrastructure 页面挂导航
- K8s 动作审计补强

**C3. 产物中心完善**
- Agent/工作流生成产物统一管理、分享

### 后续扩展（可选）
- 成本分析（FinOps）、细粒度 RBAC（角色/权限矩阵）、租户隔离、容量预测

---

## 4. 分批实施计划

| 批次 | 内容 | 依赖 | 预估 |
|---|---|---|---|
| **批 1** | A1 告警自动调查 + A5 告警自动恢复 | 告警引擎 | 中 |
| **批 2** | B1 4 重降噪 + B2 规则类型补齐 | 告警引擎 | 中 |
| **批 3** | A2 function-calling + A3 双层 Agent | AI 编排 | 大 |
| **批 4** | A4 异常检测统计 + B3 SLO 烧毁率 | 指标 | 中 |
| **批 5** | B4 Monitor 看板 + C1 审批闸门 | 前端 + 治理 | 中 |
| **批 6** | C2 Infrastructure + C3 产物中心 | 前端 | 小 |

---

## 5. 设计原则

- **全部自研**：参考 ongrid 功能/设计，绝不复制其代码（AGPL 合规红线）
- **数据所有权**：延续已对齐的契约（AI 业务数据 orchestrator 写，平台基础 query-api 写）
- **组件最小化**：优先复用现有引擎（VM/CH/MySQL/现有 Agent），不引入新组件
- **分批验证**：每批独立 spec/plan + TDD + 部署验证

---

## 6. 自审
- [x] 覆盖 ongrid 三大差距（AI 智能/告警闭环/治理）
- [x] 基于当前代码实际（非旧文档）
- [x] 三阶段分批，每批独立可交付
- [x] 合规（自研不复制 ongrid）
