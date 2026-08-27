package query

import (
	"context"
	"fmt"
	"strings"
)

// ResourceScope 是 resource（服务/目录）资源域的作用域。
type ResourceScope struct {
	TenantID  string
	ClusterID string
	Services  []string
	// Minutes bounds resource discovery and metrics to the requested window.
	// Zero keeps the legacy 24-hour default for callers that do not specify it.
	Minutes int
}

func resourceTimeWhere(scope ResourceScope) string {
	minutes := scope.Minutes
	if minutes <= 0 {
		minutes = 1440
	}
	days := (minutes + 1439) / 1440
	return fmt.Sprintf("date >= today()-%d AND start_time >= now() - INTERVAL %d MINUTE", days, minutes)
}

// ServiceMetric 单个服务的近 24h 指标聚合（calls/errors/avg_ms）。
type ServiceMetric struct {
	Service string
	Calls   int64
	Errors  int64
	AvgMS   float64
}

// ResourceRepository 是 resource（服务/目录）资源域的 domain repository（V9.2 Phase 6）。
// 服务发现/指标 SoT 固定 ClickHouse（trace_spans，冻结职责）。SQL ownership 在此。
type ResourceRepository struct {
	ch *ClickHouseRepo
}

// NewResourceRepository 构造 resource repository，共享 ClickHouseExecutor。
func NewResourceRepository(ch *ClickHouseRepo) *ResourceRepository {
	return &ResourceRepository{ch: ch}
}

// resourceWhere 构造 resource 域的 tenant/cluster/service 过滤子句。
func resourceWhere(scope ResourceScope) string {
	var parts []string
	parts = append(parts, "tenant_id='"+scope.TenantID+"'")
	if scope.ClusterID != "" {
		parts = append(parts, "cluster_id='"+scope.ClusterID+"'")
	}
	if len(scope.Services) > 0 {
		quoted := make([]string, 0, len(scope.Services))
		for _, s := range scope.Services {
			quoted = append(quoted, sqlStr(s))
		}
		parts = append(parts, "service_name IN ("+strings.Join(quoted, ",")+")")
	}
	return strings.Join(parts, " AND ")
}

// ActiveServices 返回时间窗口内活跃的去重服务名列表（P1-5 口径，与 stats 一致）。
// includeDeleted 不在此过滤——deleted 过滤是业务展示逻辑，由 handler 决定。
func (r *ResourceRepository) ActiveServices(ctx context.Context, scope ResourceScope, includeDeleted bool) ([]string, error) {
	sql := "SELECT DISTINCT service_name FROM observability.trace_spans WHERE " +
		resourceWhere(scope) + " AND " + resourceTimeWhere(scope) + " AND service_name != '' ORDER BY service_name"
	rows, err := r.ch.QueryJSON(ctx, sql)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		if s := str(row, "service_name"); s != "" {
			out = append(out, s)
		}
	}
	return out, nil
}

// ServiceMetrics 返回各服务时间窗口内的 calls/errors/avg_ms 聚合（ListServices 指标列）。
func (r *ResourceRepository) ServiceMetrics(ctx context.Context, scope ResourceScope) ([]ServiceMetric, error) {
	sql := fmt.Sprintf(
		"SELECT service_name AS service, count() AS calls, countIf(is_error=1) AS errs, avg(duration_ns)/1000000 AS avg_ms "+
			"FROM observability.trace_spans WHERE %s AND %s GROUP BY service_name",
		resourceWhere(scope), resourceTimeWhere(scope))
	rows, err := r.ch.QueryJSON(ctx, sql)
	if err != nil {
		return nil, err
	}
	out := make([]ServiceMetric, 0, len(rows))
	for _, row := range rows {
		out = append(out, ServiceMetric{
			Service: str(row, "service"),
			Calls:   toInt64Val(row, "calls"),
			Errors:  toInt64Val(row, "errs"),
			AvgMS:   toFloatVal(row, "avg_ms"),
		})
	}
	return out, nil
}
