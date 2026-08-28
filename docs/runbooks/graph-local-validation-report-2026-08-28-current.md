# 当前工作区本机验证记录（2026-08-28）

## 已完成

- 当前工作区已执行破坏性 Fresh Install：删除并重建 `observability`、`deepflow`
  和 `aiops-canary`，随后按 bootstrap → migration → runtime 两阶段 Helm 流程安装。
- 当前 Git 基线为 `26b9e8a196dd`；全部自研镜像已使用统一标签
  `git-26b9e8a196dd` 构建。
- MySQL users/schema hooks、`0011_graph_projection`、HugeGraph schema migrator、
  Query API、Worker（2 副本）、LLM Proxy、Ingest、Event Collector、Frontend、
  ClickHouse、Victoria Metrics/Logs、HugeGraph 均 Ready；只读栈验证退出码为 0。
- Executor 保持 `EXECUTION_MODE=disabled`、`realMutation=false`、
  `POD_SA_ACCESS=false`，且 disabled 模式没有 canary namespace RBAC；本轮没有执行真实
  Kubernetes mutation。
- DeepFlow Helm release 已部署，Agent/Server/ClickHouse/MySQL/Grafana Pod 均 Ready。

## 图谱 fixture

类型化 loader 已真实写入 HugeGraph，并返回：

- 200,000 vertices；
- 1,000,000 unique typed edges；
- 20,000 service、50,000 pod、50,000 container、5,000 VM、5,000 VMI、4,000
  k8s_node、3,000 physical_server、3,000 dimm 等本体分布；
- loader duration `147413ms`，batch mutation P95 `211.316ms`。

## 尚未通过的门禁

当前 Fresh Install 的 admin 账号仍处于首次登录强制改密状态。图谱公开 API 因此返回
`password_change_required`，七项 Query API P95 采样被安全地记为
`BLOCKED_BY_ENV`，没有绕过认证或伪造 PASS。完成一次明确授权的本机临时密码变更后，
应使用 Fresh Install 生成的旋转 token 重跑：

```bash
GRAPH_API_TENANT_ID='<authorized-tenant-uuid>' \
GRAPH_API_CLUSTER_ID='<authorized-cluster-uuid>' \
GRAPH_API_TOKEN='<rotated-jwt>' \
HUGEGRAPH_URL='http://127.0.0.1:18080' \
GRAPH_API_BASE_URL='http://127.0.0.1:18081/api/v1/ai/kg' \
bash deploy/scripts/graph-load-test.sh \
  --output /tmp/aiops-graph-load-report-20260828.json
```

真实 metric/log/event marker、真实 provider 响应、DeepFlow flow/span、2 小时 shadow、
24 小时 soak、多节点 failover、PITR、Credential Broker 仍需对应环境证据；没有证据时
必须保持 `BLOCKED_BY_ENV`。
