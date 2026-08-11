# 多集群纳管 + 平台 20 项设计修复 实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 将 AIOps 平台升级为多集群纳管架构（数据汇聚 + 全局集群切换），并一次性修复 19 项前端设计问题，保持 v3.0 亮色极简风格。

**Architecture:** 数据汇聚（中心化）多集群——所有可观测表加 `cluster_id`，纳管集群采集组件回传平台中心存储；前端新增全局集群下拉（uiStore + ClusterSwitcher），各数据页按 `cluster_id` 过滤。2.x 设计修复遵循 UI 设计规范（token/PageKit 复用）。

**Tech Stack:** Go（query-api/ingest）+ ClickHouse + React/antd/ECharts + Helm + OTel/vmagent

## Global Constraints

- UI：亮色极简，靛蓝 `#2f54eb` 单主色；一律用 `theme/tokens.ts` 的 `var(--*)`；优先复用 PageKit；不引入新风格
- 主集群数据也打 `cluster_id='default'`，无空值特判；`cluster_id=all` 或省略 = 全部集群
- 所有 docker/pip 操作用国内源；kubectl 从本地 `docker/kubectl` 复制
- 后端 Go 代码需 `go build` 通过；前端需 `npm run build` 通过

---

### Task 1: 后端 cluster_id 数据模型（ClickHouse schema + ingest 打标）

**Files:**
- Modify: `deploy/helm/aiops/files/clickhouse/init_clickhouse.sql`（4 表加 `cluster_id String DEFAULT 'default'` 并入 ORDER BY）
- Modify: `ai-apm-ingest-go/internal/clickhouse/*.go`（writer 打 cluster_id）
- Modify: `ai-apm-ingest-go/internal/model/*.go`（span/log/metrics 模型加 cluster_id）

**Interfaces:**
- Consumes: 现有 writer 结构
- Produces: `observability.*` 表含 `cluster_id` 列

- [ ] **Step 1**: `init_clickhouse.sql` 在 trace_spans/log_records/service_topology/alert_events 各表加 `cluster_id String DEFAULT 'default'`（置于 tenant_id 后），ORDER BY 加入 cluster_id
- [ ] **Step 2**: ingest 各 writer 的 INSERT 列与模型加 cluster_id（从上报 env/字段取，默认 'default'）
- [ ] **Step 3**: `go build ./...` 通过

### Task 2: 后端 extractClusterID 过滤（所有 handler 支持 cluster_id）

**Files:**
- Modify: `ai-apm-query-go/internal/api/handler.go`（新增 `extractClusterID(r)`）
- Modify: `ai-apm-query-go/internal/api/*.go`（所有 CH 查询 handler 拼 `AND cluster_id=`）

**Interfaces:**
- Produces: `extractClusterID(r *http.Request) string` 返回 `"default"|"all"|具体集群`

- [ ] **Step 1**: handler.go 新增 `extractClusterID`（读 `?cluster_id=`，省略→`all`；`default`→`'default'`；其他→该值）
- [ ] **Step 2**: 在 ListServices/ListTraces/TraceDetail/QueryMetrics/DashboardStats/GlobalTopology/TopologyNodeDetail/QueryLogs/LogAggregate 等查询中加 `clusterClause`（`cluster_id=all` 时不加；否则 `AND cluster_id=`）
- [ ] **Step 3**: `go build ./...` + `go test ./internal/api/...` 通过

### Task 3: 前端全局集群 state + ClusterSwitcher

**Files:**
- Modify: `observability-frontend/src/store/uiStore.ts`（currentClusterId/clusters/setCurrentCluster/setClusters/refreshClusters）
- Create: `observability-frontend/src/components/ClusterSwitcher.tsx`
- Modify: `observability-frontend/src/App.tsx`（顶栏加 ClusterSwitcher；初始化拉集群）
- Modify: `observability-frontend/src/api/client.ts`（请求拦截器自动附 cluster_id）

**Interfaces:**
- Consumes: `listClusters()` 已存在
- Produces: uiStore `currentClusterId`（`'all'`|具体 id）；client 请求自动带 `cluster_id`

- [ ] **Step 1**: uiStore 新增集群 state（persist `current_cluster_id` 到 localStorage）
- [ ] **Step 2**: client.ts 请求拦截器读 uiStore currentClusterId，非 all 时注入 `cluster_id` 参数
- [ ] **Step 3**: 新建 ClusterSwitcher 组件（"全部集群"+ 各集群），放顶栏
- [ ] **Step 4**: App.tsx 初始化 `refreshClusters()`；`npm run build` 通过

### Task 4: 系统设置纳管集群 Tab + AI 集群上下文

**Files:**
- Modify: `observability-frontend/src/pages/admin/AdminSettings.tsx`（Tabs：AI 模型 / 纳管集群 / 审计日志）
- Modify: `observability-frontend/src/pages/ai/AiChat.tsx`、`components/AiDock.tsx`（附 cluster_id）
- Modify: `ai-orchestrator/`（NL2SQL/RCA 感知 cluster_id，默认 all）

- [ ] **Step 1**: AdminSettings 加"纳管集群"Tab（列表 + createCluster 表单 + kubeconfig + 节点/命名空间/事件查看）
- [ ] **Step 2**: AdminSettings 加"审计日志"Tab（复用 listAuditLogs）
- [ ] **Step 3**: AiChat/AiDock 请求带当前 cluster_id；orchestrator 查询默认 all
- [ ] **Step 4**: `npm run build` 通过

### Task 5: 2.x 前端设计修复（拓扑/Trace/日志/告警/报告/容量/基础设施/AI 工具）

**Files:**
- Modify: `ServiceObservability.tsx`（2.1 出框 2.2 rpm 2.3 调用次数 2.4 健康度 2.5 集群名）
- Modify: `Trace.tsx`（2.6 树形瀑布图 2.7 搜索框）
- Modify: `LogMetrics.tsx`（2.8 数据源/级别/时间/集群过滤）
- Modify: `AlertEvents.tsx`（2.9 RCA 2.10 当前/历史）、`AlertRules.tsx`（2.11 规则查看）
- Modify: `AiTasks.tsx`（2.12）、`AiWorkflow.tsx`（2.13 标题）、`AiTools.tsx`（2.14）
- Modify: `Capacity.tsx`（2.15 集群分组+实际叠加）
- Modify: `InfraK8s.tsx`+`InfraHardware.tsx`（2.16 合并单页）
- Modify: `Report.tsx`（2.18 列/预览/集群）
- Modify: `App.tsx`（导航更新：删 /infra/hardware、/report/governance、/report/artifacts；新增 基础设施 合并入口）
- Delete: `pages/report/Governance.tsx`、`pages/report/ReportArtifacts.tsx`、`pages/infra/InfraHardware.tsx`

- [ ] **Step 1**: 拓扑页（出框/单位/调用次数/健康度/集群名）
- [ ] **Step 2**: Trace 页（树形瀑布图 + 搜索框）
- [ ] **Step 3**: 日志页（数据源/级别/时间/集群）
- [ ] **Step 4**: 告警（RCA/当前历史/规则查看）
- [ ] **Step 5**: AI 工具/任务/工作流修复
- [ ] **Step 6**: 容量页
- [ ] **Step 7**: 基础设施合并 + 导航路由更新 + 删除废弃页面
- [ ] **Step 8**: 报告中心（列/预览/集群）
- [ ] **Step 9**: `npm run build` 通过；`tsc --noEmit` 无错

### Task 6: 部署构建 + 逐页验证 + 复审

**Files:**
- Modify: 镜像版本（前端/query-api/orchestrator）
- 部署到 minikube（NodePort 30253，admin/admin123）

- [ ] **Step 1**: 前端 npm build → 离线构建镜像（本地 nginx 镜像 + dist）
- [ ] **Step 2**: 后端 go build → 构建镜像（国内源）
- [ ] **Step 3**: helm 部署到 minikube，确认各服务启动
- [ ] **Step 4**: 登录逐页验证（截图 + 数据核验），输出验证报告
- [ ] **Step 5**: 复审（requesting-code-review）
