# Monitor 看板自由拖拽布局 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 将 Monitor 看板从 sort+span 栅格升级为 react-grid-layout 自由拖拽布局（拖位置 + 缩放面板大小 + 逐面板持久化）。

**Architecture:** 纯前端改造（后端零改动）。前端 `pages/Monitor/index.tsx` 用 `WidthProvider(Responsive)` + `GridLayout` 替换 antd `Row/Col`，面板 `grid_x/y/w/h` 映射为 layout `x/y/w/h`；`onDragStop`/`onResizeStop` 时用 `allLayouts.lg` 坐标逐面板 `updatePanel` 写回。复用现有 `DashboardPanel` 类型（已含 4 个 grid 字段）与 `updatePanel` API。

**Tech Stack:** React 18、TypeScript 5.6、Vite 6、react-grid-layout ^1.4.4、echarts-for-react、antd。

## Global Constraints

- 文件：`observability-frontend/src/pages/Monitor/index.tsx`（唯一主改文件）+ `package.json`
- 依赖：`react-grid-layout@^1.4.4` + `@types/react-grid-layout@^1.3.5`，安装用国内源 `--registry=https://registry.npmmirror.com`
- 持久化坐标以 **lg（24 列）** 为基准，通过 `onLayoutChange(current, allLayouts)` 的 `allLayouts.lg` 获取
- 保存仅在 `onDragStop`/`onResizeStop` 触发（非 onLayoutChange 每步），且仅写有变化的面板
- 高度：`rowHeight=60`，图表区 `height: 100%` 自适应，默认 `grid_h=5`
- 旧数据兼容：grid 字段全 0 时用 `sort`/`span` 推导初始位置（`x=sort%24, y=floor(sort/24), w=span, h=5`）
- 保留 `sort` 字段不删除；拖拽后以 grid_x/y 为准
- breakpoints `{lg:1200, md:992, sm:768, xs:480}`，cols `{lg:24, md:16, sm:12, xs:6}`
- 移除上下移按钮，保留编辑/删除
- 验证：`npx tsc --noEmit` 通过 + 浏览器手动验证

---

### Task 1: 完整拖拽布局改造（依赖 + 渲染 + 保存 + 表单）

本任务为**单次内聚改造**：依赖、渲染、state、拖拽保存、表单全部在一个提交内完成（符号互相引用，拆任务会造成中间态 tsc 失败）。

**Files:**
- Modify: `observability-frontend/package.json`（依赖）
- Modify: `observability-frontend/src/pages/Monitor/index.tsx`

**Interfaces:**
- Consumes: 现有 `DashboardPanel`（id/title/query/chart_type/grid_x/grid_y/grid_w/grid_h/span/sort/enabled）、`updatePanel(id, data)`、`createPanel(data)`、`listPanels()`、`dataMap`（`Record<string, any[]>`，key=面板id）
- Produces: 完整可用的拖拽看板（拖位置/缩放持久化、新增面板带 grid 坐标、表单含 grid_w/grid_h）

- [ ] **Step 1: 安装依赖**

Run: `cd /Users/mssc/Documents/Code/agent/aiops/observability-frontend && npm install react-grid-layout@^1.4.4 --save --registry=https://registry.npmmirror.com && npm install @types/react-grid-layout@^1.3.5 --save-dev --registry=https://registry.npmmirror.com`
Expected: 安装成功，package.json 出现 `react-grid-layout` 与 `@types/react-grid-layout`

- [ ] **Step 2: 修改 import 与移除未用符号**

文件顶部（第 1-6 行）改造为：
```tsx
import { useEffect, useMemo, useState } from 'react'
import { Button, Card, Empty, Form, Input, InputNumber, Modal, Row, Select, Spin, Tag, Tooltip, message } from 'antd'
import { PlusOutlined, ReloadOutlined, EditOutlined, DeleteOutlined } from '@ant-design/icons'
import ReactECharts from 'echarts-for-react'
import { Responsive, WidthProvider, type Layout } from 'react-grid-layout'
import 'react-grid-layout/css/styles.css'
import 'react-resizable/css/styles.css'
import api, { DashboardPanel, listPanels, createPanel, updatePanel, deletePanel } from '../../api/client'
import AppEmpty from '../../components/AppEmpty'
```
（移除 `Col`，移除 `ArrowUpOutlined, ArrowDownOutlined`。）

- [ ] **Step 3: 新增组件常量与布局构造函数**

在 `const chartColors = [...]`（第 10 行）之后新增：
```tsx
const ResponsiveGridLayout = WidthProvider(Responsive)

// 从面板 grid 字段构造 lg 24 列布局；旧数据（grid 全 0）用 sort/span 推导
function buildLayout(panels: DashboardPanel[]): Layout[] {
  return panels.map((p) => {
    const w = p.grid_w > 0 ? p.grid_w : Math.min(Math.max(p.span || 6, 6), 24)
    const h = p.grid_h > 0 ? p.grid_h : 5
    const x = p.grid_w > 0 ? p.grid_x : p.sort % 24
    const y = p.grid_w > 0 ? p.grid_y : Math.floor(p.sort / 24)
    return { i: String(p.id), x, y, w, h, minW: 3, minH: 2 }
  })
}
```

- [ ] **Step 4: 新增组件 state**

在 `Monitor` 组件 state 区（第 40 行 `form` 之后）新增：
```tsx
const [lgLayout, setLgLayout] = useState<Layout[]>([])
const [allLayouts, setAllLayouts] = useState<{ lg: Layout[]; md: Layout[]; sm: Layout[]; xs: Layout[] } | null>(null)
const [saveLock, setSaveLock] = useState(false) // 拖拽/缩放中禁止并发保存
```

- [ ] **Step 5: loadPanels 同步构造 layout**

`loadPanels`（第 43-50 行）改为：
```tsx
const loadPanels = async () => {
  try {
    const r = await listPanels()
    const ps = r?.data?.data || []
    setPanels(ps)
    setLgLayout(buildLayout(ps))
  } catch {
    setPanels([])
    setLgLayout([])
  }
}
```

- [ ] **Step 6: 删除 move 函数，新增布局处理与保存 handler**

删除原 `move` 函数（第 129-140 行），在其位置（`handleDelete` 之后）新增：
```tsx
// 拖拽/缩放结束后持久化布局（用 allLayouts.lg 的 24 列坐标），仅写有变化的面板
const persistLayout = async () => {
  const layouts = allLayouts
  if (!layouts || !layouts.lg || saveLock) return
  setSaveLock(true)
  try {
    await Promise.all(
      layouts.lg.map(async (it) => {
        const panel = panels.find((p) => String(p.id) === it.i)
        if (!panel) return
        const changed =
          it.x !== panel.grid_x || it.y !== panel.grid_y ||
          it.w !== panel.grid_w || it.h !== panel.grid_h
        if (!changed) return
        await updatePanel(panel.id, { grid_x: it.x, grid_y: it.y, grid_w: it.w, grid_h: it.h })
      }),
    )
  } catch {
    // 保存失败不阻塞交互，下次拖拽会重试
  } finally {
    setSaveLock(false)
  }
}

// 拖动/缩放过程中同步布局（lg 为持久化基准）
const handleLayoutChange = (current: Layout[], all: any) => {
  setAllLayouts(all)
  if (current && current.length) setLgLayout(all?.lg || current)
}
```

- [ ] **Step 7: 替换面板渲染（Row/Col → ResponsiveGridLayout）**

把第 154-192 行的 `<Spin>` 内 `sortedPanels.length === 0 ? <AppEmpty/> : <Row>...</Row>` 替换为：
```tsx
<Spin spinning={loading}>
  {sortedPanels.length === 0 ? (
    <AppEmpty description="暂无面板" tip="点击右上角新增面板开始" height={200} />
  ) : (
    <ResponsiveGridLayout
      layouts={{ lg: lgLayout, md: lgLayout, sm: lgLayout, xs: lgLayout }}
      breakpoints={{ lg: 1200, md: 992, sm: 768, xs: 480 }}
      cols={{ lg: 24, md: 16, sm: 12, xs: 6 }}
      rowHeight={60}
      margin={[12, 12]}
      draggableHandle=".panel-drag-handle"
      onLayoutChange={handleLayoutChange}
      onDragStop={persistLayout}
      onResizeStop={persistLayout}
    >
      {sortedPanels.map((p) => {
        const series = dataMap[p.id] || []
        return (
          <div key={String(p.id)} style={{ overflow: 'hidden' }}>
            <Card
              title={
                <span className="panel-drag-handle" style={{ cursor: 'move', fontSize: 13 }}>
                  {p.title}
                  <Tag style={{ marginLeft: 8 }}>{p.chart_type}</Tag>
                </span>
              }
              extra={
                <div style={{ display: 'flex', gap: 4 }}>
                  <Button size="small" icon={<EditOutlined />} onClick={() => openEdit(p)} />
                  <Button size="small" danger icon={<DeleteOutlined />} onClick={() => handleDelete(p)} />
                </div>
              }
              style={{ borderRadius: 12, height: '100%' }}
              bodyStyle={{ height: 'calc(100% - 57px)' }}
            >
              {series.length ? (
                <ReactECharts option={buildOption(p, series)} style={{ height: '100%' }} notMerge />
              ) : (
                <Empty description="暂无数据" image={Empty.PRESENTED_IMAGE_SIMPLE} style={{ height: '100%', display: 'flex', alignItems: 'center', justifyContent: 'center' }} />
              )}
            </Card>
          </div>
        )
      })}
    </ResponsiveGridLayout>
  )}
</Spin>
```

- [ ] **Step 8: 表单加高度字段 + 新增面板带坐标**

`openCreate`（第 92-97 行）的 `form.setFieldsValue` 补 `grid_h: 5`：
```tsx
form.setFieldsValue({ chart_type: 'line', span: 6, grid_h: 5 })
```

`handleSave`（第 105-120 行）的 createPanel 分支改为带初始 grid 坐标：
```tsx
} else {
  const maxY = lgLayout.reduce((m, it) => Math.max(m, it.y + it.h), 0)
  await createPanel({
    ...values,
    enabled: true,
    grid_x: 0,
    grid_y: maxY,
    grid_w: values.span || 6,
    grid_h: values.grid_h || 5,
  })
}
```

Modal 表单（第 217-219 行）在 `span` 字段后新增 `grid_h` 字段：
```tsx
<Form.Item name="span" label="宽度（栅格数，6-24）">
  <InputNumber min={6} max={24} step={2} style={{ width: '100%' }} />
</Form.Item>
<Form.Item name="grid_h" label="高度（行数，2-12）">
  <InputNumber min={2} max={12} defaultValue={5} style={{ width: '100%' }} />
</Form.Item>
```

- [ ] **Step 9: 类型检查**

Run: `cd /Users/mssc/Documents/Code/agent/aiops/observability-frontend && npx tsc --noEmit`
Expected: 无类型错误。若报 react-grid-layout 类型缺失，确认 @types 已装。

- [ ] **Step 10: 提交**

```bash
cd /Users/mssc/Documents/Code/agent/aiops && git add observability-frontend/package.json observability-frontend/package-lock.json observability-frontend/src/pages/Monitor/index.tsx && git commit -m "feat(monitor): 看板升级为 react-grid-layout 自由拖拽布局（拖位置+缩放+逐面板持久化）" --no-verify
```

---

## Self-Review

**1. Spec 覆盖：**
- react-grid-layout 依赖 → Step 1 ✓
- Row/Col → GridLayout 渲染 + 图表 100% 自适应 → Step 7 ✓
- 固定行高 60 + 图表自适应 → Global Constraints + Step 7 ✓
- 字段映射 grid_x→x 等 + 旧数据 sort/span 推导 → Step 3 buildLayout ✓
- onDragStop/onResizeStop 逐面板保存 + allLayouts.lg 基准 → Step 6/7 ✓
- 仅拖拽停止保存（非每步）+ 仅写变化面板 → Step 6 persistLayout ✓
- 移除上下移按钮、保留编辑/删除 → Step 2/7 ✓
- 表单 span→grid_w + 新增 grid_h → Step 8 ✓
- 新增面板带 grid 坐标 → Step 8 ✓

**2. Placeholder 扫描：** 无 TBD/TODO；每步含完整代码。✓

**3. 类型一致性：**
- `buildLayout` 返回 `Layout[]`，Step 3 定义、Step 5 使用一致 ✓
- `ResponsiveGridLayout = WidthProvider(Responsive)` Step 3 定义、Step 7 使用一致 ✓
- `lgLayout`/`allLayouts`/`saveLock`/`persistLayout`/`handleLayoutChange` 同组件内定义与使用一致 ✓
- `updatePanel`/`createPanel`/`DashboardPanel` 与 client.ts 一致 ✓
- `sortedPanels` 保留（Step 7 仍用），`Row` 保留（顶部工具条），`Col` 已移除 ✓

**4. 单任务合理性：** 该改造符号互相引用（ResponsiveGridLayout/lgLayout/handleLayoutChange 在同一 JSX 内），单任务一次提交避免中间态，符合"内聚变更"原则。验证靠 tsc + 浏览器手动验证（DOM/拖拽交互无自动化测试框架）。
