package api

import (
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
