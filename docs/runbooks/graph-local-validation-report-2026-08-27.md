# 双存储本机验证报告（2026-08-27）

## 结论

本机 OrbStack Kubernetes 已完成 Fresh Install + runtime upgrade：最新一轮 AIOps
Helm release `aiops` revision 2 为 `deployed`，MySQL 作为控制面权威，Query API
运行在 `GRAPH_BACKEND=hugegraph`，HugeGraph 只承载可重建图投影。本轮验证显式跳过
DeepFlow（`--skip-deepflow`），因此不把 DeepFlow 环境门禁记为通过。

本报告中的集群观测数据来自 2026-08-28 当日 Fresh Install；镜像标签为
`git-39f4e7e8e0eb`。代码提交已在本地 `main`，远端 `origin/main` 尚未同步。

## 2026-08-28 代码闭环补充

针对本报告之后发现的验收偏差，当前工作树已完成以下代码修复并在本机验证：

- 服务全景主页面已固定为“服务摘要 → 服务地图 → 依赖主链 → 调用矩阵 → 服务列表 → 专家关系探索”；移除 ECharts force 全量图、稳定坐标缓存和 30 秒 force 重启轮询。服务地图使用有界 DAG 布局（最多 300 个实体、1000 条边）。
- 调用矩阵改为二维服务×服务表格，支持调用量、错误率、延迟、分页和滚动；新增页面、矩阵和地图契约测试。
- RCARequest 现在从 `ai_runs.time_range_start/time_range_end` 冻结窗口（提供 `from_ai_run` 工厂），RCAResult/GraphContext 保留同一时间窗；temporal score 在 RCA scorer 内按固定时间带计算；传播解释只保留实际的根因→症状有向路径。
- `graph-load-test.sh` 与类型化 Go fixture generator 已实现 200,000 vertex / 1,000,000 edge 数据集加载，以及 entity、1-hop、2-hop、shortest path、RCA candidate、impact、batch mutation 的 P95 固定门禁。真实环境已实际尝试：默认 2Gi HugeGraph 在约 50.5k vertex 写入时 EOF；本机验证 profile 已调整为 6Gi。随后 200k/1k 与 200k/10k 真实回归通过；完整 1M edge 在单节点审计/REST 吞吐下未在本轮完成，最终门禁仍为 `BLOCKED_BY_ENV`。
- HugeGraph edge batching 已修正为在服务端 16KiB `id in [...]` 限制下使用 15KB 预算，避免旧 12KB 拆分造成不必要的请求放大；对应客户端单测已通过。
- 本机代码验证：Go `go test ./... -count=1` 通过；Go Graph/API/Store `go test -race` 通过；Python `.venv314/bin/python -m pytest -q` 为 `1181 passed, 1 skipped`；前端全量 `25 files / 39 tests` 和 `npm run build` 通过。

## 已通过

- Go 全量测试：`go test ./... -count=1`。
- Go 并发检测：`go test -race ./internal/graph ./internal/api ./internal/store`。
- Python 全量测试：`1181 passed, 1 skipped`（2026-08-28，本机权限下运行）。
- 前端：`25 files / 39 tests passed`，生产构建成功（2026-08-28）。
- Helm lint、镜像统一 tag、Secret、RBAC、disabled Executor 和 canary 部署契约全部通过。
- MySQL migrations 已到 `0011_graph_projection`；`aiops_app` 与 `aiops_migrator` 账号权限已核验。
- HugeGraph 1.7.0 `/graphspaces/DEFAULT/graphs/aiops` 为 RocksDB；schema migrator
  首次创建与 runtime upgrade 幂等执行均成功，19 个 EdgeLabel 均包含完整 16 个
  可空边属性（含 `attrs_json`）。
- HugeGraph `Entity` 使用 `CUSTOMIZE_STRING`，包含完整 18 个实体属性（含
  `attrs_json`），没有混入 edge-only 属性。
- Query API、Worker、LLM Proxy、Ingest、Event Collector、Frontend、MySQL、
  ClickHouse、Victoria Metrics/Logs、HugeGraph 全部 Ready；`/readyz` 通过。
- Executor 处于 `EXECUTION_MODE=disabled`、`POD_SA_ACCESS=false`，无 canary mutation RBAC。
- canonical source reconcile 已真实执行：Kubernetes `402 mutations`、KubeVirt
  `7 mutations`、Middleware `54 mutations`；Catalog、Hardware、Trace、Change、Network
  在本机无事实时均为合法 `no_data`，没有 source 失败。
- 此前 canonical reconcile 样本（非本轮 200k/1M 负载 fixture）只读统计：300 vertices、189 edges；节点类型包括 1 个 cluster、1 个
  node、8 个 namespace、23 个 deployment、63 个 pod、71 个 container、27 个 service、
  27 个 EndpointSlice、7 个 PVC、7 个 PV、1 个 StorageClass 等；关系包括
  `CONTAINS`、`OWNS`、`RUNS_ON`、`TARGETS`、`BACKED_BY`、`USES_VOLUME`、`BOUND_TO`、
  `DEPENDS_ON`。数据来源为 Kubernetes、KubeVirt、Middleware，未伪造业务 Catalog 数据。
- MySQL `graph_reconcile_runs` 最新记录中所有 8 个 source 均为 `success`；Kubernetes
  `vertices_seen=246, edges_seen=156`，KubeVirt `7/0`，Middleware `36/18`，并回写了
  stale 计数 `2/3`。
- 本轮 ClickHouse 创建并核验了空的 `observability.change_records` SoT 表，Change
  canonical query 返回 `no_data`；不会将缺表或 SQL 类型错误伪装成空数据。

## 环境阻断项

以下不是静态测试替代项，当前没有伪造通过：

- 没有提供真实 metric/log/event marker，因此真实观测数据链路未判通过。
- 已提供本机可写 HugeGraph 并执行真实加载尝试；由于 1M edge 在单节点审计/REST 吞吐下未完成，完整负载与全部 P95 采样仍为 `BLOCKED_BY_ENV`。小规模 200k/1k、200k/10k 只作为连通性/批处理回归，不替代固定门禁。
- 没有执行真实 LLM provider response、DeepFlow flow/span、multi-node failover、PITR
  和 Credential Broker 证据。
- 未在本次会话内持续完成 2 小时 shadow 或 24 小时 soak；需要按
  `docs/runbooks/graph-cutover.md` 的切换前置条件继续观察。

验证脚本会将上述状态报告为 `BLOCKED_BY_ENV`，而不是将空数据当成成功。

## 依赖源

自研镜像优先使用 DaoCloud 国内基础镜像，Go 使用 `goproxy.cn`，前端 npm 使用
`npmmirror.com`，Alpine 使用阿里云 APK 源，Debian 使用清华源；DeepFlow chart
使用已缓存的官方 Helm 仓库版本 7.1.002，镜像采用 `IfNotPresent`。
