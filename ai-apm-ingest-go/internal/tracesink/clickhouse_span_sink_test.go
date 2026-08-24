package tracesink

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/observability-platform/ai-apm-ingest-go/internal/model"
)

var fixedSpanTime = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func testSpan() *model.Span {
	return &model.Span{
		TenantID: "t1", ClusterID: "c1", TraceID: "trace-1", SpanID: "span-1",
		ServiceName: "checkout", OperationName: "GET /cart", SpanKind: "SERVER",
		StartTime: fixedSpanTime, DurationNs: 12345,
	}
}

func TestSpanDedupKeyDeterministic(t *testing.T) {
	a := spanDedupKey(testSpan())
	b := spanDedupKey(testSpan())
	if a == "" || a != b {
		t.Fatalf("dedup key should be deterministic non-empty, got %q %q", a, b)
	}
	// 不同 span_id → 不同 key
	s2 := testSpan()
	s2.SpanID = "span-2"
	if a == spanDedupKey(s2) {
		t.Fatalf("dedup key should differ by span_id")
	}
}

func TestClickHouseSpanSinkAddWritesHTTP(t *testing.T) {
	got := ""
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.URL.RawQuery
		w.WriteHeader(200)
	}))
	defer srv.Close()

	sink := NewClickHouseSpanSink(srv.URL, 5*time.Second)
	sink.Add(testSpan())
	if !sink.Healthy() {
		t.Fatalf("sink should be healthy after successful write")
	}
	if got == "" || len(got) == 0 {
		t.Fatalf("expected insert query in URL, got empty")
	}
}

func TestClickHouseSpanSinkFailClosed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(503)
	}))
	defer srv.Close()

	sink := NewClickHouseSpanSink(srv.URL, 5*time.Second)
	sink.Add(testSpan())
	if sink.Healthy() {
		t.Fatalf("sink must be unhealthy after failed write (fail-closed)")
	}
	if sink.LastError() == nil {
		t.Fatalf("expected last error recorded")
	}
}
