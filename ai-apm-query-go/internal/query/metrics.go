package query

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// Scope 是查询的租户/集群作用域。tenant/cluster 为 canonical UUID（已由鉴权层解析）。
type Scope struct {
	TenantID  string
	ClusterID string
	Services  []string // 可选：限定服务范围
}

// REDPoint 一条 service RED（Rate/Error/Duration）时序列采样点。
type REDPoint struct {
	T          time.Time
	CallCount  int
	ErrorCount int
	AvgMS      float64
}

// MetricsRepository 是 metrics 资源域的 domain repository（V9.2 Phase 6 统一查询层）。
// SQL ownership 在此：handler 不组业务 SQL，只提交 typed request。
//
// SoT 语义（冻结职责）：
//   - Raw Metrics：new mode → VictoriaMetrics（SoT）；legacy → ClickHouse trace_spans（transition path）。
//   - 禁止 new mode 失败时 fallback ClickHouse（unavailable 即 unavailable）。
type MetricsRepository struct {
	ch     *ClickHouseRepo
	vm     *VictoriaMetricsReader // new mode 的 Raw Metrics reader；nil 时 new mode 返回 unavailable
	router *SourceRouter
}

// NewMetricsRepository 构造 metrics repository，共享 ClickHouseExecutor。
func NewMetricsRepository(ch *ClickHouseRepo) *MetricsRepository {
	return &MetricsRepository{ch: ch, router: NewSourceRouter(ModeLegacy)}
}

// WithVMRouter 注入 VictoriaMetrics reader 与 reader 模式（供 new mode 路由）。
func (r *MetricsRepository) WithVMRouter(vm *VictoriaMetricsReader, mode ReaderMode) *MetricsRepository {
	r.vm = vm
	r.router = NewSourceRouter(mode)
	return r
}

// ServiceRED 查询某服务最近 N 分钟的 RED 时序列（按分钟分组）。
// 返回规范化时序列；无数据返回 empty slice（NoData 由调用方处理）。
func (r *MetricsRepository) ServiceRED(ctx context.Context, scope Scope, service string, minutes int) ([]REDPoint, error) {
	// P6.3.1：new mode → VictoriaMetrics（SoT）；无 VM reader 时返回 unavailable，禁止 fallback ClickHouse。
	if r.router != nil && r.router.Mode() == ModeNew {
		if r.vm == nil {
			return nil, Unavailable("raw metrics new SoT (VictoriaMetrics) reader not configured")
		}
		return r.vm.ServiceRED(ctx, VMQuery{
			TenantID: scope.TenantID, ClusterID: scope.ClusterID, Service: service, Minutes: minutes,
		})
	}
	q := fmt.Sprintf(
		"SELECT toStartOfMinute(start_time) as t, count() as call_count, countIf(is_error=1) as error_count, "+
			"avg(duration_ns)/1000000 as avg_ms FROM observability.trace_spans "+
			"WHERE %s AND service_name=%s AND start_time >= now() - INTERVAL %d MINUTE GROUP BY t ORDER BY t",
		scopeClause(scope), sqlStr(service), minutes)

	body, err := r.ch.Query(ctx, q)
	if err != nil {
		return nil, err
	}
	return parseREDPoints(body)
}

// scopeClause 构造 tenant/cluster/service 过滤子句（SQL ownership 在 repository）。
// scope.Services 非空时追加 service_name IN (...) 过滤。
func scopeClause(scope Scope) string {
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

// REDPointDetailed 一条带分位数（p50/p95/p99）的 RED 时序列采样点。
type REDPointDetailed struct {
	T          time.Time
	CallCount  int
	ErrorCount int
	AvgMS      float64
	P50MS      float64
	P95MS      float64
	P99MS      float64
}

// ServiceREDDetailed 查询服务（或服务集）近 1 天的 RED 时序列（含分位数，按分钟分组）。
// 对应原 QueryMetrics service 路径，SQL ownership 在 repository。
func (r *MetricsRepository) ServiceREDDetailed(ctx context.Context, scope Scope, service string, services []string) ([]REDPointDetailed, error) {
	if service != "" {
		scope.Services = []string{service}
	} else if len(services) > 0 {
		scope.Services = services
	}
	q := fmt.Sprintf(
		"SELECT toStartOfMinute(start_time) as t, count() as call_count, "+
			"countIf(is_error=1) as error_count, avg(duration_ns)/1000000 as avg_ms, "+
			"quantile(0.50)(duration_ns)/1000000 as p50_ms, "+
			"quantile(0.95)(duration_ns)/1000000 as p95_ms, "+
			"quantile(0.99)(duration_ns)/1000000 as p99_ms "+
			"FROM observability.trace_spans WHERE %s AND date >= today()-1 GROUP BY t ORDER BY t",
		scopeClause(scope))

	body, err := r.ch.Query(ctx, q)
	if err != nil {
		return nil, err
	}
	return parseREDPointsDetailed(body)
}

// ServiceRuleValue 计算某服务最近 minutes 分钟的服务级阈值指标（SLO 规则评估用）：
// error_rate（%）、calls / call_count、latency_p95（ms）、latency_p99（ms）。
// 语义与原 handler evalServiceTraceRED 完全一致（全局跨租户，无 tenant/cluster 过滤，
// 仅按 service + 时间窗），SQL ownership 在 repository。
func (r *MetricsRepository) ServiceRuleValue(ctx context.Context, service, metric string, minutes int) (float64, error) {
	switch metric {
	case "error_rate":
		sql := fmt.Sprintf(
			"SELECT countIf(is_error=1) as errors, count() as total FROM observability.trace_spans "+
				"WHERE service_name=%s AND start_time >= now() - INTERVAL %d MINUTE", sqlStr(service), minutes)
		rows, err := r.ch.QueryJSON(ctx, sql)
		if err != nil {
			return 0, err
		}
		if len(rows) == 0 {
			return 0, nil
		}
		errors := toFloatVal(rows[0], "errors")
		total := toFloatVal(rows[0], "total")
		if total > 0 {
			return (errors / total) * 100, nil
		}
		return 0, nil
	case "calls", "call_count":
		sql := fmt.Sprintf(
			"SELECT count() as cnt FROM observability.trace_spans WHERE service_name=%s AND start_time >= now() - INTERVAL %d MINUTE",
			sqlStr(service), minutes)
		rows, err := r.ch.QueryJSON(ctx, sql)
		if err != nil {
			return 0, err
		}
		if len(rows) == 0 {
			return 0, nil
		}
		return toFloatVal(rows[0], "cnt"), nil
	case "latency_p95":
		sql := fmt.Sprintf(
			"SELECT quantile(0.95)(duration_ns)/1000000 as p95_ms FROM observability.trace_spans WHERE service_name=%s AND start_time >= now() - INTERVAL %d MINUTE",
			sqlStr(service), minutes)
		rows, err := r.ch.QueryJSON(ctx, sql)
		if err != nil {
			return 0, err
		}
		if len(rows) == 0 {
			return 0, nil
		}
		return toFloatVal(rows[0], "p95_ms"), nil
	case "latency_p99":
		sql := fmt.Sprintf(
			"SELECT quantile(0.99)(duration_ns)/1000000 as p99_ms FROM observability.trace_spans WHERE service_name=%s AND start_time >= now() - INTERVAL %d MINUTE",
			sqlStr(service), minutes)
		rows, err := r.ch.QueryJSON(ctx, sql)
		if err != nil {
			return 0, err
		}
		if len(rows) == 0 {
			return 0, nil
		}
		return toFloatVal(rows[0], "p99_ms"), nil
	default:
		return 0, fmt.Errorf("unknown metric: %s", metric)
	}
}

// parseREDPointsDetailed 解析带分位数的 RED 时序列。
func parseREDPointsDetailed(body []byte) ([]REDPointDetailed, error) {
	var points []REDPointDetailed
	for _, line := range strings.Split(string(body), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		cols := strings.Split(line, "\t")
		if len(cols) < 7 {
			continue
		}
		t, err := parseCHTime(cols[0])
		if err != nil {
			continue
		}
		var p REDPointDetailed
		fmt.Sscanf(cols[1], "%d", &p.CallCount)
		fmt.Sscanf(cols[2], "%d", &p.ErrorCount)
		fmt.Sscanf(cols[3], "%f", &p.AvgMS)
		fmt.Sscanf(cols[4], "%f", &p.P50MS)
		fmt.Sscanf(cols[5], "%f", &p.P95MS)
		fmt.Sscanf(cols[6], "%f", &p.P99MS)
		p.T = t
		points = append(points, p)
	}
	return points, nil
}

// sqlStr 为字符串字面量加引号（防注入；对齐 handler 原 chQuote 语义）。
func sqlStr(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "\\'") + "'"
}

// parseREDPoints 解析 ClickHouse TabSeparated 为 REDPoint 列表。
func parseREDPoints(body []byte) ([]REDPoint, error) {
	var points []REDPoint
	for _, line := range strings.Split(string(body), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		cols := strings.Split(line, "\t")
		if len(cols) < 4 {
			continue
		}
		t, err := parseCHTime(cols[0])
		if err != nil {
			continue
		}
		var call, errCnt int
		var avgMS float64
		fmt.Sscanf(cols[1], "%d", &call)
		fmt.Sscanf(cols[2], "%d", &errCnt)
		fmt.Sscanf(cols[3], "%f", &avgMS)
		points = append(points, REDPoint{T: t, CallCount: call, ErrorCount: errCnt, AvgMS: avgMS})
	}
	return points, nil
}

// parseCHTime 解析 ClickHouse datetime "2006-01-02 15:04:05" 为 UTC。
func parseCHTime(s string) (time.Time, error) {
	return time.ParseInLocation("2006-01-02 15:04:05", strings.TrimSpace(s), time.UTC)
}
