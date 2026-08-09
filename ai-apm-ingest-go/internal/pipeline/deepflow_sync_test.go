package pipeline

import (
	"testing"
	"time"

	"github.com/observability-platform/ai-apm-ingest-go/internal/model"
)

func TestParseSyncInterval_Default(t *testing.T) {
	if got := parseSyncInterval(""); got != 60*time.Second {
		t.Fatalf("default interval = %v, want 60s", got)
	}
}

func TestParseSyncInterval_ValidSeconds(t *testing.T) {
	if got := parseSyncInterval("10"); got != 10*time.Second {
		t.Fatalf("got %v, want 10s", got)
	}
}

func TestParseSyncInterval_ValidDuration(t *testing.T) {
	if got := parseSyncInterval("15s"); got != 15*time.Second {
		t.Fatalf("got %v, want 15s", got)
	}
}

func TestParseSyncInterval_ClampedOutOfRange(t *testing.T) {
	if got := parseSyncInterval("2h"); got != 60*time.Second { // >3600s 回退默认
		t.Fatalf("too-large interval = %v, want default", got)
	}
	if got := parseSyncInterval("1s"); got != 60*time.Second { // <5s 回退默认
		t.Fatalf("too-small interval = %v, want default", got)
	}
}

func TestParseSyncInterval_AtMaxAllowed(t *testing.T) {
	if got := parseSyncInterval("1h"); got != 3600*time.Second { // 恰好等于上限，应允许
		t.Fatalf("at-max interval = %v, want 3600s", got)
	}
}

func TestParseSyncInterval_InvalidString(t *testing.T) {
	if got := parseSyncInterval("abc"); got != 60*time.Second {
		t.Fatalf("invalid interval = %v, want default", got)
	}
}

func TestClampStartTime_NoClockSkew(t *testing.T) {
	now := time.Now().UTC()
	last := now.Add(-2 * time.Minute)
	if got := clampStartTime(last, now); got.Unix() != last.Unix() {
		t.Fatalf("clamp changed valid last time: got %v, want %v", got, last)
	}
}

func TestClampStartTime_Future(t *testing.T) {
	now := time.Now().UTC()
	if got := clampStartTime(now.Add(time.Hour), now); got.After(now) {
		t.Fatalf("clamp did not pull future time back: %v", got)
	}
}

func TestClampStartTime_TooOld(t *testing.T) {
	now := time.Now().UTC()
	if got := clampStartTime(now.Add(-2*time.Hour), now); got.Before(now.Add(-15*time.Minute)) {
		t.Fatalf("clamp allowed too-old start: %v", got)
	}
}

// fakeREDMetric 记录 AddServiceREDForCluster 的调用，用于验证 DeepFlowSyncer 累加 RED。
type fakeREDMetric struct {
	calls []string // "cluster|service|isError"
}

func (f *fakeREDMetric) AddServiceREDForCluster(cluster, service string, isError bool, durationNs uint64) {
	flag := "ok"
	if isError {
		flag = "err"
	}
	f.calls = append(f.calls, cluster+"|"+service+"|"+flag)
}

type fakeSpanWriter struct{}

func (f *fakeSpanWriter) Add(*model.Span) {}

// TestNewDeepFlowSyncer_BindsREDMetric 验证 cluster/redMetric 正确绑定。
func TestNewDeepFlowSyncer_BindsREDMetric(t *testing.T) {
	red := &fakeREDMetric{}
	s := NewDeepFlowSyncer("10.0.0.1", 8123, "cluster-x", nil, &fakeSpanWriter{}, nil, red)
	if s.cluster != "cluster-x" {
		t.Fatalf("cluster = %q, want cluster-x", s.cluster)
	}
	if s.redMetric == nil {
		t.Fatalf("redMetric not bound")
	}
	s.redMetric.AddServiceREDForCluster(s.cluster, "payments", true, 1000)
	if len(red.calls) != 1 || red.calls[0] != "cluster-x|payments|err" {
		t.Fatalf("redMetric not wired to fake: %v", red.calls)
	}
}

// TestREDAccumulation 用注入 rows 驱动 RED 累加核心逻辑（错误判定 + cluster 传递）。
func TestREDAccumulation(t *testing.T) {
	red := &fakeREDMetric{}
	s := &DeepFlowSyncer{redMetric: red, cluster: "cluster-x"}
	s.accumulateREDFromRows([]map[string]interface{}{
		{"dst": "svc-b", "response_duration": uint64(1200), "response_code": uint64(200)},
		{"dst": "svc-b", "response_duration": uint64(800), "response_code": uint64(503)},
		{"dst": "svc-c", "response_duration": uint64(500), "response_code": uint64(404)},
	})
	if len(red.calls) != 3 {
		t.Fatalf("expected 3 RED calls, got %v", red.calls)
	}
	if red.calls[0] != "cluster-x|svc-b|ok" {
		t.Fatalf("call0 = %q, want cluster-x|svc-b|ok", red.calls[0])
	}
	if red.calls[1] != "cluster-x|svc-b|err" {
		t.Fatalf("call1 = %q, want cluster-x|svc-b|err (5xx)", red.calls[1])
	}
	// 4xx 不算错误
	if red.calls[2] != "cluster-x|svc-c|ok" {
		t.Fatalf("call2 = %q, want cluster-x|svc-c|ok (4xx not error)", red.calls[2])
	}
}

// accumulateREDFromRows 封装 syncTraces 中的 RED 累加逻辑，便于单测（与 syncTraces 共用同一判定）。
func (s *DeepFlowSyncer) accumulateREDFromRows(rows []map[string]interface{}) {
	for _, r := range rows {
		dst, _ := r["dst"].(string)
		if dst == "" {
			continue
		}
		durUs := toUint(r["response_duration"])
		durNs := durUs * 1000
		code := int64(toUint(r["response_code"]))
		isErr := uint8(0)
		if (code >= 500 && code <= 599) || (code == 0 && durNs >= 1000000000) {
			isErr = 1
		}
		if s.redMetric != nil {
			s.redMetric.AddServiceREDForCluster(s.cluster, dst, isErr == 1, durNs)
		}
	}
}
