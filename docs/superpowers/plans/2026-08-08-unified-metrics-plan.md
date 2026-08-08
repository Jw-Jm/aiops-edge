# 统一指标架构收敛 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 三引擎职责收敛——VM=唯一指标库（node-exporter + 服务 RED + 硬件 + 内部 metrics）；CH=trace+log；MySQL=业务/资产。停写 metric_service_red 死代码；Dashboard/告警/拓扑迁 VM PromQL；Monitor 面板有数据。

**Architecture:**
```
node-exporter ─┐
ingest  /metrics ─┼─► vmagent ──remoteWrite──► VM (:8428)  ← 唯一指标入口
orchestrator /metrics ┘
```

**Tech Stack:** Go(ingest/query-api), Helm(vmagent), VictoriaMetrics, React

## Global Constraints

- 三引擎职责收敛，组件零新增（vmagent 是唯一新增部署，用 victoria-metrics:v1.101.0 同族镜像）
- ingest 已暴露 `/metrics`（main.go:174，手写 Prometheus 文本）→ 扩展它暴露服务 RED，不引入 prometheus client
- 保留 trace 落 CH（trace_spans 仍写）；只停写 `metric_service_red`
- Monitor 面板指标名语义承接：`http_requests_total` → `service_requests_total`
- 现有 Go test / 前端 tsc+build 不回归
- 国内源：vmagent 镜像若本地无，用 `docker.1ms.run` 拉取或复用现有 VM 镜像

---

## Task 1: ingest 暴露服务 RED 指标

**Files:**
- Modify: `ai-apm-ingest-go/internal/metrics/metrics.go`
- Modify: `ai-apm-ingest-go/internal/metrics/metrics_test.go`（新增测试）
- Modify: `ai-apm-ingest-go/internal/clickhouse/metrics_writer.go`（RED 计数同时喂 metrics）
- Test: `go test ./internal/metrics/ ./internal/clickhouse/`

**Interfaces:**
- Consumes: ingest trace 聚合结果（现有 RED 聚合逻辑）
- Produces: `/metrics` 输出 `service_requests_total{service}`、`service_errors_total{service,status}`、`service_request_duration_seconds_sum/count{service}`

- [ ] **Step 1: 写失败测试**

在 `internal/metrics/metrics_test.go` 追加：

```go
func TestSnapshotServiceRED(t *testing.T) {
	m := New()
	m.SetServiceRED("frontend", 100, 5, 45.2, 100) // reqs, errs, durSum, durCount
	out := m.Snapshot()
	if !strings.Contains(out, `service_requests_total{service="frontend"} 100`) {
		t.Fatalf("service_requests_total missing:\n%s", out)
	}
	if !strings.Contains(out, `service_errors_total{service="frontend",status="500"} 5`) {
		t.Fatalf("service_errors_total missing")
	}
}
```

- [ ] **Step 2: 运行确认失败**

Run: `cd ai-apm-ingest-go && go test ./internal/metrics/ -run TestSnapshotServiceRED`
Expected: FAIL — `SetServiceRED` 未定义

- [ ] **Step 3: metrics.go 加服务 RED 标签化计数器**

在 `Metrics` 加 `serviceRED map[string]*serviceREDEntry`（含 mutex），`SetServiceRED(service string, reqs, errs int64, durSum float64, durCount int64)` 更新；`Snapshot()` 末尾追加服务 RED 文本（格式见 spec §3.2）。

- [ ] **Step 4: metrics_writer.go 喂 RED**

在写 CH 逻辑中（RED 聚合处），同步调用 `SetServiceRED` 更新 metrics 状态（按 service 累计）。

- [ ] **Step 5: 运行确认通过**

Run: `cd ai-apm-ingest-go && go test ./...`
Expected: PASS

- [ ] **Step 6: 提交**

```bash
git add ai-apm-ingest-go
git commit -m "feat(ingest): 暴露服务 RED 指标（service_requests_total/errors/duration）到 /metrics"
```

---

## Task 2: vmagent 抓 node-exporter + ingest + orchestrator

**Files:**
- Modify: `deploy/helm/aiops/values.yaml`
- Modify: `deploy/helm/aiops/templates/vmagent/configmap.yaml`
- Modify: `deploy/helm/aiops/templates/vmagent/deployment.yaml`
- Test: `helm lint` + `helm template`

**Interfaces:**
- Consumes: node-exporter:9100、ingest:8080/metrics、orchestrator:/metrics
- Produces: `up`（node-exporter/ingest/orchestrator）+ service_requests_total 等进 VM

- [ ] **Step 1: values.yaml 追加 ingest/orchestrator 抓取目标**

在 `vmagent` 配置下追加 `ingestTarget`（默认 `ingest:8080`）、`orchestratorTarget`（`orchestrator:8000`，实施时按实际 svc 端口）。

- [ ] **Step 2: configmap.yaml 扩展 scrape_configs**

增加 ingest + orchestrator job（见 spec §3.3），保留 node-exporter job。

- [ ] **Step 3: helm lint + template 验证**

Run: `cd deploy/helm/aiops && helm lint . && helm template . | grep -A30 vmagent`
Expected: exit 0

- [ ] **Step 4: 提交**

```bash
git add deploy/helm/aiops
git commit -m "feat(deploy): vmagent 抓 node-exporter/ingest/orchestrator 到 VM"
```

---

## Task 3: 部署 vmagent + 验证 Monitor 有数据

**Files:**
- Test: 冒烟
- （vmagent 镜像本地无，需 `docker pull docker.1ms.run/victoriametrics/vmagent:v1.101.0` 后 tag）

- [ ] **Step 1: 拉取并 tag vmagent 镜像**

Run: `docker pull docker.1ms.run/victoriametrics/vmagent:v1.101.0 && docker tag docker.1ms.run/victoriametrics/vmagent:v1.101.0 victoriametrics/vmagent:v1.101.0`
Expected: 镜像就绪

- [ ] **Step 2: helm upgrade 部署 vmagent**

Run: `kubectl -n observability rollout status deploy/vmagent --timeout=90s`
Expected: 1/1 Running

- [ ] **Step 3: 验证 up 指标**

Run: `curl -s 'http://localhost:8428/api/v1/query?query=up' | head -c 300`
Expected: node-exporter/ingest/orchestrator 的 up=1

- [ ] **Step 4: 验证服务 RED 进 VM**

Run: `curl -s 'http://localhost:8428/api/v1/query?query=service_requests_total' | head -c 300`
Expected: 非空（ingest 暴露的 RED 已入库）

- [ ] **Step 5: agent-browser 验证 /monitor**

打开 `http://localhost:30253/monitor?t=$(date +%s)`，确认面板有数据（不再"暂无数据"）。

- [ ] **Step 6: 提交**

```bash
git add deploy/helm/aiops
git commit -m "feat(deploy): VM 采集链路验证通过（Monitor 面板有数据）"
```

---

## Task 4: 停写 metric_service_red + 迁移 Dashboard/告警/拓扑到 VM

**Files:**
- Modify: `ai-apm-ingest-go/internal/clickhouse/metrics_writer.go`（停写 metric_service_red）
- Modify: `ai-apm-query-go/internal/api/dashboard.go`（服务列表/趋势迁 VM）
- Modify: `ai-apm-query-go/internal/api/alerts.go`（告警评估迁 VM）
- Modify: `ai-apm-query-go/internal/api/topology_graph.go`（拓扑边指标迁 VM）
- Modify: `ai-apm-query-go/internal/api/metrics_proxy.go`（VM PromQL 封装）
- Test: `go test ./...`

**Interfaces:**
- Consumes: VM PromQL（`/metrics/query`、`/metrics/query_range`）
- Produces: Dashboard/告警/拓扑从 trace_spans 迁到 VM PromQL

- [ ] **Step 1: metrics_writer.go 停写 metric_service_red**

移除 `metrics_writer.go:234` 的 `INSERT INTO observability.metric_service_red`（保留 service_requests_total 等指标进 VM 的逻辑）。

- [ ] **Step 2: 新增 VM PromQL 查询封装**

`internal/api/metrics_proxy.go` 提供 `queryVM(query)` / `queryRangeVM(query, step)`，调 VM `/api/v1/query`/`query_range`。

- [ ] **Step 3: Dashboard 迁移**

服务列表/趋势/QPS/错误率/延迟 handler 改为查 VM PromQL（`label_values`、`sum(rate(service_requests_total[5m])) by (service)` 等）。

- [ ] **Step 4: 告警迁移**

error_rate/latency_p99/call_count 评估改为 VM PromQL 查询。

- [ ] **Step 5: 拓扑边迁移**

`service_edge_calls_total{source,dest}` 提供拓扑边指标（ingest 暴露），GlobalTopology 改从 VM 读。

- [ ] **Step 6: 运行确认通过**

Run: `cd ai-apm-query-go && go build ./... && go test ./...`
Expected: PASS

- [ ] **Step 7: 冒烟**

- Dashboard 服务列表/趋势有数据（VM）
- 告警 error_rate 正常评估
- 拓扑边显示
- CH metric_service_red 不再增长

- [ ] **Step 8: 提交**

```bash
git add ai-apm-ingest-go ai-apm-query-go
git commit -m "feat(metrics): Dashboard/告警/拓扑迁 VM PromQL，停写 metric_service_red"
```

---

## Task 5: 硬件 gauge 进 VM（附带）

**Files:**
- Modify: `ai-orchestrator/...`（ipmi/snmp 状态暴露为 Prometheus gauge）
- Test: 冒烟

- [ ] **Step 1: orchestrator 暴露硬件 gauge**

在 orchestrator 的 `/metrics` 端点扩展 ipmi/snmp 部件可用性 gauge（如 `node_component_available{host,component} 0/1`）。

- [ ] **Step 2: 验证 vmagent 抓取**

Run: `curl -s 'http://localhost:8428/api/v1/query?query=node_component_available'`
Expected: 非空

- [ ] **Step 3: 提交**

```bash
git add ai-orchestrator
git commit -m "feat(orchestrator): 硬件部件可用性暴露为 Prometheus gauge"
```

---

## Self-Review

**1. Spec coverage:** 覆盖 unified-metrics spec Phase1/2/3（RED 暴露 + 采集 + 停写死代码 + 迁移 + 硬件 gauge）。
**2. Placeholder scan:** 无 TBD/TODO；orchestrator 端口在 Step 标记"实施时按实际确认"。
**3. Type consistency:** `SetServiceRED`/`service_requests_total`/`queryVM` 跨 Task 命名一致。
**4. 回滚:** 若 VM PromQL 与 trace 聚合差异，dashboard 可回退到 trace_spans（保留原 handler 分支）。
