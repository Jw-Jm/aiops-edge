# AIOps 平台复刻 ongrid 最终构建方案（界面 / 功能 / 框架 / 架构 四维）

> 版本：v1.0-final ｜ 日期：2026-08-07 ｜ 编制：架构组
> 状态：**已评审定稿**（资深架构师审核修订通过）
> 适用范围：本次"构建 ongrid 风格 AIOps 平台"项目的唯一权威实施依据

---

## 0. 合规红线（最高优先级）

- **ongrid 为 AGPL-3.0 许可**，本项目**全部自研实现**，仅借鉴 ongrid 的**界面设计、功能清单、架构思想**。
- **严禁复制** ongrid 的任何代码文件（Go / TSX / CSS / 脚本），包括片段级复制。
- 参考来源：已克隆 `/tmp/ongrid-src`（web 前端 201 文件 + internal/manager 后端 699 文件），仅用于**阅读理解**与**功能对标**，不落地到本仓库。

---

## 1. 背景与目标

### 1.1 为什么复刻
用户对 ongrid 的**界面观感**（深色极简、聊天驱动、状态胶囊+迷你图、工具调用可视化）高度认可，希望我们自研 AIOps 平台在**界面、功能、框架、整体架构**四个维度向其看齐。

### 1.2 我们与 ongrid 的核心差异（知己知彼）

| 维度 | ongrid | 我们自研 | 差距 |
|---|---|---|---|
| 采集 | 边缘 agent 插件化（每主机）| **DeepFlow eBPF 零侵扰** + OTLP | 我们 eBPF 更强，缺边缘主机代理 |
| 存储 | MySQL + Prometheus/Loki/Tempo + Qdrant | **ClickHouse 统一** + Victoria + Redis + ChromaDB | 我们数据仓更优，缺业务状态库 |
| AI | 图内核 ReAct + 40+ 工具 + investigator | LangGraph + CrewAI + 8 工具 + RCA 引擎 | 我们编排成熟，工具/闭环待扩 |
| 治理 | 审批中心 + 审计 + 权限分级 | shell_policy + risk_score | 缺审批中心/审计/分级 |
| 执行 | 浏览器 SSH + 零入站隧道 | kubectl/shell 工具 | 缺 WebShell/边缘执行 |
| 协作 | IM 桥接（飞书/钉钉/Slack）| 无 | 缺 IM 通道 |

---

## 2. 总体架构决策（评审后定稿）

| 决策项 | 决策 | 理由 |
|---|---|---|
| **业务持久化** | 引入 **MySQL**，定位"业务状态库"，与 ClickHouse 数据仓分离；**P1b 引入**，P0 先用 Redis 落地最小闭环 | 审批/审计/Agent/规则/报告需持久化；不拖累 P0 节奏 |
| **指标/日志/链路** | **保留 ClickHouse 统一存储**（优于 ongrid 三件套）| 关联查询强 |
| **向量库** | 保留 ChromaDB（不引入 Qdrant）| 已有 RAG 基础 |
| **AI 内核** | 保留 LangGraph（不引入 eino）| 更成熟 |
| **采集** | 保留 DeepFlow eBPF（差异化优势），edge agent 远期可选 | 不强推边缘化 |
| **前端样式** | **Tailwind + Zustand 渐进式引入**；AntD 仅复杂表格/表单 | 避免双体系冲突与存量页回归 |
| **Dashboard** | **新增独立 `/dashboard` 路由**，不与 Overview 冲突 | 保现有功能 |
| **IM 通道** | webhook → 加飞书/钉钉 | 补协作通道 |

---

## 3. 界面层复刻方案

### 3.1 ongrid 界面设计基因（复刻必须遵循）

| # | 基因 | ongrid 实现 | 复刻落地 |
|---|---|---|---|
| G1 | 深色 zinc 极简 | `bg-zinc-950` 底 / `bg-zinc-900/40` 卡片 / `border-zinc-800` 细边框 | 统一 CSS 变量：`--bg:#0a0a0a; --surface:#18181b; --border:#27272a; --text-1:rgba(255,255,255,.9); --text-2:rgba(255,255,255,.55)` |
| G2 | 聊天为第一入口 | Home=大输入框+PromptCard；会话→Agent 推理→工具卡+Markdown | 重做 AIChat 为"首页 + 聊天线程"两态 |
| G3 | 状态胶囊+迷你图 | StatusPill / Sparkline / StatusRow | 新建通用组件库 |
| G4 | 工具调用可视化 | Agent 每调工具渲染 tool_card（pending→done/error）| 聊天内实时工具卡片 |
| G5 | ⌘K/⌘P+侧面板 | CommandPalette / AgentSidePanel | 全局快捷键+浮层 |

### 3.2 布局框架

```
┌──────────┬────────────────────────────┐
│ Sidebar   │  PageHeader（页标题+操作）   │
│ 7 分区菜单 │                            │
│ 折叠/隐藏  │   Outlet（Suspense 懒加载） │
│ 会话列表   │                            │
│ 用户菜单   │                            │
└──────────┴────────────────────────────┘
   ⌘K AgentSidePanel / ⌘P CommandPalette（浮层）
```

侧边栏 7 分区：
1. 顶级：首页、仪表盘
2. AI 智能：AI 诊断、Agent、技能、工作流、MCP
3. 知识库：知识库、代码仓库
4. 基础设施：设备、集群、K8s、拓扑
5. 监控告警：监控、日志、链路、告警（未确认红点）
6. 日常：任务、产物
7. 管理（仅管理员）：用户、审计、设置

### 3.3 组件库清单（新建 `src/components/`）

`StatusPill` / `Sparkline` / `StatusRow` / `PromptCard` / `MessageBubble` / `AgentBadge` / `ActionChip` / `ToolCard` / `CommandPalette` / `AgentSidePanel` / `ChatInput` / `XTerminal` / `ReportCards` / `ReportContent`

---

## 4. 功能层复刻方案（10 大功能域）

| # | 功能域 | ongrid 能力 | 现状 | 补齐动作 | 优先级 |
|---|---|---|---|---|---|
| A | AI 聊天+Agent | 工具卡片流/审批卡/40+工具/NL→查数/@mention | 🔶 有聊天（SSE progress/chunk/done），8 工具 | 加 tool 事件+审批+工具收敛 12-15 | **P0b** |
| B | Dashboard | KPI+Sparkline+趋势+告警环形图+top5 | 🔶 Overview 简单 | 新增独立 /dashboard + 聚合接口 | **P1d** |
| C | 告警 | 多规则+预览+incident 详情+根因报告+ack/resolve/silence | 🔶 有规则(threshold/mutation)+事件聚合 | 详情页(手动RCA)+规则类型扩展 | **P0c / P1d** |
| D | Agent/技能/工作流 | 助理库/技能目录/流程编排(React Flow) | ❌ | 技能目录(P1a)+flow引擎(P1c) | P1 |
| E | 知识库/MCP | RAG/代码索引/MCP 注册 | 🔶 有 RAG(ChromaDB) | 前端+代码索引 | **P1b** |
| F | 审批/审计 | 审批中心/审计日志 | ❌ | 新增(Redis→MySQL) | **P1b** |
| G | 基础设施/WebShell | 设备/节点/浏览器 SSH | 🔶 有 Infra | 升级+WebShell(只读优先,安全专项) | **P2a** |
| H | 报告/产物 | 报告保存/分享 | ❌ | 新增 | **P2a** |
| I | IM 桥接 | 飞书/钉钉/Slack 通知 | ❌ | 新增 | **P2b** |
| J | 设置/治理 | 健康/密钥/用户/审计 | 🔶 有 LLM/DeepFlow | 扩展 | P2 |

---

## 5. 框架层复刻方案

### 5.1 前端框架

| 维度 | ongrid | 现状 | 复刻动作 | 阶段 |
|---|---|---|---|---|
| 构建 | Vite5+React18+TS5 严格 | Vite6+React18+TS5 | 对齐严格模式 | P0a |
| 样式 | Tailwind CSS v3 | AntD token | **渐进式引入 Tailwind**（新增页用，存量页仅收敛主题 token）| P0a+ |
| 状态 | Zustand（9 store）| useState 散落 | 引入 Zustand（auth/chatSessions/ui/theme/model）| P0a |
| 路由 | react-router v6+lazy+守卫 | v6 | 补懒加载+RequireAuth | P0a |
| API 层 | `src/api/` 38 模块统一 request | axios 单文件 | 拆分为按域模块 | P0a |
| 图表 | recharts+手写 SVG | ECharts | 保留 ECharts | - |
| Markdown | react-markdown | 无 | 引入 | P0b |
| 流程画布 | @xyflow/react | 无 | 引入 | P1c |
| 终端 | xterm.js | 无 | 引入 | P2a |

### 5.2 后端框架

| ongrid | 现状 | 补齐动作 | 阶段 |
|---|---|---|---|
| 分层 biz/data/model/server | 单薄 | query-api 引 MySQL+分层 | P1b |
| 工具注册表 tools.Registry（自动生成 schema）| tools.py 手工 | 增强 skill_registry：元数据驱动自动派生 | P1a |
| AI 双内核 | LangGraph 单一 | 保留（够用）| - |
| 告警主动调查 investigator | 手动 RCA | 补告警→自动RCA | P1d |
| 审批体系（safe/mutating/dangerous 分级）| shell_policy+risk_score | 升级审批中心+分级 | P1b |
| flow 引擎 | 无 | 新增（MVP 顺序执行）| P1c |

---

## 6. 整体架构层复刻方案

### 6.1 目标架构图

```
               ┌─────────────────────────────┐
  用户 ─HTTPS─▶│  frontend (nginx SPA+TLS)    │
               └──────────┬──────────────────┘
                          │ /api 反代
               ┌──────────▼──────────────────┐
               │  query-api (Go) :8080         │◀─ MySQL(新增, P1b)
               │ 查询/告警/K8s/设置/审批/审计      │
               └───┬──────┬──────┬────────────┘
                   │      │      │
   ┌───────────────┘      │      └───────────────┐
   ▼                      ▼                      ▼
┌──────────┐     ┌────────────┐          ┌──────────────┐
│ clickhouse│     │ ingest (Go) │          │ ai-orchestrator│
│ (observ+df)│◀─OTLP/DeepFlow──│          │ AI 编排核心    │
└──────────┘     └────────────┘          └──────┬───────┘
┌──────────┐ ┌────────────┐ ┌──────────┐        │
│ victoria  │ │  victoria   │ │  redis    │   ChromaDB + flow
│ metrics   │ │  logs       │ │          │   + 审批 + 工具注册
└──────────┘ └────────────┘ └──────────┘
```

### 6.2 存储职责划分

| 存储 | 定位 | 数据 |
|---|---|---|
| ClickHouse | **数据仓**（时序/日志/拓扑/链路）| trace_spans / metric_service_red / service_topology / log_records / DeepFlow flow_* |
| VictoriaMetrics | 指标 | PromQL 指标 |
| VictoriaLogs | 日志 | 应用日志 |
| **MySQL（新增）** | **业务状态库** | 审批 / 审计 / Agent 定义 / 告警规则 / 报告 / flow 元数据 / 用户组织 |
| Redis | 运行时状态 | 会话 / 任务 / 缓存 |
| ChromaDB | 向量库 | 知识库 RAG |

### 6.3 分阶段架构演进

| 阶段 | 架构改动 | 交付 | 验收标准 |
|---|---|---|---|
| **P0a** | 前端基座：Zustand + 深色 token 统一 + Sidebar 重构 | 观感统一 | Sidebar 7 分区可折叠；全局深色一致；新增页用 Tailwind |
| **P0b** | AI 聊天升级：工具卡 + Agent 徽章 + PromptCard 首页 | 聊天能力 | 工具卡片流实时渲染；Esc 中断；工具收敛 12-15 |
| **P0c** | 告警详情页 + 手动 RCA 根因报告 | 告警闭环（手动）| 详情页展示根因/证据/置信度 |
| **P1a** | skills 元数据驱动 + 技能目录页 | AI 广度 | 技能可注册/列出/运行；权限分级生效 |
| **P1b** | 知识库/MCP 前端 + 代码索引 + 审批/审计（**引 MySQL**）| 治理能力 | 知识可检索；审批可批/拒并审计 |
| **P1c** | flow 引擎 MVP（顺序+手动/定时+tool/llm/notify/condition）| 自动化基础 | 可编排并运行一个工作流 |
| **P1d** | Dashboard（新增 /dashboard）+ 告警自动调查 + NL→ClickHouse SQL | 智能增强 | KPI 一致；告警自动触发 RCA |
| **P2a** | WebShell（只读优先）+ 用户管理 + 报告中心 | 安全+治理 | 终端可审计；用户可管理 |
| **P2b** | IM 通知（飞书/钉钉）+ Agent/技能/工作流完善 | 协作闭环 | 通知闭环 |
| **远期** | edge agent + frontier 隧道（可选）| 主机级运维 | 零入站端口 |

---

## 7. 资深架构师审核修订记录

> 以下为评审阶段对第一版方案的修订，**已全部采纳**，构成最终方案。

### R1【严重】MySQL 引入时机与定位
- **修订**：MySQL **不在 P0 引入**。P0 用 Redis（已有）+ 内存落地审批/审计最小闭环；**P1b 引入 MySQL**，定位"业务状态库"，与 ClickHouse 数据仓明确分离。

### R2【严重】前端 Tailwind 迁移风险
- **修订**：**渐进式**——新增页面用 Tailwind 全新开发；存量 15 页先不动，P0 仅做全局主题 token 统一（深色收敛到 ongrid 色板）。Tailwind 与 AntD 边界写入编码规范（AntD 负责表单/表格/复杂交互，Tailwind 负责布局/视觉）。

### R3【严重】工作流引擎工作量与依赖
- **修订**：flow 引擎**单列为 P1c**，前置依赖（A 聊天工具/工具注册/D 技能/notify）必须先行。**MVP 收缩**：顺序执行 + 手动/定时触发 + tool/llm/notify/condition 四类节点；agent/http/transform 后置。

### R4【中等】工具数量与 NL 查询范围
- **修订**：P0 工具**收敛到 12-15 个确定性工具**（已有 8 个 + 告警/日志/主机负载查询）。**NL→PromQL 降级为 P1b**，且先做 **NL→ClickHouse SQL/LogQL**（我们数据在 CH，不强行上 PromQL）。

### R5【中等】告警根因报告依赖
- **修订**：P0 先做**告警详情页 + 手动触发 RCA**（复用 `rcaAlertAnalysis` 接口）；**自动触发调查（investigator）降级到 P1d**，与告警评估循环一起实现。

### R6【中等】WebShell 安全边界
- **修订**：WebShell 标注**高优先级安全专项**，配套 JWT 鉴权 + 会话审计 + shell_policy 命令白名单（read-only 优先）+ 超时与并发限制。无明确安全负责人则**延后 P2a**，先做"只读诊断模式"。

### R7【低】Dashboard 与 Overview 关系
- **修订**：**保留 Overview**（总览首页），**新增独立 `/dashboard` 路由**对标 ongrid Dashboard，Sidebar 顶级加入口。两者并存。

### R8【低】验收与回滚机制
- **修订**：每阶段明确**验收标准**（见 6.3）+ **回滚方案**（前端镜像版本回退、query-api/orchestrator deployment 回滚、MySQL 变更用版本化迁移）。

---

## 8. 工程规范（编码约束）

### 8.1 前端
- Tailwind 仅用于**新增页面与布局视觉**；AntD 负责复杂表单/表格/弹窗/交互组件。
- 状态管理统一走 Zustand（auth/chatSessions/ui/theme/modelSelection），存量 useState 可渐进迁移。
- 路由统一 `lazy` 懒加载 + `RequireAuth`/`PublicOnly` 守卫。
- API 层按域拆分为独立模块（`src/api/` 下），统一封装 `request()`。

### 8.2 后端
- 数据仓（ClickHouse）与业务状态库（MySQL）**职责分离**，不混用。
- AI 工具统一走 `skill_registry` 元数据注册，自动派生 LLM schema / HTTP API / UI 表单 / 权限门 / 审计。
- 工具调用通过 SSE 事件（tool_start/tool_end）向前端透出，支撑工具卡片流。
- 命令执行必须过 `shell_policy` 白名单 + 权限分级（safe/mutating/dangerous）。

### 8.3 安全
- 所有写操作（命令执行、规则修改、审批放行）必须可审计。
- WebShell / 边缘执行默认只读，写操作需审批。

---

## 9. 参考（仅对标，不复制）

- ongrid 源码（已克隆 `/tmp/ongrid-src`）：用于阅读理解界面、功能、架构。
- ongrid 官方文档 ongrid.cloud（部分页面 403 时以源码为准）。
- 自研平台源码：`ai-apm-query-go` / `ai-apm-ingest-go` / `ai-orchestrator` / `observability-frontend`。

---

## 10. 修订历史

| 版本 | 日期 | 变更 |
|---|---|---|
| v1.0 | 2026-08-07 | 初稿：四维完整方案 |
| **v1.0-final** | 2026-08-07 | 资深架构师审核修订（R1~R8 全部采纳），定稿 |
