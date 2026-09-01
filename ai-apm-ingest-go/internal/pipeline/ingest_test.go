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
