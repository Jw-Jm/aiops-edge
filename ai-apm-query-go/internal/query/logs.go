package query

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// LogQuery 是 logs 资源域的规范化查询请求（SQL ownership 在 repository，handler 不组 SQL）。
type LogQuery struct {
	TenantID      string
	ClusterID     string
	ResourceID    string
	Service       string
	Query         string // 文本过滤
	Level         string // severity/level filter
	Services      []string
	Minutes       int
	ExcludeHealth bool
}

// LogRecord 一条规范化日志记录。
type LogRecord struct {
	Timestamp   time.Time
	ServiceName string
	Severity    string
	Body        string
	TraceID     string
	TenantID    string
	ClusterID   string
	ResourceID  string
}

// LogRepository 是 logs 资源域的 domain repository（V9.2 Phase 6）。
//
// SoT 语义（冻结职责）：
//   - Raw Logs：新 reader 走 VictoriaLogs（SoT）；legacy ClickHouse log_records 仅
//     作为 Phase 6 cutover 前 transition path，**禁止新增 fallback**。
//   - Log Pattern / Derived Analytics：→ ClickHouse（冻结职责）。
type LogRepository struct {
	ch     *ClickHouseRepo
	vlogs  *VLogsReader // new mode 的 Raw Logs reader；nil 时 new mode 返回 unavailable
	router *SourceRouter
}

// NewLogRepository 构造 logs repository。
func NewLogRepository(ch *ClickHouseRepo, vlogs *VLogsReader, router *SourceRouter) *LogRepository {
	if router == nil {
		router = NewSourceRouter(ModeLegacy)
	}
	return &LogRepository{ch: ch, vlogs: vlogs, router: router}
}

// SearchRawLogs 查询原始日志。按 router 模式路由：
//   - legacy（transition path）→ ClickHouse log_records
//   - new → VictoriaLogs（SoT）。无 VLogs reader 时返回 unavailable，**不 fallback**。
func (r *LogRepository) SearchRawLogs(ctx context.Context, q LogQuery) ([]LogRecord, error) {
	if r.router.Mode() == ModeNew {
		if r.vlogs == nil {
			return nil, Unavailable("raw logs new SoT (VictoriaLogs) reader not configured")
		}
		return r.vlogs.Search(ctx, q)
	}
	return r.searchLegacy(ctx, q)
}

// SearchRawLogsFromSource selects an explicitly requested raw-log source while
// preserving the same tenant/cluster scope and filter semantics. The public
// source selector must not bypass the repository and call an unscoped reader.
func (r *LogRepository) SearchRawLogsFromSource(ctx context.Context, q LogQuery, source string) ([]LogRecord, error) {
	switch strings.ToLower(strings.TrimSpace(source)) {
	case "victorialogs":
		if r.vlogs == nil {
			return nil, Unavailable("raw logs source VictoriaLogs reader not configured")
		}
		return r.vlogs.Search(ctx, q)
	case "clickhouse":
		return r.searchLegacy(ctx, q)
	default:
		return r.SearchRawLogs(ctx, q)
	}
}

// RawLogSource returns the source used by the configured default reader.
func (r *LogRepository) RawLogSource() string {
	if r.router != nil && r.router.Mode() == ModeNew {
		return "victorialogs"
	}
	return "clickhouse"
}

// LogRuleValue 计算日志类规则标量（SLO 规则评估用），语义与原 handler logMetricQuery 完全一致：
// 全局跨租户，近 5 分钟固定窗口。
//   - log_error_rate：错误日志（ERROR/FATAL）占比 %
//   - log_keyword：body LIKE %keyword% 命中条数
func (r *LogRepository) LogRuleValue(ctx context.Context, service, metric, keyword string) (float64, error) {
	svcClause := ""
	if service != "" {
		svcClause = " AND service_name=" + sqlStr(service)
	}
	var sql string
	if metric == "log_error_rate" {
		sql = fmt.Sprintf(
			"SELECT countIf(severity IN ('ERROR','FATAL')) / count() * 100 FROM observability.log_records WHERE date >= today() AND timestamp >= now() - INTERVAL 5 MINUTE%s",
			svcClause)
	} else {
		sql = fmt.Sprintf(
			"SELECT count() FROM observability.log_records WHERE date >= today() AND body LIKE %s AND timestamp >= now() - INTERVAL 5 MINUTE%s",
			chLike(keyword), svcClause)
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

// TraceLogs 查询与指定 trace_id 关联的日志（TraceContext 血缘闭环）。
// 关联日志走 ClickHouse log_records（derived correlation，非 raw-log SoT）。
func (r *LogRepository) TraceLogs(ctx context.Context, tenantID, clusterID, traceID string) ([]LogRecord, error) {
	var conds []string
	conds = append(conds, "tenant_id='"+tenantID+"'")
	if clusterID != "" {
		conds = append(conds, "cluster_id='"+clusterID+"'")
	}
	conds = append(conds, "trace_id='"+traceID+"'")
	sql := "SELECT timestamp, service_name, severity, body, trace_id FROM observability.log_records WHERE " +
		strings.Join(conds, " AND ") + " ORDER BY timestamp DESC LIMIT 50"

	body, err := r.ch.Query(ctx, sql)
	if err != nil {
		return nil, err
	}
	return parseLogRecords(body)
}

// searchLegacy 是 transition path：读 ClickHouse log_records（Raw Logs legacy SoT）。
func (r *LogRepository) searchLegacy(ctx context.Context, q LogQuery) ([]LogRecord, error) {
	var conds []string
	conds = append(conds, "tenant_id='"+q.TenantID+"'")
	if q.ClusterID != "" {
		conds = append(conds, "cluster_id='"+q.ClusterID+"'")
	}
	if q.Service != "" {
		conds = append(conds, "service_name LIKE '%"+q.Service+"%'")
	}
	if q.Query != "" {
		conds = append(conds, "body LIKE '%"+q.Query+"%'")
	}
	if level := strings.ToLower(strings.TrimSpace(q.Level)); level != "" {
		switch level {
		case "error", "warning", "info", "debug":
			conds = append(conds, "lower(severity)="+sqlStr(level))
		}
	}
	if len(q.Services) > 0 {
		quoted := make([]string, 0, len(q.Services))
		for _, s := range q.Services {
			quoted = append(quoted, sqlStr(s))
		}
		conds = append(conds, "service_name IN ("+strings.Join(quoted, ",")+")")
	}
	if q.ExcludeHealth {
		conds = append(conds, "(body NOT LIKE '%/health%' AND body NOT LIKE '%/ready%' AND body NOT LIKE '%/v1/query%' AND body NOT LIKE '%metrics%')")
	}
	if q.Minutes <= 0 {
		q.Minutes = 1440
	}
	conds = append(conds, fmt.Sprintf("timestamp >= now() - INTERVAL %d MINUTE", q.Minutes))

	sql := "SELECT timestamp, service_name, severity, body, trace_id FROM observability.log_records WHERE " +
		strings.Join(conds, " AND ") + " ORDER BY timestamp DESC LIMIT 100"

	body, err := r.ch.Query(ctx, sql)
	if err != nil {
		return nil, err
	}
	return parseLogRecords(body)
}

// parseLogRecords 解析 ClickHouse TabSeparated 日志行。
func parseLogRecords(body []byte) ([]LogRecord, error) {
	var out []LogRecord
	for _, line := range strings.Split(string(body), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		cols := strings.Split(line, "\t")
		if len(cols) < 5 {
			continue
		}
		t, err := parseCHTime(cols[0])
		if err != nil {
			continue
		}
		out = append(out, LogRecord{
			Timestamp:   t,
			ServiceName: cols[1],
			Severity:    cols[2],
			Body:        cols[3],
			TraceID:     cols[4],
		})
	}
	return out, nil
}

// VLogsReader 是 Raw Logs 新 SoT（VictoriaLogs）的 reader（P6.3）。
// 通过 VictoriaLogs /select/logsql/query 查询原始日志；tenant/cluster/service 由可信 scope
// 注入 LogsQL 过滤。失败语义：unavailable / timeout / no_data；**禁止 fallback ClickHouse**。
type VLogsReader struct {
	endpoint string
	client   *http.Client
}

// NewVLogsReader 构造 VictoriaLogs reader。
func NewVLogsReader(endpoint string, client *http.Client) *VLogsReader {
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	return &VLogsReader{endpoint: strings.TrimSuffix(endpoint, "/"), client: client}
}

// vlogsQuery 构造 VictoriaLogs LogsQL 查询（可信 scope 注入 tenant/cluster/service 过滤）。
// 注意：VictoriaLogs LogsQL 用 `field:"value"`（冒号）做字段过滤，不能用 PromQL 的 `=`。
func vlogsQuery(q LogQuery) string {
	var parts []string
	parts = append(parts, fmt.Sprintf(`tenant_id:%s`, strconv.Quote(q.TenantID)))
	if q.ClusterID != "" {
		parts = append(parts, fmt.Sprintf(`cluster_id:%s`, strconv.Quote(q.ClusterID)))
	}
	if q.Service != "" {
		parts = append(parts, fmt.Sprintf(`service_name:%s`, strconv.Quote(q.Service)))
	}
	if q.Query != "" {
		parts = append(parts, fmt.Sprintf(`_msg:%s`, strconv.Quote(q.Query)))
	}
	if level := strings.ToLower(strings.TrimSpace(q.Level)); level != "" {
		switch level {
		case "error", "warning", "info", "debug":
			value := strconv.Quote(level)
			parts = append(parts, fmt.Sprintf(`(level:equals_common_case(%s) OR severity:equals_common_case(%s))`, value, value))
		}
	}
	if q.ExcludeHealth {
		for _, term := range []string{"health", "ready", "v1/query", "metrics"} {
			parts = append(parts, fmt.Sprintf(`NOT *%s*`, term))
		}
	}
	if q.Minutes <= 0 {
		q.Minutes = 1440
	}
	expr := strings.Join(parts, " AND ")
	// VictoriaLogs 相对时长过滤用 `_time:30m`（过去 N 分钟），不是 `now-Nm`。
	return expr + fmt.Sprintf(" AND _time:%dm", q.Minutes)
}

// vlogsRecord 是 VictoriaLogs JSON Line 的一条日志。
type vlogsRecord struct {
	Timestamp   string `json:"_time"`
	ServiceName string `json:"service_name"`
	Level       string `json:"level"`
	Severity    string `json:"severity"`
	Msg         string `json:"_msg"`
	TraceID     string `json:"trace_id"`
}

// Endpoint 返回 reader 使用的 base endpoint（P6.5 reader wiring 验收用）。
func (r *VLogsReader) Endpoint() string { return r.endpoint }

// Search 在 VictoriaLogs 中查询原始日志。
func (r *VLogsReader) Search(ctx context.Context, q LogQuery) ([]LogRecord, error) {
	u := r.endpoint + "/select/logsql/query"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.URL.RawQuery = "query=" + url.QueryEscape(vlogsQuery(q))

	resp, err := r.client.Do(req)
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return nil, Timeout("victorialogs: " + err.Error())
		}
		return nil, Unavailable("victorialogs: " + err.Error())
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, Unavailable(fmt.Sprintf("victorialogs: status %d", resp.StatusCode))
	}
	var out []LogRecord
	dec := json.NewDecoder(resp.Body)
	for dec.More() {
		var rec vlogsRecord
		if err := dec.Decode(&rec); err != nil {
			if err.Error() == "EOF" {
				break
			}
			return nil, Unavailable("victorialogs: decode: " + err.Error())
		}
		if rec.Msg == "" && rec.ServiceName == "" {
			continue
		}
		level := rec.Level
		if level == "" {
			level = rec.Severity
		}
		out = append(out, LogRecord{
			Timestamp:   parseVLogsTime(rec.Timestamp),
			ServiceName: rec.ServiceName,
			Severity:    level,
			Body:        rec.Msg,
			TraceID:     rec.TraceID,
		})
	}
	if len(out) == 0 {
		return nil, NoData()
	}
	return out, nil
}

// parseVLogsTime 解析 VictoriaLogs 时间（RFC3339）。
func parseVLogsTime(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}
	}
	return t
}

// TrendBucket 一个日志量时间桶（derived analytics，ClickHouse SoT）。
type TrendBucket struct {
	Bucket time.Time
	Count  int
}

// logWhere 构造 derived analytics 共享的 tenant/cluster/service 过滤子句（SQL ownership 在 repository）。
func logWhere(q LogQuery) string {
	var conds []string
	conds = append(conds, "tenant_id='"+q.TenantID+"'")
	if q.ClusterID != "" {
		conds = append(conds, "cluster_id='"+q.ClusterID+"'")
	}
	if q.Service != "" {
		conds = append(conds, "service_name LIKE '%"+q.Service+"%'")
	}
	if q.Query != "" {
		conds = append(conds, "body LIKE '%"+q.Query+"%'")
	}
	if q.Minutes <= 0 {
		q.Minutes = 1440
	}
	conds = append(conds, fmt.Sprintf("timestamp >= now() - INTERVAL %d MINUTE", q.Minutes))
	return strings.Join(conds, " AND ")
}

// AggregateTrend 按 interval 聚合日志量时间序列（derived analytics，走 ClickHouse）。
func (r *LogRepository) AggregateTrend(ctx context.Context, q LogQuery, intervalMinutes int) ([]TrendBucket, error) {
	if intervalMinutes <= 0 {
		intervalMinutes = 5
	}
	sql := fmt.Sprintf(
		"SELECT toStartOfInterval(timestamp, INTERVAL %d MINUTE) AS bucket, count() AS cnt FROM observability.log_records WHERE %s GROUP BY bucket ORDER BY bucket",
		intervalMinutes, logWhere(q))
	body, err := r.ch.Query(ctx, sql)
	if err != nil {
		return nil, err
	}
	var out []TrendBucket
	for _, line := range strings.Split(string(body), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		cols := strings.Split(line, "\t")
		if len(cols) < 2 {
			continue
		}
		t, err := parseCHTime(cols[0])
		if err != nil {
			continue
		}
		var cnt int
		fmt.Sscanf(cols[1], "%d", &cnt)
		out = append(out, TrendBucket{Bucket: t, Count: cnt})
	}
	return out, nil
}

// LevelCount 一条日志级别分布（derived analytics）。
type LevelCount struct {
	Level string
	Count int
}

// AggregateLevels 按级别聚合日志量（derived analytics，走 ClickHouse）。
func (r *LogRepository) AggregateLevels(ctx context.Context, q LogQuery) ([]LevelCount, error) {
	sql := "SELECT severity AS level, count() AS cnt FROM observability.log_records WHERE " + logWhere(q) +
		" GROUP BY severity ORDER BY cnt DESC"
	body, err := r.ch.Query(ctx, sql)
	if err != nil {
		return nil, err
	}
	var out []LevelCount
	for _, line := range strings.Split(string(body), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		cols := strings.Split(line, "\t")
		if len(cols) < 2 {
			continue
		}
		var cnt int
		fmt.Sscanf(cols[1], "%d", &cnt)
		out = append(out, LevelCount{Level: cols[0], Count: cnt})
	}
	return out, nil
}

// ServiceCount 一条服务日志量 TOP（derived analytics）。
type ServiceCount struct {
	Service string
	Count   int
}

// AggregateServices 按服务聚合日志量 TOP N（derived analytics，走 ClickHouse）。
func (r *LogRepository) AggregateServices(ctx context.Context, q LogQuery, limit int) ([]ServiceCount, error) {
	if limit <= 0 {
		limit = 10
	}
	sql := "SELECT service_name AS service, count() AS cnt FROM observability.log_records WHERE " + logWhere(q) +
		fmt.Sprintf(" GROUP BY service_name ORDER BY cnt DESC LIMIT %d", limit)
	body, err := r.ch.Query(ctx, sql)
	if err != nil {
		return nil, err
	}
	var out []ServiceCount
	for _, line := range strings.Split(string(body), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		cols := strings.Split(line, "\t")
		if len(cols) < 2 {
			continue
		}
		var cnt int
		fmt.Sscanf(cols[1], "%d", &cnt)
		out = append(out, ServiceCount{Service: cols[0], Count: cnt})
	}
	return out, nil
}
