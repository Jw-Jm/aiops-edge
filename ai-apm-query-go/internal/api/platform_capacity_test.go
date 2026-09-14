package api

import (
	"testing"
	"time"

	"github.com/observability-platform/ai-apm-query-go/internal/store"
)

const nodesFixture = `{
  "items": [
    {"metadata":{"name":"n1"},"status":{"conditions":[{"type":"Ready","status":"True"}],"allocatable":{"cpu":"8","memory":"16Gi"},"capacity":{"cpu":"8","memory":"16Gi"}}},
    {"metadata":{"name":"n2"},"status":{"conditions":[{"type":"Ready","status":"False"}],"allocatable":{"cpu":"4","memory":"8Gi"},"capacity":{"cpu":"4","memory":"8Gi"}}},
    {"metadata":{"name":"n3"},"status":{"conditions":[],"allocatable":{"cpu":"4","memory":"8Gi"},"capacity":{"cpu":"4","memory":"8Gi"}}}
  ]
}`

const metricsFixture = `{
  "items": [
    {"metadata":{"name":"n1"},"usage":{"cpu":"4","memory":"8Gi"}},
    {"metadata":{"name":"n2"},"usage":{"cpu":"3900m","memory":"7900Mi"}}
  ]
}`

func TestAggregateClusterCapacityUsesAllocatableAsDenominator(t *testing.T) {
	samples, fellBack := parseNodeAllocatable([]byte(nodesFixture))
	if fellBack {
		t.Fatal("allocatable present, fallback must not trigger")
	}
	if len(samples) != 3 {
		t.Fatalf("expected 3 nodes, got %d", len(samples))
	}
	if matched := applyNodeUsage(samples, []byte(metricsFixture)); matched != 2 {
		t.Fatalf("expected 2 nodes with usage, got %d", matched)
	}

	fact := aggregateClusterCapacity(store.Cluster{ClusterID: "c1", Name: "cluster-1", LifecycleStatus: "active"}, samples, time.Now().UTC(), "partial", "n3 missing usage")

	// 集群口径：16 核可分配，用量 4 + 3.9 = 7.9
	if fact.CPU.Allocatable != 16 {
		t.Fatalf("allocatable cores must be sum of node allocatable, got %v", fact.CPU.Allocatable)
	}
	if diff := fact.CPU.Used - 7.9; diff > 1e-9 || diff < -1e-9 {
		t.Fatalf("used cores must be sum of node usage, got %v", fact.CPU.Used)
	}
	if fact.CPU.UsageRatio == nil {
		t.Fatal("usage ratio must be present when usage is known")
	}
	// 内存：32Gi 可分配，8Gi + 7900Mi 用量
	// 单位契约：内存必须以 bytes 对外表达（parseQuantity 内部为 KiB）。
	// 32Gi = 32 * 1024 * 1024 KiB = 32 * 1024^3 bytes。
	if fact.Memory.Allocatable != 32*1024*1024*1024 {
		t.Fatalf("memory allocatable must be bytes, got %v", fact.Memory.Allocatable)
	}
	if fact.Memory.Used != (8*1024*1024+7900*1024)*1024 {
		t.Fatalf("memory used must be bytes, got %v", fact.Memory.Used)
	}
	if fact.CPU.Unit != "cores" || fact.Memory.Unit != "bytes" {
		t.Fatalf("units must be explicit: %s / %s", fact.CPU.Unit, fact.Memory.Unit)
	}
	if fact.CPU.Aggregation == "" || fact.CPU.Source == "" {
		t.Fatal("aggregation and source must be declared for reconciliation")
	}
	if fact.Nodes.Total != 3 || fact.Nodes.Ready != 1 || fact.Nodes.NotReady != 1 || fact.Nodes.Unknown != 1 {
		t.Fatalf("node readiness must be split honestly, got %+v", fact.Nodes)
	}
	if fact.Quality != "partial" || fact.QualityReason == "" {
		t.Fatal("partial quality must carry a reason")
	}
}

func TestAggregateClusterCapacityReportsHotNodesAndP95(t *testing.T) {
	samples := []nodeCapacitySample{
		{name: "n1", ready: true, knownReady: true, allocCPU: 4, allocMem: 8 * 1024 * 1024, usedCPU: 3.9, usedMem: 1, hasUsage: true}, // 97.5% cpu
		{name: "n2", ready: true, knownReady: true, allocCPU: 4, allocMem: 8 * 1024 * 1024, usedCPU: 0.2, usedMem: 1, hasUsage: true}, // 5% cpu
	}
	fact := aggregateClusterCapacity(store.Cluster{ClusterID: "c2"}, samples, time.Now().UTC(), "healthy", "")

	if fact.HotNodeCount != 1 {
		t.Fatalf("expected 1 hot node above 80%%, got %d", fact.HotNodeCount)
	}
	if fact.P95CPUUtilization == nil || fact.MaxCPUUtilization == nil {
		t.Fatal("P95 and max must both be present")
	}
	if *fact.MaxCPUUtilization < 97 {
		t.Fatalf("max node utilization must not be masked by the cluster average, got %v", *fact.MaxCPUUtilization)
	}
	if fact.CPU.UsageRatio == nil || *fact.CPU.UsageRatio >= 0.6 {
		t.Fatalf("cluster average should be low here, got %v", fact.CPU.UsageRatio)
	}
}

func TestAggregateClusterCapacityWithoutUsageDoesNotFakeZero(t *testing.T) {
	samples := []nodeCapacitySample{{name: "n1", ready: true, knownReady: true, allocCPU: 8, allocMem: 16 * 1024 * 1024}}
	fact := aggregateClusterCapacity(store.Cluster{ClusterID: "c3"}, samples, time.Now().UTC(), "partial", "metrics-server unavailable")

	if fact.CPU.UsageRatio != nil || fact.Memory.UsageRatio != nil {
		t.Fatal("missing usage must stay unknown, never 0 or healthy")
	}
	if fact.P95CPUUtilization != nil || fact.MaxCPUUtilization != nil {
		t.Fatal("no usage samples means no utilization percentiles")
	}
	if fact.CPU.Allocatable != 8 {
		t.Fatalf("allocatable still known, got %v", fact.CPU.Allocatable)
	}
}

func TestParseNodeAllocatableFallsBackAndFlags(t *testing.T) {
	fixture := `{"items":[{"metadata":{"name":"n1"},"status":{"conditions":[{"type":"Ready","status":"True"}],"capacity":{"cpu":"2","memory":"4Gi"}}}]}`
	samples, fellBack := parseNodeAllocatable([]byte(fixture))
	if !fellBack {
		t.Fatal("missing allocatable must be flagged")
	}
	if len(samples) != 1 || samples[0].allocCPU != 2 {
		t.Fatalf("capacity fallback must still produce a denominator, got %+v", samples)
	}
}

func TestPercentileClosestRank(t *testing.T) {
	values := make([]float64, 0, 100)
	for i := 1; i <= 100; i++ {
		values = append(values, float64(i))
	}
	got := percentile(values, 0.95)
	if got == nil || *got != 95 {
		t.Fatalf("p95 of 1..100 must be 95, got %v", got)
	}
	if percentile(nil, 0.95) != nil {
		t.Fatal("empty sample must return nil, not 0")
	}
}
