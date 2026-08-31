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
