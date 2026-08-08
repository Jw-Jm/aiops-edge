# AIOps 平台整体进展总结（截至 2026-08-08）

> **换账号后从这里继续开发。** 本文件是 aiops 可观测平台（ongrid 复刻）的整体进展索引，涵盖：项目定位、已交付成果、ongrid 化进展、近期拓扑视觉修复、未完成项、部署与验证方法。

---

## 一、项目定位

自研云原生 AIOps 平台，**复刻 ongrid 能力面 + 页面，但不复制其技术栈**（ongrid 为 AGPL-3.0，仅对标不复制，`ongrid-ref/` 已隔离不入库）。全链路自研：采集 → 存储 → AI 编排 → 可视化。

**代码仓库位置（本机）：** `/Users/mssc/Documents/Code/agent/aiops/`
- `observability-frontend/` — React + AntD + @xyflow/react + Vite
- `ai-apm-query-go/` — Go 后端（查询/告警/拓扑/目录/MySQL）
- `ai-orchestrator/` — Python AI 编排（LangGraph + RCA + 多专家 + RAG）
- `deploy/helm/aiops/` — Helm Chart 部署
- `docs/superpowers/` — 所有 spec / plan / 进展文件

**运行环境：** 本机 macOS(arm64) + OrbStack K8s 单节点，访问入口 `http://localhost:30253`

---

## 二、已交付成果（按阶段）

### Phase A — 界面地基 + 可观测（已完成 ✅）
| 交付 | 说明 |
|---|---|
| 布局壳 | Sidebar 8 区段 + zinc 色板 + Zustand + ⌘K/⌘P 命令面板 + AI 助理侧栏 |
| Dashboard | 后端 `/dashboard/stats` 聚合 + 前端 Overview（错误率/延迟，双 y 轴） |
| Monitor | PromQL 面板页（4 面板） |
| Logs / Traces | UI 对齐 zinc 基调 |
| VictoriaMetrics 接入 | VM + 通用 PromQL `query_range` 端点 |

### Phase B — AI 运维功能（大部完成 ✅）
| 交付 | 说明 |
|---|---|
| 告警 incident 状态机 | AlertEvent 状态（firing/ack/resolved）+ transitionStatus + ack/resolve/详情接口 |
| IncidentDetail | `/alerts/incidents/:id` 详情页（状态时间线 + AI RCA） |
| SSE 事件协议 | 标准 event 帧 + tool_start/tool_end/done 结构化 |
| ChatThread | `/chat/:sessionId` 独立会话线程页 |
| Skills | `/skills` 目录页 + execute_skill |
| Agents | `/agents` 卡片页 + CRUD |
| Workflows | `/workflows` 页（只读 DAG 图）+ run |
| 工具 params 全量 | 16 工具全量参数 schema |

---

## 三、ongrid 化进展

### 3.1 设计文档（已产出）
- `specs/2026-08-07-ongrid-capability-replication-design.md` — 能力复刻可行性
- `specs/2026-08-07-ongrid-page-replication-design.md` — 页面复刻设计（含合规红线）
- `specs/2026-08-08-ongrid-gap-completion-design.md` — 差距补齐设计（8 块差距）
- `plans/2026-08-08-ongrid-gap-completion.md` — 差距补齐实施计划（10 Task）

### 3.2 已落地：拓扑专项（ongrid 风格对齐）✅

**目标：** `/topology` 页面完全对齐 ongrid 展示（tier 分层 react-flow + 点击查看详情）。

**已交付：**
1. **拓扑目录体系（后端）**
   - `ai-apm-query-go/internal/store/mysql.go` + `topology.go`：4 表 schema（topology_nodes / topology_relations / topology_node_types / topology_relation_types），EnsureSchema 幂等建表
   - `SeedTopologyTypes()`：5 node_types（app/service/cluster/device/rack, tier 0-4）+ 7 relation_types
   - `topoListNodes/Relations/NodeTypes/RelationTypes` CRUD API（client.ts 已接）
   - `POST /api/v1/topology/sync-catalog`：从 ClickHouse trace_spans 按 trace_id 分组、时序建边（src→dst 调用方向，type=depends_on）

2. **ongrid 风格图谱（前端）**
   - `observability-frontend/src/components/topology/TopologyGraph.tsx`：react-flow + @dagrejs/dagre TB 分层布局
   - `observability-frontend/src/pages/Topology/index.tsx`：页面（工具栏 + 摘要条 + 人话详情 + 只看异常 + 聚焦）
   - 节点微指标（错误率/健康点）、hover 链路高亮、tier 中文标签、图例面板

3. **UX 优化（P0-P3，已用 superpowers 实施）**
   - 节点简化、自动摘要条、只看异常+故障链路红色、tier 中文标签、人话详情、默认聚焦告警、工具栏简化

4. **视觉修复（2026-08-08 完成）**
   - 节点不可见 bug（opacity 0.12）→ 修复 ✅
   - 对比度差（背景太暗）→ 提亮至 WCAG AA ✅
   - 平行边错开 → 重写为多 handle 平行通道 ✅

### 3.3 计划中但未实施（ongrid 差距补齐剩余）

> 完整计划见 `plans/2026-08-08-ongrid-gap-completion.md`

| 差距 | 状态 | 说明 |
|---|---|---|
| ① VM 采集链路 | 待办 | vmagent + scrape_configs（node-exporter/ipmi-exporter/ingest） |
| ② 告警 DB 化 + 全类型 + incident + Webhook | 部分 | incident 状态机已有；MySQL 持久化、6 类型评估、Webhook 待补 |
| ③ 用户 scope | 待办 | users 表 scope 字段 + 权限过滤 |
| ④ 设备实时指标 / WebSSH | 待办 | /devices/{id}/metrics（VM PromQL）+ xterm.js |
| ⑤ 集群事件 | 待办 | /clusters/{id}/events（kubectl get events） |
| ⑥ 拓扑目录 | **已完成** ✅ | 见 3.2 |
| ⑦ 日志聚合 | 待办 | /logs/aggregate（LogsQL 分组） |
| ⑧ AI incident 工具 | 待办 | incident_query/ack/resolve/notification_send + skill.incident |

**后续路线（Phase C/D）：** Devices、Clusters、Knowledge、Admin/RBAC、Reports、MySQL 业务库、IM 通道、SNMP、FlowEditor 可编辑引擎

---

## 四、关键文件索引

| 文件 | 职责 |
|---|---|
| `observability-frontend/src/components/topology/TopologyGraph.tsx` | 核心图谱组件（节点/边/hover/对比度/平行边） |
| `observability-frontend/src/pages/Topology/index.tsx` | 拓扑页面（工具栏/摘要/详情/异常聚焦） |
| `observability-frontend/src/api/client.ts` | 前端 API（topoList*/getTopology/getAlertEvents） |
| `ai-apm-query-go/internal/api/topology_graph.go` | 后端 sync-catalog handler |
| `ai-apm-query-go/internal/store/mysql.go` + `topology.go` | 后端 4 表 schema + DAO + seed |
| `ai-apm-query-go/internal/api/main.go` | 路由注册 |
| `docs/superpowers/plans/2026-08-08-ongrid-gap-completion.md` | 差距补齐实施计划 |
| `docs/superpowers/specs/2026-08-08-ongrid-gap-completion-design.md` | 差距补齐设计 |
| `docs/superpowers/2026-08-07-implementation-summary.md` | Phase A/B 成果汇总 |

---

## 五、部署命令（前端）

```bash
cd aiops/observability-frontend
docker build --platform linux/arm64 -t observability-frontend:latest .
kubectl -n observability rollout restart deploy/frontend
kubectl -n observability rollout status deploy/frontend --timeout=60s
```

访问：`http://localhost:30253/topology`（登录 admin/admin123）

## 六、验证方法（agent-browser）

```bash
agent-browser open "http://localhost:30253/topology?t=$(date +%s)"
# 务必加 ?t=timestamp 防止浏览器缓存旧 JS bundle
agent-browser screenshot
```

---

## 七、换账号后继续开发的入口

1. **读本文件** — 了解整体进展
2. **读 `plans/2026-08-08-ongrid-gap-completion.md`** — 选择下一个 Task 实施
3. **按 superpowers 流程**：`using-superpowers` → 对应技能（writing-plans / subagent-driven-development / systematic-debugging / verification-before-completion）
4. **视觉问题修复** — 详见 `PROGRESS-TOPOLOGY-VISUAL-2026-08-08.md`

---

## 八、未决项 / 可改进（拓扑专项）

1. 平行边 label 重叠（多条平行边共用 pairKey 只显示第一条 label）
2. MAX_PARALLEL=4 上限（超出回退第 4 槽位）
3. 节点微指标仅显示错误率%，延迟/吞吐在详情页
4. 移动端/小屏适配

---

**一句话总结：** AIOps 平台本机已完整部署运行（14 服务 + deepflow），Phase A + Phase B 大部交付；ongrid 化中**拓扑专项已完全对齐并视觉修复完成**，其余 7 块差距（VM 采集/告警 DB 化/用户 scope/设备指标/集群事件/日志聚合/AI incident 工具）按 `plans/2026-08-08-ongrid-gap-completion.md` 待实施。
