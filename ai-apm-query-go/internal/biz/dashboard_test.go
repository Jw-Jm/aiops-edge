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

// aggregateAlerts 应按 severity 与服务正确聚合告警计数。
func TestAlertStatsAggregation(t *testing.T) {
	byService := map[string]map[string]int{
		"svc-a": {"critical": 2, "warning": 1},
		"svc-b": {"warning": 3, "info": 4},
	}
	a := AggregateAlerts(byService)
	if a.Total != 10 {
		t.Fatalf("Total = %d, want 10", a.Total)
	}
	if a.Critical != 2 {
		t.Fatalf("Critical = %d, want 2", a.Critical)
	}
	if a.Warning != 4 {
		t.Fatalf("Warning = %d, want 4", a.Warning)
	}
	if a.Info != 4 {
		t.Fatalf("Info = %d, want 4", a.Info)
	}
	if len(a.ByService) != 2 {
		t.Fatalf("ByService len = %d, want 2", len(a.ByService))
	}
}
