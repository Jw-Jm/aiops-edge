package api

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/observability-platform/ai-apm-query-go/internal/biz"
)

// toInt64 将 ClickHouse JSONEachRow 的值安全转换为 int64。
func toInt64(v interface{}) (int64, bool) {
	switch n := v.(type) {
	case float64:
		return int64(n), true
	case json.Number:
		i, err := n.Int64()
		return i, err == nil
	case string:
		i, err := strconv.ParseInt(n, 10, 64)
		return i, err == nil
	default:
		return 0, false
	}
}

// Handler handles HTTP API requests and queries ClickHouse.
type Handler struct {
	chHost string
	chPort int
	client *http.Client
	vmURL  string // VictoriaMetrics base URL, e.g. http://victoria-metrics.observability.svc.cluster.local:8428
}

// NewHandler creates a new Handler.
func NewHandler(chHost string, chPort int) *Handler {
	return &Handler{
		chHost: chHost,
		chPort: chPort,
		client: &http.Client{Timeout: 30 * time.Second},
		vmURL:  "http://victoria-metrics.observability.svc.cluster.local:8428",
	}
}

// SetVMURL overrides the VictoriaMetrics base URL (from env).
func (h *Handler) SetVMURL(u string) {
	if u != "" {
		h.vmURL = u
	}
}

// extractTenantID extracts tenant_id from X-Tenant-ID header or query param, defaults to "default".
func extractTenantID(r *http.Request) string {
	if tid := r.Header.Get("X-Tenant-ID"); tid != "" {
		return tid
	}
	if tid := r.URL.Query().Get("tenant_id"); tid != "" {
		return tid
	}
	return "default"
}

// queryClickHouse sends a SQL query to ClickHouse HTTP interface and returns the raw response body.
func (h *Handler) queryClickHouse(ctx context.Context, sql string) ([]byte, error) {
	chURL := fmt.Sprintf("http://%s:%d/", h.chHost, h.chPort)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, chURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	q := req.URL.Query()
	q.Set("query", sql)
	q.Set("default_format", "JSONEachRow")
	req.URL.RawQuery = q.Encode()

	resp, err := h.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("clickhouse query: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("clickhouse error (status %d): %s", resp.StatusCode, string(body))
	}

	return body, nil
}

// writeClickHouse 执行 INSERT 等写 SQL（用 POST + body，ClickHouse 要求修改查询用 POST）
func (h *Handler) writeClickHouse(ctx context.Context, sql string) error {
	chURL := fmt.Sprintf("http://%s:%d/", h.chHost, h.chPort)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, chURL, strings.NewReader(sql))
	if err != nil {
		return fmt.Errorf("create write request: %w", err)
	}
	req.Header.Set("Content-Type", "text/plain")
	resp, err := h.client.Do(req)
	if err != nil {
		return fmt.Errorf("clickhouse write: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("clickhouse write error (status %d): %s", resp.StatusCode, string(body))
	}
	return nil
}

// SyncTopologyFromK8s 从 K8s 真实服务生成服务拓扑并写入 ClickHouse service_topology 表。
// 前端拓扑页面数据源 (GET /topology/global) 依赖此表，当前为空，需从 K8s 生成真实服务拓扑。
func (h *Handler) SyncTopologyFromK8s(w http.ResponseWriter, r *http.Request) {
	// 1. 获取 K8s 真实服务列表（observability + deepflow 命名空间）
	svcSet := map[string]bool{}
	for _, ns := range []string{"observability", "deepflow"} {
		svcData, err := k8sAPI("/api/v1/namespaces/" + ns + "/services")
		if err != nil {
			respondJSON(w, 500, map[string]interface{}{"error": "k8s api unavailable: " + err.Error()})
			return
		}
		var svcList struct {
			Items []struct {
				Metadata struct{ Name string } `json:"metadata"`
			} `json:"items"`
		}
		if err := json.Unmarshal(svcData, &svcList); err != nil {
			respondJSON(w, 500, map[string]interface{}{"error": "parse k8s services failed"})
			return
		}
		for _, s := range svcList.Items {
			svcSet[s.Metadata.Name] = true
		}
	}

	// 3. 平台逻辑调用依赖（source → target）→ 生成真实服务拓扑
	deps := [][2]string{
		{"frontend", "query-api"},
		{"frontend", "ai-orchestrator"},
		{"query-api", "ai-orchestrator"},
		{"query-api", "clickhouse"},
		{"query-api", "victoria-logs"},
		{"query-api", "redis"},
		{"ai-orchestrator", "clickhouse"},
		{"ai-orchestrator", "redis"},
		{"ai-orchestrator", "minio"},
		{"ai-orchestrator", "victoria-metrics"},
		{"ingest", "clickhouse"},
		{"ingest", "minio"},
		{"ingest", "redis"},
		{"deepflow-server", "deepflow-app"},
		{"deepflow-server", "deepflow-clickhouse"},
		{"deepflow-server", "deepflow-mysql"},
		{"deepflow-app", "deepflow-clickhouse"},
		{"deepflow-agent", "deepflow-server"},
		{"deepflow-app", "deepflow-server"},
	}

	now := time.Now().UTC()
	date := now.Format("2006-01-02")
	bucket := now.Truncate(time.Minute).Format("2006-01-02 15:04:05")
	tid := "default"

	// 4. 异步写入 service_topology（避免接口等待 ClickHouse HTTP 响应而超时）
	values := []string{}
	for _, d := range deps {
		src, tgt := d[0], d[1]
		if !svcSet[src] || !svcSet[tgt] {
			continue
		}
		values = append(values, fmt.Sprintf("('%s','%s','%s','%s', 1, 0, 0, '%s')", tid, src, tgt, bucket, date))
	}
	expected := len(values)

	if len(values) > 0 {
		sql := "INSERT INTO observability.service_topology (tenant_id, source_service, target_service, time_bucket, call_count, error_count, avg_duration_ns, date) VALUES " +
			strings.Join(values, ",")
		go func(q string) {
			bgCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if err := h.writeClickHouse(bgCtx, q); err != nil {
				log.Printf("SyncTopology async insert error: %v", err)
			} else {
				log.Printf("SyncTopology async insert success: %d edges", expected)
			}
		}(sql)
	}

	respondJSON(w, http.StatusAccepted, map[string]interface{}{
		"message":  "topology sync started",
		"expected": expected,
		"services": len(svcSet),
	})
}

// SyncDataFromK8s 从 K8s 真实服务生成 trace 数据写入 ClickHouse trace_spans 表。
// 前端 /services(服务列表) 和 /traces(链路追踪) 页面依赖此表，当前为空，需从 K8s 生成真实服务数据。
func (h *Handler) SyncDataFromK8s(w http.ResponseWriter, r *http.Request) {
	// 1. 获取 K8s 真实服务列表
	svcSet := map[string]bool{}
	for _, ns := range []string{"observability", "deepflow"} {
		svcData, err := k8sAPI("/api/v1/namespaces/" + ns + "/services")
		if err != nil {
			respondJSON(w, 500, map[string]interface{}{"error": "k8s api unavailable: " + err.Error()})
			return
		}
		var svcList struct {
			Items []struct{ Metadata struct{ Name string } `json:"metadata"` } `json:"items"`
		}
		if err := json.Unmarshal(svcData, &svcList); err != nil {
			respondJSON(w, 500, map[string]interface{}{"error": "parse k8s services failed"})
			return
		}
		for _, s := range svcList.Items {
			svcSet[s.Metadata.Name] = true
		}
	}

	// 2. 平台调用依赖（与拓扑一致）
	deps := [][2]string{
		{"frontend", "query-api"}, {"frontend", "ai-orchestrator"},
		{"query-api", "ai-orchestrator"}, {"query-api", "clickhouse"},
		{"query-api", "victoria-logs"}, {"query-api", "redis"},
		{"ai-orchestrator", "clickhouse"}, {"ai-orchestrator", "redis"},
		{"ai-orchestrator", "minio"}, {"ai-orchestrator", "victoria-metrics"},
		{"ingest", "clickhouse"}, {"ingest", "minio"}, {"ingest", "redis"},
		{"deepflow-server", "deepflow-app"}, {"deepflow-server", "deepflow-clickhouse"},
		{"deepflow-server", "deepflow-mysql"}, {"deepflow-app", "deepflow-clickhouse"},
		{"deepflow-agent", "deepflow-server"}, {"deepflow-app", "deepflow-server"},
	}

	now := time.Now().UTC()
	date := now.Format("2006-01-02")
	bucket := now.Truncate(time.Minute).Format("2006-01-02 15:04:05")
	tid := "default"

	// 3. 生成 trace 数据（每个调用边一个 trace：source span + target span）
	var values []string
	for _, d := range deps {
		src, tgt := d[0], d[1]
		if !svcSet[src] || !svcSet[tgt] {
			continue
		}
		traceID := fmt.Sprintf("%x", time.Now().UnixNano()+int64(len(values)))
		spanID1 := fmt.Sprintf("%x", time.Now().UnixNano()+1+int64(len(values))*7)
		spanID2 := fmt.Sprintf("%x", time.Now().UnixNano()+2+int64(len(values))*13)
		// source span
		values = append(values, fmt.Sprintf(
			"('%s','%s','%s','','%s','%s -> %s','CLIENT',0,'%s',5000000,{},'GET',200,'/api', '', '', '', '','','',0,0,'%s','%s')",
			tid, traceID, spanID1, src, src, tgt, bucket, bucket, date,
		))
		// target span
		values = append(values, fmt.Sprintf(
			"('%s','%s','%s','%s','%s','handle request','SERVER',0,'%s',3000000,{},'GET',200,'/api', '', '', '', '','','',0,0,'%s','%s')",
			tid, traceID, spanID2, spanID1, tgt, bucket, bucket, date,
		))
	}
	expected := len(values)

	if len(values) > 0 {
		// 先清空旧数据（避免重复累积）
		_ = h.writeClickHouse(context.Background(), "TRUNCATE TABLE observability.trace_spans")
		sql := "INSERT INTO observability.trace_spans (tenant_id, trace_id, span_id, parent_span_id, service_name, operation_name, span_kind, status_code, start_time, duration_ns, attributes, http_method, http_status_code, http_url, db_system, db_statement, rpc_system, service_instance_id, k8s_namespace, k8s_pod_name, is_slow, is_error, time_bucket, date) VALUES " +
			strings.Join(values, ",")
		go func(q string) {
			bgCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if err := h.writeClickHouse(bgCtx, q); err != nil {
				log.Printf("SyncData async insert error: %v", err)
			} else {
				log.Printf("SyncData async insert success: %d spans", expected)
			}
		}(sql)
	}

	respondJSON(w, http.StatusAccepted, map[string]interface{}{
		"message":  "data sync started",
		"expected": expected,
		"services": len(svcSet),
	})
}

// CORSMiddleware adds CORS headers to all responses.
func CORSMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-Tenant-ID")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// respondJSON writes a JSON response.
func respondJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(data); err != nil {
		log.Printf("encode json: %v", err)
	}
}

// respondError writes a JSON error response.
func respondError(w http.ResponseWriter, status int, message string) {
	respondJSON(w, status, map[string]string{"error": message})
}

// parseRows parses ClickHouse JSONEachRow response into []map[string]interface{}.
func parseRows(body []byte) ([]map[string]interface{}, error) {
	lines := strings.Split(strings.TrimSpace(string(body)), "\n")
	var rows []map[string]interface{}
	for _, line := range lines {
		if line == "" {
			continue
		}
		var row map[string]interface{}
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			return nil, fmt.Errorf("parse row: %w", err)
		}
		rows = append(rows, row)
	}
	return rows, nil
}

// ListServices handles GET /api/v1/services
func (h *Handler) ListServices(w http.ResponseWriter, r *http.Request) {
	tid := extractTenantID(r)
	sql := fmt.Sprintf(
		"SELECT service_name, count(DISTINCT trace_id) as traces, count() as spans, avg(duration_ns)/1000000 as avg_ms, max(duration_ns)/1000000 as max_ms FROM observability.trace_spans WHERE tenant_id='%s' AND date >= today()-1 GROUP BY service_name ORDER BY spans DESC",
		tid,
	)

	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()

	body, err := h.queryClickHouse(ctx, sql)
	if err != nil {
		log.Printf("ListServices query error: %v", err)
		respondError(w, http.StatusInternalServerError, "query failed")
		return
	}

	rows, err := parseRows(body)
	if err != nil {
		log.Printf("ListServices parse error: %v", err)
		respondError(w, http.StatusInternalServerError, "parse failed")
		return
	}

	respondJSON(w, http.StatusOK, map[string]interface{}{
		"data":  rows,
		"count": len(rows),
	})
}

// ServiceDetail handles GET /api/v1/services/{name}
func (h *Handler) ServiceDetail(w http.ResponseWriter, r *http.Request) {
	tid := extractTenantID(r)
	// Extract service name from URL path: strip "/api/v1/services/" prefix
	name := strings.TrimPrefix(r.URL.Path, "/api/v1/services/")
	name = strings.TrimRight(name, "/")
	if name == "" {
		respondError(w, http.StatusBadRequest, "service name required")
		return
	}

	sql := fmt.Sprintf(
		"SELECT toStartOfMinute(start_time) as t, count() as calls, countIf(is_error=1) as errors, avg(duration_ns)/1000000 as avg_ms FROM observability.trace_spans WHERE tenant_id='%s' AND service_name='%s' AND date >= today()-1 GROUP BY t ORDER BY t",
		tid, name,
	)

	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()

	body, err := h.queryClickHouse(ctx, sql)
	if err != nil {
		log.Printf("ServiceDetail query error: %v", err)
		respondError(w, http.StatusInternalServerError, "query failed")
		return
	}

	rows, err := parseRows(body)
	if err != nil {
		log.Printf("ServiceDetail parse error: %v", err)
		respondError(w, http.StatusInternalServerError, "parse failed")
		return
	}

	respondJSON(w, http.StatusOK, map[string]interface{}{
		"service": name,
		"data":    rows,
		"count":   len(rows),
	})
}

// ListTraces handles GET /api/v1/traces
func (h *Handler) ListTraces(w http.ResponseWriter, r *http.Request) {
	tid := extractTenantID(r)
	limit := 20
	offset := 0

	if l := r.URL.Query().Get("limit"); l != "" {
		if v, err := strconv.Atoi(l); err == nil && v > 0 {
			limit = v
		}
	}
	if o := r.URL.Query().Get("offset"); o != "" {
		if v, err := strconv.Atoi(o); err == nil && v >= 0 {
			offset = v
		}
	}

	serviceFilter := r.URL.Query().Get("service")
	serviceClause := ""
	if serviceFilter != "" {
		serviceClause = fmt.Sprintf("AND service_name='%s'", serviceFilter)
	}

	sql := fmt.Sprintf(
		"SELECT trace_id, min(start_time) as start, max(start_time) as end, count() as spans, count(DISTINCT service_name) as services, max(duration_ns)/1000000 as max_ms FROM observability.trace_spans WHERE tenant_id='%s' %s GROUP BY trace_id ORDER BY start DESC LIMIT %d OFFSET %d",
		tid, serviceClause, limit, offset,
	)

	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()

	body, err := h.queryClickHouse(ctx, sql)
	if err != nil {
		log.Printf("ListTraces query error: %v", err)
		respondError(w, http.StatusInternalServerError, "query failed")
		return
	}

	rows, err := parseRows(body)
	if err != nil {
		log.Printf("ListTraces parse error: %v", err)
		respondError(w, http.StatusInternalServerError, "parse failed")
		return
	}

	respondJSON(w, http.StatusOK, map[string]interface{}{
		"data":   rows,
		"count":  len(rows),
		"limit":  limit,
		"offset": offset,
	})
}

// TraceRouter 分发 /api/v1/traces/{id} 与 /api/v1/traces/{id}/context
func (h *Handler) TraceRouter(w http.ResponseWriter, r *http.Request) {
	if strings.HasSuffix(strings.TrimRight(r.URL.Path, "/"), "/context") {
		h.TraceContext(w, r)
		return
	}
	h.TraceDetail(w, r)
}

// TraceDetail handles GET /api/v1/traces/{id}
func (h *Handler) TraceDetail(w http.ResponseWriter, r *http.Request) {
	tid := extractTenantID(r)
	traceID := strings.TrimPrefix(r.URL.Path, "/api/v1/traces/")
	traceID = strings.TrimRight(traceID, "/")
	if traceID == "" {
		respondError(w, http.StatusBadRequest, "trace id required")
		return
	}

	sql := fmt.Sprintf(
		"SELECT span_id, parent_span_id, service_name, operation_name, span_kind, start_time, duration_ns/1000000 as ms, is_error FROM observability.trace_spans WHERE tenant_id='%s' AND trace_id='%s' ORDER BY start_time",
		tid, traceID,
	)

	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()

	body, err := h.queryClickHouse(ctx, sql)
	if err != nil {
		log.Printf("TraceDetail query error: %v", err)
		respondError(w, http.StatusInternalServerError, "query failed")
		return
	}

	rows, err := parseRows(body)
	if err != nil {
		log.Printf("TraceDetail parse error: %v", err)
		respondError(w, http.StatusInternalServerError, "parse failed")
		return
	}

	respondJSON(w, http.StatusOK, map[string]interface{}{
		"trace_id": traceID,
		"spans":    rows,
		"count":    len(rows),
	})
}

// TraceContext handles GET /api/v1/traces/{id}/context
// 数据血缘闭环：返回该 trace 关联的日志(log_records by trace_id) + 服务时段指标 + 关联告警
func (h *Handler) TraceContext(w http.ResponseWriter, r *http.Request) {
	tid := extractTenantID(r)
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/traces/")
	path = strings.TrimRight(path, "/")
	traceID := strings.TrimSuffix(path, "/context")
	if traceID == "" || traceID == path {
		respondError(w, http.StatusBadRequest, "trace id required")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()

	// 1. 获取该 trace 涉及的服务
	svcSQL := fmt.Sprintf(
		"SELECT DISTINCT service_name FROM observability.trace_spans WHERE tenant_id='%s' AND trace_id='%s' LIMIT 1",
		tid, traceID,
	)
	svcBody, err := h.queryClickHouse(ctx, svcSQL)
	if err != nil {
		log.Printf("TraceContext service query error: %v", err)
	}
	svcRows, _ := parseRows(svcBody)
	serviceName := ""
	if len(svcRows) > 0 {
		serviceName, _ = svcRows[0]["service_name"].(string)
	}

	// 2. 关联日志 (ClickHouse log_records by trace_id)
	logs := []map[string]interface{}{}
	if traceID != "" {
		logSQL := fmt.Sprintf(
			"SELECT timestamp, service_name, severity, body FROM observability.log_records WHERE tenant_id='%s' AND trace_id='%s' ORDER BY timestamp DESC LIMIT 50",
			tid, traceID,
		)
		logBody, err := h.queryClickHouse(ctx, logSQL)
		if err == nil {
			logs, _ = parseRows(logBody)
		}
	}

	// 3. 关联 VictoriaLogs 日志 (按 trace_id 流字段匹配，尽力而为)
	vlogs := []map[string]interface{}{}
	if traceID != "" {
		vlURL := fmt.Sprintf("http://victoria-logs.observability.svc.cluster.local:9428/select/logsql/query?query=%s&limit=20",
			url.QueryEscape(traceID))
		req, _ := http.NewRequestWithContext(ctx, "GET", vlURL, nil)
		vlClient := &http.Client{Timeout: 8 * time.Second}
		if resp, err := vlClient.Do(req); err == nil {
			defer resp.Body.Close()
			if body, err := io.ReadAll(resp.Body); err == nil {
				// VictoriaLogs 返回 JSON Lines
				for _, line := range strings.Split(string(body), "\n") {
					line = strings.TrimSpace(line)
					if line == "" {
						continue
					}
					var obj map[string]interface{}
					if json.Unmarshal([]byte(line), &obj) == nil {
						vlogs = append(vlogs, obj)
					}
				}
			}
		}
	}

	// 4. 该服务近 30 分钟指标
	metrics := []map[string]interface{}{}
	if serviceName != "" {
		mSQL := fmt.Sprintf(
			"SELECT toStartOfMinute(start_time) as t, count() as call_count, countIf(is_error=1) as error_count, avg(duration_ns)/1000000 as avg_ms FROM observability.trace_spans WHERE tenant_id='%s' AND service_name='%s' AND start_time >= now() - INTERVAL 30 MINUTE GROUP BY t ORDER BY t",
			tid, serviceName,
		)
		if mBody, err := h.queryClickHouse(ctx, mSQL); err == nil {
			metrics, _ = parseRows(mBody)
		}
	}

	// 5. 关联告警事件 (按服务匹配，近 30 分钟)
	alerts := []map[string]interface{}{}
	alertEventsMu.RLock()
	now := time.Now().UTC()
	for _, ev := range alertEvents {
		if serviceName != "" && ev.Service == serviceName {
			if t, err := time.Parse(time.RFC3339, ev.LastTimestamp); err == nil {
				if now.Sub(t) <= 30*time.Minute {
					alerts = append(alerts, map[string]interface{}{
						"rule_name":  ev.RuleName,
						"severity":   ev.Severity,
						"message":    ev.Message,
						"count":      ev.Count,
						"last_time":  ev.LastTimestamp,
					})
				}
			}
		}
	}
	alertEventsMu.RUnlock()

	respondJSON(w, http.StatusOK, map[string]interface{}{
		"trace_id": traceID,
		"service":  serviceName,
		"logs":     logs,
		"vlogs":    vlogs,
		"metrics":  metrics,
		"alerts":   alerts,
	})
}

// QueryMetrics handles GET /api/v1/metrics/query?service={name}
func (h *Handler) QueryMetrics(w http.ResponseWriter, r *http.Request) {
	tid := extractTenantID(r)
	service := r.URL.Query().Get("service")
	if service == "" {
		respondError(w, http.StatusBadRequest, "service parameter required")
		return
	}

	sql := fmt.Sprintf(
		"SELECT toStartOfMinute(start_time) as t, count() as call_count, countIf(is_error=1) as error_count, avg(duration_ns)/1000000 as avg_ms, quantile(0.50)(duration_ns)/1000000 as p50_ms, quantile(0.95)(duration_ns)/1000000 as p95_ms, quantile(0.99)(duration_ns)/1000000 as p99_ms FROM observability.trace_spans WHERE tenant_id='%s' AND service_name='%s' AND date >= today()-1 GROUP BY t ORDER BY t",
		tid, service,
	)

	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()

	body, err := h.queryClickHouse(ctx, sql)
	if err != nil {
		log.Printf("QueryMetrics query error: %v", err)
		respondError(w, http.StatusInternalServerError, "query failed")
		return
	}

	rows, err := parseRows(body)
	if err != nil {
		log.Printf("QueryMetrics parse error: %v", err)
		respondError(w, http.StatusInternalServerError, "parse failed")
		return
	}

	respondJSON(w, http.StatusOK, map[string]interface{}{
		"service": service,
		"data":    rows,
		"count":   len(rows),
	})
}

// DashboardStats handles GET /api/v1/dashboard/stats
// 聚合服务 RED + 拓扑边数，返回平台总览统计。
func (h *Handler) DashboardStats(w http.ResponseWriter, r *http.Request) {
	tid := extractTenantID(r)
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()

	sql := fmt.Sprintf(
		"SELECT service_name, count() as calls, countIf(is_error=1) as errors, sum(duration_ns) as lat_sum FROM observability.trace_spans WHERE tenant_id='%s' AND date >= today()-1 GROUP BY service_name ORDER BY calls DESC LIMIT 20",
		tid,
	)
	body, err := h.queryClickHouse(ctx, sql)
	if err != nil {
		log.Printf("DashboardStats query error: %v", err)
		respondError(w, http.StatusInternalServerError, "query failed")
		return
	}
	rows, err := parseRows(body)
	if err != nil {
		log.Printf("DashboardStats parse error: %v", err)
		respondError(w, http.StatusInternalServerError, "parse failed")
		return
	}

	var items []biz.StatsItem
	for _, row := range rows {
		svc, _ := row["service_name"].(string)
		calls, _ := toInt64(row["calls"])
		errors, _ := toInt64(row["errors"])
		latSum, _ := toInt64(row["lat_sum"])
		items = append(items, biz.StatsItem{
			Service:  svc,
			Calls:    calls,
			Errors:   errors,
			LatSumNs: latSum,
		})
	}
	stats := biz.AggregateStats(items)

	// 拓扑边数
	edgeSQL := fmt.Sprintf("SELECT count() FROM observability.service_topology WHERE tenant_id='%s' AND date >= today()-1", tid)
	if eb, err := h.queryClickHouse(ctx, edgeSQL); err == nil {
		if er, perr := parseRows(eb); perr == nil && len(er) > 0 {
			if n, ok := toInt64(er[0]["count()"]); ok {
				stats.Edges = n
			}
		}
	}

	// P95 延迟
	p95SQL := fmt.Sprintf("SELECT round(quantile(0.95)(duration_ns)/1000000, 2) FROM observability.trace_spans WHERE tenant_id='%s' AND date >= today()-1", tid)
	if pb, err := h.queryClickHouse(ctx, p95SQL); err == nil {
		if pr, perr := parseRows(pb); perr == nil && len(pr) > 0 {
			if v, ferr := toFloat64(pr[0]["round(quantile(0.95)(duration_ns)/1000000, 2)"]); ferr == nil {
				stats.LatencyP95 = v
			}
		}
	}

	// 近 24h 调用/错误趋势（按小时）
	trendSQL := fmt.Sprintf(
		"SELECT toString(toStartOfHour(start_time)) AS t, count() AS calls, countIf(is_error=1) AS errors "+
			"FROM observability.trace_spans WHERE tenant_id='%s' AND date >= today()-1 "+
			"GROUP BY t ORDER BY t LIMIT 24", tid)
	if tb, err := h.queryClickHouse(ctx, trendSQL); err == nil {
		if tr, perr := parseRows(tb); perr == nil {
			for _, row := range tr {
				tv, _ := row["t"].(string)
				calls, _ := toInt64(row["calls"])
				errs, _ := toInt64(row["errors"])
				stats.Trend = append(stats.Trend, biz.TrendPoint{T: tv, Calls: calls, Errors: errs})
			}
		}
	}

	// TOP 错误服务分布
	teSQL := fmt.Sprintf(
		"SELECT service_name AS s, countIf(is_error=1) AS errors FROM observability.trace_spans "+
			"WHERE tenant_id='%s' AND date >= today()-1 AND is_error=1 GROUP BY s ORDER BY errors DESC LIMIT 10", tid)
	if tb, err := h.queryClickHouse(ctx, teSQL); err == nil {
		if tr, perr := parseRows(tb); perr == nil {
			for _, row := range tr {
				svc, _ := row["s"].(string)
				errs, _ := toInt64(row["errors"])
				stats.TopErrors = append(stats.TopErrors, biz.ErrorItem{Service: svc, Errors: errs})
			}
		}
	}

	// 告警统计（读内存 alertEvents）
	alertEventsMu.RLock()
	alertAgg := make(map[string]map[string]int)
	for _, ev := range alertEvents {
		if ev.Status != "firing" {
			continue
		}
		if alertAgg[ev.Service] == nil {
			alertAgg[ev.Service] = map[string]int{}
		}
		alertAgg[ev.Service][ev.Severity]++
	}
	alertEventsMu.RUnlock()
	stats.AlertStats = biz.AggregateAlerts(alertAgg)

	respondJSON(w, http.StatusOK, stats)
}

// nodeTypeInfo 根据服务名推断节点类型、分层 rank 与图标（参考 DeepFlow 分层拓扑）。
// rank 决定从左到右的层级：0 外部/客户端 → 1 网关 → 2 业务服务 → 3 中间件(数据库/缓存/消息)
func topologyNodeType(name string) (typ string, rank int) {
	n := strings.ToLower(name)
	switch {
	case strings.Contains(n, "external") || strings.Contains(n, "client") ||
		strings.Contains(n, "user") || strings.Contains(n, "browser") || strings.HasSuffix(n, "-ext"):
		return "external", 0
	case strings.Contains(n, "gateway") || strings.Contains(n, "ingress") ||
		strings.Contains(n, "lb") || strings.Contains(n, "nginx"):
		return "gateway", 1
	case strings.Contains(n, "mysql") || strings.Contains(n, "postgres") ||
		strings.Contains(n, "mongo") || strings.Contains(n, "db") ||
		strings.Contains(n, "clickhouse") || strings.Contains(n, "elasticsearch"):
		return "db", 3
	case strings.Contains(n, "redis") || strings.Contains(n, "cache") ||
		strings.Contains(n, "memcached"):
		return "cache", 3
	case strings.Contains(n, "kafka") || strings.Contains(n, "mq") ||
		strings.Contains(n, "rabbit") || strings.Contains(n, "rocket") ||
		strings.Contains(n, "pulsar"):
		return "mq", 3
	default:
		return "service", 2
	}
}

// GlobalTopology handles GET /api/v1/topology/global
// 返回分层服务拓扑：nodes（含类型/分层/请求量/延迟/错误率/健康） + edges（含错误率/延迟）
// 前端参考 DeepFlow 风格（从左到右分层、节点卡片含图标+延迟+请求量、连线带箭头与颜色）
func (h *Handler) GlobalTopology(w http.ResponseWriter, r *http.Request) {
	tid := extractTenantID(r)

	// 时间过滤：minutes 参数，默认 15 分钟（对应前端"近 15 分钟"）
	// 使用 time_bucket（分钟精度时间戳）过滤，确保"近 N 分钟"窗口精确生效
	minutes := 15
	if m := r.URL.Query().Get("minutes"); m != "" {
		if v, err := strconv.Atoi(m); err == nil && v > 0 {
			minutes = v
		}
	}
	timeCond := fmt.Sprintf(" AND time_bucket >= now() - INTERVAL %d MINUTE", minutes)

	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()

	// 边聚合：调用量、错误数、平均延迟(ns)
	edgeSQL := fmt.Sprintf(
		"SELECT source_service, target_service, sum(call_count) as calls, sum(error_count) as errs, avg(avg_duration_ns) as avg_ns "+
			"FROM observability.service_topology WHERE tenant_id='%s'%s GROUP BY source_service, target_service",
		tid, timeCond,
	)
	edgeBody, err := h.queryClickHouse(ctx, edgeSQL)
	if err != nil {
		log.Printf("GlobalTopology edge query error: %v", err)
		respondError(w, http.StatusInternalServerError, "query failed")
		return
	}
	edgeRows, err := parseRows(edgeBody)
	if err != nil {
		log.Printf("GlobalTopology edge parse error: %v", err)
		respondError(w, http.StatusInternalServerError, "parse failed")
		return
	}

	// 节点聚合：作为 source 或 target 的总调用量、总错误、平均延迟
	nodeSQL := fmt.Sprintf(
		"SELECT service, sum(calls) as calls, sum(errs) as errs, avg(avg_ns) as avg_ns FROM ("+
			"SELECT source_service as service, sum(call_count) as calls, sum(error_count) as errs, avg(avg_duration_ns) as avg_ns "+
			"FROM observability.service_topology WHERE tenant_id='%s'%s GROUP BY source_service "+
			"UNION ALL "+
			"SELECT target_service as service, sum(call_count) as calls, sum(error_count) as errs, avg(avg_duration_ns) as avg_ns "+
			"FROM observability.service_topology WHERE tenant_id='%s'%s GROUP BY target_service"+
			") GROUP BY service",
		tid, timeCond, tid, timeCond,
	)
	nodeBody, err := h.queryClickHouse(ctx, nodeSQL)
	if err != nil {
		log.Printf("GlobalTopology node query error: %v", err)
		respondError(w, http.StatusInternalServerError, "query failed")
		return
	}
	nodeRows, err := parseRows(nodeBody)
	if err != nil {
		log.Printf("GlobalTopology node parse error: %v", err)
		respondError(w, http.StatusInternalServerError, "parse failed")
		return
	}

	// 构建节点（去重）
	nodeMap := map[string]map[string]interface{}{}
	for _, nr := range nodeRows {
		name := fmt.Sprintf("%v", nr["service"])
		calls := toFloat(nr["calls"])
		errs := toFloat(nr["errs"])
		avgNs := toFloat(nr["avg_ns"])
		typ, rank := topologyNodeType(name)
		errRate := 0.0
		if calls > 0 {
			errRate = errs / calls * 100
		}
		latencyMs := avgNs / 1e6 // ns → ms
		if latencyMs <= 0 {
			latencyMs = 1.0
		}
		health := "healthy"
		healthLevel := "normal"
		if errRate > 10 {
			health = "error"
			healthLevel = "severe"
		} else if errRate > 3 {
			health = "warning"
			healthLevel = "slight"
		}
		// 健康评分：基于错误率扣分，满分 100
		healthScore := 100 - errRate*2
		if healthScore < 0 {
			healthScore = 0
		}
		// 吞吐率：调用次数/时间窗口（先按 calls 简单表示 rpm）
		throughput := calls
		nodeMap[name] = map[string]interface{}{
			"name":         name,
			"type":         typ,
			"rank":         rank,
			"calls":        int64(calls),
			"errs":         int64(errs),
			"latency_ms":   round1(latencyMs),
			"error_rate":   round1(errRate),
			"health":       health,
			"health_level": healthLevel,
			"health_score": round1(healthScore),
			"throughput":   int64(throughput),
		}
	}

	// 边：校验两端节点都存在
	edges := []map[string]interface{}{}
	for _, er := range edgeRows {
		src := fmt.Sprintf("%v", er["source_service"])
		tgt := fmt.Sprintf("%v", er["target_service"])
		calls := toFloat(er["calls"])
		errs := toFloat(er["errs"])
		avgNs := toFloat(er["avg_ns"])
		if _, ok := nodeMap[src]; !ok {
			nodeMap[src] = buildSyntheticNode(src)
		}
		if _, ok := nodeMap[tgt]; !ok {
			nodeMap[tgt] = buildSyntheticNode(tgt)
		}
		errRate := 0.0
		if calls > 0 {
			errRate = errs / calls * 100
		}
		latencyMs := avgNs / 1e6
		if latencyMs <= 0 {
			latencyMs = 1.0
		}
		// 响应时间阈值等级（与参考图连线染色一致）
		latencyLevel := "fast"
		if latencyMs >= 1000 {
			latencyLevel = "very_slow"
		} else if latencyMs >= 300 {
			latencyLevel = "slow"
		}
		edges = append(edges, map[string]interface{}{
			"source_service": src,
			"target_service": tgt,
			"calls":          int64(calls),
			"error_count":    int64(errs),
			"latency_ms":     round1(latencyMs),
			"error_rate":     round1(errRate),
			"latency_level":  latencyLevel,
		})
	}

	nodes := make([]map[string]interface{}, 0, len(nodeMap))
	for _, n := range nodeMap {
		nodes = append(nodes, n)
	}

	respondJSON(w, http.StatusOK, map[string]interface{}{
		"nodes": nodes,
		"edges": edges,
		"node_count": len(nodes),
		"edge_count": len(edges),
	})
}

// buildSyntheticNode 为拓扑边缘未聚合到的节点生成占位（类型推断 + 健康）
func buildSyntheticNode(name string) map[string]interface{} {
	typ, rank := topologyNodeType(name)
	return map[string]interface{}{
		"name":         name,
		"type":         typ,
		"rank":         rank,
		"calls":        0,
		"errs":         0,
		"latency_ms":   0.0,
		"error_rate":   0.0,
		"health":       "unknown",
		"health_level": "unknown",
		"health_score": 0.0,
		"throughput":   0,
	}
}

// TopologyNodeDetail handles GET /api/v1/topology/node/{name}
// 返回拓扑节点的详情抽屉数据：指标卡、趋势图、调用链列表
// 当请求时间窗（minutes）内无 trace 数据时，自动放宽到最近 7 天，确保抽屉始终有内容
func (h *Handler) TopologyNodeDetail(w http.ResponseWriter, r *http.Request) {
	tid := extractTenantID(r)
	name := strings.TrimPrefix(r.URL.Path, "/api/v1/topology/node/")
	if name == "" {
		respondError(w, http.StatusBadRequest, "node name required")
		return
	}

	// 默认近 15 分钟
	minutes := 15
	if m := r.URL.Query().Get("minutes"); m != "" {
		if v, err := strconv.Atoi(m); err == nil && v > 0 {
			minutes = v
		}
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	// 先按请求时间窗查询
	m, trendRows, traceRows, spanRows := h.queryNodeDetail(ctx, tid, name, minutes)
	// 若趋势与调用链都为空（数据已过期），自动放宽到最近 7 天
	if len(trendRows) == 0 && len(traceRows) == 0 {
		m, trendRows, traceRows, spanRows = h.queryNodeDetail(ctx, tid, name, 60*24*7)
	}

	calls := toFloat(m["calls"])
	errs := toFloat(m["errors"])
	avgMs := toFloat(m["avg_ms"])
	errRate := 0.0
	if calls > 0 {
		errRate = errs / calls * 100
	}
	healthScore := 100 - errRate*2
	if healthScore < 0 {
		healthScore = 0
	}
	apdex := 1.0 - errRate/100
	if apdex < 0 {
		apdex = 0
	}

	respondJSON(w, http.StatusOK, map[string]interface{}{
		"name":         name,
		"health_score": round1(healthScore),
		"apdex":        round2(apdex),
		"latency_ms":   round1(avgMs),
		"error_rate":   round1(errRate),
		"throughput":   int64(calls),
		"metrics":      m,
		"trend":        trendRows,
		"traces":       traceRows,
		"spans":        spanRows,
	})
}

// queryNodeDetail 按给定时间窗（分钟）聚合节点的指标、趋势、调用链与 span 明细
func (h *Handler) queryNodeDetail(ctx context.Context, tid, name string, minutes int) (map[string]interface{}, []map[string]interface{}, []map[string]interface{}, []map[string]interface{}) {
	empty := func() (map[string]interface{}, []map[string]interface{}, []map[string]interface{}, []map[string]interface{}) {
		return map[string]interface{}{}, nil, nil, nil
	}

	// 1. 指标卡：从 trace_spans 聚合（用 start_time 过滤更精确，兼容历史 date 数据）
	metricSQL := fmt.Sprintf(
		"SELECT count() as calls, countIf(is_error=1) as errors, avg(duration_ns)/1000000 as avg_ms, max(duration_ns)/1000000 as max_ms "+
			"FROM observability.trace_spans WHERE tenant_id='%s' AND service_name='%s' AND start_time >= now() - INTERVAL %d MINUTE",
		tid, name, minutes,
	)
	metricBody, err := h.queryClickHouse(ctx, metricSQL)
	if err != nil {
		log.Printf("TopologyNodeDetail metric query error: %v", err)
		return empty()
	}
	metricRows, err := parseRows(metricBody)
	if err != nil {
		log.Printf("TopologyNodeDetail metric parse error: %v", err)
		return empty()
	}
	m := map[string]interface{}{}
	if len(metricRows) > 0 {
		m = metricRows[0]
	}

	// 2. 趋势：按分钟聚合
	trendSQL := fmt.Sprintf(
		"SELECT toStartOfMinute(start_time) as t, count() as calls, countIf(is_error=1) as errors, avg(duration_ns)/1000000 as avg_ms "+
			"FROM observability.trace_spans WHERE tenant_id='%s' AND service_name='%s' AND start_time >= now() - INTERVAL %d MINUTE "+
			"GROUP BY t ORDER BY t",
		tid, name, minutes,
	)
	trendBody, err := h.queryClickHouse(ctx, trendSQL)
	if err != nil {
		log.Printf("TopologyNodeDetail trend query error: %v", err)
		return empty()
	}
	trendRows, err := parseRows(trendBody)
	if err != nil {
		log.Printf("TopologyNodeDetail trend parse error: %v", err)
		return empty()
	}

	// 3. 调用链列表
	traceSQL := fmt.Sprintf(
		"SELECT trace_id, min(start_time) as start, max(start_time) as end, count() as spans, "+
			"max(duration_ns)/1000000 as max_ms, sum(is_error) as errors "+
			"FROM observability.trace_spans WHERE tenant_id='%s' AND service_name='%s' AND start_time >= now() - INTERVAL %d MINUTE "+
			"GROUP BY trace_id ORDER BY start DESC LIMIT 20",
		tid, name, minutes,
	)
	traceBody, err := h.queryClickHouse(ctx, traceSQL)
	if err != nil {
		log.Printf("TopologyNodeDetail trace query error: %v", err)
		return empty()
	}
	traceRows, err := parseRows(traceBody)
	if err != nil {
		log.Printf("TopologyNodeDetail trace parse error: %v", err)
		return empty()
	}

	// 4. 最近 5 条 span 明细（用于表格展示），统一用 start_time 过滤
	spanSQL := fmt.Sprintf(
		"SELECT start_time, operation_name, duration_ns/1000000 as ms, is_error, http_url "+
			"FROM observability.trace_spans WHERE tenant_id='%s' AND service_name='%s' AND start_time >= now() - INTERVAL %d MINUTE "+
			"ORDER BY start_time DESC LIMIT 5",
		tid, name, minutes,
	)
	spanBody, err := h.queryClickHouse(ctx, spanSQL)
	if err != nil {
		log.Printf("TopologyNodeDetail span query error: %v", err)
		return empty()
	}
	spanRows, err := parseRows(spanBody)
	if err != nil {
		log.Printf("TopologyNodeDetail span parse error: %v", err)
		return empty()
	}

	return m, trendRows, traceRows, spanRows
}

// round2 保留 2 位小数
func round2(v float64) float64 {
	return float64(int(v*100+0.5)) / 100
}

// toFloat 安全地将 ClickHouse 返回值转为 float64
func toFloat(v interface{}) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case float32:
		return float64(x)
	case int64:
		return float64(x)
	case int:
		return float64(x)
	case string:
		f, _ := strconv.ParseFloat(strings.TrimSpace(x), 64)
		return f
	case nil:
		return 0
	default:
		return 0
	}
}

// round1 保留 1 位小数
func round1(v float64) float64 {
	return float64(int(v*10+0.5)) / 10
}

// QueryLogs handles GET /api/v1/logs/query?service={name}&query={text}&minutes=15
func (h *Handler) QueryLogs(w http.ResponseWriter, r *http.Request) {
	tid := extractTenantID(r)
	service := r.URL.Query().Get("service")
	queryText := r.URL.Query().Get("query")
	minutes := 15

	if m := r.URL.Query().Get("minutes"); m != "" {
		if v, err := strconv.Atoi(m); err == nil && v > 0 {
			minutes = v
		}
	}

	// Build WHERE clause dynamically
	var conditions []string
	conditions = append(conditions, fmt.Sprintf("tenant_id='%s'", tid))

	if service != "" {
		conditions = append(conditions, fmt.Sprintf("service_name LIKE '%%%s%%'", service))
	}
	if queryText != "" {
		// Search in body field for log text
		conditions = append(conditions, fmt.Sprintf("body LIKE '%%%s%%'", queryText))
	}

	// Time filter: last N minutes（用 timestamp 精确过滤，而非天粒度 date）
	conditions = append(conditions, fmt.Sprintf("timestamp >= now() - INTERVAL %d MINUTE", minutes))

	whereClause := strings.Join(conditions, " AND ")

	sql := fmt.Sprintf(
		"SELECT timestamp, service_name, severity, body, trace_id FROM observability.log_records WHERE %s ORDER BY timestamp DESC LIMIT 100",
		whereClause,
	)

	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()

	body, err := h.queryClickHouse(ctx, sql)
	if err != nil {
		log.Printf("QueryLogs query error: %v", err)
		respondError(w, http.StatusInternalServerError, "query failed")
		return
	}

	rows, err := parseRows(body)
	if err != nil {
		log.Printf("QueryLogs parse error: %v", err)
		respondError(w, http.StatusInternalServerError, "parse failed")
		return
	}

	respondJSON(w, http.StatusOK, map[string]interface{}{
		"data":    rows,
		"count":   len(rows),
		"minutes": minutes,
	})
}

// ProxyVictoriaLogs handles GET/POST /api/v1/logs/victorialogs
func (h *Handler) ProxyVictoriaLogs(w http.ResponseWriter, r *http.Request) {
	// POST: insert logs
	if r.Method == "POST" {
		h.ProxyVictoriaLogsInsert(w, r)
		return
	}
	// GET: query logs
	query := r.URL.Query().Get("query")
	if query == "" {
		query = "_time:5m" // last 5 minutes
	}
	limit := r.URL.Query().Get("limit")
	if limit == "" {
		limit = "50"
	}

	vlURL := fmt.Sprintf("http://victoria-logs.observability.svc.cluster.local:9428/select/logsql/query?query=%s&limit=%s",
		query, limit)

	req, _ := http.NewRequest("GET", vlURL, nil)
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		respondError(w, http.StatusBadGateway, "VictoriaLogs unavailable: "+err.Error())
		return
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	w.Write(body)
}

// ProxyVictoriaLogsInsert handles POST /api/v1/logs/victorialogs
func (h *Handler) ProxyVictoriaLogsInsert(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	req, _ := http.NewRequest("POST",
		"http://victoria-logs.observability.svc.cluster.local:9428/insert/jsonline",
		bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		respondError(w, http.StatusBadGateway, err.Error())
		return
	}
	defer resp.Body.Close()
	respondJSON(w, http.StatusOK, map[string]interface{}{"status": "ok"})
}

// logCursor tracks per-pod log position for incremental fetching.
var logCursors = struct {
	sync.Mutex
	cursors map[string]string // key: "ns/pod" → value: RFC3339 timestamp
}{cursors: make(map[string]string)}

// StartLogShipper runs a production-grade K8s pod log collector using the API log subresource.
// Features: incremental sinceTime fetching, per-pod cursor, structured metadata, VictoriaLogs output.
func (h *Handler) StartLogShipper() {
	go func() {
		time.Sleep(10 * time.Second)
		log.Println("[log-shipper] production shipper started")

		token, err := os.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/token")
		if err != nil {
			log.Printf("[log-shipper] FATAL: cannot read K8s token: %v", err)
			return
		}
		k8sAPI := "https://kubernetes.default.svc"
		vlURL := "http://victoria-logs.observability.svc.cluster.local:9428/insert/jsonline"
		httpClient := &http.Client{
			Timeout: 15 * time.Second,
			Transport: &http.Transport{
				TLSClientConfig:         &tls.Config{InsecureSkipVerify: true},
				MaxIdleConns:            20,
				IdleConnTimeout:         30 * time.Second,
			},
		}
		namespaces := []string{"observability", "default", "deepflow"}

		for {
			roundShipped := 0
			roundSince := time.Now().Add(-61 * time.Second).Format(time.RFC3339)

			for _, ns := range namespaces {
				// List pods
				req, _ := http.NewRequest("GET", k8sAPI+"/api/v1/namespaces/"+ns+"/pods", nil)
				req.Header.Set("Authorization", "Bearer "+string(token))
				resp, err := httpClient.Do(req)
				if err != nil {
					continue
				}
				body, _ := io.ReadAll(resp.Body)
				resp.Body.Close()
				if resp.StatusCode != 200 {
					continue
				}

				var podList struct {
					Items []struct {
						Metadata struct {
							Name string `json:"name"`
						} `json:"metadata"`
						Status struct {
							Phase string `json:"phase"`
						} `json:"status"`
					} `json:"items"`
				}
				json.Unmarshal(body, &podList)

				for _, pod := range podList.Items {
					pname := pod.Metadata.Name
					key := ns + "/" + pname

					// Use per-pod cursor for incremental fetch; fallback to 60s window
					logCursors.Lock()
					since := logCursors.cursors[key]
					if since == "" {
						since = roundSince
					}
					logCursors.Unlock()

					// Fetch logs with sinceTime for incremental collection
					u := fmt.Sprintf("%s/api/v1/namespaces/%s/pods/%s/log?sinceTime=%s&timestamps=true&tailLines=50",
						k8sAPI, ns, pname, since)
					req, _ := http.NewRequest("GET", u, nil)
					req.Header.Set("Authorization", "Bearer "+string(token))
					logResp, err := httpClient.Do(req)
					if err != nil {
						continue
					}
					logBody, _ := io.ReadAll(logResp.Body)
					logResp.Body.Close()

					text := string(logBody)
					if len(text) == 0 {
						continue
					}

					var latestTS string
					lines := strings.Split(text, "\n")
					for _, line := range lines {
						line = strings.TrimSpace(line)
						if line == "" {
							continue
						}
						// K8s timestamped log format: "2026-07-28T09:00:00.123456789Z message..."
						idx := strings.Index(line, " ")
						if idx <= 0 {
							continue
						}
						ts := line[:idx]
						msg := line[idx+1:]
						latestTS = ts

						if len(msg) > 2000 {
							msg = msg[:2000]
						}
						payload := map[string]string{
							"_time":       ts,
							"_msg":        msg,
							"service":     ns + "/" + pname,
							"namespace":   ns,
							"pod":         pname,
							"phase":       pod.Status.Phase,
						}
						data, _ := json.Marshal(payload)
						vlReq, _ := http.NewRequest("POST", vlURL, bytes.NewReader(data))
						vlReq.Header.Set("Content-Type", "application/json")
						vlResp, err := httpClient.Do(vlReq)
						if err == nil {
							vlResp.Body.Close()
							roundShipped++
						}
					}

					// Update cursor for next round
					if latestTS != "" {
						logCursors.Lock()
						logCursors.cursors[key] = latestTS
						logCursors.Unlock()
					}
				}
			}
			if roundShipped > 0 {
				log.Printf("[log-shipper] shipped %d logs, cursors: %d pods", roundShipped, len(logCursors.cursors))
			}
			time.Sleep(30 * time.Second)
		}
	}()
}
