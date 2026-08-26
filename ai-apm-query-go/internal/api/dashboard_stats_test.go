package api

import (
	"testing"

	"github.com/observability-platform/ai-apm-query-go/internal/biz"
)

func TestTopologyServiceCountKeepsTraceServicesWhenLegacyDirectoryIsEmpty(t *testing.T) {
	items := []biz.StatsItem{
		{Service: "query-api"},
		{Service: "ingest"},
	}

	if got := topologyServiceCount(items, nil); got != 2 {
		t.Fatalf("topologyServiceCount(empty legacy directory) = %d, want 2", got)
	}
}

func TestTopologyServiceCountAddsNonDeletedLegacyDirectoryServices(t *testing.T) {
	items := []biz.StatsItem{{Service: "query-api"}}

	if got := topologyServiceCount(items, []string{"query-api", "legacy-worker", "legacy-worker (deleted)"}); got != 2 {
		t.Fatalf("topologyServiceCount(legacy directory) = %d, want 2", got)
	}
}
