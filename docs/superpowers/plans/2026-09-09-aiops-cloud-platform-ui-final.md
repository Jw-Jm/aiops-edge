# AIOps 多集群云平台 UI 最终实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development to implement this plan task-by-task, superpowers:test-driven-development for every production change, and superpowers:verification-before-completion before every commit. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 将现有上一版资源运维 UI 重建为已确认的单一生产环境、多 Kubernetes 集群云平台运维产品，并让平台健康、集群健康、容器、KubeVirt、观测、调查、处置、报告和知识图谱严格遵循正式规格与实际渲染基线。

**Architecture:** 服务端以当前用户获授权的真实 Kubernetes 集群为授权根，分别提供不受活动集群影响的平台聚合、明确集群下的健康/资源只读投影，以及可分页的图谱关系事实。前端以路径中的 `clusterId` 表达集群工作区，以 canonical resource UID 表达资源身份；平台级和集群级 query cache 完全隔离。页面共享一套有边界的数据区域、健康语义、视觉 Token 和响应式壳层，但不复用旧页面布局。

**Tech Stack:** Go 1.25（`toolchain go1.26.6`）、React 18.3、TypeScript 5.6、React Router 6.28、Zustand 5、TanStack Query 5、Ant Design 5.22、ECharts 5.5、AntV G6 5.1、Vitest 2.1、Testing Library 16、Playwright/Chromium（仓库手工 E2E harness）

**Spec:** [正式设计方案](../specs/2026-09-08-aiops-information-architecture-design.md)

**Visual contract:** [UI 渲染与编码指南](../specs/assets/ui-v2/UI_RENDER_GUIDE.md) · [可运行 UI 原型](../specs/assets/ui-v2/aiops-ui-prototype.html)

**Architecture audit:** `/Users/mssc/.cursor/projects/empty-window/canvases/aiops-cloud-platform-architecture-audit-2026-09-09.canvas.tsx`

## 设计终审结论

- 正式规格、UI 指南、14 张 Chromium 渲染图、可运行原型和架构审核 Canvas 的产品方向一致，可以进入编码。
- 正式规格是语义与数据合同的最高优先级；PNG/HTML 是视觉层级和响应式基线，示例数据不得进入生产代码或接口失败回退。
- 渲染图有两处必须按规格修正后实现：聚合边必须在边标签直接显示数量，例如 `Kubernetes Service ─选择 8 个→ Pod`；网络关系必须保持 `VMI ─连接到→ NAD`，不得因布局把 `virt-launcher Pod` 误作关系源。
- 2026-09-08 旧计划已经执行过，但其“五资源域、全局资源选择器、宽侧栏、全局搜索、径向图谱”目标已失效；本计划从当前 `main` 代码继续迁移，不重复已正确的授权、Run、Evidence、Action 和审计底层。

## Global Constraints

- 所有编码、测试、提交和发布都在 `main` 进行；不得创建功能分支或 worktree。每个任务开始前运行 `git status --short --branch`，只允许前一任务已提交后的干净工作树。
- 多智能体执行时，每个任务只分配给一个新智能体；共享工作树禁止并行写。任务必须按依赖顺序完成红灯测试、实现、验证和单独 commit，下一智能体从该 commit 继续。
- 产品只有生产环境；`prod/dev/staging/test`、`生产平台`、`生产集群`不得作为 UI 层级、可选 Scope、URL 参数或用户偏好。旧 Run/Chat 必需的 `environment: 'prod'` 只能由兼容适配器生成。
- 顶栏唯一主要上下文控件是实际 Kubernetes 集群选择器；无全局搜索、全局 Namespace、节点或资源选择器。时间范围只出现在需要时间上下文的页面内容区。
- `/overview` 始终聚合当前用户全部授权集群且忽略活动集群；`/clusters/:clusterId/**` 必须先完成 `/me/scope` 提交和 `/me` 回读确认后渲染。
- 一级资源只分为 `container` 与 `kubevirt`；`foundation` 仅为辅助诊断入口。`business/application/service/middleware` 默认隐藏但兼容数据保留；`k8s_service` 必须显示为 Kubernetes Service，不能与 APM `service` 混同。
- 健康状态只有 `healthy/degraded/critical/unknown` 四态；风险等级、流程进度和健康是不同字段。浏览器不计算 0–100 综合健康分数，不把资源计数、单一利用率或 API 失败映射为健康。
- 每个数据区域必须可验证正常、加载、空、错误、部分、陈旧、无权限七种呈现；接口失败时禁止回退到原型示例数据。
- 页面正文 14px，辅助文字不低于 12px，标题 22px；1440×900、1280×720、1024×768 不得有页面级横向溢出、按钮重叠、浮层出界或只能靠 Tooltip 读取的关键文本。
- 图谱 `80 nodes / 200 edges` 只是首屏画布预算，不是查询或总量上限。超量资源必须有准确总数、聚合节点、带数量的聚合边、游标分页和等价关系列表。
- 每条可见边都必须直接读成 `源资源 ─中文关系→ 目标资源`；事实为实线，推断为虚线且文字包含“疑似”，边检查器显示来源、同步时间、事实状态和证据。
- 高风险动作继续由服务端 capability、预检、审批、执行、验证和审计控制；本计划不扩大自动执行权限，不改写 Run/Action 持久化语义。
- 不新增 UI 框架、状态库、图谱库或视觉依赖；使用仓库现有依赖和图标系统。

## 执行依赖与提交顺序

```text
Task 1 资源与健康类型
  ├─> Task 2 可分页资源只读投影
  ├─> Task 3 平台/集群健康接口
  └─> Task 4 图谱分页与关系事实
          ↓
Task 5 前端 API、Query Key 与路由上下文
          ↓
Task 6 全新壳层、登录与显示基础
          ↓
Task 7 平台总览与集群总览
          ↓
Task 8 资源目录与资源详情
          ↓
Task 9 观测与调查
          ↓
Task 10 处置、报告与系统管理
          ↓
Task 11 知识图谱三模式
          ↓
Task 12 旧深链兼容与旧 UI 清理
          ↓
Task 13 单测、E2E 与视觉验收
          ↓
Task 14 提交闭合、镜像发布与运行态验收
```

---

### Task 1: 冻结四态健康和三组资源类型系统

**Files:**

- Modify: `observability-frontend/src/api/graphContracts.ts`
- Modify: `observability-frontend/src/features/resources/types.ts`
- Modify: `observability-frontend/src/features/resources/resourceDomain.ts`
- Modify: `observability-frontend/src/features/resources/resourceDomain.test.ts`
- Modify: `ai-apm-query-go/internal/graph/ontology.go`
- Modify: `ai-apm-query-go/internal/graph/ontology_test.go`
- Modify: `ai-apm-query-go/internal/graph/schema_manifest_v2.json`
- Modify: `ai-apm-query-go/internal/graph/schema_resources_test.go`

**Interfaces:**

- Produces: `ResourceGroup`, `ResourceHealth`, `RESOURCE_TYPES_BY_GROUP`, `resourceGroupOf()` and the authoritative Graph entity/relation vocabulary used by every later task.
- Preserves: unknown server strings remain decodable in `GraphEntity.entity_type`; only known visible carriers can enter the new catalog.

- [ ] **Step 1: Write failing frontend taxonomy tests**

```ts
expect(RESOURCE_TYPES_BY_GROUP.container).toEqual(expect.arrayContaining([
  'deployment', 'statefulset', 'daemonset', 'replicaset', 'job', 'cronjob',
  'pod', 'container', 'k8s_service', 'endpoint_slice', 'ingress', 'pvc',
]))
expect(RESOURCE_TYPES_BY_GROUP.kubevirt).toEqual(['vm', 'vmi', 'data_volume'])
expect(resourceGroupOf('physical_server')).toBe('foundation')
expect(resourceGroupOf('business')).toBeNull()
expect(resourceGroupOf('service')).toBeNull()
expect(resourceGroupOf('namespace')).toBeNull()
expect(resourceTypeLabel('k8s_service')).toBe('Kubernetes Service')
expect(resourceTypeLabel('service')).toBe('APM Service（兼容）')
```

Define the target contracts in the test imports:

```ts
export type ResourceGroup = 'container' | 'kubevirt' | 'foundation'
export type ResourceHealth = 'critical' | 'degraded' | 'healthy' | 'unknown'

export interface PlatformResourceRef {
  clusterId: string
  uid: string
  type: GraphEntityType
  group: ResourceGroup
  name: string
  namespace?: string
}
```

- [ ] **Step 2: Run tests and verify the previous five-domain model fails**

Run: `cd observability-frontend && npm run test:run -- src/features/resources/resourceDomain.test.ts`

Expected: FAIL because `ResourceGroup`, `RESOURCE_TYPES_BY_GROUP` and the new entity types are absent, while `ResourceDomain` still exposes application as a primary domain.

- [ ] **Step 3: Write failing Go ontology tests**

```go
for _, entityType := range []string{"job", "cronjob", "ingress", "data_volume"} {
    if err := ValidateEntityType(entityType); err != nil { t.Fatalf("%s: %v", entityType, err) }
}
for _, tc := range []struct{ relation, source, target string }{
    {"ROUTES_TO", "ingress", "k8s_service"},
    {"SELECTS", "k8s_service", "pod"},
    {"INSTANTIATES", "vm", "vmi"},
    {"LAUNCHED_BY", "vmi", "pod"},
    {"SCHEDULED_TO", "vmi", "k8s_node"},
    {"ATTACHED_TO", "vmi", "nad"},
    {"MAPS_TO", "k8s_node", "physical_server"},
} {
    if err := ValidateRelation(tc.relation, tc.source, tc.target); err != nil { t.Fatal(err) }
}
```

- [ ] **Step 4: Implement the minimal shared taxonomy**

Replace `ResourceDomain` and `SELECTABLE_RESOURCE_TYPES` with:

```ts
export const RESOURCE_TYPES_BY_GROUP = {
  container: ['deployment', 'statefulset', 'daemonset', 'replicaset', 'job', 'cronjob', 'pod', 'container', 'k8s_service', 'endpoint_slice', 'ingress', 'pvc'],
  kubevirt: ['vm', 'vmi', 'data_volume'],
  foundation: ['k8s_node', 'physical_server', 'nad', 'network', 'nic', 'switch', 'switch_port', 'pv', 'storage_class', 'disk'],
} as const satisfies Record<ResourceGroup, readonly GraphEntityType[]>
```

Extend the Go ontology and JSON schema with the entity types and directed relation pairs from Step 3. Keep existing compatibility entity types and relations valid; do not expose them through `resourceGroupOf()`.

- [ ] **Step 5: Verify and commit**

Run: `cd observability-frontend && npm run test:run -- src/features/resources/resourceDomain.test.ts src/api/knowledgeGraph.test.ts`

Run: `cd ai-apm-query-go && go test ./internal/graph`

Expected: PASS.

```bash
git add observability-frontend/src/api/graphContracts.ts observability-frontend/src/features/resources ai-apm-query-go/internal/graph
git commit -m "refactor: align resource taxonomy to container and kubevirt"
```

---

### Task 2: 将资源目录改为真实总量和游标分页的只读投影

**Files:**

- Modify: `ai-apm-query-go/internal/graph/repository.go`
- Modify: `ai-apm-query-go/internal/graph/hugegraph_repository.go`
- Modify: `ai-apm-query-go/internal/graph/legacy_repository.go`
- Modify: `ai-apm-query-go/internal/graph/shadow_repository.go`
- Modify: `ai-apm-query-go/internal/api/resource_catalog.go`
- Modify: `ai-apm-query-go/internal/api/resource_catalog_test.go`
- Modify: `ai-apm-query-go/internal/graph/outbox_projector_test.go`
- Modify: `ai-apm-query-go/internal/graph/shadow_repository_test.go`

**Interfaces:**

- Consumes: Task 1 resource groups and visible type allow-list.
- Produces: `SearchEntityPage`, resource catalog/summary/detail response metadata, server-side filters and truthful totals for Tasks 5 and 8.

```go
type EntityPageQuery struct {
    EntityTypes []string
    Name        string
    Namespace   string
    Health      string
    Cursor      string
    Limit       int
}

type EntityPage struct {
    Items      []Entity
    Total      int
    NextCursor string
}

type PagedEntityRepository interface {
    SearchEntityPage(context.Context, GraphScope, EntityPageQuery) (EntityPage, error)
}
```

- [ ] **Step 1: Write failing repository and handler tests**

Tests must create at least 125 mixed entities and prove:

```go
if got.Total != 125 || len(got.Items) != 50 || got.NextCursor == "" { t.Fatalf("page=%+v", got) }
if page2.Items[0].EntityUID == got.Items[0].EntityUID { t.Fatal("cursor repeated first page") }
```

Handler assertions:

```go
GET /api/v1/resources/catalog?group=container&type=pod&namespace=commerce&q=order&health=critical&limit=50&cursor=...
```

must return `total`, `loaded`, `next_cursor`, `generated_at`, `partial`, `stale`, `warning_codes`, `read_count` and `expected_count`. `domain` is accepted only as a compatibility parameter; `group` and `domain` together return `400 INVALID_RESOURCE_FILTER`.

- [ ] **Step 2: Run red tests**

Run: `cd ai-apm-query-go && go test ./internal/graph ./internal/api -run 'Test(SearchEntityPage|ResourceCatalog|ResourceSummary|ResourceDetail)'`

Expected: FAIL because the current handler downloads at most 300 entities, slices them in memory and reports five domains.

- [ ] **Step 3: Implement cursor paging in all graph repositories**

Use a base64url JSON cursor containing normalized `name_key + entity_uid`; ordering is always health priority, `name_key`, `entity_uid`. HugeGraph performs filtering, `count()` and `range()` in the backend; Memory and Legacy repositories implement the same deterministic order. Shadow delegates reads to its active backend and does not compare page order asynchronously.

- [ ] **Step 4: Replace the resource wire contract**

```go
type resourceCatalogItem struct {
    UID string `json:"uid"`
    ClusterID string `json:"cluster_id"`
    Type string `json:"type"`
    Group string `json:"group"`
    Name string `json:"name"`
    Namespace string `json:"namespace,omitempty"`
    Location string `json:"location"`
    Health string `json:"health"`
    StatusReasons []StatusReason `json:"status_reasons"`
    Source string `json:"source"`
    LastSeenAt string `json:"last_seen_at,omitempty"`
    Capabilities []string `json:"capabilities,omitempty"`
    BusinessLabels []BusinessLabel `json:"business_labels,omitempty"`
    Attributes map[string]interface{} `json:"attributes,omitempty"`
}
```

`ResourceSummary` returns `groups: [{group,count,health,incomplete}]` for container and kubevirt plus a separate `foundation` summary. Remove `risk` from normalized health; source risk remains a separate severity field in problem projections.

- [ ] **Step 5: Verify no 300-object false total and commit**

Run: `cd ai-apm-query-go && go test ./internal/graph ./internal/api ./internal/bootstrap`

Expected: PASS, including a 400+ entity fixture whose `total` exceeds the first page while `partial=false` when the source answered completely.

```bash
git add ai-apm-query-go/internal/graph ai-apm-query-go/internal/api/resource_catalog.go ai-apm-query-go/internal/api/resource_catalog_test.go
git commit -m "feat(query-api): page cluster resource read models"
```

---

### Task 3: 新增平台聚合与集群健康事实接口

**Files:**

- Create: `ai-apm-query-go/internal/api/platform_health.go`
- Create: `ai-apm-query-go/internal/api/platform_health_test.go`
- Create: `ai-apm-query-go/internal/api/cluster_overview.go`
- Create: `ai-apm-query-go/internal/api/cluster_overview_test.go`
- Modify: `ai-apm-query-go/internal/api/users.go`
- Modify: `ai-apm-query-go/internal/api/system.go`
- Modify: `ai-apm-query-go/internal/api/handler.go`
- Modify: `ai-apm-query-go/internal/api/clusters.go`
- Modify: `ai-apm-query-go/internal/bootstrap/http.go`
- Modify: `ai-apm-query-go/internal/api/auth_internal_route_test.go`

**Interfaces:**

- Consumes: Task 1 health/taxonomy and Task 2 paged resource facts.
- Produces: `GET /api/v1/platform/health`, `GET /api/v1/platform/clusters`, `GET /api/v1/clusters/{clusterId}/overview`.

```go
type HealthStatus string
const (
    HealthHealthy HealthStatus = "healthy"
    HealthDegraded HealthStatus = "degraded"
    HealthCritical HealthStatus = "critical"
    HealthUnknown HealthStatus = "unknown"
)

type StatusReason struct {
    Code string `json:"code"`
    Summary string `json:"summary"`
    Source string `json:"source"`
    ObservedAt *time.Time `json:"observed_at,omitempty"`
}

type CoverageFact struct {
    Collected *int `json:"collected"`
    Expected *int `json:"expected"`
    Percent *float64 `json:"percent"`
    Source string `json:"source"`
}
```

- [ ] **Step 1: Write failing health policy tests**

Lock version `cloud-health-v1` to these exact server rules:

```text
cluster critical = Kubernetes API/control-plane confirmed unavailable OR a confirmed cluster-wide critical foundation risk
cluster degraded = no critical rule and at least one confirmed degraded foundation plane OR authoritative collection coverage below 98%
cluster unknown  = no critical/degraded rule and API, coverage or latest-data fact is missing/stale
cluster healthy  = required facts are present/fresh and none of the above rules match
platform critical = any authorized cluster critical
platform degraded = no critical cluster and any degraded cluster
platform unknown  = no critical/degraded cluster and any unknown cluster
platform healthy  = every authorized cluster healthy
```

Tests must also prove one critical Pod does not by itself mark the cluster control plane critical, missing coverage renders unknown rather than `0/0` or green, and returned `status_reasons` contain the rule code and source.

- [ ] **Step 2: Write failing authorization and scope tests**

```go
// ActiveClusterID intentionally differs; platform result must remain identical.
first := decodePlatformResponse(platformRequest(userID, "cluster-a"))
second := decodePlatformResponse(platformRequest(userID, "cluster-b"))
if !reflect.DeepEqual(first.ScopeClusterIDs, second.ScopeClusterIDs) {
    t.Fatalf("platform scope changed with active cluster: first=%v second=%v", first.ScopeClusterIDs, second.ScopeClusterIDs)
}
```

Also prove an unauthorized cluster ID in `/clusters/{clusterId}/overview` returns 403 without name/count leakage, and platform pagination reports unreadable clusters separately as `read_count` and `expected_count`.

- [ ] **Step 3: Implement typed fact collection and policy**

Refactor `availableScopeOptions` to reuse a typed authorized-cluster reader instead of `map[string]interface{}` internally. Reuse existing cluster registry, Graph facts and system component probes; do not add persistence. If authoritative coverage numerator/denominator is unavailable, return null counts, `status=unknown` and warning `COVERAGE_UNAVAILABLE`.

`SystemComponents` remains admin-only for full details. `platform/health` exposes only the compact trust strip:

```json
{"normal":6,"total":7,"abnormal":[{"name":"拓扑同步","status":"degraded","impact":"关系可能不完整","last_success_at":"..."}]}
```

No internal addresses, credentials or hidden cluster names may be returned.

- [ ] **Step 4: Register routes and exact response contracts**

`platform/health` includes platform status/reasons, rule version, cluster four-state counts, coverage, latest data, highest priority risk, operations trust, and read metadata. `platform/clusters` supports `status`, `q`, `limit` 1–100 and cursor. `clusters/{id}/overview` includes header facts, most important problems, recent changes, equal-weight container/KubeVirt summaries, foundation plane status/reason/impact and data quality.

- [ ] **Step 5: Verify and commit**

Run: `cd ai-apm-query-go && go test ./internal/api ./internal/graph ./internal/bootstrap`

Expected: PASS.

```bash
git add ai-apm-query-go/internal/api ai-apm-query-go/internal/bootstrap/http.go
git commit -m "feat(query-api): expose explainable platform and cluster health"
```

---

### Task 4: 为图谱增加总量、渐进展开和等价关系列表

**Files:**

- Modify: `ai-apm-query-go/internal/graph/models.go`
- Modify: `ai-apm-query-go/internal/graph/repository.go`
- Modify: `ai-apm-query-go/internal/graph/hugegraph_repository.go`
- Modify: `ai-apm-query-go/internal/graph/legacy_repository.go`
- Modify: `ai-apm-query-go/internal/graph/shadow_repository.go`
- Modify: `ai-apm-query-go/internal/api/graph_public.go`
- Modify: `ai-apm-query-go/internal/api/graph_public_test.go`
- Modify: `ai-apm-query-go/internal/graph/outbox_projector_test.go`
- Modify: `ai-apm-query-go/internal/graph/shadow_repository_test.go`

**Interfaces:**

- Produces: truthful graph load metadata and a paged relation endpoint consumed by Task 11.

```go
type GraphMeta struct {
    ContractVersion string `json:"contract_version"`
    SchemaVersion int `json:"schema_version"`
    TotalVertices int `json:"total_vertices"`
    TotalEdges int `json:"total_edges"`
    LoadedVertices int `json:"loaded_vertices"`
    LoadedEdges int `json:"loaded_edges"`
    HasMore bool `json:"has_more"`
    Partial bool `json:"partial"`
    Stale bool `json:"stale"`
    GeneratedAt string `json:"generated_at"`
    WarningCodes []string `json:"warning_codes"`
}

type RelationPage struct {
    Items []Edge `json:"items"`
    Vertices []Entity `json:"vertices"`
    Total int `json:"total"`
    NextCursor string `json:"next_cursor,omitempty"`
}
```

- [ ] **Step 1: Write failing graph metadata and relation-page tests**

Build a fixture with 146 Pods and 240 relationships. Request 80/200 and assert:

```go
if meta.TotalVertices != 147 || meta.LoadedVertices > 80 || !meta.HasMore { t.Fatalf("meta=%+v", meta) }
if page.Total != 240 || len(page.Items) != 50 || page.NextCursor == "" { t.Fatalf("page=%+v", page) }
```

Verify `GET /api/v1/ai/kg/relations?center_uid=<uid>&mode=resource&limit=50&cursor=...` is cluster-scoped, filters by type/relation/Namespace, and never accepts raw Gremlin/Cypher.

- [ ] **Step 2: Run red tests**

Run: `cd ai-apm-query-go && go test ./internal/graph ./internal/api -run 'Test(GraphMeta|GraphRelationPage|GraphPublic)'`

Expected: FAIL because current `GraphMeta` cannot distinguish total, loaded and has-more.

- [ ] **Step 3: Implement backend counting and page retrieval**

Count within the authorized scope before applying requested render limits. Relation cursor is stable base64url JSON of `relation_type + source_uid + target_uid + edge_uid`. `partial=true` is reserved for unavailable/failed sources; hitting a requested page or canvas limit sets `has_more=true` without falsely marking source data partial.

- [ ] **Step 4: Preserve provenance on every edge**

Ensure each relation row exposes `source_uid`, `relation_type`, `target_uid`, `status`, `source`, `last_seen_ms`, `confidence`, `propagates_failure`, `candidate_direction`, and evidence count/IDs from allow-listed attrs. Missing provenance makes the row `unknown`, never confirmed.

- [ ] **Step 5: Verify and commit**

Run: `cd ai-apm-query-go && go test ./internal/graph ./internal/api ./internal/bootstrap`

Expected: PASS.

```bash
git add ai-apm-query-go/internal/graph ai-apm-query-go/internal/api/graph_public.go ai-apm-query-go/internal/api/graph_public_test.go
git commit -m "feat(query-api): add progressive graph relation reads"
```

---

### Task 5: 建立前端事实客户端、缓存边界和路径集群上下文

**Files:**

- Create: `observability-frontend/src/api/platform.ts`
- Create: `observability-frontend/src/api/platform.test.ts`
- Modify: `observability-frontend/src/api/resources.ts`
- Modify: `observability-frontend/src/api/resources.test.ts`
- Modify: `observability-frontend/src/api/graphContracts.ts`
- Modify: `observability-frontend/src/api/knowledgeGraph.ts`
- Modify: `observability-frontend/src/api/knowledgeGraph.test.ts`
- Modify: `observability-frontend/src/query/keys.ts`
- Modify: `observability-frontend/src/query/keys.test.ts`
- Modify: `observability-frontend/src/store/scopeStore.ts`
- Modify: `observability-frontend/src/store/scopeStore.test.ts`
- Create: `observability-frontend/src/features/scope/ClusterRouteGuard.tsx`
- Create: `observability-frontend/src/features/scope/ClusterRouteGuard.test.tsx`

**Interfaces:**

```ts
export const queryKeys = {
  platformHealth: () => ['platform-health'] as const,
  platformClusters: (filters: PlatformClusterFilters) => ['platform-clusters', stableFilters(filters)] as const,
  clusterOverview: (tenantId: string, clusterId: string) => ['cluster-overview', tenantId, clusterId] as const,
  resourceCatalog: (ctx: ClusterContext, filters: ResourceCatalogParams) => ['resource-catalog', ctx.tenantId, ctx.clusterId, stableFilters(filters)] as const,
  resourceDetail: (ctx: ClusterContext, uid: string) => ['resource-detail', ctx.tenantId, ctx.clusterId, uid, timeKey(ctx.timeRange)] as const,
  graph: (ctx: ClusterContext, request: GraphRequest) => ['graph', ctx.tenantId, ctx.clusterId, stableFilters(request)] as const,
}
```

- [ ] **Step 1: Write failing client decode tests**

Use `vi.spyOn(api, 'get')` to prove snake_case decoding retains `statusReasons`, nullable coverage, `readCount/expectedCount`, totals/cursors, edge provenance and warning codes. Resource requests send `group`, never `domain`, from new pages.

- [ ] **Step 2: Write failing cache isolation and route-guard tests**

```ts
expect(queryKeys.platformHealth()).not.toContain('cluster-a')
expect(queryKeys.clusterOverview('tenant-a', 'cluster-a')).not.toEqual(queryKeys.clusterOverview('tenant-a', 'cluster-b'))
```

`ClusterRouteGuard` must validate `params.clusterId` against `/me.available_clusters`, call `switchCluster()` when it differs, wait for POST + GET confirmation, and preserve the original location when switching fails.

- [ ] **Step 3: Run red tests**

Run: `cd observability-frontend && npm run test:run -- src/api/platform.test.ts src/api/resources.test.ts src/api/knowledgeGraph.test.ts src/query/keys.test.ts src/store/scopeStore.test.ts src/features/scope/ClusterRouteGuard.test.tsx`

Expected: FAIL because platform clients and path guard do not exist and current resource wire types still use `domain`.

- [ ] **Step 4: Implement clients and cache reset semantics**

On cluster switch, cancel active cluster queries before POST; retain the current page/data until POST + `/me` readback succeeds; then clear resource selection, graph instances and incompatible query caches. Failure restores the old active scope and exposes a `role="alert"` message.

- [ ] **Step 5: Verify and commit**

Run: `cd observability-frontend && npm run test:run -- src/api src/query src/store src/features/scope`

Run: `cd observability-frontend && npm run build`

Expected: PASS.

```bash
git add observability-frontend/src/api observability-frontend/src/query observability-frontend/src/store observability-frontend/src/features/scope
git commit -m "feat(frontend): add platform and cluster route data contracts"
```

---

### Task 6: 重建应用壳层、登录页和有边界显示基础

**Files:**

- Create: `observability-frontend/src/layout/AppFrame.tsx`
- Create: `observability-frontend/src/layout/AppFrame.test.tsx`
- Create: `observability-frontend/src/components/ClusterNavigator.tsx`
- Create: `observability-frontend/src/components/ClusterNavigator.test.tsx`
- Modify: `observability-frontend/src/layout/navConfig.ts`
- Modify: `observability-frontend/src/layout/navConfig.test.ts`
- Modify: `observability-frontend/src/App.tsx`
- Modify: `observability-frontend/src/App.test.tsx`
- Modify: `observability-frontend/src/pages/Login/index.tsx`
- Modify: `observability-frontend/src/pages/Login/Login.test.tsx`
- Modify: `observability-frontend/src/components/display/DataState.tsx`
- Modify: `observability-frontend/src/components/display/DataState.test.tsx`
- Create: `observability-frontend/src/components/display/BoundedDataRegion.tsx`
- Create: `observability-frontend/src/components/display/BoundedDataRegion.test.tsx`
- Modify: `observability-frontend/src/theme/tokens.ts`
- Modify: `observability-frontend/src/theme/tokens.test.ts`
- Modify: `observability-frontend/src/index.css`

**Interfaces:**

- Consumes: Task 5 `ClusterRouteGuard` and cluster switching state.
- Produces: `AppFrame`, `ClusterNavigator`, `BoundedDataRegion` and final route skeleton used by every page.

- [ ] **Step 1: Write failing shell and navigation tests**

Assert the authenticated shell has the six user-visible entries `平台、集群、调查、处置、报告、管理` (admin only), an 88px rail at 1440 and 72px at 1280, and no string or accessible control matching `全局搜索|生产平台|生产集群|Namespace|节点选择|资源选择`. The topbar contains only cluster navigator, notifications and user menu.

Expected route contract:

```text
/overview
/clusters/:clusterId
/clusters/:clusterId/resources
/clusters/:clusterId/resources/:entityUid
/clusters/:clusterId/observe
/clusters/:clusterId/graph
/clusters/:clusterId/investigations
/clusters/:clusterId/investigations/new
/clusters/:clusterId/investigations/:runId
/clusters/:clusterId/actions
/clusters/:clusterId/reports
/admin
```

- [ ] **Step 2: Write failing display-state and login tests**

`BoundedDataRegion` accepts `state: 'ready' | 'loading' | 'empty' | 'error' | 'partial' | 'stale' | 'forbidden'`; ready renders children, error exposes retry, forbidden contains no hidden counts, and critical failures use `role="alert"`. Login copy must contain `智能云平台运维` and the three real capabilities, with no fabricated live counts.

- [ ] **Step 3: Run red tests**

Run: `cd observability-frontend && npm run test:run -- src/App.test.tsx src/layout src/components/ClusterNavigator.test.tsx src/components/display src/pages/Login/Login.test.tsx src/theme`

Expected: FAIL against the current wide sidebar, command palette, stacked ScopeBar and old login brand.

- [ ] **Step 4: Implement the visual contract**

Use the exact tokens from the formal spec: canvas `#F3F5F7`, surface `#FFFFFF`, subtle `#F8FAFC`, border `#D9E0E7`, text `#162033`, secondary `#5C687A`, interaction `#2F5BD3`, critical `#C83C35`, degraded `#C26A1B`, healthy `#168654`, unknown `#6B7482`. Cards use 10px radius and borders rather than decorative shadows.

Remove the command-palette state/effect, sidebar AI hero, wide/collapsible menu behavior and global `<ScopeBar />`. `ClusterNavigator` displays actual name, Kubernetes version and four-state text; on `/overview` its helper states that selection does not affect platform aggregation.

- [ ] **Step 5: Implement responsive boundaries**

At 1024–1279 the rail is icon-only with accessible names, page side panels become Drawers, and two-column regions stack. Below 1024 platform/cluster health remains readable while action mutation controls show the explicit unsupported notice. Do not replace all routes with a global width gate.

- [ ] **Step 6: Verify and commit**

Run: `cd observability-frontend && npm run test:run -- src/App.test.tsx src/layout src/components/ClusterNavigator.test.tsx src/components/display src/pages/Login src/theme`

Run: `cd observability-frontend && npm run build`

Expected: PASS.

```bash
git add observability-frontend/src/App.tsx observability-frontend/src/layout observability-frontend/src/components/ClusterNavigator.tsx observability-frontend/src/components/ClusterNavigator.test.tsx observability-frontend/src/components/display observability-frontend/src/pages/Login observability-frontend/src/theme observability-frontend/src/index.css
git commit -m "feat(frontend): rebuild the cloud operations application frame"
```

---

### Task 7: 实现生产云平台健康与实际集群详细总览

**Files:**

- Replace: `observability-frontend/src/pages/Overview/index.tsx`
- Replace: `observability-frontend/src/pages/Overview/Overview.test.tsx`
- Create: `observability-frontend/src/pages/Overview/PlatformHealthBand.tsx`
- Create: `observability-frontend/src/pages/Overview/ClusterHealthMatrix.tsx`
- Create: `observability-frontend/src/pages/Overview/PriorityRiskQueue.tsx`
- Create: `observability-frontend/src/pages/Overview/OperationsTrustStrip.tsx`
- Create: `observability-frontend/src/pages/ClusterOverview/index.tsx`
- Create: `observability-frontend/src/pages/ClusterOverview/ClusterOverview.test.tsx`
- Create: `observability-frontend/src/pages/ClusterOverview/CarrierPlanePanel.tsx`
- Create: `observability-frontend/src/pages/ClusterOverview/ClusterFoundationBand.tsx`
- Modify: `observability-frontend/src/App.tsx`

**Interfaces:**

- Consumes: Task 3 response models and Task 6 layout/display primitives.
- Produces: `/overview` and `/clusters/:clusterId` page baselines for all operational journeys.

- [ ] **Step 1: Write failing platform overview tests**

Mock `getPlatformHealth()` and prove the page simultaneously shows status text + icon + reasons, cluster total/four states, coverage numerator/denominator, latest data, highest priority risk and compact operations trust. Change the active cluster store between renders and assert the API call/key and displayed aggregate scope do not change.

- [ ] **Step 2: Write failing cluster overview tests**

Assert container and KubeVirt panels have equal grid spans and equivalent down-drill actions; foundation shows control plane, nodes/physical hosts, network, storage and KubeVirt capability as `status + reason + impact`, not raw counts. Assert no text matches `健康资源|应用服务健康|网络 \d+|存储 \d+`.

- [ ] **Step 3: Run red tests**

Run: `cd observability-frontend && npm run test:run -- src/pages/Overview src/pages/ClusterOverview`

Expected: FAIL because current Overview is cluster-bound and shows service/call/P95 KPI cards.

- [ ] **Step 4: Implement platform page to match `01-platform-overview-1440.png`**

Keep one visual conclusion and one main action. Cluster list sorting is `critical → degraded → unknown → healthy`; each row shows actual cluster, main reason and latest data. Coverage appears exactly once. Operations trust collapses normal components to `normal/total` and expands only abnormal/stale/missing entries.

- [ ] **Step 5: Implement cluster page to match `02-cluster-overview-1440.png`**

Header includes name, status, reasons, version, last sync and coverage. Most important issues precede carrier counts. Foundation problems promoted by the API appear in the issue queue. Business labels may appear only on issue/resource rows.

- [ ] **Step 6: Verify and commit**

Run: `cd observability-frontend && npm run test:run -- src/pages/Overview src/pages/ClusterOverview src/App.test.tsx`

Run: `cd observability-frontend && npm run build`

Expected: PASS.

```bash
git add observability-frontend/src/pages/Overview observability-frontend/src/pages/ClusterOverview observability-frontend/src/App.tsx
git commit -m "feat(frontend): add platform and cluster health overviews"
```

---

### Task 8: 重建资源目录与类型化资源详情

**Files:**

- Replace: `observability-frontend/src/pages/Resources/index.tsx`
- Replace: `observability-frontend/src/pages/Resources/index.test.tsx`
- Replace: `observability-frontend/src/pages/Resources/ResourceDirectory.tsx`
- Replace: `observability-frontend/src/pages/Resources/ResourceDirectory.test.tsx`
- Replace: `observability-frontend/src/pages/Resources/ResourceCenter.tsx`
- Modify: `observability-frontend/src/pages/Resources/ResourceIdentity.tsx`
- Modify: `observability-frontend/src/pages/Resources/resourceDetailModel.ts`
- Modify: `observability-frontend/src/pages/Resources/resourceDetailModel.test.ts`
- Create: `observability-frontend/src/pages/Resources/ResourceCatalogFilters.tsx`
- Create: `observability-frontend/src/pages/Resources/ResourceResultList.tsx`
- Create: `observability-frontend/src/pages/Resources/ResourceInspectorDrawer.tsx`
- Create: `observability-frontend/src/pages/Resources/ResourceDetailPage.tsx`
- Create: `observability-frontend/src/pages/Resources/ResourceDetailPage.test.tsx`
- Create: `observability-frontend/src/pages/Resources/RelationContextPanel.tsx`
- Modify: `observability-frontend/src/App.tsx`

**Interfaces:**

- Consumes: Task 2 catalog/detail and Task 5 URL/query contracts.
- Produces: final resource list/detail experience and resource identity input for observe, investigation, action and graph pages.

- [ ] **Step 1: Write failing catalog tests**

Assert exactly two equal primary tabs: `容器资源` and `KubeVirt 虚拟机`; foundation is an auxiliary link/filter, not a third tab. Search is local, debounced 250ms, requires two characters, and URL persists `group,type,namespace,health,freshness,q,cursor`. Result rows show type, name, location, four-state health, symptom, latest observation and Run.

- [ ] **Step 2: Write failing full-content and responsive tests**

At 1440 the layout is 240px filters + wide results and inspector opens only on demand. At 1024 filters and inspector are Drawers. A 160-character name and UID must be fully reachable through wrapping/expand/copy; `documentElement.scrollWidth <= clientWidth + 2`.

- [ ] **Step 3: Write failing detail projection tests**

Lock the type fields from formal spec §8.5. Missing fields render `未提供（来源：<source>）`; no `未知 Node/Namespace`. Capability buttons render only from `detail.capabilities`. Business/app/middleware appear only under `业务标签` with their source.

- [ ] **Step 4: Run red tests**

Run: `cd observability-frontend && npm run test:run -- src/pages/Resources src/api/resources.test.ts`

Expected: FAIL because current page has eight old tabs, a permanent three-column center and domain-based rows.

- [ ] **Step 5: Implement catalog and detail layouts**

Use the `03-resource-catalog` and `04-resource-detail` PNGs as grid/spacing baselines. Detail page uses identity band, summary facts, evidence tabs and right relation/capability context. Selection navigates to `/clusters/:clusterId/resources/:entityUid`; quick view uses Drawer without changing canonical identity.

- [ ] **Step 6: Verify and commit**

Run: `cd observability-frontend && npm run test:run -- src/pages/Resources src/api/resources.test.ts`

Run: `cd observability-frontend && npm run build`

Expected: PASS.

```bash
git add observability-frontend/src/pages/Resources observability-frontend/src/App.tsx
git commit -m "feat(frontend): rebuild carrier resource catalog and detail"
```

---

### Task 9: 重建资源证据观测与冻结范围调查

**Files:**

- Replace: `observability-frontend/src/pages/Observe/index.tsx`
- Replace: `observability-frontend/src/pages/Observe/index.test.tsx`
- Modify: `observability-frontend/src/features/investigation/draft.ts`
- Modify: `observability-frontend/src/features/investigation/draft.test.ts`
- Modify: `observability-frontend/src/pages/investigation/InvestigationCenter.tsx`
- Modify: `observability-frontend/src/pages/investigation/InvestigationCenter.test.tsx`
- Modify: `observability-frontend/src/pages/investigation/NewInvestigation.tsx`
- Modify: `observability-frontend/src/pages/investigation/NewInvestigation.test.tsx`
- Replace: `observability-frontend/src/pages/investigation/IntelligentInvestigation.tsx`
- Replace: `observability-frontend/src/pages/investigation/IntelligentInvestigation.test.tsx`
- Modify: `observability-frontend/src/pages/investigation/InvestigationShell.tsx`
- Modify: `observability-frontend/src/pages/investigation/EvidenceDetail.tsx`
- Create: `observability-frontend/src/pages/investigation/InvestigationScopeStrip.tsx`
- Create: `observability-frontend/src/pages/investigation/InvestigationScopeStrip.test.tsx`

**Interfaces:**

- Consumes: Task 8 `PlatformResourceRef` and Task 4 failure-chain provenance.
- Preserves: existing Run/Evidence creation, SSE, hypothesis and frozen graph context APIs.

- [ ] **Step 1: Write failing observe tests**

Tabs are `问题、告警、指标、日志、Trace、事件、变更`; data-source product names are absent from primary navigation. With a selected resource, visible chips show type/name/Namespace/time; without one, the page says `当前集群`. A chart titled `请求失败率与副本就绪变化` must contain two series, two named axes, units, legend, time range, source and last update.

- [ ] **Step 2: Write failing investigation tests**

List rows prioritize real carrier, symptom, impact, phase, owner and evidence state; Run ID is secondary. New investigation creates nothing until explicit submit, then freezes relative time to absolute UTC. Existing Run view never calls `switchCluster`, `setResource` or `setTimeRange`.

Failure-chain assertion:

```ts
expect(edge).toMatchObject({ sourceName: '镜像变更', label: '触发', targetName: 'Pod 启动失败', evidenceCount: 3 })
expect(inferredEdge).toMatchObject({ label: '疑似导致', factStatus: 'inferred', lineStyle: 'dashed' })
```

- [ ] **Step 3: Run red tests**

Run: `cd observability-frontend && npm run test:run -- src/pages/Observe src/features/investigation src/pages/investigation`

Expected: FAIL against the current generic tables and card stack.

- [ ] **Step 4: Implement observe to match `05-observe-1440.png`**

Use 8/4 evidence/timeline layout; each mode keeps the same cluster/resource/time contract. Render error, partial and stale independently. Grafana remains a secondary expert link.

- [ ] **Step 5: Implement investigation to match `06-investigation-1440.png`**

Use a top frozen-scope strip and 7/5 evidence/conclusion layout. Show evidence timeline, main failure chain, conclusion, hypotheses, counter-evidence, missing evidence and next action. When `insufficient_evidence` is set, ban deterministic root-cause copy.

- [ ] **Step 6: Verify and commit**

Run: `cd observability-frontend && npm run test:run -- src/pages/Observe src/features/investigation src/pages/investigation`

Run: `cd observability-frontend && npm run build`

Expected: PASS.

```bash
git add observability-frontend/src/pages/Observe observability-frontend/src/features/investigation observability-frontend/src/pages/investigation
git commit -m "feat(frontend): rebuild evidence and investigation workspaces"
```

---

### Task 10: 重建处置、报告与 AIOps 自身健康管理

**Files:**

- Modify: `observability-frontend/src/pages/Actions/actionModel.ts`
- Modify: `observability-frontend/src/pages/Actions/actionModel.test.ts`
- Replace: `observability-frontend/src/pages/Actions/ActionCenter.tsx`
- Replace: `observability-frontend/src/pages/Actions/ActionCenter.test.tsx`
- Replace: `observability-frontend/src/pages/Reports/index.tsx`
- Replace: `observability-frontend/src/pages/Reports/index.test.tsx`
- Replace: `observability-frontend/src/pages/admin/AdminHome.tsx`
- Modify: `observability-frontend/src/pages/admin/AdminSettings.tsx`
- Modify: `observability-frontend/src/pages/admin/AdminSettings.test.tsx`
- Modify: `observability-frontend/src/pages/admin/GraphOperations.tsx`

**Interfaces:**

- Consumes: existing Action state/version/idempotency/preflight APIs, Task 3 system trust facts and Task 8 resource identity.
- Produces: final action pipeline, report layout and management information architecture.

- [ ] **Step 1: Write failing action tests**

Pipeline counts are `待预检、待审批、执行中、待验证、已完成、失败`. Running uses interaction blue; green is allowed only for success or verification pass. Inspector must continuously show target UID/version, risk, normalized command, approval, execution log, verification and rollback condition. A changed resource version displays `需要重新预检` and disables approval/execution.

- [ ] **Step 2: Write failing report/admin tests**

Reports use natural content height and real carrier identity, impact chain, evidence, root cause, action, recovery verification and capacity trend. Admin navigation is `纳管集群、AIOps 自身健康、采集器状态、图谱同步、数据可信度、能力策略、用户与角色、审计日志`; it must separate registry status, cloud health and AIOps health.

- [ ] **Step 3: Run red tests**

Run: `cd observability-frontend && npm run test:run -- src/pages/Actions src/pages/Reports src/pages/admin`

Expected: FAIL because Reports is a thin wrapper and current Admin IA does not expose the required separation.

- [ ] **Step 4: Implement to the action/report/admin PNG baselines**

Keep all mutation authorization on the server; UI renders only returned capabilities and maps 403 to forbidden. Report cards have no fixed large height. Normal AIOps components are summarized; abnormal components include impact and last successful time.

- [ ] **Step 5: Verify and commit**

Run: `cd observability-frontend && npm run test:run -- src/pages/Actions src/pages/Reports src/pages/admin`

Run: `cd observability-frontend && npm run build`

Expected: PASS.

```bash
git add observability-frontend/src/pages/Actions observability-frontend/src/pages/Reports observability-frontend/src/pages/admin
git commit -m "feat(frontend): align actions reports and platform management"
```

---

### Task 11: 实现知识图谱三种语义布局和可访问等价列表

**Files:**

- Replace: `observability-frontend/src/components/graph/graphPresentation.ts`
- Replace: `observability-frontend/src/components/graph/graphPresentation.test.ts`
- Replace: `observability-frontend/src/components/graph/GraphMap.tsx`
- Replace: `observability-frontend/src/components/graph/GraphMap.test.tsx`
- Modify: `observability-frontend/src/components/graph/GraphExplorer.tsx`
- Modify: `observability-frontend/src/components/graph/GraphToolbar.tsx`
- Modify: `observability-frontend/src/components/graph/GraphLegend.tsx`
- Replace: `observability-frontend/src/components/graph/GraphContextPanel.tsx`
- Replace: `observability-frontend/src/components/graph/GraphContextPanel.test.tsx`
- Replace: `observability-frontend/src/components/graph/GraphRelationList.tsx`
- Replace: `observability-frontend/src/components/graph/GraphRelationList.test.tsx`
- Replace: `observability-frontend/src/pages/observability/ResourceRelationships.tsx`
- Replace: `observability-frontend/src/pages/observability/ResourceRelationships.test.tsx`
- Modify: `observability-frontend/src/pages/Resources/MainFailureChain.tsx`
- Modify: `observability-frontend/src/pages/Resources/MainFailureChain.test.tsx`

**Interfaces:**

```ts
export type GraphViewMode = 'cluster-topology' | 'resource-relations' | 'failure-chain'
export type GraphLayoutMode = 'semantic-layers' | 'centered-relations' | 'failure-lr'

export interface GraphDisplayEdge {
  id: string
  source: string
  target: string
  relationType: string
  label: string
  factStatus: 'fact' | 'inferred' | 'unknown'
  sourceSystem: string
  syncedAt?: string
  evidenceCount: number
  aggregateCount?: number
  lineStyle: 'solid' | 'dashed'
}
```

- [ ] **Step 1: Write failing pure presentation tests**

```ts
expect(layoutForMode('cluster-topology')).toBe('semantic-layers')
expect(layoutForMode('resource-relations')).toBe('centered-relations')
expect(layoutForMode('failure-chain')).toBe('failure-lr')
expect(relationLabel('SELECTS', { aggregateCount: 8 })).toBe('选择 8 个')
expect(relationLabel('ATTACHED_TO')).toBe('连接到')
```

Prove lane assignments: access/traffic, controller, runtime, foundation vertically; container and KubeVirt horizontally parallel. Resource mode fixes upstream left, center middle, downstream right, hosting/network/storage bottom. Failure mode is cause → result → impact left-to-right.

- [ ] **Step 2: Write failing semantic edge tests**

Create a fixture and assert these exact directions:

```text
Deployment ─管理→ Pod
Ingress ─路由到→ Kubernetes Service
Kubernetes Service ─选择 8 个→ Pod aggregate
VM ─实例化→ VMI
VMI ─通过启动器运行→ virt-launcher Pod
VMI ─调度到→ Kubernetes Node
VMI ─连接到→ NAD
Kubernetes Node ─映射到→ Physical Server
Pod/VMI ─挂载→ PVC ─绑定到→ PV
```

Every inferred edge label includes `疑似`, uses dashed line and retains its semantic source/target direction.

- [ ] **Step 3: Write failing workspace and accessibility tests**

Assert toolbar controls, 80/200 budget text, total/loaded counts, aggregate expansion, inspector and relation list pagination. The equivalent list columns are source, relation, target, fact status, source and sync time. If G6 throws, the list remains usable. At 1024 inspector opens as a Drawer and the canvas remains in bounds.

- [ ] **Step 4: Run red tests**

Run: `cd observability-frontend && npm run test:run -- src/components/graph src/pages/observability/ResourceRelationships.test.tsx src/pages/Resources/MainFailureChain.test.tsx`

Expected: FAIL against the current radial/resource and top-bottom/failure layouts, hidden-at-low-zoom labels and incomplete relation vocabulary.

- [ ] **Step 5: Implement orthogonal G6 layouts and lifecycle**

Use precomputed semantic coordinates/ports for cluster and resource modes, and left-to-right Dagre for failure chains. Render resource cards, opaque labels and arrowheads at least 8px. Never hide all visible relationship labels at low zoom; aggregate first. Disable animation above 50 nodes or with reduced motion. Cleanup destroys G6, ResizeObserver and event listeners.

- [ ] **Step 6: Implement progressive pages and inspector**

Use Task 4 totals and relation cursor; do not download full graph then slice. Aggregate nodes show total/critical/unknown/loaded. Aggregate edge labels include counts. Node inspector shows identity/health/neighbors/impact/evidence/actions; edge inspector shows source/relation/target/fact/source/time/evidence.

- [ ] **Step 7: Verify and commit**

Run: `cd observability-frontend && npm run test:run -- src/components/graph src/pages/observability/ResourceRelationships.test.tsx src/pages/Resources/MainFailureChain.test.tsx`

Run: `cd observability-frontend && npm run build`

Expected: PASS.

```bash
git add observability-frontend/src/components/graph observability-frontend/src/pages/observability/ResourceRelationships.tsx observability-frontend/src/pages/observability/ResourceRelationships.test.tsx observability-frontend/src/pages/Resources/MainFailureChain.tsx observability-frontend/src/pages/Resources/MainFailureChain.test.tsx
git commit -m "feat(frontend): implement semantic resource relationship graphs"
```

---

### Task 12: 收敛旧深链并移除可到达的旧信息架构

**Files:**

- Modify: `observability-frontend/src/layout/navConfig.ts`
- Modify: `observability-frontend/src/layout/navConfig.test.ts`
- Modify: `observability-frontend/src/App.tsx`
- Modify: `observability-frontend/src/App.test.tsx`
- Modify: `observability-frontend/src/features/scope/runScopeAdapter.ts`
- Modify: `observability-frontend/src/features/scope/runScopeAdapter.test.ts`
- Delete: `observability-frontend/src/components/ClusterSwitcher.tsx`
- Delete: `observability-frontend/src/components/AiDock.tsx`
- Delete: `observability-frontend/src/features/scope/ScopeBar.tsx`
- Delete: `observability-frontend/src/features/scope/ScopeBar.test.tsx`
- Delete: `observability-frontend/src/pages/Resources/ClusterPanorama.tsx`
- Delete: `observability-frontend/src/pages/Resources/ClusterPanorama.test.tsx`
- Delete: `observability-frontend/src/pages/observability/ServiceObservability.tsx`
- Delete: `observability-frontend/src/pages/observability/ServiceObservability.test.tsx`
- Delete: `observability-frontend/src/pages/observability/VirtualMachines.tsx`
- Delete: `observability-frontend/src/pages/observability/VirtualMachines.test.tsx`
- Delete: `observability-frontend/src/pages/infra/Hardware.tsx`
- Delete: `observability-frontend/src/pages/infra/K8sActions.tsx`
- Delete: `observability-frontend/src/pages/infra/K8sActions.test.tsx`
- Delete: `observability-frontend/src/pages/report/Report.tsx`

**Interfaces:**

- Consumes: final routes from Tasks 6–11.
- Produces: two-release compatibility redirects without restoring obsolete product domains.

- [ ] **Step 1: Write failing redirect tests**

```text
/observability/service       -> /clusters/:clusterId/resources?group=container&compat=apm-service
/observability/vms           -> /clusters/:clusterId/resources?group=kubevirt&type=vm
/infra/k8s                   -> /clusters/:clusterId/resources?group=container
/hardware                    -> /clusters/:clusterId/resources?foundation=physical_server
/capacity                    -> /clusters/:clusterId/resources?view=capacity
/observability/relationships -> /clusters/:clusterId/graph
/investigation/:runId        -> /clusters/:clusterId/investigations/:runId after Run scope resolve
```

Without an active/authorized cluster, redirects lead to a cluster-selection prompt, never a fabricated all-cluster page.

- [ ] **Step 2: Run red tests**

Run: `cd observability-frontend && npm run test:run -- src/layout/navConfig.test.ts src/App.test.tsx src/features/scope/runScopeAdapter.test.ts`

Expected: FAIL because legacy targets still point to `/resources?domain=...` and snapshots still say `生产平台`.

- [ ] **Step 3: Implement compatibility adapters and remove obsolete reachability**

Keep `environment: 'prod'` only inside `toLegacyRunScope()`. Historical business/application/middleware/service detail renders a compatibility banner plus links to mapped real carriers when the API supplies mapping; it never guesses by name.

Before deletion, run `rg -n 'ClusterSwitcher|AiDock|ScopeBar|ClusterPanorama|ServiceObservability|VirtualMachines|Hardware|K8sActions|pages/report/Report' observability-frontend/src`. Update the final `App.tsx`, Actions, Reports and investigation imports first; then delete the exact files listed above. Preserve shared API clients, `Trace`, `LogMetrics`, `Grafana`, `Capacity`, `Changes`, alert pages and historical Run readers because final workspaces still consume them.

- [ ] **Step 4: Prove banned concepts are unreachable**

Run:

```bash
rg -n "全局搜索|生产平台|生产集群|domain=application|setEnvironment|setNamespace|context\.environment|context\.namespace" observability-frontend/src
```

Expected: zero UI matches; only test assertions and the explicit legacy `environment: 'prod'` adapter are allowed.

- [ ] **Step 5: Verify and commit**

Run: `cd observability-frontend && npm run test:run`

Run: `cd observability-frontend && npm run build`

Expected: PASS.

```bash
git add -A observability-frontend/src
git commit -m "refactor(frontend): retire the superseded information architecture"
```

---

### Task 13: 建立最终端到端、响应式和实际视觉验收证据

**Files:**

- Replace: `tests/manual-e2e/ia-workbench.js`
- Replace: `tests/manual-e2e/ia-resources.js`
- Replace: `tests/manual-e2e/ia-graph.js`
- Replace: `tests/manual-e2e/ia-investigation.js`
- Replace: `tests/manual-e2e/ia-actions.js`
- Create: `tests/manual-e2e/ia-reports-admin.js`
- Modify: `tests/manual-e2e/lib/laneD-runner.js`
- Modify: `tests/manual-e2e/lib/harness.js`
- Create: `docs/superpowers/verification/2026-09-09-aiops-cloud-platform-ui-final.md`

**Interfaces:**

- Produces: reproducible screenshots, DOM/overflow results, console/network logs and journey evidence for the release gate.

- [ ] **Step 1: Update the five journey lanes and add reports/admin**

Each lane runs at 1440×900, 1280×720 and 1024×768. Use final routes and semantic locators. Required journeys are the eight journeys in formal spec §13.4, including cluster-switch failure/no-permission, long content, large graph aggregation and Action verification.

- [ ] **Step 2: Add automatic visual-boundary assertions**

For every page state record:

```js
check('no_page_horizontal_overflow', await page.evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth + 2))
check('body_font_at_least_14', await page.evaluate(() => parseFloat(getComputedStyle(document.body).fontSize) >= 14))
check('dialog_inside_viewport', await page.locator('[role="dialog"]').evaluateAll((nodes) => nodes.every((node) => { const r = node.getBoundingClientRect(); return r.left >= 0 && r.top >= 0 && r.right <= innerWidth && r.bottom <= innerHeight })))
```

Also assert no primary text is available only through `title`/Tooltip, all icon buttons have accessible names, and graph list rows match currently visible semantic edges.

- [ ] **Step 3: Run full automated gates**

Run: `make test-workflow-all`

Run: `deploy/scripts/test-deployment-contracts.sh`

Run: `deploy/scripts/test-image-digest-contracts.sh`

Expected: all exit 0.

- [ ] **Step 4: Run live E2E against `http://127.0.0.1:30253`**

Run each IA script with one shared `TEST_RUN_ID`; store screenshots and logs under `test-results/<run-id>/`. Any console error, unexplained 4xx/5xx, white screen, overflow or missing semantic assertion fails the lane.

- [ ] **Step 5: Compare screenshots to all 14 baselines**

Review login, platform, cluster, catalog, detail, observe, investigation, actions, graph, reports, admin plus 1280 platform, 1024 catalog and 1024 graph. Pixel differences from real data are allowed only when information priority, grid, typography, complete-content path, color semantics and responsive behavior remain equivalent.

Record the two corrected graph details explicitly: Service aggregate edge includes count; VMI is the source of the NAD edge.

- [ ] **Step 6: Commit verification harness and evidence**

```bash
git add tests/manual-e2e docs/superpowers/verification/2026-09-09-aiops-cloud-platform-ui-final.md
git commit -m "test: verify the final cloud platform operations UI"
```

---

### Task 14: 闭合提交、构建镜像、同步环境并复验运行态

**Files:**

- Modify: `docs/superpowers/verification/2026-09-09-aiops-cloud-platform-ui-final.md`
- Runtime targets: `observability-frontend`, `query-api-http`, `query-run-dispatch`, `query-alert-eval` and other chart-managed workloads sharing the immutable release tag.

**Interfaces:**

- Consumes: clean `main` HEAD and every prior task's passing evidence.
- Produces: deployed images whose revision label and Kubernetes workload image match the exact final commit.

- [ ] **Step 1: Verify repository and commit closure before building**

Run: `git status --short --branch`

Run: `git log --oneline --decorate -14`

Run: `git diff --check HEAD~14..HEAD`

Expected: branch is `main`, working tree is clean, and all final-plan task commits are present in order.

- [ ] **Step 2: Run fresh final verification**

Run: `make test-workflow-all`

Run: `deploy/scripts/verify-aiops-workflow-gates.sh`

Expected: all configured stages exit 0; do not rely on an earlier run.

- [ ] **Step 3: Build and deploy the exact HEAD**

Use the repository release path so all self-built images share `git-<HEAD short SHA>` and `org.opencontainers.image.revision=<full HEAD>`:

```bash
deploy/scripts/apply.sh
```

The executor must supply/reuse the already configured deployment secrets; no credential value is written into this plan or Git.

- [ ] **Step 4: Verify rollout and image revision**

```bash
kubectl -n observability rollout status deployment/frontend --timeout=15m
kubectl -n observability rollout status deployment/query-api-http --timeout=15m
kubectl -n observability get pods -o wide
kubectl -n observability get deployment frontend query-api-http -o jsonpath='{range .items[*]}{.metadata.name}{"="}{.spec.template.spec.containers[0].image}{"\n"}{end}'
```

Expected: rollouts complete and image tag equals `git-<final HEAD short SHA>`.

- [ ] **Step 5: Run post-deploy smoke and browser evidence**

Verify login, `/api/v1/platform/health`, `/api/v1/platform/clusters`, one authorized cluster overview, resource catalog pagination, graph totals/relations, the eight UI journeys and all three viewports against `http://127.0.0.1:30253`. Hard reload once to rule out stale frontend assets.

- [ ] **Step 6: Record deployment evidence and commit it**

Append final commit, image tag, deployment revisions, rollout timestamps, E2E run ID and screenshot directory to the verification document, then commit only that evidence update:

```bash
git add docs/superpowers/verification/2026-09-09-aiops-cloud-platform-ui-final.md
git commit -m "docs: record final cloud platform UI deployment"
```

Run: `git status --short --branch`

Expected: clean `main`; the evidence commit is now the final HEAD, so one final immutable rebuild is required before reporting completion.

- [ ] **Step 7: Rebuild and redeploy the exact final `main` HEAD**

Run:

```bash
deploy/scripts/apply.sh
kubectl -n observability rollout status deployment/frontend --timeout=15m
kubectl -n observability rollout status deployment/query-api-http --timeout=15m
kubectl -n observability get deployment frontend query-api-http -o jsonpath='{range .items[*]}{.metadata.name}{"="}{.spec.template.spec.containers[0].image}{"\n"}{end}'
git status --short --branch
```

Hard reload `http://127.0.0.1:30253`, repeat the login and platform-overview smoke assertions, and compare the deployed image tag/revision label with the current full and short HEAD.

Expected: working tree is clean; every self-built workload uses `git-<current final HEAD short SHA>` and `org.opencontainers.image.revision=<current final full HEAD>`; the UI remains the verified final design after hard reload.

---

## Final Release Gate

All items must be true before reporting 100% completion:

1. `main` is clean; no uncommitted implementation or verification file remains.
2. Platform overview is independent of active cluster and shows explainable status, four-state cluster distribution, coverage numerator/denominator or explicit unavailable state, latest data, highest risk and compact AIOps trust.
3. Topbar has only actual cluster selection, notifications and user; no global search, environment, production-platform, Namespace, node or resource selector.
4. Choosing a real cluster enters its detailed overview; failed switch/deep-link authorization preserves the old context and leaks no target data.
5. Container and KubeVirt are equal primary resource planes; foundation is auxiliary; business/application/APM service/middleware remain compatible but hidden as first-class UI.
6. Platform/cluster health never uses a 0–100 score or heterogeneous healthy-resource percentage; network/storage use status, reason and impact rather than naked counts.
7. Every data region has verified ready/loading/empty/error/partial/stale/forbidden states without sample-data fallback.
8. Observe, investigation, action and report pages remain bound to real cluster/resource facts and preserve existing Run/Evidence/Action authorization and audit semantics.
9. All three graph modes, directed Chinese edge labels, fact/inference provenance, 80/200 render budget, accurate totals, aggregations, paged expansion and equivalent list pass tests. Service aggregate count and `VMI → NAD` direction are correct.
10. 1440×900, 1280×720 and 1024×768 screenshots pass structure, typography, complete-content, overlay and overflow review for all required pages.
11. Frontend tests/build, Go tests, workflow gates, deployment contracts and live E2E all exit 0 with recorded output.
12. The environment at `http://127.0.0.1:30253` serves the immutable image whose revision equals the final clean `main` HEAD after a hard reload, and rollout/evidence identifiers are recorded.
