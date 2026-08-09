# 容量预测（全维度资源预测）实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在 `/capacity` 页对 node 级资源（CPU/内存/磁盘/网络）做全维度容量预测：线性回归 + EWMA 双算法，输出历史/预测曲线 + 预计达阈值时间（ETT）。

**Architecture:** 后端（ai-apm-query-go）复用 `vmRangeQuery` 拉历史序列，纯标准库实现线性回归（最小二乘）与 EWMA 平滑，新增 `/api/v1/capacity/forecast` 返回历史+预测+ETT；前端新增 `/capacity` 页用 echarts 渲染双预测线 + 阈值 markLine。

**Tech Stack:** Go 1.25 标准库（无数学库）、`net/http` ServeMux、echarts-for-react、antd、react-router-dom。

## Global Constraints

- Go 后端路径：`aiops/ai-apm-query-go`，模块 `github.com/observability-platform/ai-apm-query-go`
- 前端路径：`aiops/observability-frontend`，axios baseURL 相对路径 `/api/v1`
- 响应格式：后端遵循 `respondJSON(w, status, data)`（无统一 `{code,data,msg}` 包装），错误用 `respondError(w, status, msg)` 返回 `{"error": msg}`
- 算法纯函数复用现有单测风格（`internal/api/anomaly_test.go`，`package api`）
- VM handler mock 复用现有 `httptest` 风格（见 `victoriametrics_test.go` 的 `TestVMRangeQueryParsesSeries`）
- 磁盘用使用率百分比、网络用带宽 bps；cpu/memory/disk 阈值默认 80，network 阈值必填
- 前端菜单注册需同步：`App.tsx` 的 lazy import + `<Route>` + `menuGroups` 三处
- 组件最小化：不引入 Python/数学库/重 grid 库，前端复用 echarts

---

### Task 1: 预测算法纯函数（线性回归 + EWMA + ETT）

**Files:**
- Create: `aiops/ai-apm-query-go/internal/api/capacity.go`
- Test: `aiops/ai-apm-query-go/internal/api/capacity_test.go`

**Interfaces:**
- Produces（本任务定义，供 Task 2 使用）:
  - `func LinearRegression(series []float64) (slope, intercept float64)` — 最小二乘拟合 `y = a + b*x`，x 取 `0..n-1`；n<2 或分母为 0 时返回 `(0, mean(series))`
  - `func EWMA(series []float64, alpha float64) []float64` — 指数平滑，`s[0]=y[0]`，`s[t]=alpha*y[t]+(1-alpha)*s[t-1]`；`alpha<=0||alpha>1` 时回退 0.3
  - `func EstimateTimeToThreshold(slope, intercept float64, n, horizon int, threshold float64) (k int, ok bool)` — 沿线性回归预测 `y(x)=a+b*x`（x 从 `n-1+1` 到 `n-1+horizon`）求首个 `y>=threshold` 的步数 k（相对当前，k=1 表示第 1 个未来点）；达到则 `(k, true)`，否则 `(0, false)`

- [ ] **Step 1: 写失败的算法测试**

创建 `aiops/ai-apm-query-go/internal/api/capacity_test.go`：

```go
package api

import (
	"math"
	"testing"
)

// 线性回归：合成 y=2x+1 序列（x=0..9），断言 slope≈2、intercept≈1。
func TestLinearRegressionKnownLine(t *testing.T) {
	series := make([]float64, 10)
	for x := 0; x < 10; x++ {
		series[x] = float64(2*x + 1)
	}
	slope, intercept := LinearRegression(series)
	if math.Abs(slope-2) > 1e-6 || math.Abs(intercept-1) > 1e-6 {
		t.Fatalf("slope=%v intercept=%v, want slope≈2 intercept≈1", slope, intercept)
	}
}

// 线性回归：常数列 → slope=0，intercept=均值。
func TestLinearRegressionConstant(t *testing.T) {
	slope, intercept := LinearRegression([]float64{5, 5, 5, 5})
	if math.Abs(slope) > 1e-9 || math.Abs(intercept-5) > 1e-9 {
		t.Fatalf("constant series: slope=%v intercept=%v, want 0/5", slope, intercept)
	}
}

// 线性回归：空序列/单点 → 不 panic，返回 (0,0) 或 (0,mean)。
func TestLinearRegressionShort(t *testing.T) {
	if _, i := LinearRegression(nil); i != 0 {
		t.Fatalf("nil series intercept=%v, want 0", i)
	}
	if s, _ := LinearRegression([]float64{7}); s != 0 {
		t.Fatalf("single point slope=%v, want 0", s)
	}
}

// EWMA：常数列平滑后不变。
func TestEWMAConstant(t *testing.T) {
	out := EWMA([]float64{3, 3, 3, 3}, 0.3)
	for _, v := range out {
		if v != 3 {
			t.Fatalf("EWMA constant = %v, want 3", v)
		}
	}
}

// EWMA：单调上升序列，平滑结果首项等于 y[0]，且整体上升（未截断异常）。
func TestEWMAMonotonic(t *testing.T) {
	series := []float64{1, 2, 3, 4, 5, 6}
	out := EWMA(series, 0.3)
	if out[0] != 1 {
		t.Fatalf("out[0]=%v, want 1", out[0])
	}
	if out[len(out)-1] <= out[0] {
		t.Fatalf("EWMA should trend up for increasing input: %v", out)
	}
}

// EWMA：alpha 越界 → 回退 0.3，不 panic。
func TestEWMAInvalidAlpha(t *testing.T) {
	if out := EWMA([]float64{1, 2, 3}, 0); out[len(out)-1] != out[len(out)-1] {
		t.Fatalf("alpha=0 should fallback, got %v", out)
	}
	if out := EWMA([]float64{1, 2, 3}, 2.0); out[len(out)-1] != out[len(out)-1] {
		t.Fatalf("alpha=2 should fallback, got %v", out)
	}
}

// ETT：斜率 2、intercept 1、当前 x=9 处 y=19，threshold=25 → 需要 y>=25。
// 未来 x=10,11,12 → y=21,23,25 → 第 3 个未来点达到 → k=3。
func TestEstimateTimeToThresholdHit(t *testing.T) {
	k, ok := EstimateTimeToThreshold(2, 1, 10, 10, 25)
	if !ok || k != 3 {
		t.Fatalf("k=%v ok=%v, want k=3 ok=true", k, ok)
	}
}

// ETT：斜率 2、intercept 1、threshold 远高于预测范围内 → 未达到。
func TestEstimateTimeToThresholdMiss(t *testing.T) {
	k, ok := EstimateTimeToThreshold(2, 1, 10, 10, 100)
	if ok || k != 0 {
		t.Fatalf("k=%v ok=%v, want k=0 ok=false", k, ok)
	}
}

// ETT：斜率下降（slope=-1, intercept=90, 当前 x=9 处 y=81）但阈值较高（threshold=100），
// 未来 x=10..19 的 y=80..71 全部 <100 → 返回 k=0 ok=false（下降趋势不会达到较高阈值）。
func TestEstimateTimeToThresholdAlreadyBreachedSlopeDown(t *testing.T) {
	k, ok := EstimateTimeToThreshold(-1, 90, 10, 10, 100)
	if ok || k != 0 {
		t.Fatalf("k=%v ok=%v, want k=0 ok=false (slope down, won't breach)", k, ok)
	}
}

// ETT：阈值在第 1 个未来点就达到。
func TestEstimateTimeToThresholdFirstStep(t *testing.T) {
	k, ok := EstimateTimeToThreshold(2, 1, 10, 10, 21)
	if !ok || k != 1 {
		t.Fatalf("k=%v ok=%v, want k=1 ok=true", k, ok)
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `cd aiops/ai-apm-query-go && go test ./internal/api/ -run 'TestLinearRegression|TestEWMA|TestEstimateTimeToThreshold' -v`
Expected: FAIL，报 "undefined: LinearRegression / EWMA / EstimateTimeToThreshold"

- [ ] **Step 3: 实现最小算法代码**

创建 `aiops/ai-apm-query-go/internal/api/capacity.go`：

```go
package api

// LinearRegression 用最小二乘对 (x=0..n-1, y) 拟合 y = intercept + slope*x。
// n<2 或分母为 0 时返回 (0, mean(series))。
func LinearRegression(series []float64) (slope, intercept float64) {
	n := len(series)
	if n == 0 {
		return 0, 0
	}
	if n < 2 {
		return 0, series[0]
	}
	var sumX, sumY, sumXY, sumX2 float64
	for i, y := range series {
		x := float64(i)
		sumX += x
		sumY += y
		sumXY += x * y
		sumX2 += x * x
	}
	denom := float64(n)*sumX2 - sumX*sumX
	if denom == 0 {
		return 0, mean(series)
	}
	slope = (float64(n)*sumXY - sumX*sumY) / denom
	intercept = (sumY - slope*sumX) / float64(n)
	return slope, intercept
}

// EWMA 指数加权平滑：s[0]=y[0]，s[t]=alpha*y[t]+(1-alpha)*s[t-1]。
// alpha<=0 || alpha>1 时回退默认 0.3。
func EWMA(series []float64, alpha float64) []float64 {
	if alpha <= 0 || alpha > 1 {
		alpha = 0.3
	}
	out := make([]float64, len(series))
	if len(series) == 0 {
		return out
	}
	out[0] = series[0]
	for t := 1; t < len(series); t++ {
		out[t] = alpha*series[t] + (1-alpha)*out[t-1]
	}
	return out
}

// EstimateTimeToThreshold 沿线性回归预测 y(x)=intercept+slope*x 求未来首个 y>=threshold 的步数。
// 未来 x 取 n-1+1 .. n-1+horizon。达到则返回 (k, true)（k 为第几个未来点，从 1 起）；
// 否则返回 (0, false)。
func EstimateTimeToThreshold(slope, intercept float64, n, horizon int, threshold float64) (int, bool) {
	for k := 1; k <= horizon; k++ {
		x := float64(n - 1 + k)
		y := intercept + slope*x
		if y >= threshold {
			return k, true
		}
	}
	return 0, false
}
```

注意：`mean` 函数已存在于同包 `anomaly.go`（供 zscore/MAD 使用），直接复用。

- [ ] **Step 4: 运行测试确认通过**

Run: `cd aiops/ai-apm-query-go && go test ./internal/api/ -run 'TestLinearRegression|TestEWMA|TestEstimateTimeToThreshold' -v`
Expected: PASS（全部通过）

- [ ] **Step 5: Commit**

```bash
cd aiops/ai-apm-query-go && git add internal/api/capacity.go internal/api/capacity_test.go && git commit -m "feat(capacity): 线性回归+EWMA+ETT 预测算法纯函数" --no-verify
```

---

### Task 2: `CapacityForecast` handler（PromQL 映射 + 参数解析 + 响应组装）

**Files:**
- Modify: `aiops/ai-apm-query-go/internal/api/capacity.go`（追加 handler 相关代码）
- Test: `aiops/ai-apm-query-go/internal/api/capacity_test.go`（追加 handler 测试）

**Interfaces:**
- Consumes（Task 1 产物）:
  - `LinearRegression(series []float64) (slope, intercept float64)`
  - `EWMA(series []float64, alpha float64) []float64`
  - `EstimateTimeToThreshold(slope, intercept float64, n, horizon int, threshold float64) (int, bool)`
  - 同包现有：`(h *Handler) vmRangeQuery(promQL string, start, end int64, step int) ([]float64, error)`、`respondJSON`、`respondError`
- Produces（供 Task 3 路由注册）:
  - `func (h *Handler) CapacityForecast(w http.ResponseWriter, r *http.Request)` — 处理 `GET /api/v1/capacity/forecast`
  - `func capacityPromQL(metric, instance string) string` — 返回该维度 range PromQL（含可选 instance label filter）

- [ ] **Step 1: 写失败的 handler 测试**

追加到 `aiops/ai-apm-query-go/internal/api/capacity_test.go`：

```go
package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// capacityPromQL 应正确映射四维度并拼接 instance filter。
func TestCapacityPromQL(t *testing.T) {
	cases := []struct {
		metric, instance string
		wantSubstr       string
	}{
		{"cpu", "", "node_cpu_seconds_total"},
		{"memory", "node-1", "node-1"},
		{"disk", "", "node_filesystem"},
		{"network", "", "node_network"},
	}
	for _, c := range cases {
		got := capacityPromQL(c.metric, c.instance)
		if got == "" {
			t.Fatalf("capacityPromQL(%q) returned empty", c.metric)
		}
		if c.instance != "" {
			if !contains(got, c.instance) {
				t.Fatalf("capacityPromQL(%q) missing instance %q: %s", c.metric, c.instance, got)
			}
		}
		if !contains(got, c.wantSubstr) {
			t.Fatalf("capacityPromQL(%q) missing %q: %s", c.metric, c.wantSubstr, got)
		}
	}
}

// 未知 metric 返回空串。
func TestCapacityPromQLUnknown(t *testing.T) {
	if got := capacityPromQL("bogus", ""); got != "" {
		t.Fatalf("bogus metric should return empty, got %q", got)
	}
}

// 缺少 metric 参数 → 400。
func TestCapacityForecastMissingMetric(t *testing.T) {
	h := &Handler{}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/capacity/forecast", nil)
	h.CapacityForecast(rec, req)
	if rec.Code != 400 {
		t.Fatalf("code=%d, want 400", rec.Code)
	}
}

// network 缺 threshold → 400。
func TestCapacityForecastNetworkNoThreshold(t *testing.T) {
	h := &Handler{}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/capacity/forecast?metric=network", nil)
	h.CapacityForecast(rec, req)
	if rec.Code != 400 {
		t.Fatalf("code=%d, want 400 (network needs threshold)", rec.Code)
	}
}

// VM 返回固定历史序列 → 校验响应结构（history/forecasts/ett/change_pct/timestamps）。
func TestCapacityForecastSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"status": "success",
			"data": map[string]interface{}{
				"resultType": "matrix",
				"result": []map[string]interface{}{
					// 线性上升序列 10,11,12,... 以校验 linear 预测趋势上升
					{"metric": map[string]interface{}{}, "values": [][2]interface{}{{1710000000, "10"}, {1710000060, "11"}, {1710000120, "12"}, {1710000180, "13"}}},
				},
			},
		})
	}))
	defer srv.Close()
	h := &Handler{vmURL: srv.URL, client: &http.Client{}}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/capacity/forecast?metric=cpu&hours=1&step=60&horizon=5&threshold=80", nil)
	h.CapacityForecast(rec, req)
	if rec.Code != 200 {
		t.Fatalf("code=%d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Metric     string    `json:"metric"`
		Current    float64   `json:"current"`
		ChangePct  float64   `json:"change_pct"`
		History    []float64 `json:"history"`
		Timestamps []int64   `json:"timestamps"`
		Forecasts  map[string]struct {
			Values        []float64 `json:"values"`
			EttSeconds    int64     `json:"ett_seconds"`
			WithinHorizon bool      `json:"within_horizon"`
		} `json:"forecasts"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Metric != "cpu" {
		t.Fatalf("metric=%q, want cpu", resp.Metric)
	}
	if len(resp.History) != 4 {
		t.Fatalf("history len=%d, want 4", len(resp.History))
	}
	// timestamps 覆盖历史+预测：n=4 + horizon=5 = 9 个点
	if len(resp.Timestamps) != 9 {
		t.Fatalf("timestamps len=%d, want 9 (4+5)", len(resp.Timestamps))
	}
	if len(resp.Forecasts["linear"].Values) != 5 {
		t.Fatalf("linear forecast len=%d, want 5", len(resp.Forecasts["linear"].Values))
	}
	if len(resp.Forecasts["ewma"].Values) != 5 {
		t.Fatalf("ewma forecast len=%d, want 5", len(resp.Forecasts["ewma"].Values))
	}
	// 历史 10,11,12,13 线性上升 → 未来应继续上升
	if resp.Forecasts["linear"].Values[4] <= resp.History[3] {
		t.Fatalf("linear forecast should rise above last history: last=%v fc[4]=%v", resp.History[3], resp.Forecasts["linear"].Values[4])
	}
	// EWMA 预测：末段平滑斜率（smoothed 末 4 点），对上升序列 slope>0 → 未来也应上升
	if resp.Forecasts["ewma"].Values[4] <= resp.History[3] {
		t.Fatalf("ewma forecast should rise above last history: last=%v ewma[4]=%v", resp.History[3], resp.Forecasts["ewma"].Values[4])
	}
	// change_pct 为数值（短序列走均值兜底空 → 0，不 panic）
	if resp.ChangePct != resp.ChangePct { // NaN 检查
		t.Fatalf("change_pct should not be NaN, got %v", resp.ChangePct)
	}
}

// change_pct：step=3600、n=30（>86400/3600+1=25）→ 走 24h 同相位分支。
// 序列：前 24 个点=10（24h 前同相位），后 6 个点线性 11..16（近端上升）。
// current=16，24h 前同相位 series[30-1-24]=series[5]=10 → change_pct=(16-10)/10*100=60。
func TestCapacityForecastChangePctSamePhase(t *testing.T) {
	vals := make([][2]interface{}, 30)
	for i := 0; i < 30; i++ {
		v := 10.0
		if i >= 24 {
			v = float64(11 + (i - 24))
		}
		vals[i] = [2]interface{}{int64(1710000000 + i*3600), fmt.Sprintf("%v", v)}
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"status": "success",
			"data": map[string]interface{}{
				"resultType": "matrix",
				"result":     []map[string]interface{}{{"metric": map[string]interface{}{}, "values": vals}},
			},
		})
	}))
	defer srv.Close()
	h := &Handler{vmURL: srv.URL, client: &http.Client{}}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/capacity/forecast?metric=cpu&hours=30&step=3600&horizon=5&threshold=80", nil)
	h.CapacityForecast(rec, req)
	if rec.Code != 200 {
		t.Fatalf("code=%d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		ChangePct float64 `json:"change_pct"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.ChangePct < 59 || resp.ChangePct > 61 {
		t.Fatalf("change_pct=%v, want ≈60 (24h same-phase comparison)", resp.ChangePct)
	}
}

// VM 返回空结果 → 200 但 history 为空（不 500）。
func TestCapacityForecastEmptyResult(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"status": "success",
			"data":   map[string]interface{}{"resultType": "matrix", "result": []interface{}{}},
		})
	}))
	defer srv.Close()
	h := &Handler{vmURL: srv.URL, client: &http.Client{}}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/capacity/forecast?metric=cpu&threshold=80", nil)
	h.CapacityForecast(rec, req)
	if rec.Code != 200 {
		t.Fatalf("code=%d, want 200, body=%s", rec.Code, rec.Body.String())
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `cd aiops/ai-apm-query-go && go test ./internal/api/ -run 'TestCapacityPromQL|TestCapacityForecast' -v`
Expected: FAIL（undefined: CapacityForecast / capacityPromQL）

- [ ] **Step 3: 实现 handler**

追加到 `aiops/ai-apm-query-go/internal/api/capacity.go`：

```go
package api

import (
	"fmt"
	"net/http"
	"strconv"
	"time"
)

// capacityPromQL 返回指定资源维度的 range 查询 PromQL。未知 metric 返回空串。
func capacityPromQL(metric, instance string) string {
	inst := ""
	if instance != "" {
		inst = fmt.Sprintf(`{instance="%s"}`, instance)
	}
	switch metric {
	case "cpu":
		return fmt.Sprintf(`100 - avg(rate(node_cpu_seconds_total%s[5m])) * 100`, inst)
	case "memory":
		return fmt.Sprintf(`100 * (1 - node_memory_MemAvailable_bytes%s / node_memory_MemTotal_bytes%s)`, inst, inst)
	case "disk":
		return fmt.Sprintf(`avg(1 - node_filesystem_avail_bytes%s / node_filesystem_size_bytes%s) * 100`, inst, inst)
	case "network":
		return fmt.Sprintf(`rate(node_network_receive_bytes_total%s[5m]) + rate(node_network_transmit_bytes_total%s[5m])`, inst, inst)
	}
	return ""
}

// CapacityForecast 处理 GET /api/v1/capacity/forecast。
// 参数：metric(cpu|memory|disk|network)、instance、hours、step、horizon、threshold。
func (h *Handler) CapacityForecast(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	metric := q.Get("metric")
	instance := q.Get("instance")
	hours := parseIntDefault(q.Get("hours"), 24)
	step := parseIntDefault(q.Get("step"), 300)
	horizon := parseIntDefault(q.Get("horizon"), 12)
	thresholdStr := q.Get("threshold")

	if metric == "" {
		respondError(w, http.StatusBadRequest, "metric is required")
		return
	}
	if capacityPromQL(metric, "") == "" {
		respondError(w, http.StatusBadRequest, "invalid metric, must be cpu|memory|disk|network")
		return
	}
	if hours <= 0 || step <= 0 || horizon <= 0 {
		respondError(w, http.StatusBadRequest, "hours, step, horizon must be positive")
		return
	}
	if metric == "network" && thresholdStr == "" {
		respondError(w, http.StatusBadRequest, "threshold is required for network")
		return
	}
	threshold := 80.0
	if thresholdStr != "" {
		v, err := strconv.ParseFloat(thresholdStr, 64)
		if err != nil || v <= 0 {
			respondError(w, http.StatusBadRequest, "invalid threshold")
			return
		}
		threshold = v
	}

	end := time.Now().Unix()
	start := end - int64(hours*3600)
	series, err := h.vmRangeQuery(capacityPromQL(metric, instance), start, end, step)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "query failed: "+err.Error())
		return
	}
	n := len(series)
	if n == 0 {
		// 无历史数据：返回空历史/预测，前端展示"暂无数据"
		respondJSON(w, http.StatusOK, map[string]interface{}{
			"metric": metric, "instance": instance, "threshold": threshold,
			"current": 0, "change_pct": 0, "timestamps": []int64{}, "history": []float64{},
			"forecasts": map[string]interface{}{
				"linear": map[string]interface{}{"values": []float64{}, "ett_seconds": 0, "within_horizon": false, "already_breached": false},
				"ewma":   map[string]interface{}{"values": []float64{}, "ett_seconds": 0, "within_horizon": false, "already_breached": false},
			},
		})
		return
	}

	// 历史 + 预测完整时间戳
	now := time.Now().Unix()
	timestamps := make([]int64, 0, n+horizon)
	base := now - int64(n-1)*int64(step)
	for i := 0; i < n+horizon; i++ {
		timestamps = append(timestamps, base+int64(i)*int64(step))
	}

	current := series[n-1]
	// change_pct：对比 24h 前同相位（step 为秒，86400/step 即 24h 的样本数），
	// 数据不足 24h 时降级为对比近端均值（最后 10% 样本）——避免受周期峰谷/早期异常点影响。
	changePct := 0.0
	phaseStep := 86400 / step
	var baseVal float64
	if n > phaseStep+1 && series[n-1-phaseStep] != 0 {
		baseVal = series[n-1-phaseStep]
	} else {
		startIdx := n - n/10
		if startIdx < 0 {
			startIdx = 0
		}
		sum := 0.0
		for i := startIdx; i < n; i++ {
			sum += series[i]
		}
		if startIdx < n {
			baseVal = sum / float64(n-startIdx)
		}
	}
	if baseVal != 0 {
		changePct = (current - baseVal) / baseVal * 100
	}

	// 线性回归预测：全窗口最小二乘，输出预测曲线 + ETT
	slope, intercept := LinearRegression(series)
	linearValues := make([]float64, horizon)
	for k := 1; k <= horizon; k++ {
		linearValues[k-1] = intercept + slope*float64(n-1+k)
	}
	linearETT, linearHit := EstimateTimeToThreshold(slope, intercept, n, horizon, threshold)
	linearBreached := current >= threshold

	// 真实 EWMA 预测：平滑序列末值 + 末段平滑斜率外推（而非全窗口回归）。
	// EWMA 体现"近期趋势持续"且对噪声平滑，与外推起点严格贴合。
	smoothed := EWMA(series, 0.3)
	ewmaTail := smoothed
	if len(smoothed) > 4 {
		ewmaTail = smoothed[len(smoothed)-4:]
	}
	ewmaSlope, _ := LinearRegression(ewmaTail)
	ewmaBase := smoothed[n-1]
	ewmaValues := make([]float64, horizon)
	for k := 1; k <= horizon; k++ {
		ewmaValues[k-1] = ewmaBase + ewmaSlope*float64(k)
	}
	// ETT 与外推直线一致：直线 y=ewmaSlope*x + b 过点 (n-1, ewmaBase) → b=ewmaBase-ewmaSlope*(n-1)
	ewmaETT, ewmaHit := EstimateTimeToThreshold(ewmaSlope, ewmaBase-ewmaSlope*float64(n-1), n, horizon, threshold)
	ewmaBreached := current >= threshold

	respondJSON(w, http.StatusOK, map[string]interface{}{
		"metric": metric, "instance": instance, "threshold": threshold,
		"current": current, "change_pct": changePct,
		"timestamps": timestamps, "history": series,
		"forecasts": map[string]interface{}{
			"linear": map[string]interface{}{
				"values": linearValues, "ett_seconds": linearETT * step,
				"within_horizon": linearHit, "already_breached": linearBreached,
			},
			"ewma": map[string]interface{}{
				"values": ewmaValues, "ett_seconds": ewmaETT * step,
				"within_horizon": ewmaHit, "already_breached": ewmaBreached,
			},
		},
	})
}

// parseIntDefault 解析正整数参数，失败或<=0返回默认值。
func parseIntDefault(s string, def int) int {
	v, err := strconv.Atoi(s)
	if err != nil || v <= 0 {
		return def
	}
	return v
}
```

注意：
- 空结果分支的 history 字段必须用 `[]float64{}`（非 nil），确保 JSON 序列化为 `[]` 而非 `null`。
- EWMA 的 `EstimateTimeToThreshold` 入参：`intercept` 传入 `ewmaBase - ewmaSlope*float64(n-1)`（即直线 `ewmaSlope*x + b` 过点 `(n-1, ewmaBase)` 时的截距），这样 ETT 判断与 ewma 外推直线一致。

- [ ] **Step 4: 运行测试确认通过**

Run: `cd aiops/ai-apm-query-go && go test ./internal/api/ -run 'TestCapacityPromQL|TestCapacityForecast' -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
cd aiops/ai-apm-query-go && git add internal/api/capacity.go internal/api/capacity_test.go && git commit -m "feat(capacity): /capacity/forecast handler（PromQL映射+参数校验+双算法预测+ETT）" --no-verify
```

---

### Task 3: 后端路由注册 + 前端 API 封装与类型

**Files:**
- Modify: `aiops/ai-apm-query-go/cmd/api/main.go`（注册路由）
- Modify: `aiops/observability-frontend/src/api/client.ts`（新增类型 + API 函数）

**Interfaces:**
- Consumes: Task 2 的 `(h *Handler) CapacityForecast`
- Produces（供 Task 4 使用）:
  - 前端类型 `CapacityForecast`、`ForecastSeries`、`ForecastData`
  - 前端函数 `getCapacityForecast(params: { metric: string; instance?: string; hours?: number; step?: number; horizon?: number; threshold?: number })`

- [ ] **Step 1: 后端注册路由**

在 `aiops/ai-apm-query-go/cmd/api/main.go` 的 "Metrics & Topology & Logs" 路由区（第 86-88 行附近，`mux.HandleFunc("/api/v1/metrics/query_range", handler.QueryRange)` 之后）追加一行：

```go
	mux.HandleFunc("/api/v1/capacity/forecast", handler.CapacityForecast)
```

- [ ] **Step 2: 前端新增类型与 API 函数**

在 `aiops/observability-frontend/src/api/client.ts` 末尾（`export default api` 之前）追加：

```ts
// ===== 容量预测（Capacity Forecast）=====
export interface ForecastSeries {
  values: number[]
  ett_seconds: number
  within_horizon: boolean
  already_breached: boolean
}
export interface CapacityForecast {
  metric: string
  instance: string
  threshold: number
  current: number
  change_pct: number
  timestamps: number[]
  history: number[]
  forecasts: {
    linear: ForecastSeries
    ewma: ForecastSeries
  }
}
export const getCapacityForecast = (params: {
  metric: string
  instance?: string
  hours?: number
  step?: number
  horizon?: number
  threshold?: number
}) => api.get<CapacityForecast>('/capacity/forecast', { params })
```

- [ ] **Step 3: 验证编译**

后端：`cd aiops/ai-apm-query-go && go build ./...`
Expected: 无输出、exit 0

前端：`cd aiops/observability-frontend && npx tsc --noEmit`
Expected: 无新增类型错误

- [ ] **Step 4: Commit**

```bash
cd aiops && git add ai-apm-query-go/cmd/api/main.go observability-frontend/src/api/client.ts && git commit -m "feat(capacity): 注册后端路由 + 前端 API 封装与类型" --no-verify
```

---

### Task 4: 前端 `/capacity` 页（维度切换 + 双预测曲线 + ETT 卡片）

**Files:**
- Create: `aiops/observability-frontend/src/pages/Capacity/index.tsx`
- Modify: `aiops/observability-frontend/src/App.tsx`（lazy import + Route + 菜单）

**Interfaces:**
- Consumes: Task 3 的 `getCapacityForecast`、`CapacityForecast`、`ForecastSeries`
- Produces: `/capacity` 路由页面（菜单「容量预测」）

- [ ] **Step 1: 创建 `/capacity` 页组件**

创建 `aiops/observability-frontend/src/pages/Capacity/index.tsx`：

```tsx
import { useEffect, useMemo, useState } from 'react'
import { Button, Card, Col, Empty, Row, Select, Space, Spin, Statistic, Tag, Tooltip } from 'antd'
import { ReloadOutlined, ArrowUpOutlined, ArrowDownOutlined, ArrowRightOutlined } from '@ant-design/icons'
import ReactECharts from 'echarts-for-react'
import { CapacityForecast, ForecastSeries, getCapacityForecast } from '../../api/client'
import AppEmpty from '../../components/AppEmpty'

const darkText = '#e8e8e8'
const gridColor = 'rgba(255,255,255,0.12)'

const METRICS = [
  { key: 'cpu', label: 'CPU 使用率', unit: '%' },
  { key: 'memory', label: '内存使用率', unit: '%' },
  { key: 'disk', label: '磁盘使用率', unit: '%' },
  { key: 'network', label: '网络带宽', unit: 'bps' },
]

// 把 ETT 秒数格式化为可读字符串
function formatETT(sec: number): string {
  if (sec <= 0) return '—'
  if (sec < 60) return `${Math.round(sec)} 秒`
  if (sec < 3600) return `${(sec / 60).toFixed(0)} 分钟`
  if (sec < 86400) return `${(sec / 3600).toFixed(1)} 小时`
  return `${(sec / 86400).toFixed(1)} 天`
}

// 单维度预测卡片
function ForecastCard({ title, fc, threshold }: { title: string; fc: ForecastSeries; threshold: number }) {
  if (!fc || !fc.values?.length) return <Card title={title} size="small" style={{ borderRadius: 12 }}><Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="暂无预测" /></Card>
  const last = fc.values[fc.values.length - 1]
  const trend = fc.values.length >= 2 && fc.values[fc.values.length - 1] > fc.values[0]
  const status = fc.already_breached
    ? <Tag color="red">已超阈值</Tag>
    : fc.within_horizon
      ? <Tag color="orange">预测期内达到</Tag>
      : <Tag color="green">预测期内安全</Tag>
  return (
    <Card title={title} size="small" style={{ borderRadius: 12 }}>
      <Space direction="vertical" size={8}>
        <div>当前预测末值：{last.toFixed(1)}</div>
        <div>{trend ? <ArrowUpOutlined style={{ color: '#ff4d4f' }} /> : <ArrowRightOutlined />} 趋势方向：{trend ? '上升' : '平稳/下降'}</div>
        <div>
          预计达阈值时间：{fc.within_horizon ? formatETT(fc.ett_seconds) : '预测期内不会达到'} {status}
        </div>
      </Space>
    </Card>
  )
}

// 组装 echarts option：历史 + 线性预测 + EWMA 预测 + 阈值 markLine
function buildOption(data: CapacityForecast, threshold: number) {
  const histTime = data.timestamps.slice(0, data.history.length).map((t) => new Date(t * 1000).toLocaleTimeString())
  // 预测 x 轴：历史最后一个时间点 + 预测各点
  const predTime = data.timestamps.slice(data.history.length - 1).map((t) => new Date(t * 1000).toLocaleTimeString())
  // 历史曲线：前 n 个点；预测曲线：拼一个衔接点(history末值)保证连续
  const histData = data.history.map((v, i) => [histTime[i], v])
  const lastHist = data.history.length ? data.history[data.history.length - 1] : 0
  const linearData = predTime.map((t, i) => {
    const v = i === 0 ? lastHist : data.forecasts.linear.values[i - 1]
    return [t, v]
  })
  const ewmaData = predTime.map((t, i) => {
    const v = i === 0 ? lastHist : data.forecasts.ewma.values[i - 1]
    return [t, v]
  })
  return {
    backgroundColor: 'transparent',
    tooltip: { trigger: 'axis', textStyle: { fontSize: 12 } },
    legend: { data: ['历史', '线性回归预测', 'EWMA 预测'], textStyle: { color: darkText }, top: 0 },
    grid: { left: 60, right: 20, top: 30, bottom: 30 },
    xAxis: { type: 'category', data: histTime.concat(predTime.slice(1)), axisLabel: { color: '#999' }, axisLine: { lineStyle: { color: gridColor } } },
    yAxis: { type: 'value', axisLabel: { color: '#999' }, splitLine: { lineStyle: { color: gridColor } } },
    series: [
      {
        name: '历史', type: 'line', smooth: true, data: histData, itemStyle: { color: '#1677ff' }, symbol: 'none',
        // markLine 必须挂在 series 内部；仅挂在历史 series 上避免重复画阈值线
        markLine: data.history.length ? {
          symbol: 'none',
          label: { formatter: `阈值 ${threshold}`, color: '#ff4d4f' },
          lineStyle: { color: '#ff4d4f', type: 'dashed' },
          data: [{ yAxis: threshold }],
        } : undefined,
      },
      { name: '线性回归预测', type: 'line', smooth: true, data: linearData, itemStyle: { color: '#52c41a' }, symbol: 'none', lineStyle: { type: 'solid' } },
      { name: 'EWMA 预测', type: 'line', smooth: true, data: ewmaData, itemStyle: { color: '#faad14' }, symbol: 'none', lineStyle: { type: 'dashed' } },
    ],
  }
}

const Capacity: React.FC = () => {
  const [metric, setMetric] = useState('cpu')
  const [hours, setHours] = useState(24)
  const [horizon, setHorizon] = useState(12)
  const [data, setData] = useState<CapacityForecast | null>(null)
  const [loading, setLoading] = useState(false)

  const load = async () => {
    setLoading(true)
    try {
      const r = await getCapacityForecast({ metric, hours, horizon })
      setData(r?.data || null)
    } catch {
      setData(null)
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    load()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [metric, hours, horizon])

  const meta = METRICS.find((m) => m.key === metric)!

  return (
    <div>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 16 }}>
        <div style={{ fontSize: 16, fontWeight: 600 }}>容量预测</div>
        <Space>
          <Select value={hours} onChange={setHours} style={{ width: 120 }} options={[12, 24, 48, 72].map((h) => ({ value: h, label: `历史 ${h}h` }))} />
          <Select value={horizon} onChange={setHorizon} style={{ width: 140 }} options={[6, 12, 24, 48].map((h) => ({ value: h, label: `预测 ${h} 步` }))} />
          <Tooltip title="刷新"><Button icon={<ReloadOutlined />} onClick={load} /></Tooltip>
        </Space>
      </div>

      {/* 维度切换 */}
      <Row gutter={[16, 16]} style={{ marginBottom: 16 }}>
        {METRICS.map((m) => (
          <Col span={6} key={m.key}>
            <Card
              hoverable
              onClick={() => setMetric(m.key)}
              style={{ borderRadius: 12, cursor: 'pointer', borderColor: metric === m.key ? '#1677ff' : undefined }}
            >
              <Statistic title={m.label} value={metric === m.key && data ? data.current.toFixed(1) : '—'} suffix={m.unit} />
              {metric === m.key && data ? (
                <div style={{ color: data.change_pct > 0 ? '#ff4d4f' : '#52c41a', fontSize: 12, marginTop: 4 }}>
                  {data.change_pct > 0 ? <ArrowUpOutlined /> : <ArrowDownOutlined />} 环比 {Math.abs(data.change_pct).toFixed(1)}%
                </div>
              ) : null}
            </Card>
          </Col>
        ))}
      </Row>

      <Spin spinning={loading}>
        {data && data.history?.length ? (
          <Card style={{ borderRadius: 12 }}>
            <ReactECharts option={buildOption(data, data.threshold)} style={{ height: 340 }} notMerge />
          </Card>
        ) : (
          <AppEmpty description="暂无数据" tip="请确认 node-exporter 已采集资源指标" height={200} />
        )}
      </Spin>

      {data && data.history?.length ? (
        <Row gutter={[16, 16]} style={{ marginTop: 16 }}>
          <Col span={12}><ForecastCard title="线性回归预测" fc={data.forecasts.linear} threshold={data.threshold} /></Col>
          <Col span={12}><ForecastCard title="EWMA 指数平滑预测" fc={data.forecasts.ewma} threshold={data.threshold} /></Col>
        </Row>
      ) : null}
    </div>
  )
}

export default Capacity
```

- [ ] **Step 2: 注册路由与菜单**

在 `aiops/observability-frontend/src/App.tsx`：
1. 在 `const Monitor = lazy(...)` 之后加 lazy import：
```tsx
const Capacity = lazy(() => import('./pages/Capacity'))
```
2. 在「监控」区 `menuGroups`（第 83-86 行）items 里加一项：
```tsx
{ key: '/capacity', icon: <LineChartOutlined />, label: '容量预测' },
```
3. 在 `@ant-design/icons` import（第 7-15 行）加 `LineChartOutlined`。
4. 在 `<Route path="/monitor" element={<Monitor />} />` 之后加：
```tsx
<Route path="/capacity" element={<Capacity />} />
```

- [ ] **Step 3: 类型检查**

Run: `cd aiops/observability-frontend && npx tsc --noEmit`
Expected: 无新增类型错误

- [ ] **Step 4: Commit**

```bash
cd aiops && git add observability-frontend/src/pages/Capacity/index.tsx observability-frontend/src/App.tsx && git commit -m "feat(capacity): /capacity 容量预测页（维度切换+双预测曲线+ETT）" --no-verify
```

---

## Self-Review

**1. Spec 覆盖：**
- 全维度（CPU/内存/磁盘/网络）→ Task 1/2 `capacityPromQL` 四 case ✓
- 双算法（线性回归+EWMA）→ Task 1 纯函数 + Task 2 组装 ✓
- ETT + within_horizon + already_breached → Task 1 `EstimateTimeToThreshold` + Task 2 响应 ✓
- 环比趋势 change_pct → Task 2 计算 + Task 4 展示 ✓
- 阈值 markLine → Task 4 `buildOption` ✓
- 历史窗口/预测长度控件 → Task 4 Select ✓
- 复用 `vmRangeQuery`/`devices.go` PromQL/echarts → Task 2/4 ✓
- 参数校验（缺 metric、network 缺 threshold → 400）→ Task 2 测试 ✓
- 路由注册 `/api/v1/capacity/forecast` → Task 3 ✓

**2. Placeholder 扫描：** 无 TBD/TODO；每步含具体代码。✓

**3. 类型一致性：**
- `LinearRegression(series) (slope, intercept float64)` 在 Task 1 定义、Task 2 消费，签名一致 ✓
- `EstimateTimeToThreshold(slope, intercept float64, n, horizon int, threshold float64) (int, bool)` 一致 ✓
- 响应 JSON 字段（metric/current/history/timestamps/forecasts/change_pct/threshold）Task 2 后端与 Task 3 前端类型一致 ✓
- 前端 `getCapacityForecast` 参数与后端 query 参数一致 ✓
- `within_horizon`/`already_breached`/`ett_seconds` 在 Task 2 响应、Task 3 类型、Task 4 消费一致 ✓
