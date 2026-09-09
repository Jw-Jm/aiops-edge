# AIOps Cloud Platform Resource Operations Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:test-driven-development for each implementation task and superpowers:verification-before-completion before every commit. Use superpowers:subagent-driven-development only when the user explicitly requests parallel agents. Track every checkbox in order.

**Goal:** 将现有前端收敛为单一生产环境、多 Kubernetes 集群、集群严格拥有全部平台资源的智能运维平台，并让工作台、资源、观测、调查、处置、报告和知识图谱形成一致、美观、可审计的资源运维体验。

**Architecture:** 以服务端确认的 tenant + cluster 为唯一授权根，以 typed resource + time range 为可选查询上下文。Query API 只增加资源目录、五域摘要和资源详情三个只读投影，复用现有 Graph Repository 与资产事实；Run、Evidence、Action 和 Graph DTO 继续是权威事实。前端通过一个 Scope store、一个资源类型系统、一个数据状态组件族和一套视觉 Token 统一所有页面。

**Tech Stack:** Go 1.24、React 18、TypeScript 5.6、React Router 6、Zustand 5、TanStack Query 5、Ant Design 5、ECharts 5、AntV G6 5、Vitest、Testing Library、Playwright

**Spec:** [AIOps 云平台资源运维信息架构设计方案](../specs/2026-09-08-aiops-information-architecture-design.md)

## Global Constraints

- 全程在 main 分支工作；每个任务完成测试后独立提交，开始任务前确认工作树只有本计划产生的改动。
- 产品只有生产环境。environment='prod' 只能存在于 Run/Chat 兼容适配器，不得出现在可选 UI、URL Scope 或持久化用户偏好中。
- 集群是唯一授权根。Catalog、Detail、Graph、Observe、Run 和 Action 都不得接受客户端 tenant 扩权，也不得用资源名称替代 canonical cluster id。
- Namespace 只属于 Kubernetes 资源或页面局部筛选，不能回到全局 Scope。
- 物理服务器、Kubernetes 节点、虚拟机必须保持不同 type、图标、中文名称和详情字段。
- 前端不拼接五套 API 推断资源身份；资源目录、摘要和详情使用新的只读 Query API 投影。
- 所有 query key 至少包含 tenantId、clusterId、entityUid 和时间窗口；切换集群时先取消并清空旧查询。
- Run 打开后使用冻结快照，禁止通过副作用改写活动 Scope。
- Action 入口由服务端 capabilities 决定；无 capability 时只读。
- 所有数据区域必须覆盖 loading、empty、error、partial、stale、forbidden 六类状态，且尺寸稳定。
- 知识图谱默认上限 80 个节点、200 条边；超限必须聚合并显示省略数量，不得静默截断。
- 最小操作宽度 1024px；视觉验收视口固定为 1440×900、1280×720、1024×768。
- 不更换 UI 框架，不改变数据库事实，不扩大自动执行权限，不删除旧深链兼容。

---

### Task 1: 建立唯一的资源类型系统

**Files:**

- Create: observability-frontend/src/features/resources/types.ts
- Create: observability-frontend/src/features/resources/resourceDomain.ts
- Create: observability-frontend/src/features/resources/resourceDomain.test.ts
- Modify: observability-frontend/src/api/graphContracts.ts

**Stable contracts:**

~~~ts
export type ResourceDomain = 'compute' | 'network' | 'storage' | 'kubernetes' | 'application'

export interface PlatformResourceRef {
  clusterId: string
  uid: string
  type: GraphEntityType
  domain: ResourceDomain
  name: string
  namespace?: string
}

export const SELECTABLE_RESOURCE_TYPES = {
  compute: ['physical_server', 'k8s_node', 'vm', 'vmi'],
  network: ['switch', 'switch_port', 'nic', 'network', 'nad'],
  storage: ['disk', 'storage_class', 'pv', 'pvc'],
  kubernetes: ['namespace', 'deployment', 'replicaset', 'statefulset', 'daemonset', 'pod', 'container', 'k8s_service', 'endpoint_slice'],
  application: ['business', 'application', 'service', 'middleware'],
} as const
~~~

- [ ] **Step 1: 写失败测试**

在 resourceDomain.test.ts 覆盖：

- physical_server、k8s_node、vm 分别映射到 compute，但 displayName 不相同。
- namespace 只映射到 kubernetes。
- cpu、dimm、alert、change、case、sel_event、migration 均不可作为一级选择结果。
- toPlatformResourceRef 在 cluster 不一致或类型不可选时返回明确错误。

- [ ] **Step 2: 运行测试并确认失败**

Run: cd observability-frontend && npm run test:run -- src/features/resources/resourceDomain.test.ts

Expected: FAIL，模块尚不存在。

- [ ] **Step 3: 实现纯函数**

在 resourceDomain.ts 导出：

~~~ts
export function resourceDomainOf(type: GraphEntityType): ResourceDomain | null
export function isSelectableResourceType(type: GraphEntityType): boolean
export function resourceTypeLabel(type: GraphEntityType): string
export function resourceLocation(resource: PlatformResourceRef): string
export function toPlatformResourceRef(entity: GraphEntity, activeClusterId: string): PlatformResourceRef
~~~

toPlatformResourceRef 必须校验 entity.cluster_id === activeClusterId；Kubernetes 资源可投影 namespace，其他域不携带空 namespace。

- [ ] **Step 4: 收紧 Graph 类型**

保留 Graph DTO 对未知服务端类型的容错，但业务函数不把未知字符串视为可选资源。增加 GraphEntityTypeKnown 联合类型，GraphEntity.entity_type 仍允许未知 string 进入“不可识别类型”降级态。

- [ ] **Step 5: 运行测试与类型检查**

Run: cd observability-frontend && npm run test:run -- src/features/resources/resourceDomain.test.ts src/api/knowledgeGraph.test.ts

Run: cd observability-frontend && npm run build

Expected: PASS。

- [ ] **Step 6: 提交**

~~~bash
git add observability-frontend/src/features/resources observability-frontend/src/api/graphContracts.ts
git commit -m "feat(frontend): add typed platform resource model"
~~~

---

### Task 2: 增加集群授权的资源只读投影

**Files:**

- Create: ai-apm-query-go/internal/api/resource_catalog.go
- Create: ai-apm-query-go/internal/api/resource_catalog_test.go
- Modify: ai-apm-query-go/internal/bootstrap/http.go
- Modify: ai-apm-query-go/internal/api/auth_internal_route_test.go

**HTTP contracts:**

~~~text
GET /api/v1/resources/catalog?domain=compute&type=physical_server&q=node&health=degraded&limit=50&cursor=<opaque>
GET /api/v1/resources/summary
GET /api/v1/resources/detail?uid=<url-encoded-entity-uid>
~~~

统一 envelope：

~~~go
type ResourceReadMeta struct {
    GeneratedAt  time.Time `json:"generated_at"`
    Partial      bool      `json:"partial"`
    Stale        bool      `json:"stale"`
    WarningCodes []string  `json:"warning_codes"`
}
~~~

- [ ] **Step 1: 写 handler 失败测试**

resource_catalog_test.go 使用 graph.NewMemoryRepository 注入 Handler，覆盖：

- Catalog 只返回 request authorization context 中 tenant + cluster 的资源。
- domain/type/health/q 过滤组合稳定，limit 范围为 1–100，cursor 为服务端编码游标。
- cpu、dimm、alert、change、case、sel_event、migration 被排除。
- Summary 固定返回 compute、network、storage、kubernetes、application 五项，即使某项 count 为 0。
- Detail 的 UID 跨集群返回 GRAPH_SCOPE_VIOLATION 对应的 403，不存在返回 404。
- graphRepo 不可用返回 503、partial=false、warning_codes 包含 RESOURCE_CATALOG_UNAVAILABLE。

- [ ] **Step 2: 运行测试并确认失败**

Run: cd ai-apm-query-go && go test ./internal/api -run 'TestResource(Catalog|Summary|Detail)'

Expected: FAIL，三个 handler 尚未注册。

- [ ] **Step 3: 实现只读投影**

在 resource_catalog.go 定义：

~~~go
func (h *Handler) ResourceCatalog(w http.ResponseWriter, r *http.Request)
func (h *Handler) ResourceSummary(w http.ResponseWriter, r *http.Request)
func (h *Handler) ResourceDetail(w http.ResponseWriter, r *http.Request)
~~~

实现要求：

- 使用 RequestAuthorizationContext 和 graphScope 建立范围，不读取 tenant 查询参数。
- 使用 GraphRepository.SearchEntities/GetEntity，UID 与 Graph DTO 一致。
- 类型到五个域的映射由后端常量维护；resource_catalog_test.go 按设计文档 4.1 节锁定五组完整类型集合，前端 resourceDomain.test.ts 锁定相同集合。
- Detail 只投影 entity attrs 中白名单字段、health、source、resolution、last_seen_ms 和 capabilities；不返回 provider URL、token、SQL、内部地址或原始栈。
- Catalog 首版 cursor 编码最后一个 name_key + entity_uid；排序固定为 health priority、name_key、entity_uid。
- meta.stale 基于同步时间阈值，partial 只来自明确来源缺失，不把空集合判为 partial。

- [ ] **Step 4: 注册公开只读路由**

在 http.go 注册：

~~~go
mux.HandleFunc("/api/v1/resources/catalog", handler.ResourceCatalog)
mux.HandleFunc("/api/v1/resources/summary", handler.ResourceSummary)
mux.HandleFunc("/api/v1/resources/detail", handler.ResourceDetail)
~~~

更新 auth_internal_route_test.go，证明它们走浏览器公开认证中间件，不进入内部 capability 路由。

- [ ] **Step 5: 运行后端验证**

Run: cd ai-apm-query-go && go test ./internal/api ./internal/graph ./internal/bootstrap

Expected: PASS。

- [ ] **Step 6: 提交**

~~~bash
git add ai-apm-query-go/internal/api/resource_catalog.go ai-apm-query-go/internal/api/resource_catalog_test.go ai-apm-query-go/internal/api/auth_internal_route_test.go ai-apm-query-go/internal/bootstrap/http.go
git commit -m "feat(query-api): expose cluster-scoped resource read models"
~~~

---

### Task 3: 接入前端资源 API 与查询键

**Files:**

- Create: observability-frontend/src/api/resources.ts
- Create: observability-frontend/src/api/resources.test.ts
- Modify: observability-frontend/src/query/keys.ts
- Modify: observability-frontend/src/query/keys.test.ts

**Client contracts:**

~~~ts
export interface ResourceReadMeta {
  generated_at: string
  partial: boolean
  stale: boolean
  warning_codes: string[]
}

export interface ResourceCatalogItem extends PlatformResourceRef {
  typeLabel: string
  location: string
  health: 'critical' | 'degraded' | 'risk' | 'healthy' | 'unknown'
  source: string
  lastSeenAt?: string
}

export function getResourceCatalog(params: ResourceCatalogParams, signal?: AbortSignal): Promise<ResourceCatalogResponse>
export function getResourceSummary(signal?: AbortSignal): Promise<ResourceSummaryResponse>
export function getResourceDetail(uid: string, signal?: AbortSignal): Promise<ResourceDetailResponse>
~~~

- [ ] **Step 1: 写 URL、解码和错误语义测试**

覆盖 q/domain/type/health/limit/cursor 编码、uid 双斜杠与中文编码、403/404/503 到 forbidden/notFound/unavailable 的稳定映射，以及 partial/stale/warning_codes 原样保留。

- [ ] **Step 2: 运行测试并确认失败**

Run: cd observability-frontend && npm run test:run -- src/api/resources.test.ts src/query/keys.test.ts

Expected: FAIL。

- [ ] **Step 3: 实现 API 与 query key**

新增：

~~~ts
queryKeys.resourceCatalog(context, filters)
queryKeys.resourceSummary(context)
queryKeys.resourceDetail(context, entityUid)
queryKeys.resourceGraph(context, entityUid, mode, depth, domains, relations)
~~~

所有 key 均通过现有 scope(context) 包含 tenantId、activeClusterId、entityUid、from、to；filters 使用排序后的稳定序列化结果。

- [ ] **Step 4: 运行验证并提交**

Run: cd observability-frontend && npm run test:run -- src/api/resources.test.ts src/query/keys.test.ts

Expected: PASS。

~~~bash
git add observability-frontend/src/api/resources.ts observability-frontend/src/api/resources.test.ts observability-frontend/src/query/keys.ts observability-frontend/src/query/keys.test.ts
git commit -m "feat(frontend): add resource catalog client"
~~~

---

### Task 4: 简化 Scope 并建立兼容边界

**Files:**

- Modify: observability-frontend/src/features/scope/types.ts
- Modify: observability-frontend/src/store/scopeStore.ts
- Modify: observability-frontend/src/store/scopeStore.test.ts
- Create: observability-frontend/src/features/scope/runScopeAdapter.ts
- Create: observability-frontend/src/features/scope/runScopeAdapter.test.ts
- Modify: observability-frontend/src/api/scopeRuntime.ts

**Target state:**

~~~ts
export interface ActiveScope {
  tenantId: string
  clusterId: string
  resource?: PlatformResourceRef
  timeRange: TimeRange
}

export interface RunScopeSnapshot extends ActiveScope {
  mode: 'snapshot'
  runId: string
  timeRange: Extract<TimeRange, { mode: 'absolute' }>
}
~~~

- [ ] **Step 1: 先把测试改成目标行为**

覆盖：

- Scope 类型和 store 不再暴露 setEnvironment/setNamespace。
- initialize 只接受 /me.active_scope 和 /me.available_clusters。
- switchCluster 在 POST /me/scope 与回读确认成功后才提交；成功清空 resource，失败恢复旧范围。
- setResource 拒绝 resource.clusterId !== active cluster。
- 持久化只保存 preferredClusterId，不保存授权 Scope 或资源。
- resetScopeQueries 在切换前取消旧请求，回读成功后再发新范围查询。

- [ ] **Step 2: 运行测试并确认旧实现失败**

Run: cd observability-frontend && npm run test:run -- src/store/scopeStore.test.ts

Expected: FAIL，旧环境和 Namespace API 仍存在。

- [ ] **Step 3: 实现新 Scope store**

保留 authScope 服务端事实与 context UI 状态也可以，但导出的 useActiveScope 必须只产生 tenantId、clusterId、resource、timeRange。删除 Environment、ScopeContext.namespace、DEFAULT_SCOPE_CONTEXT.environment。

- [ ] **Step 4: 实现 Run/Chat 兼容适配器**

~~~ts
export function toLegacyRunScope(scope: ActiveScope): {
  environment: 'prod'
  cluster_id: string
  namespace?: string
  target_resource_id?: string
  target_resource_type?: string
  time_start: string
  time_end: string
}
~~~

相对时间以提交瞬间转换成绝对 UTC；namespace 只从 Kubernetes resource.namespace 投影。

- [ ] **Step 5: 搜索并消除越界读取**

Run: rg -n 'setEnvironment|setNamespace|context\.environment|context\.namespace' observability-frontend/src

Expected: 结果为 0。environment='prod' 只允许以 runScopeAdapter.ts 返回字段和 runScopeAdapter.test.ts 断言的形式存在，不再通过 context 读取。

- [ ] **Step 6: 运行测试、构建并提交**

Run: cd observability-frontend && npm run test:run -- src/store/scopeStore.test.ts src/features/scope/runScopeAdapter.test.ts src/query/client.test.ts

Run: cd observability-frontend && npm run build

Expected: PASS。

~~~bash
git add observability-frontend/src/features/scope observability-frontend/src/store/scopeStore.ts observability-frontend/src/store/scopeStore.test.ts observability-frontend/src/api/scopeRuntime.ts
git commit -m "refactor(frontend): make cluster the sole scope root"
~~~

---

### Task 5: 重做 Scope Bar 与统一资源选择器

**Files:**

- Create: observability-frontend/src/features/scope/ResourcePicker.tsx
- Create: observability-frontend/src/features/scope/ResourcePicker.test.tsx
- Modify: observability-frontend/src/features/scope/ScopeBar.tsx
- Modify: observability-frontend/src/features/scope/ScopeBar.test.tsx
- Modify: observability-frontend/src/index.css

**Component contract:**

~~~ts
export interface ResourcePickerProps {
  clusterId: string
  value?: PlatformResourceRef
  disabled?: boolean
  onChange(resource?: PlatformResourceRef): void
}
~~~

- [ ] **Step 1: 写交互失败测试**

覆盖：

- Scope Bar 只出现“集群”“资源”“时间范围”，不出现环境、全局 Namespace、“节点”泛称。
- 单集群显示只读集群身份，多集群显示搜索下拉。
- ResourcePicker 按五域分组，显示类型、名称、定位路径和健康文字。
- 输入少于 2 字符不请求；250ms 防抖；“仅看异常”和最近访问可用。
- 同名 physical_server/k8s_node/vm 能被明确区分。
- 403 清空选中资源并显示 role=alert；切换集群期间选择器禁用。
- URL resource 参数刷新恢复，非法或跨集群 UID 被移除而不是选第一条。

- [ ] **Step 2: 运行测试并确认失败**

Run: cd observability-frontend && npm run test:run -- src/features/scope/ScopeBar.test.tsx src/features/scope/ResourcePicker.test.tsx

Expected: FAIL。

- [ ] **Step 3: 实现控件与样式**

使用 Ant Design Select/Popover/Segmented 现有组件；结果行最小高度 52px，名称一行、类型与路径一行、状态为带文字的 Tag。资源值使用 entity UID，不使用 name。

- [ ] **Step 4: 补齐键盘和状态**

Tab 进入、方向键移动、Enter 选择、Escape 关闭；本任务先使用等高 Ant Design Skeleton、Empty 和 Alert 保证交互完整，Task 6 再统一替换为 DataState，并复跑本任务测试。

- [ ] **Step 5: 运行测试、构建并提交**

Run: cd observability-frontend && npm run test:run -- src/features/scope/ScopeBar.test.tsx src/features/scope/ResourcePicker.test.tsx

Run: cd observability-frontend && npm run build

Expected: PASS。

~~~bash
git add observability-frontend/src/features/scope observability-frontend/src/index.css
git commit -m "feat(frontend): add cluster resource scope controls"
~~~

---

### Task 6: 建立全站视觉基础与统一显示状态

**Files:**

- Modify: observability-frontend/src/theme/tokens.ts
- Modify: observability-frontend/src/theme/tokens.test.ts
- Create: observability-frontend/src/components/display/DataState.tsx
- Create: observability-frontend/src/components/display/DataState.test.tsx
- Create: observability-frontend/src/components/display/RawDataPanel.tsx
- Create: observability-frontend/src/components/display/RawDataPanel.test.tsx
- Modify: observability-frontend/src/features/scope/ScopeBar.tsx
- Modify: observability-frontend/src/features/scope/ResourcePicker.tsx
- Modify: observability-frontend/src/index.css

**Visual tokens:**

~~~ts
export const operationsPalette = {
  canvas: '#F5F7FA',
  surface: '#FFFFFF',
  surfaceSubtle: '#F8FAFC',
  text: '#172033',
  textSecondary: '#5B667A',
  border: '#DDE3EA',
  interaction: '#3157D5',
  critical: '#C9362B',
  degraded: '#C46816',
  risk: '#A46F0A',
  healthy: '#18864B',
} as const
~~~

- [ ] **Step 1: 写 Token 与状态组件失败测试**

覆盖颜色值、圆角 8px、表格行高 44px、点击目标最小 36px；DataState 六种 kind 均有标题、说明和准确 ARIA；错误态可重试；RawDataPanel 默认折叠、格式化、复制、超过 200KB 截断并提示。

- [ ] **Step 2: 运行测试并确认失败**

Run: cd observability-frontend && npm run test:run -- src/theme/tokens.test.ts src/components/display/DataState.test.tsx src/components/display/RawDataPanel.test.tsx

Expected: FAIL。

- [ ] **Step 3: 实现组件和 CSS primitives**

DataState 的 kind 固定为 loading | empty | error | partial | stale | forbidden。页面骨架使用 .page-grid、.page-header、.surface-card、.resource-layout、.context-panel；禁止页面继续新增散落的主色和阴影常量。

将 ScopeBar 和 ResourcePicker 的临时 Skeleton、Empty、Alert 替换为 DataState，并保持 Task 5 已通过的键盘与错误语义不变。

- [ ] **Step 4: 加入响应式与减少动态效果**

- >=1440px：侧栏 216px，资源 264px / minmax(0,1fr) / 336px。
- 1280–1439px：侧栏 64px，资源 224px / minmax(0,1fr) / 304px。
- 1024–1279px：目录 Drawer、上下文 Tab、内容单列。
- <1024px：显示只读提示并隐藏生产处置主按钮。
- prefers-reduced-motion 下关闭非必要 transition 和图布局动画。

- [ ] **Step 5: 运行验证并提交**

Run: cd observability-frontend && npm run test:run -- src/theme/tokens.test.ts src/components/display src/features/scope/ScopeBar.test.tsx src/features/scope/ResourcePicker.test.tsx

Run: cd observability-frontend && npm run build

Expected: PASS。

~~~bash
git add observability-frontend/src/theme observability-frontend/src/components/display observability-frontend/src/features/scope/ScopeBar.tsx observability-frontend/src/features/scope/ResourcePicker.tsx observability-frontend/src/index.css
git commit -m "feat(frontend): unify visual tokens and data states"
~~~

---

### Task 7: 重构资源中心为集群全景与类型化详情

**Files:**

- Modify: observability-frontend/src/pages/Resources/index.tsx
- Modify: observability-frontend/src/pages/Resources/index.test.tsx
- Modify: observability-frontend/src/pages/Resources/ResourceCenter.tsx
- Create: observability-frontend/src/pages/Resources/ClusterPanorama.tsx
- Create: observability-frontend/src/pages/Resources/ClusterPanorama.test.tsx
- Create: observability-frontend/src/pages/Resources/ResourceDirectory.tsx
- Create: observability-frontend/src/pages/Resources/ResourceDirectory.test.tsx
- Modify: observability-frontend/src/pages/Resources/ResourceIdentity.tsx
- Create: observability-frontend/src/pages/Resources/resourceDetailModel.ts
- Create: observability-frontend/src/pages/Resources/resourceDetailModel.test.ts

- [ ] **Step 1: 写页面与投影失败测试**

覆盖：

- 无资源选择时默认展示集群身份、采集完整度、五域健康、异常队列、容量风险、近期变更，不渲染完整图谱。
- 二级导航固定为集群全景、计算、网络、存储、Kubernetes、应用服务、容量、关系探索。
- 选中资源时显示目录/详情/上下文三栏，并按视口切换 Drawer/Tab。
- physical_server 显示厂商/型号/序列号/BMC/组件健康；k8s_node 显示角色/版本/Ready/污点/容量/宿主；vm 显示 Namespace/节点/CPU/内存/磁盘/网络/迁移。
- 没有字段显示“未提供”和来源，不显示“未知 Namespace/Node”。
- 后端 capability 决定“进入处置”是否出现。

- [ ] **Step 2: 运行测试并确认失败**

Run: cd observability-frontend && npm run test:run -- src/pages/Resources

Expected: FAIL。

- [ ] **Step 3: 实现集群全景**

ClusterPanorama 使用 getResourceSummary，并将五域卡控制在同一高度。异常队列排序固定为 severity、impactCount、duration、recentChange；集群级问题使用“集群范围”标签。

- [ ] **Step 4: 实现目录和深链**

ResourceDirectory 使用 catalog 游标分页；domain/type/health/q 写入 URL。resource 使用 encodeURIComponent(entity UID)；后退/前进必须恢复选择与滚动位置。

- [ ] **Step 5: 实现类型化详情**

resourceDetailModel.ts 导出：

~~~ts
export interface DetailSection { key: string; title: string; fields: DetailField[] }
export function projectResourceDetail(detail: ResourceDetailResponse): DetailSection[]
~~~

仅展示对应类型白名单字段；身份、健康、关键指标、事件、变更、依赖、调查、动作和数据质量使用统一 Section。

- [ ] **Step 6: 运行测试、构建并提交**

Run: cd observability-frontend && npm run test:run -- src/pages/Resources src/api/resources.test.ts

Run: cd observability-frontend && npm run build

Expected: PASS。

~~~bash
git add observability-frontend/src/pages/Resources
git commit -m "feat(frontend): build cluster resource operations center"
~~~

---

### Task 8: 重构知识图谱视觉、布局与降级体验

**Files:**

- Create: observability-frontend/src/components/graph/graphPresentation.ts
- Create: observability-frontend/src/components/graph/graphPresentation.test.ts
- Modify: observability-frontend/src/components/graph/GraphMap.tsx
- Modify: observability-frontend/src/components/graph/GraphMap.test.tsx
- Modify: observability-frontend/src/components/graph/GraphExplorer.tsx
- Modify: observability-frontend/src/components/graph/GraphContextPanel.tsx
- Modify: observability-frontend/src/components/graph/GraphContextPanel.test.tsx
- Create: observability-frontend/src/components/graph/GraphToolbar.tsx
- Create: observability-frontend/src/components/graph/GraphLegend.tsx
- Create: observability-frontend/src/components/graph/GraphRelationList.tsx
- Create: observability-frontend/src/components/graph/GraphRelationList.test.tsx
- Modify: observability-frontend/src/pages/Resources/MainFailureChain.tsx
- Modify: observability-frontend/src/pages/Resources/MainFailureChain.test.tsx

**Presentation contract:**

~~~ts
export type GraphViewMode = 'resource-relations' | 'failure-chain' | 'expert'
export type GraphLayoutMode = 'radial' | 'hierarchy-tb' | 'dag-lr'

export interface GraphDisplayModel {
  nodes: GraphDisplayNode[]
  edges: GraphDisplayEdge[]
  omittedByType: Record<string, number>
  relationRows: GraphRelationRow[]
  meta: GraphMeta
}

export function buildGraphDisplayModel(
  graph: GraphSubgraph,
  options: { mode: GraphViewMode; centerUid: string; maxNodes: 80; maxEdges: 200 }
): GraphDisplayModel
~~~

- [ ] **Step 1: 写纯模型失败测试**

覆盖：

- resource-relations 使用 radial；包含/宿主/绑定关系使用 hierarchy-tb；failure-chain 使用 dag-lr。
- 节点按资源域分组，中心节点保留，超过 80/200 后生成“还有 N 个”聚合节点并计入 omittedByType。
- critical/degraded/risk/healthy/unknown 使用独立边框和文字，不改变资源域类型色。
- CONTAINS/HOSTS/RUNS_ON/USES_VOLUME/ATTACHED_TO/DEPENDS_ON 映射稳定中文标签。
- 只有 propagates_failure=true 的故障链边使用红/橙；结构边中性实线、推测边虚线。
- physical_server、k8s_node、vm 的 iconKey 和 typeLabel 不同。
- relationRows 与当前可见节点/边等价。

- [ ] **Step 2: 运行模型测试并确认失败**

Run: cd observability-frontend && npm run test:run -- src/components/graph/graphPresentation.test.ts

Expected: FAIL。

- [ ] **Step 3: 实现 GraphMap 生命周期与节点视觉**

节点改为中性卡片形态：类型图标、最多两行名称、中文类型、健康角标；选中态使用蓝色外环。缩放低于 0.65 隐藏边标签，选中或放大时恢复。节点超过 50 或 prefers-reduced-motion 时关闭动画。effect cleanup 必须 destroy G6 实例并解绑 ResizeObserver。

- [ ] **Step 4: 实现工具栏、图例和过滤**

GraphToolbar 固定提供适应画布、放大、缩小、回到中心、全屏、深度 1–3、资源域、关系过滤。所有 icon button 有中文 aria-label 和 tooltip；过滤不能改变原始 Graph DTO。

- [ ] **Step 5: 实现等价关系列表和上下文面板**

GraphRelationList 支持键盘、排序和定位节点。GraphContextPanel 显示身份、健康、邻居、影响、证据、数据质量、capability；GraphMap 失败时关系列表和资源详情仍可用。

- [ ] **Step 6: 实现六类图谱状态**

loading 使用与画布等高骨架；empty 提供刷新同步；error 展示来源、request ID 和重试；partial 展示已加载数量与 warning code；stale 展示快照时间；forbidden 不泄露隐藏节点数量。

- [ ] **Step 7: 运行图谱测试、构建并提交**

Run: cd observability-frontend && npm run test:run -- src/components/graph src/pages/Resources/MainFailureChain.test.tsx

Run: cd observability-frontend && npm run build

Expected: PASS。

~~~bash
git add observability-frontend/src/components/graph observability-frontend/src/pages/Resources/MainFailureChain.tsx observability-frontend/src/pages/Resources/MainFailureChain.test.tsx
git commit -m "feat(frontend): redesign resource knowledge graph"
~~~

---

### Task 9: 将工作台与观测中心改为资源优先

**Files:**

- Modify: observability-frontend/src/pages/Overview/index.tsx
- Modify: observability-frontend/src/pages/Overview/Overview.test.tsx
- Modify: observability-frontend/src/pages/Overview/IssueQueue.tsx
- Modify: observability-frontend/src/pages/Overview/priority.ts
- Modify: observability-frontend/src/pages/Overview/priority.test.ts
- Modify: observability-frontend/src/pages/Observe/index.tsx
- Modify: observability-frontend/src/pages/Observe/index.test.tsx

- [ ] **Step 1: 写工作台失败测试**

覆盖全部五域问题不会因 service 为空被丢弃；问题项显示 typed resource、集群、影响、持续时间、数据质量、变更、调查/处置状态；主动作严格由状态机映射。首屏 8/4 网格，普通资源 KPI 在第二层。

- [ ] **Step 2: 写观测中心失败测试**

覆盖默认“问题”视图；告警、指标、日志、Trace、事件、变更继承当前 cluster/resource/timeRange；选中资源显示明确 filter chip；空、来源错误、部分、陈旧不被映射为健康；Grafana 仅为专家入口。

- [ ] **Step 3: 运行测试并确认失败**

Run: cd observability-frontend && npm run test:run -- src/pages/Overview src/pages/Observe

Expected: FAIL。

- [ ] **Step 4: 实现资源问题投影与页面**

priority.ts 的排序输入使用 severity、impactCount、durationMs、hasRecentChange；缺少 resource 时显式生成 cluster-scope view model，不伪造 service。

- [ ] **Step 5: 统一观测查询范围与显示状态**

每个 Tab 使用 queryKeys 中相同 ActiveScope；切换 Tab 保留 URL，但不保留上个资源的数据。所有图表补标题、单位、时间范围、来源、最后更新时间，并区分零值和无数据。

- [ ] **Step 6: 运行测试、构建并提交**

Run: cd observability-frontend && npm run test:run -- src/pages/Overview src/pages/Observe

Run: cd observability-frontend && npm run build

Expected: PASS。

~~~bash
git add observability-frontend/src/pages/Overview observability-frontend/src/pages/Observe
git commit -m "feat(frontend): make overview and observe resource-first"
~~~

---

### Task 10: 将调查与 Chat 接入 typed resource 和冻结快照

**Files:**

- Modify: observability-frontend/src/features/investigation/draft.ts
- Modify: observability-frontend/src/features/investigation/draft.test.ts
- Modify: observability-frontend/src/pages/investigation/NewInvestigation.tsx
- Create: observability-frontend/src/pages/investigation/NewInvestigation.test.tsx
- Modify: observability-frontend/src/pages/investigation/InvestigationCenter.tsx
- Modify: observability-frontend/src/pages/investigation/InvestigationCenter.test.tsx
- Modify: observability-frontend/src/pages/investigation/InvestigationShell.tsx
- Modify: observability-frontend/src/pages/investigation/IntelligentInvestigation.tsx
- Modify: observability-frontend/src/pages/investigation/IntelligentInvestigation.test.tsx
- Modify: observability-frontend/src/pages/ai/AiChat.tsx

- [ ] **Step 1: 写草稿与创建行为失败测试**

覆盖问题、资源、Chat 三个来源都能生成 PlatformResourceRef；页面加载不创建 Run；用户提交时 toLegacyRunScope 固定 prod、Kubernetes 才投影 namespace、相对时间冻结为绝对 UTC。

- [ ] **Step 2: 写 Run 只读快照失败测试**

打开 Run 只显示 cluster/resource type/name/absolute window；不得调用 setResource、switchCluster 或 setTimeRange。主故障链只使用持久化 graph-context；insufficient_evidence 时不显示确定性根因措辞。

- [ ] **Step 3: 运行测试并确认失败**

Run: cd observability-frontend && npm run test:run -- src/features/investigation src/pages/investigation

Expected: FAIL。

- [ ] **Step 4: 实现新建调查与队列**

表单资源字段使用 ResourcePicker，不再使用自由文本 resourceId；targetType 由资源决定且只读。集群级调查通过单独“集群范围”选项创建，不伪造 k8s_cluster 为普通资源。

- [ ] **Step 5: 实现三栏调查工作台**

左栏资源身份/影响链/冻结 Scope/变更；中栏 Evidence 时间线与 RawDataPanel；右栏结论/候选假设/反证/缺失证据/建议动作。1024–1279px 将左右栏收为 Tabs。

- [ ] **Step 6: 收敛 Chat**

Chat 顶部展示活动资源标签；只读查询携带 typed UID。转调查只生成 draft URL，不能直接创建 Action 或 Run；切换集群后清除旧 Chat 资源引用。

- [ ] **Step 7: 运行测试、构建并提交**

Run: cd observability-frontend && npm run test:run -- src/features/investigation src/pages/investigation src/pages/ai

Run: cd observability-frontend && npm run build

Expected: PASS。

~~~bash
git add observability-frontend/src/features/investigation observability-frontend/src/pages/investigation observability-frontend/src/pages/ai/AiChat.tsx
git commit -m "feat(frontend): bind investigations to typed resources"
~~~

---

### Task 11: 将处置与报告改为资源事件主线

**Files:**

- Modify: observability-frontend/src/pages/Actions/actionModel.ts
- Modify: observability-frontend/src/pages/Actions/actionModel.test.ts
- Modify: observability-frontend/src/pages/Actions/ActionCenter.tsx
- Create: observability-frontend/src/pages/Actions/ActionCenter.test.tsx
- Modify: observability-frontend/src/pages/Reports/index.tsx
- Create: observability-frontend/src/pages/Reports/index.test.tsx

- [ ] **Step 1: 写 capability 和动作审计失败测试**

覆盖：

- physical_server、k8s_node、workload 的动作仅在响应 capability 存在时出现。
- VM、网络、存储没有 capability 时只读。
- 审批按钮只对 approver/admin 可见；服务端拒绝仍映射为 forbidden。
- 决策携带 action_version 和 idempotency key；资源版本变化显示“需要重新预检”。
- 列表与详情展示 typed target、来源 Run、风险、预检、审批、执行、验证、回滚。

- [ ] **Step 2: 写报告失败测试**

报告标题和摘要以资源事件为主语，包含资源身份、集群、影响链、证据、根因、动作、恢复验证、容量趋势；只有 type=service 时才使用“服务”称谓。

- [ ] **Step 3: 运行测试并确认失败**

Run: cd observability-frontend && npm run test:run -- src/pages/Actions src/pages/Reports

Expected: FAIL。

- [ ] **Step 4: 实现动作与报告页面**

资源详情只链接 /actions?resource=<uid>；审批和执行表单全部留在 ActionCenter。报告中的图表使用统一单位和状态；缺失证据展示 partial，不生成前端推断文本。

- [ ] **Step 5: 运行测试、构建并提交**

Run: cd observability-frontend && npm run test:run -- src/pages/Actions src/pages/Reports

Run: cd observability-frontend && npm run build

Expected: PASS。

~~~bash
git add observability-frontend/src/pages/Actions observability-frontend/src/pages/Reports
git commit -m "feat(frontend): align actions and reports to resources"
~~~

---

### Task 12: 统一壳层、兼容路由与全链路视觉验收

**Files:**

- Modify: observability-frontend/src/App.tsx
- Modify: observability-frontend/src/layout/navConfig.ts
- Modify: observability-frontend/src/layout/navConfig.test.ts
- Modify: observability-frontend/src/index.css
- Create: tests/manual-e2e/ia-resources.js
- Create: tests/manual-e2e/ia-graph.js
- Modify: tests/manual-e2e/ia-workbench.js
- Modify: tests/manual-e2e/ia-investigation.js
- Modify: tests/manual-e2e/ia-actions.js
- Modify: tests/manual-e2e/lib/harness.js
- Create: docs/superpowers/verification/2026-09-09-aiops-resource-operations.md

- [ ] **Step 1: 写导航和旧深链测试**

一级导航固定为工作台、调查、资源、观测、处置、报告；管理员追加系统管理。验证：

~~~text
/observability/service        -> /resources?domain=application
/observability/relationships  -> /resources?view=graph
/observability/vms            -> /resources?domain=compute&type=vm
/infra/k8s                    -> /resources?domain=kubernetes
/hardware                     -> /resources?domain=compute&type=physical_server
/capacity                     -> /resources?view=capacity
~~~

- [ ] **Step 2: 运行单元测试并确认路由差异**

Run: cd observability-frontend && npm run test:run -- src/layout/navConfig.test.ts src/App.test.tsx

Expected: 在实现前至少一个新目标失败。

- [ ] **Step 3: 更新壳层与路由**

顶部第一行仅放全局搜索、通知、用户；第二行固定 Scope Bar。页面标题区最多两个主动作。旧路由使用 Navigate replace，保留查询参数中与新模型兼容的 resource/timeRange。

- [ ] **Step 4: 编写五条 Playwright 主链**

ia-resources.js：

1. 切换集群 → 集群全景 → 物理服务器 → 硬件/关系/观测。
2. Kubernetes Workload → Namespace 局部筛选 → 调查草稿 → 显式创建 Run。
3. 虚拟机 → 网络与存储关系 → 正式调查。

ia-graph.js：

4. 资源搜索 → 一跳关系 → 域过滤 → 关系列表 → 全屏/回中心。

ia-actions.js：

5. Run 主故障链 → Action Proposal → 审批 → 执行/验证回显。

每条链在 harness 中采集 1440×900、1280×720、1024×768；截图名称固定为 route--viewport--state.png。

- [ ] **Step 5: 执行自动验证**

Run: cd observability-frontend && npm run test:run

Run: cd observability-frontend && npm run build

Run: cd ai-apm-query-go && go test ./internal/api ./internal/graph ./internal/bootstrap

Run: node tests/manual-e2e/ia-workbench.js

Run: node tests/manual-e2e/ia-resources.js

Run: node tests/manual-e2e/ia-graph.js

Run: node tests/manual-e2e/ia-investigation.js

Run: node tests/manual-e2e/ia-actions.js

Expected: 全部 PASS。

- [ ] **Step 6: 逐页人工视觉验收**

在 verification 文档记录每个视口的：

- 对齐、留白、字体层级、卡片高度、表格密度。
- 横向溢出、标签遮挡、关键语义截断、Drawer/Tab 降级。
- 状态色文字、图标、键盘焦点、ARIA、对比度。
- 图表标题、单位、时间范围、来源、零值/无数据差异。
- loading/empty/error/partial/stale/forbidden 六态。
- 图谱节点重叠、两行标签、边标签缩放、聚合数量、图例、关系列表一致性。

任何一项失败都回到对应任务修复并重跑该页面截图，不以“功能可用”替代视觉通过。

- [ ] **Step 7: 检查旧概念与原始显示**

Run: rg -n "prod.*staging|staging.*test|setEnvironment|全局命名空间|context\.namespace|JSON\.stringify\(" observability-frontend/src

Expected: environment 选项、全局 Namespace 和主界面 JSON.stringify 为 0；RawDataPanel 内受控格式化可作为唯一例外并由测试覆盖。

- [ ] **Step 8: 完成验证记录并提交**

verification 文档写入测试命令、退出码、截图目录、已知非阻塞限制和回滚点。最终检查：

Run: git diff --check

Run: git status --short

Expected: diff check 无输出，status 只包含本任务预期文件。

~~~bash
git add observability-frontend/src/App.tsx observability-frontend/src/layout observability-frontend/src/index.css tests/manual-e2e docs/superpowers/verification/2026-09-09-aiops-resource-operations.md
git commit -m "feat(frontend): complete cloud resource operations redesign"
~~~

---

## Release Gate

实施全部完成后才允许构建镜像并同步环境。发布前必须同时满足：

1. main 工作树干净，全部 12 个任务提交存在且按顺序可追溯。
2. 前端全量 Vitest、TypeScript/Vite build、Query API 相关 Go tests 全部通过。
3. 五条 Playwright 主链和三档视口截图全部通过。
4. 浏览器中不存在环境选择器、全局 Namespace 或无类型“节点”选择器。
5. 资源中心默认是集群全景，五个资源域均有真实状态或明确空/缺失说明。
6. 知识图谱三种模式、聚合上限、工具栏、图例、等价关系列表和六类状态均可验证。
7. 镜像 tag 或 digest 与 main HEAD 一致，部署清单引用该新 digest，rollout 完成后再做运行态冒烟。
8. 回滚点为发布前 deployment revision 与前一镜像 digest，不通过验收时回滚，不在生产页面临时修补。
