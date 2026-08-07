package biz

import "testing"

// aggregateStats 应聚合各计数并汇总 top 服务。
func TestDashboardStatsAggregation(t *testing.T) {
	input := []StatsItem{
		{Service: "svc-a", Calls: 100, Errors: 5, LatSumNs: 2000},
		{Service: "svc-b", Calls: 50, Errors: 0, LatSumNs: 1000},
	}
	stats := AggregateStats(input)
	if stats.Services != 2 {
		t.Fatalf("Services = %d, want 2", stats.Services)
	}
	if stats.TotalCalls != 150 {
		t.Fatalf("TotalCalls = %d, want 150", stats.TotalCalls)
	}
	if stats.TotalErrors != 5 {
		t.Fatalf("TotalErrors = %d, want 5", stats.TotalErrors)
	}
	if stats.TopServices == nil || len(stats.TopServices) != 2 {
		t.Fatalf("TopServices = %v, want 2 items", stats.TopServices)
	}
}
