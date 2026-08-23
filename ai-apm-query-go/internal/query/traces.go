package query

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// TraceQuery 是 traces 资源域的规范化查询请求（SQL ownership 在 repository）。
type TraceQuery struct {
	TenantID  string
	ClusterID string
	Service   string
	Services  []string
	Keyword   string // 搜索 trace_id/operation/http_url
	Hours     int
	Limit     int
	Offset    int
}

// TraceSummary 一条 trace 的摘要（list 行）。
type TraceSummary struct {
	TraceID  string
	Start    time.Time
	End      time.Time
	Spans    int
	Services int
	MaxMS    float64
}

// Span 一条 span 详情。
type Span struct {
	SpanID       string
	ParentSpanID string
	ServiceName  string
	OperationName string
	SpanKind     string
	StartTime    time.Time
	MS           float64
	IsError      bool
}

// FindSpans 查询某 trace 的所有 span（按 start_time 排序）。trace SoT 固定 ClickHouse。
func (r *TraceRepository) FindSpans(ctx context.Context, tenantID, clusterID, traceID string) ([]Span, error) {
	var conds []string
	conds = append(conds, "tenant_id='"+tenantID+"'")
	if clusterID != "" {
		conds = append(conds, "cluster_id='"+clusterID+"'")
	}
	conds = append(conds, "trace_id='"+traceID+"'")
	sql := "SELECT span_id, parent_span_id, service_name, operation_name, span_kind, start_time, duration_ns/1000000 as ms, is_error " +
		"FROM observability.trace_spans WHERE " + strings.Join(conds, " AND ") + " ORDER BY start_time"

	body, err := r.ch.Query(ctx, sql)
	if err != nil {
		return nil, err
	}
	var out []Span
	for _, line := range strings.Split(string(body), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		cols := strings.Split(line, "\t")
		if len(cols) < 8 {
			continue
		}
		var sp Span
		sp.SpanID = cols[0]
		sp.ParentSpanID = cols[1]
		sp.ServiceName = cols[2]
		sp.OperationName = cols[3]
		sp.SpanKind = cols[4]
		if t, err := parseCHTime(cols[5]); err == nil {
			sp.StartTime = t
		}
		fmt.Sscanf(cols[6], "%f", &sp.MS)
		var isErr int
		fmt.Sscanf(cols[7], "%d", &isErr)
		sp.IsError = isErr == 1
		out = append(out, sp)
	}
	return out, nil
}

// TraceRuleValue 计算链路类规则标量（SLO 规则评估用），语义与原 handler traceMetricQuery 完全一致：
// 全局跨租户，近 5 分钟固定窗口。
//   - trace_latency：P99 延迟（ms）
//   - trace_error_rate：错误率 %
func (r *TraceRepository) TraceRuleValue(ctx context.Context, service, metric string) (float64, error) {
	svcClause := ""
	if service != "" {
		svcClause = " AND service_name=" + sqlStr(service)
	}
	var sql string
	if metric == "trace_latency" {
		sql = fmt.Sprintf(
			"SELECT quantile(0.99)(duration_ns)/1000000 FROM observability.trace_spans WHERE date >= today() AND start_time >= now() - INTERVAL 5 MINUTE%s",
			svcClause)
	} else {
		sql = fmt.Sprintf(
			"SELECT countIf(is_error=1) / count() * 100 FROM observability.trace_spans WHERE date >= today() AND start_time >= now() - INTERVAL 5 MINUTE%s",
			svcClause)
	}
	rows, err := r.ch.QueryJSON(ctx, sql)
	if err != nil {
		return 0, err
	}
	if len(rows) == 0 {
		return 0, nil
	}
	return scalarVal(rows[0]), nil
}

// TraceService 返回指定 trace 涉及的第一个服务名（TraceContext 血缘关联用）。
// 无该 trace / 无服务返回空串（NoData 由调用方容忍为"无关联服务"）。
func (r *TraceRepository) TraceService(ctx context.Context, tenantID, clusterID, traceID string) (string, error) {
	var conds []string
	conds = append(conds, "tenant_id='"+tenantID+"'")
	if clusterID != "" {
		conds = append(conds, "cluster_id='"+clusterID+"'")
	}
	conds = append(conds, "trace_id='"+traceID+"'")
	sql := "SELECT DISTINCT service_name FROM observability.trace_spans WHERE " +
		strings.Join(conds, " AND ") + " LIMIT 1"

	body, err := r.ch.Query(ctx, sql)
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(body), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		cols := strings.Split(line, "\t")
		if len(cols) >= 1 {
			return cols[0], nil
		}
	}
	return "", nil
}

// TraceRepository 是 traces 资源域的 domain repository（V9.2 Phase 6）。
// trace/edge SoT 固定 ClickHouse（冻结职责），无 SoT 切换。
type TraceRepository struct {
	ch *ClickHouseRepo
}

// NewTraceRepository 构造 traces repository。
func NewTraceRepository(ch *ClickHouseRepo) *TraceRepository {
	return &TraceRepository{ch: ch}
}

// FindTraces 列出 trace 摘要（按 start 倒序，支持分页/搜索/服务过滤）。
func (r *TraceRepository) FindTraces(ctx context.Context, q TraceQuery) ([]TraceSummary, error) {
	var conds []string
	conds = append(conds, "tenant_id='"+q.TenantID+"'")
	if q.ClusterID != "" {
		conds = append(conds, "cluster_id='"+q.ClusterID+"'")
	}
	if q.Service != "" {
		conds = append(conds, "service_name='"+q.Service+"'")
	}
	if len(q.Services) > 0 {
		quoted := make([]string, 0, len(q.Services))
		for _, s := range q.Services {
			quoted = append(quoted, sqlStr(s))
		}
		conds = append(conds, "service_name IN ("+strings.Join(quoted, ",")+")")
	}
	if q.Keyword != "" {
		kw := "'%" + q.Keyword + "%'"
		conds = append(conds, "(trace_id LIKE "+kw+" OR operation_name LIKE "+kw+" OR http_url LIKE "+kw+")")
	}
	if q.Hours >= 1 {
		conds = append(conds, fmt.Sprintf("start_time >= now() - INTERVAL %d HOUR", q.Hours))
	}
	if q.Limit <= 0 {
		q.Limit = 20
	}

	sql := "SELECT trace_id, min(start_time) as start, max(start_time) as end, count() as spans, " +
		"count(DISTINCT service_name) as services, max(duration_ns)/1000000 as max_ms " +
		"FROM observability.trace_spans WHERE " + strings.Join(conds, " AND ") +
		fmt.Sprintf(" GROUP BY trace_id ORDER BY start DESC LIMIT %d OFFSET %d", q.Limit, q.Offset)

	body, err := r.ch.Query(ctx, sql)
	if err != nil {
		return nil, err
	}
	var out []TraceSummary
	for _, line := range strings.Split(string(body), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		cols := strings.Split(line, "\t")
		if len(cols) < 6 {
			continue
		}
		var ts TraceSummary
		ts.TraceID = cols[0]
		if s, err := parseCHTime(cols[1]); err == nil {
			ts.Start = s
		}
		if e, err := parseCHTime(cols[2]); err == nil {
			ts.End = e
		}
		fmt.Sscanf(cols[3], "%d", &ts.Spans)
		fmt.Sscanf(cols[4], "%d", &ts.Services)
		fmt.Sscanf(cols[5], "%f", &ts.MaxMS)
		out = append(out, ts)
	}
	return out, nil
}
