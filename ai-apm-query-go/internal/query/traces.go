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
	// Minutes is an optional exact window for callers such as the service
	// detail drawer. When it is zero, Hours keeps the public trace-list
	// contract and its legacy 24-hour default.
	Minutes int
	Limit   int
	Offset  int
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
	SpanID        string
	ParentSpanID  string
	ServiceName   string
	OperationName string
	SpanKind      string
	StartTime     time.Time
	MS            float64
	IsError       bool
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

// TraceErrorCounts reads error counts only for an already bounded set of
// Trace IDs. It is intentionally separate from FindTraces: summary state
// keeps the list query cheap, while this small raw-span lookup preserves the
// exact error count shown by the service detail drawer.
func (r *TraceRepository) TraceErrorCounts(ctx context.Context, tenantID, clusterID string, traceIDs []string) (map[string]int64, error) {
	out := make(map[string]int64)
	if len(traceIDs) == 0 {
		return out, nil
	}
	conds := []string{"tenant_id=" + sqlStr(tenantID)}
	if clusterID != "" {
		conds = append(conds, "cluster_id="+sqlStr(clusterID))
	}
	quoted := make([]string, 0, len(traceIDs))
	for _, id := range traceIDs {
		quoted = append(quoted, sqlStr(id))
	}
	conds = append(conds, "trace_id IN ("+strings.Join(quoted, ",")+")")
	rows, err := r.ch.QueryJSON(ctx, "SELECT trace_id, sum(is_error) AS errors FROM observability.trace_spans WHERE "+strings.Join(conds, " AND ")+" GROUP BY trace_id")
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		id := str(row, "trace_id")
		if id == "" {
			continue
		}
		out[id] = toInt64Val(row, "errors")
	}
	return out, nil
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
	if q.Limit <= 0 {
		q.Limit = 20
	}
	if q.Offset < 0 {
		q.Offset = 0
	}
	// The UI always supplies a window. Keep an omitted window bounded as well,
	// so older clients cannot turn the index scan into an unbounded history scan.
	windowMinutes, windowExpr := traceWindow(q)
	days := (windowMinutes + 24*60 - 1) / (24 * 60)

	// First read only candidate IDs from the time-ordered lightweight index.
	// This keeps FINAL (which merges AggregateFunction states) limited to a
	// small candidate set instead of forcing it to merge millions of summaries.
	candidateLimit := (q.Limit + q.Offset) * 20
	if candidateLimit < 1000 {
		candidateLimit = 1000
	}
	if candidateLimit > 5000 {
		candidateLimit = 5000
	}
	traceIDs, err := r.findTraceCandidates(ctx, q, windowExpr, days, candidateLimit)
	if err != nil {
		return nil, err
	}
	if len(traceIDs) == 0 {
		return []TraceSummary{}, nil
	}

	for {
		var conds []string
		conds = append(conds, "tenant_id='"+q.TenantID+"'")
		conds = append(conds, fmt.Sprintf("date >= today() - INTERVAL %d DAY", days))
		conds = append(conds, "finalizeAggregation(start_state) >= now() - INTERVAL "+windowExpr)
		if q.ClusterID != "" {
			conds = append(conds, "cluster_id='"+q.ClusterID+"'")
		}
		quotedIDs := make([]string, 0, len(traceIDs))
		for _, id := range traceIDs {
			quotedIDs = append(quotedIDs, sqlStr(id))
		}
		conds = append(conds, "trace_id IN ("+strings.Join(quotedIDs, ",")+")")

		// FINAL is applied only after the index has reduced the candidate set.
		// The inner query finalizes bounded Summary rows; the outer GROUP BY merges
		// date partitions into one logical Trace without touching raw trace_spans.
		sql := "SELECT trace_id, min(trace_start) AS start, max(trace_end) AS end, " +
			"sum(span_count) AS spans, " +
			"length(arrayDistinct(arrayFlatten(groupArray(service_names)))) AS services, " +
			"max(max_ms) AS max_ms " +
			"FROM (SELECT trace_id, " +
			"finalizeAggregation(start_state) AS trace_start, " +
			"finalizeAggregation(end_state) AS trace_end, " +
			"finalizeAggregation(span_count_state) AS span_count, " +
			"finalizeAggregation(service_names_state) AS service_names, " +
			"finalizeAggregation(operation_names_state) AS operation_names, " +
			"finalizeAggregation(http_urls_state) AS http_urls, " +
			"finalizeAggregation(max_duration_state)/1000000 AS max_ms " +
			"FROM observability.trace_summary_state FINAL WHERE " + strings.Join(conds, " AND ") +
			") GROUP BY trace_id"
		var having []string
		serviceNames := "arrayDistinct(arrayFlatten(groupArray(service_names)))"
		if q.Service != "" {
			having = append(having, "has("+serviceNames+", "+sqlStr(q.Service)+")")
		}
		if len(q.Services) > 0 {
			quoted := make([]string, 0, len(q.Services))
			for _, s := range q.Services {
				quoted = append(quoted, sqlStr(s))
			}
			having = append(having, "hasAny("+serviceNames+", ["+strings.Join(quoted, ",")+"])")
		}
		if q.Keyword != "" {
			kw := sqlStr(q.Keyword)
			operations := "arrayDistinct(arrayFlatten(groupArray(operation_names)))"
			urls := "arrayDistinct(arrayFlatten(groupArray(http_urls)))"
			having = append(having, "(trace_id LIKE concat('%', "+kw+", '%') OR "+
				"arrayExists(x -> positionCaseInsensitive(x, "+kw+") > 0, "+operations+") OR "+
				"arrayExists(x -> positionCaseInsensitive(x, "+kw+") > 0, "+urls+"))")
		}
		if len(having) > 0 {
			sql += " HAVING " + strings.Join(having, " AND ")
		}
		sql += fmt.Sprintf(" ORDER BY start DESC LIMIT %d OFFSET %d", q.Limit, q.Offset)

		body, err := r.ch.Query(ctx, sql)
		if err != nil {
			if qe, ok := err.(*QueryError); !ok || qe.Code != NoDataCode {
				return nil, err
			}
			body = nil
		}
		out := parseTraceSummaries(body)
		if len(out) >= q.Limit || len(traceIDs) < candidateLimit || candidateLimit >= 5000 {
			return out, nil
		}
		candidateLimit *= 2
		if candidateLimit > 5000 {
			candidateLimit = 5000
		}
	}
}

func traceWindow(q TraceQuery) (int, string) {
	if q.Minutes > 0 {
		return q.Minutes, fmt.Sprintf("%d MINUTE", q.Minutes)
	}
	hours := q.Hours
	if hours < 1 {
		hours = 24
	}
	return hours * 60, fmt.Sprintf("%d HOUR", hours)
}

// findTraceCandidates reads the time-ordered index without a high-cardinality
// ClickHouse LIMIT BY. The index stores a negative nanosecond timestamp, so
// ascending physical order is newest-first. Reading one exact date partition
// at a time lets ClickHouse stop at the physical candidate limit while keeping
// the request count bounded by the number of date partitions (normally 1–2,
// rather than one request per five-minute bucket).
func (r *TraceRepository) findTraceCandidates(ctx context.Context, q TraceQuery, windowExpr string, days, candidateLimit int) ([]string, error) {
	indexReadLimit := candidateLimit * 2
	if indexReadLimit > 10000 {
		indexReadLimit = 10000
	}
	baseConds := []string{"tenant_id='" + q.TenantID + "'"}
	if q.ClusterID != "" {
		baseConds = append(baseConds, "cluster_id='"+q.ClusterID+"'")
	}
	if q.Service != "" {
		baseConds = append(baseConds, "service_name="+sqlStr(q.Service))
	}
	if len(q.Services) > 0 {
		quoted := make([]string, 0, len(q.Services))
		for _, s := range q.Services {
			quoted = append(quoted, sqlStr(s))
		}
		baseConds = append(baseConds, "service_name IN ("+strings.Join(quoted, ",")+")")
	}
	if q.Keyword != "" {
		kw := sqlStr(q.Keyword)
		baseConds = append(baseConds, "(trace_id LIKE concat('%', "+kw+", '%') OR search_text LIKE concat('%', "+kw+", '%'))")
	}

	seen := make(map[string]struct{})
	var ids []string
	for dayOffset := 0; dayOffset <= days && len(ids) < candidateLimit; dayOffset++ {
		conds := append([]string{}, baseConds...)
		conds = append(conds,
			fmt.Sprintf("date = today() - INTERVAL %d DAY", dayOffset),
			"latest_start >= now() - INTERVAL "+windowExpr,
		)
		indexSQL := "SELECT trace_id FROM observability.trace_summary_index WHERE " + strings.Join(conds, " AND ") +
			fmt.Sprintf(" ORDER BY latest_start_key ASC, cluster_id ASC, trace_id ASC, service_name ASC LIMIT %d", indexReadLimit) +
			" SETTINGS optimize_read_in_order=1, max_threads=1, max_block_size=256, max_read_buffer_size=1048576"
		body, err := r.ch.Query(ctx, indexSQL)
		if err != nil {
			if qe, ok := err.(*QueryError); ok && qe.Code == NoDataCode {
				continue
			}
			return nil, err
		}
		for _, id := range parseTraceIDs(body) {
			if _, ok := seen[id]; ok {
				continue
			}
			seen[id] = struct{}{}
			ids = append(ids, id)
			if len(ids) >= candidateLimit {
				break
			}
		}
	}
	return ids, nil
}

func parseTraceIDs(body []byte) []string {
	seen := make(map[string]struct{})
	var ids []string
	for _, line := range strings.Split(string(body), "\n") {
		cols := strings.Split(line, "\t")
		if len(cols) == 0 || strings.TrimSpace(cols[0]) == "" {
			continue
		}
		id := cols[0]
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	return ids
}

func parseTraceSummaries(body []byte) []TraceSummary {
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
	return out
}
