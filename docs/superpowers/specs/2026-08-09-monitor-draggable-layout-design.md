# Monitor 看板自由拖拽布局（设计）

**日期**: 2026-08-09
**性质**: 设计文档（需求已与用户对齐）
**目标**: 将 Monitor 看板从"sort 排序 + span 栅格"升级为 **react-grid-layout 自由拖拽布局**（Grafana 同款），支持拖拽位置 + 缩放面板大小并持久化。
**落点**: 纯前端（后端零改动，数据层已就绪）。

---

## 0. 已对齐的关键决策

| # | 决策 | 选择 |
|---|---|---|
| 1 | 拖拽库 | **react-grid-layout**（^1.4.4，Grafana 同款，React 18 兼容） |
| 2 | 保存方式 | **逐面板保存**：拖拽/缩放停止后对变化面板逐个 `updatePanel` 写回 `grid_x/y/w/h`，复用现有 API |
| 3 | 高度方案 | **固定行高 60 + 面板内图表 100% 自适应**（拖拽缩放面板高度，echarts 填满面板） |
| 4 | 后端改动 | **零改动**（dashboard_panels 表/Go 结构体/前端类型已含 grid_x/y/w/h，updatePanel 已支持全字段写回） |

---

## 1. 现状与差距（代码实际）

| 模块 | 现状 | 差距 |
|---|---|---|
| **数据层** | `dashboard_panels` 表含 `grid_x/grid_y/grid_w/grid_h/span/sort` 列；Go `DashboardPanel` 结构体 + 前端 `DashboardPanel` 类型均已含 4 个 grid 字段；`updatePanel` PUT 全字段 Upsert，grid_w<=0 时取 span 兜底 | **已就绪，零改动** |
| **渲染** | `Monitor/index.tsx` 用 antd `Row/Col span` 栅格，`sort` 排序，`grid_x/y/w/h` **未参与渲染** | 需换为 GridLayout |
| **交互** | 上移/下移按钮（交换 sort，循环 updatePanel）| 需换成拖拽/缩放 |
| **表单** | 只有 `span` 字段（宽度栅格），无高度/grid 字段 | 需映射 grid_w + 加 grid_h |
| **依赖** | package.json 无 react-grid-layout | 需新增 + @types |

---

## 2. 范围

### 2.1 依赖（前端 package.json）
```json
"react-grid-layout": "^1.4.4",
"@types/react-grid-layout": "^1.3.5"
```
安装用国内 npm 源（`registry.npmmirror.com` 或清华镜像）。

### 2.2 渲染改造（`pages/Monitor/index.tsx`）
- `Row/Col span` → `WidthProvider(Responsive)` + `GridLayout`
- breakpoints: `{ lg:1200, md:992, sm:768, xs:480 }`，cols: `{ lg:24, md:16, sm:12, xs:6 }`（lg 24 对齐现有 span 体系）
- 面板字段映射：`grid_x→x, grid_y→y, grid_w→w, grid_h→h`，`i`=面板 id
- **旧数据兼容**（grid 字段全 0 时推导初始位置）：
  - `grid_x = sort % cols[lg]`（= sort % 24）
  - `grid_y = floor(sort / 24)`
  - `grid_w = span`（span<=0 时 6，grid_w 已由后端兜底）
  - `grid_h = 5`（默认高度行数）
- **高度**：`rowHeight=60`，默认 `grid_h=5`（≈300px 内容区）。面板内 echarts 图表用 `100%` 高度自适应填满（`style={{ height: '100%' }}` 而非固定 260px）

### 2.3 交互与保存
- **持久化坐标基准**：以 **lg 断点（24 列）** 为唯一持久化基准。`Responsive` 组件维护各断点布局，通过 `onLayoutChange(currentLayout, allLayouts)` 的 `allLayouts.lg` 获取当前布局在 lg 24 列下的坐标，作为写库值。react-grid-layout 在断点间会自动做比例/位置映射，无需手动换算。
- **保存触发**：仅 `onDragStop(layout, oldItem, newItem)` 与 `onResizeStop(...)` 触发写库（非 `onLayoutChange` 每步），避免拖动过程中频繁请求。触发时用 `allLayouts.lg` 里对应面板（按 i=面板 id）的 `x/y/w/h` 调 `updatePanel(id, { grid_x:x, grid_y:y, grid_w:w, grid_h:h })`。
- **layout 状态**：组件维护一个 `lgLayout` state（`{i, x, y, w, h}[]`），在 `onLayoutChange` 更新（供渲染与拖拽读取），写库时引用它。
- **新增面板**：`createPanel` 后，基于现有 lgLayout 最大 y 追加到下一行（`y = maxY + 1`，`x=0`, `w=span`, `h=5`），grid 字段随创建请求写入。
- **移除上下移按钮**（拖拽已替代），保留编辑/删除按钮。
- **表单**：`span` 字段映射为 `grid_w`（宽度，6-24），新增 `grid_h`（高度行数，1-12，默认 5）；编辑面板时把 `grid_w/grid_h` 回填到 span/高度字段。

### 2.4 兼容
- 保留 `sort` 字段（不删除，兼容旧数据读取），但拖拽后以 grid_y/grid_x 为准
- 新增面板默认位置：追加到最末（基于现有面板最大 y 下方或自动布局）

---

## 3. 测试（TDD / 验证）
- 前端 `tsc --noEmit` 通过
- 手动验证：拖拽移动面板 → 位置持久化（刷新后保持）；缩放面板 → 高度/宽度持久化；旧数据面板（grid 字段 0）正确推导初始位置
- 依赖安装成功（国内源）

## 4. 数据/合规
- 全自研，不复制 ongrid（ongrid-ref 也无 react-grid-layout 实现，从零引入）
- 组件最小化：仅新增 react-grid-layout 一个依赖，后端零改动
- 拖拽布局持久化到已有 dashboard_panels 的 grid 字段，无新表

## 5. 自审
- [x] 自由拖拽（react-grid-layout）+ 面板缩放
- [x] 逐面板保存（复用 updatePanel，零后端改动）
- [x] 高度 100% 自适应 + 固定行高 60
- [x] 旧数据兼容（grid 字段推导）+ sort 保留
- [x] 移除上下移按钮、表单加 grid_w/grid_h
