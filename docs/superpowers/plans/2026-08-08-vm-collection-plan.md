# VM 采集链路 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 部署 vmagent 抓取 node-exporter/ipmi-exporter/ingest 的 `/metrics` 到 VictoriaMetrics，让 `/monitor` 面板 PromQL 有数据。

**Architecture:** 新增 vmagent Deployment（Helm），scrape_configs 指向 node-exporter(节点IP:9100)/ipmi-exporter/ingest，remoteWrite 到 `victoria-metrics:8428`。

**Tech Stack:** Helm, vmagent (victoriametrics/vmagent:v1.101.0), VictoriaMetrics

## Global Constraints

- vmagent 镜像用 `victoriametrics/vmagent:v1.101.0`（与现有 VM 版本一致）
- node-exporter 是 hostNetwork DaemonSet → target 用节点 IP:9100（不能 ClusterIP Service）
- remoteWrite URL: `http://victoria-metrics:8428/api/v1/write`
- scrape interval 15s
- 现有测试/构建不回归（前端 tsc+build、Go test）
- 国内源约束：vmagent 镜像若本地无，需用国内 registry 或复用本地已有镜像

---

### Task 1: vmagent Helm 部署 + scrape_configs

**Files:**
- Create: `deploy/helm/aiops/templates/vmagent/configmap.yaml`
- Create: `deploy/helm/aiops/templates/vmagent/deployment.yaml`
- Modify: `deploy/helm/aiops/values.yaml`
- Test: `helm lint` + `helm template`

**Interfaces:**
- Consumes: `victoria-metrics:8428`（remoteWrite 目标）
- Produces: `up` 指标（node-exporter/ipmi-exporter/ingest）

- [ ] **Step 1: values.yaml 加 vmagent 配置**

在 `deploy/helm/aiops/values.yaml` 末尾追加：

```yaml
vmagent:
  enabled: true
  image: victoriametrics/vmagent:v1.101.0
  scrapeInterval: 15s
  remoteWriteUrl: http://victoria-metrics:8428/api/v1/write
```

- [ ] **Step 2: configmap.yaml 定义 scrape_configs**

创建 `deploy/helm/aiops/templates/vmagent/configmap.yaml`：

```yaml
{{- if .Values.vmagent.enabled }}
apiVersion: v1
kind: ConfigMap
metadata:
  name: vmagent-config
  namespace: {{ .Release.Namespace }}
data:
  scrape.yml: |
    global:
      scrape_interval: {{ .Values.vmagent.scrapeInterval }}
    scrape_configs:
      - job_name: node-exporter
        static_configs:
          - targets: ['{{ .Values.nodeExporterTarget }}:9100']
            labels:
              job: node-exporter
      - job_name: ipmi-exporter
        static_configs:
          - targets: ['{{ .Values.ipmiExporterTarget }}:8080']
            labels:
              job: ipmi-exporter
      - job_name: ingest
        static_configs:
          - targets: ['ingest:8080']
            labels:
              job: ingest
{{- end }}
```

- [ ] **Step 3: deployment.yaml 部署 vmagent**

创建 `deploy/helm/aiops/templates/vmagent/deployment.yaml`：

```yaml
{{- if .Values.vmagent.enabled }}
apiVersion: apps/v1
kind: Deployment
metadata:
  name: vmagent
  namespace: {{ .Release.Namespace }}
  labels:
    app.kubernetes.io/name: vmagent
spec:
  replicas: 1
  selector:
    matchLabels:
      app.kubernetes.io/name: vmagent
  template:
    metadata:
      labels:
        app.kubernetes.io/name: vmagent
    spec:
      containers:
        - name: vmagent
          image: {{ .Values.vmagent.image }}
          imagePullPolicy: IfNotPresent
          args:
            - -promscrape.config=/etc/vmagent/scrape.yml
            - -remoteWrite.url={{ .Values.vmagent.remoteWriteUrl }}
            - -httpListenAddr=:8429
          ports:
            - name: http
              containerPort: 8429
          volumeMounts:
            - name: config
              mountPath: /etc/vmagent
      volumes:
        - name: config
          configMap:
            name: vmagent-config
{{- end }}
```

- [ ] **Step 4: values.yaml 加 target 节点地址**

在 `deploy/helm/aiops/values.yaml` 追加 target 占位（实施时用 `kubectl get nodes -o wide` 获取节点 IP 填入）：

```yaml
# node-exporter/ipmi-exporter 为 hostNetwork，需用节点 IP 抓取
nodeExporterTarget: 192.168.194.51   # 实施时替换为实际节点 IP
ipmiExporterTarget: 192.168.194.51   # 实施时替换为实际节点 IP
```

- [ ] **Step 5: helm lint + template 验证**

Run: `cd deploy/helm/aiops && helm lint . && helm template . | grep -A25 vmagent`
Expected: exit 0, vmagent Deployment/ConfigMap 生成

- [ ] **Step 6: 获取实际节点 IP**

Run: `kubectl get nodes -o wide`
Expected: 记录节点 IP，替换 Step 4 的占位值

- [ ] **Step 7: 提交**

```bash
git add deploy/helm/aiops
git commit -m "feat(deploy): vmagent 采集 node-exporter/ipmi-exporter/ingest 到 VM"
```

---

### Task 2: 部署 + 验证 up 指标

**Files:**
- Modify: 无（部署验证）
- Test: 冒烟

**Interfaces:**
- Consumes: Task 1 的 vmagent manifests
- Produces: `up` 指标 + `/monitor` 面板数据

- [ ] **Step 1: 应用部署**

Run: `kubectl -n observability get pods -l app.kubernetes.io/name=vmagent` 确认已存在；若不存在执行 `helm upgrade --install aiops ./deploy/helm/aiops -n observability`

- [ ] **Step 2: 确认 vmagent 运行**

Run: `kubectl -n observability rollout status deploy/vmagent --timeout=90s`
Expected: 1/1 Running

- [ ] **Step 3: 验证 up 指标**

Run: `kubectl exec -n observability deploy/victoria-metrics -- wget -qO- 'localhost:8428/api/v1/query?query=up' 2>/dev/null || curl -s 'http://localhost:8428/api/v1/query?query=up'`
Expected: 返回 node-exporter/ipmi-exporter/ingest 的 up=1

- [ ] **Step 4: 验证 /monitor 面板数据源**

Run: `curl -s 'http://localhost:8428/api/v1/query?query=node_cpu_seconds_total' | head -c 300`
Expected: 非空（node-exporter 指标已入库）

- [ ] **Step 5: 前端验证 /monitor 面板**

用 agent-browser 打开 `http://localhost:30253/monitor?t=$(date +%s)`，确认面板不再显示"暂无数据"。

- [ ] **Step 6: 提交**

```bash
git add deploy/helm/aiops
git commit -m "feat(deploy): VM 采集链路验证通过（监控面板有数据）"
```

---

## Self-Review

**1. Spec coverage:** 覆盖 gap-completion Task 1（vmagent + scrape_configs）+ 监控面板数据。
**2. Placeholder scan:** target 节点 IP 在 Step 4/6 明确为"实施时获取"，非模糊占位。
**3. Type consistency:** 镜像/URL/端口均与既有 VM 部署一致。
