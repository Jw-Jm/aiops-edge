package api

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/observability-platform/ai-apm-query-go/internal/biz"
	"github.com/observability-platform/ai-apm-query-go/internal/store"
)

// firstNonEmpty 返回第一个非空字符串（用于 env 缺省回退）。
func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// orchestratorBase 返回 ai-orchestrator 服务地址（可经 env 覆盖，可移植）。
func orchestratorBase() string {
	return firstNonEmpty(os.Getenv("AI_ORCHESTRATOR_URL"), "http://ai-orchestrator.observability.svc.cluster.local:8080")
}

// ProxyShellWS 代理 WebShell WebSocket 到 ai-orchestrator。
//
// 安全设计：WebSocket 无法携带 Authorization header，前端把 JWT 放在 ?token= query 参数。
// 本 handler 先验证该 JWT（证明是已登录用户），再把 query 的 token 替换为 INTERNAL_TOKEN
// 后转发给 orchestrator，让 orchestrator 的 shell_ws 校验通过。这样：
//   - 前端必须携带合法 JWT 才能建立终端（防未授权执行白名单命令）
//   - orchestrator 只信任经 query-api 代理注入的内部 token（防绕过直连）
func (h *Handler) ProxyShellWS(w http.ResponseWriter, r *http.Request) {
	// 1. 验证前端 JWT（?token=）
	userToken := r.URL.Query().Get("token")
	if userToken == "" {
		http.Error(w, "unauthorized: missing token", http.StatusUnauthorized)
		return
	}
	if _, _, _, ok := validateJWT(userToken); !ok {
		http.Error(w, "unauthorized: invalid token", http.StatusUnauthorized)
		return
	}
	// 2. 注入内部 token：替换 query 的 token 为 INTERNAL_TOKEN（orchestrator 据此校验）
	internal := os.Getenv("INTERNAL_TOKEN")
	if internal == "" {
		http.Error(w, "server misconfigured: INTERNAL_TOKEN not set", http.StatusInternalServerError)
		return
	}
	q := r.URL.Query()
	q.Set("token", internal)
	r.URL.RawQuery = q.Encode()
	r.URL.Path = "/api/v1/shell/ws"

	// 3. ReverseProxy 原生支持 WebSocket（自动处理 Upgrade 握手与双向字节流）
	target, err := url.Parse(orchestratorBase())
	if err != nil {
		http.Error(w, "bad orchestrator url", http.StatusInternalServerError)
		return
	}
	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.ServeHTTP(w, r)
}

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
	chHost     string
	chPort     int
	chUser     string // ClickHouse 用户名（经 env 注入，空则不启用认证）
	chPassword string // ClickHouse 密码（经 Secret 注入，空则不启用认证）
	client     *http.Client
	vmURL      string // VictoriaMetrics base URL（经 env 注入，可移植）
}

// NewHandler creates a new Handler.
func NewHandler(chHost string, chPort int) *Handler {
	return &Handler{
		chHost:     chHost,
		chPort:     chPort,
		chUser:     os.Getenv("CLICKHOUSE_USER"),
		chPassword: os.Getenv("CLICKHOUSE_PASSWORD"),
		client:     &http.Client{Timeout: 30 * time.Second},
		vmURL:      firstNonEmpty(os.Getenv("VICTORIA_METRICS_URL"), "http://victoria-metrics.observability.svc.cluster.local:8428"),
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

// extractClusterClause 返回按 cluster_id 过滤的 SQL 片段。
// 语义：cluster_id 为空或 "all" → 返回空串（查询所有集群，不追加过滤）；
// 其他值 → 返回 " AND cluster_id='xxx'"（仅查询该集群）。
func extractClusterClause(r *http.Request) string {
	cid := r.URL.Query().Get("cluster_id")
	if cid == "" || cid == "all" {
		return ""
	}
	return " AND cluster_id=" + chQuote(cid)
}

// extractClusterID 返回原始 cluster_id 值（供响应透传；空或 all 表示全部）。
func extractClusterID(r *http.Request) string {
	cid := r.URL.Query().Get("cluster_id")
	if cid == "" {
		return "all"
	}
	return cid
}

// chQuote 对拼入 ClickHouse SQL 的字符串字面量做安全转义，防止 SQL 注入。
// ClickHouse 字符串使用单引号包裹，其中单引号转义为两个单引号 ''，反斜杠转义为 \\。
// 所有由用户/外部输入拼入 SQL 的值都必须经过本函数后再嵌入。
func chQuote(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	return "'" + strings.ReplaceAll(s, `'`, `''`) + "'"
}

// chLike 构造 LIKE 模式的安全字符串（含 % 通配符转义），返回已加引号的字面量。
// pattern 中的 % 和 _ 会被转义为普通字符（用于精确包含匹配），并包在 %...% 中。
func chLike(pattern string) string {
	escaped := strings.ReplaceAll(pattern, `\`, `\\`)
	escaped = strings.ReplaceAll(escaped, `'`, `''`)
	// 转义 LIKE 通配符，避免用户输入影响匹配语义
	escaped = strings.ReplaceAll(escaped, `%`, `\%`)
	escaped = strings.ReplaceAll(escaped, `_`, `\_`)
	return "'%" + escaped + "%'"
}

// applyCHAuth 为 ClickHouse 请求附加 Basic Auth（若配置了 CLICKHOUSE_USER/PASSWORD）。
// 未配置时（本地/dev）保持无凭据，向后兼容。
func (h *Handler) applyCHAuth(req *http.Request) {
	if h.chUser != "" && h.chPassword != "" {
		req.SetBasicAuth(h.chUser, h.chPassword)
	}
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
	h.applyCHAuth(req)

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
	h.applyCHAuth(req)
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

	// 收敛：拓扑主数据源为 trace_spans 实时聚合（GlobalTopology），service_topology 为归档。
	// 本 handler 的硬编码调用依赖边（call_count=1）会覆盖 trace/DeepFlow 的真实指标，
	// 故停用 service_topology 边写入（数据归属治理：收敛拓扑多写）。
	respondJSON(w, http.StatusAccepted, map[string]interface{}{
		"message":  "topology sync disabled (trace 聚合为主源)",
		"services": len(svcSet),
	})
}

// SyncDataFromK8s 已废弃为只读接口。
// 该接口原用于"从 K8s 生成合成 trace 数据填充空表"，但会 TRUNCATE 掉 ingest 实时写入的真实链路数据。
// 现在 ingest 已实时落库 trace_spans/service_topology，此接口保留路由但不再写入任何数据，
// 避免清空真实链路数据（数据破坏型双写隐患）。返回说明即可。
func (h *Handler) SyncDataFromK8s(w http.ResponseWriter, r *http.Request) {
	respondJSON(w, http.StatusGone, map[string]interface{}{
		"message":  "deprecated: trace_spans/service_topology 现由 ingest 实时写入，此接口不再生成或清空数据",
		"expected": 0,
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
// 从 ClickHouse 动态发现服务，LEFT JOIN MySQL service_metadata 富化元数据。
// 数据归属治理：服务列表以 trace_spans 实际接入为准（source=trace），
// service_metadata 仅提供 owner/team/tier/description 富化，缺失字段走默认值。
func (h *Handler) ListServices(w http.ResponseWriter, r *http.Request) {
	tid := extractTenantID(r)
	_ = tid // CH 查询暂不用 tenant 过滤（trace_spans 无 tenant_id 列）

	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()

	// 1. 从 ClickHouse 拿动态服务列表（近 7 天有 trace 的服务）
	chSQL := `SELECT DISTINCT service_name
              FROM observability.trace_spans
              WHERE date >= today()-7
                AND service_name != ''
              ORDER BY service_name`
	body, err := h.queryClickHouse(ctx, chSQL)
	if err != nil {
		log.Printf("ListServices CH query error: %v", err)
		respondError(w, http.StatusInternalServerError, "query failed")
		return
	}
	rows, err := parseRows(body)
	if err != nil {
		log.Printf("ListServices parse error: %v", err)
		respondError(w, http.StatusInternalServerError, "parse failed")
		return
	}

	// 提取服务名列表
	services := make([]string, 0, len(rows))
	for _, row := range rows {
		if name, ok := row["service_name"].(string); ok && name != "" {
			services = append(services, name)
		}
	}

	// 2. 从 MySQL 拿富化元数据（LEFT JOIN 语义：缺失则用默认值）
	meta := h.loadServiceMetadataForHandler(services)

	// 3. 组装响应：每个服务一行，富化字段缺失时走默认值
	result := make([]map[string]interface{}, 0, len(services))
	for _, svc := range services {
		m := meta[svc]
		item := map[string]interface{}{
			"service_name": svc,
			"owner":        "",
			"team":         "",
			"tier":         "standard",
			"description":  "",
			"source":       "trace",
		}
		if m != nil {
			if m.Owner != "" {
				item["owner"] = m.Owner
			}
			if m.Team != "" {
				item["team"] = m.Team
			}
			if m.Tier != "" {
				item["tier"] = m.Tier
			}
			if m.Description != "" {
				item["description"] = m.Description
			}
		}
		result = append(result, item)
	}

	respondJSON(w, http.StatusOK, map[string]interface{}{
		"services": result,
		"total":    len(result),
	})
}

// serviceMeta 是 service_metadata 表的行映射（富化元数据）。
type serviceMeta struct {
	Owner       string
	Team        string
	Tier        string
	Description string
}

// loadServiceMetadata 批量加载服务富化元数据。
// db 为 nil 时返回空 map，调用方降级为默认值。
// 这实现了 LEFT JOIN 语义：CH 中存在的服务即使 MySQL 无对应行也保留，富化字段走默认。
func loadServiceMetadata(services []string, db *sql.DB) map[string]*serviceMeta {
	result := make(map[string]*serviceMeta)
	if len(services) == 0 {
		return result
	}
	if db == nil {
		return result
	}

	// 构造 IN 子句的占位符与参数（参数化查询防 SQL 注入）
	placeholders := make([]string, len(services))
	args := make([]interface{}, len(services))
	for i, svc := range services {
		placeholders[i] = "?"
		args[i] = svc
	}
	query := fmt.Sprintf("SELECT service_name, owner, team, tier, description FROM service_metadata WHERE service_name IN (%s)",
		strings.Join(placeholders, ","))

	rows, err := db.Query(query, args...)
	if err != nil {
		log.Printf("loadServiceMetadata query error: %v", err)
		return result
	}
	defer rows.Close()

	for rows.Next() {
		var name, owner, team, tier, desc string
		if err := rows.Scan(&name, &owner, &team, &tier, &desc); err != nil {
			continue
		}
		result[name] = &serviceMeta{Owner: owner, Team: team, Tier: tier, Description: desc}
	}
	return result
}

// loadServiceMetadataForHandler 是 Handler 对包级 loadServiceMetadata 的封装，
// 从全局 store.GetDB() 取连接（MySQL 不可达时返回空 map，调用方降级）。
func (h *Handler) loadServiceMetadataForHandler(services []string) map[string]*serviceMeta {
	return loadServiceMetadata(services, store.GetDB())
}

// ServiceDetail handles GET /api/v1/services/{name}
func (h *Handler) ServiceDetail(w http.ResponseWriter, r *http.Request) {
	tid := extractTenantID(r)
	clusterClause := extractClusterClause(r)
	// Extract service name from URL path: strip "/api/v1/services/" prefix
	name := strings.TrimPrefix(r.URL.Path, "/api/v1/services/")
	name = strings.TrimRight(name, "/")
	if name == "" {
		respondError(w, http.StatusBadRequest, "service name required")
		return
	}

	sql := fmt.Sprintf(
		"SELECT toStartOfMinute(start_time) as t, count() as calls, countIf(is_error=1) as errors, avg(duration_ns)/1000000 as avg_ms FROM observability.trace_spans WHERE tenant_id=%s%s AND service_name=%s AND date >= today()-1 GROUP BY t ORDER BY t",
		chQuote(tid), clusterClause, chQuote(name),
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
	clusterClause := extractClusterClause(r)
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
		serviceClause = fmt.Sprintf("AND service_name=%s", chQuote(serviceFilter))
	}

	// 2.7 搜索框：支持按 trace_id / operation / http_url 文本搜索
	searchClause := ""
	if s := r.URL.Query().Get("search"); s != "" {
		searchClause = fmt.Sprintf(" AND (trace_id LIKE %s OR operation_name LIKE %s OR http_url LIKE %s)", chLike(s), chLike(s), chLike(s))
	}

	sql := fmt.Sprintf(
		"SELECT trace_id, min(start_time) as start, max(start_time) as end, count() as spans, count(DISTINCT service_name) as services, max(duration_ns)/1000000 as max_ms FROM observability.trace_spans WHERE tenant_id=%s%s%s %s GROUP BY trace_id ORDER BY start DESC LIMIT %d OFFSET %d",
		chQuote(tid), clusterClause, searchClause, serviceClause, limit, offset,
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
	clusterClause := extractClusterClause(r)
	traceID := strings.TrimPrefix(r.URL.Path, "/api/v1/traces/")
	traceID = strings.TrimRight(traceID, "/")
	if traceID == "" {
		respondError(w, http.StatusBadRequest, "trace id required")
		return
	}

	sql := fmt.Sprintf(
		"SELECT span_id, parent_span_id, service_name, operation_name, span_kind, start_time, duration_ns/1000000 as ms, is_error FROM observability.trace_spans WHERE tenant_id=%s%s AND trace_id=%s ORDER BY start_time",
		chQuote(tid), clusterClause, chQuote(traceID),
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
	clusterClause := extractClusterClause(r)
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
		"SELECT DISTINCT service_name FROM observability.trace_spans WHERE tenant_id=%s%s AND trace_id=%s LIMIT 1",
		chQuote(tid), clusterClause, chQuote(traceID),
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
			"SELECT timestamp, service_name, severity, body FROM observability.log_records WHERE tenant_id=%s%s AND trace_id=%s ORDER BY timestamp DESC LIMIT 50",
			chQuote(tid), clusterClause, chQuote(traceID),
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
			"SELECT toStartOfMinute(start_time) as t, count() as call_count, countIf(is_error=1) as error_count, avg(duration_ns)/1000000 as avg_ms FROM observability.trace_spans WHERE tenant_id=%s%s AND service_name=%s AND start_time >= now() - INTERVAL 30 MINUTE GROUP BY t ORDER BY t",
			chQuote(tid), clusterClause, chQuote(serviceName),
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
						"rule_name": ev.RuleName,
						"severity":  ev.Severity,
						"message":   ev.Message,
						"count":     ev.Count,
						"last_time": ev.LastTimestamp,
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
// service 参数可选：有则按服务过滤，无则返回全局聚合
func (h *Handler) QueryMetrics(w http.ResponseWriter, r *http.Request) {
	tid := extractTenantID(r)
	clusterClause := extractClusterClause(r)
	service := r.URL.Query().Get("service")

	serviceClause := ""
	if service != "" {
		serviceClause = fmt.Sprintf(" AND service_name=%s", chQuote(service))
	}

	sql := fmt.Sprintf(
		"SELECT toStartOfMinute(start_time) as t, count() as call_count, "+
			"countIf(is_error=1) as error_count, avg(duration_ns)/1000000 as avg_ms, "+
			"quantile(0.50)(duration_ns)/1000000 as p50_ms, "+
			"quantile(0.95)(duration_ns)/1000000 as p95_ms, "+
			"quantile(0.99)(duration_ns)/1000000 as p99_ms "+
			"FROM observability.trace_spans WHERE tenant_id=%s%s%s AND date >= today()-1 "+
			"GROUP BY t ORDER BY t",
		chQuote(tid), clusterClause, serviceClause,
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
	clusterClause := extractClusterClause(r)
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()

	sql := fmt.Sprintf(
		"SELECT service_name, count() as calls, countIf(is_error=1) as errors, sum(duration_ns) as lat_sum FROM observability.trace_spans WHERE tenant_id=%s%s AND date >= today()-1 GROUP BY service_name ORDER BY calls DESC LIMIT 20",
		chQuote(tid), clusterClause,
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

	// 拓扑边数（与 GlobalTopology 一致的降级链：service_topology → MySQL topology_relations）
	edgeCount := int64(0)
	edgeSQL := fmt.Sprintf("SELECT count() FROM observability.service_topology WHERE tenant_id=%s%s AND date >= today()-1", chQuote(tid), clusterClause)
	if eb, err := h.queryClickHouse(ctx, edgeSQL); err == nil {
		if er, perr := parseRows(eb); perr == nil && len(er) > 0 {
			if n, ok := toInt64(er[0]["count()"]); ok {
				edgeCount = n
			}
		}
	}
	if edgeCount > 0 {
		stats.Edges = edgeCount
	} else if mysqlEdges := loadTopologyEdgesFromMySQL(); len(mysqlEdges) > 0 {
		stats.Edges = int64(len(mysqlEdges))
	}

	// P95 延迟（给聚合列加别名，避免 ClickHouse 将 / 规范化为 divide() 导致 key 匹配失败）
	p95SQL := fmt.Sprintf("SELECT round(quantile(0.95)(duration_ns)/1000000, 2) AS p95_ms FROM observability.trace_spans WHERE tenant_id=%s%s AND date >= today()-1", chQuote(tid), clusterClause)
	if pb, err := h.queryClickHouse(ctx, p95SQL); err == nil {
		if pr, perr := parseRows(pb); perr == nil && len(pr) > 0 {
			if v, ferr := toFloat64(pr[0]["p95_ms"]); ferr == nil {
				stats.LatencyP95 = v
			}
		}
	}

	// 近 24h 调用/错误趋势（按小时）
	trendSQL := fmt.Sprintf(
		"SELECT toString(toStartOfHour(start_time)) AS t, count() AS calls, countIf(is_error=1) AS errors "+
			"FROM observability.trace_spans WHERE tenant_id=%s%s AND date >= today()-1 "+
			"GROUP BY t ORDER BY t LIMIT 24", chQuote(tid), clusterClause)
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
			"WHERE tenant_id=%s%s AND date >= today()-1 AND is_error=1 GROUP BY s ORDER BY errors DESC LIMIT 10", chQuote(tid), clusterClause)
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
	clusterClause := extractClusterClause(r)

	// 时间过滤：minutes 参数（前端"近 N 分钟"），默认 24 小时，避免清理/低流量期出现空拓扑
	minutes := 1440
	if m := r.URL.Query().Get("minutes"); m != "" {
		if v, err := strconv.Atoi(m); err == nil && v > 0 {
			minutes = v
		}
	}
	timeCond := fmt.Sprintf(" AND start_time >= now() - INTERVAL %d MINUTE", minutes)

	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()

	// 边聚合：优先 service_topology（真实调用边），无数据时回退到 trace_spans 按服务对聚合
	// service_topology 用 time_bucket 列做时间过滤
	edgeCond := fmt.Sprintf(" AND time_bucket >= now() - INTERVAL %d MINUTE", minutes)
	edgeSQL := fmt.Sprintf(
		"SELECT source_service, target_service, sum(call_count) AS calls, sum(error_count) AS errs, avg(avg_duration_ns) AS avg_ns "+
			"FROM observability.service_topology WHERE tenant_id=%s%s%s "+
			"GROUP BY source_service, target_service ORDER BY calls DESC LIMIT 200",
		chQuote(tid), clusterClause, edgeCond,
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
	// P0-2: 若 service_topology 无调用边，回退到 trace_spans 按 trace 内相邻 span 的服务对聚合（尽力而为）
	if len(edgeRows) == 0 {
		fallbackSQL := fmt.Sprintf(
			"SELECT s1.service_name AS source_service, s2.service_name AS target_service, count() AS calls, 0 AS errs, avg(s2.duration_ns) AS avg_ns "+
				"FROM observability.trace_spans AS s1 "+
				"JOIN observability.trace_spans AS s2 ON s1.trace_id = s2.trace_id AND s1.span_id = s2.parent_span_id "+
				"WHERE s1.tenant_id=%s%s AND s1.start_time >= now() - INTERVAL %d MINUTE "+
				"GROUP BY s1.service_name, s2.service_name ORDER BY calls DESC LIMIT 200",
			chQuote(tid), clusterClause, minutes,
		)
		if fb, err := h.queryClickHouse(ctx, fallbackSQL); err == nil {
			if fr, perr := parseRows(fb); perr == nil {
				edgeRows = fr
			}
		}
	}
	// P0-2: 若 ClickHouse 仍无边（service_topology 为空且 trace 无 parent_span_id），
	// 回退到 MySQL topology_relations（由 SyncTopologyCatalog 按 trace 内服务时序生成的真实调用边）。
	if len(edgeRows) == 0 {
		if mysqlEdges := loadTopologyEdgesFromMySQL(); len(mysqlEdges) > 0 {
			edgeRows = mysqlEdges
		}
	}

	// 节点聚合（真实 trace）：按 service_name 聚合调用量、错误、平均延迟
	nodeSQL := fmt.Sprintf(
		"SELECT service_name AS service, count() AS calls, countIf(is_error=1) AS errs, avg(duration_ns) AS avg_ns "+
			"FROM observability.trace_spans WHERE tenant_id=%s%s%s GROUP BY service_name ORDER BY calls DESC LIMIT 200",
		chQuote(tid), clusterClause, timeCond,
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
		"nodes":      nodes,
		"edges":      edges,
		"node_count": len(nodes),
		"edge_count": len(edges),
	})
}

// loadTopologyEdgesFromMySQL 从 MySQL topology_relations（关联 topology_nodes）读取真实调用边。
// 返回与 ClickHouse 边行同构的 map 切片（source_service/target_service/calls/errs/avg_ns），
// 供 GlobalTopology 复用统一边构建逻辑。数据由 SyncTopologyCatalog 按 trace 内服务时序生成。
func loadTopologyEdgesFromMySQL() []map[string]interface{} {
	nd := &store.TopologyNodeDAO{}
	rd := &store.TopologyRelationDAO{}
	rels, _, err := rd.List(0, 0, "", 500, 0)
	if err != nil {
		return nil
	}
	// 一次拉全节点建立 id → name 映射（避免逐 id Get 在缺省 mysql 下静默失败）
	id2name := map[int64]string{}
	nodes, _, nerr := nd.List("", "", 500, 0)
	if nerr == nil {
		for _, n := range nodes {
			id2name[n.ID] = n.Name
		}
	}
	rows := []map[string]interface{}{}
	for _, rel := range rels {
		src := id2name[rel.SrcID]
		dst := id2name[rel.DstID]
		if src == "" || dst == "" || src == dst {
			continue
		}
		rows = append(rows, map[string]interface{}{
			"source_service": src,
			"target_service": dst,
			"calls":          float64(1),
			"errs":           float64(0),
			"avg_ns":         float64(1e6), // 1ms 默认，边无延迟时给个合理占位
		})
	}
	return rows
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
	clusterClause := extractClusterClause(r)
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
	m, trendRows, traceRows, spanRows := h.queryNodeDetail(ctx, tid, name, minutes, clusterClause)
	// 若趋势与调用链都为空（数据已过期），自动放宽到最近 7 天
	if len(trendRows) == 0 && len(traceRows) == 0 {
		m, trendRows, traceRows, spanRows = h.queryNodeDetail(ctx, tid, name, 60*24*7, clusterClause)
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
func (h *Handler) queryNodeDetail(ctx context.Context, tid, name string, minutes int, clusterClause string) (map[string]interface{}, []map[string]interface{}, []map[string]interface{}, []map[string]interface{}) {
	empty := func() (map[string]interface{}, []map[string]interface{}, []map[string]interface{}, []map[string]interface{}) {
		return map[string]interface{}{}, nil, nil, nil
	}

	// 1. 指标卡：从 trace_spans 聚合（用 start_time 过滤更精确，兼容历史 date 数据）
	metricSQL := fmt.Sprintf(
		"SELECT count() as calls, countIf(is_error=1) as errors, avg(duration_ns)/1000000 as avg_ms, max(duration_ns)/1000000 as max_ms "+
			"FROM observability.trace_spans WHERE tenant_id=%s%s AND service_name=%s AND start_time >= now() - INTERVAL %d MINUTE",
		chQuote(tid), clusterClause, chQuote(name), minutes,
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
			"FROM observability.trace_spans WHERE tenant_id=%s%s AND service_name=%s AND start_time >= now() - INTERVAL %d MINUTE "+
			"GROUP BY t ORDER BY t",
		chQuote(tid), clusterClause, chQuote(name), minutes,
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
			"FROM observability.trace_spans WHERE tenant_id=%s%s AND service_name=%s AND start_time >= now() - INTERVAL %d MINUTE "+
			"GROUP BY trace_id ORDER BY start DESC LIMIT 20",
		chQuote(tid), clusterClause, chQuote(name), minutes,
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
	// Issue4: 补充 service_name/span_id/trace_id 字段，前端 span 明细表"服务"列及行 key 才有值
	spanSQL := fmt.Sprintf(
		"SELECT span_id, trace_id, start_time, service_name, operation_name, duration_ns/1000000 as ms, is_error, http_url "+
			"FROM observability.trace_spans WHERE tenant_id=%s%s AND service_name=%s AND start_time >= now() - INTERVAL %d MINUTE "+
			"ORDER BY start_time DESC LIMIT 5",
		chQuote(tid), clusterClause, chQuote(name), minutes,
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
// source 参数：clickhouse（默认）或 victorialogs。切到 victorialogs 时路由到 VictoriaLogs 并归一化字段，
// 避免 UI 显示 VictoriaLogs 标签但实际返回 ClickHouse 数据（P0-1）。
func (h *Handler) QueryLogs(w http.ResponseWriter, r *http.Request) {
	tid := extractTenantID(r)
	service := r.URL.Query().Get("service")
	queryText := r.URL.Query().Get("query")
	source := r.URL.Query().Get("source")
	minutes := 1440 // 默认近 24 小时

	if m := r.URL.Query().Get("minutes"); m != "" {
		if v, err := strconv.Atoi(m); err == nil && v > 0 {
			minutes = v
		}
	} else if h := r.URL.Query().Get("hours"); h != "" {
		// P0-1 修复：兼容前端 hours 参数（hours*60 = minutes）
		if v, err := strconv.Atoi(h); err == nil && v > 0 {
			minutes = v * 60
		}
	}

	// P0-1 修复：source=victorialogs 时路由到 VictoriaLogs（K8s pod 日志），归一化后返回
	if strings.EqualFold(source, "victorialogs") {
		rows, err := h.queryVictoriaLogs(service, queryText, minutes)
		if err != nil {
			log.Printf("QueryLogs(victorialogs) error: %v", err)
			respondError(w, http.StatusBadGateway, "VictoriaLogs unavailable: "+err.Error())
			return
		}
		respondJSON(w, http.StatusOK, map[string]interface{}{
			"data":    rows,
			"count":   len(rows),
			"minutes": minutes,
			"source":  "victorialogs",
		})
		return
	}

	// Build WHERE clause dynamically
	var conditions []string
	conditions = append(conditions, fmt.Sprintf("tenant_id=%s", chQuote(tid)))

	// 多集群过滤：cluster_id 为空或 all 时不追加（查询所有集群）
	if cc := extractClusterClause(r); cc != "" {
		conditions = append(conditions, strings.TrimPrefix(cc, " AND "))
	}

	if service != "" {
		conditions = append(conditions, fmt.Sprintf("service_name LIKE %s", chLike(service)))
	}
	if queryText != "" {
		// Search in body field for log text
		conditions = append(conditions, fmt.Sprintf("body LIKE %s", chLike(queryText)))
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

// LogAggregate handles GET /api/v1/logs/aggregate?service={name}&query={text}&minutes=60&interval=5
// 按 interval 聚合日志量，并给出级别分布与服务 TOP。
func (h *Handler) LogAggregate(w http.ResponseWriter, r *http.Request) {
	tid := extractTenantID(r)
	service := r.URL.Query().Get("service")
	queryText := r.URL.Query().Get("query")
	minutes := 1440
	interval := 5
	if v := r.URL.Query().Get("minutes"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			minutes = n
		}
	} else if h := r.URL.Query().Get("hours"); h != "" {
		// P0-1 修复：兼容前端 hours 参数
		if n, err := strconv.Atoi(h); err == nil && n > 0 {
			minutes = n * 60
		}
	}
	if v := r.URL.Query().Get("interval"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			interval = n
		}
	}
	conditions := []string{fmt.Sprintf("tenant_id=%s", chQuote(tid))}
	// 多集群过滤：cluster_id 为空或 all 时不追加（查询所有集群）
	if cc := extractClusterClause(r); cc != "" {
		conditions = append(conditions, strings.TrimPrefix(cc, " AND "))
	}
	if service != "" {
		conditions = append(conditions, fmt.Sprintf("service_name LIKE %s", chLike(service)))
	}
	if queryText != "" {
		conditions = append(conditions, fmt.Sprintf("body LIKE %s", chLike(queryText)))
	}
	conditions = append(conditions, fmt.Sprintf("timestamp >= now() - INTERVAL %d MINUTE", minutes))
	whereClause := strings.Join(conditions, " AND ")

	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()

	// 1. 时间序列（每 interval 的日志量）
	trendSQL := fmt.Sprintf(
		"SELECT toStartOfInterval(timestamp, INTERVAL %d MINUTE) AS bucket, count() AS cnt FROM observability.log_records WHERE %s GROUP BY bucket ORDER BY bucket",
		interval, whereClause)
	trendBody, err := h.queryClickHouse(ctx, trendSQL)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "aggregate failed")
		return
	}
	trendRows, _ := parseRows(trendBody)
	trend := []map[string]interface{}{}
	for _, row := range trendRows {
		trend = append(trend, map[string]interface{}{
			"bucket": row["bucket"], "count": row["cnt"],
		})
	}

	// 2. 级别分布
	levelSQL := fmt.Sprintf(
		"SELECT severity AS level, count() AS cnt FROM observability.log_records WHERE %s GROUP BY severity ORDER BY cnt DESC",
		whereClause)
	levelBody, err := h.queryClickHouse(ctx, levelSQL)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "aggregate failed")
		return
	}
	levelRows, _ := parseRows(levelBody)
	levels := []map[string]interface{}{}
	for _, row := range levelRows {
		levels = append(levels, map[string]interface{}{"level": row["level"], "count": row["cnt"]})
	}

	// 3. 服务 TOP
	svcSQL := fmt.Sprintf(
		"SELECT service_name AS service, count() AS cnt FROM observability.log_records WHERE %s GROUP BY service_name ORDER BY cnt DESC LIMIT 10",
		whereClause)
	svcBody, err := h.queryClickHouse(ctx, svcSQL)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "aggregate failed")
		return
	}
	svcRows, _ := parseRows(svcBody)
	services := []map[string]interface{}{}
	for _, row := range svcRows {
		services = append(services, map[string]interface{}{"service": row["service"], "count": row["cnt"]})
	}

	respondJSON(w, http.StatusOK, map[string]interface{}{
		"trend": trend, "levels": levels, "services": services, "minutes": minutes, "interval": interval,
	})
}

// queryVictoriaLogs 查询 VictoriaLogs（K8s pod 日志）并归一化为与 ClickHouse 行同构的结构。
// 返回 []map[string]interface{}{timestamp, service_name, severity, body, trace_id, source}，
// 前端统一按 body/service_name/severity/timestamp 字段展示（P0-1 修复）。
func (h *Handler) queryVictoriaLogs(service, queryText string, minutes int) ([]map[string]interface{}, error) {
	logsQL := buildVictoriaLogsSQL(service, queryText, minutes)
	vlURL := fmt.Sprintf("http://victoria-logs.observability.svc.cluster.local:9428/select/logsql/query?query=%s&limit=100",
		url.QueryEscape(logsQL))
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", vlURL, nil)
	if err != nil {
		return nil, err
	}
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	return normalizeVictoriaLogsRows(body), nil
}

// buildVictoriaLogsSQL 构造 VictoriaLogs LogsQL 查询串（时间过滤 + 可选服务/关键词）。
func buildVictoriaLogsSQL(service, queryText string, minutes int) string {
	parts := []string{fmt.Sprintf("_time:%dm", minutes)}
	if service != "" {
		// service 字段形如 "observability/query-api-xxx"，用 LogsQL 模糊匹配 service:"xxx"*
		parts = append(parts, fmt.Sprintf("service:%q*", service))
	}
	if queryText != "" {
		parts = append(parts, fmt.Sprintf("%q", queryText))
	}
	return strings.Join(parts, " ")
}

// normalizeVictoriaLogsRows 将 VictoriaLogs JSON Lines 归一化为前端期望的字段结构。
// 字段映射：timestamp ← _time；service_name ← service（去 namespace 前缀）或 pod；body ← _msg。
func normalizeVictoriaLogsRows(body []byte) []map[string]interface{} {
	rows := []map[string]interface{}{}
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var obj map[string]interface{}
		if err := json.Unmarshal([]byte(line), &obj); err != nil {
			continue
		}
		ts, _ := obj["_time"].(string)
		svcName := ""
		if s, ok := obj["service"].(string); ok {
			svcName = s
			if idx := strings.Index(svcName, "/"); idx >= 0 {
				svcName = svcName[idx+1:]
			}
		}
		if svcName == "" {
			if p, ok := obj["pod"].(string); ok {
				svcName = p
			} else {
				svcName = "-"
			}
		}
		msg, _ := obj["_msg"].(string)
		rows = append(rows, map[string]interface{}{
			"timestamp":    ts,
			"service_name": svcName,
			"severity":     "info", // pod 生命周期事件无级别，统一 info
			"body":         msg,
			"trace_id":     "",
			"source":       "victorialogs",
		})
	}
	return rows
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
