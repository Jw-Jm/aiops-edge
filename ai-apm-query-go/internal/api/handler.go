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
	"net/url"
	"os"
	"sort"
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

// isDeletedService 判断服务名是否为已删除残留（以 "(deleted)" 结尾）。
// P1-5 服务口径统一：活跃服务 = 最近 24h 有流量 且 service_name 不以 "(deleted)" 结尾。
func isDeletedService(name string) bool {
	return strings.HasSuffix(name, "(deleted)")
}

// orchestratorBase 返回 ai-orchestrator 服务地址（可经 env 覆盖，可移植）。
func orchestratorBase() string {
	return firstNonEmpty(os.Getenv("AI_ORCHESTRATOR_URL"), "http://ai-orchestrator.observability.svc.cluster.local:8080")
}

// ProxyShellWS keeps R4 shell outside browser authorization and canonical
// query-api read/write paths. Manual access is configured directly on the
// orchestrator and cannot be enabled by JWT or legacy internal headers.
func (h *Handler) ProxyShellWS(w http.ResponseWriter, r *http.Request) {
	respondJSON(w, http.StatusForbidden, map[string]interface{}{"error": "permission_denied"})
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
	vmURL      string         // VictoriaMetrics base URL（经 env 注入，可移植）
	podNS      *podNSResolver // K8s pod→ns 兜底映射（GlobalTopology 对空 ns 服务使用；nil 时不启用）
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
		podNS:      newPodNSResolver(),
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

// scopeServicesClause 若 scope.services 非空，返回 " AND service_name IN (...)" SQL 子句；
// 空 scope（不限制）= 返回空串，保持全量兼容（安全 P1-2 最小实现）。
func scopeServicesClause(r *http.Request) string {
	return scopeINClause(r, "")
}

// scopeINClause 构造 scope.services 的 IN 过滤子句；prefix 为列名前缀（如 "s1."、"s2."）。
func scopeINClause(r *http.Request, prefix string) string {
	svcs := currentScope(r).Services
	if len(svcs) == 0 {
		return ""
	}
	quoted := make([]string, 0, len(svcs))
	for _, s := range svcs {
		quoted = append(quoted, chQuote(s))
	}
	return " AND " + prefix + "service_name IN (" + strings.Join(quoted, ",") + ")"
}

// scopeEdgeClause 用于 source_service/target_service 型查询（service_topology）：
// scope.services 非空时要求边两端都在授权范围内。
func scopeEdgeClause(r *http.Request) string {
	svcs := currentScope(r).Services
	if len(svcs) == 0 {
		return ""
	}
	quoted := make([]string, 0, len(svcs))
	for _, s := range svcs {
		quoted = append(quoted, chQuote(s))
	}
	in := "(" + strings.Join(quoted, ",") + ")"
	return " AND source_service IN " + in + " AND target_service IN " + in
}

// chDateWindow 根据分钟窗口生成 CH date 分区谓词（数据 P1-4/P1-5 修复），
// 避免时间窗口查询退化为全分区扫描。与分钟窗口对齐：
// 5 MINUTE → today()；24H(1440min) → today()-1；48H → today()-2。
func chDateWindow(minutes int) string {
	return fmt.Sprintf("date >= today() - %d", minutes/1440)
}

// chQuote 对拼入 ClickHouse SQL 的字符串字面量做安全转义，防止 SQL 注入。
// ClickHouse 字符串使用单引号包裹，其中单引号转义为两个单引号 ”，反斜杠转义为 \\。
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
// P1-5 服务口径统一：活跃服务 = 最近 24h 有流量 且 不以 "(deleted)" 结尾；
// 与 /dashboard/stats 的 services 计数同款过滤。默认剔除 deleted 残留，
// 传 include_deleted=true 可放开（此时条目带 deleted:true 标记供前端区分）。
func (h *Handler) ListServices(w http.ResponseWriter, r *http.Request) {
	tid := extractTenantID(r)
	// 数据(P0-1)：trace_spans 有 tenant_id 列，必须按租户过滤（原注释"无 tenant_id 列"有误）
	tenantClause := " AND tenant_id=" + chQuote(tid)
	scopeClause := scopeServicesClause(r)    // 安全(P1-2)：scope.services 非空时按服务范围过滤
	clusterClause := extractClusterClause(r) // A-3 修复：服务列表按集群过滤
	// P1-5：默认过滤 deleted 残留服务；include_deleted=true 时放开
	includeDeleted := r.URL.Query().Get("include_deleted") == "true"

	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()

	// 1. 从 ClickHouse 拿动态服务列表（P1-5：近 24h 有 trace 的活跃服务，与 stats 口径一致）
	chSQL := `SELECT DISTINCT service_name
              FROM observability.trace_spans
              WHERE date >= today()-1` + tenantClause + clusterClause + scopeClause + `
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

	// 提取服务名列表（剔除 deleted 残留，除非 include_deleted=true）
	services := make([]string, 0, len(rows))
	for _, row := range rows {
		if name, ok := row["service_name"].(string); ok && name != "" {
			if !includeDeleted && isDeletedService(name) {
				continue
			}
			services = append(services, name)
		}
	}

	// 2. 从 ClickHouse 拿各服务指标聚合（调用量/错误数/平均延迟，近 24h）
	//    解决服务列表三项指标(calls/errors/avg_latency_ms)恒为 0 的问题。
	metricsSQL := `SELECT service_name AS service, count() AS calls,
	                     countIf(is_error=1) AS errs, avg(duration_ns)/1000000 AS avg_ms
	              FROM observability.trace_spans
	              WHERE date >= today()-1` + tenantClause + clusterClause + scopeClause + `
	              GROUP BY service_name`
	metricsBody, err := h.queryClickHouse(ctx, metricsSQL)
	if err != nil {
		log.Printf("ListServices CH metrics query error: %v", err)
	}
	metrics := make(map[string]map[string]interface{})
	if mrows, perr := parseRows(metricsBody); perr == nil {
		for _, row := range mrows {
			svc, _ := row["service"].(string)
			if svc == "" {
				continue
			}
			// count()/countIf() 在 ClickHouse JSONEachRow 中可能返回字符串（如 "3579"），
			// 用 toFloat 兼容数字/字符串两种类型，避免类型断言失败导致 calls/errors 为 0。
			calls := toFloat(row["calls"])
			errs := toFloat(row["errs"])
			avgMS := toFloat(row["avg_ms"])
			errorRate := 0.0
			if calls > 0 {
				errorRate = errs / calls
			}
			metrics[svc] = map[string]interface{}{
				"calls":          int64(calls),
				"errors":         int64(errs),
				"error_rate":     errorRate,
				"avg_latency_ms": round2(avgMS),
			}
		}
	}

	// 3. 从 MySQL 拿富化元数据（LEFT JOIN 语义：缺失则用默认值）
	meta := h.loadServiceMetadataForHandler(services)

	// 4. 组装响应：每个服务一行，富化字段缺失时走默认值，指标缺失时为 0
	result := make([]map[string]interface{}, 0, len(services))
	for _, svc := range services {
		m := meta[svc]
		item := map[string]interface{}{
			"service_name":   svc,
			"owner":          "",
			"team":           "",
			"tier":           "standard",
			"description":    "",
			"source":         "trace",
			"calls":          int64(0),
			"errors":         int64(0),
			"error_rate":     0.0,
			"avg_latency_ms": 0.0,
			// P1-5：保留 deleted 标记（默认过滤后通常为 false；include_deleted=true 时区分展示）
			"deleted": isDeletedService(svc),
		}
		if mm, ok := metrics[svc]; ok {
			for k, v := range mm {
				item[k] = v
			}
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
	// Extract service name from URL path: strip "/api/v1/services/" prefix
	name := strings.TrimPrefix(r.URL.Path, "/api/v1/services/")
	name = strings.TrimRight(name, "/")
	if name == "" {
		respondError(w, http.StatusBadRequest, "service name required")
		return
	}

	// The service-list drawer renders the topology detail contract (health,
	// relations, traces, and trend). Redirect rather than maintaining a second,
	// partial implementation that can drift from the canonical endpoint.
	query := r.URL.Query()
	minutes, err := strconv.Atoi(query.Get("minutes"))
	if err != nil || minutes <= 0 {
		// `/services/{name}` historically used a 24-hour data window. Keep that
		// contract for callers that do not supply the detail-window parameter.
		query.Set("minutes", "1440")
	}
	target := "/api/v1/topology/node/" + url.PathEscape(name) + "?" + query.Encode()
	http.Redirect(w, r, target, http.StatusTemporaryRedirect)
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
	hoursClause := ""
	if rawHours := r.URL.Query().Get("hours"); rawHours != "" {
		if hours, err := strconv.Atoi(rawHours); err == nil && hours >= 1 {
			hoursClause = fmt.Sprintf(" AND start_time >= now() - INTERVAL %d HOUR", hours)
		}
	}

	serviceFilter := r.URL.Query().Get("service")
	serviceClause := ""
	if serviceFilter != "" {
		serviceClause = fmt.Sprintf("AND service_name=%s", chQuote(serviceFilter))
	}

	// 2.7 搜索框：支持按 trace_id / operation / http_url 文本搜索。
	// P3-6 修复：keyword 与 search 等价，keyword 优先（向后兼容 search 参数）。
	searchClause := ""
	searchKeyword := r.URL.Query().Get("keyword")
	if searchKeyword == "" {
		searchKeyword = r.URL.Query().Get("search")
	}
	if searchKeyword != "" {
		searchClause = fmt.Sprintf(" AND (trace_id LIKE %s OR operation_name LIKE %s OR http_url LIKE %s)", chLike(searchKeyword), chLike(searchKeyword), chLike(searchKeyword))
	}

	sql := fmt.Sprintf(
		"SELECT trace_id, min(start_time) as start, max(start_time) as end, count() as spans, count(DISTINCT service_name) as services, max(duration_ns)/1000000 as max_ms FROM observability.trace_spans WHERE tenant_id=%s%s%s%s%s %s GROUP BY trace_id ORDER BY start DESC LIMIT %d OFFSET %d",
		chQuote(tid), clusterClause, scopeServicesClause(r), searchClause, serviceClause, hoursClause, limit, offset,
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

	// P1-6 修复：count()/count(DISTINCT) 经 CH JSON 输出可能是字符串或数字，
	// 统一转换为 int，避免前端收到字符串类型（"2"）。
	for _, row := range rows {
		if v, ok := toInt64(row["spans"]); ok {
			row["spans"] = int(v)
		}
		if v, ok := toInt64(row["services"]); ok {
			row["services"] = int(v)
		}
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
// service 参数可选：有则按服务过滤，无则返回全局聚合。
// P2-6 修复：当请求带 `query`（PromQL 表达式）参数且无 service 参数时，
// 代理到 VictoriaMetrics /api/v1/query（instant query），与 /metrics/query_range 语义对齐；
// service 参数存在时保持现有 CH RED 聚合行为不变（向后兼容）。
// 安全（文档化已知限制）：PromQL 透传路径无租户/cluster 隔离（见 proxyVMInstantQuery），
// 已由 AuthMiddleware 强制 JWT 鉴权，需配合网络层隔离控制可达性。
func (h *Handler) QueryMetrics(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query().Get("query")
	service := r.URL.Query().Get("service")

	if query != "" && service == "" {
		h.proxyVMInstantQuery(w, r, query)
		return
	}

	tid := extractTenantID(r)
	clusterClause := extractClusterClause(r)

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
			"FROM observability.trace_spans WHERE tenant_id=%s%s%s%s AND date >= today()-1 "+
			"GROUP BY t ORDER BY t",
		chQuote(tid), clusterClause, scopeServicesClause(r), serviceClause,
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
	// H5 修复（R4）：DashboardStats 聚合 7+ 个 CH 查询，前端每 60s 轮询。
	// 加 30s TTL 内存缓存（key 含 tenant+path+query，即 cluster_id 维度），
	// 复用 cache.go 的 appCache（mutex 保护 + 过期清理），降低 CH 压力。
	ck := cacheKey(r)
	if cached, ok := appCache.Get(ck); ok {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Cache", "HIT")
		w.Write([]byte(cached))
		return
	}
	tid := extractTenantID(r)
	clusterClause := extractClusterClause(r)
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()

	sql := fmt.Sprintf(
		"SELECT service_name, count() as calls, countIf(is_error=1) as errors, sum(duration_ns) as lat_sum FROM observability.trace_spans WHERE tenant_id=%s%s%s AND service_name != '' AND date >= today()-1 GROUP BY service_name ORDER BY calls DESC LIMIT 50",
		chQuote(tid), clusterClause, scopeServicesClause(r),
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
		// P1-5 服务口径统一：与 ListServices 同款过滤，剔除 "(deleted)" 残留服务，
		// 保证 stats.services 与 /services 活跃服务数一致。
		if isDeletedService(svc) {
			continue
		}
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

	// 修复(P1-4)：统计口径统一。
	// services 数 = 仅 trace_spans 中出现的服务（与 ListServices 同口径，真实服务数）；
	// topology_services = 含 service_topology 目录中无 trace 服务的总数（前端展示用）。
	// P1-5：topology_services 亦剔除 "(deleted)" 残留服务（与 GlobalTopology 剔除 deleted 节点一致）。
	topologySQL := fmt.Sprintf(
		"SELECT DISTINCT source_service AS s FROM observability.service_topology WHERE tenant_id=%s%s%s AND s != '' UNION DISTINCT SELECT DISTINCT target_service AS s FROM observability.service_topology WHERE tenant_id=%s%s%s AND s != ''",
		chQuote(tid), clusterClause, scopeEdgeClause(r), chQuote(tid), clusterClause, scopeEdgeClause(r),
	)
	topologyServices := 0
	if tb, err := h.queryClickHouse(ctx, topologySQL); err == nil {
		if tr, perr := parseRows(tb); perr == nil && len(tr) > 0 {
			svcSet := map[string]bool{}
			for _, it := range items {
				svcSet[it.Service] = true
			}
			for _, row := range tr {
				if s, _ := row["s"].(string); s != "" && !isDeletedService(s) {
					svcSet[s] = true
				}
			}
			topologyServices = len(svcSet)
		}
	}
	if topologyServices > 0 {
		stats.TopologyServices = topologyServices
	}

	// 拓扑边数（与 GlobalTopology 同口径：service_topology 近 1440 分钟、
	// source!=target 去重后的边数，自环不计入）。
	edgeCount := int64(0)
	edgeSQL := fmt.Sprintf(
		"SELECT count() AS cnt FROM (SELECT source_service, target_service FROM observability.service_topology WHERE tenant_id=%s%s%s AND date >= today()-1 AND time_bucket >= now() - INTERVAL 1440 MINUTE AND source_service != '' AND target_service != '' AND source_service != target_service GROUP BY source_service, target_service)",
		chQuote(tid), clusterClause, scopeEdgeClause(r),
	)
	if eb, err := h.queryClickHouse(ctx, edgeSQL); err == nil {
		if er, perr := parseRows(eb); perr == nil && len(er) > 0 {
			if n, ok := toInt64(er[0]["cnt"]); ok {
				edgeCount = n
			}
		}
	}
	if edgeCount > 0 {
		stats.Edges = edgeCount
	} else if extractClusterClause(r) == "" {
		// 安全(P1-9)：MySQL topology_relations 无 cluster/tenant/scope 区分，
		// 仅在未按集群过滤（cluster_id 为空/all 的全集群视图）时回退，避免返回他集群合成边。
		if mysqlEdges := loadTopologyEdgesFromMySQL(); len(mysqlEdges) > 0 {
			stats.Edges = int64(len(mysqlEdges))
		}
	}

	// P95 延迟（给聚合列加别名，避免 ClickHouse 将 / 规范化为 divide() 导致 key 匹配失败）
	p95SQL := fmt.Sprintf("SELECT round(quantile(0.95)(duration_ns)/1000000, 2) AS p95_ms FROM observability.trace_spans WHERE tenant_id=%s%s%s AND date >= today()-1", chQuote(tid), clusterClause, scopeServicesClause(r))
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
			"FROM observability.trace_spans WHERE tenant_id=%s%s%s AND date >= today()-1 "+
			"GROUP BY t ORDER BY t LIMIT 24", chQuote(tid), clusterClause, scopeServicesClause(r))
	if tb, err := h.queryClickHouse(ctx, trendSQL); err == nil {
		if tr, perr := parseRows(tb); perr == nil {
			for _, row := range tr {
				tv, _ := row["t"].(string)
				calls, _ := toInt64(row["calls"])
				errs, _ := toInt64(row["errors"])
				stats.Trend = append(stats.Trend, biz.TrendPoint{T: tv, Calls: calls, Errors: errs})
			}
			// P1-3：检测缺失小时窗口（采集中断），供前端展示缺口提示
			stats.DataGaps = detectTrendGaps(tr, 24)
		}
	}

	// TOP 错误服务分布
	teSQL := fmt.Sprintf(
		"SELECT service_name AS s, countIf(is_error=1) AS errors FROM observability.trace_spans "+
			"WHERE tenant_id=%s%s%s AND date >= today()-1 AND is_error=1 GROUP BY s ORDER BY errors DESC LIMIT 10", chQuote(tid), clusterClause, scopeServicesClause(r))
	if tb, err := h.queryClickHouse(ctx, teSQL); err == nil {
		if tr, perr := parseRows(tb); perr == nil {
			for _, row := range tr {
				svc, _ := row["s"].(string)
				errs, _ := toInt64(row["errors"])
				stats.TopErrors = append(stats.TopErrors, biz.ErrorItem{Service: svc, Errors: errs})
			}
		}
	}

	// 告警统计（读内存 alertEvents，P1: 与事件页一致按 rule_id 聚合，避免原始事件数与
	// 规则数口径不一致——首页 222 vs 事件页 3）
	alertEventsMu.RLock()
	alertAgg := make(map[string]map[string]int)
	seenRule := map[string]bool{}
	for _, ev := range alertEvents {
		if ev.Status != "firing" {
			continue
		}
		// 相同 rule_id 只统计一次（与 AlertEvents 聚合口径一致）
		if seenRule[ev.RuleID] {
			continue
		}
		seenRule[ev.RuleID] = true
		if alertAgg[ev.Service] == nil {
			alertAgg[ev.Service] = map[string]int{}
		}
		alertAgg[ev.Service][ev.Severity]++
	}
	alertEventsMu.RUnlock()
	stats.AlertStats = biz.AggregateAlerts(alertAgg)

	// H5 修复（R4）：写入 30s TTL 缓存后返回。
	data, _ := json.Marshal(stats)
	appCache.Set(ck, string(data), 30*time.Second)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Cache", "MISS")
	w.Write(data)
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
	// ns 过滤（契约）：namespace 参数省略 = 全部；指定时返回该 ns 节点 + 外部邻居节点
	namespaceFilter := r.URL.Query().Get("namespace")

	// 时间过滤：minutes 参数（前端"近 N 分钟"），默认 24 小时，避免清理/低流量期出现空拓扑
	minutes := 1440
	if m := r.URL.Query().Get("minutes"); m != "" {
		if v, err := strconv.Atoi(m); err == nil && v > 0 {
			minutes = v
		}
	}
	timeCond := fmt.Sprintf(" AND start_time >= now() - INTERVAL %d MINUTE", minutes)
	// 数据(P1-4/P1-5)：时间窗口查询补 date 分区谓词，避免全分区扫描
	dateCond := " AND " + chDateWindow(minutes)
	aliasDateCond := fmt.Sprintf(" AND s1.%s AND s2.%s", chDateWindow(minutes), chDateWindow(minutes))
	edgeScope := scopeEdgeClause(r) // 安全(P1-2)：scope.services 非空时边两端都要在授权范围

	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()

	// 边聚合：优先 service_topology（真实调用边），无数据时回退到 trace_spans 按服务对聚合
	// service_topology 用 time_bucket 列做时间过滤
	edgeCond := fmt.Sprintf(" AND time_bucket >= now() - INTERVAL %d MINUTE", minutes)
	edgeSQL := fmt.Sprintf(
		"SELECT source_service, target_service, sum(call_count) AS calls, sum(error_count) AS errs, avg(avg_duration_ns) AS avg_ns "+
			"FROM observability.service_topology WHERE tenant_id=%s%s%s%s%s "+
			"GROUP BY source_service, target_service ORDER BY calls DESC LIMIT 200",
		chQuote(tid), clusterClause, edgeCond, dateCond, edgeScope,
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
		// 第一级：parent_span_id self-join（最准确，依赖完整调用链）
		fallbackSQL := fmt.Sprintf(
			"SELECT s1.service_name AS source_service, s2.service_name AS target_service, count() AS calls, 0 AS errs, avg(s2.duration_ns) AS avg_ns "+
				"FROM observability.trace_spans AS s1 "+
				"JOIN observability.trace_spans AS s2 ON s1.trace_id = s2.trace_id AND s1.span_id = s2.parent_span_id "+
				"WHERE s1.tenant_id=%s%s%s%s%s AND s1.start_time >= now() - INTERVAL %d MINUTE "+
				"GROUP BY s1.service_name, s2.service_name ORDER BY calls DESC LIMIT 200",
			chQuote(tid), clusterClause, scopeINClause(r, "s1."), scopeINClause(r, "s2."), aliasDateCond, minutes,
		)
		if fb, err := h.queryClickHouse(ctx, fallbackSQL); err == nil {
			if fr, perr := parseRows(fb); perr == nil && len(fr) > 0 {
				edgeRows = fr
			}
		}
	}
	// P0-2b: 若仍无边（数据无 parent_span_id 关联），改用 trace 内服务时序的相邻调用统计。
	// 不依赖父子链：按 trace 分组后，统计同一 trace 内相邻出现(按 start_time 排序)的不同服务对，
	// 能反映真实的服务间调用关系与调用量。
	// 注：用 lagInFrame 窗口函数（neighbor 在 ClickHouse 24.8 已弃用会报错）。
	if len(edgeRows) == 0 {
		seqSQL := fmt.Sprintf(
			"SELECT source_service, target_service, count() AS calls, 0 AS errs, avg(target_dur_ns) AS avg_ns FROM ( "+
				"  SELECT service_name AS target_service, "+
				"         lagInFrame(service_name, 1, '') OVER (ORDER BY trace_id, rn) AS source_service, "+
				"         duration_ns AS target_dur_ns "+
				"  FROM ( "+
				"    SELECT trace_id, service_name, duration_ns, "+
				"           row_number() OVER (PARTITION BY trace_id ORDER BY start_time) AS rn "+
				"    FROM observability.trace_spans "+
				"    WHERE tenant_id=%s%s%s%s AND start_time >= now() - INTERVAL %d MINUTE "+
				"  ) "+
				") WHERE source_service != '' AND source_service != target_service "+
				"GROUP BY source_service, target_service ORDER BY calls DESC LIMIT 200",
			chQuote(tid), clusterClause, scopeServicesClause(r), dateCond, minutes,
		)
		if fb, err := h.queryClickHouse(ctx, seqSQL); err == nil {
			if fr, perr := parseRows(fb); perr == nil && len(fr) > 0 {
				edgeRows = fr
			}
		}
	}
	// P0-2: 若 ClickHouse 仍无边（service_topology 为空且 trace 无 parent_span_id），
	// 回退到 MySQL topology_relations（由 SyncTopologyCatalog 按 trace 内服务时序生成的真实调用边）。
	// 安全(P1-9)：MySQL 边无 cluster/tenant/scope 区分，仅在未按集群过滤（全集群视图）时回退，
	// 避免按集群过滤后返回他集群合成边。
	if len(edgeRows) == 0 && extractClusterClause(r) == "" {
		if mysqlEdges := loadTopologyEdgesFromMySQL(); len(mysqlEdges) > 0 {
			edgeRows = mysqlEdges
		}
	}

	// 节点聚合（真实 trace）：按 service_name 聚合调用量、错误、平均延迟
	nodeSQL := fmt.Sprintf(
		"SELECT service_name AS service, count() AS calls, countIf(is_error=1) AS errs, avg(duration_ns) AS avg_ns "+
			"FROM observability.trace_spans WHERE tenant_id=%s%s%s%s%s GROUP BY service_name ORDER BY calls DESC LIMIT 200",
		chQuote(tid), clusterClause, timeCond, dateCond, scopeServicesClause(r),
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

	// ns 聚合（契约2/3）：窗口内 trace_spans 按 service 取调用量最大的 k8s_namespace，
	// 生成 服务→namespace 映射。k8s_namespace 为可选列，查询/解析失败时降级为空映射
	// （拓扑主视图仍可渲染，仅 namespaces 列表与节点 ns 标注为空），与其他可选富化查询一致。
	serviceNS := map[string]string{}
	nsSQL := fmt.Sprintf(
		"SELECT service_name AS service, k8s_namespace AS ns, count() AS calls "+
			"FROM observability.trace_spans WHERE tenant_id=%s%s%s%s%s "+
			"GROUP BY service_name, k8s_namespace ORDER BY calls DESC",
		chQuote(tid), clusterClause, timeCond, dateCond, scopeServicesClause(r),
	)
	if nsBody, qerr := h.queryClickHouse(ctx, nsSQL); qerr == nil {
		if nsRows, perr := parseRows(nsBody); perr == nil {
			best := map[string]int64{} // service → 该 ns 组合的最大调用量
			for _, nr := range nsRows {
				svc, _ := nr["service"].(string)
				ns, _ := nr["ns"].(string)
				calls, _ := toInt64(nr["calls"])
				if svc == "" {
					continue
				}
				if prev, ok := best[svc]; !ok || calls > prev {
					best[svc] = calls
					serviceNS[svc] = ns
				}
			}
		} else {
			log.Printf("GlobalTopology ns parse error: %v", perr)
		}
	} else {
		log.Printf("GlobalTopology ns query error: %v", qerr)
	}

	// 构建节点（去重）
	nodeMap := map[string]map[string]interface{}{}
	for _, nr := range nodeRows {
		name := fmt.Sprintf("%v", nr["service"])
		// 契约5：剔除含 "(deleted)" 的已删除服务节点
		if strings.Contains(name, "(deleted)") {
			continue
		}
		calls := toFloat(nr["calls"])
		errs := toFloat(nr["errs"])
		avgNs := toFloat(nr["avg_ns"])
		typ, rank := topologyNodeType(name)
		errRate := 0.0
		if calls > 0 {
			// A2 修复：error_rate 统一为 0-1 小数
			errRate = errs / calls
		}
		latencyMs := avgNs / 1e6 // ns → ms
		if latencyMs <= 0 {
			latencyMs = 1.0
		}
		health := "healthy"
		healthLevel := "normal"
		if errRate > 0.10 {
			health = "error"
			healthLevel = "severe"
		} else if errRate > 0.03 {
			health = "warning"
			healthLevel = "slight"
		}
		// 健康评分：基于错误率扣分，满分 100（errRate 为 0-1，等价于旧的 0-100 口径下 -errRate*2）
		healthScore := 100 - errRate*200
		if healthScore < 0 {
			healthScore = 0
		}
		// 吞吐率：调用次数/时间窗口（先按 calls 简单表示 rpm）
		throughput := calls
		nodeMap[name] = map[string]interface{}{
			"name":         name,
			"namespace":    serviceNS[name],
			"type":         typ,
			"rank":         rank,
			"calls":        int64(calls),
			"errs":         int64(errs),
			"latency_ms":   round1(latencyMs),
			"error_rate":   round3(errRate),
			"health":       health,
			"health_level": healthLevel,
			"health_score": round1(healthScore),
			"throughput":   int64(throughput),
		}
	}

	// 边：校验两端节点都存在；自环边（source_service==target_service）必须过滤，
	// 但保留计数供前端参考（P1-4 修复：此前自环被算作真实边，导致 42 vs 487 口径混乱）。
	// 探针噪声边（如 ingest→kube-dns 这类 target 为 kube-dns/system 服务的高错误边）
	// 保留不删——数据真实反映系统服务调用关系，仅在此注释说明。
	edges := []map[string]interface{}{}
	selfLoops := 0
	for _, er := range edgeRows {
		src := fmt.Sprintf("%v", er["source_service"])
		tgt := fmt.Sprintf("%v", er["target_service"])
		// 契约5：剔除两端任一端含 "(deleted)" 的边（含因此引入的占位节点）
		if strings.Contains(src, "(deleted)") || strings.Contains(tgt, "(deleted)") {
			continue
		}
		if src == "" || tgt == "" || src == tgt {
			selfLoops++
			continue
		}
		calls := toFloat(er["calls"])
		errs := toFloat(er["errs"])
		avgNs := toFloat(er["avg_ns"])
		if _, ok := nodeMap[src]; !ok {
			nodeMap[src] = buildSyntheticNode(src, serviceNS[src])
		}
		if _, ok := nodeMap[tgt]; !ok {
			nodeMap[tgt] = buildSyntheticNode(tgt, serviceNS[tgt])
		}
		errRate := 0.0
		if calls > 0 {
			// A2 修复：error_rate 统一为 0-1 小数
			errRate = errs / calls
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
			"error_rate":     round3(errRate),
			"latency_level":  latencyLevel,
		})
	}

	// ns 过滤（契约4）：传 namespace 时返回该 ns 全部节点（正常渲染）+ 与之有调用关系的
	// 其他 ns 节点（标记 external:true 并带真实 namespace）；跨 ns 边保留。未传 = 全部节点。
	nodes := make([]map[string]interface{}, 0, len(nodeMap))
	// K8s 兜底（层2）：ns 为空的服务节点用 K8s pod 映射补 namespace（修复 deepflow 同步/
	// 存量 span 无 k8s_namespace 时无法按真实 ns 过滤的问题）。只补空 ns，不覆盖已有 span ns。
	// 兜底结果写回 serviceNS，使 namespaces 列表与 ns 过滤同时生效。
	if h.podNS != nil {
		for name, n := range nodeMap {
			if ns, _ := n["namespace"].(string); ns != "" {
				continue
			}
			if mapped := h.podNS.resolve(name); mapped != "" {
				n["namespace"] = mapped
				serviceNS[name] = mapped
			}
		}
	}
	if namespaceFilter != "" {
		inNS := map[string]bool{}
		for name, n := range nodeMap {
			if ns, _ := n["namespace"].(string); ns == namespaceFilter {
				inNS[name] = true
			}
		}
		keep := map[string]bool{}
		for name := range inNS {
			keep[name] = true
		}
		// 扩展：与选中 ns 节点有调用边（任一方向）的外部邻居节点
		for _, e := range edges {
			src, _ := e["source_service"].(string)
			tgt, _ := e["target_service"].(string)
			if inNS[src] {
				keep[tgt] = true
			}
			if inNS[tgt] {
				keep[src] = true
			}
		}
		keptEdges := make([]map[string]interface{}, 0, len(edges))
		for _, e := range edges {
			src, _ := e["source_service"].(string)
			tgt, _ := e["target_service"].(string)
			if keep[src] && keep[tgt] {
				keptEdges = append(keptEdges, e)
			}
		}
		edges = keptEdges
		for name, n := range nodeMap {
			if !keep[name] {
				continue
			}
			// 契约4：namespace 参数存在时，节点 namespace != 选中 ns 的标记 external:true
			if ns, _ := n["namespace"].(string); ns != namespaceFilter {
				n["external"] = true
			}
			nodes = append(nodes, n)
		}
	} else {
		for _, n := range nodeMap {
			nodes = append(nodes, n)
		}
	}

	// namespaces 列表（契约2）：窗口内全部非 deleted 服务映射出的非空 ns，去重排序。
	nsSet := map[string]bool{}
	for svc, ns := range serviceNS {
		if ns == "" || strings.Contains(svc, "(deleted)") {
			continue
		}
		nsSet[ns] = true
	}
	namespaces := make([]string, 0, len(nsSet))
	for ns := range nsSet {
		namespaces = append(namespaces, ns)
	}
	sort.Strings(namespaces)

	respondJSON(w, http.StatusOK, map[string]interface{}{
		"nodes":           nodes,
		"edges":           edges,
		"node_count":      len(nodes),
		"edge_count":      len(edges),
		"self_loop_count": selfLoops,
		"namespaces":      namespaces,
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

// buildSyntheticNode 为拓扑边缘未聚合到的节点生成占位（类型推断 + 健康）。
// namespace 取自窗口内 trace_spans 的 ns 聚合（无记录为空串）。
func buildSyntheticNode(name, namespace string) map[string]interface{} {
	typ, rank := topologyNodeType(name)
	return map[string]interface{}{
		"name":         name,
		"namespace":    namespace,
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
		// A2 修复：error_rate 统一为 0-1 小数
		errRate = errs / calls
	}
	// 健康评分：基于错误率扣分，满分 100（errRate 为 0-1，等价于旧的 0-100 口径下 -errRate*2）
	healthScore := 100 - errRate*200
	if healthScore < 0 {
		healthScore = 0
	}
	apdex := 1.0 - errRate
	if apdex < 0 {
		apdex = 0
	}

	respondJSON(w, http.StatusOK, map[string]interface{}{
		"name":         name,
		"health_score": round1(healthScore),
		"apdex":        round2(apdex),
		"latency_ms":   round1(avgMs),
		"error_rate":   round3(errRate),
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

// round3 保留 3 位小数（A2：0-1 口径 error_rate 用，保留 0.1% 粒度）
func round3(v float64) float64 {
	return float64(int(v*1000+0.5)) / 1000
}

// QueryLogs handles GET /api/v1/logs/query?service={name}&query={text}&minutes=15
// source 参数：clickhouse（默认）或 victorialogs。切到 victorialogs 时路由到 VictoriaLogs 并归一化字段，
// 避免 UI 显示 VictoriaLogs 标签但实际返回 ClickHouse 数据（P0-1）。
func (h *Handler) QueryLogs(w http.ResponseWriter, r *http.Request) {
	tid := extractTenantID(r)
	service := r.URL.Query().Get("service")
	queryText := r.URL.Query().Get("query")
	// P2-5 修复：keyword 作为 query 的别名（前端兼容），
	// 两者都传时取 query 优先，keyword 非空才作过滤条件。
	if queryText == "" {
		queryText = r.URL.Query().Get("keyword")
	}
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
	// 修复(P2-3)：exclude_health=1 时过滤健康检查/探针噪音日志（/health、/ready、/v1/query）
	if r.URL.Query().Get("exclude_health") == "true" || r.URL.Query().Get("exclude_health") == "1" {
		conditions = append(conditions,
			"(body NOT LIKE '%/health%' AND body NOT LIKE '%/ready%' AND body NOT LIKE '%/v1/query%' AND body NOT LIKE '%metrics%')")
	}

	// 安全(P1-2)：scope.services 非空时追加服务范围过滤
	if sc := scopeServicesClause(r); sc != "" {
		conditions = append(conditions, strings.TrimPrefix(sc, " AND "))
	}

	// Time filter: last N minutes（用 timestamp 精确过滤，而非天粒度 date）
	conditions = append(conditions, fmt.Sprintf("timestamp >= now() - INTERVAL %d MINUTE", minutes))
	// 数据(P1-4)：补 date 分区谓词，避免全分区扫描（与分钟窗口对齐）
	conditions = append(conditions, chDateWindow(minutes))

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
	// 安全(P1-2)：scope.services 非空时追加服务范围过滤
	if sc := scopeServicesClause(r); sc != "" {
		conditions = append(conditions, strings.TrimPrefix(sc, " AND "))
	}
	conditions = append(conditions, fmt.Sprintf("timestamp >= now() - INTERVAL %d MINUTE", minutes))
	// 数据(P1-4)：补 date 分区谓词，避免全分区扫描（与分钟窗口对齐）
	conditions = append(conditions, chDateWindow(minutes))
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

// maxVLLimit 限制 VictoriaLogs 查询 limit 参数上限（G4/S15 修复），防止无界返回。
const maxVLLimit = 10000

// maxVLResponse 限制 VictoriaLogs 代理响应体上限（20MB）。
const maxVLResponse = 20 << 20

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
	// G4 修复（S15）：limit 参数上限 10000，超限拒绝（防止无界返回拖垮内存/带宽）。
	if n, err := strconv.Atoi(limit); err != nil || n < 1 {
		respondError(w, http.StatusBadRequest, "invalid limit")
		return
	} else if n > maxVLLimit {
		respondError(w, http.StatusBadRequest, fmt.Sprintf("limit too large (max %d)", maxVLLimit))
		return
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

	// G4 修复（S15）：响应体大小上限（20MB），超限返回 502。
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxVLResponse+1))
	if err != nil {
		respondError(w, http.StatusBadGateway, "VictoriaLogs read error: "+err.Error())
		return
	}
	if len(body) > maxVLResponse {
		respondError(w, http.StatusBadGateway, "VictoriaLogs response too large (>20MB)")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	w.Write(body)
}

// ProxyVictoriaLogsInsert handles POST /api/v1/logs/victorialogs
func (h *Handler) ProxyVictoriaLogsInsert(w http.ResponseWriter, r *http.Request) {
	// G4 修复（S15）：插入日志是写操作，仅 admin/approver 可调用，普通 user 403。
	if !hasPrivilegedRole(r) {
		respondError(w, http.StatusForbidden, "forbidden: admin or approver role required")
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxVLResponse+1))
	if err != nil {
		respondError(w, http.StatusBadRequest, "failed to read body")
		return
	}
	if len(body) > maxVLResponse {
		respondError(w, http.StatusRequestEntityTooLarge, "request body too large (>20MB)")
		return
	}
	// G4 修复（S15）：schema 校验——payload 必须是 JSON 数组，且每项为对象并含 _msg 或 msg 字段。
	var entries []map[string]interface{}
	if err := json.Unmarshal(body, &entries); err != nil {
		respondError(w, http.StatusBadRequest, "payload must be a JSON array of objects")
		return
	}
	if len(entries) == 0 {
		respondError(w, http.StatusBadRequest, "payload must be a non-empty JSON array")
		return
	}
	for i, e := range entries {
		if e == nil {
			respondError(w, http.StatusBadRequest, fmt.Sprintf("entry %d: must be an object", i))
			return
		}
		if _, ok := e["_msg"]; !ok {
			if _, ok2 := e["msg"]; !ok2 {
				respondError(w, http.StatusBadRequest, fmt.Sprintf("entry %d: missing _msg or msg field", i))
				return
			}
		}
	}
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

// detectTrendGaps 基于"近 hours 小时应有点"检测 trend 缺失小时，返回缺口描述列表。
// rows 的 t 字段格式 "2006-01-02 15:04:05"（toString(toStartOfHour(...))）。
// 返回形如 "08-12 15:00 ~ 08-12 23:00" 的连续缺失区间（P1-3）。
func detectTrendGaps(rows []map[string]interface{}, hours int) []string {
	now := time.Now().Truncate(time.Hour)
	got := map[int64]bool{}
	for _, r := range rows {
		if ts, ok := r["t"].(string); ok {
			if t, err := time.ParseInLocation("2006-01-02 15:04:05", ts, time.Local); err == nil {
				got[t.Unix()] = true
			}
		}
	}
	var gaps []string
	var gapStart, gapEnd *time.Time
	flush := func() {
		if gapStart != nil {
			gaps = append(gaps, fmt.Sprintf("%s ~ %s",
				gapStart.Format("01-02 15:00"), gapEnd.Format("01-02 15:00")))
			gapStart, gapEnd = nil, nil
		}
	}
	for i := hours - 1; i >= 0; i-- {
		h := now.Add(-time.Duration(i) * time.Hour)
		if !got[h.Unix()] {
			if gapStart == nil {
				gapStart = &h
			}
			gapEnd = &h
		} else {
			flush()
		}
	}
	flush()
	return gaps
}
