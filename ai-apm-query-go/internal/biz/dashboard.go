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

// AlertBySvc 按服务聚合的告警统计。
type AlertBySvc struct {
	Service  string `json:"service"`
	Critical int    `json:"critical"`
	Warning  int    `json:"warning"`
	Info     int    `json:"info"`
	Total    int    `json:"total"`
}

// AlertStats 告警统计（Dashboard 环形图数据）。
type AlertStats struct {
	Total     int          `json:"total"`
	Critical  int          `json:"critical"`
	Warning   int          `json:"warning"`
	Info      int          `json:"info"`
	ByService []AlertBySvc `json:"by_service"`
}

// TrendPoint 时间窗口内的调用/错误趋势点。
type TrendPoint struct {
	T      string `json:"t"`
	Calls  int64  `json:"calls"`
	Errors int64  `json:"errors"`
}

// ErrorItem TOP 错误服务分布项。
type ErrorItem struct {
	Service string `json:"service"`
	Errors  int64  `json:"errors"`
}

// DashboardStats 是 /dashboard/stats 的聚合响应。
type DashboardStats struct {
	Services int `json:"services"`
	// TopologyServices 含拓扑目录中无 trace 的服务总数（前端展示用，P1-4 口径统一）。
	TopologyServices int          `json:"topology_services"`
	Edges            int64        `json:"edges"`
	TotalCalls       int64        `json:"total_calls"`
	TotalErrors      int64        `json:"total_errors"`
	ErrorRate        float64      `json:"error_rate"`
	LatencyP95       float64      `json:"latency_p95"`
	TopServices      []StatsItem  `json:"top_services"`
	Trend            []TrendPoint `json:"trend"`
	TopErrors        []ErrorItem  `json:"top_errors"`
	AlertStats       AlertStats   `json:"alerts"`
	DataGaps         []string     `json:"data_gaps"` // P1-3：近 24h 缺失的小时窗口（"MM-DD HH:00 ~ MM-DD HH:00"）
}

// AggregateStats 从服务 RED 聚合行汇总出 Dashboard 统计。
func AggregateStats(rows []StatsItem) *DashboardStats {
	var s DashboardStats
	for _, r := range rows {
		s.TotalCalls += r.Calls
		s.TotalErrors += r.Errors
		// P1-2 修复：逐项计算平均延迟(ms)与错误率(%)，避免 top_services 恒为 0
		item := r
		if item.Calls > 0 {
			item.AvgLatency = float64(item.LatSumNs) / float64(item.Calls) / 1e6 // ns → ms
			// A2 修复：error_rate 统一为 0-1 小数（与 /services 口径一致），前端负责乘 100 展示
			item.ErrorRate = float64(item.Errors) / float64(item.Calls)
		}
		s.TopServices = append(s.TopServices, item)
	}
	s.Services = len(rows)
	if s.TotalCalls > 0 {
		s.ErrorRate = float64(s.TotalErrors) / float64(s.TotalCalls)
	}
	return &s
}

// AggregateAlerts 按严重级别与服务聚合告警统计（供 Dashboard 环形图使用）。
// byService 形如 map[service]map[severity]count。
func AggregateAlerts(byService map[string]map[string]int) AlertStats {
	var a AlertStats
	for svc, sev := range byService {
		item := AlertBySvc{Service: svc}
		for severity, cnt := range sev {
			item.Total += cnt
			a.Total += cnt
			switch severity {
			case "critical":
				item.Critical += cnt
				a.Critical += cnt
			case "warning":
				item.Warning += cnt
				a.Warning += cnt
			case "info":
				item.Info += cnt
				a.Info += cnt
			}
		}
		if item.Total > 0 {
			a.ByService = append(a.ByService, item)
		}
	}
	return a
}
