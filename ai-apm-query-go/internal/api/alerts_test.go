package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestLogTypeQuery 验证 log 类型规则构造 CH 日志查询
func TestLogTypeQuery(t *testing.T) {
	q := logMetricQuery("svc", "log_error_rate", "error")
	if !strings.Contains(q, "log_records") {
		t.Fatal("log query should target log_records")
	}
	if !strings.Contains(q, "severity") {
		t.Fatal("log_error_rate should filter by severity")
	}
}

// TestCooldownBlocksRepeat 验证 cooldown 冷却期内不重复告警
func TestCooldownBlocksRepeat(t *testing.T) {
	c := AlertRule{Cooldown: 10}
	if !inCooldown(c, time.Now().Add(-2*time.Minute), time.Now()) {
		t.Fatal("recent trigger should be in cooldown")
	}
	if inCooldown(c, time.Now().Add(-20*time.Minute), time.Now()) {
		t.Fatal("old trigger should not be in cooldown")
	}
}

// TestTraceTypeQuery 验证 trace 类型规则构造 CH trace 查询
func TestTraceTypeQuery(t *testing.T) {
	q := traceMetricQuery("svc", "trace_latency")
	if !strings.Contains(q, "trace_spans") {
		t.Fatal("trace query should target trace_spans")
	}
	if !strings.Contains(q, "quantile") {
		t.Fatal("trace_latency should use quantile")
	}
}

// TestDampeningStreak 验证连续 breach 达阈值才告警
func TestDampeningStreak(t *testing.T) {
	d := AlertRule{Dampening: 3}
	if shouldAlertAfterDampening(d, 2) {
		t.Fatal("streak 2 < dampening 3, should not alert")
	}
	if !shouldAlertAfterDampening(d, 3) {
		t.Fatal("streak 3 >= dampening 3, should alert")
	}
	// dampening=0/1 视为不启用
	if !shouldAlertAfterDampening(AlertRule{Dampening: 0}, 0) {
		t.Fatal("dampening 0 should alert immediately")
	}
}

// TestDedupeSignature 验证相同 rule+service+signature 的事件在窗口内被合并（Count++ 而非新增）
func TestDedupeSignature(t *testing.T) {
	sig := "svc-error:500"
	e1 := eventSignature("rule1", "svc", sig)
	e2 := eventSignature("rule1", "svc", sig)
	if e1 != e2 {
		t.Fatal("same signature should dedupe")
	}
	e3 := eventSignature("rule1", "svc", "svc-error:502")
	if e1 == e3 {
		t.Fatal("different signature should not dedupe")
	}
}

func TestComputeBurnRate(t *testing.T) {
	// SLO 99.9% → 目标错误率 0.1%；实际错误率 1.44% → burn_rate 14.4
	br := ComputeBurnRate(1.44, 99.9)
	if br < 14.3 || br > 14.5 {
		t.Fatalf("burn_rate = %v, want ≈14.4", br)
	}
	// 无错误 → burn_rate 0
	if ComputeBurnRate(0, 99.9) != 0 {
		t.Fatalf("no error should burn 0")
	}
	// 100% SLO（目标错误率 0）→ burn_rate 0（避免除零）
	if ComputeBurnRate(50, 100) != 0 {
		t.Fatalf("100pct SLO should burn 0 (avoid div by zero)")
	}
}

// mockVM 模拟 VM query_range 返回固定序列。
func mockVM(t *testing.T, values [][2]interface{}) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"status": "success",
			"data": map[string]interface{}{
				"resultType": "matrix",
				"result": []map[string]interface{}{
					{"metric": map[string]interface{}{}, "values": values},
				},
			},
		})
	}))
}

// TestEvaluateRuleAnomalyZScore 验证 anomaly(zscore) 规则评估：平稳基线 + 突刺应触发。
func TestEvaluateRuleAnomalyZScore(t *testing.T) {
	// 基线 ~10，最后一点为突刺 50
	var vals [][2]interface{}
	for i := 0; i < 15; i++ {
		v := "10"
		if i == 14 {
			v = "50"
		}
		vals = append(vals, [2]interface{}{1710000000 + int64(i*60), v})
	}
	srv := mockVM(t, vals)
	defer srv.Close()

	h := &Handler{vmURL: srv.URL, client: &http.Client{}}
	rule := AlertRule{Name: "anom", Type: "anomaly", Service: "payments",
		Metric: "error_rate", BaselineSeconds: 900, AnomalyMethod: "zscore", Threshold: 3}
	value, breached := h.evaluateRuleAnomaly(rule)
	if !breached {
		t.Fatalf("anomaly zscore should trigger (current=50 vs baseline~10), value=%v", value)
	}
}

// TestEvaluateRuleAnomalySteady 验证平稳基线不触发。
func TestEvaluateRuleAnomalySteady(t *testing.T) {
	var vals [][2]interface{}
	for i := 0; i < 15; i++ {
		vals = append(vals, [2]interface{}{1710000000 + int64(i*60), "10"})
	}
	srv := mockVM(t, vals)
	defer srv.Close()

	h := &Handler{vmURL: srv.URL, client: &http.Client{}}
	rule := AlertRule{Name: "anom2", Type: "anomaly", Service: "payments",
		Metric: "error_rate", BaselineSeconds: 900, AnomalyMethod: "zscore", Threshold: 3}
	_, breached := h.evaluateRuleAnomaly(rule)
	if breached {
		t.Fatalf("steady baseline should NOT trigger anomaly")
	}
}

// TestEvaluateRuleAnomalyInsufficientData 验证数据不足不误报。
func TestEvaluateRuleAnomalyInsufficientData(t *testing.T) {
	// 只有 2 个点（不足 3 个）
	vals := [][2]interface{}{{1710000000, "10"}, {1710000060, "50"}}
	srv := mockVM(t, vals)
	defer srv.Close()

	h := &Handler{vmURL: srv.URL, client: &http.Client{}}
	rule := AlertRule{Name: "anom3", Type: "anomaly", Service: "payments",
		Metric: "error_rate", BaselineSeconds: 900, AnomalyMethod: "zscore", Threshold: 3}
	_, breached := h.evaluateRuleAnomaly(rule)
	if breached {
		t.Fatalf("insufficient data should not trigger")
	}
}
