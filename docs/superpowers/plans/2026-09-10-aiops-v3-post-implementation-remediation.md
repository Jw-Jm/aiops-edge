# AIOps V3 Post-Implementation Remediation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task in the existing `main` checkout. Do not create worktrees or run parallel edits against this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 修复 2026-09-10 现场审核和 2026-09-11 OnGrid 对照审核发现的运行阻断、集群边界缺口、健康事实矛盾和核心 UI 判读缺陷，使 AIOps V3 在真实生产部署中满足已确认的信息架构、视觉合同与正式验收门禁。

**Architecture:** 先把 KubeVirt 等所有用户可见 Kubernetes 资源读取收敛到唯一 canonical cluster boundary，再修复资源目录与观测鉴权并建立平台/集群共用的健康事实投影；随后以“默认可搜索资源边界 + 可过滤关系语义 + 点击聚焦 + 真实因果链”重构图谱，以摘要列表、结论优先详情和 Drawer 收敛调查/处置长表格，并增加受策略控制的告警调查草稿/只读调查。最后用真实登录、两个同名资源的隔离集群、真实 API 响应和固定视口浏览器检查替代浅门禁。持久化集群库存只进入独立 RFC 决策门，不阻塞本轮发布。

**Tech Stack:** Go、React 18、TypeScript 5.6、React Router 6、TanStack Query 5、Ant Design 5、AntV G6 5、Vitest、Playwright、Helm/Kubernetes。

**Spec:** [正式信息架构](../specs/2026-09-08-aiops-information-architecture-design.md)；[V3 完整 UI 设计与编码指南](../specs/assets/ui-v2/UI_RENDER_GUIDE.md)；[完整验收说明](../verification/2026-09-11-aiops-v3-complete-acceptance.md)；[原 V3 实施计划](2026-09-10-aiops-cloud-operations-ui-v3.md)。本计划的 `Audited Baseline` 固化了实施后现场审核结果，因此执行不依赖工作区之外的临时报告。

## Global Constraints

- 所有实现与提交都在 `main` 顺序进行；一个任务通过定向验证并提交后再进入下一任务，不创建并行工作树。
- 平台只有生产环境；任何页面、筛选、测试断言和示例文案都不得重新引入 `prod/dev/staging/test` 环境选择或“生产平台/生产集群”伪层级。
- `/overview` 聚合当前用户全部授权集群且不受活动集群影响；其余主工作区绑定一个经 `/me.available_clusters` 验证的真实 Kubernetes 集群。
- 所有用户可见 Kubernetes/KubeVirt 资源读取必须使用 `canonical cluster_id → credential_ref → Secret → kubeconfig → kube-system UID` 唯一边界；禁止使用进程默认 kubeconfig、当前 context、集群名称或空字符串回退。
- 容器一级 Kind 固定为 Deployment、StatefulSet、DaemonSet、Job、CronJob、Pod、Kubernetes Service、Ingress；Container、ReplicaSet、EndpointSlice 仅在详情或证据中出现。
- 业务、应用、APM Service、中间件保留底层兼容数据，但不得进入默认资源目录、默认图谱搜索、一级导航或独立健康结论。
- 顶级健康只使用 `healthy | degraded | critical | unknown`；注册状态与观测健康必须分离，不计算 0–100 综合分数。
- 数据超过显示预算时必须分页、聚合或进入详情；禁止通过逐字换行、不可恢复省略或页面级横向滚动伪装“不超限”。
- 图谱 `80 节点 / 200 边` 只约束首屏渲染；后端查询预算继续使用现有受控上限，完整结果通过总数、聚合、游标和等价关系列表访问。
- 每个数据区的 loading、empty、error、partial、stale、forbidden 必须互斥且语义一致；接口失败时不得同时显示健康空态。
- 助手不得直接执行动作；结构化回答继续包含结论、事实引用、已发布知识引用、不确定性、下一步和受控动作。
- 告警触发只允许创建调查草稿或 `action_mode=read_only` 的调查 Run；不得创建、审批或执行动作。缺省策略保持“人工发起”，管理员可按真实集群改为“自动草稿”或“自动只读调查”。
- 调查必须持久化明确中止原因和预算消耗；`partial` 不是原因，UI 必须区分根因已确认、证据耗尽、预算耗尽、数据源不可用、取消和运行失败。
- 桌面验收视口固定为 `1440×900`、`1280×720`、`1024×768`，助手与知识补充 `1024×900`。
- 本计划不新增应用管理、中间件管理、跨集群批量处置、新图数据库或新前端依赖。
- [OnGrid](https://github.com/ongridio/ongrid) 只作为产品思想、数据合同和交互模式参考；不得复制其 AGPL-3.0 源码或组件。不得引入每主机 Edge/远控隧道、ChatOps 主界面、LLM 执行授权或第二套 Incident 状态机。

## Audited Baseline

- Git 基线：`main`，审核时 HEAD `812013e`，工作树干净，分支领先 `origin/main` 65 个提交。
- 部署基线：Helm release `aiops` revision 55；自研镜像 tag `git-812013e`；Pod Ready，前端入口返回 HTTP 200。
- 自动验证基线：前端 68 个测试文件、164 项测试通过；`npm run build`、`go test ./...`、Helm strict lint 和部署契约通过。
- 现场阻断一：`GET /api/v1/resources/catalog?group=containers` 返回 502；`resource_catalog.go` 在上游 partial 且过滤结果小于公开上限时执行越界切片。
- 现场阻断二：`GET /api/v1/alerts/aggregation` 返回 403；路由已注册，但 `isCanonicalProtectedRoute` 未列入该只读端点。
- 事实矛盾：同一陈旧集群在平台列表显示健康，在集群详情显示降级；平台同时显示 `0/2` 覆盖、2 个未知/陈旧集群和 2 个健康集群。
- 显示缺陷：图谱默认泄漏 ReplicaSet/应用服务、结构边被画成故障红色、依赖主链串联不相邻节点；调查表格逐字换行；助手侧栏的 inline `display:flex` 覆盖 1024px 隐藏规则。
- 集群边界缺口：`internal/api/kubevirt.go` 的 VM/VMI 列表和详情仍使用空 kubeconfig 调用 `kubectl`，可能读取进程当前 context，与多集群严格归属原则冲突；canonical `ListKubeVirtObjects` 已存在但公共端点未复用。
- 调查可读性缺口：现有证据模型、证据不足判定和处置门禁严谨，但告警与 Run 之间没有策略化关联，调查详情缺少固定的因果链与明确终止原因，首屏仍偏工程数据展示。

## File and Ownership Map

- `ai-apm-query-go/internal/api/resource_catalog.go`：资源目录后端预算与 Kubernetes snapshot fallback。
- `ai-apm-query-go/internal/k8sboundary/k8sboundary.go`、`internal/query/kubernetes.go`：唯一集群身份边界与 KubeVirt/事件窄只读能力。
- `ai-apm-query-go/internal/api/kubevirt.go`：公共 VM/VMI 列表和详情，只消费 `KubernetesRepository` 的 cluster-bound snapshot。
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
- `ai-apm-query-go/internal/api/alert_investigation.go`、`internal/store/alert_investigation.go`：告警到既有 Run 的幂等链接、每集群策略、限流和跳过原因；不创建新 Incident 状态机。
- `ai-orchestrator/investigation_runtime.py`、`apps/investigation.py`：调查因果链、预算快照和明确终止原因。
- `observability-frontend/src/features/investigation/model.ts`、`src/pages/investigation/InvestigationShell.tsx`：结论优先调查报告和终止原因呈现。
- `observability-frontend/src/pages/ai/AiChat.tsx`、`src/index.css`：助手三栏与 1024px 单栏/Drawer 切换。
- `observability-frontend/src/App.tsx`、`src/features/scope/ScopeBar.tsx`：可发现的桌面导航和不重复的集群选择。
- `observability-frontend/src/pages/Knowledge/index.tsx`、`src/pages/report/Report.tsx`、`src/pages/admin/AdminSettings.tsx`：紧凑、可行动的真实空态。
- `tests/manual-e2e/v3-acceptance.js`、`tests/manual-e2e/lib/v3-assertions.js`：真实浏览器发布门禁。
- `deploy/scripts/verify-v3-ui-acceptance.sh`、`deploy/scripts/collect-release-evidence.sh`：版本绑定的 UI 验收与发布证据。
- `docs/architecture/ADR-0002-cluster-inventory-projection.md`、`docs/contracts/kubernetes-inventory-projection.md`：全量快照 + Watch 增量库存投影的发布后决策门。

## Delivery Order

1. P0 集群边界与可用性：Task 0–3。
2. P1 判读体验：Task 4–7。
3. P2 视觉与门禁：Task 8–9。
4. P1 告警调查闭环：Task 10–11。
5. 发布后架构决策门：Task 12；不进入本轮镜像发布阻塞项。
6. 正式发布：Task 13。Task 0 必须先完成；Task 3 完成后才允许调整总览截图；Task 4 的数据边界先于 Task 5 的图谱视觉；Task 10 依赖既有 Run/outbox，Task 11 依赖 Task 10 的来源投影；Task 13 只能在 Task 0–11 全部提交进入 `main` 后执行。

---

### Task 0: 消除 KubeVirt 默认 context，统一真实集群读取边界

**Files:**
- Modify: `ai-apm-query-go/internal/query/kubernetes.go`
- Modify: `ai-apm-query-go/internal/query/kubernetes_test.go`
- Modify: `ai-apm-query-go/internal/api/kubernetes_boundary.go`
- Modify: `ai-apm-query-go/internal/api/kubevirt.go`
- Create: `ai-apm-query-go/internal/api/kubevirt_test.go`
- Modify: `ai-apm-query-go/internal/k8sboundary/k8sboundary_test.go`

**Interfaces:**
- Consumes: `AuthorizationContext{TenantID, ActiveClusterID}`、`KubernetesRepository.ListKubeVirtObjects(ctx, scope, clusterID)`、已验证的 `k8sboundary.Client`。
- Produces: `KubeEventClient.ListEvents() ([]map[string]interface{}, error)`；`ListKubeVirtObjects` 固定输出 `virtual_machines`、`virtual_machine_instances`、`events`、`installed`、`partial`、`warning_codes`；公共 VM/VMI API 不再调用 `kubeList("", ...)` 或 `objectEvents(...)`。

**Acceptance:** 使用两个 cluster UUID、相同 namespace/name、不同 VMI UID 的测试时，端点只返回 JWT 活动集群的数据；非法 cluster、identity mismatch 和未配置边界均 fail closed；KubeVirt 未安装与已安装但零 VM 可区分；生产代码搜索不到空 kubeconfig 的 VM/VMI 查询。

- [ ] **Step 1: 写失败测试锁定跨集群隔离和能力状态**

在 `kubevirt_test.go` 构造 `cluster-a`、`cluster-b` 两个 fake accessor，两个集群都含 `default/shared-vm`，但 UID 分别为 `uid-a`、`uid-b`：

```go
func TestVMsUsesAuthorizedCanonicalCluster(t *testing.T) {
    h := kubeVirtHandlerForClusters(map[string]map[string]interface{}{
        clusterA: kubeVirtSnapshot("uid-a", "Running"),
        clusterB: kubeVirtSnapshot("uid-b", "Stopped"),
    })
    req := httptest.NewRequest(http.MethodGet, "/api/v1/infrastructure/vms", nil)
    req = withAuthorizationContext(req, AuthorizationContext{
        UserID: "operator", TenantID: tenantA, ActiveClusterID: clusterA,
    })
    rec := httptest.NewRecorder()
    h.VMs(rec, req)
    if rec.Code != http.StatusOK { t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String()) }
    if strings.Contains(rec.Body.String(), "uid-b") || !strings.Contains(rec.Body.String(), "uid-a") {
        t.Fatalf("cross-cluster response: %s", rec.Body.String())
    }
}
```

再覆盖：无授权上下文返回 403；repository `PermissionDenied` 返回 403；`Unavailable` 返回 503；key 缺失时 `installed=false`，key 存在但 slice 为空时 `installed=true,count=0`。

- [ ] **Step 2: 运行定向测试并确认旧端点失败**

Run: `cd ai-apm-query-go && go test ./internal/api ./internal/query ./internal/k8sboundary -run 'KubeVirt|VMsUsesAuthorized|VMDetailUsesAuthorized|ListKubeVirt' -count=1`

Expected: FAIL；旧 `VMs/VMDetail` 不消费 `AuthorizationContext.ActiveClusterID`，且使用进程默认 kubeconfig。

- [ ] **Step 3: 扩展窄事件能力并保持凭据私有**

在 `internal/query/kubernetes.go` 定义可选窄接口，不向上层暴露 kubeconfig 或任意 kubectl 参数：

```go
type KubeEventClient interface {
    ListEvents() ([]map[string]interface{}, error)
}
```

`boundaryClient.ListEvents()` 只调用 `c.client.KubeEvents()`；`ListKubeVirtObjects` 从同一个已验证 client 读取图对象和事件，并输出：

```go
result["installed"] = hasVirtualMachines || hasVirtualMachineInstances
result["events"] = events
result["partial"] = graphPartial || eventsUnavailable
result["warning_codes"] = dedupeCodes(warningCodes)
```

KubeVirt CRD 不存在时 `installed=false`；CRD 存在且无对象时 `installed=true`。事件读取失败保留 VM/VMI 数据并标记 `partial=true`，不得伪造空的完整结果。

- [ ] **Step 4: 重写公共 VM/VMI handler**

`VMs` 和 `VMDetail` 首先读取 `requestAuthorizationContext`，以 `auth.ActiveClusterID` 构造：

```go
scope := query.KubernetesScope{TenantID: auth.TenantID, ClusterID: auth.ActiveClusterID}
snapshot, err := h.kubeRepo.ListKubeVirtObjects(r.Context(), scope, auth.ActiveClusterID)
```

列表从 `virtual_machine_instances` 投影，详情按 namespace/name 精确匹配并保留 metadata.uid/resourceVersion、phase、nodeName、podName、接口、资源请求、卷和关联 Warning 事件。禁止名称跨 namespace 匹配；没有对象返回 404；边界错误使用统一 `permission_denied/unavailable` HTTP 映射。

- [ ] **Step 5: 运行测试、静态边界检查和 Go 全量测试**

Run: `cd ai-apm-query-go && go test ./internal/api ./internal/query ./internal/k8sboundary -run 'KubeVirt|VMs|VMDetail|ListKubeVirt' -count=1`

Run: `! rg -n 'kubeList\("".*vmi|objectEvents\(' ai-apm-query-go/internal/api/kubevirt.go`

Run: `cd ai-apm-query-go && go test ./...`

Expected: 全部 PASS；静态检查无命中；两个同名 VMI 测试证明 cluster/namespace/UID 隔离。

- [ ] **Step 6: 提交**

```bash
git add ai-apm-query-go/internal/query/kubernetes.go ai-apm-query-go/internal/query/kubernetes_test.go ai-apm-query-go/internal/api/kubernetes_boundary.go ai-apm-query-go/internal/api/kubevirt.go ai-apm-query-go/internal/api/kubevirt_test.go ai-apm-query-go/internal/k8sboundary/k8sboundary_test.go
git commit -m "fix(kubevirt): enforce canonical cluster boundary"
```

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
- Modify: `observability-frontend/src/components/graph/GraphRelationList.tsx`
- Modify: `observability-frontend/src/index.css`

**Interfaces:**
- `edgeStyle(edge, mode)`：结构视图永远用 neutral；只有 failure-chain 中 `propagates_failure=true` 的事实边使用 failure；推断边始终为 dashed warning。
- `RelationSemantic = 'structural' | 'runtime' | 'traffic' | 'storage' | 'network' | 'failure-propagation' | 'inferred'`；每个 raw `relation_type` 必须映射到一个稳定语义，未知类型归入 `structural` 并显示原始关系名。
- `focusGraph(display, selection)`：选择节点时保留其一跳直接关系和二跳上下游上下文，选择边时保留源、目标及该边；其他节点/边进入 dimmed，不从等价关系列表消失。
- Namespace 与 Node 默认作为折叠上下文；当它们是中心资源、告警目标、关键故障传播节点或用户主动展开时才作为普通节点展示。VM/VMI 与 Pod/Workload/Service 保持同级主对象。
- 图节点最小尺寸 `196×64`，正文最小 12px；箭头不小于 10px；edge label 最小 12px 并带不透明背景。
- 1024px 时画布保留完整宽度，关系检查器通过“查看关系”按钮进入 Drawer。

**Acceptance:** CONTAINS/OWNS 等结构边在资源关系模式不得为红色；故障传播模式才允许红色；任一可见边无需猜颜色即可读取中文关系和方向；点击节点或边后无关路径明显弱化且检查器同步；按语义筛选不会改变原始事实；节点健康状态使用文字/图标/边框共同表达；80/200 预算、总匹配量和当前加载量始终可见。

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

补充纯函数测试：`relationSemantic` 覆盖结构、运行、流量、存储、网络、故障传播和推断；`focusGraph` 选中节点后直接边不 dimmed、无关分支 dimmed；Namespace/Node 只有满足展开条件才进入默认节点集合。

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

同时新增集中式语义注册表，禁止在组件内部按关系字符串临时猜颜色：

```ts
export type RelationSemantic = 'structural' | 'runtime' | 'traffic' | 'storage' | 'network' | 'failure-propagation' | 'inferred'

const RELATION_SEMANTICS: Record<string, RelationSemantic> = {
  CONTAINS: 'structural', OWNS: 'structural', CONTROLS: 'runtime', RUNS_ON: 'runtime',
  ROUTES_TO: 'traffic', SELECTS: 'traffic', USES_VOLUME: 'storage', BOUND_TO: 'storage',
  CONNECTS_TO_NAD: 'network', USES_CNI: 'network', CONNECTS_TO_NETWORK: 'network',
}

export function relationSemantic(edge: GraphEdge, mode: GraphViewMode): RelationSemantic {
  if (edge.attrs?.fact_status === 'inferred' || edge.status === 'candidate' || edge.status === 'inferred') return 'inferred'
  if (mode === 'failure-chain' && edge.propagates_failure) return 'failure-propagation'
  return RELATION_SEMANTICS[edge.relation_type] ?? 'structural'
}
```

G6 配置使用：

```ts
layout: { type: 'dagre', rankdir, nodesep: 54, ranksep: 132 },
      node: { type: 'rect', style: { size: [196, 64], radius: 8, labelFontSize: 12, labelMaxWidth: 174, labelMaxLines: 2, labelWordWrap: true, stroke: nodeHealthStroke, fill: nodeHealthFill } },
edge: { type: 'polyline', style: { endArrow: true, endArrowSize: 10, lineWidth: 1.6, labelFontSize: 12, labelBackgroundOpacity: 1, labelPadding: [3, 6] } },
behaviors: ['drag-canvas', 'zoom-canvas', 'drag-element'],
```

结构色使用 `var(--graph-edge-structural)` 对应的中性灰，故障使用 `var(--danger)`，推断使用 `var(--warning)`；健康色不得用作资源类别色。

- [ ] **Step 4: 实现关系筛选、点击聚焦和上下文折叠**

`GraphExplorer` 持有 `selectedNodeId/selectedEdgeId/enabledSemantics/showContextNodes/hideOrphans`；默认启用全部事实语义和推断关系，但 Namespace/Node 使用折叠上下文模式。`GraphMap` 增加：

```ts
onNodeSelect?: (nodeId: string | null) => void
onEdgeSelect?: (edgeId: string | null) => void
focusedNodeId?: string | null
focusedEdgeId?: string | null
```

注册 G6 `node:click`、`edge:click` 和 `canvas:click`；键盘等价操作由画布旁的关系列表提供。选中后调用 `focusGraph` 写入 `dimmed` 数据态，无关元素 opacity 降低但仍可恢复；关系检查器与等价关系列表使用同一个 `selectedEdgeId`。筛选控件按七个中文语义显示，筛选结果为零时保留工具栏和明确空态，不渲染空画布框。

- [ ] **Step 5: 实现检查器 Drawer 和可访问按钮**

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

- [ ] **Step 6: 运行组件测试与构建**

Run: `cd observability-frontend && npm test -- --run src/components/graph && npm run build`

Expected: PASS；测试覆盖七类 relation semantic、structural/failure/inferred 三种线型、点击聚焦、上下文折叠、健康状态和 1024px inspector 入口。

- [ ] **Step 7: 提交**

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
- Modify: `observability-frontend/src/features/investigation/model.ts`
- Modify: `observability-frontend/src/features/investigation/model.test.ts`
- Modify: `observability-frontend/src/pages/investigation/InvestigationShell.tsx`
- Create: `observability-frontend/src/pages/investigation/InvestigationShell.test.tsx`
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
- 调查详情首屏固定为：结论状态、根因或“尚未确认”、因果链、影响资源与冻结窗口、3–5 条关键证据、反证/缺失证据、终止原因和下一步验证；完整证据时间线、工具活动和原始载荷放在可展开技术详情。
- 数值置信度只作为辅助信息，必须同时展示支持证据数、反证数、缺失证据数和数据完整性；`partial/stale` 不得显示“根因已确认”。

**Acceptance:** 1440、1280、1024 视口中主名称与状态可读；任何 UUID 不得逐字换行；所有原字段可从详情访问；界面不再显示 `awaiting approval`、`proposed`、`failed` 等裸英文状态；值班人员在不展开技术详情时即可判断结论是否成立、依据是否完整以及下一步是什么。

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

- [ ] **Step 4: 将调查详情改为摘要优先报告**

`toInvestigationViewModel` 额外投影 `causalChain`、`terminationReason`、`budget` 和证据计数，且继续执行既有确认门槛：存在根因、`confidence >= 0.8`、至少一条 eligible evidence、非 `partial/stale` 才能输出 `confirmed`。

```ts
export interface InvestigationSummary {
  conclusionState: 'confirmed' | 'insufficient_evidence'
  rootCause: string | null
  causalChain: Array<{ source: string; relation: string; target: string; factStatus: 'fact' | 'inferred' }>
  evidenceFactors: { supporting: number; contradicting: number; missing: number; partial: boolean; stale: boolean }
  terminationReason: 'root_confirmed' | 'evidence_exhausted' | 'budget_exhausted' | 'source_unavailable' | 'cancelled' | 'runtime_failed'
  nextVerification: string[]
}
```

`InvestigationShell` 不再使用三个等宽信息卡。1440px 为 `minmax(0, 1fr) 360px`：左侧依次显示结论、因果链和关键证据，右侧显示影响、证据完整性、终止原因和下一步；1024px 改为单列。完整证据时间线、工具活动、所有假设和原始载荷进入默认收起的“技术详情”，且键盘可展开。

- [ ] **Step 5: 收敛处置表并统一流程中文标签**

`toActionViewModel` 输出已本地化的 `approvalStatus/executionStatus/verificationStatus`；`lifecycle()` 只消费这些 view 字段。Table 删除来源 Run、影响范围、创建时间首屏列，这些字段进入现有动作详情 Drawer；风险和验证列设置 `responsive: ['xl']`，保证 1024/1280 内容区优先显示目标、审批、执行和操作。页面描述从“所有环境变更”改为“当前集群的全部处置动作”。

- [ ] **Step 6: 运行测试与构建**

Run: `cd observability-frontend && npm test -- --run src/features/workflow src/features/investigation/model.test.ts src/pages/investigation/InvestigationCenter.test.tsx src/pages/investigation/InvestigationShell.test.tsx src/pages/Actions && npm run build`

Expected: PASS；所有裸状态有稳定中文映射，详情仍可访问完整字段；partial/stale/缺少 eligible evidence 的 fixture 只能显示“证据不足”。

- [ ] **Step 7: 提交**

```bash
git add observability-frontend/src/features/workflow observability-frontend/src/features/investigation/model.ts observability-frontend/src/features/investigation/model.test.ts observability-frontend/src/pages/investigation/InvestigationCenter.tsx observability-frontend/src/pages/investigation/InvestigationCenter.test.tsx observability-frontend/src/pages/investigation/InvestigationShell.tsx observability-frontend/src/pages/investigation/InvestigationShell.test.tsx observability-frontend/src/pages/Actions observability-frontend/src/index.css
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

### Task 10: 建立告警到调查的受控、幂等关联

**Files:**
- Create: `ai-apm-query-go/internal/store/migrations/versions/0022_alert_investigation.sql`
- Create: `ai-apm-query-go/internal/store/alert_investigation.go`
- Create: `ai-apm-query-go/internal/store/alert_investigation_test.go`
- Create: `ai-apm-query-go/internal/api/alert_investigation.go`
- Create: `ai-apm-query-go/internal/api/alert_investigation_test.go`
- Modify: `ai-apm-query-go/internal/api/alert_engine.go`
- Modify: `ai-apm-query-go/internal/api/alert_engine_test.go`
- Modify: `ai-apm-query-go/internal/api/handler.go`
- Modify: `ai-apm-query-go/internal/bootstrap/http.go`
- Modify: `ai-apm-query-go/internal/api/auth.go`
- Modify: `observability-frontend/src/api/client.ts`
- Modify: `observability-frontend/src/pages/Observe/index.tsx`
- Modify: `observability-frontend/src/pages/Observe/index.test.tsx`
- Modify: `observability-frontend/src/pages/investigation/InvestigationCenter.tsx`
- Modify: `observability-frontend/src/pages/admin/AdminSettings.tsx`

**Interfaces:**
- Consumes: 既有 `AlertEvent{ID,TenantID,Cluster,Severity,Signature,FirstTimestamp,Object}`、`AIRunDAO`、`AIRunOutboxDAO` 和唯一 Run 状态机。
- Produces: `AlertInvestigationPolicy{TenantID,ClusterID,Mode,MinimumSeverity,MaxConcurrent,MaxPerHour}`，其中 `Mode = manual | draft | auto_readonly`；`AlertRunLink{AlertEventID,RunID,Decision,ReasonCode}`；`AlertInvestigationService.OnAlert(ctx, event)`。
- Public API: `GET/PUT /api/v1/system/alert-investigation-policy?cluster_id=...`；`POST /api/v1/alerts/{event_id}/investigation`；告警列表投影 `investigation.mode/status/reason_code/run_id`。

**Acceptance:** 缺少策略行时严格等同 `manual`；`draft` 只生成可接受草稿，不消耗 AI 配额；`auto_readonly` 只为满足严重度、范围、并发和频率门槛的告警创建 `principal_type=system,action_mode=read_only` Run；同一 alert event 在并发、重启和补偿扫描中最多关联一个 Run；任何模式均不得创建 Action。

- [ ] **Step 1: 写迁移和 DAO 失败测试**

迁移固定两个表，告警事件仍以 ClickHouse `alert_events` 为权威，不新增 Incident 表：

```sql
CREATE TABLE IF NOT EXISTS ai_alert_investigation_policies (
  tenant_id CHAR(36) NOT NULL,
  cluster_id CHAR(36) NOT NULL,
  mode VARCHAR(24) NOT NULL DEFAULT 'manual',
  minimum_severity VARCHAR(16) NOT NULL DEFAULT 'critical',
  max_concurrent INT NOT NULL DEFAULT 2,
  max_per_hour INT NOT NULL DEFAULT 10,
  updated_by VARCHAR(255) NOT NULL,
  created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  PRIMARY KEY (tenant_id, cluster_id),
  CONSTRAINT chk_alert_investigation_mode CHECK (mode IN ('manual','draft','auto_readonly'))
);

CREATE TABLE IF NOT EXISTS ai_alert_run_links (
  tenant_id CHAR(36) NOT NULL,
  cluster_id CHAR(36) NOT NULL,
  alert_event_id VARCHAR(128) NOT NULL,
  alert_signature VARCHAR(255) NOT NULL,
  run_id CHAR(36) NULL,
  decision VARCHAR(24) NOT NULL,
  reason_code VARCHAR(64) NULL,
  source_resolved TINYINT NOT NULL DEFAULT 0,
  created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  PRIMARY KEY (tenant_id, cluster_id, alert_event_id),
  UNIQUE KEY uk_alert_run (tenant_id, cluster_id, run_id)
);
```

DAO 测试必须覆盖：缺省 manual、非法 mode 拒绝、`max_concurrent` 取值 1–8、`max_per_hour` 取值 1–100、重复链接返回首次记录、同一事务创建 Run + outbox + link、事务任一步失败全部回滚。

- [ ] **Step 2: 运行迁移/DAO 测试并确认失败**

Run: `cd ai-apm-query-go && go test ./internal/store ./internal/store/migrations -run 'AlertInvestigation|MigrationCoverage|Manifest' -count=1`

Expected: FAIL；0022 和 DAO 尚不存在。

- [ ] **Step 3: 实现单一告警调查服务**

`OnAlert` 只接受 tenant/cluster/event ID 均非空的事件，按顺序执行：读取策略 → 判断严重度 → 查询既有 link → 计算 active auto Run 数与过去一小时创建数 → 在同一事务中写 link/Run/outbox。稳定 reason code 固定为：

```go
const (
    AlertInvestigationPolicyManual = "policy_manual"
    AlertInvestigationBelowSeverity = "below_severity"
    AlertInvestigationConcurrencyLimited = "concurrency_limited"
    AlertInvestigationRateLimited = "rate_limited"
    AlertInvestigationInvalidScope = "invalid_scope"
    AlertInvestigationPersistenceUnavailable = "persistence_unavailable"
)
```

`draft` 写 `decision=draft,run_id=NULL`；`auto_readonly` 创建冻结窗口 `[FirstTimestamp-15m, LastTimestamp+5m]`，总跨度仍受 24h 上限，`request_id` 使用 `idempotencyRequestID(tenantID, "alert:"+clusterID+":"+event.ID)`，`target_type` 根据 alert object 映射为 `pod|deployment|node|vm|resource|alert`，不能识别时用 `alert`。

- [ ] **Step 4: 接入告警生成与有界补偿**

新事件完成 ID、Scope 和签名后，在释放 `alertEventsMu` 之后调用 `OnAlert`；不能在告警锁内访问 MySQL 或 outbox。query-api 启动后及每 5 分钟执行一次最近 6 小时、最多 100 条 firing critical 告警的补偿扫描，扫描只调用同一幂等服务。扫描失败记录稳定指标/日志，不影响告警权威写入。

告警恢复时只更新 link 的 `source_resolved=1`；已开始的只读调查继续使用冻结窗口，不因当前恢复而改写历史证据或静默取消。

- [ ] **Step 5: 实现公共策略、接受草稿和告警投影 API**

`PUT policy` 需要系统管理权限并验证 cluster 属于当前 tenant；`POST /alerts/{id}/investigation` 需要 `ai.investigate`，接受 draft 或为 manual 告警显式创建 Run，重试返回首次 `run_id`。浏览器不能提交 `principal_type=system` 或绕过 cluster Scope。

错误映射固定为：未知告警 404、跨 tenant/cluster 403、策略/输入非法 422、持久化不可用 503、同一事件不同不可变参数 409。

- [ ] **Step 6: 补充 UI，而不改变平台总览主层级**

观测问题列表为每条告警显示：`未创建调查 | 调查草稿 | 调查中 | 已完成 | 已跳过`。草稿提供“开始调查”，已有 Run 提供“打开调查”；reason code 使用中文说明且不隐藏。调查中心用“系统建议/系统自动/人工发起”来源标签区分 principal，不新增单独 Incident 页面。

系统管理的当前实际集群配置区提供三个模式和严重度/并发/每小时上限；页面必须明确“自动调查仅只读，不会执行处置”。配置属于集群，不使用 prod/dev 环境层级。

- [ ] **Step 7: 运行后端、前端和并发幂等测试**

Run: `cd ai-apm-query-go && go test ./internal/store ./internal/api -run 'AlertInvestigation|AlertEvaluation' -count=1`

Run: `cd ai-apm-query-go && go test -race ./internal/api -run 'AlertInvestigationConcurrent' -count=1`

Run: `cd observability-frontend && npm test -- --run src/pages/Observe/index.test.tsx src/pages/investigation/InvestigationCenter.test.tsx src/pages/admin/AdminSettings.test.tsx && npm run build`

Expected: PASS；50 个并发同事件触发只产生一个 link、一个 Run、一个 outbox；manual/draft 不产生自动 Run；所有自动 Run 都是 read_only 且 Action 数为 0。

- [ ] **Step 8: 提交**

```bash
git add ai-apm-query-go/internal/store/migrations/versions/0022_alert_investigation.sql ai-apm-query-go/internal/store/alert_investigation.go ai-apm-query-go/internal/store/alert_investigation_test.go ai-apm-query-go/internal/api/alert_investigation.go ai-apm-query-go/internal/api/alert_investigation_test.go ai-apm-query-go/internal/api/alert_engine.go ai-apm-query-go/internal/api/alert_engine_test.go ai-apm-query-go/internal/api/handler.go ai-apm-query-go/internal/bootstrap/http.go ai-apm-query-go/internal/api/auth.go observability-frontend/src/api/client.ts observability-frontend/src/pages/Observe observability-frontend/src/pages/investigation/InvestigationCenter.tsx observability-frontend/src/pages/admin/AdminSettings.tsx
git commit -m "feat(investigation): link alerts to governed read-only runs"
```

### Task 11: 持久化调查因果摘要、预算和明确终止原因

**Files:**
- Modify: `ai-orchestrator/apps/investigation.py`
- Modify: `ai-orchestrator/investigation_runtime.py`
- Modify: `ai-orchestrator/tests/test_investigation_runtime.py`
- Modify: `ai-orchestrator/tests/test_rca_engine_v2_contract.py`
- Modify: `ai-apm-query-go/internal/store/ai_runs.go`
- Modify: `ai-apm-query-go/internal/store/ai_runs_test.go`
- Modify: `ai-apm-query-go/internal/api/runs_public.go`
- Modify: `ai-apm-query-go/internal/api/runs_public_projection_test.go`
- Modify: `observability-frontend/src/features/investigation/model.ts`
- Modify: `observability-frontend/src/features/investigation/model.test.ts`
- Modify: `observability-frontend/src/pages/investigation/InvestigationShell.tsx`
- Modify: `observability-frontend/src/pages/investigation/InvestigationShell.test.tsx`

**Interfaces:**
- Produces: `termination_reason = root_confirmed | evidence_exhausted | budget_exhausted | source_unavailable | cancelled | runtime_failed`；`budget_summary{max_steps,max_tools,consumed_steps,consumed_tools}`；`causal_chain[]` 每项包含 `source_uid,relation_type,relation_label,target_uid,fact_status,evidence_ids`。
- Persistence: 复用 0004 已有 `ai_runs.runtime_metadata_json` 和 `last_failure_code`，不新增第二套状态列；公共 Run 投影输出 `investigation_summary`。

**Acceptance:** 每个终态 Run 都有稳定终止原因；`partial` 必须能解释为何 partial；因果链只引用当前 Run 的图边和 evidence ID；预算耗尽时保留已取得证据并返回 partial，不能伪装 failed 或 confirmed；UI 不依赖英文内部状态猜原因。

- [ ] **Step 1: 写运行时合同失败测试**

```python
def test_budget_exhaustion_commits_truthful_partial_summary():
    result = normalize_outcome({
        "status": "partial",
        "error_code": "BUDGET_EXCEEDED",
        "budget": {"max_steps": 10, "max_tools": 16, "consumed_steps": 10, "consumed_tools": 16},
        "evidence": [{"evidence_id": "ev-1"}],
    })
    assert result["investigation_summary"]["termination_reason"] == "budget_exhausted"
    assert result["investigation_summary"]["budget_summary"]["consumed_tools"] == 16
    assert result["investigation_summary"]["evidence_ids"] == ["ev-1"]
```

再覆盖：confirmed root → `root_confirmed`；无可继续证据 → `evidence_exhausted`；必要数据源不可用 → `source_unavailable`；取消和异常分别映射；任一 causal edge 引用不存在 evidence 时被剔除并产生 warning code。

- [ ] **Step 2: 运行 orchestrator 测试并确认失败**

Run: `cd ai-orchestrator && .venv314/bin/python -m pytest tests/test_investigation_runtime.py tests/test_rca_engine_v2_contract.py -q`

Expected: FAIL；当前仅有 terminal status/error code，没有完整稳定的 termination/budget/causal summary。

- [ ] **Step 3: 在唯一 terminal commit 边界生成摘要**

`apps/investigation.py` 负责把 RCA 输出规范为一个 `investigation_summary`；`InvestigationRuntime` 只在 lease 仍有效、graph context 最终化完成后随 terminal commit 一次性写入。不得从前端文本或 Markdown 反向解析：

```python
summary = {
    "schema_version": 1,
    "termination_reason": termination_reason,
    "budget_summary": budget_summary,
    "root_cause_uid": root_cause_uid or None,
    "causal_chain": validated_causal_chain[:12],
    "evidence_ids": eligible_evidence_ids,
    "missing_evidence": missing_evidence,
    "warning_codes": warning_codes,
}
```

因果链最多 12 跳；同一方向/关系/目标去重；推断边必须保留 `fact_status=inferred`；无 evidence 的推断不能升级根因确认。已有自动读取预算保持最多 16 次证据调用，并把实际消耗写入 summary。

- [ ] **Step 4: 让 Query API 持久化并投影 runtime metadata**

`AIRunDAO` 的 terminal commit、Get/List scanner 和公共详情投影必须包含 `runtime_metadata_json`；解析失败时返回 `investigation_summary=null` 与 `warning_codes=["INVESTIGATION_SUMMARY_INVALID"]`，不得 500 或编造空成功摘要。`last_failure_code` 仍保留机器故障码，不能代替 termination reason。

- [ ] **Step 5: 在调查详情呈现可判定摘要**

前端按 Task 6 的布局读取服务端 `investigation_summary`。顶部文案固定：

- `root_confirmed` 且满足证据门槛：`根因已确认`；
- 其他任何原因：`根因尚未确认`；
- `budget_exhausted`：`已达到本次调查预算，以下为已取得证据`；
- `source_unavailable`：列出不可用来源；
- `evidence_exhausted`：列出仍缺少的证据；
- `runtime_failed/cancelled`：明确失败或取消，不显示健康空态。

数值置信度旁同时显示支持/反驳/缺失数量；因果链每行使用 `源资源 ─关系→ 目标资源`，事实实线、推断虚线，点击打开对应关系和 evidence。

- [ ] **Step 6: 运行跨语言合同、全量测试和构建**

Run: `cd ai-orchestrator && .venv314/bin/python -m pytest tests/test_investigation_runtime.py tests/test_rca_engine_v2_contract.py -q`

Run: `cd ai-apm-query-go && go test ./internal/store ./internal/api -run 'Run.*Metadata|InvestigationSummary|RunPublic' -count=1`

Run: `cd observability-frontend && npm test -- --run src/features/investigation/model.test.ts src/pages/investigation/InvestigationShell.test.tsx && npm run build`

Expected: PASS；六种终止原因均有后端持久化和 UI 文案测试；partial/stale fixture 不能呈现 confirmed。

- [ ] **Step 7: 提交**

```bash
git add ai-orchestrator/apps/investigation.py ai-orchestrator/investigation_runtime.py ai-orchestrator/tests/test_investigation_runtime.py ai-orchestrator/tests/test_rca_engine_v2_contract.py ai-apm-query-go/internal/store/ai_runs.go ai-apm-query-go/internal/store/ai_runs_test.go ai-apm-query-go/internal/api/runs_public.go ai-apm-query-go/internal/api/runs_public_projection_test.go observability-frontend/src/features/investigation/model.ts observability-frontend/src/features/investigation/model.test.ts observability-frontend/src/pages/investigation/InvestigationShell.tsx observability-frontend/src/pages/investigation/InvestigationShell.test.tsx
git commit -m "feat(investigation): persist causal and termination summaries"
```

### Task 12: 完成持久化集群库存投影 ADR 决策门

**Files:**
- Create: `docs/architecture/ADR-0002-cluster-inventory-projection.md`
- Create: `docs/contracts/kubernetes-inventory-projection.md`
- Modify: `docs/architecture/index.md`
- Modify: `docs/ownership/data-owners.md`
- Modify: `docs/remediation/BACKLOG.md`

**Interfaces:**
- Evaluates: `full snapshot + watch delta + resourceVersion + snapshot_generation + last_seen + bounded chunks` 是否替代查询时大范围 kubectl snapshot。
- Must preserve: `k8sboundary` 仍是凭据和身份唯一边界；MySQL/对象存储中的库存投影只存经过清洗的资源事实，不存 kubeconfig/token；HugeGraph 仍是可重建投影，不成为资源权威。

**Acceptance:** ADR 对采用、暂缓、拒绝三种结果给出唯一结论；包含现状测量、数据所有权、全量/增量顺序、断线重连、乱序、删除、重放、分块、安全、容量、迁移和回退；未满足决策阈值时不得创建库存表、collector 或第二套 API。

- [ ] **Step 1: 记录当前基线而不修改运行代码**

在隔离测试集群分别测量 1k、10k、100k 对象规模下 `KubeGraphObjects` 的总响应字节、执行时间、query-api 峰值 RSS、API Server 请求数和图谱构建耗时；再记录 1、10、50 个集群的容量外推。所有无法实测的数据写 `NOT_MEASURED` 并使 ADR 结论保持“暂缓”，不得填估算值冒充测量。

- [ ] **Step 2: 写完整数据合同**

`kubernetes-inventory-projection.md` 固定：

```text
InventoryEnvelope {
  tenant_id, cluster_id, kubernetes_identity_uid,
  snapshot_generation, mode(full|delta),
  chunk_index, chunk_count, payload_bytes,
  started_resource_version, finished_resource_version,
  observed_at, resources[], deletions[]
}
```

每个 chunk 目标不超过 4 MiB，硬上限 8 MiB；同 generation 的 chunk 必须 index 连续、count 一致、cluster identity 一致；delta 必须引用最后成功 full generation；旧 generation、重复修改和乱序 delta 可幂等拒绝；删除以 UID 为主键；任何资源都携带 canonical cluster_id、UID、resourceVersion、observed_at/last_seen。

- [ ] **Step 3: 写 ADR 和唯一决策阈值**

推荐“采用”必须同时满足：

- 当前查询时 snapshot 在 10k 对象基线明显影响页面/图谱时延，或造成 API Server 压力；
- 原型证明断线后能从 resourceVersion 失效恢复为新 full snapshot；
- 乱序、重复、漏 chunk 和跨集群 identity mismatch 全部 fail closed；
- 投影端到端新鲜度 p95 不超过 120 秒，且无静默丢对象；
- 引入后 query-api 关键读接口 p95 不劣化超过 10%；
- 容量、保留、压缩、重建和回滚成本已量化并获架构负责人批准。

任一条件未满足则选择“暂缓”；只有现有按需查询在目标规模内满足 SLO 且投影收益不足成本时选择“拒绝”。

- [ ] **Step 4: 更新所有权和 backlog**

明确 collector 仅负责读取/上传，query-api control plane 负责接收顺序与身份校验，inventory store 是资源事实投影，graph worker 只消费投影。若 ADR 采用，在 backlog 新建后续独立实施计划入口；本 Task 不提前创建表、服务、依赖或 Helm 资源。

- [ ] **Step 5: 校验文档并提交**

Run: `rg -n 'NOT_MEASURED|Decision:|4 MiB|8 MiB|120 秒|resourceVersion|snapshot_generation|kubernetes_identity_uid' docs/architecture/ADR-0002-cluster-inventory-projection.md docs/contracts/kubernetes-inventory-projection.md`

Expected: 每个必需字段有命中；存在 `NOT_MEASURED` 时 Decision 必须为“暂缓”。

```bash
git add docs/architecture/ADR-0002-cluster-inventory-projection.md docs/contracts/kubernetes-inventory-projection.md docs/architecture/index.md docs/ownership/data-owners.md docs/remediation/BACKLOG.md
git commit -m "docs(architecture): decide cluster inventory projection"
```

### Task 13: 全量验证、生产数据核对、镜像发布与正式复验

**Files:**
- Modify only if a failing gate proves a defect in files owned by Task 0–11.
- Generate: `test-results/<RUN_ID>/v3-acceptance.json`
- Generate: `deploy/evidence/release-evidence.json`
- Do not commit generated credentials, browser storage state, temporary screenshots outside `test-results`, or Kubernetes Secret values.

**Interfaces:**
- Consumes: Task 0–11 的全部提交、现有生产 Secret、两个可用于隔离验证的真实授权集群、版本化镜像和 Helm chart。Task 12 的 ADR 结论不阻塞本轮发布。
- Produces: 同一 commit/image/Helm revision 绑定的测试、运行、截图和发布证据。

**Acceptance:** 前后端全量测试通过；主路由无 4xx/5xx；平台/集群健康一致；两个集群的同名 KubeVirt/Pod 资源严格隔离；资源目录与观测可用；图谱使用真实关系语法并可点击聚焦；告警调查策略幂等且永远只读；调查/处置/助手在规定视口可读；正式租户不存在未经确认的测试告警或测试集群；回滚 revision 在发布前记录并可用。

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
- 为多集群隔离验收准备两个已授权真实集群；若无法在两集群创建同名测试资源，必须标记 `BLOCKED_BY_ENV`，不能以单集群结果宣称隔离通过。
- 告警调查策略发布前保持 `manual`，先完成 manual/draft/auto_readonly 自动化测试；若正式启用 `auto_readonly`，必须由管理员按实际集群显式配置并记录审计。

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

按[完整验收说明](../verification/2026-09-11-aiops-v3-complete-acceptance.md)执行 API、双集群隔离、状态语义和真实浏览器矩阵。人工对照 `UI_RENDER_GUIDE.md` 检查生成的 1440/1280/1024 截图，签字项必须包含：导航标签、顶栏 Scope、总览事实、资源目录、KubeVirt 同名资源隔离、观测、告警调查状态、图谱关系/聚焦、调查摘要/终止原因、处置详情、助手 Drawer、知识/报告空态。任何跨集群数据、关键关系不可读、状态矛盾、原因缺失或逐字换行都判 FAIL。

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

只有 Task 13 验收过程中确实修改了文档或门禁配置时才创建提交：

```bash
git add docs/superpowers deploy/scripts tests/manual-e2e
git commit -m "docs(release): record aiops v3 remediation acceptance"
git status --short --branch
```

Expected: `main` 工作树干净；所有实现提交、镜像 revision、Helm revision 和验收 JSON 指向同一 commit。

---

## Final Definition of Done

- 原 2026-09-10 的 10 项、60 步整改要求全部保留；本补充不得用新功能抵消原运行阻断、健康语义、导航、空态、助手和发布门禁缺陷。
- VM/VMI 等所有用户可见 Kubernetes 资源只从已验证 canonical cluster boundary 读取；公共 handler 不再使用默认 kubeconfig/current context。
- 资源目录和观测中心在真实授权集群上返回业务结果或可信空态，无 502/403、panic、pageerror 和 console error。
- 平台总览、平台集群列表、集群详情对同一集群输出相同健康与原因；注册 active/ready 不再等同于健康。
- 平台能力摘要来自真实组件探测；不可用时显示未知/异常，不使用占位分子分母。
- 默认图谱搜索不出现 ReplicaSet、Container、EndpointSlice、业务、应用、APM Service 或中间件；兼容数据仍可通过详情证据访问。
- 图谱所有可见边包含中文关系和方向；七类关系语义、结构/故障传播/推断线型可区分；节点/边点击聚焦和检查器同步；关键关系链只由真实 edge 构成。
- 调查与处置首屏没有逐字换行或不可读 UUID；详情 Drawer 可查看和复制所有完整字段；状态全部中文化；调查详情优先展示可验证摘要、因果链、反证/缺失证据和明确终止原因。
- 告警到调查的 manual/draft/auto_readonly 策略按实际集群生效；并发/重启/补偿不重复建 Run，自动调查只读且不生成 Action。
- 助手在 1024×900 为单主列，两侧面板进入 Drawer；真实回答满足六段式合同且没有直接执行入口。
- 一级导航文字在三种桌面视口可见；顶栏集群名称不重复；知识、报告、管理空态自然高度且提供权限允许的下一步。
- 真实浏览器验收结果绑定 commit、image tag 与 Helm revision，并成为 `publishable` 的必要条件。
- 正式租户中的测试集群、回归告警和陈旧数据均有明确归属或已通过审计方式清理；不通过伪造新鲜时间或健康状态换取绿灯。
- 库存投影 ADR 已按实测数据给出采用/暂缓/拒绝的唯一结论；该结论不伪装为本轮已经实施的运行能力。

## Self-Review Record

- Spec coverage：覆盖原 10 项、60 步整改，并补齐 KubeVirt 集群边界、七类图谱语义/点击聚焦、告警调查策略、调查因果/终止摘要和库存投影决策门。
- Scope：只调整既有 V3 主链，不新增业务应用管理、中间件管理、跨集群动作、LLM 执行授权、Incident 状态机或本轮运行基础设施依赖。
- Type consistency：`clusterOperationalState`、`systemComponentResultView`、`DEFAULT_GRAPH_SEARCH_TYPES`、`RelationSemantic`、`RelationChain`、`AlertInvestigationPolicy`、`InvestigationSummary` 和状态投影函数均在首次消费前定义。
- Rollback：Task 10 的 0022 为 additive migration，旧代码忽略新表；代码与 UI 可通过 Helm 回滚到发布前 revision，策略缺省 manual，失败证据保留。
- Ambiguity：明确规定 active/ready 只代表接入状态；stale/uncovered 不得为 healthy；真实空态不能替代未执行的能力验收。
