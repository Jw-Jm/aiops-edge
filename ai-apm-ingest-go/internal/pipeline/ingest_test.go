package pipeline

import (
	"testing"
	"time"

	"github.com/observability-platform/ai-apm-ingest-go/internal/model"
)

func TestProcessSpansServiceMetricCallbackPreservesCluster(t *testing.T) {
	p := New(nil, nil)
	p.SetClusterID("91771a6e-9c2d-11f1-8271-bea176fe9f9f")
	defer p.Close()

	var gotCluster string
	p.SetOnServiceMetricWithCluster(func(cluster, service string, isError bool, durationNs uint64) {
		gotCluster = cluster
	})

	_, err := p.ProcessSpans("7ed01afc-cc79-4ecd-8767-a2befa6168ad", []*model.Span{{
		TenantID:    "7ed01afc-cc79-4ecd-8767-a2befa6168ad",
		ClusterID:   "91771a6e-9c2d-11f1-8271-bea176fe9f9f",
		TraceID:     "00112233445566778899aabbccddeeff",
		SpanID:      "0102030405060708",
		ServiceName: "checkout",
		StartTime:   time.Unix(1725000000, 0).UTC(),
	}})
	if err != nil {
		t.Fatalf("ProcessSpans() error = %v", err)
	}
	if gotCluster != "91771a6e-9c2d-11f1-8271-bea176fe9f9f" {
		t.Fatalf("service metric cluster = %q, want ingest cluster", gotCluster)
	}
}

type captureBatchEdgeSink struct {
	rows  []*model.TopologyEdge
	calls int
}

func (s *captureBatchEdgeSink) AddEdge(edge *model.TopologyEdge) { s.rows = append(s.rows, edge) }

func (s *captureBatchEdgeSink) AddEdges(edges []*model.TopologyEdge) error {
	s.calls++
	s.rows = append(s.rows, edges...)
	return nil
}

func TestFlushMetricsUsesDurableEdgeBatch(t *testing.T) {
	sink := &captureBatchEdgeSink{}
	p := New(nil, sink)
	defer p.Close()
	accepted, failed := 0, false
	p.SetEdgeSinkResultObserver(func(n int, isFailed bool) {
		accepted += n
		failed = isFailed
	})

	p.mu.Lock()
	p.edgesAgg[edgeKey{tenantID: "t1", sourceService: "frontend", targetService: "backend", timeBucket: "2026-09-01T04:00"}] = &edgeValue{callCount: 2, durationSumNs: 200, durationCount: 2}
	p.mu.Unlock()
	p.flushMetrics()

	if sink.calls != 1 {
		t.Fatalf("batch calls = %d, want 1", sink.calls)
	}
	if len(sink.rows) != 1 || sink.rows[0].CallCount != 2 {
		t.Fatalf("batched rows = %#v", sink.rows)
	}
	if accepted != 1 || failed {
		t.Fatalf("edge sink observer = (%d, %v), want (1, false)", accepted, failed)
	}
}

// captureSpanSink records spans accepted by the pipeline write path.
type captureSpanSink struct {
	spans []*model.Span
}

func (s *captureSpanSink) Add(sp *model.Span) { s.spans = append(s.spans, sp) }

func (s *captureSpanSink) AddBatch(spans []*model.Span) error {
	s.spans = append(s.spans, spans...)
	return nil
}

const (
	testTenantID  = "7ed01afc-cc79-4ecd-8767-a2befa6168ad"
	testClusterID = "91771a6e-9c2d-11f1-8271-bea176fe9f9f"
)

// PF-DATA-003: a span whose parent_span_id equals its own span_id is an
// invalid self-reference; ingest must clear it to "" so no trace_spans row is
// ever written with parent_span_id == span_id.
func TestProcessSpansClearsSelfReferentialParent(t *testing.T) {
	sink := &captureSpanSink{}
	p := New(sink, nil)
	p.SetClusterID(testClusterID)
	defer p.Close()

	span := &model.Span{
		TenantID:     testTenantID,
		ClusterID:    testClusterID,
		TraceID:      "00112233445566778899aabbccddeeff",
		SpanID:       "0102030405060708",
		ParentSpanID: "0102030405060708", // self-referential defect
		ServiceName:  "payments",
		StartTime:    time.Unix(1725000000, 0).UTC(),
	}
	got, err := p.ProcessSpans(testTenantID, []*model.Span{span})
	if err != nil {
		t.Fatalf("ProcessSpans() error = %v", err)
	}
	if got != 1 {
		t.Fatalf("ProcessSpans() = %d spans, want 1", got)
	}
	if len(sink.spans) != 1 {
		t.Fatalf("sink spans = %d, want 1", len(sink.spans))
	}
	if sink.spans[0].ParentSpanID != "" {
		t.Fatalf("self-referential parent_span_id = %q, want empty (missing parent)", sink.spans[0].ParentSpanID)
	}
}

// PF-DATA-003: a missing parent_span_id must stay "" — never defaulted to the
// span's own span_id.
func TestProcessSpansMissingParentStaysEmpty(t *testing.T) {
	sink := &captureSpanSink{}
	p := New(sink, nil)
	p.SetClusterID(testClusterID)
	defer p.Close()

	span := &model.Span{
		TenantID:     testTenantID,
		ClusterID:    testClusterID,
		TraceID:      "00112233445566778899aabbccddeeff",
		SpanID:       "0102030405060708",
		ParentSpanID: "", // root span
		ServiceName:  "payments",
		StartTime:    time.Unix(1725000000, 0).UTC(),
	}
	if _, err := p.ProcessSpans(testTenantID, []*model.Span{span}); err != nil {
		t.Fatalf("ProcessSpans() error = %v", err)
	}
	if len(sink.spans) != 1 || sink.spans[0].ParentSpanID != "" {
		t.Fatalf("missing parent_span_id must stay empty, got %#v", sink.spans)
	}
}

// PF-DATA-003: real parent relationships (parent_span_id != span_id) must be
// preserved untouched.
func TestProcessSpansPreservesRealParent(t *testing.T) {
	sink := &captureSpanSink{}
	p := New(sink, nil)
	p.SetClusterID(testClusterID)
	defer p.Close()

	child := &model.Span{
		TenantID:     testTenantID,
		ClusterID:    testClusterID,
		TraceID:      "00112233445566778899aabbccddeeff",
		SpanID:       "0102030405060709",
		ParentSpanID: "0102030405060708",
		ServiceName:  "orders",
		StartTime:    time.Unix(1725000000, 0).UTC(),
	}
	if _, err := p.ProcessSpans(testTenantID, []*model.Span{child}); err != nil {
		t.Fatalf("ProcessSpans() error = %v", err)
	}
	if len(sink.spans) != 1 || sink.spans[0].ParentSpanID != "0102030405060708" {
		t.Fatalf("real parent_span_id must be preserved, got %#v", sink.spans)
	}
}

// PF-DATA-003: repeated deliveries of the same logical span (same
// tenant/cluster/trace_id/span_id) within a batch must produce a single
// trace_spans row and a single RED metric sample. Distinct traces sharing a
// span_id (different trace_id) must both be kept.
func TestProcessSpansDedupesRepeatedSpanIdentity(t *testing.T) {
	sink := &captureSpanSink{}
	p := New(sink, nil)
	p.SetClusterID(testClusterID)
	defer p.Close()

	traceID := "00112233445566778899aabbccddeeff"
	spans := []*model.Span{
		{
			TenantID: testTenantID, ClusterID: testClusterID, TraceID: traceID,
			SpanID: "0102030405060708", ServiceName: "payments",
			StartTime: time.Unix(1725000000, 0).UTC(),
		},
		// same (trace_id, span_id), different start time — re-delivery/defect
		{
			TenantID: testTenantID, ClusterID: testClusterID, TraceID: traceID,
			SpanID: "0102030405060708", ServiceName: "payments",
			StartTime: time.Unix(1725000001, 0).UTC(),
		},
		// same span_id, different trace — must be kept
		{
			TenantID: testTenantID, ClusterID: testClusterID, TraceID: "aabbccddeeff00112233445566778899",
			SpanID: "0102030405060708", ServiceName: "orders",
			StartTime: time.Unix(1725000000, 0).UTC(),
		},
	}
	got, err := p.ProcessSpans(testTenantID, spans)
	if err != nil {
		t.Fatalf("ProcessSpans() error = %v", err)
	}
	if got != 2 {
		t.Fatalf("ProcessSpans() = %d spans, want 2 (deduped)", got)
	}
	if len(sink.spans) != 2 {
		t.Fatalf("sink spans = %d, want 2", len(sink.spans))
	}
	traceSpans := 0
	for _, sp := range sink.spans {
		if sp.TraceID == traceID {
			traceSpans++
		}
	}
	if traceSpans != 1 {
		t.Fatalf("trace %q has %d spans after dedup, want 1", traceID, traceSpans)
	}
}
