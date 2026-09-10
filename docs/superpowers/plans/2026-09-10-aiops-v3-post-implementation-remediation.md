# AIOps V3 Post-Implementation Remediation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 修复 2026-09-10 现场审核发现的运行阻断、健康事实矛盾和核心 UI 判读缺陷，使 AIOps V3 在真实生产部署中满足已确认的信息架构、视觉合同与正式验收门禁。

**Architecture:** 先在 Query API 修复资源目录与观测鉴权，并建立平台/集群共用的健康事实投影；再以“默认可搜索资源边界 + 真实关系链 + 视图相关关系颜色”重构图谱，以摘要列表和详情 Drawer 收敛调查/处置长表格，以 CSS 主导的响应式布局修复助手。最后用真实登录、真实集群 Scope、真实 API 响应和固定视口浏览器检查替代仅验证标题/无溢出的浅门禁。

**Tech Stack:** Go、React 18、TypeScript 5.6、React Router 6、TanStack Query 5、Ant Design 5、AntV G6 5、Vitest、Playwright、Helm/Kubernetes。

**Spec:** [正式信息架构](../specs/2026-09-08-aiops-information-architecture-design.md)；[V3 实际渲染与编码指南](../specs/assets/ui-v2/UI_RENDER_GUIDE.md)；[原 V3 实施计划](2026-09-10-aiops-cloud-operations-ui-v3.md)。本计划的 `Audited Baseline` 固化了实施后现场审核结果，因此执行不依赖工作区之外的临时报告。

## Global Constraints

- 所有实现与提交都在 `main` 顺序进行；一个任务通过定向验证并提交后再进入下一任务，不创建并行工作树。
- 平台只有生产环境；任何页面、筛选、测试断言和示例文案都不得重新引入 `prod/dev/staging/test` 环境选择或“生产平台/生产集群”伪层级。
- `/overview` 聚合当前用户全部授权集群且不受活动集群影响；其余主工作区绑定一个经 `/me.available_clusters` 验证的真实 Kubernetes 集群。
- 容器一级 Kind 固定为 Deployment、StatefulSet、DaemonSet、Job、CronJob、Pod、Kubernetes Service、Ingress；Container、ReplicaSet、EndpointSlice 仅在详情或证据中出现。
- 业务、应用、APM Service、中间件保留底层兼容数据，但不得进入默认资源目录、默认图谱搜索、一级导航或独立健康结论。
- 顶级健康只使用 `healthy | degraded | critical | unknown`；注册状态与观测健康必须分离，不计算 0–100 综合分数。
- 数据超过显示预算时必须分页、聚合或进入详情；禁止通过逐字换行、不可恢复省略或页面级横向滚动伪装“不超限”。
- 图谱 `80 节点 / 200 边` 只约束首屏渲染；后端查询预算继续使用现有受控上限，完整结果通过总数、聚合、游标和等价关系列表访问。
- 每个数据区的 loading、empty、error、partial、stale、forbidden 必须互斥且语义一致；接口失败时不得同时显示健康空态。
- 助手不得直接执行动作；结构化回答继续包含结论、事实引用、已发布知识引用、不确定性、下一步和受控动作。
- 桌面验收视口固定为 `1440×900`、`1280×720`、`1024×768`，助手与知识补充 `1024×900`。
- 本计划不新增应用管理、中间件管理、跨集群批量处置、新图数据库或新前端依赖。

## Audited Baseline

- Git 基线：`main`，审核时 HEAD `812013e`，工作树干净，分支领先 `origin/main` 65 个提交。
- 部署基线：Helm release `aiops` revision 55；自研镜像 tag `git-812013e`；Pod Ready，前端入口返回 HTTP 200。
- 自动验证基线：前端 68 个测试文件、164 项测试通过；`npm run build`、`go test ./...`、Helm strict lint 和部署契约通过。
- 现场阻断一：`GET /api/v1/resources/catalog?group=containers` 返回 502；`resource_catalog.go` 在上游 partial 且过滤结果小于公开上限时执行越界切片。
- 现场阻断二：`GET /api/v1/alerts/aggregation` 返回 403；路由已注册，但 `isCanonicalProtectedRoute` 未列入该只读端点。
- 事实矛盾：同一陈旧集群在平台列表显示健康，在集群详情显示降级；平台同时显示 `0/2` 覆盖、2 个未知/陈旧集群和 2 个健康集群。
- 显示缺陷：图谱默认泄漏 ReplicaSet/应用服务、结构边被画成故障红色、依赖主链串联不相邻节点；调查表格逐字换行；助手侧栏的 inline `display:flex` 覆盖 1024px 隐藏规则。

## File and Ownership Map

- `ai-apm-query-go/internal/api/resource_catalog.go`：资源目录后端预算与 Kubernetes snapshot fallback。
- `ai-apm-query-go/internal/api/auth.go`、`internal/bootstrap/http.go`：浏览器 canonical 路由鉴权与注册一致性。
- `ai-apm-query-go/internal/api/platform_health.go`：新增的集群观测健康与能力摘要纯投影，不负责注册生命周期。
- `ai-apm-query-go/internal/api/platform_overview.go`、`cluster_overview.go`、`system.go`：复用同一投影并输出一致 DTO。
- `observability-frontend/src/pages/Observe/index.tsx`：问题聚合的互斥数据状态。
- `observability-frontend/src/features/resources/resourceDomain.ts`：保留完整兼容类型，并额外定义默认图谱可搜索类型。
- `ai-apm-query-go/internal/api/graph_public.go`：服务端执行 `operations` 搜索 profile，防止前端过滤造成合法结果被截断。
- `observability-frontend/src/components/graph/graphPresentation.ts`：关系标签、样式、预算和真实关系链投影。
- `observability-frontend/src/components/graph/GraphMap.tsx`、`GraphExplorer.tsx`、`DependencyChain.tsx`：图谱布局、检查器和可读关系链。
- `observability-frontend/src/features/workflow/statusPresentation.ts`：新增的调查/处置状态中文投影。
- `observability-frontend/src/pages/investigation/InvestigationCenter.tsx`、`src/pages/Actions/ActionCenter.tsx`：首屏摘要列与完整详情 Drawer。
- `observability-frontend/src/pages/ai/AiChat.tsx`、`src/index.css`：助手三栏与 1024px 单栏/Drawer 切换。
- `observability-frontend/src/App.tsx`、`src/features/scope/ScopeBar.tsx`：可发现的桌面导航和不重复的集群选择。
- `observability-frontend/src/pages/Knowledge/index.tsx`、`src/pages/report/Report.tsx`、`src/pages/admin/AdminSettings.tsx`：紧凑、可行动的真实空态。
- `tests/manual-e2e/v3-acceptance.js`、`tests/manual-e2e/lib/v3-assertions.js`：真实浏览器发布门禁。
- `deploy/scripts/verify-v3-ui-acceptance.sh`、`deploy/scripts/collect-release-evidence.sh`：版本绑定的 UI 验收与发布证据。

## Delivery Order

1. P0 可用性：Task 1–3。
2. P1 判读体验：Task 4–7。
3. P2 视觉与门禁：Task 8–10。
4. Task 1、2 可分别评审；Task 3 完成后才允许调整总览截图；Task 4 的数据边界先于 Task 5 的图谱视觉；Task 10 只能在前九项提交全部进入 `main` 后执行。

---

### Task 1: 修复资源目录 fallback 越界并锁定部分数据语义

**Files:**
- Modify: `ai-apm-query-go/internal/api/resource_catalog.go:315-319`
- Modify: `ai-apm-query-go/internal/api/resource_catalog_test.go`

**Interfaces:**
- Consumes: `[]graph.Entity`、上游 `snapshotPartial`、`graph.DefaultPublicMaxVertices`。
- Produces: `boundFilteredResourceEntities(items []graph.Entity, upstreamPartial bool) ([]graph.Entity, bool)`；返回值永不越界，且保留上游 partial 事实。

**Acceptance:** 过滤结果为 0、少于上限、等于上限和超过上限时都不 panic；`partial` 分别等于上游 partial 或本地截断事实的逻辑或。

- [ ] **Step 1: 写失败测试覆盖现场 panic 和四个边界**

在 `resource_catalog_test.go` 增加：

```go
func TestBoundFilteredResourceEntitiesPreservesUpstreamPartialWithoutSlicingPastLength(t *testing.T) {
    cases := []struct {
        name            string
        count           int
        upstreamPartial bool
        wantCount       int
        wantPartial     bool
    }{
        {name: "empty upstream partial", count: 0, upstreamPartial: true, wantCount: 0, wantPartial: true},
        {name: "small upstream partial", count: 3, upstreamPartial: true, wantCount: 3, wantPartial: true},
        {name: "exact public budget", count: graphpkg.DefaultPublicMaxVertices, wantCount: graphpkg.DefaultPublicMaxVertices},
        {name: "over public budget", count: graphpkg.DefaultPublicMaxVertices + 1, wantCount: graphpkg.DefaultPublicMaxVertices, wantPartial: true},
    }
    for _, tc := range cases {
        t.Run(tc.name, func(t *testing.T) {
            items := make([]graphpkg.Entity, tc.count)
            got, partial := boundFilteredResourceEntities(items, tc.upstreamPartial)
            if len(got) != tc.wantCount || partial != tc.wantPartial {
                t.Fatalf("len=%d partial=%v, want len=%d partial=%v", len(got), partial, tc.wantCount, tc.wantPartial)
            }
        })
    }
}
```

同时增加 handler 回归：snapshot 返回 `partial: true`，只含会被 `group=containers` 过滤掉的对象，断言 HTTP 200、`items=[]`、`meta.partial=true`。

```go
func TestResourceCatalogFallbackKeepsPartialWhenGroupFilterIsEmpty(t *testing.T) {
    const clusterID = "11111111-1111-4111-8111-111111111111"
    snapshot := map[string]interface{}{
        "partial": true,
        "namespaces": []map[string]interface{}{{
            "metadata": map[string]interface{}{"uid": "uid-namespace", "name": "payments"},
        }},
    }
    h := &Handler{
        graphRepo: resourceFeatureUnavailableRepository{MemoryRepository: graphpkg.NewMemoryRepository()},
        kubeRepo: query.NewKubernetesRepository(&k8sTestAccessor{client: &k8sTestClient{graphObjects: snapshot}}),
    }
    req := httptest.NewRequest(http.MethodGet, "/api/v1/resources/catalog?group=containers&limit=20", nil)
    req = withAuthorizationContext(req, AuthorizationContext{UserID: "user", SessionID: "session", TenantID: "tenant-a", ActiveClusterID: clusterID})
    rec := httptest.NewRecorder()
    h.ResourceCatalog(rec, req)
    if rec.Code != http.StatusOK { t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String()) }
    var body struct {
        Items []json.RawMessage `json:"items"`
        Meta ResourceReadMeta `json:"meta"`
    }
    if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil { t.Fatal(err) }
    if len(body.Items) != 0 || !body.Meta.Partial { t.Fatalf("items=%d meta=%+v", len(body.Items), body.Meta) }
}
```

- [ ] **Step 2: 运行测试并确认旧实现失败**

Run: `cd ai-apm-query-go && go test ./internal/api -run 'BoundFilteredResourceEntities|ResourceCatalogFallsBack.*FilteredEmpty' -count=1`

Expected: FAIL；helper 尚不存在，或 handler 测试复现 slice bounds panic。

- [ ] **Step 3: 实现唯一预算 helper 并替换危险切片**

```go
func boundFilteredResourceEntities(items []graphpkg.Entity, upstreamPartial bool) ([]graphpkg.Entity, bool) {
    overflow := len(items) > graphpkg.DefaultPublicMaxVertices
    if overflow {
        items = items[:graphpkg.DefaultPublicMaxVertices]
    }
    return items, upstreamPartial || overflow
}
```

在 `resourceEntityLookup` 中改为：

```go
bounded, partial := boundFilteredResourceEntities(filtered, snapshotPartial)
return resourceEntityLookup{entities: bounded, partial: partial}, nil
```

- [ ] **Step 4: 运行资源目录定向测试和全 Go 测试**

Run: `cd ai-apm-query-go && go test ./internal/api -run ResourceCatalog -count=1 && go test ./...`

Expected: PASS；无 panic，原有类型、租户、集群与分页合同不回归。

- [ ] **Step 5: 提交**

```bash
git add ai-apm-query-go/internal/api/resource_catalog.go ai-apm-query-go/internal/api/resource_catalog_test.go
git commit -m "fix(resources): bound filtered fallback results safely"
```

### Task 2: 放行观测聚合只读路由并使错误/空态互斥

**Files:**
- Modify: `ai-apm-query-go/internal/api/auth.go:838-876`
- Modify: `ai-apm-query-go/internal/api/auth_test.go`
- Modify: `observability-frontend/src/pages/Observe/index.tsx`
- Modify: `observability-frontend/src/pages/Observe/index.test.tsx`

**Interfaces:**
- Consumes: 已注册的 `GET /api/v1/alerts/aggregation`、canonical tenant、JWT session、活动集群 Scope。
- Produces: 鉴权后可读的告警聚合；`ProblemsView` 在 loading/error/empty/ready 之间只呈现一种主状态。

**Acceptance:** 已授权成员 GET 返回业务处理结果；无 tenant/session 仍 fail closed；接口拒绝或失败时 DOM 不出现“暂无问题”和健康事实条。

- [ ] **Step 1: 写失败测试锁定鉴权和互斥状态**

在 `auth_test.go` 增加：

```go
func TestCanonicalProtectedRouteIncludesAlertAggregationRead(t *testing.T) {
    if !isCanonicalProtectedRoute("/api/v1/alerts/aggregation") {
        t.Fatal("alert aggregation must use canonical browser authorization")
    }
}
```

在 `Observe/index.test.tsx` 增加：

```tsx
it('does not present a healthy empty state when aggregation fails', async () => {
  vi.mocked(getAlertAggregation).mockRejectedValueOnce(new Error('permission_denied'))
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  render(<QueryClientProvider client={queryClient}><MemoryRouter><Observe /></MemoryRouter></QueryClientProvider>)
  expect(await screen.findByText('问题数据读取失败')).toBeVisible()
  expect(screen.queryByText('暂无问题')).not.toBeInTheDocument()
  expect(screen.queryByLabelText('问题事实摘要')).not.toBeInTheDocument()
})
```

- [ ] **Step 2: 运行测试并确认失败**

Run: `cd ai-apm-query-go && go test ./internal/api -run 'CanonicalProtectedRouteIncludesAlertAggregationRead' -count=1`

Run: `cd observability-frontend && npm test -- --run src/pages/Observe/index.test.tsx`

Expected: Go 测试 FAIL，因为路由未列入；前端测试 FAIL，因为错误 Alert 与空表同时渲染。

- [ ] **Step 3: 添加精确只读路由并重排 ProblemsView 状态**

在 `isCanonicalProtectedRoute` 的 exact switch 中加入：

```go
"/api/v1/alerts/aggregation",
```

在 `ProblemsView` 使用互斥返回：

```tsx
if (!activeClusterId) return <DataState kind="empty" title="请选择集群" description="选择集群后查看问题" />
if (problemsQuery.isLoading) return <DataState kind="loading" title="正在读取问题" />
if (problemsQuery.isError) return <DataState kind="error" title="问题数据读取失败" description={error || '告警聚合暂不可用'} onRetry={() => void problemsQuery.refetch()} />
if (rows.length === 0) return <DataState kind="empty" title="暂无问题" description="当前集群没有可展示的活动问题" />
```

只有 `rows.length > 0` 时渲染事实条和 Table。

- [ ] **Step 4: 运行鉴权、告警和前端测试**

Run: `cd ai-apm-query-go && go test ./internal/api -run 'Auth|AlertAggregation' -count=1`

Run: `cd observability-frontend && npm test -- --run src/pages/Observe/index.test.tsx src/api/client.test.ts`

Expected: PASS；未授权请求仍为 401/403，授权请求不再被 canonical 路由拒绝。

- [ ] **Step 5: 提交**

```bash
git add ai-apm-query-go/internal/api/auth.go ai-apm-query-go/internal/api/auth_test.go observability-frontend/src/pages/Observe/index.tsx observability-frontend/src/pages/Observe/index.test.tsx
git commit -m "fix(observe): authorize aggregation and separate data states"
```

### Task 3: 建立平台与集群共用的可信健康投影

**Files:**
- Create: `ai-apm-query-go/internal/api/platform_health.go`
- Create: `ai-apm-query-go/internal/api/platform_health_test.go`
- Modify: `ai-apm-query-go/internal/api/platform_overview.go`
- Modify: `ai-apm-query-go/internal/api/platform_overview_test.go`
- Modify: `ai-apm-query-go/internal/api/cluster_overview.go`
- Modify: `ai-apm-query-go/internal/api/cluster_overview_test.go`
- Modify: `ai-apm-query-go/internal/api/system.go`
- Modify: `observability-frontend/src/api/platform.ts`
- Modify: `observability-frontend/src/api/platform.test.ts`
- Modify: `observability-frontend/src/pages/Overview/index.tsx`
- Modify: `observability-frontend/src/pages/Overview/Overview.test.tsx`
- Modify: `observability-frontend/src/pages/Clusters/ClusterOverview.tsx`
- Modify: `observability-frontend/src/pages/Clusters/ClusterOverview.test.tsx`

**Interfaces:**
- Produces: `projectClusterOperationalState(cluster store.Cluster, now time.Time) clusterOperationalState`。
- Produces: `collectSystemComponentResults(probe func(kind, addr string) bool) []systemComponentResultView` 与 `summarizePlatformCapabilities(rows []systemComponentResultView) platformCapabilitySummary`。
- API 增量字段：平台集群项增加 `registration_status`、`status_reason`；既有 `status` 保持观测健康含义。

**Health precedence:**

1. `UpdatedAt` 为空或超过 5 分钟：`unknown`，原因分别为“没有观测证据”或“观测数据陈旧”。
2. 数据新鲜且 `Status` 为 `critical/down/error/offline/disconnected`：`critical`。
3. 数据新鲜且 `Status` 为 `degraded/warning/unhealthy`：`degraded`。
4. 数据新鲜且 `Status` 为 `healthy`：`healthy`。
5. `active/ready/running/ok` 只说明注册或进程状态，不能单独产生健康结论：`unknown`。

**Acceptance:** 同一集群在平台矩阵、平台列表和集群详情中使用完全相同的 `status/status_reason`；0 覆盖或陈旧状态不计入 healthy；平台能力摘要来自与系统管理相同的组件探测结果，不再出现硬编码 `1/1`。

- [ ] **Step 1: 写失败测试锁定健康真值与能力摘要**

```go
func TestProjectClusterOperationalStateDoesNotTreatRegistrationAsHealth(t *testing.T) {
    now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
    active := store.Cluster{Status: "active", LifecycleStatus: "ready", UpdatedAt: now.Add(-30 * time.Second)}
    if got := projectClusterOperationalState(active, now); got.Health != "unknown" || !got.Covered {
        t.Fatalf("active registration projection=%+v", got)
    }
    stale := store.Cluster{Status: "healthy", LifecycleStatus: "ready", UpdatedAt: now.Add(-6 * time.Minute)}
    if got := projectClusterOperationalState(stale, now); got.Health != "unknown" || !got.Stale || got.Covered {
        t.Fatalf("stale projection=%+v", got)
    }
    healthy := store.Cluster{Status: "healthy", LifecycleStatus: "ready", UpdatedAt: now.Add(-30 * time.Second)}
    if got := projectClusterOperationalState(healthy, now); got.Health != "healthy" || !got.Covered {
        t.Fatalf("healthy projection=%+v", got)
    }
}

func TestSummarizePlatformCapabilitiesUsesProbeRows(t *testing.T) {
    rows := []systemComponentResultView{{Name: "query-api", Status: "ok"}, {Name: "ingest", Status: "down"}, {Name: "minio", Status: "not_configured"}}
    got := summarizePlatformCapabilities(rows)
    if got.Healthy != 1 || got.Total != 2 || len(got.Issues) != 1 || got.Issues[0] != "ingest" {
        t.Fatalf("summary=%+v", got)
    }
}
```

更新 `platform_overview_test.go`：陈旧 `healthy` 集群必须计入 unknown；更新 `cluster_overview_test.go`：同一输入必须得到相同状态与原因。

- [ ] **Step 2: 运行定向测试并确认旧映射失败**

Run: `cd ai-apm-query-go && go test ./internal/api -run 'ProjectClusterOperationalState|SummarizePlatformCapabilities|PlatformOverview|ClusterOverview' -count=1`

Expected: FAIL；新纯函数不存在，旧实现仍把 active/ready 直接映射为 healthy。

- [ ] **Step 3: 实现纯投影并在三个页面复用**

在 `platform_health.go` 定义：

```go
const clusterEvidenceFreshness = 5 * time.Minute

type clusterOperationalState struct {
    Health             string
    RegistrationStatus string
    Reason             string
    Covered            bool
    Stale              bool
}

func projectClusterOperationalState(cluster store.Cluster, now time.Time) clusterOperationalState {
    registration := strings.ToLower(strings.TrimSpace(cluster.LifecycleStatus))
    if cluster.UpdatedAt.IsZero() {
        return clusterOperationalState{Health: "unknown", RegistrationStatus: registration, Reason: "没有观测证据"}
    }
    if now.Sub(cluster.UpdatedAt) > clusterEvidenceFreshness {
        return clusterOperationalState{Health: "unknown", RegistrationStatus: registration, Reason: "观测数据陈旧", Stale: true}
    }
    state := strings.ToLower(strings.TrimSpace(cluster.Status))
    switch state {
    case "critical", "down", "error", "offline", "disconnected":
        return clusterOperationalState{Health: "critical", RegistrationStatus: registration, Reason: "观测到严重异常", Covered: true}
    case "degraded", "warning", "unhealthy":
        return clusterOperationalState{Health: "degraded", RegistrationStatus: registration, Reason: "观测到降级信号", Covered: true}
    case "healthy":
        return clusterOperationalState{Health: "healthy", RegistrationStatus: registration, Reason: "健康证据有效", Covered: true}
    default:
        return clusterOperationalState{Health: "unknown", RegistrationStatus: registration, Reason: "只有接入状态，缺少资源健康证据", Covered: true}
    }
}
```

将 `system.go` 的 map 结果改成 typed `systemComponentResultView`，`SystemComponents` 输出该 slice；`PlatformOverview` 对同一 slice 调用 `summarizePlatformCapabilities`，其中 `not_configured` 不进入分母，非健康项名称进入 `issues`。

- [ ] **Step 4: 更新前端 DTO 与一致性呈现**

在 `platform.ts` 增加：

```ts
export interface PlatformClusterItem {
  clusterId: string
  name: string
  status: PlatformClusterHealth
  registrationStatus: string
  statusReason: string
  updatedAt: string
}
```

平台列表显示 `statusReason` 和“接入：已就绪/待处理”；集群详情复用相同健康标签。能力总数为 0 时显示“未获得组件状态”，不显示 `0/0 正常`。

- [ ] **Step 5: 运行全链定向测试**

Run: `cd ai-apm-query-go && go test ./internal/api -run 'Platform|ClusterOverview|SystemComponents' -count=1`

Run: `cd observability-frontend && npm test -- --run src/api/platform.test.ts src/pages/Overview/Overview.test.tsx src/pages/Clusters/ClusterOverview.test.tsx`

Expected: PASS；测试明确断言 stale/0 覆盖不显示 healthy，平台与集群详情原因文本相同。

- [ ] **Step 6: 提交**

```bash
git add ai-apm-query-go/internal/api/platform_health.go ai-apm-query-go/internal/api/platform_health_test.go ai-apm-query-go/internal/api/platform_overview.go ai-apm-query-go/internal/api/platform_overview_test.go ai-apm-query-go/internal/api/cluster_overview.go ai-apm-query-go/internal/api/cluster_overview_test.go ai-apm-query-go/internal/api/system.go observability-frontend/src/api/platform.ts observability-frontend/src/api/platform.test.ts observability-frontend/src/pages/Overview observability-frontend/src/pages/Clusters
git commit -m "fix(platform): unify observed cluster health facts"
```

### Task 4: 收紧默认图谱搜索边界并删除虚构依赖主链

**Files:**
- Modify: `observability-frontend/src/features/resources/resourceDomain.ts`
- Modify: `observability-frontend/src/features/resources/resourceDomain.test.ts`
- Modify: `observability-frontend/src/api/knowledgeGraph.ts`
- Modify: `observability-frontend/src/api/knowledgeGraph.test.ts`
- Modify: `observability-frontend/src/pages/observability/ResourceRelationships.tsx`
- Modify: `observability-frontend/src/pages/observability/ResourceRelationships.test.tsx`
- Modify: `ai-apm-query-go/internal/api/graph_public.go`
- Modify: `ai-apm-query-go/internal/api/graph_public_test.go`
- Modify: `observability-frontend/src/components/graph/graphPresentation.ts`
- Modify: `observability-frontend/src/components/graph/graphPresentation.test.ts`
- Modify: `observability-frontend/src/components/graph/DependencyChain.tsx`
- Create: `observability-frontend/src/components/graph/DependencyChain.test.tsx`

**Interfaces:**
- Produces: `DEFAULT_GRAPH_SEARCH_TYPES` 与 `isDefaultGraphSearchType(type)`；原 `SELECTABLE_RESOURCE_TYPES` 继续兼容详情和历史数据。
- API 增量：`GET /api/v1/ai/kg/entities/search?q=...&profile=operations`。
- `searchGraphAliases(ctx, scope, entityType, profile, query string, limit int)`：operations profile 固定读取最多 50 个 alias 候选，先解析实体并过滤 profile，再应用调用方 limit。
- Produces: `buildRelationChains(rows, centerUid, mode)`；每条链必须由真实 edge 连接，不再遍历 vertices 顺序。

**Default operations profile:** 允许八类容器主资源、VM/VMI、Kubernetes Node、Physical Server，以及 PVC/PV/StorageClass、DataVolume、NAD、Network 等真实依赖；排除 Container、ReplicaSet、EndpointSlice、business、application、service、middleware 和事件/证据节点。

- [ ] **Step 1: 写失败测试锁定服务端和前端边界**

```ts
it('keeps compatibility types but excludes them from default graph search', () => {
  expect(isSelectableResourceType('replicaset')).toBe(true)
  expect(isDefaultGraphSearchType('replicaset')).toBe(false)
  expect(isDefaultGraphSearchType('service')).toBe(false)
  expect(isDefaultGraphSearchType('pod')).toBe(true)
  expect(isDefaultGraphSearchType('vmi')).toBe(true)
  expect(isDefaultGraphSearchType('pvc')).toBe(true)
})
```

```go
func TestGraphSearchOperationsProfileExcludesCompatibilityAndDetailOnlyTypes(t *testing.T) {
    repo := graphpkg.NewMemoryRepository()
    types := []string{"pod", "vmi", "pvc", "replicaset", "service", "middleware"}
    vertices := make([]graphpkg.Entity, 0, len(types))
    for _, entityType := range types {
        vertices = append(vertices, graphpkg.Entity{
            EntityUID: "entity:" + entityType, EntityType: entityType,
            TenantID: "tenant-a", ClusterID: "cluster-a",
            Name: "item-" + entityType, NameKey: "item-" + entityType,
            Source: "test", Status: "active",
        })
    }
    if _, err := repo.BatchMutate(context.Background(), graphpkg.MutationBatch{TenantID: "tenant-a", ClusterID: "cluster-a", Vertices: vertices}); err != nil {
        t.Fatal(err)
    }
    h := &Handler{graphRepo: repo}
    req := httptest.NewRequest(http.MethodGet, "/api/v1/ai/kg/entities/search?q=item&profile=operations&limit=20", nil)
    req = withAuthorizationContext(req, AuthorizationContext{UserID: "user", TenantID: "tenant-a", SessionID: "session", ActiveClusterID: "cluster-a"})
    rec := httptest.NewRecorder()
    h.GraphPublicRouter(rec, req)
    if rec.Code != http.StatusOK { t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String()) }
    var body struct { Items []graphpkg.Entity `json:"items"` }
    if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil { t.Fatal(err) }
    got := make([]string, 0, len(body.Items))
    for _, item := range body.Items { got = append(got, item.EntityType) }
    sort.Strings(got)
    if !reflect.DeepEqual(got, []string{"pod", "pvc", "vmi"}) { t.Fatalf("types=%v", got) }
}
```

为 `buildRelationChains` 构造两个不相连的连通分量，断言输出中不存在跨分量相邻节点。

- [ ] **Step 2: 运行测试并确认当前实现泄漏类型/串链**

Run: `cd ai-apm-query-go && go test ./internal/api -run GraphSearchOperationsProfile -count=1`

Run: `cd observability-frontend && npm test -- --run src/features/resources/resourceDomain.test.ts src/api/knowledgeGraph.test.ts src/components/graph/graphPresentation.test.ts src/components/graph/DependencyChain.test.tsx src/pages/observability/ResourceRelationships.test.tsx`

Expected: FAIL；profile 未实现，DependencyChain 仍按 vertices 顺序插箭头。

- [ ] **Step 3: 实现服务端 profile 过滤和前端请求**

```go
var operationsGraphSearchTypes = map[string]struct{}{
    "deployment": {}, "statefulset": {}, "daemonset": {}, "job": {}, "cronjob": {}, "pod": {}, "k8s_service": {}, "ingress": {},
    "vm": {}, "vmi": {}, "k8s_node": {}, "physical_server": {},
    "pvc": {}, "pv": {}, "storage_class": {}, "data_volume": {}, "volume": {}, "disk_device": {},
    "nad": {}, "network": {}, "virtual_interface": {}, "cni": {}, "nic": {}, "switch": {}, "switch_port": {},
}

func graphSearchProfileAllows(profile, entityType string) bool {
    if profile == "" { return true }
    if profile != "operations" { return false }
    _, ok := operationsGraphSearchTypes[entityType]
    return ok
}
```

未知 profile 返回 `GRAPH_INVALID_ARGUMENT`，而不是退回全类型搜索。`searchGraphAliases` 对 alias DAO 和 MemoryRepository 两条路径都使用以下顺序，避免先按调用方 limit 截断后再由前端过滤：

```go
candidateLimit := limit
if profile == "operations" { candidateLimit = 50 }
aliases, err := h.graphAliasDAO.Search(scope.TenantID, firstScopeClusterID(scope), graphpkg.NameKeyV1(query), candidateLimit)
if err == nil {
    items := make([]graphpkg.Entity, 0, limit)
    for _, alias := range aliases {
        entity, getErr := h.graphRepo.GetEntity(ctx, scope, alias.CanonicalEntityUID)
        if getErr != nil { return nil, getErr }
        if entityType != "" && entity.EntityType != entityType { continue }
        if !graphSearchProfileAllows(profile, entity.EntityType) { continue }
        items = append(items, entity)
        if len(items) == limit { break }
    }
    return items, nil
}
```

MemoryRepository 路径同样先取 `candidateLimit`，再用 `graphSearchProfileAllows` 过滤并截到 `limit`。前端顶级图谱页固定传 `profile: 'operations'`，删除“应用服务”域选项。

- [ ] **Step 4: 用真实 edge 构建可读关系链**

```ts
export interface RelationChain {
  id: string
  source: string
  relation: string
  target: string
  factStatus: 'fact' | 'inferred'
}

export function buildRelationChains(rows: GraphRelationRow[], centerUid: string, mode: GraphViewMode): RelationChain[] {
  return rows
    .filter((row) => mode !== 'failure-chain' || row.propagatesFailure)
    .filter((row) => row.sourceUid === centerUid || row.targetUid === centerUid || mode === 'failure-chain')
    .slice(0, 6)
    .map((row) => ({ id: row.id, source: row.sourceName, relation: row.label, target: row.targetName, factStatus: row.factStatus }))
}
```

`DependencyChain` 放为逐行显示 `源资源 ─关系→ 目标资源`，标题改为“关键关系链”；没有符合条件的 edge 时显示明确空态。

- [ ] **Step 5: 运行图谱数据合同测试**

Run: `cd ai-apm-query-go && go test ./internal/api -run 'GraphPublic|GraphSearchOperationsProfile' -count=1`

Run: `cd observability-frontend && npm test -- --run src/features/resources/resourceDomain.test.ts src/api/knowledgeGraph.test.ts src/components/graph src/pages/observability/ResourceRelationships.test.tsx`

Expected: PASS；兼容数据仍可通过详情/API 精确类型访问，默认图谱不显示被隐藏类型。

- [ ] **Step 6: 提交**

```bash
git add ai-apm-query-go/internal/api/graph_public.go ai-apm-query-go/internal/api/graph_public_test.go observability-frontend/src/features/resources observability-frontend/src/api/knowledgeGraph.ts observability-frontend/src/api/knowledgeGraph.test.ts observability-frontend/src/pages/observability/ResourceRelationships.tsx observability-frontend/src/pages/observability/ResourceRelationships.test.tsx observability-frontend/src/components/graph/graphPresentation.ts observability-frontend/src/components/graph/graphPresentation.test.ts observability-frontend/src/components/graph/DependencyChain.tsx observability-frontend/src/components/graph/DependencyChain.test.tsx
git commit -m "fix(graph): constrain search and render factual relation chains"
```

### Task 5: 重构图谱视觉语法、布局和 1024px 检查器

**Files:**
- Modify: `observability-frontend/src/components/graph/graphPresentation.ts`
- Modify: `observability-frontend/src/components/graph/graphPresentation.test.ts`
- Modify: `observability-frontend/src/components/graph/GraphMap.tsx`
- Modify: `observability-frontend/src/components/graph/GraphMap.test.tsx`
- Modify: `observability-frontend/src/components/graph/GraphExplorer.tsx`
- Modify: `observability-frontend/src/components/graph/GraphExplorer.test.tsx`
- Modify: `observability-frontend/src/components/graph/GraphLegend.tsx`
- Modify: `observability-frontend/src/index.css`

**Interfaces:**
- `edgeStyle(edge, mode)`：结构视图永远用 neutral；只有 failure-chain 中 `propagates_failure=true` 的事实边使用 failure；推断边始终为 dashed warning。
- 图节点最小尺寸 `196×64`，正文最小 12px；箭头不小于 10px；edge label 最小 12px 并带不透明背景。
- 1024px 时画布保留完整宽度，关系检查器通过“查看关系”按钮进入 Drawer。

**Acceptance:** CONTAINS/OWNS 等结构边在资源关系模式不得为红色；故障传播模式才允许红色；任一可见边无需猜颜色即可读取中文关系和方向；80/200 预算、总匹配量和当前加载量始终可见。

- [ ] **Step 1: 写失败测试锁定模式相关颜色与画布尺寸**

```ts
it('uses red only for failure propagation mode', () => {
  const input = graph()
  input.edges[0].relation_type = 'CONTAINS'
  input.edges[0].propagates_failure = true
  expect(buildGraphDisplayModel(input, { mode: 'resource-relations', centerUid: input.center_entity_uid, maxNodes: 80, maxEdges: 200 }).edges[0].style).toBe('structural')
  expect(buildGraphDisplayModel(input, { mode: 'failure-chain', centerUid: input.center_entity_uid, maxNodes: 80, maxEdges: 200 }).edges[0].style).toBe('failure')
  input.edges[0].status = 'inferred'
  expect(buildGraphDisplayModel(input, { mode: 'failure-chain', centerUid: input.center_entity_uid, maxNodes: 80, maxEdges: 200 }).edges[0].style).toBe('inferred')
})
```

在 `GraphMap.test.tsx` 捕获 G6 options 并断言：

```ts
expect(options.node.style.size).toEqual([196, 64])
expect(options.node.style.labelFontSize).toBeGreaterThanOrEqual(12)
expect(options.edge.style.labelFontSize).toBeGreaterThanOrEqual(12)
expect(options.edge.style.endArrowSize).toBeGreaterThanOrEqual(10)
expect(options.layout.ranksep).toBeGreaterThanOrEqual(120)
```

- [ ] **Step 2: 运行组件测试并确认失败**

Run: `cd observability-frontend && npm test -- --run src/components/graph/graphPresentation.test.ts src/components/graph/GraphMap.test.tsx src/components/graph/GraphExplorer.test.tsx`

Expected: FAIL；现有节点为 `150×52`，关系样式不接收 mode，检查器始终在主内容下方。

- [ ] **Step 3: 实现视图相关语法与更可读的 G6 配置**

```ts
function edgeStyle(edge: GraphEdge, mode: GraphViewMode): GraphDisplayEdge['style'] {
  if (edge.attrs?.fact_status === 'inferred' || edge.status === 'candidate' || edge.status === 'inferred') return 'inferred'
  if (mode === 'failure-chain' && edge.propagates_failure) return 'failure'
  return 'structural'
}
```

G6 配置使用：

```ts
layout: { type: 'dagre', rankdir, nodesep: 54, ranksep: 132 },
node: { type: 'rect', style: { size: [196, 64], radius: 8, labelFontSize: 12, labelMaxWidth: 174, labelMaxLines: 2, labelWordWrap: true } },
edge: { type: 'polyline', style: { endArrow: true, endArrowSize: 10, lineWidth: 1.6, labelFontSize: 12, labelBackgroundOpacity: 1, labelPadding: [3, 6] } },
behaviors: ['drag-canvas', 'zoom-canvas', 'drag-element'],
```

结构色使用 `var(--graph-edge-structural)` 对应的中性灰，故障使用 `var(--danger)`，推断使用 `var(--warning)`；健康色不得用作资源类别色。

- [ ] **Step 4: 实现检查器 Drawer 和可访问按钮**

`GraphExplorer` 保留桌面内联 inspector；为该区域增加 `.graph-inline-inspector`，按钮使用 `.graph-inspector-trigger`。按钮常驻 DOM 但桌面隐藏，在 1024px 及以下显示，同时通过 media query 隐藏内联 inspector：

```tsx
<Button className="graph-inspector-trigger" disabled={!selectedEdge} onClick={() => setInspectorOpen(true)}>
  查看关系
</Button>
<Drawer title="关系检查器" open={inspectorOpen} onClose={() => setInspectorOpen(false)} width="min(420px, 100vw)">
  {selectedEdge ? <RelationInspector edge={selectedEdge} /> : <DataState kind="empty" compact title="请选择一条关系" />}
</Drawer>
```

CSS 在 `@media (max-width: 1024px)` 令 `.graph-inspector-trigger { display: inline-flex; }`、`.graph-inline-inspector { display: none; }`；Drawer body 使用内部滚动。

- [ ] **Step 5: 运行组件测试与构建**

Run: `cd observability-frontend && npm test -- --run src/components/graph && npm run build`

Expected: PASS；测试覆盖 structural/failure/inferred 三类边和 1024px inspector 入口。

- [ ] **Step 6: 提交**

```bash
git add observability-frontend/src/components/graph observability-frontend/src/index.css
git commit -m "fix(graph): make relations and directions visually explicit"
```

### Task 6: 将调查与处置长表格改为摘要列表加完整详情

**Files:**
- Create: `observability-frontend/src/features/workflow/statusPresentation.ts`
- Create: `observability-frontend/src/features/workflow/statusPresentation.test.ts`
- Modify: `observability-frontend/src/pages/investigation/InvestigationCenter.tsx`
- Modify: `observability-frontend/src/pages/investigation/InvestigationCenter.test.tsx`
- Modify: `observability-frontend/src/pages/Actions/ActionCenter.tsx`
- Modify: `observability-frontend/src/pages/Actions/ActionCenter.test.tsx`
- Modify: `observability-frontend/src/pages/Actions/actionModel.ts`
- Modify: `observability-frontend/src/pages/Actions/actionModel.test.ts`
- Modify: `observability-frontend/src/index.css`

**Interfaces:**
- Produces: `investigationStatusLabel(status)`、`actionStatusLabel(status)`、`executionStatusLabel(status)`。
- 调查首屏列固定为：资源、症状、状态、证据、发起时间、操作。
- 处置首屏列固定为：操作/目标、风险、审批、执行、验证、操作。
- 完整 UID、Run ID、集群 ID、冻结窗口、根因、置信度、发起人、快照和审计字段进入 Drawer，保留复制入口。

**Acceptance:** 1440、1280、1024 视口中主名称与状态可读；任何 UUID 不得逐字换行；所有原字段可从详情访问；界面不再显示 `awaiting approval`、`proposed`、`failed` 等裸英文状态。

- [ ] **Step 1: 写失败测试锁定中文状态和摘要列**

```ts
it.each([
  ['awaiting_approval', '待审批'],
  ['investigating', '调查中'],
  ['failed', '失败'],
  ['cancelled', '已取消'],
])('maps investigation status %s', (input, expected) => {
  expect(investigationStatusLabel(input)).toBe(expected)
})

it.each([
  ['proposed', '待审批'],
  ['approved', '已批准'],
  ['running', '执行中'],
  ['succeeded', '已成功'],
])('maps action status %s', (input, expected) => {
  expect(actionStatusLabel(input)).toBe(expected)
})
```

在两个页面测试中断言主表不再出现“集群”“影响”“持续时间”“冻结窗口”“Run ID”等辅助表头，点击“查看详情”后 Drawer 中可以读取完整值。

- [ ] **Step 2: 运行工作流前端测试并确认失败**

Run: `cd observability-frontend && npm test -- --run src/features/workflow/statusPresentation.test.ts src/pages/investigation/InvestigationCenter.test.tsx src/pages/Actions/ActionCenter.test.tsx src/pages/Actions/actionModel.test.ts`

Expected: FAIL；状态 helper 不存在，调查表仍包含 14 列，状态仍直接替换下划线。

- [ ] **Step 3: 实现状态投影与调查详情 Drawer**

```ts
const INVESTIGATION_STATUS_LABELS: Record<string, string> = {
  created: '已创建', planning: '规划中', investigating: '调查中', awaiting_confirmation: '待确认',
  awaiting_approval: '待审批', executing: '执行中', verifying: '验证中', success: '已完成',
  partial: '部分完成', failed: '失败', regressed: '发生回归', cancelled: '已取消',
}

export const investigationStatusLabel = (status: string) => INVESTIGATION_STATUS_LABELS[status] ?? '未知'
```

调查 Table 设置 `tableLayout="fixed"`；资源宽 250、症状自适应、状态 110、证据 80、时间 160、操作 96。证据和时间列设置 `responsive: ['xl']`，1024/1280 内容区只保留资源、症状、状态和操作。行点击和“查看详情”打开 `Drawer width="min(720px, 100vw)"`，使用 `Descriptions` 展示完整字段，ID 使用 `Typography.Text copyable code`。

- [ ] **Step 4: 收敛处置表并统一流程中文标签**

`toActionViewModel` 输出已本地化的 `approvalStatus/executionStatus/verificationStatus`；`lifecycle()` 只消费这些 view 字段。Table 删除来源 Run、影响范围、创建时间首屏列，这些字段进入现有动作详情 Drawer；风险和验证列设置 `responsive: ['xl']`，保证 1024/1280 内容区优先显示目标、审批、执行和操作。页面描述从“所有环境变更”改为“当前集群的全部处置动作”。

- [ ] **Step 5: 运行测试与构建**

Run: `cd observability-frontend && npm test -- --run src/features/workflow src/pages/investigation/InvestigationCenter.test.tsx src/pages/Actions && npm run build`

Expected: PASS；所有裸状态有稳定中文映射，详情仍可访问完整字段。

- [ ] **Step 6: 提交**

```bash
git add observability-frontend/src/features/workflow observability-frontend/src/pages/investigation/InvestigationCenter.tsx observability-frontend/src/pages/investigation/InvestigationCenter.test.tsx observability-frontend/src/pages/Actions observability-frontend/src/index.css
git commit -m "fix(workflow): replace dense tables with readable summaries"
```

### Task 7: 修复助手 1024px 布局并建立真实结构化回答门禁

**Files:**
- Modify: `observability-frontend/src/pages/ai/AiChat.tsx`
- Modify: `observability-frontend/src/pages/ai/AiChat.test.tsx`
- Modify: `observability-frontend/src/pages/Assistant/AssistantAnswerCard.test.tsx`
- Modify: `observability-frontend/src/index.css`
- Create: `tests/manual-e2e/ia-assistant-v3.js`

**Interfaces:**
- 桌面：220px 会话 + minmax(0,1fr) 回答 + 300px 上下文。
- `max-width:1024px`：只保留回答主列；会话和上下文各由一个可访问 Drawer 入口打开。
- 真实回答门禁：必须渲染结论、关键事实证据、运维知识引用、不确定性与缺失证据、建议下一步，以及“不直接执行”边界。

**Acceptance:** 1024×900 首屏中 `.assistant-session-pane` 和直接子级 `.assistant-context-panel` 的 computed display 为 `none`；主列宽度至少占 workspace 的 95%；两个 Drawer 入口可用；发送只读诊断问题后得到结构化回答或明确、可重试的服务不可用状态，不能以空白会话判通过。

- [ ] **Step 1: 写失败测试识别 inline style 覆盖问题**

在 `AiChat.test.tsx` 增加源码合同：

```ts
it('does not use inline display on responsive panes', () => {
  expect(source).not.toMatch(/assistant-session-pane[^>]+style=\{\{[^}]*display:/)
  expect(source).not.toMatch(/assistant-main-pane[^>]+style=\{\{[^}]*display:/)
  expect(source).toContain('assistant-conversation-drawer')
  expect(source).toContain('assistant-context-drawer')
})
```

更新 `AssistantAnswerCard.test.tsx`，断言六个固定区域按顺序出现，空引用显示缺口而不是伪造事实。

- [ ] **Step 2: 运行助手测试并确认失败**

Run: `cd observability-frontend && npm test -- --run src/pages/ai/AiChat.test.tsx src/pages/Assistant/AssistantAnswerCard.test.tsx`

Expected: FAIL；现有 pane 带 inline `display:flex`，CSS 无法在 1024px 隐藏。

- [ ] **Step 3: 将布局控制移入 CSS 并固定 Drawer 边界**

JSX 只保留 class：

```tsx
<div className="card assistant-session-pane">
<div className="card assistant-main-pane">
<Drawer rootClassName="assistant-conversation-drawer" title="会话" placement="left" width="min(360px, 100vw)" open={mobileSessionOpen} onClose={() => setMobileSessionOpen(false)}>
<Drawer rootClassName="assistant-context-drawer" title="会话 Scope" placement="right" width="min(420px, 100vw)" open={mobileContextOpen} onClose={() => setMobileContextOpen(false)}>
```

CSS：

```css
.assistant-session-pane { width: 220px; display: flex; flex-direction: column; margin-bottom: 0; }
.assistant-main-pane { display: flex; flex-direction: column; margin-bottom: 0; min-width: 0; }
@media (max-width: 1024px) {
  .assistant-workspace { grid-template-columns: minmax(0, 1fr); }
  .assistant-workspace > .assistant-session-pane,
  .assistant-workspace > .assistant-context-panel { display: none; }
  .assistant-main-pane { width: 100%; }
}
```

- [ ] **Step 4: 实现真实浏览器助手检查**

`ia-assistant-v3.js` 使用 `withSession`，在 1440×900 和 1024×900 访问 `/clusters/${ENV.clusterId}/assistant`；发送“总结当前集群可引用的运维事实，不创建调查或动作”，等待 `[data-testid=assistant-answer-card]` 或显式 `role=alert`。结构化回答出现时检查六段标题、Scope 冻结文本和无“立即执行”；1024 时检查 computed style 与 Drawer。

- [ ] **Step 5: 运行组件测试、构建和浏览器检查**

Run: `cd observability-frontend && npm test -- --run src/pages/ai/AiChat.test.tsx src/pages/Assistant/AssistantAnswerCard.test.tsx && npm run build`

Run: `node tests/manual-e2e/ia-assistant-v3.js`

Expected: 单元测试 PASS；真实浏览器输出 PASS。若 AI 服务不可用，脚本必须记录 FAIL，而不是把显式错误当成成功。

- [ ] **Step 6: 提交**

```bash
git add observability-frontend/src/pages/ai/AiChat.tsx observability-frontend/src/pages/ai/AiChat.test.tsx observability-frontend/src/pages/Assistant/AssistantAnswerCard.test.tsx observability-frontend/src/index.css tests/manual-e2e/ia-assistant-v3.js
git commit -m "fix(assistant): preserve answer width at compact desktop"
```

### Task 8: 恢复桌面导航可发现性并收敛空态留白

**Files:**
- Modify: `observability-frontend/src/App.tsx`
- Modify: `observability-frontend/src/test/v3Acceptance.test.tsx`
- Modify: `observability-frontend/src/features/scope/ScopeBar.tsx`
- Modify: `observability-frontend/src/features/scope/ScopeBar.test.tsx`
- Modify: `observability-frontend/src/pages/Knowledge/index.tsx`
- Modify: `observability-frontend/src/pages/Knowledge/Knowledge.test.tsx`
- Modify: `observability-frontend/src/pages/report/Report.tsx`
- Modify: `observability-frontend/src/pages/Reports/index.test.tsx`
- Modify: `observability-frontend/src/pages/admin/AdminSettings.tsx`
- Modify: `observability-frontend/src/pages/admin/AdminSettings.test.tsx`
- Modify: `observability-frontend/src/index.css`

**Interfaces:**
- 桌面导航宽度仍遵守 1440px 为 88px、1280/1024 为 72px，但每项使用“图标在上、11px 文字在下”，不依赖 hover title 才能识别。
- `ScopeBar` 的 Select 是集群名唯一常驻显示；删除相邻重复 `.scope-bar__summary`。
- 空态组件提供一个与当前权限一致的主动作，不增加虚构数据。

**Acceptance:** 所有一级导航文字在 1440/1280/1024 可见；顶栏集群名称只出现于选择器；知识、报告、系统管理在无数据时使用自然高度，不产生 450px 以上无信息空白。

- [ ] **Step 1: 写失败测试锁定导航、顶栏和空态**

```tsx
it('renders visible labels for every primary navigation item', async () => {
  render(<MemoryRouter initialEntries={['/overview']}><AppLayout /></MemoryRouter>)
  for (const label of ['平台', '集群', '助手', '调查', '处置', '知识', '报告', '系统管理']) {
    expect(await screen.findByText(label, { selector: '.nav__label' })).toBeVisible()
  }
})
```

`ScopeBar.test.tsx` 断言集群名只存在于 combobox 的 selected value，不再存在 `.scope-bar__summary`。知识空列表时断言“当前类型暂无知识”和“新增知识”；报告空列表时断言“当前集群暂无报告”和“前往调查”；无写权限时不显示新增按钮。

- [ ] **Step 2: 运行相关测试并确认失败**

Run: `cd observability-frontend && npm test -- --run src/test/v3Acceptance.test.tsx src/features/scope/ScopeBar.test.tsx src/pages/Knowledge/Knowledge.test.tsx src/pages/Reports/index.test.tsx src/pages/admin/AdminSettings.test.tsx`

Expected: FAIL；`isCollapsed=true` 隐藏所有文字，ScopeBar 重复显示集群名，空态缺少要求的动作。

- [ ] **Step 3: 实现带标签的固定导航轨和唯一集群显示**

将 App 中 `isCollapsed=true` 分支删除，导航项固定渲染：

```tsx
<div className={'nav__item' + (selectedItem === it ? ' is-active' : '')} role="button" tabIndex={0} aria-label={it.label}>
  <AppIcon name={it.icon} />
  <span className="nav__label">{it.label}</span>
</div>
```

`.nav__item` 改为纵向居中，最小高度 48px；`.nav__label` 为 11px、最多两行。`ScopeBar` 删除 `scope-bar__summary`，平台页顶栏继续显示选择器但不让其影响全局总览请求。

- [ ] **Step 4: 实现紧凑且可行动的空态**

- Knowledge：当前 tab 的 `visibleItems.length===0` 且非 loading/error 时，列表 pane 显示类型化空态；有 `knowledge.write` 时主动作进入 `/clusters/:clusterId/knowledge/new`。
- Reports：空数据时不渲染整张空 Table，显示自然高度卡片和“前往调查”链接；错误态与空态互斥。
- Admin：组件或索引状态无数据时显示“状态未获得”和刷新入口；三张摘要卡自然高度，不通过固定最小高度制造留白。

- [ ] **Step 5: 运行页面测试、全前端测试与构建**

Run: `cd observability-frontend && npm test -- --run src/test/v3Acceptance.test.tsx src/features/scope/ScopeBar.test.tsx src/pages/Knowledge src/pages/Reports src/pages/admin/AdminSettings.test.tsx && npm run test:run && npm run build`

Expected: PASS；导航标签可见，空态权限正确，无禁用文案回归。

- [ ] **Step 6: 提交**

```bash
git add observability-frontend/src/App.tsx observability-frontend/src/test/v3Acceptance.test.tsx observability-frontend/src/features/scope/ScopeBar.tsx observability-frontend/src/features/scope/ScopeBar.test.tsx observability-frontend/src/pages/Knowledge observability-frontend/src/pages/report/Report.tsx observability-frontend/src/pages/Reports observability-frontend/src/pages/admin/AdminSettings.tsx observability-frontend/src/pages/admin/AdminSettings.test.tsx observability-frontend/src/index.css
git commit -m "fix(ui): improve navigation and actionable empty states"
```

### Task 9: 建立真实浏览器语义与显示验收门禁

**Files:**
- Create: `tests/manual-e2e/lib/v3-assertions.js`
- Create: `tests/manual-e2e/lib/v3-assertions.test.js`
- Create: `tests/manual-e2e/v3-acceptance.js`
- Modify: `tests/manual-e2e/ia-graph.js`
- Modify: `tests/manual-e2e/ia-workbench.js`
- Create: `deploy/scripts/verify-v3-ui-acceptance.sh`
- Create: `deploy/scripts/test-v3-ui-acceptance-contract.sh`
- Modify: `deploy/scripts/collect-release-evidence.sh`
- Modify: `deploy/scripts/test-release-evidence-contract.sh`
- Modify: `deploy/evidence/release-evidence.schema.json`

**Interfaces:**
- `assertNoForbiddenProductCopy(text)`：禁止环境选择、生产平台/生产集群、综合健康分数、全局搜索。
- `assertNoBadResponses(responses)`：任何主流程 4xx/5xx 均失败，不允许 resource/observe 路由白名单。
- `assertReadablePrimaryCells(metrics)`：主列不得逐字换行；完整 ID 必须有详情/复制入口。
- `assertHealthFacts(overviewPayload, clusterPayload)`：相同 clusterId 的状态一致，stale 或 uncovered 不得为 healthy。
- 输出 `test-results/<RUN_ID>/v3-acceptance.json`，包含 commit、image tag、Helm revision、视口、请求失败、DOM 断言和截图路径。

**Acceptance:** 发布证据的 `publishable` 判定必须要求 `v3_ui_acceptance=pass`；旧 `ia-workbench.js` 不得再查找“生产平台”；空态只能在相应 API 200 且返回空集合时判为通过。

- [ ] **Step 1: 写 assertion helper 的失败测试**

```js
const test = require('node:test')
const assert = require('node:assert/strict')
const { assertNoForbiddenProductCopy, healthFactsConsistent, primaryCellReadable } = require('./v3-assertions')

test('rejects removed product layers and synthetic scores', () => {
  assert.throws(() => assertNoForbiddenProductCopy('生产平台 综合健康分数 92'))
  assert.doesNotThrow(() => assertNoForbiddenProductCopy('云平台运营态势 集群 上海一号'))
})

test('rejects stale healthy facts', () => {
  assert.equal(healthFactsConsistent({ status: 'healthy', stale: true, covered: false }, { status: 'unknown' }), false)
})

test('rejects one-character wrapping in a primary cell', () => {
  assert.equal(primaryCellReadable({ textLength: 36, renderedLines: 18, clientWidth: 24 }), false)
})
```

- [ ] **Step 2: 运行 helper 测试并确认模块不存在**

Run: `node --test tests/manual-e2e/lib/v3-assertions.test.js`

Expected: FAIL with module not found。

- [ ] **Step 3: 实现无依赖 assertion helper 和统一 runner**

```js
const FORBIDDEN = [/生产平台/, /生产集群/, /综合健康分数/, /健康评分/, /全局搜索/, /\b(prod|dev|staging|test)\b\s*(环境|集群)/i]

function assertNoForbiddenProductCopy(text) {
  const hit = FORBIDDEN.find((pattern) => pattern.test(text))
  if (hit) throw new Error(`forbidden product copy: ${hit}`)
}

function healthFactsConsistent(platform, detail) {
  if ((platform.stale || !platform.covered) && platform.status === 'healthy') return false
  return platform.status === detail.status
}

function primaryCellReadable(metric) {
  if (!metric.textLength) return true
  return metric.clientWidth >= 80 && metric.renderedLines <= 3
}

module.exports = { assertNoForbiddenProductCopy, healthFactsConsistent, primaryCellReadable }
```

`v3-acceptance.js` 复用 `laneD-runner` 的 collector 和真实 UI 登录，顺序访问 overview、cluster、resources、observe、graph、assistant、investigations、actions、knowledge、reports、admin。每个视口记录 pageerror、console error、所有 4xx/5xx、页面级 overflow、主列 computed line count、Drawer 边界和截图。

- [ ] **Step 4: 修正旧门禁并把真实结果绑定到发布证据**

- `ia-workbench.js` 将 `production_scope_bar_visible` 改为 `no_fake_platform_layer`，断言“生产平台”不存在。
- `ia-graph.js` 搜索词由环境变量 `AIOPS_E2E_GRAPH_QUERY` 提供；未配置或无合法资源时 FAIL，不再把“图谱不可用或无匹配”当成通过。
- `verify-v3-ui-acceptance.sh` 要求 `TEST_RUN_ID`、运行脚本并检查结果 JSON 中 `status=PASS`。
- `collect-release-evidence.sh` 在 `AIOPS_V3_UI_ACCEPTANCE_FILE` 存在时校验其 `git_commit/image_tag/helm_revision` 与当前发布材料一致；缺失或不一致时 `checks.v3_ui_acceptance=unverified/fail`，`publishable=false`。

- [ ] **Step 5: 运行门禁合同测试**

Run: `node --test tests/manual-e2e/lib/v3-assertions.test.js`

Run: `bash deploy/scripts/test-v3-ui-acceptance-contract.sh && bash deploy/scripts/test-release-evidence-contract.sh`

Expected: PASS；伪造 PASS、版本不一致、遗漏截图或包含 4xx/5xx 的 fixture 都必须被拒绝。

- [ ] **Step 6: 提交**

```bash
git add tests/manual-e2e/lib/v3-assertions.js tests/manual-e2e/lib/v3-assertions.test.js tests/manual-e2e/v3-acceptance.js tests/manual-e2e/ia-graph.js tests/manual-e2e/ia-workbench.js deploy/scripts/verify-v3-ui-acceptance.sh deploy/scripts/test-v3-ui-acceptance-contract.sh deploy/scripts/collect-release-evidence.sh deploy/scripts/test-release-evidence-contract.sh deploy/evidence/release-evidence.schema.json
git commit -m "test(release): gate v3 on live semantic UI acceptance"
```

### Task 10: 全量验证、生产数据核对、镜像发布与正式复验

**Files:**
- Modify only if a failing gate proves a defect in files owned by Task 1–9.
- Generate: `test-results/<RUN_ID>/v3-acceptance.json`
- Generate: `deploy/evidence/release-evidence.json`
- Do not commit generated credentials, browser storage state, temporary screenshots outside `test-results`, or Kubernetes Secret values.

**Interfaces:**
- Consumes: Task 1–9 的全部提交、现有生产 Secret、真实授权集群、版本化镜像和 Helm chart。
- Produces: 同一 commit/image/Helm revision 绑定的测试、运行、截图和发布证据。

**Acceptance:** 前后端全量测试通过；主路由无 4xx/5xx；平台/集群健康一致；资源目录与观测可用；图谱使用真实关系语法；调查/处置/助手在规定视口可读；正式租户不存在未经确认的测试告警或测试集群；回滚 revision 在发布前记录并可用。

- [ ] **Step 1: 运行静态、单元和构建门禁**

Run: `git diff --check`

Run: `make test-query-go && make test-frontend`

Run: `bash deploy/scripts/test-deployment-contracts.sh && bash deploy/scripts/test-production-architecture-contracts.sh`

Run: `helm lint --strict deploy/helm/aiops`

Expected: 全部 exit 0；不得使用已有绿灯抵消新失败。

- [ ] **Step 2: 核对正式租户数据而不自动删除**

通过管理员 API 导出当前授权集群、活动告警、最新采集时间和知识索引状态。逐项确认：

- 名称含 `kind`、`demo`、`sample`、`regression` 的集群或告警是否为正式纳管对象。
- 任何未获负责人确认的测试对象不得进入正式租户；清理动作必须使用对象的 canonical ID，并保留审计记录。
- 集群最新观测时间超过 5 分钟时必须显示 unknown/stale，不能为了验收人工改成 healthy。
- 无知识样本时允许知识页通过空态验收，但 RAG/引用能力只能记为“未验证”，不能记为完成。

- [ ] **Step 3: 记录回滚点并构建同一版本的全部镜像**

```bash
release_tag="git-$(git rev-parse --short=12 HEAD)"
rollback_revision="$(helm history aiops -n observability -o json | python3 -c 'import json,sys; rows=json.load(sys.stdin); deployed=[r for r in rows if r.get("status")=="deployed"]; print(deployed[-1]["revision"])')"
test -n "$rollback_revision"
IMAGE_TAG="$release_tag" bash deploy/scripts/build-images.sh all
```

Expected: 每个镜像存在且 `org.opencontainers.image.revision` 等于当前完整 commit；tag 不是 `latest`。

- [ ] **Step 4: 使用既有 Secret 升级 Helm release**

发布操作者从批准的 Secret 通道注入 `ADMIN_PASSWORD`；其余既有 Secret 由 `apply.sh` 从集群复用：

```bash
AIOPS_ENV=production IMAGE_TAG="$release_tag" SKIP_IMAGE_BUILD=1 ADMIN_PASSWORD="$ADMIN_PASSWORD" bash deploy/scripts/apply.sh
kubectl -n observability rollout status deployment/query-api --timeout=10m
kubectl -n observability rollout status deployment/observability-frontend --timeout=10m
kubectl -n observability get pods
```

Expected: 新 revision 完成，Query API 与 frontend Ready，所有自研 Pod 使用同一个 `$release_tag`。

- [ ] **Step 5: 在新 revision 上运行真实 V3 验收**

```bash
export TEST_RUN_ID="v3-remediation-${release_tag}"
export AIOPS_E2E_GRAPH_QUERY="aiops"
bash deploy/scripts/verify-v3-ui-acceptance.sh
node tests/manual-e2e/ia-assistant-v3.js
```

人工对照 `UI_RENDER_GUIDE.md` 检查生成的 1440/1280/1024 截图，签字项必须包含：导航标签、顶栏 Scope、总览事实、资源目录、观测、图谱关系、调查/处置详情、助手 Drawer、知识/报告空态。任何关键关系不可读、状态矛盾或逐字换行都判 FAIL。

- [ ] **Step 6: 生成版本绑定发布证据**

```bash
AIOPS_RELEASE_IMAGE_TAG="$release_tag" \
AIOPS_RELEASE_UNIT_TESTS_COMMAND="make test-workflow-all" \
AIOPS_V3_UI_ACCEPTANCE_FILE="test-results/${TEST_RUN_ID}/v3-acceptance.json" \
bash deploy/scripts/collect-release-evidence.sh
```

Expected: 本地部署证据中 `v3_ui_acceptance=pass`。正式 registry 发布还必须满足 registry digest、rendered manifest、签名 binding 与 public key 门禁，才能令 `publishable=true`。

- [ ] **Step 7: 失败时执行明确回滚并保留失败证据**

```bash
helm rollback aiops "$rollback_revision" -n observability --wait --timeout 15m
kubectl -n observability get pods
```

回滚不删除 `test-results/${TEST_RUN_ID}` 和失败 release evidence；在修复提交中引用失败检查名称后重新从 Step 1 执行。

- [ ] **Step 8: 提交最终文档性调整并确认工作树**

只有 Task 10 验收过程中确实修改了文档或门禁配置时才创建提交：

```bash
git add docs/superpowers deploy/scripts tests/manual-e2e
git commit -m "docs(release): record aiops v3 remediation acceptance"
git status --short --branch
```

Expected: `main` 工作树干净；所有实现提交、镜像 revision、Helm revision 和验收 JSON 指向同一 commit。

---

## Final Definition of Done

- 资源目录和观测中心在真实授权集群上返回业务结果或可信空态，无 502/403、panic、pageerror 和 console error。
- 平台总览、平台集群列表、集群详情对同一集群输出相同健康与原因；注册 active/ready 不再等同于健康。
- 平台能力摘要来自真实组件探测；不可用时显示未知/异常，不使用占位分子分母。
- 默认图谱搜索不出现 ReplicaSet、Container、EndpointSlice、业务、应用、APM Service 或中间件；兼容数据仍可通过详情证据访问。
- 图谱所有可见边包含中文关系和方向；结构、故障传播、推断三种语法可区分；关键关系链只由真实 edge 构成。
- 调查与处置首屏没有逐字换行或不可读 UUID；详情 Drawer 可查看和复制所有完整字段；状态全部中文化。
- 助手在 1024×900 为单主列，两侧面板进入 Drawer；真实回答满足六段式合同且没有直接执行入口。
- 一级导航文字在三种桌面视口可见；顶栏集群名称不重复；知识、报告、管理空态自然高度且提供权限允许的下一步。
- 真实浏览器验收结果绑定 commit、image tag 与 Helm revision，并成为 `publishable` 的必要条件。
- 正式租户中的测试集群、回归告警和陈旧数据均有明确归属或已通过审计方式清理；不通过伪造新鲜时间或健康状态换取绿灯。

## Self-Review Record

- Spec coverage：覆盖现场审计的两个运行阻断、健康语义、图谱边界/语法、长表、助手响应式、导航、空态、数据卫生和发布门禁。
- Scope：只调整既有 V3 主链，不新增业务应用管理、中间件管理、跨集群动作或新的基础设施依赖。
- Type consistency：`clusterOperationalState`、`systemComponentResultView`、`DEFAULT_GRAPH_SEARCH_TYPES`、`RelationChain` 和状态投影函数均在首次消费前定义。
- Rollback：无数据库迁移；代码与 UI 可通过 Helm 回滚到发布前 revision，失败证据保留。
- Ambiguity：明确规定 active/ready 只代表接入状态；stale/uncovered 不得为 healthy；真实空态不能替代未执行的能力验收。
