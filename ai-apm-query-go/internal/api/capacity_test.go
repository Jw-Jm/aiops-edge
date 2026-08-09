package api

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
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
