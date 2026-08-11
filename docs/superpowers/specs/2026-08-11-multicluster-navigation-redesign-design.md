# 多集群纳管架构 + 平台 20 项设计修复方案

**日期**: 2026-08-11
**状态**: 已实施并生产部署验证
**驱动**: 用户指出"整体架构没有按照多集群纳管进行设计"，并提出 1.x 多集群纳管（5 项）+ 2.x 设计问题（19 项）共约 20 项修复需求
**已确认决策**:
1. 多集群数据采集 = **数据汇聚（中心化）**：纳管集群采集组件回传平台中心存储，各可观测表加 `cluster_id` 字段；页面集群下拉 = 按 `cluster_id` 过滤查询
2. 纳管集群采集组件 = **独立轻量采集 chart**（新建采集 agent chart，OTel collector / vmagent / 日志采集）
3. 1.5 主集群数据也统一打 `cluster_id`（固定 `default`），无空值特判
4. 1.2 回传通道 = 平台对外 Ingress/NodePort/HTTP 端点 + token 认证
5. 2.16 基础设施页合并为单页，删除原 `/infra/hardware` 路由
6. 2.17 审计日志挪到系统设置，SLO/产物中心删除
7. 2.18 报告中心列结构由我确定（含集群列 + 预览）
8. 2.4 健康度 = Apdex × 0.7 + (1-错误率) × 0.3 综合分档
9. **横切约束**：所有 UI 改动必须与现有"亮色极简"设计系统一致、自然、美观、精简（见下文"UI 设计规范"）

---

## UI 设计规范（横切约束，所有 2.x 改动强制遵循）

当前前端为 **v3.0 亮色极简**设计语言。所有 UI 改动必须复用现有设计系统，不得引入新风格：

| 约束 | 规则 |
|------|------|
| **设计语言** | 亮色极简、无玻璃拟态/紫蓝渐变；主色靛蓝 `#2f54eb` 单主色 |
| **token** | 一律用 `theme/tokens.ts` 颜色/间距/圆角 token（`var(--*)`），不写死十六进制；ECharts canvas 场景才用对应具体色值 |
| **组件复用** | 优先复用 `PageKit`（PageHeader/Breadcrumb/PaneCard/StatCard/StatusBadge/Sparkline/Empty）与 `.card`/`.page-header`/`.stat` 等既有 CSS 类，不新造组件 |
| **自然** | 布局对齐现有页面栅格与卡片间距；信息密度与现有页面一致 |
| **美观精简** | 不加多余装饰、不堆叠无意义元素；新功能保持轻量，突出数据本身 |
| **一致性** | 集群下拉、健康度、状态色、单位等所有新增元素全站统一（同一组件/同一函数/同一颜色语义） |
| **新建公共件** | 新集群下拉 `ClusterSwitcher`、健康度 `healthScore`、瀑布图等作为共享组件/工具，避免每页复制

---

## 现状盘点（探索结论）

| 维度 | 现状 |
|------|------|
| 后端多集群 API | ✅ 已具备：`clusters.go` 提供 CRUD + kubeconfig 注册 + 按集群查 nodes/namespaces/events；`client.ts` 已定义对应 API |
| 前端集群管理 UI | ❌ 缺失：AdminSettings 仅 AI 模型配置，无集群管理 Tab；InfraK8s 硬编码第一个集群、无切换下拉 |
| 全局 cluster state | ❌ 缺失：uiStore 无 cluster state，components 无集群选择器 |
| ClickHouse 表结构 | ❌ 无 `cluster_id` 字段：trace_spans / log_records / service_topology / alert_events 均无 |
| 各页面集群维度 | ❌ 全部页面（Infra/Report/Log/Trace/Capacity/Ai*）均无集群过滤/列 |

---

## 第一部分：多集群纳管架构（1.x）

### 1.1 系统设置页加"纳管集群"管理

**现状**：`AdminSettings.tsx` 只有 AI 模型配置。
**方案**：把 `AdminSettings` 改为 Tabs 多区块：
- **Tab1 AI 模型配置**（保留现状）
- **Tab2 纳管集群**（新增）：
  - 集群列表（name/provider/api_server/node_count/status/version）
  - **新增集群**表单：集群名 + 提供商 + api_server + **kubeconfig 文本框/上传** → 调已就绪的 `createCluster` API
  - 每行操作：查看节点/命名空间/事件（复用 `listClusterNodes`/`getClusterNamespaces`/`getClusterEvents`）、编辑、删除、**连接测试**
  - kubeconfig 敏感字段脱敏显示

> 前端接线：`client.ts` 的 createCluster/syncClusters/getClusterEvents 已存在，只需补 UI 表单。

### 1.2 纳管集群监控组件部署（独立轻量采集 chart）

**现状**：单中心存储（ClickHouse/VM/VL 在平台侧），无纳管集群采集。
**已确认决策**：回传通道 = 平台对外暴露的 Ingress/NodePort/HTTP 端点 + **token 认证**。
**方案**：新建 `deploy/helm/agent-collector/`（独立 chart），可部署到任意纳管集群，包含：
- **OTel Collector**（traces + 日志）→ 回传平台 ingest 的 OTLP 端点
- **vmagent**（指标抓取）→ 远程写平台 VictoriaMetrics
- 日志采集（otlp/otlphttp exporter）→ 平台 ingest
- values 参数：`platform.endpoint`（回传地址）、`platform.token`（回传认证）、`cluster_id`（写数据时打标）、`collector.traces/logs/metrics` 开关
- **认证**：ingest 入口校验 token（`X-Ingest-Token`），平台侧签发/配置 token
- 部署命令写入纳管集群页的"部署指引"（复制 helm install 命令，含 token）

**数据链路**：纳管集群 OTel/vmagent → 平台对外端点（token 认证）→ ingest → 带 `cluster_id` 写入 ClickHouse/VM。

### 1.3 所有页面加集群下拉框（全局集群切换）

**现状**：uiStore 无 cluster state，无集群选择器组件。
**方案**：
- `store/uiStore.ts` 新增：`currentClusterId`、`clusters`（列表缓存）、`setCurrentCluster/setClusters`、`refreshClusters`
- 新增组件 `components/ClusterSwitcher.tsx`：顶栏集群下拉（"全部集群" + 各集群），持久化到 localStorage
- **AppLayout 顶栏**加集群下拉，切换后：
  - 本地存储 `current_cluster_id`
  - 触发全局刷新（页面监听到变化后按 cluster 重新拉数）
- **App.tsx** 初始化时拉取集群列表填充 store
- 各数据页读取 `currentClusterId`，作为查询参数传给后端（`cluster_id=xxx` 或 `all`）

### 1.4 AI 助手默认所有集群

**现状**：AI 对话未感知集群。
**方案**：AI 上下文注入当前集群范围：
- AiChat/AiDock 发送问题时附带 `cluster_id`（默认 `all`）
- orchestrator 查询语句默认不加 cluster 过滤（=所有集群）；若用户当前选中某集群，则限定该集群
- AI 回答中展示数据来源集群

### 1.5 数据模型：可观测表加 `cluster_id` 字段

**已确认决策**：主集群（平台自身）数据也统一打标（固定 `cluster_id`，如 `default`）。所有数据都有归属，查询逻辑统一、无空值特判。
**方案**：
- `init_clickhouse.sql` 所有可观测表（trace_spans/log_records/service_topology/alert_events）加 `cluster_id String DEFAULT 'default'`，并纳入 ORDER BY（放在 tenant_id 之后）
- 后端 handler 查询统一支持 `cluster_id` 参数过滤（`cluster_id=xxx` 限定集群；`cluster_id=all` 或省略=全部）
- ingest 写入时根据上报的 `cluster_id` 打标；主集群数据默认 `default`

---

## 第二部分：19 项设计问题修复（2.x）

### 2.1 拓扑节点出框（约束加强）
**方案**：`ServiceObservability.tsx` 拓扑图 force 布局 + 环形初始坐标基础上，增加：
- 节点坐标 clamp 到画布边距（预留节点半径）
- 缩小 repulsion / 增大 gravity，防止扩散出框
- 监听 resize 重新布局

### 2.2 吞吐率单位
**方案**：统一吞吐率单位为 `rpm`（requests/min），图表/卡片/图例统一标注；若数据为 QPS 需 ×60 换算并标注来源。

### 2.3 调用次数
**方案**：拓扑边、节点详情增加"调用次数"（call_count）展示，来源 `service_topology.call_count`；节点卡片/详情表格加该列。

### 2.4 健康度依据
**已确认决策**：由我基于后端指标确定（后端 dashboard/metrics 已能提供 Apdex、错误率、延迟 p95、吞吐率）。
**方案**：健康度 = **Apdex + 错误率综合分档**：
- 前端统一 `healthScore` 函数：`score = Apdex × 0.7 + (1 - 错误率) × 0.3`（Apdex 来自后端 metrics，错误率来自统计）
- 分档：score ≥ 0.9 健康 / 0.7-0.9 亚健康 / < 0.7 异常
- 节点卡片/详情加"健康度"指标（分数 + 分档色），tooltip 展示计算依据（Apdex、错误率、阈值）
- 所有卡片共用统一函数，保证一致性

### 2.5 集群名称
**方案**：所有含集群维度的页面显示集群名称（而非 id）；拓扑/服务节点可标注所属集群；集群下拉显示友好名。

### 2.6 Trace 瀑布图
**现状**：Trace Drawer 已有简易横向色条（宽度∝耗时）。
**方案**：升级为**真树形瀑布图**：
- 按 `parent_span_id` 构建 span 树，缩进层级 + 服务名 + 操作名
- 时间轴刻度（0 起点，x 轴比例）
- 每行：耗时 ms + 状态色（error 红 / ok 绿）
- 点击 span 高亮、显示 attributes

### 2.7 Trace 搜索框
**现状**：仅"按服务过滤"下拉。
**方案**：新增文本搜索框：
- 按 trace_id / service / operation / http_url / 关键词 搜索
- 结合时间范围 + 集群下拉
- 后端 `getTraces` 增加 search 参数支持（若后端未支持，前端过滤或扩展 API）

### 2.8 日志页重设计
**现状**：LogMetrics 单一搜索框，无数据源选择。
**方案**：
- **数据源选择**：ClickHouse / VictoriaLogs 下拉（默认 ClickHouse，因 VL 无数据）
- 查询框：关键词 + 服务 + 级别(severity) + 时间范围 + 集群
- 结果表格：时间/级别/服务/内容；级别颜色 Tag
- 聚合统计 Tab 保留并支持集群过滤

### 2.9 告警事件 RCA
**现状**：AlertEvents 抽屉已调用 RCA。
**方案**：增强 RCA 展示：
- 事件详情抽屉加"根因分析"区块，展示 orchestrator RCA 结果（关联 trace/日志/变更）
- 提供"重新分析"按钮
- 根因展示结构化（可能原因 + 依据 + 建议）

### 2.10 当前/历史告警
**方案**：AlertEvents 增加状态过滤 Segmented：**当前告警**（status=firing）/ **历史告警**（resolved/firing 全部）。当前告警只显示未解决；历史显示全部 + 解决时间。

### 2.11 规则查看
**方案**：AlertRules 增强：
- 规则列表展示完整配置（条件/阈值/频率/启用状态）
- 支持"查看该规则历史告警"跳转（带 rule_id 过滤）
- 规则状态（enabled/disabled）可视化

### 2.12 任务工作台
**方案**：AiTasks 修复字段 + 增强：
- 修复"来源"字段映射（context/source）
- 增加按集群/类型过滤
- 审批操作后刷新列表

### 2.13 工作流
**方案**：AiWorkflow 修复：
- 修复"工作流运行抽屉标题 undefined"（标题用 name fallback）
- 查看抽屉展示节点序列 + 连线关系
- 运行结果反馈（成功/失败）

### 2.14 AI 工具
**方案**：AiTools 增强：
- NL2SQL / MCP / 技能目录 3 Tab 保持，增加集群维度上下文
- 工具调用结果展示数据来源集群

### 2.15 容量预测
**方案**：Capacity 增强：
- 节点选择支持按集群分组/过滤
- 预测图叠加实际曲线（当前值 vs 预测）
- 集群下拉联动

### 2.16 基础设施页合并
**已确认决策**：合并为单一"基础设施"页，删除原 `/infra/hardware` 独立路由。
**方案**：合并 `InfraK8s` + `InfraHardware` 为单一"基础设施"页：
- 顶部全局集群下拉（多集群切换，替代硬编码第一个集群）
- Tabs（AntD 可横向滚动）：集群 / 节点 / 命名空间 / 事件 / 设备资产 / SNMP / IPMI / 节点健康
- 集群下拉只影响 K8s 相关 Tab（集群/节点/命名空间/事件）；设备/SNMP/IPMI 来自 MySQL 设备表，为全局数据（标注"全部集群"）
- 导航项"基础设施 (K8s)" + "设备与硬件" 合并为单一"基础设施"入口
- `/infra/hardware` 旧路由 301 或删除，`App.tsx` 导航同步更新

### 2.17 审计SLO/产物
**已确认决策**：删除/迁移。审计日志挪到系统管理，SLO/产物中心删除。
**方案**：
- **审计日志**：迁入系统设置页（AdminSettings 新增"审计日志"Tab，复用 `/ops/audit-logs`），删除原 `/report/governance` 下的审计子 Tab
- **SLO 目标**：删除（实际不用）
- **产物中心**：删除（实际不用）
- 导航更新：`/report/governance`、`/report/artifacts` 路由移除；`App.tsx` 同步

### 2.18 报告中心命名/预览/集群/去列
**已确认决策**：列结构由我基于数据字段确定。
**方案**（基于 `/ops/reports/history` 字段：task_id/service_name/report_type/verdict/risk_score/summary/created_at）：
- **列结构**：报告（report_type 中文映射 + task_id 短码）/ 类型（Tag）/ 服务 / **集群**（新增，cluster_id→集群名）/ 风险分（百分比）/ 时间 / 操作（预览+下载）
- **命名**：标题 = `{报告类型中文} {服务} {短时间}`，如"诊断报告 order-svc 07-21 14:03"
- **去列**：去掉 verdict（不单独成列，融入预览）、summary（放预览 Drawer）、title（无此字段）
- **预览**：操作列加"预览"按钮，Drawer 渲染 summary 全文 + verdict + 元信息（markdown 渲染摘要）
- **集群**：列表支持按集群过滤（联动全局集群下拉）

---

## 影响面与工作量预估（供评审）

| 模块 | 涉及文件 | 工作量 |
|------|---------|--------|
| 多集群数据模型 | `init_clickhouse.sql`、`handler.go`、`clusters.go`、ingest | 中 |
| 前端全局集群切换 | `uiStore.ts`、`ClusterSwitcher.tsx`、`App.tsx`、各数据页 | 大 |
| 系统设置集群管理 | `AdminSettings.tsx` | 中 |
| 独立采集 chart | 新建 `deploy/helm/agent-collector/` | 中 |
| 拓扑约束 | `ServiceObservability.tsx` | 小 |
| 单位/调用次数/健康度 | 前端多页 | 小-中 |
| Trace 瀑布图 | `Trace.tsx` | 中 |
| 日志页重设计 | `LogMetrics.tsx` | 中 |
| 告警 RCA/当前历史/规则 | `AlertEvents.tsx`/`AlertRules.tsx` | 中 |
| 报告中心 | `Report.tsx` | 小-中 |
| 基础设施合并 | `InfraK8s.tsx`/`InfraHardware.tsx`/`App.tsx` 路由 | 中 |

---

## 建议实施顺序（评审通过后）

1. **地基**：1.5 数据模型 + 1.3 全局集群切换（ClusterSwitcher + uiStore）
2. **纳管闭环**：1.1 系统设置集群管理 + 1.2 采集 chart
3. **AI**：1.4 AI 默认所有集群
4. **2.x 设计修复**：按页逐个修（拓扑→Trace→日志→告警→报告→容量→基础设施合并→AI 工具/任务/工作流）
5. **验证**：部署 minikube、逐页测试、输出修复验证报告

**本文档待用户评审确认后进入 writing-plans 拆解实施计划。**
