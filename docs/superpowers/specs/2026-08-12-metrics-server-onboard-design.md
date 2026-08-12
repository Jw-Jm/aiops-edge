# 部署 metrics-server 并完成 AIOps 平台对接 — 设计文档

日期：2026-08-12
状态：已获用户批准（生产级）

## 背景与目标

AIOps 平台当前缺少**节点实时 CPU/内存用量**能力，且容量预测因 node-exporter 抓取目标为空而缺真实数据。本次：
1. 部署 metrics-server（K8s 标准组件，提供 `kubectl top` / HPA / Metrics API）
2. 让 AIOps 通过 Metrics API 展示节点实时用量（填补功能空缺）
3. 修复 node-exporter 抓取，让容量预测用上真实节点指标

**生产级要求**（用户明确）：多节点自动发现、直连 Metrics API 不依赖 kubectl、metrics-server 安全/高可用配置。

## 涉及组件

| 组件 | 改动 |
|------|------|
| metrics-server | 独立 helm 部署到 kube-system（生产级配置） |
| `query-api` | 新增节点实时用量接口（复用 `k8sAPI` 调 Metrics API）+ RBAC |
| `observability-frontend` | 管理后台节点表新增实时用量列 |
| `deploy/helm/aiops` | node-exporter 用 kubernetes_sd_configs 让 VM 自动发现抓取 |

---

## 部分 1：部署 metrics-server（独立 helm 到 kube-system）

**部署**：官方 `metrics-server/metrics-server` helm chart，release 名 `metrics-server`，namespace `kube-system`。

**生产级 values**：
```yaml
replicas: 2
args:
  - --kubelet-preferred-address-types=InternalIP
  # 生产挂载 kubelet CA（不用 --kubelet-insecure-tls）
  - --kubelet-certificate-authority=/etc/kubernetes/pki/ca.crt
securityContext:
  runAsNonRoot: true
resources:
  requests: { cpu: 50m, memory: 100Mi }
  limits:   { cpu: 200m, memory: 300Mi }
```

**依赖**：
- `metrics.k8s.io` APIService 注册（helm chart 自带）
- ClusterRole/ClusterRoleBinding（chart 自带，`system:metrics-server`）

> 注：OrbStack 单节点无 CA 文件时，作为环境适配可降级 `--kubelet-insecure-tls`，但生产清单保持 CA 挂载。实现时以环境实际可用性为准，在代码/spec 中标注。

**验证**：`kubectl top nodes` 返回 CPU/内存；`kubectl get --raw /apis/metrics.k8s.io/v1beta1/nodes` 返回节点用量。

---

## 部分 2：AIOps 节点实时用量（直连 Metrics API）

### 后端 `query-api`

**新增接口**：`GET /api/v1/nodes/metrics`

**实现**：复用 `infrastructure.go` 的 `k8sAPI(path)`（in-cluster 直接 HTTP + SA token），调 `GET /apis/metrics.k8s.io/v1beta1/nodes`：

```go
// k8sNodesMetrics 获取节点实时 CPU/内存用量（metrics-server Metrics API）
func (h *Handler) nodesMetrics(w http.ResponseWriter, r *http.Request) {
    data, err := k8sAPI("/apis/metrics.k8s.io/v1beta1/nodes")
    if err != nil { respondJSON(w, 500, map[string]interface{}{"nodes": []interface{}{}, "error": err.Error()}); return }
    // 解析 MetricsList：usage[].cpu.usageNanoCores / usage[].memory.usageBytes
    // 与 k8sNodes() 的 capacity/allocatable 合并，计算 cpu/mem usage_pct
    nodes := mergeNodeMetrics(parseMetricsNodes(data), k8sNodes())
    respondJSON(w, 200, map[string]interface{}{"nodes": nodes})
}
```

返回结构（每节点）：
```
{ node, cpu_usage, cpu_capacity, cpu_usage_pct, mem_usage, mem_capacity, mem_usage_pct, allocatable_cpu, allocatable_mem }
```

**多集群**：单集群走 `k8sAPI`（in-cluster）。多集群集群详情页复用 `clusterKubeconfig` → 解析 `server`+`token` 直连该集群 Metrics API（复用 `k8sNodesWithKubeconfig` 模式）。

**RBAC**：为 query-api 的 ServiceAccount 增加 `metrics.k8s.io` 的 `get` 权限。需确认现有 ClusterRoleBinding `view` 是否已含；若无，新增 ClusterRole + RoleBinding 允许 query-api SA `get /metrics.k8s.io/v1beta1/nodes`。

**kubectlFallback 扩展**：`k8sAPI` 失败时，`kubectlFallback` 增加 `metrics.k8s.io` 路径 → `kubectl top nodes -o json`（兜底）。

### 前端 `observability-frontend`

**管理后台节点表**（`AdminSettings.tsx` L180-188）新增列：
- **CPU 用量**：`cpu_usage_pct` 百分比 + 用量（如 `12.5% (250m/2)`）
- **内存用量**：`mem_usage_pct` 百分比 + 用量（如 `45% (1.8Gi/4Gi)`）

**client.ts**：新增 `getNodeMetrics()` → `GET /api/v1/nodes/metrics`。

---

## 部分 3：node-exporter 抓取（kubernetes_sd_configs 自动发现）

**根因**：`values.yaml` 的 `nodeExporterTarget: ""` → VM scrape-config 不抓 node-exporter（daemonset 已部署，hostNetwork:true + hostPort:9100）。单值 `nodeExporterTarget` 在多节点有缺陷（Service 负载均衡只命中部分节点）。

**方案**：VictoriaMetrics（v1.101.0，支持 k8s SD）用 **`kubernetes_sd_configs`** 自动发现 node-exporter DaemonSet pod（`role: pod` + 标签选择），多节点自动发现。

**scrape-config 修改**（`victoria-metrics/scrape-config.yaml`）：
```yaml
- job_name: node-exporter
  kubernetes_sd_configs:
    - role: pod
  relabel_configs:
    - source_labels: [__meta_kubernetes_pod_label_app_kubernetes_io_name]
      regex: node-exporter
      action: keep
    - source_labels: [__meta_kubernetes_pod_ip, __meta_kubernetes_pod_container_port_number]
      regex: (.+);(.+)
      action: replace
      target_label: __address__
      replacement: ${1}:${2}
```

**依赖**：VM 的 SA 需有 `list/get pods` 权限（k8s SD 用 in-cluster 读 pod）。VM 部署若在 observability namespace，需 ClusterRoleBinding 允许 VM SA 读 pod（或复用已有）。

**values.yaml**：移除 `nodeExporterTarget`（不再需要单值），或保留作为兼容 fallback。

**验证**：VM 的 `promapi/v1/query?query=up{job="node-exporter"}` 返回 `1`；容量预测页（CapacityForecast）current 有真实 CPU/内存值。

---

## 测试计划

**后端（query-api）**：
- `nodes/metrics` 接口单测（mock `k8sAPI` 返回 MetricsList JSON + k8sNodes capacity）→ 断言 usage_pct 计算正确
- `parseMetricsNodes`/`mergeNodeMetrics` 单测（cpu/mem 转换 n 字节 ↔ Gi）
- 容量预测回归测试

**前端**：
- 节点表实时用量列渲染（tsc + playwright 验证）

**部署验证**：
- `kubectl top nodes` 可用
- `GET /api/v1/nodes/metrics` 返回真实用量
- 节点表显示实时 CPU/内存
- 容量预测页有真实数据（VM 抓到 node-exporter）

## 部署与同步

1. metrics-server helm 部署到 kube-system
2. query-api 重建镜像（如 v1.3.15）、frontend 重建（如 v3.4.9）
3. aiops helm upgrade（node-exporter k8s SD + RBAC）
4. 本地验证后同步 GitHub `Jw-Jm/aiops-edge` main

## 不做的事（YAGNI）
- 不为节点用量新建独立大页面（复用管理后台节点表，足够展示）
- 不实现 Pod 级实时用量（本次聚焦节点级）
- 不改容量预测算法（只补数据源）
