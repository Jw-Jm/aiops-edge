# AIOps 平台 — 审计报告 vs 代码实际状态 对齐清单

> 日期：2026-08-08 ｜ 依据：实际代码探索（前端 33 页面 / 后端 20 api + 9 store）+ git 历史 ｜ 目的：校正 `AUDIT-REPORT`/`UI-DESIGNER-FULL-AUDIT`/`PROGRESS-*` 中与代码实际不符的结论，给出真实进度与待办。

---

## 一、核心结论（先读这段）

**审计报告（AUDIT-REPORT / UI-DESIGNER-FULL-AUDIT）描述的现状已滞后于工作区代码。**

最典型证据：报告称「拓扑目录 / AI 诊断 / SQL 查询」三个页面**完全空白**（P0，评分 0.5），但代码里这三个页面**均有完整实现且已接好 API**。原因是：**运行中的前端镜像是旧版本，而新页面代码在工作区但未重新构建/部署**。

因此，对齐后应区分两类问题：
1. **代码层面已解决、仅缺部署** → 重建前端镜像即可消除（审计的 P0 大面积消失）
2. **代码层面确实缺失** → 才是真正的开发待办（见 §三）

---

## 二、审计报告 P0/P1 问题 → 实际状态校正

| 审计编号 | 报告描述 | 代码实际状态 | 校正结论 |
|---|---|---|---|
| AI-01/AI-02 | AI 诊断 `/ai-diagnosis` 空白 | 实为 `AIChat/`（约 21KB，完整聊天 UI + 专家模式 + 审批卡）| ✅ 已实现，**缺部署** |
| SQL-01/SQL-02 | SQL 查询 `/sql` 空白 | 实为 `NL2SQL/`（126 行，NL→SQL→执行→结果表）| ✅ 已实现，**缺部署** |
| TC-01/02/03 | 拓扑目录 `/topology-catalog` 空白 | 实为 `TopologyCatalog/`（322 行，4 Tab CRUD）| ✅ 已实现，**缺部署** |
| M-01/02 | 监控面板 `/monitor` 无数据 | node-exporter/ipmi-exporter 已部署，但 **vmagent 缺失** → 无 scrape | 🔴 **真实待办**（差距①）|
| G-01 | 空白页无统一空态 | 代码中无 `AppEmpty`/`EmptyState` 组件 | ⚠️ 部分待办（可选增强）|

> **路由映射**（报告路由与实际代码不同）：`/ai-diagnosis`→`/aichat`、`/sql`→`/nl2sql`、`/topology-catalog`→`/topology/catalog`。

---

## 三、真实待办清单（按 ongrid 差距补齐计划对齐）

### 核实结论：差距补齐计划的 10 个 Task，部分已实现、部分是真正待办

| 差距块 | Task | 代码实际状态 | 判定 |
|---|---|---|---|
| ① VM 采集 | Task 1 vmagent | **vmagent 完全未部署**（templates/ 无 vmagent）；node-exporter/ipmi-exporter DaemonSet 已部署（2026-08-08）| 🔴 **真待办**（最大前置）|
| ② 告警 DB 化 | Task 2 迁移 MySQL | 告警**纯内存+JSON**（`/tmp/observability-*.json`）；mysql.go **无 5 张告警表**（alert_rules/events/incidents/silences/timeline）| 🔴 真待办 |
| ② 全类型评估 | Task 3 | rule_type **仅 threshold/mutation**；`evaluateRuleHistorical` 是 **stub**；无 anomaly/forecast/burn_rate/log | 🔴 真待办 |
| ② incident+webhook | Task 4 | **无独立 incident 实体/表/timeline**；仅 AlertEvent ack/resolve 状态机（transitionStatus）；webhook 仅全局 env | 🔴 真待办 |
| ③ 用户 scope | Task 5 | User **无 scope 字段**；users 表无 scope 列；**无 RequireScope**；JWT 仅 sub/role | 🔴 真待办 |
| ④ 设备指标 | Task 6 | devices CRUD 有；`/devices/{id}/metrics` 依赖 VM（未做）| ⚠️ 依赖 VM 后做 |
| ④ 集群事件 | Task 6 | clusters CRUD + kubectl 发现有；`/clusters/{id}/events` 待确认 | ⚠️ 待确认 |
| ⑥ 拓扑目录 | Task 7 | `TopologyNodeDAO`+4 表+`SeedTopologyTypes`+`TopologyNodesRouter` CRUD+`SyncTopologyCatalog` **全部就绪并注册路由**；前端 `TopologyCatalog` 已接 API | ✅ 已完成 |
| ⑦ 日志聚合 | Task 7 | 后端**无 `/logs/aggregate`**；client.ts 无 aggregate 封装 | 🔴 真待办 |
| ⑧ AI incident 工具 | Task 8 | `incident_query/ack/resolve/notification_send`、`skill.incident` **均不存在** | 🔴 真待办 |

---

## 四、文档未记录的最新进展（Phase 2，2026-08-08 上午提交）

git log 显示 PROGRESS 文档未包含的一批已完成功能：

| 提交（已 commit） | 内容 |
|---|---|
| `3ccb30a` | SNMP 管理网交换机采集（pysnmp OID）+ 设备 CRUD + 采集调度 |
| `1086bae` | 本地 IPMI ingest（/dev/ipmi0）+ node_exporter 部件可用性聚合 |
| `d0f88be` | ToolDef Class/Scope/Origin 元数据 + snmp/ipmi/node 查询工具 |
| `2ad7d31` | 前端 SNMP 网络设备 + 硬件健康/部件可用性 页面 |
| `4797dea` | node-exporter + ipmi-exporter DaemonSet（本地采集/带外隔离）|
| `c1fa884` | 启动时应用 MySQL 迁移（建全量表）|
| `8ed6711` | 二期采集表 snmp/ipmi/部件可用性 |
| `eace991` | ipmi-exporter 默认启用 + 修复 cluster DNS |

> 这些是「二期采集（SNMP+node_exporter+本地IPMI）」成果，与 ongrid 差距补齐计划**不同轨**，需纳入总进度。

---

## 五、工作区未提交改动（重要：可能是下一步交付）

`git status` 显示大量未提交改动，属**新页面代码已写好但未部署/未提交**：

| 文件 | 性质 |
|---|---|
| `observability-frontend/src/pages/TopologyCatalog/`（新）| 拓扑目录页（已接 API）|
| `observability-frontend/src/components/topology/`（新）| 拓扑图谱组件（视觉修复）|
| `observability-frontend/src/App.tsx`（改）| 注册 `/topology/catalog` 路由 |
| `observability-frontend/src/api/client.ts`（改）| 新增 topology 系列 API |
| `observability-frontend/src/pages/Topology/index.tsx`（改）| 拓扑页重写（+385/-513）|
| `ai-apm-query-go/...topology_graph.go`（新）| SyncTopologyCatalog + TopologyNodesRouter |
| `ai-apm-query-go/...store/topology.go`（新）| TopologyNodeDAO + seed |
| `cmd/api/main.go`、`store/mysql.go`（改）| 注册拓扑路由 + 建拓扑表 |

**建议**：这些改动已充分验证过（拓扑专项已完成视觉修复），应先 `git add + commit` 固化，再重建部署前端镜像，即可消除审计报告的 P0 空白页观感。

---

## 六、环境约束（影响实施）

| 约束 | 说明 |
|---|---|
| Python 版本 | 本机仅 Python 3.9.6；ai-orchestrator 依赖（crewai>=1.0/chromadb）需 >=3.10，全栈无法本地解析 |
| langgraph 冲突 | langgraph>=0.2.23 需 langchain-core>=0.2.38+，与 langchain-core 0.1.x 冲突；已 pin `langgraph>=0.2.23,<0.3` |
| orchestrator 不可改 | 按 brief，`orchestrator.py` 不得修改 |
| 文件删除 | IDE safe-delete 拦截 shell `rm` 大目录，须用 delete_file 工具 |

---

## 七、对齐后的建议行动优先级

1. **【0 成本】提交 + 部署前端**：固化未提交改动 → 重建前端镜像 → 审计 P0 空白页观感消失（最大感知提升，几乎零开发）
2. **【前置】VM 采集链路（Task 1）**：部署 vmagent + scrape_configs（node-exporter/ipmi-exporter/ingest）→ 监控面板/告警时序/设备指标有数据
3. **【告警闭环】Task 2/3/4**：5 表建库 + 全类型评估 + incident/timeline/webhook
4. **【权限】Task 5**：user scope + RequireScope + JWT claim
5. **【日志/AI】Task 7 日志聚合 + Task 8 incident 工具**

---

*本清单基于 2026-08-08 工作区代码实际状态生成，用于校正既有审计/进度文档。后续开发应以本清单为准确认待办。*
