# AIOps Task-Flow UI Redesign Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 将 AIOps 前端重构为以“发现问题 → 定位原因 → 处置 → 验证”为主线的简洁任务流界面，同时保留全部真实后端能力、安全边界和历史深链兼容。

**Architecture:** 保持 Query API 与现有事实 DTO 为数据权威，在前端新增纯展示映射和统一页面壳层；路由、作用域、数据可信度、状态生命周期与 AI 上下文分别由独立模块负责。旧页面只作为能力迁移来源，新的总览、问题、资源、调查、处置和 AI 抽屉使用统一组件合同，所有生产状态均来自真实接口。

**Tech Stack:** React 18、TypeScript 5.6、React Router 6、TanStack Query 5、Ant Design 5、Zustand 5、Vitest 2、Testing Library、现有真实浏览器验收工具。

**Spec:** [AIOps 任务流 UI 重设计规范](../specs/2026-09-12-aiops-task-flow-ui-redesign.md)

## Global Constraints

- 一级任务导航固定为总览、问题、资源、调查、处置；AI 使用上下文抽屉。
- 全平台总览与集群工作区严格分离。
- 生产构建禁止回退到 demo、fixture、随机数、静态数组或硬编码成功状态。
- 任一页面一级页签不超过四项。
- 任一首屏表格可见列不超过六列。
- 任一页面只有一个视觉主动作。
- 首屏不显示完整 UUID、原始 ISO 时间、warning code 或未本地化错误。
- AI 只能创建调查草稿或动作建议，实际处置继续经过能力校验、预检、审批和审计。
- 1440×900、1280×720、1024×768 和 1024×900 无不可控横向溢出、遮挡或文字截断。
- 实现完成后必须使用全新 `RUN_ID` 执行 `docs/superpowers/plans/2026-09-11-aiops-full-acceptance-agent-plan.md`；不得复用历史 PASS。

## File and ownership map

- `observability-frontend/src/layout/navConfig.ts`：五个一级工作区、辅助入口、canonical 路由和旧深链映射。
- `observability-frontend/src/layout/routeContext.ts`：从 pathname 得到全平台、集群或系统作用域以及当前工作区。
- `observability-frontend/src/App.tsx`：路由注册、权限门禁和壳层装配，不负责业务展示映射。
- `observability-frontend/src/features/scope/ScopeBar.tsx`：按路由上下文显示“全平台”“系统范围”或真实集群。
- `observability-frontend/src/components/ui/PageKit.tsx`：页面标题、开放分区、状态徽标和紧凑事实区。
- `observability-frontend/src/components/ui/DataTrustBanner.tsx`：一个页面至多一个数据可信度提示。
- `observability-frontend/src/components/ui/RelativeTime.tsx`：首屏相对时间与可访问绝对时间。
- `observability-frontend/src/lib/presentation.ts`：错误、技术标识、规则表达式和状态的纯展示映射。
- `observability-frontend/src/pages/Overview/*`：全平台与集群总览的任务优先布局。
- `observability-frontend/src/pages/Problems/*`：问题列表、问题展示映射和证据抽屉。
- `observability-frontend/src/pages/Observe/index.tsx`：旧观测入口兼容，不再承载七个同级页签。
- `observability-frontend/src/pages/Resources/*`、`src/components/graph/*`：对齐资源表、详情和关系图。
- `observability-frontend/src/pages/investigation/*`：三个调查队列和结论优先列表。
- `observability-frontend/src/pages/Actions/*`：单一动作队列和五阶段生命周期。
- `observability-frontend/src/components/AiDock.tsx`、`src/pages/ai/AiChat.tsx`、`src/pages/Assistant/*`：上下文 AI 抽屉、完整会话与结构化回答。
- `observability-frontend/src/store/uiStore.ts`：AI 抽屉的瞬时页面上下文；持久化时排除敏感上下文。
- `observability-frontend/src/pages/admin/AdminHome.tsx`：系统范围与需要处理的问题。
- `tests/manual-e2e/ia-task-flow-v4.js`：真实浏览器路由、层级、响应式和主链路断言。

---

### Task 1: 固化五工作区导航与路由作用域

**Files:**
- Modify: `observability-frontend/src/layout/navConfig.ts`
- Modify: `observability-frontend/src/layout/navConfig.test.ts`
- Create: `observability-frontend/src/layout/routeContext.ts`
- Create: `observability-frontend/src/layout/routeContext.test.ts`
- Modify: `observability-frontend/src/features/scope/ScopeBar.tsx`
- Modify: `observability-frontend/src/features/scope/ScopeBar.test.tsx`
- Modify: `observability-frontend/src/App.tsx`
- Modify: `observability-frontend/src/App.test.tsx`

**Interfaces:**
- Consumes: `authScope.activeClusterId`、`visiblePrimaryNav(role)`、现有 `ClusterRouteGate`。
- Produces: `Workspace`、`PageScopeMode`、`routeContext(pathname)`、`workspacePath(workspace, clusterId?)` 和五项主导航。

- [ ] **Step 1: 写导航与作用域失败测试**

```ts
expect(PRIMARY_NAV.map((item) => item.label)).toEqual(['总览', '问题', '资源', '调查', '处置'])
expect(routeContext('/overview')).toEqual({ scopeMode: 'global', workspace: 'overview' })
expect(routeContext('/admin')).toEqual({ scopeMode: 'system', workspace: null })
expect(routeContext('/clusters/c-1/problems')).toEqual({ scopeMode: 'cluster', workspace: 'problems' })
expect(workspacePath('problems', 'c-1')).toBe('/clusters/c-1/problems')
expect(workspacePath('overview')).toBe('/overview')
```

- [ ] **Step 2: 运行定向测试并确认旧八项导航失败**

Run: `cd observability-frontend && npm test -- --run src/layout/navConfig.test.ts src/layout/routeContext.test.ts src/features/scope/ScopeBar.test.tsx src/App.test.tsx`

Expected: FAIL，显示旧导航仍包含“集群、助手、知识、报告、系统管理”，或 `routeContext` 尚不存在。

- [ ] **Step 3: 实现路由纯函数**

```ts
export type Workspace = 'overview' | 'problems' | 'resources' | 'investigations' | 'actions'
export type PageScopeMode = 'global' | 'cluster' | 'system'

export interface RouteContext {
  scopeMode: PageScopeMode
  workspace: Workspace | null
}

export function routeContext(pathname: string): RouteContext {
  if (pathname === '/overview' || pathname === '/') return { scopeMode: 'global', workspace: 'overview' }
  if (pathname.startsWith('/admin')) return { scopeMode: 'system', workspace: null }
  const match = pathname.match(/^\/clusters\/[^/]+(?:\/([^/]+))?/)
  const segment = match?.[1]
  const workspace = segment === 'problems' ? 'problems'
    : segment === 'resources' || segment === 'graph' ? 'resources'
      : segment === 'investigations' ? 'investigations'
        : segment === 'actions' ? 'actions'
          : 'overview'
  return { scopeMode: 'cluster', workspace }
}
```

- [ ] **Step 4: 注册 canonical 路由和兼容重定向**

在 `App.tsx` 注册 `/clusters/:clusterId/problems` 和 `/clusters/:clusterId/problems/explore`。旧 `/clusters/:clusterId/observe`、`/observe`、`/alerts/*`、`/observability/log`、`/observability/trace` 与 `/changes` 必须通过保留查询参数的重定向进入 problems；旧 `/clusters/:clusterId/graph` 进入 resources graph 视图。`ScopeBar` 根据 `routeContext` 在全平台和系统页面不显示集群必选警告。

- [ ] **Step 5: 运行导航测试和构建**

Run: `cd observability-frontend && npm test -- --run src/layout src/features/scope/ScopeBar.test.tsx src/App.test.tsx && npm run build`

Expected: PASS；全平台总览显示“全平台”，系统管理显示“系统范围”，集群工作区显示真实集群名且路由高亮唯一正确。

- [ ] **Step 6: 提交路由壳层**

```bash
git add observability-frontend/src/layout observability-frontend/src/features/scope/ScopeBar.tsx observability-frontend/src/features/scope/ScopeBar.test.tsx observability-frontend/src/App.tsx observability-frontend/src/App.test.tsx
git commit -m "feat(ui): establish task-flow navigation"
```

### Task 2: 建立简洁视觉组件与展示映射

**Files:**
- Modify: `observability-frontend/src/theme/tokens.ts`
- Modify: `observability-frontend/src/theme/tokens.test.ts`
- Modify: `observability-frontend/src/components/ui/PageKit.tsx`
- Create: `observability-frontend/src/components/ui/PageKit.test.tsx`
- Create: `observability-frontend/src/components/ui/DataTrustBanner.tsx`
- Create: `observability-frontend/src/components/ui/DataTrustBanner.test.tsx`
- Create: `observability-frontend/src/components/ui/RelativeTime.tsx`
- Create: `observability-frontend/src/components/ui/RelativeTime.test.tsx`
- Create: `observability-frontend/src/lib/presentation.ts`
- Create: `observability-frontend/src/lib/presentation.test.ts`
- Modify: `observability-frontend/src/index.css`

**Interfaces:**
- Consumes: 现有 `ReadMeta`、API error、ISO 时间与 health/action/run 枚举。
- Produces: `DataTrustSummary`、`toDataTrustSummary(meta)`、`humanizeTechnicalError(error)`、`RelativeTime` 和开放分区组件。

- [ ] **Step 1: 写可信度、文案和时间失败测试**

```tsx
expect(toDataTrustSummary({ partial: true, stale: false, warningCodes: ['CLUSTER_DATA_PARTIAL'], generatedAt: '2026-09-12T08:00:00+08:00' })).toMatchObject({ state: 'partial', technicalCodes: ['CLUSTER_DATA_PARTIAL'] })
expect(humanizeTechnicalError(new Error('graph operation is unavailable'))).toBe('关系图暂时不可用')
render(<RelativeTime value="2026-09-12T08:00:00+08:00" now="2026-09-12T08:06:00+08:00" />)
expect(screen.getByText('6 分钟前')).toHaveAttribute('aria-label', expect.stringContaining('2026'))
```

- [ ] **Step 2: 运行组件测试并确认新合同不存在**

Run: `cd observability-frontend && npm test -- --run src/theme/tokens.test.ts src/components/ui src/lib/presentation.test.ts`

Expected: FAIL with missing `DataTrustBanner`、`RelativeTime` 或 `presentation` module。

- [ ] **Step 3: 实现纯展示类型和唯一可信度横幅**

```ts
export interface DataTrustSummary {
  state: 'ready' | 'partial' | 'stale' | 'error'
  sourceLabels: string[]
  affectedCapabilities: string[]
  lastSuccessAt?: string
  technicalCodes: string[]
}
```

`DataTrustBanner` 只渲染一条用户文案，技术码放入可展开详情；`partial/stale` 保留已有内容，`error` 提供重试，`ready` 不占空间。

- [ ] **Step 4: 收敛 token 和布局 CSS**

保持规格中的准确色值；桌面侧栏 168px、紧凑侧栏 64px、顶栏 56px、页面边距 24–28px、卡片圆角 8px。删除全局装饰渐变和非覆盖层阴影；正文只使用 14px，辅助信息不低于 11px。增加 `open-section`、`priority-list`、`compact-facts`、`operations-table` 和 `detail-drawer` 样式，不再为每个分区套卡片。

- [ ] **Step 5: 运行组件测试和构建**

Run: `cd observability-frontend && npm test -- --run src/theme src/components/ui src/lib/presentation.test.ts && npm run build`

Expected: PASS；技术错误主文案已本地化，warning code 只在技术详情出现，相对时间可访问。

- [ ] **Step 6: 提交视觉基础**

```bash
git add observability-frontend/src/theme observability-frontend/src/components/ui observability-frontend/src/lib/presentation.ts observability-frontend/src/lib/presentation.test.ts observability-frontend/src/index.css
git commit -m "feat(ui): add concise operations presentation system"
```

### Task 3: 重建平台与集群总览

**Files:**
- Modify: `observability-frontend/src/pages/Overview/index.tsx`
- Modify: `observability-frontend/src/pages/Overview/Overview.test.tsx`
- Modify: `observability-frontend/src/pages/Overview/IssueQueue.tsx`
- Modify: `observability-frontend/src/pages/Overview/WorkQueue.tsx`
- Modify: `observability-frontend/src/pages/Clusters/ClusterOverview.tsx`
- Modify: `observability-frontend/src/pages/Clusters/ClusterOverview.test.tsx`
- Create: `observability-frontend/src/pages/Overview/overviewPresentation.ts`
- Create: `observability-frontend/src/pages/Overview/overviewPresentation.test.ts`

**Interfaces:**
- Consumes: `getPlatformOverview`、`getPlatformClusters`、`getClusterOverview` 与 `DataTrustBanner`。
- Produces: `PriorityItemView`、三项事实指标、按状态排序的集群列表和不含 UID 的首屏问题标题。

- [ ] **Step 1: 写总览层级失败测试**

```tsx
expect(screen.getByRole('heading', { name: '现在最需要处理什么' })).toBeVisible()
expect(screen.getAllByTestId('overview-fact')).toHaveLength(3)
expect(screen.getAllByRole('alert')).toHaveLength(1)
expect(screen.queryByText('平台关注的资源载体')).not.toBeInTheDocument()
expect(screen.queryByText(/CLUSTER_DATA_|[0-9a-f]{8}-[0-9a-f]{4}/i)).not.toBeInTheDocument()
```

- [ ] **Step 2: 运行总览测试并确认旧卡片布局失败**

Run: `cd observability-frontend && npm test -- --run src/pages/Overview src/pages/Clusters/ClusterOverview.test.tsx`

Expected: FAIL，旧页面仍显示四个事实卡、资源载体标签或重复预警。

- [ ] **Step 3: 实现总览展示映射**

`overviewPresentation.ts` 只从 DTO 生成三项事实、优先问题和集群状态；当 `highestPriority` 标题为规则表达式时，使用资源类型、指标名和实际值生成用户标题，原始标题放入技术详情。排序固定为严重度、影响、持续时间、数据可信度。

- [ ] **Step 4: 重排平台和集群总览**

平台页面顺序固定为标题、单一可信度提示、三项事实、优先处理、集群状态。集群页面使用相同骨架，但主区是集群问题，次区是资源与基础能力异常。所有时间使用 `RelativeTime`，所有进入调查动作指向当前真实 cluster。

- [ ] **Step 5: 运行总览测试、应用测试和构建**

Run: `cd observability-frontend && npm test -- --run src/pages/Overview src/pages/Clusters src/App.test.tsx && npm run build`

Expected: PASS；`/overview` 不依赖活动 cluster，`/clusters/:clusterId` 不显示完整 UUID，空态与错误态不伪造健康数据。

- [ ] **Step 6: 提交总览重建**

```bash
git add observability-frontend/src/pages/Overview observability-frontend/src/pages/Clusters
git commit -m "feat(ui): rebuild task-first operations overview"
```

### Task 4: 将观测中心重建为问题与证据

**Files:**
- Create: `observability-frontend/src/pages/Problems/index.tsx`
- Create: `observability-frontend/src/pages/Problems/index.test.tsx`
- Create: `observability-frontend/src/pages/Problems/problemPresentation.ts`
- Create: `observability-frontend/src/pages/Problems/problemPresentation.test.ts`
- Create: `observability-frontend/src/pages/Problems/ProblemEvidenceDrawer.tsx`
- Create: `observability-frontend/src/pages/Problems/ProblemEvidenceDrawer.test.tsx`
- Modify: `observability-frontend/src/pages/Observe/index.tsx`
- Modify: `observability-frontend/src/pages/Observe/index.test.tsx`
- Modify: `observability-frontend/src/App.tsx`

**Interfaces:**
- Consumes: `getAlertAggregation`、`acceptAlertInvestigation`、现有 metrics/logs/Trace/events/changes 视图和 `ProblemSummary`。
- Produces: `ProblemRowView`、最多六列的问题表和最多四个一级证据分组。

- [ ] **Step 1: 写问题列表和证据层级失败测试**

```tsx
expect(screen.getAllByRole('columnheader').length).toBeLessThanOrEqual(6)
expect(screen.queryByRole('columnheader', { name: '集群' })).not.toBeInTheDocument()
expect(screen.getByText('服务错误率升至 31.04%')).toBeVisible()
expect(screen.queryByText(/error_rate 31\.04 > threshold 5\.00/)).not.toBeInTheDocument()
await user.click(screen.getByText('服务错误率升至 31.04%'))
expect(screen.getByRole('dialog', { name: '问题详情' })).toBeVisible()
expect(screen.getAllByRole('tab').length).toBeLessThanOrEqual(4)
```

- [ ] **Step 2: 运行问题测试并确认七页签和九列表失败**

Run: `cd observability-frontend && npm test -- --run src/pages/Observe src/pages/Problems`

Expected: FAIL，新 Problems 页面不存在，Observe 仍渲染七个同级页签。

- [ ] **Step 3: 实现问题纯映射**

```ts
export interface ProblemRowView {
  id: string
  title: string
  severity: 'critical' | 'warning' | 'info'
  impactLabel: string
  workflowState: 'needs_investigation' | 'investigating' | 'needs_decision' | 'watching'
  changedAt: string
  nextAction: 'start_investigation' | 'open_investigation' | 'review_decision' | 'open_problem'
}
```

映射不得根据缺失字段猜测影响或健康；同一集群内不显示重复集群列。下一步动作由 investigation projection 决定，不在 JSX 内重复计算。

- [ ] **Step 4: 实现问题页面与证据抽屉**

问题列表使用“问题、影响对象、状态、最近变化、操作”五列；证据抽屉分为“摘要、观测证据、变更、关联流程”四组。观测证据内部可以切换指标、日志、Trace、事件，但不形成七个页面一级页签。“数据探索”作为次级入口复用现有视图。

- [ ] **Step 5: 运行问题测试、构建和旧深链测试**

Run: `cd observability-frontend && npm test -- --run src/pages/Problems src/pages/Observe src/layout src/App.test.tsx && npm run build`

Expected: PASS；旧 `/observe` 和信号深链进入对应问题/探索页面，查询条件保留，生产接口失败不显示“暂无问题”。

- [ ] **Step 6: 提交问题中心**

```bash
git add observability-frontend/src/pages/Problems observability-frontend/src/pages/Observe observability-frontend/src/App.tsx
git commit -m "feat(ui): make problems the observability entry"
```

### Task 5: 重排资源目录与关系图

**Files:**
- Modify: `observability-frontend/src/pages/Resources/index.tsx`
- Modify: `observability-frontend/src/pages/Resources/index.test.tsx`
- Modify: `observability-frontend/src/pages/Resources/ResourceDirectory.tsx`
- Modify: `observability-frontend/src/pages/Resources/ResourceDirectory.test.tsx`
- Modify: `observability-frontend/src/pages/Resources/ResourceIdentity.tsx`
- Modify: `observability-frontend/src/components/graph/GraphExplorer.tsx`
- Modify: `observability-frontend/src/components/graph/GraphExplorer.test.tsx`
- Modify: `observability-frontend/src/components/graph/GraphToolbar.tsx`
- Modify: `observability-frontend/src/components/graph/GraphContextPanel.tsx`
- Modify: `observability-frontend/src/components/graph/GraphLegend.tsx`

**Interfaces:**
- Consumes: 现有资源目录与图谱 DTO、canonical resource UID 和 `resourceTypeLabel/resourceLocation`。
- Produces: 六列资源表、按需详情抽屉、默认折叠图例和本地化图谱错误。

- [ ] **Step 1: 写资源首屏失败测试**

```tsx
expect(screen.getAllByRole('columnheader').map((node) => node.textContent)).toEqual(['名称', '类型 / 命名空间', '健康', '活动信号', '最近变化', '操作'])
expect(screen.queryByText(resource.uid)).not.toBeInTheDocument()
await user.click(screen.getByRole('button', { name: '查看详情' }))
expect(screen.getByText(resource.uid)).toBeVisible()
expect(screen.queryByText('graph operation is unavailable')).not.toBeInTheDocument()
```

- [ ] **Step 2: 运行资源与图谱测试并确认旧堆叠布局失败**

Run: `cd observability-frontend && npm test -- --run src/pages/Resources src/components/graph`

Expected: FAIL，资源目录仍在首屏显示 UID 或图谱默认常驻多个控制面板。

- [ ] **Step 3: 实现对齐资源目录**

将搜索与类型筛选保留在资源局部工具栏；首屏只显示六列。点击行或“查看详情”后再显示 UID、labels、owner references、完整时间、原始数据和关系入口。不存在 namespace 的对象显示类型，不显示虚构 namespace。

- [ ] **Step 4: 简化图谱工作区**

图谱初始状态只显示搜索、必要过滤和主画布；选中节点后打开检查器；图例折叠；清除动作只有存在选择或筛选时才显示。错误主文案由 `humanizeTechnicalError` 生成，技术错误放入折叠详情，并提供重试和等价关系列表入口。

- [ ] **Step 5: 运行资源测试和构建**

Run: `cd observability-frontend && npm test -- --run src/pages/Resources src/components/graph src/features/resources && npm run build`

Expected: PASS；资源 canonical UID 未被复制，图谱失败不影响资源列表，键盘可打开和关闭详情。

- [ ] **Step 6: 提交资源重排**

```bash
git add observability-frontend/src/pages/Resources observability-frontend/src/components/graph
git commit -m "feat(ui): simplify resource and graph exploration"
```

### Task 6: 统一调查队列与处置生命周期

**Files:**
- Modify: `observability-frontend/src/features/workflow/statusPresentation.ts`
- Modify: `observability-frontend/src/features/workflow/statusPresentation.test.ts`
- Modify: `observability-frontend/src/pages/investigation/InvestigationCenter.tsx`
- Modify: `observability-frontend/src/pages/investigation/InvestigationCenter.test.tsx`
- Modify: `observability-frontend/src/pages/Actions/ActionCenter.tsx`
- Modify: `observability-frontend/src/pages/Actions/ActionCenter.test.tsx`
- Modify: `observability-frontend/src/pages/Actions/actionModel.ts`
- Modify: `observability-frontend/src/pages/Actions/actionModel.test.ts`

**Interfaces:**
- Consumes: 真实 Run、Action projection、capability、role、ResourceVersion 和现有审批 API。
- Produces: 三调查队列、`ActionStage`、`ActionLifecycleView` 和阶段感知的唯一主动作。

- [ ] **Step 1: 写生命周期失败测试**

```ts
expect(INVESTIGATION_QUEUES.map((queue) => queue.label)).toEqual(['需要我处理', '进行中', '已结束'])
expect(toActionLifecycle(proposedAction)).toEqual({ current: 'approval', completed: ['risk', 'preflight'], future: ['execution', 'verification'] })
expect(actionRow(proposedAction)).not.toHaveProperty('verificationStatus')
```

- [ ] **Step 2: 运行调查与处置测试并确认旧状态碎片失败**

Run: `cd observability-frontend && npm test -- --run src/features/workflow/statusPresentation.test.ts src/pages/investigation/InvestigationCenter.test.tsx src/pages/Actions`

Expected: FAIL，调查仍有“待验证”队列，动作列表仍同时显示审批、执行和验证列。

- [ ] **Step 3: 实现阶段纯映射**

```ts
export type ActionStage = 'risk' | 'preflight' | 'approval' | 'execution' | 'verification' | 'ended'

export interface ActionLifecycleView {
  current: ActionStage
  completed: ActionStage[]
  future: ActionStage[]
}
```

映射只消费服务端状态；缺字段显示“未提供”，不得把未来阶段映射为“未知”或“待验证”。

- [ ] **Step 4: 重排调查和处置页面**

调查行显示资源、当前结论/证据缺口、阶段、证据数、更新时间和唯一动作。处置列表显示动作/目标、当前阶段、风险、更新时间和操作；选择动作后显示五阶段时间线、影响、ResourceVersion、回滚和审计。批准/拒绝继续使用现有确认对话框和服务端 capability 门禁。

- [ ] **Step 5: 运行定向测试、完整 workflow 测试和构建**

Run: `cd observability-frontend && npm test -- --run src/features/workflow src/pages/investigation src/pages/Actions && npm run build`

Expected: PASS；未审批动作首屏不出现验证状态，验证只在执行后展示，禁止用户看不到的服务端写能力被前端启用。

- [ ] **Step 6: 提交流程重排**

```bash
git add observability-frontend/src/features/workflow observability-frontend/src/pages/investigation observability-frontend/src/pages/Actions
git commit -m "feat(ui): align investigation and action lifecycles"
```

### Task 7: 将 AI 重建为上下文助手

**Files:**
- Modify: `observability-frontend/src/store/uiStore.ts`
- Create: `observability-frontend/src/store/uiStore.test.ts`
- Modify: `observability-frontend/src/components/AiDock.tsx`
- Create: `observability-frontend/src/components/AiDock.test.tsx`
- Modify: `observability-frontend/src/pages/ai/AiChat.tsx`
- Modify: `observability-frontend/src/pages/ai/AiChat.test.tsx`
- Modify: `observability-frontend/src/pages/Assistant/AssistantContextPanel.tsx`
- Modify: `observability-frontend/src/pages/Assistant/AssistantAnswerCard.tsx`
- Modify: `observability-frontend/src/pages/Assistant/AssistantAnswerCard.test.tsx`
- Modify: `observability-frontend/src/api/assistant.ts`
- Modify: `observability-frontend/src/api/assistant.test.ts`

**Interfaces:**
- Consumes: 冻结 cluster/resource/time、当前问题、现有 `/ai/chat` SSE、结构化 `AssistantAnswer` 与调查草稿 API。
- Produces: `AssistantPageContext`、`openAiDock(context)`、400–440px 抽屉和人类可识别会话列表。

- [ ] **Step 1: 写上下文和安全失败测试**

```tsx
useUIStore.getState().openAiDock({ clusterId: 'c-1', clusterName: '上海集群', problemId: 'p-1', problemLabel: '服务错误率升高', from, to })
expect(useUIStore.getState().aiContext?.problemLabel).toBe('服务错误率升高')
expect(JSON.parse(localStorage.getItem('aiops-ui-v4') || '{}').state.aiContext).toBeUndefined()
expect(screen.getByText('反证与未知项')).toBeVisible()
expect(screen.queryByRole('button', { name: /直接执行/ })).not.toBeInTheDocument()
```

- [ ] **Step 2: 运行 AI 测试并确认旧浮窗/三栏布局失败**

Run: `cd observability-frontend && npm test -- --run src/store/uiStore.test.ts src/components/AiDock.test.tsx src/pages/ai/AiChat.test.tsx src/pages/Assistant src/api/assistant.test.ts`

Expected: FAIL，store 不接受上下文，AiDock 仍使用硬编码问题和 `/ai/chat` 跳转。

- [ ] **Step 3: 扩展瞬时 AI 上下文**

```ts
export interface AssistantPageContext {
  clusterId: string
  clusterName: string
  resourceUid?: string
  resourceLabel?: string
  problemId?: string
  problemLabel?: string
  from: string
  to: string
}
```

`uiStore.partialize` 只能持久化折叠偏好，必须排除 `aiContext`、resource UID、problem ID 和冻结时间。

- [ ] **Step 4: 重写抽屉、上下文和会话标题**

抽屉顶部显示集群、资源、问题和相对时间标签；发送时仍提交冻结绝对时间。回答顺序固定为结论、关键证据、反证与未知项、置信度、下一步、受控动作。完整页面把 Scope 固定栏改为按需详情；会话按今天/昨天/更早分组，标题由首问或服务端摘要生成，清空全部移入更多菜单。

- [ ] **Step 5: 验证 SSE、调查草稿和无直接执行能力**

Run: `cd observability-frontend && npm test -- --run src/api/assistant.test.ts src/pages/Assistant src/pages/ai/AiChat.test.tsx src/components/AiDock.test.tsx src/store/uiStore.test.ts && npm run build`

Expected: PASS；发送请求包含 concrete cluster 和冻结时间；partial/stale 回答显示限制；唯一写入口是创建调查草稿或动作建议。

- [ ] **Step 6: 提交上下文助手**

```bash
git add observability-frontend/src/store/uiStore.ts observability-frontend/src/store/uiStore.test.ts observability-frontend/src/components/AiDock.tsx observability-frontend/src/components/AiDock.test.tsx observability-frontend/src/pages/ai observability-frontend/src/pages/Assistant observability-frontend/src/api/assistant.ts observability-frontend/src/api/assistant.test.ts
git commit -m "feat(ui): make AI assistant context-aware"
```

### Task 8: 重排管理入口并完成静态质量门禁

**Files:**
- Modify: `observability-frontend/src/pages/admin/AdminHome.tsx`
- Create: `observability-frontend/src/pages/admin/AdminHome.test.tsx`
- Modify: `observability-frontend/src/pages/admin/AdminSettings.tsx`
- Modify: `observability-frontend/src/pages/Knowledge/index.tsx`
- Modify: `observability-frontend/src/pages/Reports/index.tsx`
- Modify: `observability-frontend/src/test/v3Acceptance.test.tsx`
- Modify: `observability-frontend/src/index.css`
- Create: `tests/manual-e2e/ia-task-flow-v4.js`

**Interfaces:**
- Consumes: 管理能力健康、接入、索引和图谱状态；现有知识与报告路由。
- Produces: 系统范围管理页、“需要处理”列表、辅助入口和自动化 UI 结构门禁。

- [ ] **Step 1: 写管理页和全局结构失败测试**

```tsx
expect(screen.getByRole('heading', { name: '需要处理' })).toBeVisible()
expect(screen.getByText('系统范围')).toBeVisible()
expect(screen.queryByRole('combobox', { name: /集群/ })).not.toBeInTheDocument()
expect(PRIMARY_NAV).toHaveLength(5)
expect(document.body.textContent).not.toMatch(/PermissionDenied|CLUSTER_DATA_|graph operation is unavailable/)
```

- [ ] **Step 2: 运行管理与验收测试并确认旧摘要首屏失败**

Run: `cd observability-frontend && npm test -- --run src/pages/admin/AdminHome.test.tsx src/test/v3Acceptance.test.tsx`

Expected: FAIL，AdminHome 尚无需要处理列表，旧验收仍要求八项导航或助手一级入口。

- [ ] **Step 3: 实现管理问题优先页和辅助入口**

把不可用组件、集群接入失败、知识索引失败和图谱运维异常合并为“需要处理”列表；每行提供一个配置或运维入口。知识与报告从侧栏底部辅助入口进入，保留角色与 cluster Scope，不在主导航增加第六项。

- [ ] **Step 4: 更新静态验收测试**

`v3Acceptance.test.tsx` 必须断言：五项主导航；无助手一级项；无七观测页签；无首屏 UUID/技术码；处置五阶段；AI 无直接执行；所有新路由仍由 `ClusterRouteGate` 保护。

- [ ] **Step 5: 实现真实浏览器任务流脚本**

`ia-task-flow-v4.js` 复用 `tests/manual-e2e/lib/laneD-runner`，在 1440×900、1280×720、1024×768 和 1024×900 逐一验证：无横向溢出、路由高亮正确、总览到调查不超过三次主操作、问题证据为二级视图、AI 抽屉上下文可见、完整 UUID 不在首屏、系统管理无全局集群选择器。脚本只读取真实数据；没有行时记录明确 empty，不创建示例行。

- [ ] **Step 6: 运行完整前端测试和构建**

Run: `cd observability-frontend && npm test -- --run && npm run build`

Expected: PASS；没有跳过或仅更新快照掩盖的失败。

- [ ] **Step 7: 提交管理页和静态门禁**

```bash
git add observability-frontend/src/pages/admin observability-frontend/src/pages/Knowledge observability-frontend/src/pages/Reports observability-frontend/src/test/v3Acceptance.test.tsx observability-frontend/src/index.css tests/manual-e2e/ia-task-flow-v4.js
git commit -m "test(ui): enforce task-flow experience"
```

### Task 9: 执行真实环境 UI 验收

**Files:**
- Read: `docs/superpowers/plans/2026-09-11-aiops-full-acceptance-agent-plan.md`
- Create at runtime: `test-results/full-acceptance/${RUN_ID}/run-manifest.json`
- Create at runtime: `test-results/full-acceptance/${RUN_ID}/case-results.jsonl`
- Create at runtime: `test-results/full-acceptance/${RUN_ID}/acceptance-report.md`
- Create at runtime: `test-results/full-acceptance/${RUN_ID}/screenshots/*`

**Interfaces:**
- Consumes: 实际部署版本、`http://localhost:30253/`、真实 API、真实 Kubernetes/KubeVirt/遥测/图谱/AI 数据。
- Produces: 绑定新 `RUN_ID` 的可追溯结果，以及唯一 `ACCEPTED` 或 `REJECTED` 结论。

- [ ] **Step 1: 完整阅读验收计划并创建全新运行清单**

按验收计划生成 `RUN_ID`、版本、时间、目标 URL、授权范围和证据目录。不得复制已有 test-results。

- [ ] **Step 2: 执行只读 UI 与真实数据对账**

逐页对照真实 API 和权威源，验证总览、问题、资源、调查、处置、AI、知识、报告和管理页；任何 Mock、fixture 或示例不能成为 PASS 证据。

- [ ] **Step 3: 执行视觉、响应式和可访问性门禁**

保存四个目标视口的逐页截图、焦点顺序、键盘操作、对比度和横向溢出证据。验证 5 秒理解测试和关键流程交互次数。

- [ ] **Step 4: 执行 AI 真实故障定位测试**

只在用户明确授权的隔离验收集群或 namespace 注入真实故障；验证冻结 Scope、证据引用、反证、置信度、调查草稿、动作建议、审批边界和动作后恢复证据。

- [ ] **Step 5: 输出发布结论**

任一关键门禁为 FAIL、BLOCKED 或 NOT_RUN 时结论必须为 `REJECTED`；只有全部关键门禁 PASS 时为 `ACCEPTED`。验收任务只测试和报告，不直接修改产品。

---

## Self-review record

- Spec coverage: 九个任务覆盖导航/作用域、视觉系统、总览、问题证据、资源图谱、调查处置、AI、管理辅助入口和真实环境验收。
- Placeholder scan: 计划不含待填实现项；每个代码任务都有失败测试、实现合同、验证命令和提交边界。
- Type consistency: `Workspace`、`PageScopeMode`、`DataTrustSummary`、`ProblemRowView`、`ActionStage`、`ActionLifecycleView` 与 `AssistantPageContext` 名称在规格和任务间一致。
- Safety coverage: 未授权前不执行真实写入、故障注入、审批、处置、删除或清理。

