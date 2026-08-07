# Phase A · VictoriaMetrics 接入 + PromQL query_range 端点 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在 query-api 接入 VictoriaMetrics（从零接入时序库），新增通用 PromQL `query_range` 端点，作为 Monitor/Devices/Dashboard 页面复刻的可观测性地基。

**Architecture:** 在 `ai-apm-query-go/internal/api/` 新增 `victoriametrics.go`（含 VM 代理 + PromQL 查询逻辑），在 `Handler` 增加 `vmURL` 字段，main.go 注册 `/api/v1/metrics/query_range` 路由，Helm values 注入 VM Service DNS。

**Tech Stack:** Go 1.24 / net/http / VictoriaMetrics (PromQL HTTP API) / Helm values。

## Global Constraints

- 服务：query-api（Go），入口 `cmd/api/main.go`，逻辑在 `internal/api/handler.go` 等（`package api`，无分层）。
- **VictoriaMetrics 从零接入**：当前仅 VictoriaLogs 在用，VM 未接入。目标 Service DNS：`http://victoria-metrics.observability.svc.cluster.local:8428`。
- 新增端点：`GET /api/v1/metrics/query_range?query=&start=&end=&step=`，代理 VictoriaMetrics `/api/v1/query_range`。
- 配置走 env `VICTORIA_METRICS_URL`（可移植），默认 VM Service DNS。
- 合规红线：不复制 ongrid 代码；VM 端点/字段为 VictoriaMetrics 官方 API 契约（非 ongrid 专属），可自由使用。
- 基线：`github.com/Jw-Jm/aiops-edge` main=`2b94487`，每任务提交。

---

## Task 1: Handler 增加 VM 配置字段 + 构造器

**Files:**
- Modify: `aiops/ai-apm-query-go/internal/api/handler.go:21-34`（Handler struct + NewHandler）

**Interfaces:**
- Consumes: 现有 `Handler` struct（`chHost/chPort/client`）、`NewHandler(chHost, chPort)`。
- Produces: `Handler.vmURL string` 字段；`NewHandler` 增加 `vmURL string` 参数（或经 `SetVMURL` 设置）；后续 Task 2/3 使用 `h.vmURL`。

- [ ] **Step 1: 修改 Handler struct 与 NewHandler**

```go
// handler.go:21-34
type Handler struct {
	chHost string
	chPort int
	client *http.Client
	vmURL  string // VictoriaMetrics base URL, 如 http://victoria-metrics.observability.svc.cluster.local:8428
}

// NewHandler creates a new Handler.
func NewHandler(chHost string, chPort int) *Handler {
	return &Handler{
		chHost: chHost,
		chPort: chPort,
		client: &http.Client{Timeout: 30 * time.Second},
		vmURL:  "http://victoria-metrics.observability.svc.cluster.local:8428",
	}
}

// SetVMURL overrides the VictoriaMetrics URL (from env).
func (h *Handler) SetVMURL(u string) {
	if u != "" {
		h.vmURL = u
	}
}
```

- [ ] **Step 2: 编译验证**

Run: `cd aiops/ai-apm-query-go && go build ./...`
Expected: 编译通过（无报错）。

- [ ] **Step 3: 提交**

```bash
cd /Users/mssc/Documents/Code/agent/aiops
git add ai-apm-query-go/internal/api/handler.go
git commit -m "feat(query-api): add vmURL field + SetVMURL to Handler"
```

---

## Task 2: 新增 victoriametrics.go（PromQL query_range 代理）

**Files:**
- Create: `aiops/ai-apm-query-go/internal/api/victoriametrics.go`
- Test: `aiops/ai-apm-query-go/internal/api/victoriametrics_test.go`

**Interfaces:**
- Consumes: `Handler.vmURL`、`client`（Task 1）。
- Produces: `func (h *Handler) QueryRange(w http.ResponseWriter, r *http.Request)`——代理 `GET /api/v1/metrics/query_range`；`func (h *Handler) healthVictoriaMetrics() (bool, string)`——内部探活。

- [ ] **Step 1: 写失败测试**

```go
// aiops/ai-apm-query-go/internal/api/victoriametrics_test.go
package api

import (
	"net/http/httptest"
	"testing"
)

// 测试 query_range 请求正确拼接到 VM query_range 端点
func TestBuildQueryRangeURL(t *testing.T) {
	h := &Handler{vmURL: "http://vm:8428"}
	got := h.buildQueryRangeURL("sum(rate(x[5m]))", "1710000000", "1710000360", "60")
	want := "http://vm:8428/api/v1/query_range?query=sum%28rate%28x%5B5m%5D%29%29&start=1710000000&end=1710000360&step=60"
	if got != want {
		t.Fatalf("buildQueryRangeURL = %q, want %q", got, want)
	}
}

// 测试缺 query 参数返回错误
func TestQueryRangeMissingQuery(t *testing.T) {
	h := &Handler{}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/metrics/query_range?start=1&end=2&step=60", nil)
	h.QueryRange(rec, req)
	if rec.Code != 400 {
		t.Fatalf("code = %d, want 400 (missing query)", rec.Code)
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `cd aiops/ai-apm-query-go && go test ./internal/api/ -run "TestBuildQueryRangeURL|TestQueryRangeMissingQuery" -v`
Expected: FAIL（`buildQueryRangeURL`/`QueryRange` 未定义，编译失败）。

- [ ] **Step 3: 实现 victoriametrics.go**

```go
package api

import (
	"io"
	"log"
	"net/http"
	"net/url"
)

// buildQueryRangeURL 构造 VM query_range 完整 URL（query/start/end/step 校验并 URL 编码）。
func (h *Handler) buildQueryRangeURL(query, start, end, step string) string {
	u, _ := url.Parse(h.vmURL + "/api/v1/query_range")
	q := u.Query()
	q.Set("query", query)
	q.Set("start", start)
	q.Set("end", end)
	q.Set("step", step)
	u.RawQuery = q.Encode()
	return u.String()
}

// QueryRange 处理 GET /api/v1/metrics/query_range，代理 VictoriaMetrics /api/v1/query_range。
// 参数：query=PromQL, start, end, step（Prometheus 兼容时间格式）。
func (h *Handler) QueryRange(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	query := q.Get("query")
	start := q.Get("start")
	end := q.Get("end")
	step := q.Get("step")
	if query == "" || start == "" || end == "" || step == "" {
		respondError(w, http.StatusBadRequest, "query, start, end, step are required")
		return
	}
	if h.vmURL == "" {
		respondError(w, http.StatusServiceUnavailable, "victoria-metrics not configured")
		return
	}
	target := h.buildQueryRangeURL(query, start, end, step)
	req, err := http.NewRequest(http.MethodGet, target, nil)
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	resp, err := h.client.Do(req)
	if err != nil {
		log.Printf("VM query_range error: %v", err)
		respondError(w, http.StatusBadGateway, "victoria-metrics unavailable: "+err.Error())
		return
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	w.Write(body)
}

// healthVictoriaMetrics 返回 VM 是否可达及版本（供 /system/status 扩展，可选）。
func (h *Handler) healthVictoriaMetrics() (bool, string) {
	resp, err := h.client.Get(h.vmURL + "/health")
	if err != nil {
		return false, err.Error()
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK, ""
}
```

> 说明：`respondError` 已存在于 handler.go，复用。

- [ ] **Step 4: 运行测试确认通过**

Run: `cd aiops/ai-apm-query-go && go test ./internal/api/ -run "TestBuildQueryRangeURL|TestQueryRangeMissingQuery" -v`
Expected: PASS（2 tests）。

- [ ] **Step 5: 提交**

```bash
cd /Users/mssc/Documents/Code/agent/aiops
git add ai-apm-query-go/internal/api/victoriametrics.go ai-apm-query-go/internal/api/victoriametrics_test.go
git commit -m "feat(query-api): add PromQL query_range proxy to VictoriaMetrics"
```

---

## Task 3: main.go 注册路由 + env 注入

**Files:**
- Modify: `aiops/ai-apm-query-go/cmd/api/main.go:16-29, 51`

**Interfaces:**
- Consumes: `handler.SetVMURL`（Task 1）、`handler.QueryRange`（Task 2）。
- Produces: 注册 `GET /api/v1/metrics/query_range`；从 `VICTORIA_METRICS_URL` env 读 VM 地址。

- [ ] **Step 1: main.go 加 env 读取与路由**

```go
// main.go 内 NewHandler 后：
if vmURL := os.Getenv("VICTORIA_METRICS_URL"); vmURL != "" {
	handler.SetVMURL(vmURL)
}
```
```go
// main.go 路由，紧跟 line 51 QueryMetrics 后：
mux.HandleFunc("/api/v1/metrics/query_range", handler.QueryRange)
```

- [ ] **Step 2: 编译 + 冒烟**

Run: `cd aiops/ai-apm-query-go && go build ./...`
Expected: 编译通过。
Run: `cd aiops/ai-apm-query-go && go run ./cmd/api -port 8099 & sleep 2 && curl -s "http://localhost:8099/api/v1/metrics/query_range?query=up&start=1&end=2&step=60" -o /dev/null -w "%{http_code}\n" && kill %1`
Expected: 400（缺 VM 或 VM 不可达，但证明路由注册成功，返回 4xx/5xx 而非 404）。

- [ ] **Step 3: 提交**

```bash
cd /Users/mssc/Documents/Code/agent/aiops
git add ai-apm-query-go/cmd/api/main.go
git commit -m "feat(query-api): register /metrics/query_range route + VICTORIA_METRICS_URL env"
```

---

## Task 4: Helm values + Deployment env 注入 VM 地址

**Files:**
- Modify: `aiops/deploy/helm/aiops/values.yaml`（queryApi 段）
- Modify: `aiops/deploy/helm/aiops/templates/query-api/deployment.yaml`（env）

**Interfaces:**
- Consumes: Task 1-3 的 `VICTORIA_METRICS_URL` env。
- Produces: query-api Deployment 注入 VM Service DNS；可移植（其他环境改 values）。

- [ ] **Step 1: values.yaml queryApi 段加 victoriaMetricsUrl**

```yaml
queryApi:
  # ...
  victoriaMetricsUrl: "http://victoria-metrics.observability.svc.cluster.local:8428"
```

- [ ] **Step 2: deployment.yaml env 加 VICTORIA_METRICS_URL**

```yaml
env:
  - name: VICTORIA_METRICS_URL
    value: {{ .Values.queryApi.victoriaMetricsUrl | quote }}
```

- [ ] **Step 3: 渲染校验**

Run: `cd aiops/deploy/helm/aiops && helm template . --namespace observability | grep VICTORIA_METRICS_URL`
Expected: 输出含 `- name: VICTORIA_METRICS_URL` 与对应值。

- [ ] **Step 4: 提交**

```bash
cd /Users/mssc/Documents/Code/agent/aiops
git add deploy/helm/aiops/values.yaml deploy/helm/aiops/templates/query-api/deployment.yaml
git commit -m "feat(helm): inject VICTORIA_METRICS_URL into query-api"
```

---

## Task 5: 本机部署验证

**Files:**
- 无新代码；构建 + 部署 + 验证。

**Interfaces:**
- Consumes: Task 1-4 全部。

- [ ] **Step 1: 重建 query-api 镜像**

Run: `cd /Users/mssc/Documents/Code/agent/aiops && docker build -t query-api:latest ai-apm-query-go && docker tag query-api:latest docker.io/library/query-api:latest`
Expected: 构建成功（arm64，本机 OrbStack）。

- [ ] **Step 2: 升级部署**

Run: `cd /Users/mssc/Documents/Code/agent/aiops && helm upgrade aiops deploy/helm/aiops --namespace observability --set deepflow.enabled=false --set secrets.jwtSecret="dev-jwt-secret-change-me" --set secrets.internalToken="dev-internal-token" --set secrets.ingestApiKey="dev-ingest-key" --set secrets.clickhousePassword="dev-ch-pass" --set secrets.redisPassword="dev-redis-pass" --set secrets.minioAccessKey="minioadmin" --set secrets.minioSecretKey="minioadmin123" --set secrets.mysqlRootPassword="dev-mysql-pass"`
Expected: deployed（REVISION+1）。

- [ ] **Step 3: 验证路由注册**

Run: `kubectl -n observability exec deploy/query-api -- sh -c 'wget -qO- "http://localhost:8080/api/v1/metrics/query_range?query=up&start=1&end=2&step=60" 2>&1 | head -c 200'`
Expected: 非 404（因 VM 无 up 指标或无数据返回 200/空或 502，但证明路由非 404）。

- [ ] **Step 4: 验证 VM 连通**

Run: `kubectl -n observability exec deploy/query-api -- sh -c 'wget -qO- "http://victoria-metrics.observability.svc.cluster.local:8428/api/v1/query?query=up" 2>&1 | head -c 200'`
Expected: 200（VM 可达，返回 up 指标或空 result）。

- [ ] **Step 5: 提交验证通过（如有修复）**

```bash
cd /Users/mssc/Documents/Code/agent/aiops
git add -A
git commit -m "fix(query-api): deployment verification fixes" || echo "无改动"
```

---

## Task 6: 收尾推送

**Files:**
- 无新代码。

**Interfaces:**
- Consumes: 全部。

- [ ] **Step 1: 推送**

```bash
cd /Users/mssc/Documents/Code/agent/aiops
git push origin main
```
Expected: 推送成功，远程 main 更新。

- [ ] **Step 2: 完成验证**

Run: `git status --short`
Expected: 干净工作树。
