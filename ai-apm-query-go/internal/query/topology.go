package query

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// TopologyScope 是 topology 资源域的租户/集群/服务作用域（canonical UUID 已由鉴权层解析）。
// Services 为授权服务范围；nil/空 = 不限制（全量）。
type TopologyScope struct {
	TenantID  string
	ClusterID string
	Services  []string
}

// ServiceREDStat 单个服务的 RED 聚合（DashboardStats 服务统计）。
type ServiceREDStat struct {
	Service  string
	Calls    int64
	Errors   int64
	LatSumNs int64
}

// TrendPoint 小时趋势点（DashboardStats 近 24h 调用/错误趋势）。
type TrendPoint struct {
	T      string
	Calls  int64
	Errors int64
}

// ErrorStat 单个服务的错误计数（DashboardStats TOP 错误分布）。
type ErrorStat struct {
	Service string
	Errors  int64
}

// TopologyEdge 一条服务调用边（GlobalTopology / SyncTopologyCatalog）。
type TopologyEdge struct {
	Source string  `json:"source"`
	Target string  `json:"target"`
	Calls  int64   `json:"calls"`
	Errors int64   `json:"errors"`
	AvgNs  float64 `json:"avg_ns"`
}

// TopologyNode 一个服务节点聚合（GlobalTopology）。
type TopologyNode struct {
	Service string  `json:"service"`
	Calls   int64   `json:"calls"`
	Errors  int64   `json:"errors"`
	AvgNs   float64 `json:"avg_ns"`
}

// MiddlewareDependency 是服务到中间件的结构化事实行，供知识图谱构建使用。
type MiddlewareDependency struct {
	Service  string `json:"service_name"`
	Database string `json:"db_system"`
	Calls    int64  `json:"calls"`
}

// ServiceNS 一个服务→namespace 聚合行（GlobalTopology ns 标注）。
type ServiceNS struct {
	Service   string
	Namespace string
	Calls     int64
}

// CatalogNode 一个目录节点聚合（SyncTopologyCatalog）。
type CatalogNode struct {
	Service string
	Calls   int64
}

// TraceServiceSeq 一个 trace 内服务的首次出现时序（SyncTopologyCatalog 建边用）。
type TraceServiceSeq struct {
	TraceID string
	Service string
	FirstTS int64
}

// TopologyRepository 是 topology 资源域的 domain repository（V9.2 Phase 6）。
// topology 的 SoT 固定 ClickHouse（service_topology / trace_spans，冻结职责），
// 无 SoT 切换。SQL ownership 在此：handler 不组业务 SQL，只提交 typed request。
type TopologyRepository struct {
	ch        *ClickHouseRepo
	traceRepo *TraceRepository
}

// NewTopologyRepository 构造 topology repository，共享 ClickHouseExecutor。
func NewTopologyRepository(ch *ClickHouseRepo) *TopologyRepository {
	return &TopologyRepository{ch: ch, traceRepo: NewTraceRepository(ch)}
}

// topoServiceWhere 构造基于 service_name 列的 tenant/cluster/service 过滤子句。
// 对应 handler 原 scopeServicesClause（安全 P1-2 最小实现）。
func topoServiceWhere(scope TopologyScope, prefix string) string {
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
		parts = append(parts, prefix+"service_name IN ("+strings.Join(quoted, ",")+")")
	}
	return strings.Join(parts, " AND ")
}

// topoEdgeWhere 构造基于 source_service/target_service 列的过滤子句。
// 安全(P1-2)：scope.Services 非空时边两端都要在授权范围内。
func topoEdgeWhere(scope TopologyScope) string {
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
		in := "(" + strings.Join(quoted, ",") + ")"
		parts = append(parts, "source_service IN "+in+" AND target_service IN "+in)
	}
	return strings.Join(parts, " AND ")
}

// ServiceREDStats 查询近 24h 各服务 RED 聚合（DashboardStats 主统计）。
func (r *TopologyRepository) ServiceREDStats(ctx context.Context, scope TopologyScope) ([]ServiceREDStat, error) {
	sql := fmt.Sprintf(
		"SELECT service_name, count() as calls, countIf(is_error=1) as errors, sum(duration_ns) as lat_sum "+
			"FROM observability.trace_spans WHERE %s AND service_name != '' AND date >= today()-1 "+
			"GROUP BY service_name ORDER BY calls DESC LIMIT 50",
		topoServiceWhere(scope, ""))
	rows, err := r.ch.QueryJSON(ctx, sql)
	if err != nil {
		return nil, err
	}
	out := make([]ServiceREDStat, 0, len(rows))
	for _, row := range rows {
		out = append(out, ServiceREDStat{
			Service:  str(row, "service_name"),
			Calls:    toInt64Val(row, "calls"),
			Errors:   toInt64Val(row, "errors"),
			LatSumNs: toInt64Val(row, "lat_sum"),
		})
	}
	return out, nil
}

// DistinctTopologyServices 查询拓扑目录中出现的去重服务集合（DashboardStats topology_services）。
func (r *TopologyRepository) DistinctTopologyServices(ctx context.Context, scope TopologyScope) ([]string, error) {
	ew := topoEdgeWhere(scope)
	sql := fmt.Sprintf(
		"SELECT DISTINCT source_service AS s FROM observability.service_topology WHERE %s AND s != '' "+
			"UNION DISTINCT SELECT DISTINCT target_service AS s FROM observability.service_topology WHERE %s AND s != ''",
		ew, ew)
	rows, err := r.ch.QueryJSON(ctx, sql)
	if err != nil {
		return nil, err
	}
	set := make([]string, 0, len(rows))
	for _, row := range rows {
		if s := str(row, "s"); s != "" {
			set = append(set, s)
		}
	}
	return set, nil
}

// EdgeCount 查询近 1440 分钟去重后的拓扑边数（DashboardStats edges，自环不计）。
func (r *TopologyRepository) EdgeCount(ctx context.Context, scope TopologyScope) (int64, error) {
	sql := fmt.Sprintf(
		"SELECT count() AS cnt FROM (SELECT source_service, target_service FROM observability.service_topology "+
			"WHERE %s AND date >= today()-1 AND time_bucket >= now() - INTERVAL 1440 MINUTE "+
			"AND source_service != '' AND target_service != '' AND source_service != target_service "+
			"GROUP BY source_service, target_service)",
		topoEdgeWhere(scope))
	rows, err := r.ch.QueryJSON(ctx, sql)
	if err != nil {
		return 0, err
	}
	if len(rows) == 0 {
		return 0, nil
	}
	return toInt64Val(rows[0], "cnt"), nil
}

// EdgeCountWithTraceFallback 与 GlobalTopology 使用同一套真实链路兜底：
// service_topology 尚未同步时，从 parent_span_id 或 trace 内服务时序重建边，
// 避免总览显示 0 而拓扑页已有真实调用边。
func (r *TopologyRepository) EdgeCountWithTraceFallback(ctx context.Context, scope TopologyScope, minutes int) (int64, error) {
	n, err := r.EdgeCount(ctx, scope)
	if err != nil {
		return 0, err
	}
	if n > 0 {
		return n, nil
	}
	for _, loader := range []func(context.Context, TopologyScope, int) ([]TopologyEdge, error){r.ParentSpanEdges, r.SequenceEdges} {
		edges, ferr := loader(ctx, scope, minutes)
		if ferr != nil || len(edges) == 0 {
			continue
		}
		seen := make(map[string]struct{}, len(edges))
		for _, edge := range edges {
			if edge.Source == "" || edge.Target == "" || edge.Source == edge.Target {
				continue
			}
			seen[edge.Source+"\x00"+edge.Target] = struct{}{}
		}
		return int64(len(seen)), nil
	}
	return 0, nil
}

// P95Latency 查询近 24h 全局 P95 延迟（ms）（DashboardStats latency_p95）。
func (r *TopologyRepository) P95Latency(ctx context.Context, scope TopologyScope) (float64, error) {
	sql := fmt.Sprintf(
		"SELECT round(quantile(0.95)(duration_ns)/1000000, 2) AS p95_ms FROM observability.trace_spans WHERE %s AND date >= today()-1",
		topoServiceWhere(scope, ""))
	rows, err := r.ch.QueryJSON(ctx, sql)
	if err != nil {
		return 0, err
	}
	if len(rows) == 0 {
		return 0, nil
	}
	return toFloatVal(rows[0], "p95_ms"), nil
}

// HourlyTrend 查询近 24h 按小时的调用/错误趋势（DashboardStats trend）。
func (r *TopologyRepository) HourlyTrend(ctx context.Context, scope TopologyScope) ([]TrendPoint, error) {
	sql := fmt.Sprintf(
		"SELECT toString(toStartOfHour(start_time)) AS t, count() AS calls, countIf(is_error=1) AS errors "+
			"FROM observability.trace_spans WHERE %s AND date >= today()-1 GROUP BY t ORDER BY t LIMIT 24",
		topoServiceWhere(scope, ""))
	rows, err := r.ch.QueryJSON(ctx, sql)
	if err != nil {
		return nil, err
	}
	out := make([]TrendPoint, 0, len(rows))
	for _, row := range rows {
		out = append(out, TrendPoint{
			T:      str(row, "t"),
			Calls:  toInt64Val(row, "calls"),
			Errors: toInt64Val(row, "errors"),
		})
	}
	return out, nil
}

// TopErrors 查询 TOP 错误服务分布（DashboardStats top_errors）。
func (r *TopologyRepository) TopErrors(ctx context.Context, scope TopologyScope, limit int) ([]ErrorStat, error) {
	if limit <= 0 {
		limit = 10
	}
	sql := fmt.Sprintf(
		"SELECT service_name AS s, countIf(is_error=1) AS errors FROM observability.trace_spans "+
			"WHERE %s AND date >= today()-1 AND is_error=1 GROUP BY s ORDER BY errors DESC LIMIT %d",
		topoServiceWhere(scope, ""), limit)
	rows, err := r.ch.QueryJSON(ctx, sql)
	if err != nil {
		return nil, err
	}
	out := make([]ErrorStat, 0, len(rows))
	for _, row := range rows {
		out = append(out, ErrorStat{Service: str(row, "s"), Errors: toInt64Val(row, "errors")})
	}
	return out, nil
}

// GlobalEdges 查询窗口内服务调用边（GlobalTopology 主边聚合：service_topology）。
func (r *TopologyRepository) GlobalEdges(ctx context.Context, scope TopologyScope, minutes int) ([]TopologyEdge, error) {
	dateCond := "date >= today() - " + fmt.Sprint(minutes/1440)
	sql := fmt.Sprintf(
		"SELECT source_service, target_service, sum(call_count) AS calls, sum(error_count) AS errs, avg(avg_duration_ns) AS avg_ns "+
			"FROM observability.service_topology WHERE %s AND time_bucket >= now() - INTERVAL %d MINUTE AND %s "+
			"AND source_service != '' AND target_service != '' AND source_service != target_service "+
			"GROUP BY source_service, target_service ORDER BY calls DESC LIMIT 200",
		topoEdgeWhere(scope), minutes, dateCond)
	rows, err := r.ch.QueryJSON(ctx, sql)
	if err != nil {
		return nil, err
	}
	return parseTopologyEdges(rows), nil
}

// GlobalEdgesWithTraceFallback returns the materialized dependency projection
// when it is available and otherwise derives a bounded, read-only edge view
// from the Trace SoT. The fallback stays in the topology repository so
// callers do not grow a second SQL owner or bypass tenant/cluster scope.
// Backend errors are propagated when both the projection and trace derivations
// are unavailable.
func (r *TopologyRepository) GlobalEdgesWithTraceFallback(ctx context.Context, scope TopologyScope, minutes int) ([]TopologyEdge, error) {
	edges, err := r.GlobalEdges(ctx, scope, minutes)
	if err != nil {
		var queryErr *QueryError
		if !errors.As(err, &queryErr) || queryErr.Code != NoDataCode {
			return nil, err
		}
		edges = []TopologyEdge{}
	}
	materialized := make([]TopologyEdge, 0, len(edges))
	for _, edge := range edges {
		if edge.Source == "" || edge.Target == "" || edge.Source == edge.Target {
			continue
		}
		materialized = append(materialized, edge)
	}
	if len(materialized) > 0 {
		return materialized, nil
	}

	var fallbackErr error
	for _, loader := range []func(context.Context, TopologyScope, int) ([]TopologyEdge, error){r.ParentSpanEdges, r.SequenceEdges} {
		derived, derr := loader(ctx, scope, minutes)
		if derr != nil {
			var queryErr *QueryError
			if errors.As(derr, &queryErr) && queryErr.Code == NoDataCode {
				continue
			}
			fallbackErr = derr
			continue
		}
		if len(derived) > 0 {
			return derived, nil
		}
	}
	if fallbackErr != nil {
		return nil, fallbackErr
	}
	return []TopologyEdge{}, nil
}

// ParentSpanEdges 用 parent_span_id self-join 重建调用边（GlobalTopology 兜底 1）。
func (r *TopologyRepository) ParentSpanEdges(ctx context.Context, scope TopologyScope, minutes int) ([]TopologyEdge, error) {
	var parts []string
	parts = append(parts, "s1.tenant_id='"+scope.TenantID+"'")
	if scope.ClusterID != "" {
		parts = append(parts, "s1.cluster_id='"+scope.ClusterID+"'")
	}
	if len(scope.Services) > 0 {
		quoted := make([]string, 0, len(scope.Services))
		for _, s := range scope.Services {
			quoted = append(quoted, sqlStr(s))
		}
		in := "(" + strings.Join(quoted, ",") + ")"
		parts = append(parts, "s1.service_name IN "+in+" AND s2.service_name IN "+in)
	}
	aliasDate := "date >= today() - " + fmt.Sprint(minutes/1440)
	sql := fmt.Sprintf(
		"SELECT s1.service_name AS source_service, s2.service_name AS target_service, count() AS calls, 0 AS errs, avg(s2.duration_ns) AS avg_ns "+
			"FROM observability.trace_spans AS s1 JOIN observability.trace_spans AS s2 "+
			"ON s1.trace_id = s2.trace_id AND s1.span_id = s2.parent_span_id "+
			"WHERE %s AND s1.start_time >= now() - INTERVAL %d MINUTE AND s1.%s AND s2.%s "+
			"GROUP BY s1.service_name, s2.service_name ORDER BY calls DESC LIMIT 200",
		strings.Join(parts, " AND "), minutes, aliasDate, aliasDate)
	rows, err := r.ch.QueryJSON(ctx, sql)
	if err != nil {
		return nil, err
	}
	return parseTopologyEdges(rows), nil
}

// SequenceEdges 用 lagInFrame 窗口函数按 trace 内服务时序重建相邻调用边（GlobalTopology 兜底 2）。
func (r *TopologyRepository) SequenceEdges(ctx context.Context, scope TopologyScope, minutes int) ([]TopologyEdge, error) {
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
	dateCond := "date >= today() - " + fmt.Sprint(minutes/1440)
	sql := fmt.Sprintf(
		"SELECT source_service, target_service, count() AS calls, 0 AS errs, avg(target_dur_ns) AS avg_ns FROM ( "+
			"  SELECT service_name AS target_service, "+
			"         lagInFrame(service_name, 1, '') OVER (ORDER BY trace_id, rn) AS source_service, "+
			"         duration_ns AS target_dur_ns "+
			"  FROM ( "+
			"    SELECT trace_id, service_name, duration_ns, "+
			"           row_number() OVER (PARTITION BY trace_id ORDER BY start_time) AS rn "+
			"    FROM observability.trace_spans "+
			"    WHERE %s AND start_time >= now() - INTERVAL %d MINUTE AND %s "+
			"  ) "+
			") WHERE source_service != '' AND source_service != target_service "+
			"GROUP BY source_service, target_service ORDER BY calls DESC LIMIT 200",
		strings.Join(parts, " AND "), minutes, dateCond)
	rows, err := r.ch.QueryJSON(ctx, sql)
	if err != nil {
		return nil, err
	}
	return parseTopologyEdges(rows), nil
}

// GlobalNodes 查询窗口内各服务节点聚合（GlobalTopology 节点）。
func (r *TopologyRepository) GlobalNodes(ctx context.Context, scope TopologyScope, minutes int) ([]TopologyNode, error) {
	dateCond := "date >= today() - " + fmt.Sprint(minutes/1440)
	sql := fmt.Sprintf(
		"SELECT service_name AS service, count() AS calls, countIf(is_error=1) AS errs, avg(duration_ns) AS avg_ns "+
			"FROM observability.trace_spans WHERE %s AND start_time >= now() - INTERVAL %d MINUTE AND %s "+
			"GROUP BY service_name ORDER BY calls DESC LIMIT 200",
		topoServiceWhere(scope, ""), minutes, dateCond)
	rows, err := r.ch.QueryJSON(ctx, sql)
	if err != nil {
		return nil, err
	}
	out := make([]TopologyNode, 0, len(rows))
	for _, row := range rows {
		out = append(out, TopologyNode{
			Service: str(row, "service"),
			Calls:   toInt64Val(row, "calls"),
			Errors:  toInt64Val(row, "errs"),
			AvgNs:   toFloatVal(row, "avg_ns"),
		})
	}
	return out, nil
}

// GlobalServiceNS 查询窗口内服务→namespace 映射（GlobalTopology ns 标注，取调用量最大 ns）。
func (r *TopologyRepository) GlobalServiceNS(ctx context.Context, scope TopologyScope, minutes int) (map[string]string, error) {
	dateCond := "date >= today() - " + fmt.Sprint(minutes/1440)
	sql := fmt.Sprintf(
		"SELECT service_name AS service, k8s_namespace AS ns, count() AS calls "+
			"FROM observability.trace_spans WHERE %s AND start_time >= now() - INTERVAL %d MINUTE AND %s "+
			"GROUP BY service_name, k8s_namespace ORDER BY calls DESC",
		topoServiceWhere(scope, ""), minutes, dateCond)
	rows, err := r.ch.QueryJSON(ctx, sql)
	if err != nil {
		return nil, err
	}
	best := map[string]int64{}
	out := map[string]string{}
	for _, row := range rows {
		svc := str(row, "service")
		if svc == "" {
			continue
		}
		calls := toInt64Val(row, "calls")
		if prev, ok := best[svc]; !ok || calls > prev {
			best[svc] = calls
			out[svc] = str(row, "ns")
		}
	}
	return out, nil
}

// CatalogNodes 查询近 24h 各服务调用量（SyncTopologyCatalog 节点聚合）。
func (r *TopologyRepository) CatalogNodes(ctx context.Context, scope TopologyScope) ([]CatalogNode, error) {
	sql := fmt.Sprintf(
		"SELECT service_name AS service, count() AS calls FROM observability.trace_spans "+
			"WHERE %s AND date >= today()-1 GROUP BY service_name ORDER BY calls DESC LIMIT 500",
		topoServiceWhere(scope, ""))
	rows, err := r.ch.QueryJSON(ctx, sql)
	if err != nil {
		return nil, err
	}
	out := make([]CatalogNode, 0, len(rows))
	for _, row := range rows {
		out = append(out, CatalogNode{Service: str(row, "service"), Calls: toInt64Val(row, "calls")})
	}
	return out, nil
}

// TraceServiceSeq 查询近 24h 每个 trace 内服务的最早出现时间（SyncTopologyCatalog 建边用）。
func (r *TopologyRepository) TraceServiceSeq(ctx context.Context, scope TopologyScope) ([]TraceServiceSeq, error) {
	sql := fmt.Sprintf(
		"SELECT trace_id, service_name, toUnixTimestamp(min(start_time)) AS first_ts "+
			"FROM observability.trace_spans WHERE %s AND date >= today()-1 GROUP BY trace_id, service_name",
		topoServiceWhere(scope, ""))
	rows, err := r.ch.QueryJSON(ctx, sql)
	if err != nil {
		return nil, err
	}
	out := make([]TraceServiceSeq, 0, len(rows))
	for _, row := range rows {
		out = append(out, TraceServiceSeq{
			TraceID: str(row, "trace_id"),
			Service: str(row, "service_name"),
			FirstTS: toInt64Val(row, "first_ts"),
		})
	}
	return out, nil
}

// MiddlewareDependencies 查询近 24h 服务调用的 db_system 聚合。
func (r *TopologyRepository) MiddlewareDependencies(ctx context.Context, scope TopologyScope) ([]MiddlewareDependency, error) {
	where := topoServiceWhere(scope, "") + " AND db_system != '' AND start_time >= now() - INTERVAL 24 HOUR"
	sql := fmt.Sprintf(
		"SELECT service_name, db_system, count() AS calls FROM observability.trace_spans WHERE %s GROUP BY service_name, db_system LIMIT 200",
		where)
	rows, err := r.ch.QueryJSON(ctx, sql)
	if err != nil {
		return nil, err
	}
	out := make([]MiddlewareDependency, 0, len(rows))
	for _, row := range rows {
		out = append(out, MiddlewareDependency{
			Service: str(row, "service_name"), Database: str(row, "db_system"), Calls: toInt64Val(row, "calls"),
		})
	}
	return out, nil
}

// NodeMetrics 一个节点的指标卡聚合（TopologyNodeDetail）。
type NodeMetrics struct {
	Calls  int64
	Errors int64
	AvgMS  float64
	MaxMS  float64
}

// NodeTrendPoint 一个节点的分钟级趋势点（TopologyNodeDetail）。
type NodeTrendPoint struct {
	T      string
	Calls  int64
	Errors int64
	AvgMS  float64
}

// NodeTrace 一个节点的调用链摘要（TopologyNodeDetail）。
type NodeTrace struct {
	TraceID string
	Start   string
	End     string
	Spans   int64
	MaxMS   float64
	Errors  int64
}

// NodeSpan 一个节点的最近 span 明细（TopologyNodeDetail）。
type NodeSpan struct {
	SpanID        string
	TraceID       string
	StartTime     string
	ServiceName   string
	OperationName string
	MS            float64
	IsError       int64
	HTTPURL       string
}

// NodeDetail 一个拓扑节点的详情聚合（指标 + 趋势 + 调用链 + span）。
type NodeDetail struct {
	Metrics NodeMetrics
	Trend   []NodeTrendPoint
	Traces  []NodeTrace
	Spans   []NodeSpan
}

// NodeDetail 查询指定服务节点最近 minutes 分钟的详情聚合（TopologyNodeDetail 抽屉）。
// SQL ownership 在 repository；handler 不再持有业务 SQL。
func (r *TopologyRepository) NodeDetail(ctx context.Context, scope TopologyScope, name string, minutes int) (NodeDetail, error) {
	where := topoServiceWhere(scope, "")
	svc := " AND service_name=" + sqlStr(name)
	win := fmt.Sprintf(" AND start_time >= now() - INTERVAL %d MINUTE", minutes)

	var out NodeDetail

	// 1. 指标卡
	metricSQL := "SELECT count() as calls, countIf(is_error=1) as errors, avg(duration_ns)/1000000 as avg_ms, max(duration_ns)/1000000 as max_ms " +
		"FROM observability.trace_spans WHERE " + where + svc + win
	if mrows, err := r.ch.QueryJSON(ctx, metricSQL); err == nil && len(mrows) > 0 {
		out.Metrics = NodeMetrics{
			Calls:  toInt64Val(mrows[0], "calls"),
			Errors: toInt64Val(mrows[0], "errors"),
			AvgMS:  toFloatVal(mrows[0], "avg_ms"),
			MaxMS:  toFloatVal(mrows[0], "max_ms"),
		}
	}

	// 2. 分钟级趋势
	trendSQL := "SELECT toStartOfMinute(start_time) as t, count() as calls, countIf(is_error=1) as errors, avg(duration_ns)/1000000 as avg_ms " +
		"FROM observability.trace_spans WHERE " + where + svc + win + " GROUP BY t ORDER BY t"
	if trows, err := r.ch.QueryJSON(ctx, trendSQL); err == nil {
		for _, row := range trows {
			out.Trend = append(out.Trend, NodeTrendPoint{
				T:      str(row, "t"),
				Calls:  toInt64Val(row, "calls"),
				Errors: toInt64Val(row, "errors"),
				AvgMS:  toFloatVal(row, "avg_ms"),
			})
		}
	}

	// 3. 调用链列表。与主 Trace 列表保持同一 Summary/Index 数据路径：
	// 先按服务和时间窗读取少量候选 Trace，再只对这些 Trace 回查精确错误数。
	// 禁止在 trace_spans 上先 GROUP BY trace_id 再 LIMIT，避免服务抽屉复现
	// 主 Trace 列表的高基数聚合 OOM。
	if r.traceRepo != nil {
		traceMinutes := minutes
		if traceMinutes < 1 {
			traceMinutes = 1
		}
		traces, err := r.traceRepo.FindTraces(ctx, TraceQuery{
			TenantID:  scope.TenantID,
			ClusterID: scope.ClusterID,
			Service:   name,
			Minutes:   traceMinutes,
			Limit:     20,
		})
		if err == nil {
			ids := make([]string, 0, len(traces))
			for _, trace := range traces {
				ids = append(ids, trace.TraceID)
			}
			errorsByTrace, _ := r.traceRepo.TraceErrorCounts(ctx, scope.TenantID, scope.ClusterID, ids)
			for _, trace := range traces {
				out.Traces = append(out.Traces, NodeTrace{
					TraceID: trace.TraceID,
					Start:   trace.Start.Format("2006-01-02 15:04:05"),
					End:     trace.End.Format("2006-01-02 15:04:05"),
					Spans:   int64(trace.Spans),
					MaxMS:   trace.MaxMS,
					Errors:  errorsByTrace[trace.TraceID],
				})
			}
		}
	}

	// 4. 最近 5 条 span 明细
	spanSQL := "SELECT span_id, trace_id, start_time, service_name, operation_name, duration_ns/1000000 as ms, is_error, http_url " +
		"FROM observability.trace_spans WHERE " + where + svc + win + " ORDER BY start_time DESC LIMIT 5"
	if srows, err := r.ch.QueryJSON(ctx, spanSQL); err == nil {
		for _, row := range srows {
			out.Spans = append(out.Spans, NodeSpan{
				SpanID:        str(row, "span_id"),
				TraceID:       str(row, "trace_id"),
				StartTime:     str(row, "start_time"),
				ServiceName:   str(row, "service_name"),
				OperationName: str(row, "operation_name"),
				MS:            toFloatVal(row, "ms"),
				IsError:       toInt64Val(row, "is_error"),
				HTTPURL:       str(row, "http_url"),
			})
		}
	}

	return out, nil
}

// parseTopologyEdges 从 JSONEachRow 行解析边。
func parseTopologyEdges(rows []map[string]interface{}) []TopologyEdge {
	out := make([]TopologyEdge, 0, len(rows))
	for _, row := range rows {
		out = append(out, TopologyEdge{
			Source: str(row, "source_service"),
			Target: str(row, "target_service"),
			Calls:  toInt64Val(row, "calls"),
			Errors: toInt64Val(row, "errs"),
			AvgNs:  toFloatVal(row, "avg_ns"),
		})
	}
	return out
}

// toInt64Val 安全取 map 中整型值（JSON 可能为 float64 或 string）。
func toInt64Val(m map[string]interface{}, key string) int64 {
	v, ok := m[key]
	if !ok || v == nil {
		return 0
	}
	switch x := v.(type) {
	case float64:
		return int64(x)
	case string:
		var n int64
		fmt.Sscanf(x, "%d", &n)
		return n
	default:
		return 0
	}
}

// toFloatVal 安全取 map 中浮点值（JSON 可能为 float64 或 string）。
func toFloatVal(m map[string]interface{}, key string) float64 {
	v, ok := m[key]
	if !ok || v == nil {
		return 0
	}
	switch x := v.(type) {
	case float64:
		return x
	case string:
		var f float64
		fmt.Sscanf(x, "%f", &f)
		return f
	default:
		return 0
	}
}
