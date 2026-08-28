# AIOps MySQL + HugeGraph 双存储生产化改造 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 严格按 `AIOps_MySQL_HugeGraph_双存储生产化改造方案.md` 完成 MySQL 权威控制面、HugeGraph/RocksDB 关系投影、Graph API、Canonical Builder、增量同步、RCA、前端和本机部署验证。

**Architecture:** MySQL 保留控制面与原始事实权威；HugeGraph 1.7.0/RocksDB 只保存可重建的 Entity/Edge 投影。query-api 是唯一图存储访问者，orchestrator 通过严格的 internal query contract 生成事实与 RCA，前端只访问 query-api Graph API。legacy_mysql 保留为回滚路径，shadow 与 hugegraph 按方案门禁切换。

**Tech Stack:** Go 1.25+、MySQL 8.4、Apache HugeGraph 1.7.0/RocksDB/Java 11、Python 3.14、FastAPI、React 18、TypeScript、AntV G6 5.1.1、Helm、OrbStack Kubernetes。

**Spec:** `AIOps_MySQL_HugeGraph_双存储生产化改造方案.md`

## Global Constraints

- `MySQL = Authority / Source of Truth`; `HugeGraph = Derived Graph Projection`。
- 固定 `graph-identity-v1`、`graph-ontology-v2`、`graph-schema-v2`、`graph-dto-v1`、`propagation-v1`、`rca-score-v1`、`graph-ui-v1`。
- 固定 `AIOPS_ASSET_NS=0b8607dd-6b92-5e95-b007-d32874ffefab`、`AIOPS_GRAPH_MUTATION_NAMESPACE=7af0bc4b-dba0-56b1-ac7c-0fe13db2ef5b`、`GLOBAL_CLUSTER_SCOPE_ID=00000000-0000-0000-0000-000000000000`。
- Entity 使用 HugeGraph `CUSTOMIZE_STRING`，Vertex ID 直接等于 `entity_uid`；所有边使用逻辑 EdgeLabel、`frequency=SINGLE`。
- 所有新 Graph API 进行 server-side tenant/cluster scope 校验；raw Gremlin/Cypher、浏览器直连 HugeGraph、orchestrator 直连 HugeGraph 均禁止。
- 所有 MySQL 权威写与 `graph_projection_outbox` 写在同一事务；outbox projector 必须具备 lease、retry、dead、recovery、generation 和 stale grace。
- RCA 固定顺序为 Entity Resolution → Graph Candidate → Evidence → Deterministic Score → Classification → Persist → LLM Explanation；确认前不写 `CAUSED_BY`。
- 任何 Graph down、schema mismatch、scope violation、empty data 与 backend unavailable 必须返回不同错误语义，不能用空结果伪装。
- `@antv/g6` 精确锁定 `5.1.1`；本机验证使用 Fresh Install 两阶段 Helm 流程，并只允许 `aiops-canary` mutation。

### Task 1: Task A — Contract、Identity、Ontology 与 MySQL 0011

**Files:**
- Create: `docs/testdata/graph_identity_v1.json`, `docs/testdata/graph_entity_v1.json`, `docs/testdata/graph_subgraph_v1.json`, `docs/testdata/graph_impact_v1.json`, `docs/testdata/rca_graph_context_v1.json`, `docs/testdata/graph_error_v1.json`, `docs/testdata/service_dependency_v1.json`
- Create: `ai-apm-query-go/internal/graph/identity.go`, `ontology.go`, `models.go`, `errors.go`, `propagation_policy.go`
- Create: `ai-apm-query-go/internal/store/migrations/versions/0011_graph_projection.sql`
- Create: `ai-apm-query-go/internal/store/graph_projection_outbox.go`, `graph_sync_state.go`, `graph_worker_lease.go`, `graph_entity_alias.go`, `hardware_inventory.go`, `business_catalog.go`, `graph_schema_state.go`, `graph_reconcile_run.go`, `graph_shadow_diff.go`, `ai_run_graph_context.go`
- Test: `ai-apm-query-go/internal/graph/*_test.go`, `ai-apm-query-go/internal/store/graph_*_test.go`, `ai-apm-query-go/internal/store/ai_run_graph_context_test.go`
- Modify: `ai-apm-query-go/internal/store/migrations/coverage_test.go`

**Interfaces:**
- `graph.NameKeyV1(string) string`, `graph.SHA256Parts(...string) string`, `graph.EntityUID(...) string`, `graph.EdgeUID(...) string`。
- `graph.Entity`, `graph.Edge`, `graph.Subgraph`, `graph.MutationBatch`, `graph.MutationResult` 使用 `graph-dto-v1` JSON 字段。
- `graph.ValidateEntityType`、`graph.ValidateRelation`、`graph.CandidateDirection`、`graph.ImpactDirection`。
- DAO 提供 outbox claim/ack/retry/dead、sync state、lease、alias、catalog、hardware、schema state、reconcile run、shadow diff、run graph context 的明确方法。

- [ ] Step 1: 从 `docs/testdata/graph_identity_v1.json` 写 Go 失败测试，覆盖 `a + US + b`、Pod/VM/VMI、PhysicalServer、DIMM、provisional service 与 edge UID。
- [ ] Step 2: 运行 `cd ai-apm-query-go && go test ./internal/graph ./internal/store`，确认新类型/函数测试失败。
- [ ] Step 3: 实现跨语言 identity、完整 ontology 白名单、DTO 严格 JSON 编解码、传播策略与固定版本常量。
- [ ] Step 4: 写 `0011_graph_projection.sql`，建立 catalog、asset/component、alias、outbox、sync/lease/schema/reconcile/shadow/context 表及索引；迁移必须从 0010 幂等升到 0011。
- [ ] Step 5: 实现 DAO 的状态机约束：processing 超时可 reclaim，失败 10 次进入 dead，旧 `attrs_version` 不覆盖新版本，reconcile 失败不标记当前 generation stale。
- [ ] Step 6: 运行 `cd ai-apm-query-go && gofmt -w internal/graph internal/store && go test ./internal/graph ./internal/store`，确认 Task A 通过。

### Task 2: Task B — HugeGraph Client、Schema Migrator 与 Helm 基础设施

**Files:**
- Create: `ai-apm-query-go/internal/graph/hugegraph_client.go`, `hugegraph_schema.go`, `hugegraph_repository.go`
- Create: `ai-apm-query-go/cmd/graph-schema-migrator/main.go`, `ai-apm-query-go/internal/graph/schema_manifest_v2.json`
- Create: `deploy/helm/aiops/templates/hugegraph-statefulset.yaml`, `hugegraph-service.yaml`, `hugegraph-secret.yaml`, `hugegraph-pvc.yaml`, `graph-schema-migrator-job.yaml`, `graph-networkpolicy.yaml`
- Modify: `deploy/helm/aiops/Chart.yaml`, `values.yaml`, `values-prod.yaml`, `values-local-bootstrap.yaml`, `values-local-validation.yaml`, `templates/_helpers.tpl`, `templates/secrets.yaml`, `templates/networkpolicy.yaml`, `deploy/scripts/build-images.sh`, `deploy/scripts/local-validation.sh`
- Test: `ai-apm-query-go/internal/graph/hugegraph_client_test.go`, `hugegraph_schema_test.go`, `hugegraph_repository_test.go`, `deploy/scripts/test-deployment-contracts.sh`

**Interfaces:**
- `HugeGraphClient` 支持 GET/POST/PUT/DELETE vertex、edge、schema、gremlin-free traverser REST 调用，使用 context timeout 与明确 HTTP 错误。
- `SchemaManifestV2()` 返回规范化 manifest 与 SHA-256 checksum；migrator 对已存在定义进行完全比较，不一致非零退出。
- Helm 通过 `global.imageTag` 统一所有自研镜像；`GRAPH_BACKEND`、HugeGraph URL/认证、限制参数来自 Secret/values。

- [ ] Step 1: 写 client/schema manifest 失败测试，覆盖 timeout、非 2xx、幂等 schema、schema mismatch 和禁止 raw Gremlin 输入。
- [ ] Step 2: 运行 `cd ai-apm-query-go && go test ./internal/graph -run 'HugeGraph|Schema'`，确认测试失败。
- [ ] Step 3: 实现 HugeGraph 1.7 REST 映射：`/graphspaces/DEFAULT/graphs/aiops`、CUSTOMIZE_STRING Entity、固定 PropertyKey/IndexLabel/EdgeLabel。
- [ ] Step 4: 实现 `cmd/graph-schema-migrator`，让第二次执行通过、定义差异失败、checksum 进入 `graph_schema_state`。
- [ ] Step 5: 补齐 StatefulSet/Service/PVC/Secret/NetworkPolicy/Job，验证 query-api 是唯一 HugeGraph 网络访问者，orchestrator 与 frontend 无 HugeGraph 凭据。
- [ ] Step 6: 运行 `helm lint deploy/helm/aiops` 与 `bash deploy/scripts/test-deployment-contracts.sh`，再运行 `go test ./...`。

### Task 3: Task C — Graph Repository、Backend 模式、Scope 与 Graph API 基础

**Files:**
- Create: `ai-apm-query-go/internal/graph/repository.go`, `legacy_repository.go`, `shadow_repository.go`, `hugegraph_repository.go`, `scope.go`, `traverser.go`
- Create: `ai-apm-query-go/internal/api/graph_public.go`, `graph_internal.go`, `graph_ops.go`, `run_graph_context.go`
- Modify: `ai-apm-query-go/internal/api/control_plane_knowledge_graph.go`, `handler.go`, `cmd/api/main.go`
- Test: `ai-apm-query-go/internal/graph/repository_test.go`, `shadow_repository_test.go`, `traverser_test.go`, `ai-apm-query-go/internal/api/graph_public_test.go`, `graph_internal_test.go`, `graph_ops_test.go`, `run_graph_context_test.go`, `control_plane_knowledge_graph_test.go`

**Interfaces:**
- `GraphRepository`: `GetEntity`, `SearchEntities`, `Neighbors`, `ShortestPath`, `Impact`, `CandidateSubgraph`, `BatchMutate`, `Health`。
- `GraphScope{TenantID, ClusterIDs, IsAdmin}` 必须作用于 vertex、edge、path 和 impact 的两端。
- Public routes: `/api/v1/ai/kg/entities/search`, `/entities/{uid}`, `/neighbors`, `/impact`, `/path`, `/health`；internal routes: `/internal/v1/query/graph`；ops routes只读 sync/outbox/alias/stale/shadow 聚合。
- Run Graph Context 由 `ai_run_graph_contexts` 持久化，带 contract/schema/version、snapshot 时间、partial/stale/warnings。

- [ ] Step 1: 写失败测试覆盖 exact lookup、alias limit=20、1/2-hop caps、tenant/cluster 0 泄露、legacy unsupported、Graph down=503、schema mismatch=503。
- [ ] Step 2: 运行对应 Go 测试，确认旧 control-plane implementation 不满足新 DTO/UID/API。
- [ ] Step 3: 实现 backend factory：`legacy_mysql`、`shadow`、`hugegraph`；shadow 同时执行新旧查询并写 diff，不静默切换。
- [ ] Step 4: 实现 HugeGraph repository 的 batch mutation、neighbor/path/candidate/impact traverser，所有查询强制服务器端限深度、限点、限边。
- [ ] Step 5: 将 control-plane knowledge graph 改为 legacy adapter/内部兼容层；新增 native Graph API，删除生产路由对 `ProxyAI` 的 graph read 转发。
- [ ] Step 6: 注入 Handler，运行 `cd ai-apm-query-go && gofmt -w internal/graph internal/api && go test ./internal/graph ./internal/api ./internal/store`。

### Task 4: Task D — Canonical Facts 与 Builders

**Files:**
- Create: `ai-orchestrator/kg/identity.py`, `schema.py`, `models.py`, `ontology.py`, `builders/base.py`, `builders/kubernetes.py`, `builders/kubevirt.py`, `builders/hardware.py`, `builders/catalog.py`, `builders/trace.py`, `builders/middleware.py`, `builders/network.py`, `builders/change.py`
- Modify: `ai-orchestrator/internal_query.py`, `internal_query_client.py`, `kg_tools.py`, `tool_registry.py`, `node_health.py`, `main.py`
- Test: `ai-orchestrator/tests/test_graph_identity.py`, `test_graph_builders_kubernetes.py`, `test_graph_builders_kubevirt.py`, `test_graph_builders_hardware.py`, `test_graph_builders_trace.py`, `test_graph_builders_catalog.py`, `test_graph_builders_network.py`, `test_graph_query_tool.py`

**Interfaces:**
- 每个 builder 输出 `GraphMutationBatch`，实体/边只使用 canonical UID、ontology relation 和 source/generation/attrs_version。
- K8s builder 使用 metadata.uid、ownerReferences、EndpointSlice targetRef；KubeVirt builder使用真实 VM/VMI/Migration UID；hardware builder拒绝无稳定身份的确定资产；trace/middleware builder支持 provisional alias。
- `InternalQueryClient.query_graph_v1(...)` 是 orchestrator 唯一图查询工具，带 ToolExecutionContext/lease fence。

- [ ] Step 1: 读取同一 identity/DTO fixture 写 Python 失败测试，锁定 Go/Python 一致性。
- [ ] Step 2: 运行 `cd ai-orchestrator && .venv314/bin/python -m pytest tests/test_graph_identity.py tests/test_graph_builders_*.py -q`，确认新模块缺失时失败。
- [ ] Step 3: 实现 Python identity/schema/ontology 与 Go 完全一致的 hash、UID、relation 校验。
- [ ] Step 4: 实现 Kubernetes/KubeVirt/Hardware/Catalog/Trace/Middleware/Network/Change builders；禁止 Pod name→Service name 生产关系，名称启发式最多 confidence 0.5。
- [ ] Step 5: 注册 `query_graph.v1`，所有 investigation 图查询在 datasource I/O 前检查 expired lease，预算上限为每 Run 20 个自动 read ToolRun。
- [ ] Step 6: 运行 targeted pytest 与 `python -m compileall`，确认 builder fixture、scope、alias/confidence 测试通过。

### Task 5: Task E — Outbox Projector、Lease、Reconcile、Backfill 与 Shadow

**Files:**
- Create: `ai-apm-query-go/internal/graph/outbox_projector.go`, `reconciler.go`, `generation.go`, `shadow_compare.go`, `backfill.go`
- Create: `ai-orchestrator/kg/projector.py`, `reconcile.py`, `scheduler.py`, `backfill.py`
- Modify: `ai-apm-query-go/cmd/api/main.go`, `ai-orchestrator/main.py`, `deploy/scripts/phase4-gate.sh`, `deploy/scripts/validate-local-stack.sh`
- Test: `ai-apm-query-go/internal/graph/outbox_projector_test.go`, `reconcile_test.go`, `ai-orchestrator/tests/test_graph_projector.py`, `test_graph_reconcile.py`, `test_graph_backfill.py`

**Interfaces:**
- Projector claim/renew/ack/retry/dead 使用 `graph_worker_lease`；同一 deterministic mutation 重试幂等。
- Reconcile 为每 source 生成 generation，只有完整成功后才处理旧 generation 的 stale/delete grace。
- Backfill 顺序固定为 Catalog → Hardware → Kubernetes → KubeVirt → Middleware/Trace → Change → Network。

- [ ] Step 1: 写 crash/retry/dead/recovery、旧 generation、stale grace、attrs_version、reconcile failure 不误删测试。
- [ ] Step 2: 运行 Go/Python targeted tests，确认状态机未实现。
- [ ] Step 3: 实现 projector lease、claim、retry backoff、10 次 dead、恢复 processing、metrics。
- [ ] Step 4: 实现 reconcile generation/stale/delete、source watermark 与 alias conflict；Graph schema mismatch 时暂停投影和 reconcile。
- [ ] Step 5: 实现 backfill 与 shadow diff，固定 structural mismatch、identity mismatch、scope leak、lag/age/P95 门禁统计。
- [ ] Step 6: 运行 `go test ./internal/graph ./internal/store` 与 `python -m pytest tests/test_graph_* -q`，保留 JSON/文本验证报告。

### Task 6: Task F — AI RCA Engine 与 Run Graph Context

**Files:**
- Create: `ai-orchestrator/rca_engine/engine.py`, `entity_resolver.py`, `candidates.py`, `evidence.py`, `scorer.py`, `context.py`, `explanation.py`
- Modify: `ai-orchestrator/rca.py`, `kg_tools.py`, `contracts.py`, `investigation_runtime.py`
- Test: `ai-orchestrator/tests/test_rca_entity_resolver.py`, `test_rca_candidates.py`, `test_rca_scorer.py`, `test_rca_graph_context.py`, `test_rca_graph_down.py`, `test_rca_scenarios.py`

**Interfaces:**
- `diagnose_root_cause_v2(req: RCARequest, execution_context: ToolExecutionContext) -> RCAResult` 严格按方案 70.1 顺序执行。
- 评分固定 topology/temporal/anomaly/change/trace/hardware_severity/co_failure/redundancy_penalty 权重、confirmed 0.80、probable 0.65、tie delta 0.05。
- Graph down 返回 `local_only`，不基于不完整关系自动处置；top 5 hypothesis 写结构化摘要到 `ai_hypotheses`，evidence 写 `ai_evidence`，Graph Context 写 query-api/MySQL。

- [ ] Step 1: 写 DIMM→Server→Node→Pod→Service→App→Business、共宿主 VM、Middleware、Change、证据不足、Graph down、迁移历史场景失败测试。
- [ ] Step 2: 运行 targeted RCA pytest，确认旧 rca path 不满足顺序/评分/持久化要求。
- [ ] Step 3: 实现 Entity Resolver、candidate propagation、candidate prefilter、evidence join 与 contradiction penalty。
- [ ] Step 4: 实现 deterministic scorer/classifier、Graph Context versioned persistence、CAUSED_BY confirmed-only。
- [ ] Step 5: 将 LLM 限制为结构化 RCA explanation consumer，禁止 LLM 先猜 root；强制 evidence/tool budget。
- [ ] Step 6: 运行 `cd ai-orchestrator && .venv314/bin/python -m pytest tests/test_rca_* tests/test_p9_rca_engine.py -q`。

### Task 7: Task G — Public Graph API、Ops、兼容与安全收口

**Files:**
- Modify: `ai-apm-query-go/internal/api/graph_public.go`, `graph_internal.go`, `graph_ops.go`, `run_graph_context.go`, `control_plane_knowledge_graph.go`, `cmd/api/main.go`, `internal/contract/*`
- Modify: `ai-orchestrator/kg_api.py`, `kg_graph.py`, `internal_query_client.py`
- Test: `ai-apm-query-go/internal/api/*graph*_test.go`, `ai-orchestrator/tests/test_control_plane_kg.py`, `test_p0_graph_isolation.py`, `tests/workflow-e2e/test_failure_recovery.py`

- [ ] Step 1: 写严格 DTO/error/security contract tests，确认 raw Gremlin/Cypher、browser direct HugeGraph、cross-tenant/cross-cluster response 均失败。
- [ ] Step 2: 将旧 `kg_api.py` 降为兼容适配器，移除生产 Graph Read owner；`kg_graph.py` 不再在主链调用 snapshot/BFS。
- [ ] Step 3: 完善 public/internal/admin API 的 RBAC、审计、错误码、pagination/limits、run context 和 Graph Ops 聚合。
- [ ] Step 4: 运行 Go/Python/workflow security tests 与 `rg -n 'HUGEGRAPH|Gremlin|Cypher|snapshot|BFS'` 约束扫描，确认凭据/直连/生产热路径不存在。

### Task 8: Task H — Frontend Graph UI 与 Service Panorama

**Files:**
- Modify: `observability-frontend/package.json`, `package-lock.json`, `src/App.tsx`, `src/api/client.ts`, `src/api/knowledgeGraph.ts`, `src/api/graphContracts.ts`, `src/api/contracts.test-fixtures.ts`
- Create: `observability-frontend/src/components/graph/GraphSummary.tsx`, `GraphMap.tsx`, `DependencyChain.tsx`, `CallMatrix.tsx`, `GraphExplorer.tsx`, `ImpactTree.tsx`, `GraphOpsPanel.tsx` 及对应测试
- Modify/Create: `src/pages/observability/ResourceRelationships.tsx`, `ServiceObservability.tsx`, `src/pages/observability/service/*`, `src/pages/investigation/IntelligentInvestigation.tsx`, `src/pages/admin/GraphOperations.tsx`
- Test: `src/api/knowledgeGraph.test.ts`, `src/components/graph/*.test.tsx`, `src/pages/observability/service/*.test.tsx`, `src/pages/admin/GraphOperations.test.tsx`

- [x] Step 1: 写 fixture-driven contract tests，锁定 graph entity/subgraph/impact/error/service dependency 字段一一对应；本次补充服务全景、调用矩阵和地图布局契约测试。
- [ ] Step 2: 精确安装 `@antv/g6@5.1.1`，运行 `npm ci` 与 targeted tests，确认新 UI 测试先失败。
- [x] Step 3: 实现摘要→服务地图→依赖主链→调用矩阵→专家关系探索；默认使用有界 DAG 布局，节点/边分别限制为 300/1000，禁止自由力导向全量图。
- [ ] Step 4: 实现影响树、RCA graph-context、Graph Ops、历史 context 展示、health/partial/stale/warning 区分与 30 秒/按视图刷新。
- [x] Step 5: 运行 `cd observability-frontend && npm run test:run && npm run build`，确认浏览器无 HugeGraph 请求；本机结果为 24 个文件/37 个测试通过且构建成功。

### Task 9: Task I — 性能、切换门禁、本机部署与最终验证

**Files:**
- Create: `deploy/scripts/graph-load-test.sh`, `deploy/scripts/shadow-gate.sh`, `deploy/scripts/graph-recovery-test.sh`, `docs/runbooks/graph-cutover.md`, `docs/runbooks/graph-local-validation.md`
- Modify: `deploy/helm/aiops/values-prod.yaml`, `values-local-validation.yaml`, `deploy/scripts/local-validation.sh`, `deploy/scripts/test-deployment-contracts.sh`, `deploy/scripts/validate-local-stack.sh`
- Test/Artifacts: `/tmp/aiops-graph-load-report.json`, `/tmp/aiops-shadow-report.json`, `/tmp/aiops-local-validation.log`

- [ ] Step 1: 写 Helm/render/load/shadow/recovery 失败门禁测试，覆盖 200k vertex/1M edge 数据模型、所有固定 P95、资源采集字段和 24h/2h duration 配置。（代码侧已补齐真实 fixture generator、全部操作 P95 和固定门禁；真实负载采样仍待环境执行。）
- [ ] Step 2: 运行 `helm lint deploy/helm/aiops`、`helm template ... values-prod.yaml`、全部 Go/Python/Frontend 测试，修复所有失败项。
- [ ] Step 3: 构建统一 `RELEASE_TAG=git-<12 hex SHA>` 镜像；Fresh Install 先 bootstrap MySQL/观测存储和 users-init/schema-migrator，再 runtime upgrade。
- [ ] Step 4: 在 OrbStack 中创建 `aiops-canary` workload，验证 Query API/Worker/Proxy/Frontend/Action Executor、RBAC 仅 `aiops-canary` deployments get/patch，disabled 阶段无 mutation。
- [ ] Step 5: 部署 HugeGraph 1.7.0/Java 11/RocksDB，运行 graph schema migrator、backfill、固定 identity/ontology/path/impact/RCA/Graph Ops 验证；记录真实 LLM/DeepFlow 可用性。
- [ ] Step 6: 运行 shadow compare 与性能脚本；只有 identity/structural/scope/dead/lag/P95/动态依赖/固定场景全部通过才设置 `GRAPH_BACKEND=hugegraph`。（BLOCKED_BY_ENV：本轮未提供可写 HugeGraph/真实观测数据。）
- [ ] Step 7: 观察切换 2 小时；若当前会话无法持续完成 24h Shadow，则生成可重放的 soak 命令与未满足项，不宣称该门禁通过。（BLOCKED_BY_ENV：未执行 2h/24h 长时观察。）
- [ ] Step 8: 运行最终命令集：`cd ai-apm-query-go && go test ./... && go test -race ./internal/graph/... ./internal/api/... ./internal/store/...`; `cd ai-orchestrator && .venv314/bin/python -m pytest -q`; `cd observability-frontend && npm ci && npm run test:run && npm run build`; `helm lint deploy/helm/aiops`。
- [ ] Step 9: 写最终验证报告，逐项勾选方案 DoD；当前报告已明确列出代码闭环证据与 `BLOCKED_BY_ENV`，因长时/真实数据门禁未完成，不能勾选全部 DoD。

## Execution Order and Checkpoints

1. Task 1 → Task 2 → Task 3 → Task 4 → Task 5 → Task 6 → Task 7 → Task 8 → Task 9，顺序不可交换。
2. 每个任务完成后运行该任务的 targeted tests，再运行受影响模块的完整测试；失败先修复再进入下一任务。
3. 每个任务形成一个可审阅提交；本机验证前不删除 legacy 表、legacy adapter 或旧路由兼容代码。
4. 所有长时门禁、真实 LLM、DeepFlow、Docker/Kubernetes 访问若受外部环境限制，保留可复现命令、日志和准确状态，不以静态测试替代真实部署结论。
