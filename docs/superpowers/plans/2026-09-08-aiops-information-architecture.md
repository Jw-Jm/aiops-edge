# AIOps Information Architecture Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 将现有按技术模块组织的前端重构为“工作台 → 调查 → 资源 → 观测 → 处置 → 报告”的运维闭环，并建立统一 Scope、调查快照和风险可审计交互。

**Architecture:** 保留 Query API、Investigation Run、Evidence 和 Action 的现有权威接口，在 React 前端新增壳层、路由兼容层与统一 Scope 模型。业务页面按工作流重新编排，原有服务、图谱、告警、Trace、日志、K8s、硬件、容量和报告能力通过聚合入口复用，避免后端破坏性迁移。

**Tech Stack:** React 18、TypeScript 5.6、React Router 6、Zustand 5、Ant Design 5、Vitest、Testing Library、Playwright、ECharts、AntV G6

**Spec:** `docs/superpowers/specs/2026-09-08-aiops-information-architecture-design.md`

## Global Constraints

- 一级导航固定为工作台、调查、资源、观测、处置、报告；系统管理仅管理员可见。
- 最小支持宽度为 1024px；视觉验收视口为 1440×900、1280×720、1024×768。
- Cluster Scope 写入必须先成功调用 `/me/scope`，再更新本地活动 Scope。
- Investigation Scope 是创建时冻结的只读快照；打开 Run 不得改变全局活动 Scope。
- Chat 只做问答、受审计只读查询和 Investigation 草稿，不直接创建 Action 或执行环境变更。
- Run、Evidence、Action 页面不得用演示数据或前端推断替代持久化事实。
- Run/Evidence 延续 tenant + cluster + run 三元授权；Action 决策继续携带 `action_version` 和 idempotency key。
- 状态色必须同时配文字；红=严重、橙=异常、黄=风险、绿=恢复、蓝=交互/信息。
- 旧深链至少保留两个小版本并通过 React Router `Navigate` 转发。
- 不新增 UI 框架，不重写 Query API，不修改 Investigation/Action 的数据库语义。

---

## File Structure

新增或调整的文件按责任划分：

- `src/layout/AppShell.tsx`：全局侧栏、顶部栏、Scope Bar 与页面内容区域。
- `src/layout/LegacyAppShell.tsx`：迁移期保留的旧壳层，只用于 feature flag 回退。
- `src/layout/navConfig.ts`：一级导航、权限和旧路由映射；不包含 UI 状态。
- `src/features/scope/types.ts`：活动 Scope、Run Scope 与时间范围类型。
- `src/features/scope/scopeStore.ts`：活动 Scope 的唯一可写 Zustand store。
- `src/features/scope/ScopeBar.tsx`：活动 Scope 与只读 Run 快照两种呈现。
- `src/features/scope/ScopeBoundary.tsx`：根据路由选择活动或冻结 Scope。
- `src/features/investigation/draft.ts`：告警、资源、Chat 到 Investigation 草稿的纯函数。
- `src/features/investigation/components/*`：三栏调查工作台的独立组件。
- `src/pages/resources/ResourceCenter.tsx`：资源目录与详情聚合入口。
- `src/pages/observe/ObserveCenter.tsx`：告警、Trace、日志、变更和 Grafana 的聚合入口。
- `src/pages/actions/ActionCenter.tsx`：处置队列与 Action 详情。
- `src/theme/tokens.ts` 与 `src/index.css`：新版语义 Token、密度和响应式布局。
- `tests/manual-e2e/ia-*.js`：沿用仓库 Playwright harness 的三条产品主链和三档视口回归。

---

### Task 1: 建立统一 Scope 模型与级联语义

**Files:**

- Create: `observability-frontend/src/features/scope/types.ts`
- Create: `observability-frontend/src/features/scope/scopeStore.ts`
- Create: `observability-frontend/src/features/scope/scopeStore.test.ts`
- Modify: `observability-frontend/src/store/uiStore.ts`
- Modify: `observability-frontend/src/api/scopeRuntime.ts`

**Interfaces:**

- Consumes: `getMe()`、`setActiveScope(tenantId, clusterId)`、现有 `ClusterOption`。
- Produces: `ActiveScope`、`TimeRange`、`useScopeStore()`、`applyClusterScope(clusterId): Promise<boolean>`、`setNamespace(namespace)`、`setResource(resource)`、`setTimeRange(range)`。

- [ ] **Step 1: 写出级联清理和服务端成功后提交的失败测试**

```ts
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { useScopeStore } from './scopeStore'
import * as client from '../../api/client'

vi.mock('../../api/client', () => ({
  setActiveScope: vi.fn(),
  getMe: vi.fn(),
}))

describe('scopeStore', () => {
  beforeEach(() => useScopeStore.getState().reset())

  it('clears namespace and resource after cluster changes', async () => {
    useScopeStore.setState({
      clusters: [{ id: 0, cluster_id: 'cluster-b', tenant_id: 'tenant-a', name: 'prod', status: 'ready', node_count: 3 }],
      active: { tenantId: 'tenant-a', environment: 'prod', clusterId: 'cluster-a', namespace: 'payment', resource: { type: 'service', id: 'payment-api', label: 'payment-api' }, timeRange: { mode: 'relative', minutes: 60 } },
    })
    vi.mocked(client.setActiveScope).mockResolvedValue({} as never)

    expect(await useScopeStore.getState().applyClusterScope('cluster-b')).toBe(true)
    expect(useScopeStore.getState().active).toMatchObject({ clusterId: 'cluster-b', namespace: '', resource: undefined })
  })

  it('keeps the previous scope when server authorization fails', async () => {
    useScopeStore.setState({ active: { tenantId: 'tenant-a', environment: 'prod', clusterId: 'cluster-a', namespace: '', timeRange: { mode: 'relative', minutes: 60 } } })
    vi.mocked(client.setActiveScope).mockRejectedValue(new Error('forbidden'))

    expect(await useScopeStore.getState().applyClusterScope('cluster-b')).toBe(false)
    expect(useScopeStore.getState().active.clusterId).toBe('cluster-a')
  })
})
```

- [ ] **Step 2: 运行测试并确认新模块尚不存在**

Run: `cd observability-frontend && npm run test:run -- src/features/scope/scopeStore.test.ts`

Expected: FAIL，提示无法解析 `./scopeStore`。

- [ ] **Step 3: 定义稳定类型**

```ts
export type RelativeMinutes = 15 | 60 | 360 | 1440
export type TimeRange =
  | { mode: 'relative'; minutes: RelativeMinutes }
  | { mode: 'absolute'; start: string; end: string }

export interface ResourceRef { type: string; id: string; label: string }
export interface ActiveScope {
  tenantId: string
  environment: 'prod' | 'staging' | 'test' | 'dev'
  clusterId: string
  namespace: string
  resource?: ResourceRef
  timeRange: TimeRange
}

export interface RunScopeSnapshot extends ActiveScope {
  mode: 'snapshot'
  runId: string
  timeRange: { mode: 'absolute'; start: string; end: string }
}
```

- [ ] **Step 4: 实现唯一可写 Scope store**

```ts
export const useScopeStore = create<ScopeState>()(persist((set, get) => ({
  active: DEFAULT_SCOPE,
  clusters: [],
  loading: false,
  reset: () => set({ active: DEFAULT_SCOPE, clusters: [], loading: false }),
  setNamespace: (namespace) => set((state) => ({ active: { ...state.active, namespace, resource: undefined } })),
  setResource: (resource) => set((state) => ({ active: { ...state.active, resource } })),
  setTimeRange: (timeRange) => set((state) => ({ active: { ...state.active, timeRange } })),
  applyClusterScope: async (clusterId) => {
    const cluster = get().clusters.find((item) => item.cluster_id === clusterId)
    if (!cluster?.tenant_id) return false
    try {
      await setActiveScope(cluster.tenant_id, clusterId)
      set((state) => ({ active: { ...state.active, tenantId: cluster.tenant_id!, clusterId, namespace: '', resource: undefined } }))
      setScopeCluster(clusterId)
      return true
    } catch { return false }
  },
  refreshClusters: async () => {
    set({ loading: true })
    try {
      const response = await getMe()
      const clusters = (response.data?.available_clusters ?? []).map((cluster, index) => ({
        id: index, cluster_id: cluster.cluster_id || '', tenant_id: cluster.tenant_id,
        name: cluster.name, status: cluster.status || 'unknown', node_count: 0,
      }))
      set((state) => ({
        clusters,
        loading: false,
        active: clusters.some((cluster) => cluster.cluster_id === state.active.clusterId)
          ? state.active
          : { ...state.active, tenantId: '', clusterId: '', namespace: '', resource: undefined },
      }))
    } catch { set({ loading: false }) }
  },
}), { name: 'aiops-scope-v1', partialize: (state) => ({ active: state.active }) }))
```

`refreshClusters` 的具体实现必须复用 `uiStore.ts` 当前 canonical UUID 清理规则：服务端返回列表中不存在的 `clusterId` 必须置空，不得把 numeric id 或 name 作为授权上下文。

- [ ] **Step 5: 将旧 uiStore 缩减为纯 UI 状态**

移除 `currentClusterId`、`clusters`、`clusterLoading`、`setCurrentCluster`、`setClusters`、`refreshClusters`；保留 `collapsed`、`aiDockOpen` 及其动作。所有调用点改用 `useScopeStore`。

- [ ] **Step 6: 运行 Scope 与现有集群测试**

Run: `cd observability-frontend && npm run test:run -- src/features/scope/scopeStore.test.ts src/api/client.test.ts src/App.test.tsx`

Expected: PASS，且没有页面直接解析 `aiops-ui-v3` 获取 cluster。

- [ ] **Step 7: 提交**

```bash
git add observability-frontend/src/features/scope observability-frontend/src/store/uiStore.ts observability-frontend/src/api/scopeRuntime.ts observability-frontend/src/App.test.tsx
git commit -m "feat(frontend): unify operational scope state"
```

---

### Task 2: 重构全局壳层、一级导航与兼容路由

**Files:**

- Create: `observability-frontend/src/layout/navConfig.ts`
- Create: `observability-frontend/src/layout/navConfig.test.ts`
- Create: `observability-frontend/src/layout/AppShell.tsx`
- Create: `observability-frontend/src/layout/LegacyAppShell.tsx`
- Create: `observability-frontend/src/features/scope/ScopeBar.tsx`
- Create: `observability-frontend/src/features/scope/ScopeBar.test.tsx`
- Modify: `observability-frontend/src/App.tsx`
- Modify: `observability-frontend/src/index.css`

**Interfaces:**

- Consumes: `useScopeStore`、`useUIStore`、`useAuthStore`、现有通知接口。
- Produces: `PRIMARY_NAV`、`LEGACY_REDIRECTS`、`AppShell`、`ScopeBar({ snapshot? })`。

- [ ] **Step 1: 写导航权限与旧路由映射测试**

```ts
import { describe, expect, it } from 'vitest'
import { legacyTarget, visiblePrimaryNav } from './navConfig'

it('shows six workflow domains to operators', () => {
  expect(visiblePrimaryNav('operator').map((item) => item.label)).toEqual(['工作台', '调查', '资源', '观测', '处置', '报告'])
})

it('shows system administration only to admins', () => {
  expect(visiblePrimaryNav('admin').at(-1)?.label).toBe('系统管理')
})

it('keeps legacy deep links routable', () => {
  expect(legacyTarget('/observability/trace')).toBe('/observe?view=traces')
  expect(legacyTarget('/admin/approvals')).toBe('/actions')
})
```

- [ ] **Step 2: 运行测试并确认失败**

Run: `cd observability-frontend && npm run test:run -- src/layout/navConfig.test.ts`

Expected: FAIL，提示 `navConfig` 不存在。

- [ ] **Step 3: 实现声明式导航与重定向**

```ts
export const PRIMARY_NAV: NavItem[] = [
  { path: '/overview', label: '工作台', icon: 'overview' },
  { path: '/investigation', label: '调查', icon: 'chat' },
  { path: '/resources', label: '资源', icon: 'topology' },
  { path: '/observe', label: '观测', icon: 'traces' },
  { path: '/actions', label: '处置', icon: 'approvals' },
  { path: '/reports', label: '报告', icon: 'reports' },
  { path: '/admin', label: '系统管理', icon: 'settings', adminOnly: true },
]

export const LEGACY_REDIRECTS = new Map([
  ['/observability/service', '/resources?kind=service'],
  ['/observability/relationships', '/resources?view=relationships'],
  ['/observability/vms', '/resources?kind=vm'],
  ['/infra/k8s', '/resources?kind=kubernetes'],
  ['/hardware', '/resources?kind=hardware'],
  ['/capacity', '/resources?view=capacity'],
  ['/alerts/events', '/observe?view=alerts'],
  ['/alerts/rules', '/observe?view=rules'],
  ['/observability/trace', '/observe?view=traces'],
  ['/observability/log', '/observe?view=telemetry'],
  ['/changes', '/observe?view=changes'],
  ['/observability/grafana', '/observe?view=grafana'],
  ['/admin/approvals', '/actions'],
  ['/report', '/reports'],
])
```

测试遍历以上映射逐条断言，防止迁移遗漏。

- [ ] **Step 4: 写 Scope Bar 活动态与快照态测试**

```tsx
it('renders a locked run snapshot without editable selects', () => {
  render(<ScopeBar snapshot={RUN_SCOPE} />)
  expect(screen.getByText('调查快照')).toBeInTheDocument()
  expect(screen.queryByRole('combobox')).not.toBeInTheDocument()
  expect(screen.getByText('20:00–21:00')).toBeInTheDocument()
})
```

- [ ] **Step 5: 实现 AppShell 和 ScopeBar**

`AppShell` 只负责布局、导航、通知、用户菜单和 `<Outlet />`。`ScopeBar` 负责环境、cluster、namespace、resource、timeRange 控件；Run 快照通过 prop 呈现，不读取页面内部状态。

```tsx
export function ScopeBar({ snapshot }: { snapshot?: RunScopeSnapshot }) {
  if (snapshot) return <RunScopeBar snapshot={snapshot} />
  return <ActiveScopeBar scope={useScopeStore((s) => s.active)} />
}
```

- [ ] **Step 6: 将 App.tsx 收敛为路由定义**

`App.tsx` 保留 lazy imports、鉴权边界与路由。把当前壳层原样抽到 `LegacyAppShell.tsx`，新版放入 `AppShell.tsx`；使用构建变量选择，保证迁移期可回退。新页面使用 nested routes，旧路由通过 `<Navigate replace to={target} />` 保留。

```tsx
const Shell = import.meta.env.VITE_IA_V4 === 'false' ? LegacyAppShell : AppShell

<Route path="*" element={<RequireAuth><Shell /></RequireAuth>}>
  <Route path="overview" element={<Overview />} />
  <Route path="investigation/*" element={<InvestigationRoutes />} />
  <Route path="resources" element={<ResourceCenter />} />
  <Route path="observe" element={<ObserveCenter />} />
  <Route path="actions" element={<ActionCenter />} />
  <Route path="reports" element={<Report />} />
</Route>
```

- [ ] **Step 7: 验证壳层与深链**

Run: `cd observability-frontend && npm run test:run -- src/layout/navConfig.test.ts src/features/scope/ScopeBar.test.tsx src/App.test.tsx`

Expected: PASS；operator 看见 6 个业务域，admin 额外看见系统管理；旧 URL 跳转且 query 参数正确。

- [ ] **Step 8: 提交**

```bash
git add observability-frontend/src/layout observability-frontend/src/features/scope/ScopeBar.tsx observability-frontend/src/features/scope/ScopeBar.test.tsx observability-frontend/src/App.tsx observability-frontend/src/index.css
git commit -m "feat(frontend): add workflow-oriented app shell"
```

---

### Task 3: 将首页改为异常优先工作台

**Files:**

- Create: `observability-frontend/src/pages/Overview/priority.ts`
- Create: `observability-frontend/src/pages/Overview/priority.test.ts`
- Create: `observability-frontend/src/pages/Overview/IssueQueue.tsx`
- Create: `observability-frontend/src/pages/Overview/WorkQueue.tsx`
- Modify: `observability-frontend/src/pages/Overview/index.tsx`
- Modify: `observability-frontend/src/pages/Overview/Overview.test.tsx`

**Interfaces:**

- Consumes: `getDashboardStats()`、`getDashboardResources()`、`getAlertEvents()`、`listRuns()`、`listActions()`。
- Produces: `rankOperationalIssues(alerts, runs)`、`IssueQueue`、`WorkQueue`。

- [ ] **Step 1: 写异常排序和 CTA 状态测试**

```ts
it('ranks critical, wider impact, and longer duration first', () => {
  const ranked = rankOperationalIssues([
    issue({ id: 'warning', severity: 'warning', affectedServices: 1, durationMinutes: 40 }),
    issue({ id: 'critical', severity: 'critical', affectedServices: 3, durationMinutes: 20 }),
  ], [])
  expect(ranked.map((item) => item.id)).toEqual(['critical', 'warning'])
})

it('uses the run state to select the primary action', () => {
  expect(issueAction(issue({ runId: undefined }))).toEqual({ label: '开始调查', href: '/investigation/new' })
  expect(issueAction(issue({ runId: 'run-1' }))).toEqual({ label: '查看调查', href: '/investigation/run-1' })
})
```

- [ ] **Step 2: 运行测试并确认失败**

Run: `cd observability-frontend && npm run test:run -- src/pages/Overview/priority.test.ts`

Expected: FAIL，提示排序函数不存在。

- [ ] **Step 3: 实现纯函数排序与动作投影**

```ts
const SEVERITY_SCORE = { critical: 1_000_000, warning: 100_000, info: 10_000 }
export function rankOperationalIssues(items: OperationalIssue[]): OperationalIssue[] {
  return [...items].sort((a, b) =>
    score(b) - score(a) || b.startedAt.localeCompare(a.startedAt))
}
function score(item: OperationalIssue): number {
  return SEVERITY_SCORE[item.severity] + item.affectedServices * 1_000 + item.durationMinutes * 10 + (item.recentChange ? 500 : 0)
}
```

- [ ] **Step 4: 并行加载首页真实数据并保留部分失败**

使用 `Promise.allSettled`。告警失败时问题队列显示带重试按钮的来源错误；Run/Action 失败不清空其他已成功模块。禁止生成 fallback DEMO 行。

- [ ] **Step 5: 实现首屏两列布局**

左侧 `IssueQueue` 占约 68%，右侧 `WorkQueue` 占约 32%；普通 KPI、最近变更、服务风险和数据质量放在第二层。问题项主动作携带 `source=overview`、`resourceId`、`symptom`，只进入新建调查页，不直接创建 Run。

- [ ] **Step 6: 运行首页测试**

Run: `cd observability-frontend && npm run test:run -- src/pages/Overview/priority.test.ts src/pages/Overview/Overview.test.tsx`

Expected: PASS；无告警时显示真实空态；接口部分失败时成功区域仍可用。

- [ ] **Step 7: 提交**

```bash
git add observability-frontend/src/pages/Overview
git commit -m "feat(frontend): prioritize active operational issues"
```

---

### Task 4: 统一 Investigation 草稿入口与调查队列

**Files:**

- Create: `observability-frontend/src/features/investigation/draft.ts`
- Create: `observability-frontend/src/features/investigation/draft.test.ts`
- Modify: `observability-frontend/src/pages/investigation/NewInvestigation.tsx`
- Modify: `observability-frontend/src/pages/investigation/InvestigationCenter.tsx`
- Modify: `observability-frontend/src/pages/investigation/InvestigationCenter.test.tsx`
- Modify: `observability-frontend/src/pages/ai/AiChat.tsx`
- Create: `observability-frontend/src/pages/ai/AiChat.test.tsx`

**Interfaces:**

- Consumes: `ActiveScope`、`createRun()`、`listRuns()`、Chat 返回的 `__investigation_required__`/结构化 CTA。
- Produces: `InvestigationDraft`、`draftFromSearchParams()`、`draftToSearchParams()`。

- [ ] **Step 1: 写来源预填和显式提交测试**

```ts
it('round-trips a chat draft without creating a run', () => {
  const draft = { source: 'chat', clusterId: 'cluster-a', resourceId: 'payment-api', targetType: 'service', symptom: '分析错误率突增' } satisfies InvestigationDraft
  expect(draftFromSearchParams(draftToSearchParams(draft))).toEqual(draft)
})

it('does not call createRun while the new investigation page mounts', () => {
  render(<MemoryRouter initialEntries={['/investigation/new?source=chat&symptom=x']}><NewInvestigation /></MemoryRouter>)
  expect(createRun).not.toHaveBeenCalled()
})
```

- [ ] **Step 2: 运行测试并确认失败**

Run: `cd observability-frontend && npm run test:run -- src/features/investigation/draft.test.ts src/pages/ai/AiChat.test.tsx`

Expected: FAIL，提示草稿转换函数或 Chat CTA 不存在。

- [ ] **Step 3: 实现可序列化草稿**

```ts
export interface InvestigationDraft {
  source: 'overview' | 'alert' | 'resource' | 'chat'
  clusterId: string
  namespace?: string
  resourceId: string
  targetType: string
  symptom: string
}
```

仅接受 allow-list 字段，忽略未知 query 参数；`symptom` 最大 2000 字符；`clusterId` 必须通过现有 canonical UUID 校验。

- [ ] **Step 4: 修改 Chat 复杂诊断 CTA**

当 Chat 分类结果需要 Investigation 时，渲染说明、冻结范围摘要、预期证据源和按钮：

```tsx
<Button type="primary" onClick={() => navigate(`/investigation/new?${draftToSearchParams(draft)}`)}>
  转为正式调查
</Button>
```

按钮只导航，不调用 `createRun`；Chat 现有只读工具审计路径不变。

- [ ] **Step 5: 改造 NewInvestigation**

从活动 Scope 和 query 草稿预填表单。Cluster 默认值必须来自当前活动 Scope，而不是 `clusters[0]`。用户点击提交后才调用 `createRun`，并继续固定 `action_mode: 'read_only'`、`principal_type: 'user'`。

- [ ] **Step 6: 将调查中心从宽表改为状态队列**

状态视图为“需要我处理、正在调查、待验证、已结束”。每项首屏显示目标、症状、影响、持续时间、当前阶段和发起人；Run ID 使用辅助等宽文本。后端尚无 Owner/影响字段时显示“未分配/未知”，不得伪造。

- [ ] **Step 7: 运行调查入口测试**

Run: `cd observability-frontend && npm run test:run -- src/features/investigation/draft.test.ts src/pages/ai/AiChat.test.tsx src/pages/investigation/InvestigationCenter.test.tsx`

Expected: PASS；Chat、首页和资源入口均只产生草稿，只有提交按钮创建 Run。

- [ ] **Step 8: 提交**

```bash
git add observability-frontend/src/features/investigation/draft.ts observability-frontend/src/features/investigation/draft.test.ts observability-frontend/src/pages/ai/AiChat.tsx observability-frontend/src/pages/ai/AiChat.test.tsx observability-frontend/src/pages/investigation
git commit -m "feat(frontend): converge investigation entry points"
```

---

### Task 5: 实现三栏 Investigation 工作台

**Files:**

- Create: `observability-frontend/src/features/investigation/model.ts`
- Create: `observability-frontend/src/features/investigation/model.test.ts`
- Create: `observability-frontend/src/features/investigation/components/ImpactPane.tsx`
- Create: `observability-frontend/src/features/investigation/components/EvidenceTimeline.tsx`
- Create: `observability-frontend/src/features/investigation/components/JudgementPane.tsx`
- Create: `observability-frontend/src/features/investigation/components/InvestigationShell.tsx`
- Modify: `observability-frontend/src/pages/investigation/IntelligentInvestigation.tsx`
- Modify: `observability-frontend/src/pages/investigation/IntelligentInvestigation.test.tsx`
- Modify: `observability-frontend/src/pages/investigation/EvidenceDetail.tsx`

**Interfaces:**

- Consumes: `getRun()`、`listRunEvidences()`、`listRunTools()`、`getRunGraphContext()`、`streamRunEvents()`。
- Produces: `InvestigationViewModel`、`toInvestigationViewModel(snapshot)`、四个布局组件。

- [ ] **Step 1: 写证据卡、数据不足和冻结 Scope 的投影测试**

```ts
it('never promotes a root cause when evidence is insufficient', () => {
  const vm = toInvestigationViewModel(runFixture({ root_cause: '', confidence: 0.3, evidence: [] }))
  expect(vm.conclusion.state).toBe('insufficient_evidence')
  expect(vm.conclusion.title).toBe('证据不足，尚不能确认根因')
})

it('sorts evidence by observed time and preserves source metadata', () => {
  const vm = toInvestigationViewModel(runFixture({ evidence: [evidence('20:29'), evidence('20:31')] }))
  expect(vm.evidence.map((item) => item.observedAt)).toEqual(['20:31', '20:29'])
  expect(vm.evidence[0]).toMatchObject({ source: 'metrics', reliability: 0.96 })
})
```

- [ ] **Step 2: 运行测试并确认失败**

Run: `cd observability-frontend && npm run test:run -- src/features/investigation/model.test.ts`

Expected: FAIL，提示视图模型不存在。

- [ ] **Step 3: 实现纯视图模型**

视图模型只做稳定字段映射、排序、状态文案和缺失值处理，不推断不存在的 ToolRun、Evidence、Owner、影响对象或根因。

```ts
export interface EvidenceCardModel {
  id: string
  observedAt: string
  type: string
  source: string
  fact: string
  reliability: number | null
  quality: 'complete' | 'partial' | 'failed' | 'unknown'
  supports: string[]
  contradicts: string[]
}
```

- [ ] **Step 4: 拆分三栏组件**

`ImpactPane` 只接收 Scope、对象、影响链和变更；`EvidenceTimeline` 只接收证据与选中项；`JudgementPane` 只接收结论、假设、缺失证据和 Action。组件不得自行调用 API。

- [ ] **Step 5: 保留快照 + SSE 数据策略**

首次并行获取 Run、ToolRun、Evidence、Graph Context。SSE 只触发局部状态提示和按 sequence 的节流刷新；断线后现有 `streamRunEvents` 重连。详情 API 始终是权威快照，SSE payload 不直接覆盖完整 Run。

- [ ] **Step 6: 加入响应式布局**

```css
.investigation-grid{display:grid;grid-template-columns:264px minmax(420px,1fr) 352px;gap:14px}
@media(max-width:1439px){.investigation-grid{grid-template-columns:224px minmax(380px,1fr) 304px}.sidebar{width:64px}}
@media(max-width:1279px){.investigation-grid{grid-template-columns:minmax(0,1fr) 304px}.investigation-impact{display:none}}
```

1024–1279px 用按钮打开 `ImpactPane` Drawer；关键对象信息在页头保留，不得只存在 Drawer 中。

- [ ] **Step 7: 更新 Evidence 深链**

从证据卡进入详情时携带 Run tenant/cluster state；深链直接打开时继续先 `getRun` 回填 Scope。403 显示“范围拒绝访问”，404 显示“证据不存在”，两者文案不混淆。

- [ ] **Step 8: 运行 Investigation 测试**

Run: `cd observability-frontend && npm run test:run -- src/features/investigation/model.test.ts src/pages/investigation/IntelligentInvestigation.test.tsx src/api/runEvents.test.ts`

Expected: PASS；无证据时显示 insufficient evidence；SSE 重连不重复事件；Run Scope 为只读。

- [ ] **Step 9: 提交**

```bash
git add observability-frontend/src/features/investigation observability-frontend/src/pages/investigation/IntelligentInvestigation.tsx observability-frontend/src/pages/investigation/IntelligentInvestigation.test.tsx observability-frontend/src/pages/investigation/EvidenceDetail.tsx observability-frontend/src/index.css
git commit -m "feat(frontend): build three-pane investigation workspace"
```

---

### Task 6: 建立资源中心与观测聚合入口

**Files:**

- Create: `observability-frontend/src/pages/resources/ResourceCenter.tsx`
- Create: `observability-frontend/src/pages/resources/ResourceCenter.test.tsx`
- Create: `observability-frontend/src/pages/resources/ResourceIdentity.tsx`
- Create: `observability-frontend/src/pages/resources/MainFailureChain.tsx`
- Create: `observability-frontend/src/pages/observe/ObserveCenter.tsx`
- Create: `observability-frontend/src/pages/observe/ObserveCenter.test.tsx`
- Modify: `observability-frontend/src/pages/observability/ServiceObservability.tsx`
- Modify: `observability-frontend/src/pages/observability/ResourceRelationships.tsx`
- Modify: `observability-frontend/src/App.tsx`

**Interfaces:**

- Consumes: `getServiceOverview()`、`getServiceMap()`、`getServiceDependencies()`、`searchGraphEntities()`、`getGraphNeighbors()`、现有观测页面。
- Produces: `/resources`、`/observe`、`ResourceIdentity`、`MainFailureChain`。

- [ ] **Step 1: 写资源身份优先和默认主链测试**

```tsx
it('shows ownership and recent context before raw telemetry', async () => {
  render(<ResourceCenter />)
  expect(await screen.findByText('Owner')).toBeInTheDocument()
  expect(screen.getByText('最近变更')).toBeInTheDocument()
  expect(screen.getByRole('tab', { name: '关键指标' })).toBeInTheDocument()
})

it('renders one main dependency chain by default', () => {
  render(<MainFailureChain center={center} upstream={[web]} downstream={[mysql]} />)
  expect(screen.getAllByTestId('main-chain-node')).toHaveLength(3)
  expect(screen.getByRole('button', { name: /展开上下游/ })).toBeInTheDocument()
})
```

- [ ] **Step 2: 运行测试并确认失败**

Run: `cd observability-frontend && npm run test:run -- src/pages/resources/ResourceCenter.test.tsx`

Expected: FAIL，提示资源中心组件不存在。

- [ ] **Step 3: 实现资源目录和身份区**

`ResourceCenter` 从 query 参数读取 `kind`、`view` 与活动 Scope 资源。身份区字段按“类型、健康、Owner、应用、Namespace、工作负载/节点、最近变更、当前告警”固定排序。字段缺失显示“未知”，不隐藏标签。

- [ ] **Step 4: 实现主故障链选择**

优先使用后端 `ServiceDependenciesResponse` 的 center、upstream、downstream、middleware。默认只选一条与异常边权最高的上游 → center → 下游路径；若后端没有异常权重，则显示 center 和各一条直接邻居并标记“未排序”。完整 `GraphExplorer` 只在专家关系探索中加载。

- [ ] **Step 5: 将现有服务页作为资源中心子视图复用**

把 `ServiceObservability` 的 Summary、Map、Dependency、Matrix、List、Explore 组件移入资源中心的 service 视图；删除页面级重复 Scope 控件，namespace 与 timeRange 改读全局 store。页面专属的 application、health、only abnormal 保持本地过滤。

- [ ] **Step 6: 建立观测聚合入口**

`ObserveCenter` 使用 query `view=alerts|traces|telemetry|changes|grafana|rules` 选择现有页面。入口只做导航与共享 Scope，不复制各页面数据逻辑。未知 view 回退 alerts，并替换 URL。

- [ ] **Step 7: 运行资源和观测测试**

Run: `cd observability-frontend && npm run test:run -- src/pages/resources/ResourceCenter.test.tsx src/pages/observe/ObserveCenter.test.tsx src/pages/observability/ServiceObservability.test.tsx`

Expected: PASS；默认不渲染完整图；旧页面能力仍可通过新聚合路由访问。

- [ ] **Step 8: 提交**

```bash
git add observability-frontend/src/pages/resources observability-frontend/src/pages/observe observability-frontend/src/pages/observability/ServiceObservability.tsx observability-frontend/src/pages/observability/ResourceRelationships.tsx observability-frontend/src/App.tsx
git commit -m "feat(frontend): add resource and observation centers"
```

---

### Task 7: 将审批中心升级为处置中心

**Files:**

- Create: `observability-frontend/src/pages/actions/ActionCenter.tsx`
- Create: `observability-frontend/src/pages/actions/ActionCenter.test.tsx`
- Create: `observability-frontend/src/pages/actions/actionModel.ts`
- Create: `observability-frontend/src/pages/actions/actionModel.test.ts`
- Modify: `observability-frontend/src/pages/admin/Approvals.tsx`
- Modify: `observability-frontend/src/api/client.ts`
- Modify: `observability-frontend/src/App.tsx`

**Interfaces:**

- Consumes: `listActions()`、`getAction()`、`decideAction()`、`useAuthStore()`。
- Produces: `ActionViewModel`、`toActionViewModel()`、`ActionCenter`。

- [ ] **Step 1: 写风险、来源 Run 和权限投影测试**

```ts
it('keeps missing risk explicit instead of inventing a score', () => {
  expect(toActionViewModel(action({ risk_score: undefined })).risk).toEqual({ level: 'unknown', label: '未评估' })
})

it('shows decisions only to approvers and admins', () => {
  expect(canDecideAction('operator')).toBe(false)
  expect(canDecideAction('approver')).toBe(true)
  expect(canDecideAction('admin')).toBe(true)
})

it('links every action to its source run', () => {
  expect(toActionViewModel(action({ run_id: 'run-1' })).runHref).toBe('/investigation/run-1')
})
```

- [ ] **Step 2: 运行测试并确认失败**

Run: `cd observability-frontend && npm run test:run -- src/pages/actions/actionModel.test.ts`

Expected: FAIL，提示 Action 视图模型不存在。

- [ ] **Step 3: 扩展 ActionProjection 的可选展示字段**

只把服务端已经返回的 `risk_score`、`risk_level`、`impact_summary`、`verification_status`、`rollback_summary` 加为可选字段。若 API 尚未返回，UI 显示“未评估/未提供”，实施者不得从 hash、preflight 或 operation 字符串推算风险。

- [ ] **Step 4: 实现处置队列**

顶部状态为待审批、待执行、执行中、待验证、已结束。列表项首屏显示操作、目标、来源 Run、风险、影响、审批、执行和验证；普通用户可查看，只有 approver/admin 渲染决策按钮。

- [ ] **Step 5: 实现详情 Drawer 和安全决策**

Drawer 展示规范化参数、target UID、ResourceVersion、Action hash/schema/version、Policy version、Preflight、审批意见、回滚策略和验证条件。批准/驳回继续传 `action_version` 与稳定 idempotency key；409/412 显示“动作版本已变化，请刷新后重新评估”，不得自动重试决策。

- [ ] **Step 6: 保留旧组件兼容**

`pages/admin/Approvals.tsx` 临时 re-export `ActionCenter`，旧测试和深链在两版本迁移期内继续工作：

```ts
export { default } from '../actions/ActionCenter'
```

- [ ] **Step 7: 运行处置测试**

Run: `cd observability-frontend && npm run test:run -- src/pages/actions/actionModel.test.ts src/pages/actions/ActionCenter.test.tsx src/pages/admin/Approvals.test.tsx`

Expected: PASS；无风险数据时明确显示未评估；operator 看不到审批按钮；409/412 不会自动重复 POST。

- [ ] **Step 8: 提交**

```bash
git add observability-frontend/src/pages/actions observability-frontend/src/pages/admin/Approvals.tsx observability-frontend/src/api/client.ts observability-frontend/src/App.tsx
git commit -m "feat(frontend): promote approvals into action center"
```

---

### Task 8: 收敛视觉 Token、响应式与可访问性

**Files:**

- Modify: `observability-frontend/src/theme/tokens.ts`
- Modify: `observability-frontend/src/index.css`
- Modify: `observability-frontend/src/components/ui/PageKit.tsx`
- Create: `observability-frontend/src/theme/tokens.test.ts`
- Create: `tests/manual-e2e/ia-workbench.js`
- Create: `tests/manual-e2e/ia-investigation.js`
- Create: `tests/manual-e2e/ia-actions.js`
- Modify: `tests/manual-e2e/lib/harness.js`

**Interfaces:**

- Consumes: Tasks 2–7 的页面与 `data-testid`。
- Produces: 统一语义 Token、三档响应式规则、主流程 E2E 与视觉基线。

- [ ] **Step 1: 写 Token 语义测试**

```ts
it('keeps action blue distinct from severity colors', () => {
  expect(token.colorPrimary).toBe('#3157d5')
  expect(token.colorError).toBe('#c9362b')
  expect(token.colorWarning).toBe('#c46816')
  expect(token.colorSuccess).toBe('#18864b')
  expect(new Set([token.colorPrimary, token.colorError, token.colorWarning, token.colorSuccess]).size).toBe(4)
})
```

- [ ] **Step 2: 运行测试并确认旧 Token 不匹配**

Run: `cd observability-frontend && npm run test:run -- src/theme/tokens.test.ts`

Expected: FAIL，旧主色和语义色与设计规范不同。

- [ ] **Step 3: 更新 Token 和全局密度**

将设计规范第 8 节的色值同时写入 Ant Design token 与 CSS variables。卡片圆角统一 8px，默认页面间距 20px，1280px 紧凑态 16px。删除大面积蓝色 nav hero 和非必要阴影；主按钮、链接和当前导航保留蓝色。

- [ ] **Step 4: 补充响应式和可访问性**

1280px 及以下自动折叠侧栏；1024–1279px 按设计把 Investigation 左栏转 Drawer、首页纵向堆叠、资源上下文转 Tab。所有状态 badge 有文字；动态 Run 状态容器加 `aria-live="polite"`；失败提示加 `role="alert"`；主要点击目标最小 36×36px。

- [ ] **Step 5: 写三条 E2E 主链**

```js
const { runLaneA } = require('./lib/laneA')

runLaneA({
  id: 'IA-WORKBENCH-001',
  route: '/overview',
  viewports: [{ width: 1440, height: 900 }, { width: 1280, height: 720 }, { width: 1024, height: 768 }],
  actions: async (page, check) => {
    await page.getByRole('button', { name: '开始调查' }).first().click()
    check('navigates_to_draft', /\/investigation\/new/.test(page.url()))
    check('symptom_prefilled', Boolean(await page.getByLabel('症状 / 调查目标').inputValue()))
  },
})
```

另外两条脚本覆盖 Chat → 正式调查和 Run Action → 审批 → 验证。它们沿用 `tests/manual-e2e/lib/harness.js` 的认证、网络记录和证据输出，断言新建页加载时没有隐式 `POST /ai/runs`。

```js
await runPageTest({ id: 'IA-INVESTIGATION-001', route: '/ai/chat', actions: async (page, check) => {
  let createRunCalls = 0
  page.on('request', (request) => {
    if (request.method() === 'POST' && /\/api\/v1\/ai\/runs$/.test(request.url())) createRunCalls += 1
  })
  await page.getByPlaceholder(/描述问题/).fill('分析 payment-api 的跨服务根因')
  await page.getByRole('button', { name: '发送' }).click()
  await page.getByRole('button', { name: '转为正式调查' }).click()
  check('no_implicit_run', createRunCalls === 0)
  await page.getByRole('button', { name: '发起调查' }).click()
  check('one_explicit_run', createRunCalls === 1)
} })

await runPageTest({ id: 'IA-ACTIONS-001', route: '/actions', actions: async (page, check) => {
  await page.getByRole('button', { name: '审批' }).first().click()
  check('resource_version_visible', await page.getByText(/ResourceVersion/).isVisible())
  await page.getByRole('button', { name: '批准' }).click()
  check('verification_state_visible', await page.getByText('待验证').isVisible())
  await page.getByRole('link', { name: 'RUN-8F2A' }).click()
  check('returns_to_run', /\/investigation\//.test(page.url()))
} })
```

- [ ] **Step 6: 让现有 harness 接受用例级视口，并添加三档断言**

把 `runPageTest` 参数解构中的 `uiCluster = true` 替换为 `uiCluster = true, viewports = ENV.viewports`，把循环源从 `ENV.viewports` 替换为 `viewports`。同时把持久化 Scope 初始化从旧 `aiops-ui-v3` 更新为 Task 1 定义的 `aiops-scope-v1`：

```js
for (const vp of viewports) {
  const context = await browser.newContext({ viewport: { width: vp.width, height: vp.height }, storageState: state })
  if (uiCluster) {
    await context.addInitScript((clusterId) => {
      localStorage.setItem('aiops-scope-v1', JSON.stringify({
        state: { active: { tenantId: '', environment: 'prod', clusterId, namespace: '', timeRange: { mode: 'relative', minutes: 60 } } },
        version: 0,
      }))
    }, ENV.clusterId)
  }
}
```

在 `ia-investigation.js` 的 `actions` 中加入：

```js
check('no_x_overflow', await page.evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth))
check('judgement_visible', await page.getByText('AI 判断与下一步').isVisible())
```

- [ ] **Step 7: 运行全部前端验证**

Run: `cd observability-frontend && npm run test:run`

Expected: 所有 Vitest 测试 PASS。

Run: `cd observability-frontend && npm run build`

Expected: TypeScript 和 Vite 构建成功，无类型错误。

Run: `node tests/manual-e2e/ia-workbench.js && node tests/manual-e2e/ia-investigation.js && node tests/manual-e2e/ia-actions.js`

Expected: 三条主链和三档视口断言 PASS，输出首页、调查、资源、处置的视觉快照。

- [ ] **Step 8: 检查实现没有越过安全边界**

Run: `rg -n "localStorage.*cluster|createRun\(" observability-frontend/src`

Expected: cluster 只在 Scope store 的 persist 初始化中出现；`createRun(` 只在新建调查显式提交路径与测试中出现。

Run: `rg -n "DEMO|mock root cause|fallback evidence" observability-frontend/src/pages observability-frontend/src/features`

Expected: 生产页面无演示 Run、伪造根因或 fallback Evidence。

- [ ] **Step 9: 提交**

```bash
git add observability-frontend/src/theme observability-frontend/src/index.css observability-frontend/src/components/ui/PageKit.tsx tests/manual-e2e/ia-workbench.js tests/manual-e2e/ia-investigation.js tests/manual-e2e/ia-actions.js tests/manual-e2e/lib/harness.js
git commit -m "test(frontend): verify redesigned operations workflow"
```

---

## Release Checkpoints

### Checkpoint A: 壳层与 Scope

完成 Tasks 1–2 后可以独立发布。使用 feature flag `VITE_IA_V4` 在新旧壳层间切换；不改变任何业务 API。

### Checkpoint B: 首页与调查主链

完成 Tasks 3–5 后发布。验收首页异常 → 新建调查 → 三栏工作台，确认 Run 仍由显式用户操作创建。

### Checkpoint C: 资源与观测

完成 Task 6 后发布。保留全部旧深链，监控新聚合入口的 404、空数据和图谱超时率。

### Checkpoint D: 处置与视觉收尾

完成 Tasks 7–8 后发布。重点观察 Action 409/412、审批幂等、1280×720 溢出和 Investigation SSE 重连。

## Rollback

回滚只切换 `VITE_IA_V4=false` 并恢复旧路由壳层；不回滚 Run、Evidence、Action 数据。旧深链在整个迁移期持续有效。若新页面单点异常，可让对应新路由临时重定向到旧页面，不影响其他已迁移业务域。
