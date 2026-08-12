# 部署 metrics-server 并完成 AIOps 平台对接 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 部署 metrics-server（kube-system），让 AIOps 通过 K8s Metrics API 展示节点实时 CPU/内存用量，并修复 node-exporter 抓取让容量预测用上真实节点指标。

**Architecture:** metrics-server 独立 helm 部署提供 `metrics.k8s.io` APIService；query-api 复用 `k8sAPI()` in-cluster 直连 Metrics API 获取节点用量（不依赖 kubectl）；VictoriaMetrics 用 `kubernetes_sd_configs` 自动发现 node-exporter DaemonSet pod；前端集群详情节点表新增实时用量列。

**Tech Stack:** helm / Kubernetes Metrics API / Go (query-api) / VictoriaMetrics promscrape / React (Ant Design)

## Global Constraints

- metrics-server 独立 helm 部署到 kube-system（release `metrics-server`），生产级：`replicas: 2`、资源配额、CA 挂载（OrbStack 单节点无 CA 时环境适配可降级 `--kubelet-insecure-tls`）。
- query-api 节点用量**复用 `k8sAPI()` in-cluster 直连**（`/apis/metrics.k8s.io/v1beta1/nodes`），**不依赖 kubectl**；`kubectlFallback` 仅作兜底。
- query-api SA 需补 `metrics.k8s.io` 的 `get/list` 权限（`view` ClusterRole 不含）。
- node-exporter 抓取改用 `kubernetes_sd_configs`（VM v1.101.0 支持），VM SA 需补 `list/get pods` 权限。
- 前端沿用 Ant Design + 现有 CSS 变量。
- query-api Go 测试用 mock（`k8sAPI` 注入 MetricsList JSON）；本机 python3=3.9，orchestrator 改动用 `.venv-312` + `AIOPS_DATA_DIR`。
- 部署后同步 GitHub `Jw-Jm/aiops-edge` main。

---

### Task 1: 部署 metrics-server（独立 helm 到 kube-system）

**Files:**
- 部署：helm（`metrics-server/metrics-server`），无代码文件改动
- 验证：`kubectl top nodes`

**Interfaces:**
- Consumes: 无（独立部署）
- Produces: `metrics.k8s.io/v1beta1` APIService 可用（Task 2 依赖）

- [ ] **Step 1: 添加 metrics-server helm 仓库并部署**

```bash
helm repo add metrics-server https://kubernetes-sigs.github.io/metrics-server/
helm repo update metrics-server
```

- [ ] **Step 2: 用生产级 values 部署到 kube-system**

```bash
helm upgrade --install metrics-server metrics-server/metrics-server \
  --namespace kube-system \
  --set replicas=2 \
  --set resources.requests.cpu=50m \
  --set resources.requests.memory=100Mi \
  --set resources.limits.cpu=200m \
  --set resources.limits.memory=300Mi
```

> 若 OrbStack 单节点 kubelet 无 CA 证书导致 metrics-server 无法连接 kubelet，追加 `--set args[0]=--kubelet-insecure-tls` 环境适配（生产清单应挂载 CA，此处为本地验证）。

- [ ] **Step 3: 等待就绪并验证 Metrics API**

```bash
kubectl rollout status deploy/metrics-server -n kube-system --timeout=180s
kubectl get apiservice | grep metrics
kubectl top nodes
```

Expected: `kubectl top nodes` 返回节点 CPU/内存用量（如 `orbstack  250m  4%  3Gi  25%`）。

- [ ] **Step 4: 验证 `/apis/metrics.k8s.io` 可被 SA token 访问**

```bash
kubectl get --raw /apis/metrics.k8s.io/v1beta1/nodes | head -c 500
```

Expected: 返回节点 metrics JSON（usage 含 cpu/memory）。

- [ ] **Step 5: Commit（无代码，仅记录部署到部署文档即可，或跳过 commit）**

---

### Task 2: query-api 新增节点实时用量接口 + RBAC

**Files:**
- Modify: `aiops/ai-apm-query-go/internal/api/infrastructure.go`
- Modify: `aiops/ai-apm-query-go/cmd/api/main.go`
- Modify: `aiops/deploy/helm/aiops/templates/query-api/rbac.yaml`
- Test: `aiops/ai-apm-query-go/internal/api/nodes_metrics_test.go`（新建）

**Interfaces:**
- Consumes: `k8sAPI(path)`（已存在，in-cluster 直连）、`k8sNodes()`（已存在，返回 `[]store.ClusterNode` 含 CPU/Memory capacity）
- Produces: `GET /api/v1/nodes/metrics` → `{"nodes":[{node, cpu_usage, cpu_usage_pct, mem_usage, mem_usage_pct, cpu_capacity, mem_capacity}]}`

- [ ] **Step 1: 写失败测试（parseQuantity + nodesMetrics）**

新建 `internal/api/nodes_metrics_test.go`：

```go
package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestParseQuantity(t *testing.T) {
	cases := map[string]float64{
		"1000m": 1.0, "1536m": 1.536, "2": 2.0, "0.5": 0.5,
		// 内存统一换算到 Ki 基数：1Mi=1024Ki，1Gi=1024Mi=1048576Ki
		"12345Ki": 12345.0, "1Gi": 1048576.0, "500Mi": 512000.0, "3Gi": 3145728.0,
		"": 0.0,
	}
	for in, want := range cases {
		if got := parseQuantity(in); got != want {
			t.Errorf("parseQuantity(%q)=%v, want %v", in, got, want)
		}
	}
}

// 用真实 k8sAPI 返回的 MetricsList 结构测解析与 usage_pct 计算。
func TestParseNodeMetrics(t *testing.T) {
	body := []byte(`{"kind":"NodeMetricsList","items":[
		{"metadata":{"name":"orbstack"},
		 "usage":{"cpu":"250m","memory":"3212084Ki"}},
		{"metadata":{"name":"worker-1"},
		 "usage":{"cpu":"1","memory":"2Gi"}}
	]}`)
	// capacity 来自 k8sNodes：CPU="2" memory="4Gi"
	nodes := parseNodeMetrics(body, map[string]map[string]string{
		"orbstack": {"cpu": "2", "memory": "4Gi"},
		"worker-1": {"cpu": "2", "memory": "8Gi"},
	})
	if len(nodes) != 2 {
		t.Fatalf("expected 2 nodes, got %d", len(nodes))
	}
	for _, n := range nodes {
		m := n.(map[string]interface{})
		name := m["node"].(string)
		cpuPct := m["cpu_usage_pct"].(float64)
		memPct := m["mem_usage_pct"].(float64)
		if name == "orbstack" {
			// 0.25/2*100 = 12.5
			if cpuPct < 12.4 || cpuPct > 12.6 { t.Errorf("orbstack cpu_pct=%v want ~12.5", cpuPct) }
			// 3212084Ki / 4Gi(4194304Ki) *100
			if memPct < 76 || memPct > 77 { t.Errorf("orbstack mem_pct=%v want ~76.6", memPct) }
		}
	}
}

// 验证 handler：注入 k8sAPI 返回 MetricsList，assert usage_pct 计算与 200。
func TestNodesMetricsHandler(t *testing.T) {
	h := &Handler{}
	orig := k8sAPIFn
	k8sAPIFn = func(path string) ([]byte, error) {
		return []byte(`{"items":[{"metadata":{"name":"orbstack"},"usage":{"cpu":"250m","memory":"2Gi"}}]}`), nil
	}
	defer func() { k8sAPIFn = orig }()
	req := httptest.NewRequest("GET", "/api/v1/nodes/metrics", nil)
	w := httptest.NewRecorder()
	h.NodesMetrics(w, req)
	if w.Code != http.StatusOK { t.Fatalf("expected 200, got %d", w.Code) }
	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	nodes := resp["nodes"].([]interface{})
	if len(nodes) != 1 { t.Fatalf("expected 1 node, got %d", len(nodes)) }
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `cd aiops/ai-apm-query-go && go test ./internal/api/ -run 'ParseQuantity|ParseNodeMetrics|NodesMetrics' -v`
Expected: FAIL（`parseQuantity`/`parseNodeMetrics` 未定义、`k8sAPIFn` 不存在）

- [ ] **Step 3: 实现 `parseQuantity`（K8s Quantity 解析）**

`infrastructure.go` 追加：

```go
// parseQuantity 解析 K8s Quantity 为浮点值（CPU 核心数 / 内存 MiB 基数的 KB 数）。
// 支持 CPU: "250m"(0.25核), "2"(2核)；内存: "1Gi"=1024, "500Mi"=500, "12345Ki"=12345。
// 返回的数值统一到"原单位"的数值：CPU 返回核心数，内存返回以 Ki 为基数（1Gi=1024Ki）。
func parseQuantity(s string) float64 {
	s = strings.TrimSpace(s)
	if s == "" { return 0 }
	// 后缀映射：CPU 的 m=0.001；内存的 Ki/Mi/Gi 以 Ki 为基数
	mult := 1.0
	numStr := s
	lower := strings.ToLower(s)
	switch {
	case strings.HasSuffix(lower, "m"):
		mult = 0.001
		numStr = s[:len(s)-1]
	case strings.HasSuffix(lower, "ki"):
		mult = 1.0; numStr = s[:len(s)-2]
	case strings.HasSuffix(lower, "mi"):
		mult = 1024.0; numStr = s[:len(s)-2]
	case strings.HasSuffix(lower, "gi"):
		mult = 1024.0 * 1024.0; numStr = s[:len(s)-2]
	case strings.HasSuffix(lower, "k"):
		mult = 1000.0; numStr = s[:len(s)-1]
	case strings.HasSuffix(lower, "g"):
		mult = 1e9; numStr = s[:len(s)-1]
	}
	v, err := strconv.ParseFloat(numStr, 64)
	if err != nil { return 0 }
	return v * mult
}
```

> 说明：为统一 CPU 与内存的 usage_pct 计算，CPU 用 `parseQuantity(cpuUsage)`（核心数），capacity 用 `parseQuantity(node.CPU)`（如 "2"）；内存 usage（Ki 基数）与 capacity（node.Memory，如 "4Gi"→4194304Ki）用同基数。`parseNodeMetrics` 里内存统一换算为 **Ki**。

- [ ] **Step 4: 实现 `parseNodeMetrics` + `NodesMetrics` handler**

`infrastructure.go` 追加：

```go
// parseNodeMetrics 解析 MetricsList + capacity，返回每节点实时用量（usage_pct）。
func parseNodeMetrics(data []byte, capacities map[string]map[string]string) []map[string]interface{} {
	var r struct {
		Items []struct {
			Metadata struct{ Name string } `json:"metadata"`
			Usage    map[string]string     `json:"usage"`
		} `json:"items"`
	}
	json.Unmarshal(data, &r)
	out := []map[string]interface{}{}
	for _, it := range r.Items {
		cpuUsage := parseQuantity(it.Usage["cpu"])       // 核心数
		memUsage := parseQuantity(it.Usage["memory"])    // Ki 基数
		cpuCap := parseQuantity(capacities[it.Metadata.Name]["cpu"])
		memCap := parseQuantity(capacities[it.Metadata.Name]["memory"])
		cpuPct := 0.0
		if cpuCap > 0 { cpuPct = cpuUsage / cpuCap * 100 }
		memPct := 0.0
		if memCap > 0 { memPct = memUsage / memCap * 100 }
		out = append(out, map[string]interface{}{
			"node": it.Metadata.Name,
			"cpu_usage": it.Usage["cpu"], "cpu_capacity": capacities[it.Metadata.Name]["cpu"],
			"cpu_usage_pct": round2(cpuPct),
			"mem_usage": it.Usage["memory"], "mem_capacity": capacities[it.Metadata.Name]["memory"],
			"mem_usage_pct": round2(memPct),
		})
	}
	return out
}

// k8sAPIFn 可注入（测试用）；默认指向 k8sAPI。
var k8sAPIFn = k8sAPI

// NodesMetrics 处理 GET /api/v1/nodes/metrics — 节点实时 CPU/内存用量（metrics-server）。
func (h *Handler) NodesMetrics(w http.ResponseWriter, r *http.Request) {
	data, err := k8sAPIFn("/apis/metrics.k8s.io/v1beta1/nodes")
	if err != nil {
		respondJSON(w, 200, map[string]interface{}{"nodes": []map[string]interface{}{}, "error": err.Error()})
		return
	}
	// capacity 从 /api/v1/nodes 读取
	capMap := map[string]map[string]string{}
	for _, n := range k8sNodes() {
		capMap[n.Name] = map[string]string{"cpu": n.CPU, "memory": n.Memory}
	}
	respondJSON(w, 200, map[string]interface{}{"nodes": parseNodeMetrics(data, capMap)})
}
```

- [ ] **Step 5: 扩展 `kubectlFallback` 支持 metrics 路径**

`infrastructure.go` 的 `kubectlFallback` 开头追加：

```go
func kubectlFallback(path string) ([]byte, error) {
	args := []string{"get"}
	if strings.Contains(path, "/metrics.k8s.io") {
		return exec.Command("kubectl", "top", "nodes", "-o", "json").Output()
	}
	switch {
```

- [ ] **Step 6: 注册路由**

`main.go` 基础设施路由区（L128 后）追加：

```go
	mux.HandleFunc("/api/v1/nodes/metrics", handler.NodesMetrics)
```

- [ ] **Step 7: 补 RBAC（query-api SA 读 metrics.k8s.io）**

`query-api/rbac.yaml` 的 `query-api-node-reader` ClusterRole rules 追加：

```yaml
- apiGroups: ["metrics.k8s.io"]
  resources: ["nodes"]
  verbs: ["get", "list"]
```

- [ ] **Step 8: 运行测试确认通过**

Run: `cd aiops/ai-apm-query-go && go test ./internal/api/ -v 2>&1 | tail -20`
Expected: 新增测试通过，全量无回归

- [ ] **Step 9: Commit**

```bash
cd /Users/mssc/Documents/Code/agent/aiops
git add ai-apm-query-go/internal/api/infrastructure.go ai-apm-query-go/internal/api/nodes_metrics_test.go ai-apm-query-go/cmd/api/main.go deploy/helm/aiops/templates/query-api/rbac.yaml
git commit -m "feat: query-api 节点实时用量接口（Metrics API）+ RBAC"
```

---

### Task 3: node-exporter 抓取改为 kubernetes_sd_configs（容量预测真实数据）

**Files:**
- Modify: `aiops/deploy/helm/aiops/templates/victoria-metrics/scrape-config.yaml`
- Modify: `aiops/deploy/helm/aiops/values.yaml`
- Create: `aiops/deploy/helm/aiops/templates/victoria-metrics/rbac.yaml`（新建，VM SA 读 pod）

**Interfaces:**
- Consumes: node-exporter DaemonSet 标签 `app.kubernetes.io/name=node-exporter`（已有）
- Produces: VM 抓到 `up{job="node-exporter"}` → `CapacityInstances` 返回真实 instance → `CapacityForecast` current 有真实值

- [ ] **Step 1: 创建 VM RBAC（k8s SD 需要读 pod）**

新建 `victoria-metrics/rbac.yaml`：

```yaml
{{- if .Values.victoriaMetrics.enabled }}
apiVersion: v1
kind: ServiceAccount
metadata:
  name: victoria-metrics
  namespace: {{ .Values.namespace.observability }}
  labels:
    app: victoria-metrics
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: victoria-metrics-k8s-sd
  labels:
    app: victoria-metrics
rules:
- apiGroups: [""]
  resources: ["pods", "services", "endpoints"]
  verbs: ["get", "list", "watch"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: victoria-metrics-k8s-sd
  labels:
    app: victoria-metrics
subjects:
- kind: ServiceAccount
  name: victoria-metrics
  namespace: {{ .Values.namespace.observability }}
roleRef:
  kind: ClusterRole
  name: victoria-metrics-k8s-sd
  apiGroup: rbac.authorization.k8s.io
{{- end }}
```

- [ ] **Step 2: VM deployment 指定 SA**

`victoria-metrics/deployment.yaml` 的 `spec.template.spec` 加：

```yaml
      serviceAccountName: victoria-metrics
```

- [ ] **Step 3: 改造 scrape-config 为 k8s SD**

`victoria-metrics/scrape-config.yaml` 中 node-exporter 段改为（**移除 `nodeExporterTarget` 条件**，始终用 k8s SD）：

```yaml
    scrape_configs:
      - job_name: node-exporter
        kubernetes_sd_configs:
          - role: pod
        relabel_configs:
          - source_labels: [__meta_kubernetes_pod_label_app_kubernetes_io_name]
            regex: node-exporter
            action: keep
          - source_labels: [__meta_kubernetes_pod_ip]
            regex: (.+)
            action: replace
            target_label: __address__
            replacement: ${1}:9100
        metric_relabel_configs:
          - source_labels: [__name__]
            regex: (node_|process_)
            action: keep
      - job_name: ingest
        static_configs:
          - targets: ['ingest:{{ .Values.ingestPort }}']
            labels: { job: ingest }
      - job_name: orchestrator
        static_configs:
          - targets: ['ai-orchestrator:{{ .Values.orchestratorPort }}']
            labels: { job: orchestrator }
```

- [ ] **Step 4: values.yaml 移除 nodeExporterTarget（或保留空）**

`values.yaml` L137 `nodeExporterTarget: ""` 保留（scrape-config 不再引用它），并加注释说明已改用 k8s SD。

- [ ] **Step 5: helm 渲染校验**

Run: `cd /Users/mssc/Documents/Code/agent/aiops && helm template aiops deploy/helm/aiops --namespace observability 2>&1 | grep -A8 'job_name: node-exporter'`
Expected: node-exporter 段含 `kubernetes_sd_configs`。

- [ ] **Step 6: Commit**

```bash
cd /Users/mssc/Documents/Code/agent/aiops
git add deploy/helm/aiops/templates/victoria-metrics/rbac.yaml deploy/helm/aiops/templates/victoria-metrics/deployment.yaml deploy/helm/aiops/templates/victoria-metrics/scrape-config.yaml deploy/helm/aiops/values.yaml
git commit -m "feat: node-exporter 抓取改用 kubernetes_sd_configs（多节点自动发现）"
```

---

### Task 4: 前端集群详情节点表新增实时用量列

**Files:**
- Modify: `observability-frontend/src/pages/admin/AdminSettings.tsx`
- Modify: `observability-frontend/src/api/client.ts`

**Interfaces:**
- Consumes: `GET /api/v1/nodes/metrics`（Task 2 后端）；集群详情 `nodes` 数据
- Produces: 节点表新增"CPU 用量/内存用量"列

- [ ] **Step 1: client.ts 新增 `getNodeMetrics`**

`client.ts` 追加：

```typescript
export const getNodeMetrics = () => api.get('/nodes/metrics')
```

- [ ] **Step 2: 前端节点表加载用量并新增列**

`AdminSettings.tsx` 集群详情"节点" tab（L180-188）改造：

```tsx
{ key: 'nodes', label: `节点 (${nodes.length})`, children: (
  <Table rowKey="name" size="small" dataSource={nodes} pagination={false} columns={[
    { title: '节点', dataIndex: 'name' },
    { title: '角色', dataIndex: 'role', width: 90 },
    { title: '状态', dataIndex: 'status', width: 100, render: (s) => <StatusBadge text={s} tone={clusterTone(s)} /> },
    { title: 'IP', dataIndex: 'ip', width: 120 },
    { title: 'OS', dataIndex: 'os', ellipsis: true },
    { title: 'CPU 用量', width: 120, render: (_, r) => { const m = nodeMetrics[r.name]; return m ? `${m.cpu_usage_pct}% (${m.cpu_usage}/${m.cpu_capacity})` : (r.cpu || '-') } },
    { title: '内存用量', width: 130, render: (_, r) => { const m = nodeMetrics[r.name]; return m ? `${m.mem_usage_pct}% (${m.mem_usage}/${m.mem_capacity})` : (r.memory || '-') } },
  ]} />
)},
```

`AdminSettings.tsx` 组件内新增 state 与加载：

```tsx
const [nodeMetrics, setNodeMetrics] = useState<Record<string, any>>({})
// viewDetail 加载节点时并行获取用量
useEffect(() => {
  if (!detail) return
  getNodeMetrics().then((r) => {
    const m: Record<string, any> = {}
    ;(r.data?.nodes || []).forEach((n: any) => { m[n.node] = n })
    setNodeMetrics(m)
  }).catch(() => {})
}, [detail])
```

- [ ] **Step 3: TypeScript 编译检查**

Run: `cd observability-frontend && npx tsc --noEmit -p tsconfig.json`
Expected: 0 errors

- [ ] **Step 4: Commit**

```bash
cd /Users/mssc/Documents/Code/agent/aiops
git add observability-frontend/src/pages/admin/AdminSettings.tsx observability-frontend/src/api/client.ts
git commit -m "feat: 前端节点表新增实时用量列（Metrics API）"
```

---

### Task 5: 构建部署与端到端验证

**Files:**
- 部署：metrics-server helm + aiops helm upgrade
- 验证：kubectl top + curl + playwright

- [ ] **Step 1: 后端测试全绿**

Run: `cd aiops/ai-apm-query-go && go test ./internal/... 2>&1 | tail -10`
Expected: 全部通过

- [ ] **Step 2: 重建 query-api 镜像（如 v1.3.15）**

```bash
cd /Users/mssc/Documents/Code/agent/aiops && IMAGE_TAG=v1.3.15 ./deploy/scripts/build-images.sh query-api
```

- [ ] **Step 3: 重建 frontend 镜像（如 v3.4.9）**

```bash
IMAGE_TAG=v3.4.9 ./deploy/scripts/build-images.sh frontend
```

- [ ] **Step 4: helm upgrade（含 node-exporter k8s SD + RBAC + 新镜像）**

```bash
cd /Users/mssc/Documents/Code/agent/aiops
helm upgrade aiops deploy/helm/aiops --reuse-values \
  --set queryApi.image=query-api:v1.3.15 \
  --set frontend.image=observability-frontend:v3.4.9 \
  -n observability
```

- [ ] **Step 5: 端到端验证**

- `kubectl top nodes` → 有节点用量
- `curl /api/v1/nodes/metrics` → 返回节点实时用量（带 token）
- `curl /api/v1/capacity/instances` → `source: victoriametrics`（VM 抓到 node-exporter）
- `curl /api/v1/capacity/forecast?metric=cpu` → current 有真实值
- playwright：集群详情节点表显示实时用量列

- [ ] **Step 6: 同步 GitHub**

```bash
cd /Users/mssc/Documents/Code/agent/aiops
git add -A -- ':!aiops-platform-review-report.md'
git commit -m "deploy: metrics-server 对接 + node-exporter 容量数据"
git push origin main
```

---

## Self-Review

**Spec coverage:**
- 部分 1（metrics-server 部署）→ Task 1 ✓
- 部分 2（节点实时用量，Metrics API + RBAC + 前端列）→ Task 2 + Task 4 ✓
- 部分 3（node-exporter k8s SD 抓取）→ Task 3 ✓
- 部署验证同步 → Task 5 ✓

**Placeholder scan:** 无 TBD/TODO；各代码步骤含完整实现（parseQuantity/parseNodeMetrics/scrape-config/前端列）。

**Type consistency:**
- `parseQuantity(string) float64`（Task 2 定义），`parseNodeMetrics`/`NodesMetrics` 调用 ✓
- `k8sAPIFn`（Task 2 定义，可注入测试）→ `NodesMetrics` 使用 ✓
- `getNodeMetrics()`（Task 4 client）→ `AdminSettings` 使用 `r.data.nodes` ✓
- `nodeMetrics[r.name]`（Task 4）→ Task 2 接口返回 `node` 字段 ✓
- RBAC `metrics.k8s.io`（Task 2）→ 后端调 `/apis/metrics.k8s.io` 需要 ✓
- VM `victoria-metrics` SA（Task 3）→ deployment `serviceAccountName` ✓
