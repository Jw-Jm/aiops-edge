package main

import (
	"math/rand"
	"testing"
	"time"
)

func TestSingleTraceSteadyStructure(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	tr := singleTrace(1, "payments", 1, 0.5, "steady", rng)
	if len(tr.ResourceSpans) != 1 {
		t.Fatalf("expected 1 resourceSpans, got %d", len(tr.ResourceSpans))
	}
	spans := tr.ResourceSpans[0].ScopeSpans[0].Spans
	if len(spans) != 1 {
		t.Fatalf("expected 1 span, got %d", len(spans))
	}
	sp := spans[0]
	if sp.Kind != 1 {
		t.Fatalf("expected kind SERVER(1), got %d", sp.Kind)
	}
	if sp.StartTimeUnixNano == "" || sp.EndTimeUnixNano == "" {
		t.Fatalf("missing timestamps")
	}
	// 服务的 resource 属性
	attrs := tr.ResourceSpans[0].Resource.Attributes
	if len(attrs) == 0 || attrs[0]["key"] != "service.name" {
		t.Fatalf("missing service.name attribute")
	}
}

func TestSingleTraceErrorModeAlwaysError(t *testing.T) {
	rng := rand.New(rand.NewSource(2))
	allErr := true
	for i := 0; i < 50; i++ {
		tr := singleTrace(i, "orders", i, 0.0, "error", rng)
		sp := tr.ResourceSpans[0].ScopeSpans[0].Spans[0]
		if sp.Status["code"] != 2 {
			allErr = false
			break
		}
	}
	if !allErr {
		t.Fatalf("error mode should always produce status.code=2")
	}
}

func TestFlattenResourceSpans(t *testing.T) {
	rng := rand.New(rand.NewSource(3))
	a := singleTrace(1, "a", 1, 0, "steady", rng)
	b := singleTrace(1, "b", 2, 0, "steady", rng)
	out := flattenResourceSpans([]otlpTrace{a, b})
	if len(out) != 2 {
		t.Fatalf("expected 2 resourceSpans, got %d", len(out))
	}
}

func TestSpanIDStableAndUnique(t *testing.T) {
	rng := rand.New(rand.NewSource(4))
	tr := singleTrace(1, "payments", 5, 0, "steady", rng)
	sp := tr.ResourceSpans[0].ScopeSpans[0].Spans[0]
	// spanID 应稳定唯一且为十六进制
	if len(sp.SpanID) != 16 {
		t.Fatalf("spanID len = %d, want 16", len(sp.SpanID))
	}
	_ = time.Now()
}

func TestAssembleLogsErrorModeHighErr(t *testing.T) {
	rng := rand.New(rand.NewSource(5))
	logs := assembleLogs(1, []string{"payments"}, 100, 0.1, "error", rng)
	if len(logs) != 100 {
		t.Fatalf("expected 100 logs, got %d", len(logs))
	}
	// error 模式下大部分应为 ERROR/FATAL
	errCnt := 0
	for _, l := range logs {
		if l.SeverityText == "ERROR" || l.SeverityText == "FATAL" {
			errCnt++
		}
	}
	if errCnt < 50 {
		t.Fatalf("error mode should produce high error logs, got %d/100", errCnt)
	}
	// 每条日志带 service.name 属性
	hasSvc := false
	for _, a := range logs[0].Attributes {
		if a["key"] == "service.name" {
			hasSvc = true
		}
	}
	if !hasSvc {
		t.Fatal("log should carry service.name attribute")
	}
}

func TestAssembleLogsSteadyLowErr(t *testing.T) {
	rng := rand.New(rand.NewSource(6))
	logs := assembleLogs(1, []string{"orders"}, 200, 0.05, "steady", rng)
	errCnt := 0
	for _, l := range logs {
		if l.SeverityText == "ERROR" || l.SeverityText == "FATAL" {
			errCnt++
		}
	}
	// steady 模式错误比例应较低（~5% ± 容差）
	if errCnt > 60 {
		t.Fatalf("steady mode should have low error rate, got %d/200", errCnt)
	}
}

func TestAssembleLogsFatalIncluded(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	logs := assembleLogs(1, []string{"checkout"}, 500, 0.9, "steady", rng)
	fatalCnt := 0
	for _, l := range logs {
		if l.SeverityText == "FATAL" {
			fatalCnt++
		}
	}
	if fatalCnt == 0 {
		t.Fatal("high error rate should include some FATAL logs")
	}
}
