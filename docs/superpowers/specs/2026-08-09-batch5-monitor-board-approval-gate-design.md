# 批5：B4 Monitor 完整看板 + C1 工具分级与统一审批闸门（设计）

**日期**: 2026-08-09
**批次**: 批 5（master plan：B4 + C1）
**性质**: 设计文档（需求已与用户对齐）
**目标**: ① Monitor 看板从"硬编码占位"升级为完整可配置看板系统；② 完善工具分级 + 建统一工具执行审批闸门

---

## 0. 已对齐的关键决策

| # | 决策 | 选择 |
|---|---|---|
| 1 | B4 看板深度 | **完整看板系统**：面板 CRUD + 拖拽布局 + 多图表类型 + 持久化 |
| 2 | C1 审批闸门 | **工具分级 + 统一审批闸门**：vm_ops/automation 显式标 cls，建统一工具执行审批，复用/扩展审批中心 |

---

## 1. 现状与差距（代码实际）

| 模块 | 现状 | 差距 |
|---|---|---|
| **Monitor 看板** | `Monitor/index.tsx` 硬编码 PANELS（4 个），`<pre>` 打印 JSON，无 echarts | 无 echarts 渲染、无面板自定义、无持久化、无拖拽 |
| echarts | `echarts ^5.5.0` + `echarts-for-react ^3.0.2` 已装（Overview/Reports/Topology 用） | Monitor 未用 |
| **工具分级** | `ToolDef.cls` 默认 `"safe"`；`vm_operate`/`execute_shell` 设 `requires_approval=True` 但**未显式设 cls_` | vm_ops/automation 未标 mutating/dangerous，分级未真实生效 |
| **审批** | `Approvals` 页 + `Tasks` 页 + 恢复白名单（recovery_policy）+ 告警处置审批 | 工具执行无统一审批闸门；分级与审批未联动 |
| **看板持久化** | 无看板/面板存储 | 需面板元数据存储（MySQL） |

---

## 2. 批5 范围

### 2.1 B4 Monitor 完整看板系统

**数据模型（新表 `dashboard_panels`，query-api 拥有）**：
- `id, dashboard_id, title, query, chart_type, grid_x, grid_y, grid_w, grid_h, span, enabled, sort, created_at`
- `dashboard_id` 用于分组（默认 "default" 主看板）

**后端 API（query-api）**：
- `GET /api/v1/dashboard/panels` — 列看板面板
- `POST/PUT/DELETE /api/v1/dashboard/panels` — 面板 CRUD（含 grid 布局）
- 支持 `chart_type`: line/bar/area/gauge/table

**前端 Monitor 改造（完整看板）**：
- 用 **echarts-for-react** 渲染（复用 Overview 模式）
- 面板元数据从后端加载（非硬编码 PANELS）
- **面板自定义**：新增面板（标题/查询/图表类型）、编辑、删除
- **拖拽布局**：用轻量 grid（不引入重库，用 CSS grid + span 控制 12 栅格 + 上/下移动排序），或引入 gridstack（评估）
- **图表类型**：line（趋势线，多 series）、bar（柱状）、area（面积）、gauge（仪表）、table（表格）
- 刷新按钮 + 时间范围

**兼容**：保留默认 4 面板（作为种子数据），新面板可增删。

### 2.2 C1 工具分级 + 统一审批闸门

**工具分级补全（ai-orchestrator）**：
- `vm_ops.vm_operate`：标 `cls_="mutating"`
- `automation.execute_shell`：标 `cls_="dangerous"`
- `observability` 工具保持 `safe`（只读）
- 确保 `cls` 分级在 `ToolRegistry` 生效（tool 卡片展示 safe/mutating/dangerous 标识）

**统一工具执行审批闸门（ai-orchestrator 后端）**：
- 在工具执行入口（`ToolRegistry` 或 Agent 工具调用层）加统一闸门：
  - `cls=safe` → 直接执行（只读）
  - `cls=mutating` + `requires_approval` → 必须审批后执行（走现有审批中心/Tasks）
  - `cls=dangerous` → 强制审批 + 高等级审批人
- 复用现有恢复白名单（recovery_policy）+ 审批中心，不新建审批系统

**前端 Skills 页增强**：
- 工具卡片显示 cls 分级标签（safe/mutating/dangerous 颜色区分）

---

## 3. 测试（TDD）

- **B4**：面板 CRUD API 测试（create/update/delete/list + grid 字段持久化）；前端 tsc
- **C1**：工具分级断言（vm_operate.cls==mutating、execute_shell.cls==dangerous）；闸门逻辑测试（safe 直接执行、mutating 需审批、dangerous 强制审批）

## 4. 数据/合规

- 面板元数据归 query-api（平台配置数据）
- 全自研，不复制 ongrid
- 组件最小化：看板拖拽用 CSS grid（不引重库）；审批复用现有 Approvals/Tasks/白名单

## 5. 自审

- [x] B4 覆盖（echarts 渲染 + 面板 CRUD + 布局 + 持久化）
- [x] C1 覆盖（工具分级 + 统一审批闸门 + 前端标识）
- [x] 复用现有（echarts/Approvals/Tasks/白名单），组件最小化
- [x] 2 项关键决策已对齐
