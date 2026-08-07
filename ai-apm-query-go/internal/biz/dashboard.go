package biz

// StatsItem 描述单个服务的聚合指标项。
type StatsItem struct {
	Service    string  `json:"service"`
	Calls      int64   `json:"calls"`
	Errors     int64   `json:"errors"`
	LatSumNs   int64   `json:"lat_sum_ns"`
	ErrorRate  float64 `json:"error_rate"`
	AvgLatency float64 `json:"avg_latency_ms"`
}

// DashboardStats 是 /dashboard/stats 的聚合响应。
type DashboardStats struct {
	Services    int         `json:"services"`
	Edges       int64       `json:"edges"`
	TotalCalls  int64       `json:"total_calls"`
	TotalErrors int64       `json:"total_errors"`
	ErrorRate   float64     `json:"error_rate"`
	TopServices []StatsItem `json:"top_services"`
}

// AggregateStats 从服务 RED 聚合行汇总出 Dashboard 统计。
func AggregateStats(rows []StatsItem) *DashboardStats {
	var s DashboardStats
	for _, r := range rows {
		s.TotalCalls += r.Calls
		s.TotalErrors += r.Errors
	}
	s.Services = len(rows)
	if s.TotalCalls > 0 {
		s.ErrorRate = float64(s.TotalErrors) / float64(s.TotalCalls) * 100
	}
	s.TopServices = rows
	return &s
}
