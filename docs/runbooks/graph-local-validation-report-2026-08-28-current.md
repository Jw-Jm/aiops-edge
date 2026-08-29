# 当前工作区本机验证记录（2026-08-28）

## 已完成

- 当前工作区已执行破坏性 Fresh Install：删除并重建 `observability`、`deepflow`
  和 `aiops-canary`，随后按 bootstrap → migration → runtime 两阶段 Helm 流程安装。
- 破坏性 Fresh Install 首次基于 `26b9e8a196dd` 完成；历史上曾将
  `2f0423237876` 以统一标签 `git-2f0423237876` 非破坏性升级到本机集群。
- MySQL users/schema hooks、`0011_graph_projection`、HugeGraph schema migrator、
  Query API、Worker（2 副本）、LLM Proxy、Ingest、Event Collector、Frontend、
  ClickHouse、Victoria Metrics/Logs、HugeGraph 均 Ready；只读栈验证退出码为 0。
- Executor 保持 `EXECUTION_MODE=disabled`、`realMutation=false`、
  `POD_SA_ACCESS=false`，且 disabled 模式没有 canary namespace RBAC；本轮没有执行真实
  Kubernetes mutation。
- DeepFlow Helm release 已部署，Agent/Server/ClickHouse/MySQL/Grafana Pod 均 Ready。

- 本机运行时 Helm release 已推进到 `aiops` revision 5，结构/就绪状态保持正常。
- 本轮认证修复后已将本地 release 滚动升级到 revision 5；admin 登录实测 HTTP 200，
  `must_change_password=false`，没有输出 token。生产 Helm 模板回读仍为
  `GRAPH_BACKEND=legacy_mysql` 且首次改密开关为 `true`；本地 Query API 使用当前工作区
  修复镜像滚动升级，其他服务未被重新构建。

## 图谱 fixture

类型化 loader 已真实写入 HugeGraph，并返回：

- 200,000 vertices；
- 1,000,000 unique typed edges；
- 20,000 service、50,000 pod、50,000 container、5,000 VM、5,000 VMI、4,000
  k8s_node、3,000 physical_server、3,000 dimm 等本体分布；
- loader duration `147413ms`，batch mutation P95 `211.316ms`。

按新八项严格门禁重跑的报告为
`/tmp/aiops-graph-load-report-20260828-strict-final.json`：fixture
`loaded=true`、200,000/1,000,000 计数正确，八项均有完整样本，但 2-hop 为 `19/20`
成功且 P95 `1132ms`，impact 成功率 `0.65` 且 P95 `1589ms`，因此 `gate_status=FAIL`。
资源门禁另有 HugeGraph heap used/max 与浏览器 Long Task 未采集，不能宣称 PASS。

## 尚未通过的门禁

本次代码收敛已为本机验证 profile 配置临时管理员密码，并关闭首次登录强制改密；生产
仍保持首次登录改密开启且不读取本机默认值。完成性能/资源门禁整改后，应使用本地登录
生成的旋转 token 重跑八项 Query API P95 门禁：

```bash
GRAPH_API_TENANT_ID='<authorized-tenant-uuid>' \
GRAPH_API_CLUSTER_ID='<authorized-cluster-uuid>' \
GRAPH_API_TOKEN='<rotated-jwt>' \
HUGEGRAPH_URL='http://127.0.0.1:18080' \
GRAPH_API_BASE_URL='http://127.0.0.1:18081/api/v1/ai/kg' \
bash deploy/scripts/graph-load-test.sh \
  --output /tmp/aiops-graph-load-report-20260828.json
```

真实 metric/log/event marker、真实 provider 响应、DeepFlow flow/span、资源快照、2 小时 shadow、
24 小时 soak、多节点 failover、PITR、Credential Broker 仍需对应环境证据；没有证据时
必须保持 `BLOCKED_BY_ENV`。
