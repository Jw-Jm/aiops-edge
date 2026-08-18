package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/observability-platform/ai-apm-query-go/internal/store"
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

// TestLogKeywordQuery 验证 log_keyword 用独立 keyword 匹配 body（而非规则名）。
func TestLogKeywordQuery(t *testing.T) {
	q := logMetricQuery("svc", "log_keyword", "OOMKilled")
	if !strings.Contains(q, "body LIKE '%OOMKilled%'") {
		t.Fatalf("log_keyword should match body LIKE keyword, got: %s", q)
	}
	if strings.Contains(q, "severity") {
		t.Fatal("log_keyword should NOT use severity filter")
	}
	// keyword 含特殊字符时应正确转义（% 不进入 SQL 结构）
	q2 := logMetricQuery("svc", "log_keyword", "a' OR '1'='1")
	if strings.Contains(q2, "OR '1'='1") {
		t.Fatal("keyword should be escaped to avoid SQL injection")
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

// TestEvaluateRuleAnomalyDefaultThreshold 验证 Threshold<=0 时用默认 zscore=3。
func TestEvaluateRuleAnomalyDefaultThreshold(t *testing.T) {
	var vals [][2]interface{}
	for i := 0; i < 15; i++ {
		v := "10"
		if i == 14 {
			v = "50" // 突刺，zscore≈4>3 应触发（默认阈值）
		}
		vals = append(vals, [2]interface{}{1710000000 + int64(i*60), v})
	}
	srv := mockVM(t, vals)
	defer srv.Close()

	h := &Handler{vmURL: srv.URL, client: &http.Client{}}
	rule := AlertRule{Name: "anom4", Type: "anomaly", Service: "payments",
		Metric: "error_rate", BaselineSeconds: 900, AnomalyMethod: "zscore", Threshold: 0}
	_, breached := h.evaluateRuleAnomaly(rule)
	if !breached {
		t.Fatalf("default zscore threshold=3 should flag spike")
	}
}

// TestEvaluateRuleAnomalyMADMethod 验证 MAD 方法路径。
func TestEvaluateRuleAnomalyMADMethod(t *testing.T) {
	// 基线含一个温和离群点，但 MAD 稳健；最后一点强突刺才触发
	var vals [][2]interface{}
	for i := 0; i < 15; i++ {
		v := "10"
		if i == 3 {
			v = "25" // 温和离群，不触发
		}
		if i == 14 {
			v = "50" // 强突刺触发
		}
		vals = append(vals, [2]interface{}{1710000000 + int64(i*60), v})
	}
	srv := mockVM(t, vals)
	defer srv.Close()

	h := &Handler{vmURL: srv.URL, client: &http.Client{}}
	rule := AlertRule{Name: "anom5", Type: "anomaly", Service: "payments",
		Metric: "error_rate", BaselineSeconds: 900, AnomalyMethod: "mad", Threshold: 3.5}
	_, breached := h.evaluateRuleAnomaly(rule)
	if !breached {
		t.Fatalf("mad should flag strong spike (50)")
	}
}

// TestEvaluateRuleAnomalyEmptyMethod 验证 AnomalyMethod 为空默认 zscore。
func TestEvaluateRuleAnomalyEmptyMethod(t *testing.T) {
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
	rule := AlertRule{Name: "anom6", Type: "anomaly", Service: "payments",
		Metric: "error_rate", BaselineSeconds: 900, Threshold: 3} // AnomalyMethod 空
	_, breached := h.evaluateRuleAnomaly(rule)
	if !breached {
		t.Fatalf("empty method should default to zscore and flag spike")
	}
}

// TestEvaluateRuleSelEventDBUnavailable 验证 sel_event 规则在 MySQL 不可达（查询报错）时
// evaluateRule 返回 error（评估循环记日志跳过），不 panic。
func TestEvaluateRuleSelEventDBUnavailable(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	prev := store.GetDB()
	store.SetDB(db)
	t.Cleanup(func() { store.SetDB(prev) })

	// 查询返回错误 → 模拟 DB 不可达/查询失败
	mock.ExpectQuery("SELECT count").WithArgs("node-01").WillReturnError(errors.New("mysql down"))

	h := &Handler{}
	rule := AlertRule{Name: "sel", Type: "sel_event", Service: "node-01", Threshold: 5, Duration: 5}
	if _, err := h.evaluateRule(rule); err == nil {
		t.Fatal("sel_event evaluateRule should return error when DB unavailable")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

// TestEvaluateRuleSelEventCount 验证 sel_event 规则查询 ipmi_sel_events 返回最近窗口事件数。
func TestEvaluateRuleSelEventCount(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	prev := store.GetDB()
	store.SetDB(db)
	t.Cleanup(func() { store.SetDB(prev) })

	mock.ExpectQuery("SELECT count").
		WithArgs("node-01").
		WillReturnRows(sqlmock.NewRows([]string{"count(*)"}).AddRow(3))

	h := &Handler{}
	rule := AlertRule{Name: "sel2", Type: "sel_event", Service: "node-01", Threshold: 5, Duration: 10}
	v, err := h.evaluateRule(rule)
	if err != nil {
		t.Fatalf("evaluateRule: %v", err)
	}
	if v != 3 {
		t.Fatalf("count = %v, want 3", v)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

// mockVMInstant 模拟 VM /api/v1/query 返回即时向量（单个样本）。
func mockVMInstant(t *testing.T, value string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"status": "success",
			"data": map[string]interface{}{
				"resultType": "vector",
				"result": []map[string]interface{}{
					{"metric": map[string]interface{}{}, "value": []interface{}{float64(1710000000), value}},
				},
			},
		})
	}))
}

// TestEvaluateRuleMiddlewareMetric 验证 middleware_metric 规则走 VM instant query 取值。
func TestEvaluateRuleMiddlewareMetric(t *testing.T) {
	srv := mockVMInstant(t, "42.5")
	defer srv.Close()

	h := &Handler{vmURL: srv.URL, client: &http.Client{}}
	rule := AlertRule{Name: "mw", Type: "middleware_metric", Service: "mysql-01",
		Metric: "mysql_global_status_threads_connected", Threshold: 100, Duration: 5}
	v, err := h.evaluateRule(rule)
	if err != nil {
		t.Fatalf("evaluateRule: %v", err)
	}
	if v != 42.5 {
		t.Fatalf("value = %v, want 42.5", v)
	}
}

// TestCreateMiddlewareMetricRuleRequiresMetric 验证 middleware_metric 规则 metric 为空时创建返回 400。
func TestCreateMiddlewareMetricRuleRequiresMetric(t *testing.T) {
	h := &Handler{client: &http.Client{}}
	adminToken := generateJWT("admin", "admin", "")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/alerts/rules",
		strings.NewReader(`{"name":"mw-threads","type":"middleware_metric","service":"mysql-01","threshold":50,"duration":5}`))
	req.Header.Set("Authorization", "Bearer "+adminToken)
	h.createAlertRule(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for empty metric, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestAlertRuleByIDPutUpdatesExistingRule(t *testing.T) {
	alertRulesMu.Lock()
	originalRules := append([]AlertRule(nil), alertRules...)
	alertRules = []AlertRule{{
		ID: "rule-to-update", Name: "old name", Service: "checkout", Type: "threshold",
		Metric: "error_rate", Threshold: 5, Duration: 5, Severity: "warning",
	}}
	alertRulesMu.Unlock()
	t.Cleanup(func() {
		alertRulesMu.Lock()
		alertRules = originalRules
		alertRulesMu.Unlock()
	})

	req := httptest.NewRequest(http.MethodPut, "/api/v1/alerts/rules/rule-to-update",
		strings.NewReader(`{"name":"checkout errors","service":"checkout","type":"threshold","metric":"error_rate","threshold":2,"duration":10,"severity":"critical"}`))
	req.Header.Set("Authorization", "Bearer "+generateJWT("admin", "admin", ""))
	rec := httptest.NewRecorder()

	(&Handler{}).AlertRuleByID(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected PUT update status 200, got %d: %s", rec.Code, rec.Body.String())
	}

	alertRulesMu.RLock()
	updated := alertRules[0]
	alertRulesMu.RUnlock()
	if updated.ID != "rule-to-update" || updated.Name != "checkout errors" || updated.Threshold != 2 || updated.Duration != 10 {
		t.Fatalf("unexpected updated rule: %+v", updated)
	}
}
