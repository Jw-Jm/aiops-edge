# AIOps 多集群云平台资源运维 UI V3 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 将现有界面和 Query API 收敛为已确认的 A 方案：以全平台可处置问题态势为入口，在真实 Kubernetes 集群范围内运维容器、Kubernetes Service/Ingress 与 KubeVirt VM/VMI，提供可治理的 RAG 运维知识，并以可追溯智能助手连接事实、知识、调查与受控处置。

**Architecture:** 浏览器只调用 Query API 的 canonical 投影；平台聚合与集群资源使用独立路由和缓存边界，资源身份统一来自图谱/资产事实。MySQL 与 Git Playbook 是知识权威来源，Chroma 是可重建索引；AI 会话由 Query API/MySQL 按 user + tenant + cluster 持久化，回答引用服务端校验过的事实与已发布知识。所有资源、问答、知识、调查与处置请求都由服务端强制执行 tenant + cluster 授权。

**Tech Stack:** React 18、TypeScript 5.6、React Router 6、TanStack Query 5、Ant Design 5、ECharts 5、AntV G6 5、Vitest；Go、MySQL、HugeGraph、Chroma；Helm/Kubernetes。

**Spec:** [正式设计方案](../specs/2026-09-08-aiops-information-architecture-design.md)；[V3 实际渲染与编码指南](../specs/assets/ui-v2/UI_RENDER_GUIDE.md)；[可运行 V3 原型](../specs/assets/ui-v2/aiops-ui-prototype.html)

## Global Constraints

- 所有实现和提交都在 `main` 分支顺序进行；每个任务通过验证并提交后，下一智能体再接手，禁止并行修改同一工作树。
- 平台只有生产环境；UI、URL Scope 和用户偏好中不得出现 `prod/dev/staging/test` 环境选择或“生产平台/生产集群”伪层级。
- `/overview` 聚合全部授权集群且不受活动集群影响；其余主工作区必须绑定一个经 `/me.available_clusters` 验证的真实集群。
- 不提供全局搜索；只保留资源目录名称搜索和运维知识语义搜索。
- 顶级健康只使用 `healthy | degraded | critical | unknown`，同时显示文字、图标和颜色；不计算 0–100 综合健康分数。
- 容器一级 Kind 固定为 Deployment、StatefulSet、DaemonSet、Job、CronJob、Pod、Kubernetes Service、Ingress；Container、ReplicaSet、EndpointSlice 只出现在详情或证据。
- 业务、应用、APM Service、中间件默认隐藏但保留兼容数据；`k8s_service` 与历史 `service` 不得混同。
- PVC 只有一个 canonical 身份；VM 磁盘是设备视图。NAD 只在真实 `multus.networkName` 引用中出现。
- 图谱 `80 节点 / 200 边` 是首屏预算，不是查询、资源总量或聚合上限；超量必须聚合、分页展开，并提供等价关系列表。
- 每个数据区都实现 loading、empty、error、partial、stale、forbidden；生产接口失败不得回退为原型示例数据。
- 所有危险动作继续执行 capability 校验、预检、审批、执行、验证、回滚和审计；不新增跨集群批量处置。
- 智能助手是 `/clusters/:clusterId/assistant` 一级工作区；会话冻结集群、可选资源、绝对时间和知识范围。完成回答必须包含结论、事实引用、已发布知识引用、不确定性、下一步和受控动作，`ai.chat` 不获得直接执行权限。
- 桌面最低完整体验为 1024px；1440×900、1280×720、1024×768/900 都必须做真实浏览器截图审核。

## File and ownership map

- `observability-frontend/src/App.tsx`：唯一应用壳层与 canonical 路由，不承载页面业务状态。
- `observability-frontend/src/layout/navConfig.ts`：一级导航、集群路由生成和历史深链重定向。
- `observability-frontend/src/features/scope/*`、`src/store/scopeStore.ts`：集群选择、提交、回读和深链校验。
- `observability-frontend/src/api/platform.ts`、`clusterOverview.ts`、`resources.ts`、`assistant.ts`、`knowledge.ts`：页面专用 API DTO 与 wire-to-view 映射。
- `observability-frontend/src/pages/Overview/*`：全平台问题态势；禁止读取活动集群。
- `observability-frontend/src/pages/Clusters/*`：集群总览、基础能力和明确 Kind 摘要。
- `observability-frontend/src/pages/Resources/*`：资源目录、类型化详情、VM 存储/NAD 依赖。
- `observability-frontend/src/components/graph/*`：图谱呈现、关系语义、聚合与等价列表。
- `observability-frontend/src/pages/Knowledge/*`：知识搜索、列表、详情、编辑与治理。
- `observability-frontend/src/pages/ai/AiChat.tsx`、`src/pages/Assistant/*`：复用现有会话能力，重建集群级助手布局和结构化应答框；禁止保留第二套聊天产品。
- `ai-apm-query-go/internal/api/platform_overview.go`、`cluster_overview.go`：canonical 聚合处理器。
- `ai-apm-query-go/internal/api/resource_catalog.go`、`internal/graph/*`：资源和关系事实投影。
- `ai-apm-query-go/internal/store/operations_knowledge.go`、`internal/api/operations_knowledge.go`：知识权威模型与 canonical API。
- `ai-apm-query-go/internal/api/knowledge_backend.go`：只读 Chroma 投影适配，不再承担正文权威。
- `ai-apm-query-go/internal/api/settings.go`、`ai_chat_sessions.go`、`internal/store/ai_chat_sessions.go`：保持 canonical chat、幂等 turn、范围隔离与 transcript 权威，增加冻结资源/时间和结构化回答。
- `ai-apm-query-go/internal/store/migrations/versions/0019_operations_knowledge.sql`：知识、版本、审核、outbox 与索引任务。

---

### Task 1: 固化产品壳层、canonical 路由和真实集群上下文

**Files:**
- Modify: `observability-frontend/src/App.tsx`
- Modify: `observability-frontend/src/layout/navConfig.ts`
- Modify: `observability-frontend/src/layout/navConfig.test.ts`
- Modify: `observability-frontend/src/features/scope/ScopeBar.tsx`
- Modify: `observability-frontend/src/features/scope/ScopeBar.test.tsx`
- Modify: `observability-frontend/src/store/scopeStore.ts`
- Modify: `observability-frontend/src/store/scopeStore.test.ts`
- Modify: `observability-frontend/src/index.css`

**Interfaces:**
- Consumes: `ScopeState.clusters`、`switchCluster(clusterId)` 与 `initialize()`；`switchCluster` 内部调用现有 `setActiveScope(tenantId, clusterId)` 后回读 `getMe()`。
- Produces: `clusterPath(clusterId, workspace)`、无全局搜索的 `AppLayout`、提交并回读后才导航的 `ClusterNavigator`。

- [x] **Step 1: 写失败测试，锁定导航和集群切换合同**

```ts
expect(PRIMARY_NAV.map((item) => item.label)).toEqual([
  '平台', '集群', '助手', '调查', '处置', '知识', '报告', '系统管理',
])
expect(clusterPath('cluster-1', 'knowledge')).toBe('/clusters/cluster-1/knowledge')
expect(screen.queryByRole('button', { name: '全局搜索' })).not.toBeInTheDocument()
useScopeStore.setState({ clusters: [{ tenant_id: 'tenant-a', cluster_id: 'cluster-2', name: '上海集群', status: 'ready' }] })
vi.mocked(getMe).mockResolvedValue(me('cluster-2') as never)
await useScopeStore.getState().switchCluster('cluster-2')
expect(setActiveScope).toHaveBeenCalledWith('tenant-a', 'cluster-2')
expect(vi.mocked(getMe).mock.invocationCallOrder[0]).toBeGreaterThan(vi.mocked(setActiveScope).mock.invocationCallOrder[0])
```

- [x] **Step 2: 运行定向测试并确认旧壳层失败**

Run: `cd observability-frontend && npm test -- --run src/layout/navConfig.test.ts src/features/scope/ScopeBar.test.tsx src/store/scopeStore.test.ts src/App.test.tsx`

Expected: FAIL，原因包含旧“工作台”导航、全局搜索仍存在，或切换后未回读 Scope。

- [x] **Step 3: 实现最小壳层与路由**

```ts
export type ClusterWorkspace = 'overview' | 'resources' | 'observe' | 'graph' | 'assistant' | 'investigations' | 'actions' | 'knowledge' | 'reports'
export const clusterPath = (clusterId: string, workspace: ClusterWorkspace) =>
  workspace === 'overview'
    ? `/clusters/${encodeURIComponent(clusterId)}`
    : `/clusters/${encodeURIComponent(clusterId)}/${workspace}`
```

在 `App.tsx` 删除 `searchOpen/searchQuery`、⌘K 监听、搜索按钮和搜索结果；注册规格中的 canonical 路由。旧 `/ai/chat` 在存在已确认活动集群时重定向到 `/clusters/:clusterId/assistant`，否则进入明确的集群选择态。深链渲染前调用 `ensureAuthorizedCluster(routeClusterId)`，失败保持旧 Scope 与原页面，成功后再导航。顶栏只显示真实集群选择、上下文说明、通知和用户。

- [x] **Step 4: 运行测试与构建**

Run: `cd observability-frontend && npm test -- --run src/layout/navConfig.test.ts src/features/scope/ScopeBar.test.tsx src/store/scopeStore.test.ts src/App.test.tsx && npm run build`

Expected: PASS；壳层测试确认页面 DOM 不出现“全局搜索”“生产平台”，构建无错误。

- [x] **Step 5: 提交**

```bash
git add observability-frontend/src/App.tsx observability-frontend/src/layout observability-frontend/src/features/scope observability-frontend/src/store/scopeStore.ts observability-frontend/src/store/scopeStore.test.ts observability-frontend/src/index.css
git commit -m "feat(ui): establish cluster-scoped v3 shell"
```

### Task 2: 建立统一状态、内容边界和视觉 token

**Files:**
- Modify: `observability-frontend/src/theme/tokens.ts`
- Modify: `observability-frontend/src/theme/tokens.test.ts`
- Modify: `observability-frontend/src/components/display/DataState.tsx`
- Modify: `observability-frontend/src/components/display/DataState.test.tsx`
- Create: `observability-frontend/src/components/display/BoundedDataRegion.tsx`
- Create: `observability-frontend/src/components/display/BoundedDataRegion.test.tsx`
- Modify: `observability-frontend/src/index.css`

**Interfaces:**
- Consumes: API `meta.generated_at/partial/stale/warning_codes`。
- Produces: `HealthState = 'healthy' | 'degraded' | 'critical' | 'unknown'` 与 `<BoundedDataRegion state meta error onRetry>`。

- [x] **Step 1: 写失败测试覆盖七种数据态和无横向溢出类**

```tsx
const states = ['loading', 'empty', 'error', 'partial', 'stale', 'forbidden'] as const
states.forEach((state) => {
  const { getByTestId, unmount } = render(<BoundedDataRegion state={state}>facts</BoundedDataRegion>)
  expect(getByTestId('bounded-data-region')).toHaveAttribute('data-state', state)
  unmount()
})
expect(HEALTH_LABELS).toEqual({ healthy: '健康', degraded: '降级', critical: '严重', unknown: '未知' })
```

- [x] **Step 2: 运行定向测试并确认组件不存在**

Run: `cd observability-frontend && npm test -- --run src/theme/tokens.test.ts src/components/display/DataState.test.tsx src/components/display/BoundedDataRegion.test.tsx`

Expected: FAIL with “Cannot find module BoundedDataRegion”。

- [x] **Step 3: 实现统一边界**

```ts
export type DataRegionState = 'ready' | 'loading' | 'empty' | 'error' | 'partial' | 'stale' | 'forbidden'
export interface ReadMeta {
  generatedAt: string
  partial: boolean
  stale: boolean
  warningCodes: string[]
}
```

组件必须保留与内容相近的最小高度；`partial/stale` 显示警示但仍渲染已有事实；`forbidden` 不重试；错误文案不声称“暂无数据”。CSS 定义 V3 导航、64px 顶栏、12 栏网格、10px 卡片圆角和容器内断词。

- [x] **Step 4: 运行测试和样式构建**

Run: `cd observability-frontend && npm test -- --run src/theme/tokens.test.ts src/components/display && npm run build`

Expected: PASS，无 TypeScript 或 CSS 构建错误。

- [x] **Step 5: 提交**

```bash
git add observability-frontend/src/theme observability-frontend/src/components/display observability-frontend/src/index.css
git commit -m "feat(ui): add bounded v3 data states"
```

### Task 3: 提供全平台运营态势 canonical API

**Files:**
- Create: `ai-apm-query-go/internal/api/platform_overview.go`
- Create: `ai-apm-query-go/internal/api/platform_overview_test.go`
- Modify: `ai-apm-query-go/internal/bootstrap/http.go`
- Modify: `ai-apm-query-go/internal/api/auth.go`
- Create: `observability-frontend/src/api/platform.ts`
- Create: `observability-frontend/src/api/platform.test.ts`

**Interfaces:**
- Consumes: 当前登录态 tenant、全部授权集群、告警/Run/Action 生命周期、采集与同步时效。
- Produces: `GET /api/v1/platform/overview` 与 `GET /api/v1/platform/clusters`；接口忽略活动 cluster。

- [x] **Step 1: 写失败测试锁定聚合口径与 Scope 隔离**

```go
func TestPlatformOverviewIgnoresActiveClusterAndUsesAuthorizedSet(t *testing.T) {
    req := authorizedRequest("tenant-1", "cluster-a", []string{"cluster-a", "cluster-b"})
    rr := httptest.NewRecorder()
    handler.PlatformOverview(rr, req)
    require.Equal(t, http.StatusOK, rr.Code)
    require.JSONEq(t, `{"managed_clusters":2,"affected_clusters":1}`, pick(rr.Body.Bytes(), "managed_clusters", "affected_clusters"))
    require.NotContains(t, rr.Body.String(), `"platform_status"`)
}
```

同时覆盖问题去重键、严重问题生命周期、健康/降级/严重/未知分布、118/120 一类覆盖分子分母、最新/最旧时间、partial/stale、无权限集群排除。

- [x] **Step 2: 运行 Go 测试并确认路由不存在**

Run: `cd ai-apm-query-go && go test ./internal/api ./internal/bootstrap -run 'PlatformOverview|PlatformClusters' -count=1`

Expected: FAIL，处理器或路由未定义。

- [x] **Step 3: 实现服务端 DTO 与前端映射**

```go
type platformOverviewResponse struct {
    ActiveCriticalIssues issueLifecycleCounts `json:"active_critical_issues"`
    ManagedClusters int `json:"managed_clusters"`
    AffectedClusters int `json:"affected_clusters"`
    UnknownOrStaleClusters int `json:"unknown_or_stale_clusters"`
    ClusterStates map[string]int `json:"cluster_states"`
    Coverage coverageRatio `json:"coverage"`
    FreshestAt time.Time `json:"freshest_at"`
    OldestValidAt time.Time `json:"oldest_valid_at"`
    HighestPriority *platformIssue `json:"highest_priority,omitempty"`
    CapabilitySummary capabilitySummary `json:"capability_summary"`
    Meta ResourceReadMeta `json:"meta"`
}
```

`PlatformOverview` 从授权集合聚合，不调用 `currentScope(r)` 的 active cluster 过滤；问题按 `tenant + cluster + resource_uid + rule_id + active_lifecycle` 去重。前端 `getPlatformOverview(signal)` 只负责 wire-to-view 映射。

- [x] **Step 4: 运行后端和前端 API 测试**

Run: `cd ai-apm-query-go && go test ./internal/api ./internal/bootstrap -run 'PlatformOverview|PlatformClusters' -count=1`

Run: `cd observability-frontend && npm test -- --run src/api/platform.test.ts`

Expected: 全部 PASS。

- [x] **Step 5: 提交**

```bash
git add ai-apm-query-go/internal/api/platform_overview* ai-apm-query-go/internal/bootstrap/http.go ai-apm-query-go/internal/api/auth.go observability-frontend/src/api/platform*
git commit -m "feat(api): add platform operations overview"
```

### Task 4: 重建平台总览和集群详细总览

**Files:**
- Replace: `observability-frontend/src/pages/Overview/index.tsx`
- Modify: `observability-frontend/src/pages/Overview/Overview.test.tsx`
- Create: `ai-apm-query-go/internal/api/cluster_overview.go`
- Create: `ai-apm-query-go/internal/api/cluster_overview_test.go`
- Create: `observability-frontend/src/api/clusterOverview.ts`
- Create: `observability-frontend/src/pages/Clusters/ClusterOverview.tsx`
- Create: `observability-frontend/src/pages/Clusters/ClusterOverview.test.tsx`
- Modify: `ai-apm-query-go/internal/bootstrap/http.go`

**Interfaces:**
- Consumes: Task 3 `PlatformOverview` 与 Task 2 `BoundedDataRegion`。
- Produces: `GET /api/v1/clusters/{clusterId}/overview`、`PlatformOperationsSummary`、`DirectResourceKindPanel`、`ClusterFoundationBand`。

- [x] **Step 1: 写失败测试覆盖首屏判读与明确 Kind**

```tsx
expect(await screen.findByText('当前活动严重问题')).toBeVisible()
expect(screen.getByText('受影响集群')).toBeVisible()
expect(screen.queryByText('被纳管云平台状态')).not.toBeInTheDocument()
expect(screen.queryByText(/综合分数|健康率/)).not.toBeInTheDocument()
for (const kind of ['Deployment', 'StatefulSet', 'DaemonSet', 'Job', 'CronJob', 'Pod', 'Kubernetes Service', 'Ingress']) {
  expect(await screen.findByText(kind)).toBeVisible()
}
expect(screen.queryByText('Workload')).not.toBeInTheDocument()
```

- [x] **Step 2: 运行定向测试确认旧总览失败**

Run: `cd observability-frontend && npm test -- --run src/pages/Overview/Overview.test.tsx src/pages/Clusters/ClusterOverview.test.tsx`

Expected: FAIL，旧总览依赖活动集群并展示服务/调用量等旧 KPI。

- [x] **Step 3: 实现平台与集群页面**

```ts
export interface ResourceKindSummary {
  kind: 'deployment' | 'statefulset' | 'daemonset' | 'job' | 'cronjob' | 'pod' | 'k8s_service' | 'ingress'
  total: number
  abnormal: number
  unknownOrStale: number
}
export interface FoundationFact {
  kind: 'control_plane' | 'nodes_hosts' | 'network' | 'storage' | 'kubevirt'
  status: HealthState
  reason: string
  affectedResourceCount: number
}
```

平台页按 V3 图 01 编排；集群页按图 02 编排。KubeVirt 只显示 VM、VMI、未就绪、迁移中/失败、受存储或网络问题影响的 VM 数，不显示磁盘/NAD 数。基础能力只显示状态、依据与影响。

- [x] **Step 4: 运行全部相关测试与构建**

Run: `cd ai-apm-query-go && go test ./internal/api ./internal/bootstrap -run 'ClusterOverview' -count=1`

Run: `cd observability-frontend && npm test -- --run src/pages/Overview src/pages/Clusters && npm run build`

Expected: PASS，平台页在没有活动集群时仍可读。

- [x] **Step 5: 提交**

```bash
git add ai-apm-query-go/internal/api/cluster_overview* ai-apm-query-go/internal/bootstrap/http.go observability-frontend/src/api/clusterOverview.ts observability-frontend/src/pages/Overview observability-frontend/src/pages/Clusters
git commit -m "feat(ui): rebuild platform and cluster overviews"
```

### Task 5: 收敛资源目录为真实载体和明确 Kind

**Files:**
- Modify: `ai-apm-query-go/internal/api/resource_catalog.go`
- Modify: `ai-apm-query-go/internal/api/resource_catalog_test.go`
- Modify: `ai-apm-query-go/internal/bootstrap/http.go`
- Modify: `observability-frontend/src/api/resources.ts`
- Modify: `observability-frontend/src/api/resources.test.ts`
- Replace: `observability-frontend/src/pages/Resources/index.tsx`
- Replace: `observability-frontend/src/pages/Resources/ResourceDirectory.tsx`
- Modify: `observability-frontend/src/pages/Resources/ResourceDirectory.test.tsx`
- Modify: `observability-frontend/src/pages/Resources/ClusterPanorama.tsx`

**Interfaces:**
- Consumes: `group=containers|kubevirt`、`type`、`namespace`、`q`、`health`、cursor。
- Produces: 路径型 `/clusters/:clusterId/resources` 目录与稳定 `ResourceCatalogItem.uid`。

- [x] **Step 1: 写失败测试锁定允许类型和分页**

```go
func TestResourceCatalogContainerGroupExposesOnlyPrimaryKinds(t *testing.T) {
    got := catalog(t, "?group=containers&limit=20")
    require.ElementsMatch(t, []string{"deployment","statefulset","daemonset","job","cronjob","pod","k8s_service","ingress"}, uniqueTypes(got.Items))
    require.NotContains(t, uniqueTypes(got.Items), "container")
    require.NotContains(t, uniqueTypes(got.Items), "replicaset")
}
```

```tsx
expect(screen.getByRole('tab', { name: '容器资源' })).toBeVisible()
expect(screen.getByRole('tab', { name: 'KubeVirt 虚拟机' })).toBeVisible()
expect(screen.queryByRole('tab', { name: '应用服务' })).not.toBeInTheDocument()
expect(screen.queryByText('Pod/Container')).not.toBeInTheDocument()
```

- [x] **Step 2: 运行资源测试并确认失败**

Run: `cd ai-apm-query-go && go test ./internal/api -run 'ResourceCatalog|ResourceSummary' -count=1`

Run: `cd observability-frontend && npm test -- --run src/api/resources.test.ts src/pages/Resources/ResourceDirectory.test.tsx`

Expected: FAIL，旧 API 仍按 compute/network/storage/kubernetes/application 五域暴露。

- [x] **Step 3: 实现 group 投影与 V3 目录**

```go
var primaryTypesByGroup = map[string]map[string]struct{}{
    "containers": {"deployment": {}, "statefulset": {}, "daemonset": {}, "job": {}, "cronjob": {}, "pod": {}, "k8s_service": {}, "ingress": {}},
    "kubevirt": {"vm": {}, "vmi": {}},
}
```

兼容 domain 参数继续可读但不生成新 UI Tab；`application/business/service/middleware` 仅在显式兼容深链下返回。目录请求必须服务端分页，显示总匹配量和当前范围；筛选写入 URL；不存在/无权限不选中第一行。

- [x] **Step 4: 运行资源回归和构建**

Run: `cd ai-apm-query-go && go test ./internal/api ./internal/graph -run 'ResourceCatalog|ResourceSummary|Scope' -count=1`

Run: `cd observability-frontend && npm test -- --run src/api/resources.test.ts src/pages/Resources && npm run build`

Expected: PASS；界面无 Workload、Pod/Container、Service/Ingress 合并入口。

- [x] **Step 5: 提交**

```bash
git add ai-apm-query-go/internal/api/resource_catalog* ai-apm-query-go/internal/bootstrap/http.go observability-frontend/src/api/resources* observability-frontend/src/pages/Resources
git commit -m "feat(resources): expose canonical carrier kinds"
```

### Task 6: 建立 KubeVirt 存储与 NAD 的事实关系

**Files:**
- Modify: `ai-apm-query-go/internal/graph/ontology.go`
- Modify: `ai-apm-query-go/internal/graph/ontology_test.go`
- Modify: `ai-apm-query-go/internal/graph/schema_manifest_v2.json`
- Modify: `ai-apm-query-go/internal/graph/schema_resources_test.go`
- Modify: `ai-apm-query-go/internal/k8sboundary/k8sboundary.go`
- Modify: `ai-apm-query-go/internal/k8sboundary/k8sboundary_test.go`
- Create: `observability-frontend/src/pages/Resources/VmDependencyPanel.tsx`
- Create: `observability-frontend/src/pages/Resources/VmDependencyPanel.test.tsx`
- Modify: `observability-frontend/src/pages/Resources/resourceDetailModel.ts`
- Modify: `observability-frontend/src/pages/Resources/resourceDetailModel.test.ts`

**Interfaces:**
- Consumes: KubeVirt VM/VMI spec、CDI DataVolume、PVC/PV、Pod/VMI network attachments。
- Produces: `Vmi → disk_device → volume → data_volume → pvc → pv` 与 `Vmi → virtual_interface → nad → cni → network` typed edges。

- [x] **Step 1: 写失败测试证明身份不重复且默认网络无 NAD**

```go
func TestKubeVirtDependencyProjection(t *testing.T) {
    graph := projectFixture("vmi-with-datavolume-and-multus.yaml")
    require.Equal(t, 1, graph.CountEntity("pvc", "uid-pvc-root"))
    require.True(t, graph.HasPath("vmi-1", "uses_disk", "disk-vda", "references", "volume-root", "sourced_from", "dv-root", "declares", "uid-pvc-root"))
    require.True(t, graph.HasEdge("iface-net1", "connects_to_nad", "nad-sriov-prod"))
    require.False(t, graph.HasAnyNADEdge("iface-eth0"))
}
```

- [x] **Step 2: 运行图谱和边界测试确认缺少实体/关系**

Run: `cd ai-apm-query-go && go test ./internal/graph ./internal/k8sboundary -run 'KubeVirtDependency|DataVolume|NAD' -count=1`

Expected: FAIL，ontology 未完整声明 DataVolume、磁盘设备、Volume、虚拟接口关系。

- [x] **Step 3: 实现真实依赖投影和详情 DTO**

```ts
export interface VmDiskDependency {
  deviceName: string
  bus?: string
  volumeName: string
  dataVolume?: ResourceRef
  pvc?: ResourceRef
  pv?: ResourceRef
  storageClass?: ResourceRef
}
export interface VmNetworkDependency {
  interfaceName: string
  binding: string
  defaultPodNetwork: boolean
  nad?: ResourceRef
  cni?: ResourceRef
  network?: ResourceRef
}
```

关系边携带 `source_field`、`fact_status`、`synced_at`；PVC entity UID 始终来自同一 Kubernetes UID。详情按 V3 图 14 展示“磁盘与卷”和“网络接口”，NAD 旁明确写“Multus 辅助网络定义”。

- [x] **Step 4: 运行关系、资源详情和 schema 测试**

Run: `cd ai-apm-query-go && go test ./internal/graph ./internal/k8sboundary ./internal/api -run 'KubeVirt|DataVolume|NAD|ResourceDetail' -count=1`

Run: `cd observability-frontend && npm test -- --run src/pages/Resources/resourceDetailModel.test.ts src/pages/Resources/VmDependencyPanel.test.tsx && npm run build`

Expected: PASS；同一 PVC 不产生第二个“VM 磁盘资源”身份。

- [x] **Step 5: 提交**

```bash
git add ai-apm-query-go/internal/graph ai-apm-query-go/internal/k8sboundary observability-frontend/src/pages/Resources
git commit -m "feat(kubevirt): model storage and multus dependencies"
```

### Task 7: 重建资源关系图谱的语义、预算和等价列表

**Files:**
- Modify: `ai-apm-query-go/internal/api/graph_public.go`
- Modify: `ai-apm-query-go/internal/api/graph_public_test.go`
- Modify: `observability-frontend/src/api/graphContracts.ts`
- Modify: `observability-frontend/src/api/knowledgeGraph.ts`
- Modify: `observability-frontend/src/components/graph/GraphExplorer.tsx`
- Modify: `observability-frontend/src/components/graph/GraphMap.tsx`
- Modify: `observability-frontend/src/components/graph/GraphMap.test.tsx`
- Modify: `observability-frontend/src/components/graph/GraphRelationList.tsx`
- Modify: `observability-frontend/src/components/graph/GraphRelationList.test.tsx`
- Modify: `observability-frontend/src/pages/observability/ResourceRelationships.tsx`
- Modify: `observability-frontend/src/pages/observability/ResourceRelationships.test.tsx`

**Interfaces:**
- Consumes: Task 6 typed edges。
- Produces: `GraphPage { totalNodes, totalEdges, nodes, edges, nextCursor, aggregated }`，首屏最多 80/200，等价列表可遍历完整结果。

- [x] **Step 1: 写失败测试覆盖方向、关系、聚合和非静默截断**

```tsx
expect(screen.getByText('选择 8 个')).toBeVisible()
expect(screen.getByText('来源于')).toBeVisible()
expect(screen.getByText('连接到 NAD')).toBeVisible()
expect(screen.getByText(/匹配总数 2,418 节点 \/ 4,806 关系/)).toBeVisible()
expect(screen.getByRole('button', { name: '等价关系列表' })).toBeVisible()
```

```go
require.LessOrEqual(t, len(response.Nodes), 80)
require.LessOrEqual(t, len(response.Edges), 200)
require.Greater(t, response.TotalNodes, len(response.Nodes))
require.NotEmpty(t, response.NextCursor)
```

- [x] **Step 2: 运行图谱测试并确认旧实现失败**

Run: `cd ai-apm-query-go && go test ./internal/api -run 'Graph.*Budget|Graph.*Cursor|Graph.*Relation' -count=1`

Run: `cd observability-frontend && npm test -- --run src/components/graph src/pages/observability/ResourceRelationships.test.tsx`

Expected: FAIL，旧图缺少完整总量、关系证据或分页等价列表。

- [x] **Step 3: 实现语义分层和关系检查器**

```ts
export interface GraphEdgeView {
  id: string
  source: string
  target: string
  relation: string
  relationLabel: string
  factStatus: 'fact' | 'inferred'
  sourceRef: string
  syncedAt: string
  aggregateCount?: number
}
```

容器与 KubeVirt 平行布局；边使用固定端口和正交路由，不穿节点。事实实线、推断虚线；边标签使用不透明底色并始终可读。选中边显示三元组、来源字段、同步时间和证据；1024px 检查器进入 Drawer。等价列表列出源、关系、目标、事实状态、来源和同步时间。

- [x] **Step 4: 运行图谱全套测试和构建**

Run: `cd ai-apm-query-go && go test ./internal/api ./internal/graph -run 'Graph|Topology' -count=1`

Run: `cd observability-frontend && npm test -- --run src/api/knowledgeGraph.test.ts src/components/graph src/pages/observability/ResourceRelationships.test.tsx && npm run build`

Expected: PASS，80/200 超限时仍可读取总量和后续关系。

- [x] **Step 5: 提交**

```bash
git add ai-apm-query-go/internal/api/graph_public* observability-frontend/src/api/graphContracts.ts observability-frontend/src/api/knowledgeGraph* observability-frontend/src/components/graph observability-frontend/src/pages/observability/ResourceRelationships*
git commit -m "feat(graph): render explicit scalable resource relations"
```

### Task 8: 建立运维知识权威模型、审核和索引 outbox

**Files:**
- Create: `ai-apm-query-go/internal/store/migrations/versions/0019_operations_knowledge.sql`
- Modify: `ai-apm-query-go/internal/store/migrations/schema_manifest_test.go`
- Create: `ai-apm-query-go/internal/store/operations_knowledge.go`
- Create: `ai-apm-query-go/internal/store/operations_knowledge_test.go`
- Create: `ai-apm-query-go/internal/api/operations_knowledge.go`
- Create: `ai-apm-query-go/internal/api/operations_knowledge_test.go`
- Modify: `ai-apm-query-go/internal/bootstrap/http.go`
- Modify: `ai-apm-query-go/internal/api/auth.go`

**Interfaces:**
- Consumes: 登录态 tenant、路径 cluster、`knowledge.read|knowledge.submit|knowledge.write` capability。
- Produces: MySQL 中 knowledge、version、review、index_outbox、index_state 五类权威记录与 canonical CRUD/治理接口。

- [x] **Step 1: 写失败测试覆盖生命周期、范围和版本不可变**

```go
func TestKnowledgeLifecycleAndScope(t *testing.T) {
    draft := createKnowledge(t, operatorCtx("tenant-1", "cluster-a"), clusterDraft())
    submit(t, draft.ID)
    require.Equal(t, "pending_review", getKnowledge(t, draft.ID).Status)
    require.Equal(t, http.StatusForbidden, reviewAsOperator(t, draft.ID))
    reviewAsAdmin(t, draft.ID, "approve", "evidence complete")
    published := getKnowledge(t, draft.ID)
    require.Equal(t, "published", published.Status)
    require.Equal(t, 1, published.CurrentVersion)
    require.Equal(t, 1, countIndexOutbox(t, published.ID, published.CurrentVersionID))
    require.Equal(t, http.StatusNotFound, readFromCluster(t, "cluster-b", published.ID))
}
```

另测：非管理员不能创建 `platform_common`；驳回必须有原因并回到 draft；修改 published 创建 v2；disable 不删除历史；重放 review 不重复 outbox。

- [x] **Step 2: 运行迁移/store/API 测试确认失败**

Run: `cd ai-apm-query-go && go test ./internal/store/migrations ./internal/store ./internal/api -run 'OperationsKnowledge|KnowledgeLifecycle|KnowledgeScope' -count=1`

Expected: FAIL，表、DAO 和处理器未定义。

- [x] **Step 3: 实现状态机和统一授权谓词**

```go
type KnowledgeScopeType string
const (
    KnowledgePlatformCommon KnowledgeScopeType = "platform_common"
    KnowledgeCluster KnowledgeScopeType = "cluster"
)
type KnowledgeStatus string
const (
    KnowledgeDraft KnowledgeStatus = "draft"
    KnowledgePendingReview KnowledgeStatus = "pending_review"
    KnowledgePublished KnowledgeStatus = "published"
    KnowledgeDisabled KnowledgeStatus = "disabled"
)
```

所有列表、详情、版本、审核和 outbox SQL 复用 `tenant_id = ? AND (scope_type='platform_common' OR cluster_id = ?)`；请求体不接受 tenant。正文与版本保存 MySQL，Git Playbook 保存 revision 记录且 UI 不可修改。审核发布与 outbox 在一个事务中提交。

- [x] **Step 4: 运行 store、API、auth 和迁移测试**

Run: `cd ai-apm-query-go && go test ./internal/store/migrations ./internal/store ./internal/api -run 'Knowledge|Auth' -count=1`

Expected: PASS；并发审核只产生一个 published version/outbox。

- [x] **Step 5: 提交**

```bash
git add ai-apm-query-go/internal/store/migrations ai-apm-query-go/internal/store/operations_knowledge* ai-apm-query-go/internal/api/operations_knowledge* ai-apm-query-go/internal/bootstrap/http.go ai-apm-query-go/internal/api/auth.go
git commit -m "feat(knowledge): add governed source-of-truth model"
```

### Task 9: 收敛 Chroma 为可重建且受授权约束的检索投影

**Files:**
- Modify: `ai-apm-query-go/internal/query/knowledge.go`
- Modify: `ai-apm-query-go/internal/query/knowledge_test.go`
- Modify: `ai-apm-query-go/internal/api/knowledge_backend.go`
- Modify: `ai-apm-query-go/internal/api/knowledge_backend_test.go`
- Create: `ai-apm-query-go/internal/api/knowledge_index_worker.go`
- Create: `ai-apm-query-go/internal/api/knowledge_index_worker_test.go`
- Modify: `ai-apm-query-go/internal/api/operations_knowledge.go`

**Interfaces:**
- Consumes: Task 8 published version/outbox。
- Produces: canonical `POST /api/v1/clusters/{clusterId}/knowledge/search`、索引状态和管理员 reindex；Chroma metadata 双重校验。

- [ ] **Step 1: 写失败测试覆盖跨集群拒绝和索引降级**

```go
func TestKnowledgeSearchDropsUnauthorizedAndNonCurrentHits(t *testing.T) {
    hits := mapChromaHits(responseWith(
        hit("ok", meta("tenant-1", "cluster-a", "published", "v2", true)),
        hit("other-cluster", meta("tenant-1", "cluster-b", "published", "v2", true)),
        hit("old-version", meta("tenant-1", "cluster-a", "published", "v1", false)),
    ), KnowledgeScope{TenantID: "tenant-1", ClusterID: "cluster-a"})
    require.Equal(t, []string{"ok"}, ids(hits))
}
```

另测：Chroma down 时列表/正文仍 200，search 返回明确 503 和 retryable；索引失败状态可读；普通用户不能 reindex。

- [ ] **Step 2: 运行知识检索测试并确认旧 metadata 不足**

Run: `cd ai-apm-query-go && go test ./internal/query ./internal/api -run 'KnowledgeSearch|KnowledgeIndex|Chroma' -count=1`

Expected: FAIL，旧模型只校验 tenant + cluster，缺少 platform_common、published、current version 与 source revision。

- [ ] **Step 3: 实现投影 metadata 和幂等 worker**

```go
type KnowledgeIndexMetadata struct {
    TenantID string `json:"tenant_id"`
    ScopeType string `json:"scope_type"`
    ClusterID string `json:"cluster_id,omitempty"`
    KnowledgeID string `json:"knowledge_id"`
    VersionID string `json:"version_id"`
    Type string `json:"type"`
    Status string `json:"status"`
    SourceRevision string `json:"source_revision"`
    ContentChecksum string `json:"content_checksum"`
    IsCurrent bool `json:"is_current"`
}
```

worker 用 outbox ID 幂等 upsert；成功更新 indexed_at，失败记录 sanitized error、attempt 和 next_retry_at。检索先由 Chroma where 收窄，再以 MySQL 当前 published version 与授权谓词复核。

- [ ] **Step 4: 运行检索、worker 和竞态测试**

Run: `cd ai-apm-query-go && go test -race ./internal/query ./internal/api -run 'Knowledge|Chroma' -count=1`

Expected: PASS，无跨范围命中、旧版本命中或重复索引。

- [ ] **Step 5: 提交**

```bash
git add ai-apm-query-go/internal/query/knowledge* ai-apm-query-go/internal/api/knowledge_backend* ai-apm-query-go/internal/api/knowledge_index_worker* ai-apm-query-go/internal/api/operations_knowledge.go
git commit -m "feat(knowledge): govern rag projection and search"
```

### Task 10: 实现运维知识列表、详情和人工新增

**Files:**
- Create: `observability-frontend/src/api/knowledge.ts`
- Create: `observability-frontend/src/api/knowledge.test.ts`
- Create: `observability-frontend/src/pages/Knowledge/index.tsx`
- Create: `observability-frontend/src/pages/Knowledge/KnowledgeList.tsx`
- Create: `observability-frontend/src/pages/Knowledge/KnowledgeInspector.tsx`
- Create: `observability-frontend/src/pages/Knowledge/KnowledgeEditor.tsx`
- Create: `observability-frontend/src/pages/Knowledge/Knowledge.test.tsx`
- Modify: `observability-frontend/src/query/keys.ts`
- Modify: `observability-frontend/src/query/keys.test.ts`
- Modify: `observability-frontend/src/App.tsx`

**Interfaces:**
- Consumes: Task 8/9 canonical knowledge API。
- Produces: `/clusters/:clusterId/knowledge` 和 `/clusters/:clusterId/knowledge/new`；独立 list/detail/index query keys。

- [ ] **Step 1: 写失败测试覆盖搜索、范围、治理和降级**

```tsx
expect(await screen.findByText('运维知识')).toBeVisible()
expect(screen.getByText('检索与索引')).toBeVisible()
expect(screen.getByRole('tab', { name: '故障案例' })).toBeVisible()
expect(screen.getByRole('tab', { name: '运维文档' })).toBeVisible()
expect(screen.getByRole('tab', { name: '内置 Playbook' })).toBeVisible()
await user.click(screen.getByRole('button', { name: '保存草稿' }))
expect(createKnowledge).toHaveBeenCalledWith(expect.objectContaining({ clusterId: 'cluster-a', status: 'draft' }))
expect(screen.getByText('语义检索暂不可用，正文仍可浏览')).toBeVisible()
```

- [ ] **Step 2: 运行前端知识测试并确认页面不存在**

Run: `cd observability-frontend && npm test -- --run src/api/knowledge.test.ts src/pages/Knowledge/Knowledge.test.tsx src/query/keys.test.ts`

Expected: FAIL，模块与路由未定义。

- [ ] **Step 3: 实现 V3 知识工作区**

```ts
export const knowledgeKeys = {
  list: (clusterId: string, filters: KnowledgeFilters) => ['knowledge', clusterId, 'list', stableFilters(filters)] as const,
  detail: (clusterId: string, knowledgeId: string) => ['knowledge', clusterId, 'detail', knowledgeId] as const,
  indexStatus: (clusterId: string) => ['knowledge', clusterId, 'index-status'] as const,
}
```

1440px 为筛选/列表/详情三栏；1024px 筛选和详情改 Drawer。保存草稿与提交审核分开；平台通用范围只对有 capability 的管理员可选；内置 Playbook 只读。索引错误不清空 MySQL 列表。

- [ ] **Step 4: 运行知识测试、键盘测试和构建**

Run: `cd observability-frontend && npm test -- --run src/api/knowledge.test.ts src/pages/Knowledge src/query/keys.test.ts src/App.test.tsx && npm run build`

Expected: PASS；1024px DOM 不出现页面级横向滚动源。

- [ ] **Step 5: 提交**

```bash
git add observability-frontend/src/api/knowledge* observability-frontend/src/pages/Knowledge observability-frontend/src/query observability-frontend/src/App.tsx
git commit -m "feat(ui): add governed operations knowledge workspace"
```

### Task 11: 重建集群级智能助手与结构化应答框

**Files:**
- Modify: `observability-frontend/src/pages/ai/AiChat.tsx`
- Modify: `observability-frontend/src/pages/ai/AiChat.test.tsx`
- Create: `observability-frontend/src/pages/Assistant/AssistantAnswerCard.tsx`
- Create: `observability-frontend/src/pages/Assistant/AssistantAnswerCard.test.tsx`
- Create: `observability-frontend/src/pages/Assistant/AssistantContextPanel.tsx`
- Create: `observability-frontend/src/api/assistant.ts`
- Create: `observability-frontend/src/api/assistant.test.ts`
- Modify: `observability-frontend/src/query/keys.ts`
- Modify: `ai-apm-query-go/internal/api/settings.go`
- Modify: `ai-apm-query-go/internal/api/proxy_runinvocation_test.go`
- Modify: `ai-apm-query-go/internal/api/ai_chat_sessions.go`
- Modify: `ai-apm-query-go/internal/api/ai_chat_sessions_test.go`
- Modify: `ai-apm-query-go/internal/store/ai_chat_sessions.go`
- Modify: `ai-apm-query-go/internal/store/ai_chat_sessions_test.go`
- Create: `ai-apm-query-go/internal/store/migrations/versions/0020_ai_chat_scope_and_answers.sql`

**Interfaces:**
- Consumes: 现有 `POST /api/v1/ai/chat` SSE、`GET /api/v1/ai/sessions`、`GET|DELETE /api/v1/ai/session/{id}`、`ai.chat` capability、资源/Evidence canonical UID 与 Task 8–10 的已发布知识检索。
- Produces: 冻结的 `AssistantConversationScope`、可持久化的 `AssistantAnswer`、三栏/单栏响应式助手页、可回溯引用和“创建调查草稿”入口。

```ts
export interface AssistantConversationScope {
  clusterId: string
  resourceUid?: string
  from: string
  to: string
  knowledgeScope: 'platform_common_and_current_cluster'
}
export interface AssistantAnswer {
  conclusion: string
  evidenceCitations: EvidenceCitation[]
  knowledgeCitations: KnowledgeCitation[]
  limitations: string[]
  recommendedNextSteps: RecommendedStep[]
  capabilities: { createInvestigationDraft: boolean; proposeAction: boolean; executeAction: false }
  generatedAt: string
  sourceFreshness: SourceFreshness[]
  completeness: 'complete' | 'partial'
}
```

- [ ] **Step 1: 写失败测试锁定会话 Scope 与应答合同**

```tsx
expect(screen.getByText('会话 Scope')).toBeVisible()
expect(screen.getByText('已冻结')).toBeVisible()
expect(screen.getByRole('heading', { name: '结论' })).toBeVisible()
expect(screen.getByText('关键事实证据')).toBeVisible()
expect(screen.getByText('运维知识引用')).toBeVisible()
expect(screen.getByText('不确定性与缺失证据')).toBeVisible()
expect(screen.getByText('建议下一步')).toBeVisible()
expect(screen.queryByRole('button', { name: '立即执行' })).not.toBeInTheDocument()
await user.click(screen.getByRole('button', { name: '创建调查草稿' }))
expect(createInvestigationDraft).toHaveBeenCalledWith(expect.objectContaining({
  clusterId: 'cluster-a', resourceUid: 'uid-order-api', status: 'draft',
}))
```

Go 测试必须同时断言：恢复会话时 cluster/resource/absolute time 不可变；跨用户、租户或集群会话返回 403/404；同一 `turn_id` 重试不重复生成；未发布或越权知识引用被拒绝；`capabilities.execute_action` 永远为 false。

- [ ] **Step 2: 运行前后端定向测试并确认旧自由文本 UI 失败**

Run: `cd observability-frontend && npm test -- --run src/pages/ai/AiChat.test.tsx src/pages/Assistant src/api/assistant.test.ts src/App.test.tsx`

Run: `cd ai-apm-query-go && go test ./internal/api ./internal/store -run 'Chat|Assistant' -count=1`

Expected: FAIL，原因包含助手 canonical 路由未注册、会话没有冻结 resource/time、完成事件没有结构化引用，或旧页面仍只渲染自由文本。

- [ ] **Step 3: 扩展 Query API 权威会话与回答协议**

迁移在现有 `ai_chat_sessions` 表增加 `resource_uid`、`time_from`、`time_to`、`knowledge_scope`，字段只能在创建时写入；不要修改已有 owner 约束。完成 SSE 事件持久化 `kind=answer` 的结构化 metadata；服务端逐个验证 evidence 与 knowledge citation 的 tenant、cluster、resource、time、published version 后再返回。保留旧 `assistant` 文本事件作为两个小版本的兼容降级，但新前端不得从自由文本解析引用或 capability。

- [ ] **Step 4: 实现全新助手页面与应答框**

沿用现有 `AiChat.tsx` 会话与 SSE 能力，拆出 `AssistantAnswerCard` 等组件；不要创建并存的第二套聊天状态。1440px 使用 `220px + 1fr + 300px`，1024px 会话与上下文进入 Drawer。回答区独立滚动、输入区固定；按结论、事实引用、知识引用、不确定性、下一步、受控动作顺序渲染。流式阶段固定为“收集事实/检索知识/组织回答”；断流保留部分回答并使用相同 `turn_id` 重试。引用点击必须带回原 Scope。

- [ ] **Step 5: 验证权限、错误态、响应式与提交**

Run: `cd ai-apm-query-go && go test ./internal/api ./internal/store -run 'Chat|Assistant' -count=1`

Run: `cd observability-frontend && npm test -- --run src/pages/ai src/pages/Assistant src/api/assistant.test.ts src/App.test.tsx && npm run build`

Expected: PASS；无证据时不出现确定性根因，无权限/陈旧/部分/断流状态均有明确 UI；`ai.chat` 不能触发直接处置；1024px 无页面级横向溢出。

```bash
git add observability-frontend/src/pages/ai observability-frontend/src/pages/Assistant observability-frontend/src/api/assistant* observability-frontend/src/query/keys.ts ai-apm-query-go/internal/api/settings.go ai-apm-query-go/internal/api/proxy_runinvocation_test.go ai-apm-query-go/internal/api/ai_chat_sessions* ai-apm-query-go/internal/store/ai_chat_sessions* ai-apm-query-go/internal/store/migrations/versions/0020_ai_chat_scope_and_answers.sql
git commit -m "feat(ai): add evidence-grounded cluster assistant"
```

### Task 12: 按资源问题闭环重排观测、调查、处置和报告

**Files:**
- Replace: `observability-frontend/src/pages/Observe/index.tsx`
- Modify: `observability-frontend/src/pages/Observe/index.test.tsx`
- Modify: `observability-frontend/src/pages/investigation/InvestigationCenter.tsx`
- Modify: `observability-frontend/src/pages/investigation/IntelligentInvestigation.tsx`
- Modify: `observability-frontend/src/pages/investigation/IntelligentInvestigation.test.tsx`
- Modify: `observability-frontend/src/pages/Actions/ActionCenter.tsx`
- Modify: `observability-frontend/src/pages/Actions/ActionCenter.test.tsx`
- Modify: `observability-frontend/src/pages/Reports/index.tsx`
- Modify: `observability-frontend/src/pages/Reports/index.test.tsx`

**Interfaces:**
- Consumes: route cluster、可选 resource UID、绝对 time range、Run/Evidence/Action 事实和 knowledge submit。
- Produces: 问题导向观测、冻结调查 Scope、连续动作管线、来源可追溯的待审核案例。

- [ ] **Step 1: 写失败测试锁定闭环语义**

```tsx
expect(screen.getAllByText('cloud-sh-01').length).toBeGreaterThan(0)
expect(screen.queryByText('全部环境')).not.toBeInTheDocument()
expect(screen.getByText('失败率')).toBeVisible()
expect(screen.getByText('就绪副本')).toBeVisible()
expect(screen.getByText('待预检')).toBeVisible()
expect(screen.getByText('待审批')).toBeVisible()
expect(screen.getByText('执行中')).toHaveClass('flow')
await user.click(screen.getByRole('button', { name: '加入运维知识（待审核）' }))
expect(createKnowledge).toHaveBeenCalledWith(expect.objectContaining({ status: 'pending_review', sourceRunId: 'run-1' }))
```

- [ ] **Step 2: 运行四工作区测试并确认旧布局失败**

Run: `cd observability-frontend && npm test -- --run src/pages/Observe src/pages/investigation/IntelligentInvestigation.test.tsx src/pages/Actions src/pages/Reports`

Expected: FAIL，旧页面存在非 canonical 路由、数据源产品导航或知识直发缺口。

- [ ] **Step 3: 实现 V3 闭环布局**

```ts
export interface FrozenInvestigationScope {
  tenantId: string
  clusterId: string
  resourceUid?: string
  from: string
  to: string
}
```

观测按问题、告警、指标、日志、Trace、事件、变更切换；调查边显示关系、证据数量和事实/推断；证据不足使用 `insufficient_evidence`。处置只渲染服务端 capability，显示资源版本与幂等键。报告正文自然高度，“加入运维知识”只创建 pending_review。

- [ ] **Step 4: 运行页面测试和构建**

Run: `cd observability-frontend && npm test -- --run src/pages/Observe src/pages/investigation src/pages/Actions src/pages/Reports && npm run build`

Expected: PASS；执行中不使用健康绿，所有工作区显示真实 cluster。

- [ ] **Step 5: 提交**

```bash
git add observability-frontend/src/pages/Observe observability-frontend/src/pages/investigation observability-frontend/src/pages/Actions observability-frontend/src/pages/Reports
git commit -m "feat(ui): align observe investigate act report loop"
```

### Task 13: 收敛系统管理为接入与 AIOps 能力状态

**Files:**
- Modify: `observability-frontend/src/pages/admin/AdminHome.tsx`
- Modify: `observability-frontend/src/pages/admin/AdminSettings.tsx`
- Modify: `observability-frontend/src/pages/admin/AdminSettings.test.tsx`
- Modify: `observability-frontend/src/pages/admin/GraphOperations.tsx`
- Create: `observability-frontend/src/pages/admin/KnowledgeIndexOperations.tsx`
- Create: `observability-frontend/src/pages/admin/KnowledgeIndexOperations.test.tsx`

**Interfaces:**
- Consumes: 集群注册状态、采集/同步/调查/处置能力、Task 9 索引状态。
- Produces: 纳管集群、AIOps 自身健康、采集器、图谱同步、知识索引、数据可信度、能力策略、用户和审计设置导航。

- [ ] **Step 1: 写失败测试分离三类状态**

```tsx
expect(screen.getByText('集群接入状态')).toBeVisible()
expect(screen.getByText('AIOps 自身健康')).toBeVisible()
expect(screen.getByText('知识索引任务')).toBeVisible()
expect(screen.queryByText('被纳管云平台健康')).not.toBeInTheDocument()
expect(screen.getByText('正文仍可浏览')).toBeVisible()
```

- [ ] **Step 2: 运行管理页测试确认知识索引入口缺失**

Run: `cd observability-frontend && npm test -- --run src/pages/admin/AdminSettings.test.tsx src/pages/admin/KnowledgeIndexOperations.test.tsx`

Expected: FAIL，旧管理页未分离集群健康与软件能力，且无索引任务。

- [ ] **Step 3: 实现异常优先的管理页**

```ts
export interface CapabilityStatus {
  id: string
  label: string
  status: HealthState
  affectedClusters: string[]
  lastSuccessAt?: string
  reason?: string
}
```

健康组件折叠为“正常数/总数”；异常项显示影响范围、最近成功与入口。知识索引列表显示 knowledge/version、状态、attempt、最近错误、next retry；仅管理员可重试。

- [ ] **Step 4: 运行管理页测试和构建**

Run: `cd observability-frontend && npm test -- --run src/pages/admin && npm run build`

Expected: PASS；管理页不把同步异常解释为 Kubernetes 集群故障。

- [ ] **Step 5: 提交**

```bash
git add observability-frontend/src/pages/admin
git commit -m "feat(admin): separate platform capability operations"
```

### Task 14: 全量兼容、视觉回归、镜像构建和部署验收

**Files:**
- Modify: `observability-frontend/src/App.test.tsx`
- Modify: `observability-frontend/src/deployment/NginxCache.test.ts`
- Create: `observability-frontend/src/test/v3Acceptance.test.tsx`
- Modify: `deploy/helm/aiops/values-prod.yaml`
- Modify: `deploy/helm/aiops/templates/networkpolicy.yaml`
- Modify: `README.md`
- Modify: `docs/superpowers/plans/2026-09-10-aiops-cloud-operations-ui-v3.md`（只勾选已完成项和记录实测证据）

**Interfaces:**
- Consumes: Tasks 1–13 全部 canonical 路由、API、权限和组件。
- Produces: 可部署镜像、三档视口证据、旧深链兼容和最终验收记录。

- [ ] **Step 1: 写跨页面验收测试**

```tsx
it.each([
  ['/overview', '云平台运营态势'],
  ['/clusters/cluster-a', '集群详细总览'],
  ['/clusters/cluster-a/resources', '资源目录'],
  ['/clusters/cluster-a/graph', '资源关系图谱'],
  ['/clusters/cluster-a/assistant', '智能运维助手'],
  ['/clusters/cluster-a/knowledge', '运维知识'],
])('%s renders canonical heading', async (route, heading) => {
  renderAppAt(route, authorizedFixture)
  expect(await screen.findByRole('heading', { name: heading })).toBeVisible()
})
```

增加断言：没有全局搜索、prod/dev、生产平台、Workload 合并、综合健康分数；旧深链重定向到 canonical 路由且保留合法查询参数。

- [ ] **Step 2: 运行完整自动化基线**

Run: `cd ai-apm-query-go && go test ./... -count=1`

Run: `cd observability-frontend && npm test -- --run && npm run build`

Expected: 全部 PASS；任何现有失败必须先证明与本改动无关并记录，不能跳过。

- [ ] **Step 3: 构建镜像并更新部署值**

```bash
docker build -t aiops/observability-frontend:v3-20260910 observability-frontend
docker build -t aiops/ai-apm-query-go:v3-20260910 ai-apm-query-go
helm template aiops deploy/helm/aiops -f deploy/helm/aiops/values-prod.yaml
```

将已验证镜像 digest 写入生产值；NetworkPolicy 只开放 Query API 到 MySQL/Chroma 所需流量。先运行 schema migrator，再滚动 Query API，最后前端；验证迁移可重复执行。

- [ ] **Step 4: 真实浏览器视觉与运行验收**

Run: `playwright screenshot --viewport-size="1440,900" "http://127.0.0.1:30253/overview" /tmp/overview-1440.png`

Run: `playwright screenshot --viewport-size="1280,720" "http://127.0.0.1:30253/overview" /tmp/overview-1280.png`

Run: `playwright screenshot --viewport-size="1024,900" "http://127.0.0.1:30253/clusters/cluster-a/knowledge" /tmp/knowledge-1024.png`

逐页与 V3 PNG 对照：标题与 Scope、五秒判读、完整文本入口、无页面级横向溢出、Drawer 边界、图谱边方向/关系/来源、助手事实/知识引用与不确定性、知识范围/版本/审核/索引。再以真实账户验证跨租户/跨集群深链和会话返回 403/404、助手断流可幂等恢复、Chroma 不可用时正文仍可浏览且回答明确降级、动作仍需审批。

- [ ] **Step 5: 提交最终验收**

```bash
git add observability-frontend/src/test observability-frontend/src/App.test.tsx observability-frontend/src/deployment deploy/helm/aiops README.md docs/superpowers/plans/2026-09-10-aiops-cloud-operations-ui-v3.md
git commit -m "chore(release): verify aiops ui v3 deployment"
```

## Final self-review gate

- [ ] 正式规格各章的每项必须要求均能映射到 Task 1–14。
- [ ] 每个实现步骤均给出具体接口、代码、测试命令和预期结果，不保留占位描述。
- [ ] `HealthState`、resource Kind、knowledge status、Scope 谓词和 route 名称在前后任务中完全一致。
- [ ] 助手会话 Scope、结构化回答字段、SSE 事件、MySQL 持久化和 UI 应答框一一对应；没有从 Markdown 猜引用或从模型输出猜 capability。
- [ ] `/overview` 不读取活动集群；集群深链在服务端和客户端均校验授权。
- [ ] PVC canonical UID、DataVolume 与 NAD 关系在 API、图谱、详情和测试中使用同一含义。
- [ ] 旧业务/应用/中间件数据仍可兼容读取，但任何新一级入口和默认图谱都不显示它们。
- [ ] 最终部署使用镜像 digest，浏览器强刷后返回新静态资源且不命中旧 Nginx 缓存。
