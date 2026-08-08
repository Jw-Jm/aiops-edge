# Topology UX Optimization (P0-P3) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让不懂可观测的技术人员能一眼看懂服务拓扑页，通过渐进式披露 + 自动摘要 + 去专家化默认视图，覆盖 P0-P3 共 7 项优化。

**Architecture:** 纯前端改动，集中在 2 个文件：`TopologyGraph.tsx`（图谱展示组件）+ `Topology/index.tsx`（页面/工具栏/摘要/详情）。数据源复用现有 `topoListNodes/Relations/RelationTypes`、`getTopology`（trace 指标）、`getAlertEvents`（告警）。

**Tech Stack:** React, TypeScript, AntD, @xyflow/react, echarts

## Global Constraints

- 不改后端 API；全部用现有 client.ts 接口：`topoListNodes/topoListRelations/topoListRelationTypes/getTopology/getAlertEvents/getTopologyNodeDetail`
- 保留 ongrid 的 tier 分层展示风格（react-flow），不做布局重构，只做信息层简化
- 中文文案，不用专业术语（`p95`/`Apdex`/`semantics_tag` 等）；健康状态只用 正常/异常 两级（延迟慢可用"偏慢"）
- 现有 tsc/build 通过，无 lint 错误，不回归
- 节点健康映射：`error_rate > 0` → 异常(红)；否则正常(绿)。来自 `getTopology` 返回的 `error_rate`
- 层级中文名映射：service→`核心服务`、cluster→`数据存储`、app→`业务入口`、device/rack→`基础设施`

---

### Task 1: 节点简化 + 层级中文标题（P1b）

**Files:**
- Modify: `observability-frontend/src/components/topology/TopologyGraph.tsx`

**Interfaces:**
- Consumes: `TopologyNodeItem.type`（现有）
- Produces: 节点 data 新增 `tierLabel: string`；导出 `typeTierLabel(type)` 供页面使用

- [ ] **Step 1: 写失败测试**

```ts
// test: typeTierLabel 映射
import { typeTierLabel } from './TopologyGraph'
it('maps tier labels to Chinese', () => {
  expect(typeTierLabel('service')).toBe('核心服务')
  expect(typeTierLabel('cluster')).toBe('数据存储')
  expect(typeTierLabel('app')).toBe('业务入口')
  expect(typeTierLabel('unknown')).toBe('其他')
})
```

> 注：本项目无前端单测框架配置，此测试以 `tsc` 类型正确性 + 构建为准。若无法跑 Jest，则通过 `tsc --noEmit` 验证导出的函数类型正确即可。

- [ ] **Step 2: 节点文字简化**

在 `CustomTopologyNode`：名称保持 `14px` 加粗；第二行**仅保留健康点 + 错误率%（异常时红字）**，去掉延迟 `ms` 展示（归入详情页）；type 用小字 `11px` 弱化。

- [ ] **Step 3: 层级中文标题**

在组件内新增 `typeTierLabel(type)` 导出；在 dagre 布局后按 tier 分组，每层上方叠加一个居中的中文层名标签（绝对定位，`fontSize 13`，`rgba(255,255,255,0.45)`）。

- [ ] **Step 4: 运行验证**

Run: `cd observability-frontend && node_modules/.bin/tsc --noEmit && npm run build`
Expected: exit 0

- [ ] **Step 5: 提交**

```bash
git add observability-frontend/src/components/topology/TopologyGraph.tsx
git commit -m "feat(topology): 节点简化 + 层级中文标题"
```

---

### Task 2: 只看异常 + 故障链路标红（P1a）

**Files:**
- Modify: `observability-frontend/src/pages/Topology/index.tsx`
- Modify: `observability-frontend/src/components/topology/TopologyGraph.tsx`

**Interfaces:**
- Consumes: `nodeMetrics`（页面 state，来自 getTopology）
- Produces: TopologyGraph 新增 Props `onlyAbnormal?: boolean`、`abnormalNames?: Set<string>`

- [ ] **Step 1: 页面加"只看异常"开关**

新增 state `onlyAbnormal`，工具栏加 `<Button type={onlyAbnormal?'primary':'default'} size="small">只看异常</Button>`。异常节点 = `nodeMetrics[name].error_rate > 0` 或 `health !== 'healthy'`。

- [ ] **Step 2: TopologyGraph 接收 onlyAbnormal + abnormalNames**

在 `layoutGraph`：若 `onlyAbnormal`，`visibleNodes` 过滤为异常节点 + 其直接邻居（保留链路上下文）；异常节点边框用红色（覆盖类型色）。

- [ ] **Step 3: 故障链路标红**

`layoutGraph` 中，`error_rate > 0` 的节点（`abnormalNames` 含）边框 `#ff4d4f` + 阴影 `0 0 12px rgba(255,77,79,0.6)`；其关联边颜色改为红色 `#ff4d4f`（覆盖 semantics 色）。

- [ ] **Step 4: 运行验证**

Run: `cd observability-frontend && node_modules/.bin/tsc --noEmit && npm run build`
Expected: exit 0

- [ ] **Step 5: 提交**

```bash
git add observability-frontend/src/pages/Topology/index.tsx observability-frontend/src/components/topology/TopologyGraph.tsx
git commit -m "feat(topology): 只看异常 + 故障链路标红"
```

---

### Task 3: 顶部自动摘要条（P0b，最高价值）

**Files:**
- Modify: `observability-frontend/src/pages/Topology/index.tsx`

**Interfaces:**
- Consumes: `nodeMetrics`（error_rate）、`getAlertEvents({limit:10})`
- Produces: `summaries: SummaryItem[]`（`{severity, service, message}`）

- [ ] **Step 1: 拉取告警数据**

`fetchCatalog` 中并行调用 `getAlertEvents({ limit: 10 }).catch(()=>null)`，存入 state `alerts`。

- [ ] **Step 2: 推导人话摘要**

新增纯函数 `buildSummary(alerts, nodeMetrics, relations, nodes)`，返回数组：
- 若有 `error_rate > 0` 的异常节点：`{severity:'warning', service:name, message:'错误率升高（X%）'}`，并统计其下游受影响服务数
- 若有 `status==='firing'` 的告警：`{severity, service: rule.service, message}`

- [ ] **Step 3: 渲染摘要条**

图谱上方渲染条件色 Alert 条（AntD `<Alert>` 或自定义条）：
- 有异常：红色/橙色条 `⚠️ 过去5分钟 {service} 错误率升高，影响 {n} 个下游服务`
- 无异常：绿色条 `✅ 系统运行正常（{nodes.length} 个服务）`
- 全部中文，一句话

- [ ] **Step 4: 运行验证**

Run: `cd observability-frontend && node_modules/.bin/tsc --noEmit && npm run build`
Expected: exit 0

- [ ] **Step 5: 提交**

```bash
git add observability-frontend/src/pages/Topology/index.tsx
git commit -m "feat(topology): 顶部自动摘要条（人话告警+影响面）"
```

---

### Task 4: 点击节点人话详情（P2a）

**Files:**
- Modify: `observability-frontend/src/pages/Topology/index.tsx`

**Interfaces:**
- Consumes: `getTopologyNodeDetail(name)`（现有）、`nodeMetrics`、`relations`、`nodes`
- Produces: 详情抽屉顶部新增"一句话 + 上下游"区块

- [ ] **Step 1: 推导一句话状态**

新增纯函数 `describeNode(node, metrics, relations, nodes)`：
- 状态词：`error_rate>0 ? '异常' : '正常'`
- 一句话：`"{name} 当前状态：{状态}；错误率 {x}%，平均响应 {y}ms"`（无数据则 `"暂无实时指标"`）
- 上下游：统计调用它的（作为 dst）+ 它调用的（作为 src）节点名列表

- [ ] **Step 2: 渲染在抽屉顶部**

详情抽屉 `nodeDetail` 区块上方新增：状态徽标 + 一句话 + `调用 N 个服务 / 被 N 个服务调用`（中文）。

- [ ] **Step 3: 运行验证**

Run: `cd observability-frontend && node_modules/.bin/tsc --noEmit && npm run build`
Expected: exit 0

- [ ] **Step 4: 提交**

```bash
git add observability-frontend/src/pages/Topology/index.tsx
git commit -m "feat(topology): 节点人话详情（状态+上下游）"
```

---

### Task 5: 默认聚焦最新告警（P2b）

**Files:**
- Modify: `observability-frontend/src/pages/Topology/index.tsx`
- Modify: `observability-frontend/src/components/topology/TopologyGraph.tsx`

**Interfaces:**
- Consumes: `alerts`（Task 3 引入）、`nodeMetrics`
- Produces: TopologyGraph 新增 `focusName?: string | null`，控制初始视图

- [ ] **Step 1: 推导聚焦节点**

`fetchCatalog` 后：若存在 `error_rate>0` 的异常节点，取错误率最高者设为 `focusName`（state）。

- [ ] **Step 2: TopologyGraph 接收 focusName**

使用 `useEffect` 在 `focusName` 变化时 `setCenter(name)`（react-flow `setCenter`/`fitView`），放大该节点及其链路。无异常时不聚焦（显示全图）。

- [ ] **Step 3: 运行验证**

Run: `cd observability-frontend && node_modules/.bin/tsc --noEmit && npm run build`
Expected: exit 0

- [ ] **Step 4: 提交**

```bash
git add observability-frontend/src/pages/Topology/index.tsx observability-frontend/src/components/topology/TopologyGraph.tsx
git commit -m "feat(topology): 默认聚焦最新告警"
```

---

### Task 6: 工具栏简化成 4 个中文按钮 + 高级设置折叠（P3）

**Files:**
- Modify: `observability-frontend/src/pages/Topology/index.tsx`

**Interfaces:**
- Consumes: 现有 toolbar state（`typeFilter/hideOrphans/visibleRelTypes`）
- Produces: 简化工具栏

- [ ] **Step 1: 主工具栏只留 4 个中文按钮**

`只看异常`（Task 2）、`按业务筛选`（复用现有类型 Select，label 改中文层名）、`时间范围`（新增 Select：15分钟/1小时/24小时，控制 getTopology 分钟数）、`自动聚焦`（复用 Task 5 focus 逻辑，点击重新聚焦）。

- [ ] **Step 2: 高级设置折叠**

将 `hideOrphans`、`visibleRelTypes`（关系类型勾选）、`typeFilter`（详细类型）收进 `<Collapse ghost>` 或 Dropdown `高级设置`，默认折叠。

- [ ] **Step 3: 运行验证**

Run: `cd observability-frontend && node_modules/.bin/tsc --noEmit && npm run build`
Expected: exit 0

- [ ] **Step 4: 提交**

```bash
git add observability-frontend/src/pages/Topology/index.tsx
git commit -m "feat(topology): 工具栏简化 + 高级设置折叠"
```

---

### Task 7: 全量验证（TDD 收尾）

**Files:**
- 无新增

- [ ] **Step 1: 全量前端检查**

Run: `cd observability-frontend && node_modules/.bin/tsc --noEmit && npm run build`
Expected: exit 0, 无类型/lint 错误

- [ ] **Step 2: 部署 + 浏览器验证**

Run: 构建镜像 `docker build -t observability-frontend:latest .`，`kubectl -n observability rollout restart deploy/frontend`

验证（agent-browser）：
- 摘要条显示中文一句话（正常/异常）
- 节点只显示服务名 + 健康点，无专业指标
- 层级中文标题（核心服务/数据存储）显示
- 点击节点抽屉显示"状态一句话 + 上下游"
- 工具栏 4 个中文按钮 + 高级设置折叠

- [ ] **Step 3: 提交**

```bash
git add observability-frontend
git commit -m "chore(topology): P0-P3 UX 优化验证"
```

---

## Self-Review

**1. Spec coverage（P0-P3）：**
- ✅ P0a 节点简化 → Task 1
- ✅ P0b 自动摘要条 → Task 3
- ✅ P1a 只看异常+链路标红 → Task 2
- ✅ P1b 层级中文标题 → Task 1
- ✅ P2a 人话详情 → Task 4
- ✅ P2b 默认聚焦最新告警 → Task 5
- ✅ P3 工具栏简化+高级折叠 → Task 6

**2. Placeholder scan：** 无 TBD/TODO；Step 1 的 Jest 测试注明无单测框架时的替代验证（tsc）。

**3. Type consistency：**
- `typeTierLabel` Task1 定义 → 页面引用
- `nodeMetrics` 页面 state → TopologyGraph `metrics` prop
- `abnormalNames`/`onlyAbnormal`/`focusName` Task2/5 定义 → TopologyGraph Props
- `alerts`/`buildSummary` Task3 定义 → Task4/5 复用
- 无跨任务签名不一致

**4. 依赖顺序：** Task2/3/4/5 依赖 Task1 的 `typeTierLabel` 与现有 `nodeMetrics`；Task5 依赖 Task3 的 `alerts`。纯前端，无后端依赖。
