# 双存储本机验证报告（2026-08-27）

## 结论

本机 OrbStack Kubernetes 已完成 Fresh Install + runtime upgrade：AIOps Helm release
`aiops` revision 10 为 `deployed`，MySQL 作为控制面权威，Query API 运行在
`GRAPH_BACKEND=hugegraph`，HugeGraph 只承载可重建图投影。本轮验证显式跳过
DeepFlow（`--skip-deepflow`），因此不把 DeepFlow 环境门禁记为通过。

本轮源码工作区存在未提交改动，因此结论适用于当前工作树和本机镜像
`git-fa13f9c46050`，不宣称对应远端 Git 分支。

## 已通过

- Go 全量测试：`go test ./... -count=1`。
- Go 并发检测：`go test -race ./internal/graph ./internal/api ./internal/store`。
- Python 全量测试：`1178 passed, 1 skipped`。
- 前端：`21 files / 34 tests passed`，生产构建成功。
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
- HugeGraph 实际只读统计：300 vertices、189 edges；节点类型包括 1 个 cluster、1 个
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
- 没有执行真实 LLM provider response、DeepFlow flow/span、multi-node failover、PITR
  和 Credential Broker 证据。
- 未在本次会话内持续完成 2 小时 shadow 或 24 小时 soak；需要按
  `docs/runbooks/graph-cutover.md` 的切换前置条件继续观察。

验证脚本会将上述状态报告为 `BLOCKED_BY_ENV`，而不是将空数据当成成功。

## 依赖源

自研镜像优先使用 DaoCloud 国内基础镜像，Go 使用 `goproxy.cn`，前端 npm 使用
`npmmirror.com`，Alpine 使用阿里云 APK 源，Debian 使用清华源；DeepFlow chart
使用已缓存的官方 Helm 仓库版本 7.1.002，镜像采用 `IfNotPresent`。
