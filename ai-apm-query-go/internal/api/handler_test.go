package api

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/observability-platform/ai-apm-query-go/internal/query"
	"github.com/observability-platform/ai-apm-query-go/internal/store"
)

// TestListServicesReturnsCHServicesWithMetadata 验证重写后的 ListServices：
//   - 从 ClickHouse trace_spans 动态发现服务列表（source=trace）
//   - LEFT JOIN MySQL service_metadata 富化 owner/team/tier/description
//   - 响应结构为 {"services":[...], "total": N}
//
// ClickHouse 通过 httptest.Server mock（无需真实 CH）。
// MySQL 不可达时（store.GetDB() 返回 nil）跳过富化断言，但仍验证 CH 发现路径。
func TestListServicesReturnsCHServicesWithMetadata(t *testing.T) {
	// 1. Mock ClickHouse：区分两种查询——服务发现（DISTINCT service_name）与指标聚合（count()）。
	//    指标聚合返回 service/calls/errs/avg_ms 结构，供 ListServices 补全列表指标。
	chSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		q := r.URL.Query().Get("query")
		if !strings.Contains(q, "FROM observability.trace_spans") {
			t.Errorf("unexpected CH query, want trace_spans, got: %s", q)
		}
		if strings.Contains(q, "count()") && strings.Contains(q, "GROUP BY service_name") {
			// 指标聚合查询
			_, _ = w.Write([]byte(
				`{"service":"frontend","calls":100,"errs":2,"avg_ms":35.5}` + "\n" +
					`{"service":"backend","calls":50,"errs":0,"avg_ms":20.1}` + "\n"))
			return
		}
		if !strings.Contains(q, "DISTINCT service_name") {
			t.Errorf("expected DISTINCT service_name in query, got: %s", q)
		}
		// JSONEachRow：每行一个 JSON 对象
		_, _ = w.Write([]byte(`{"service_name":"frontend"}` + "\n" + `{"service_name":"backend"}` + "\n"))
	}))
	defer chSrv.Close()

	h := &Handler{client: &http.Client{}}
	host, port := splitHostPort(chSrv.URL)
	h.repo = *query.NewClickHouseRepo(fmt.Sprintf("http://%s:%d", host, port), &http.Client{Timeout: 5 * time.Second})
	h.resourceRepo = query.NewResourceRepository(&h.repo)

	// 2. MySQL 富化数据：可达时插入测试行，不可达则跳过富化断言
	db := store.GetDB()
	hasMySQL := db != nil
	if hasMySQL {
		// 幂等插入：frontend 有富化数据，backend 无（验证 LEFT JOIN 缺失走默认值）
		_, _ = db.Exec("INSERT IGNORE INTO service_metadata (service_name, owner, tier) VALUES ('frontend', 'team-a', 'critical')")
	}

	req := httptest.NewRequest("GET", "/api/v1/services", nil)
	w := httptest.NewRecorder()
	h.ListServices(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d, body=%s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v, body=%s", err, w.Body.String())
	}

	// 验证响应结构：services 数组 + total
	services, ok := resp["services"].([]interface{})
	if !ok {
		t.Fatalf("expected services []interface{}, got %T: %v", resp["services"], resp["services"])
	}
	if len(services) != 2 {
		t.Fatalf("expected 2 services, got %d: %v", len(services), services)
	}

	total, ok := resp["total"].(float64)
	if !ok {
		t.Fatalf("expected total number, got %T: %v", resp["total"], resp["total"])
	}
	if int(total) != 2 {
		t.Errorf("expected total=2, got %v", total)
	}

	// 验证 source=trace + 默认 tier=standard
	for _, s := range services {
		m, ok := s.(map[string]interface{})
		if !ok {
			t.Fatalf("expected service map, got %T", s)
		}
		if m["source"] != "trace" {
			t.Errorf("expected source=trace, got %v", m["source"])
		}
		name, _ := m["service_name"].(string)
		if name == "" {
			t.Errorf("expected non-empty service_name, got %v", m["service_name"])
		}
	}

	// 验证新增的指标聚合字段（calls/errors/error_rate/avg_latency_ms），修复服务列表全 0 问题
	frontendItem := services[0].(map[string]interface{})
	if calls, ok := frontendItem["calls"]; !ok || calls == nil || calls == float64(0) {
		t.Errorf("expected frontend calls>0 from metric aggregation, got %v", frontendItem["calls"])
	}
	if _, ok := frontendItem["avg_latency_ms"]; !ok {
		t.Errorf("expected frontend avg_latency_ms field present, got keys: %v", frontendItem)
	}

	// 验证 frontend 排在最前（CH 返回按 service_name ORDER BY，frontend < backend）
	first := services[0].(map[string]interface{})
	if first["service_name"] != "frontend" {
		t.Errorf("expected first service=frontend (ORDER BY name), got %v", first["service_name"])
	}

	// MySQL 可达时验证富化字段（LEFT JOIN）
	if hasMySQL {
		fItem := first
		if fItem["owner"] != "team-a" {
			t.Errorf("expected frontend owner=team-a (enriched), got %v", fItem["owner"])
		}
		if fItem["tier"] != "critical" {
			t.Errorf("expected frontend tier=critical (enriched), got %v", fItem["tier"])
		}

		// backend 无富化数据 → 走默认值（tier=standard, owner="")
		backendItem := services[1].(map[string]interface{})
		if backendItem["service_name"] != "backend" {
			t.Errorf("expected second service=backend, got %v", backendItem["service_name"])
		}
		if backendItem["tier"] != "standard" {
			t.Errorf("expected backend tier=standard (default, no enrichment), got %v", backendItem["tier"])
		}
		if backendItem["owner"] != "" {
			t.Errorf("expected backend owner='' (default, no enrichment), got %v", backendItem["owner"])
		}
	} else {
		t.Logf("MySQL not available (store.GetDB()=nil), skipped enrichment assertions")
	}
}

// TestListServicesCHError 验证 ClickHouse 查询失败时返回统一错误码（P6.2c：经 repository 映射为 503 unavailable）。
func TestListServicesCHError(t *testing.T) {
	// Mock CH 返回 500 错误
	chSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("clickhouse internal error"))
	}))
	defer chSrv.Close()

	h := &Handler{client: &http.Client{}}
	host, port := splitHostPort(chSrv.URL)
	h.repo = *query.NewClickHouseRepo(fmt.Sprintf("http://%s:%d", host, port), &http.Client{Timeout: 5 * time.Second})
	h.resourceRepo = query.NewResourceRepository(&h.repo)

	req := httptest.NewRequest("GET", "/api/v1/services", nil)
	w := httptest.NewRecorder()
	h.ListServices(w, req)

	// CH unavailable → 503（repository 统一错误语义，替代旧的裸 500）
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 on CH unavailable, got %d, body=%s", w.Code, w.Body.String())
	}
}

// newTestHandler 构造一个指向 mock ClickHouse 的 Handler，供 QueryMetrics 等
// handler 的单元测试使用。mock CH 始终返回空 JSONEachRow 数据（即 0 行），
// 使测试聚焦于参数校验逻辑而非真实查询结果。server 在测试结束时自动关闭。
func newTestHandler(t *testing.T) *Handler {
	t.Helper()
	chSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		// 空响应 = 0 行
		_, _ = w.Write([]byte(""))
	}))
	t.Cleanup(chSrv.Close)

	h := &Handler{client: &http.Client{}}
	host, port := splitHostPort(chSrv.URL)
	h.chHost = host
	h.chPort = port
	// P6.2：统一 CH repository 指向 mock server（与 NewHandler 一致），否则 h.repo 为零值。
	h.repo = *query.NewClickHouseRepo(fmt.Sprintf("http://%s:%d", host, port), &http.Client{Timeout: 5 * time.Second})
	h.metricsRepo = query.NewMetricsRepository(&h.repo)
	h.logRepo = query.NewLogRepository(&h.repo, nil, query.NewSourceRouter(query.ModeLegacy))
	h.traceRepo = query.NewTraceRepository(&h.repo)
	h.alertRepo = query.NewAlertRepository(&h.repo)
	h.topoRepo = query.NewTopologyRepository(&h.repo)
	h.resourceRepo = query.NewResourceRepository(&h.repo)
	return h
}

// TestServiceDetailRedirectsToCanonicalTopologyDetail ensures service-list clicks
// use the same complete detail contract as topology-node clicks. It fails if the
// legacy endpoint resumes returning only a trend series.
func TestServiceDetailRedirectsToCanonicalTopologyDetail(t *testing.T) {
	h := newTestHandler(t)
	req := httptest.NewRequest("GET", "/api/v1/services/orders?minutes=60", nil)
	w := httptest.NewRecorder()

	h.ServiceDetail(w, req)

	if w.Code != http.StatusTemporaryRedirect {
		t.Fatalf("expected service detail to redirect to the canonical topology detail endpoint, got %d: %s", w.Code, w.Body.String())
	}
	if got, want := w.Header().Get("Location"), "/api/v1/topology/node/orders?minutes=60"; got != want {
		t.Fatalf("redirect location=%q, want %q", got, want)
	}
}

// Service-list callers that do not yet provide a selected time window retain
// the former 24-hour detail window when routed to topology detail.
func TestServiceDetailDefaultsToLegacyDayWindow(t *testing.T) {
	h := newTestHandler(t)
	req := httptest.NewRequest("GET", "/api/v1/services/orders", nil)
	w := httptest.NewRecorder()

	h.ServiceDetail(w, req)

	if w.Code != http.StatusTemporaryRedirect {
		t.Fatalf("expected temporary redirect, got %d: %s", w.Code, w.Body.String())
	}
	if got, want := w.Header().Get("Location"), "/api/v1/topology/node/orders?minutes=1440"; got != want {
		t.Fatalf("redirect location=%q, want %q", got, want)
	}
}

// TestQueryMetricsWithoutServiceDoesNotError 验证 service 参数为空时不再返回
// 400 "service parameter required"，而是放行到查询阶段（200 空结果或 500 CH 错误）。
func TestQueryMetricsWithoutServiceDoesNotError(t *testing.T) {
	h := newTestHandler(t)

	req := httptest.NewRequest("GET", "/api/v1/metrics/query", nil)
	w := httptest.NewRecorder()
	h.QueryMetrics(w, req)

	if w.Code == 400 {
		t.Fatalf("expected non-400 for empty service, got 400: %s", w.Body.String())
	}
	// 应该是 200 或 500（如果 CH 查询失败），但不应该是 400 "service parameter required"
}

// TestQueryMetricsWithServiceStillWorks 验证带 service 参数时仍正常工作（非 400）。
func TestQueryMetricsWithServiceStillWorks(t *testing.T) {
	h := newTestHandler(t)

	req := httptest.NewRequest("GET", "/api/v1/metrics/query?service=frontend", nil)
	w := httptest.NewRecorder()
	h.QueryMetrics(w, req)

	if w.Code == 400 {
		t.Fatalf("expected non-400 for service=frontend, got 400: %s", w.Body.String())
	}
}

// TestListServicesEmptyResult 验证 CH 返回空数据时返回空列表（非 nil）。
func TestListServicesEmptyResult(t *testing.T) {
	chSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// 空响应
		_, _ = w.Write([]byte(""))
	}))
	defer chSrv.Close()

	h := &Handler{client: &http.Client{}}
	host, port := splitHostPort(chSrv.URL)
	h.repo = *query.NewClickHouseRepo(fmt.Sprintf("http://%s:%d", host, port), &http.Client{Timeout: 5 * time.Second})
	h.resourceRepo = query.NewResourceRepository(&h.repo)

	req := httptest.NewRequest("GET", "/api/v1/services", nil)
	w := httptest.NewRecorder()
	h.ListServices(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 on empty CH result, got %d", w.Code)
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	services, ok := resp["services"].([]interface{})
	if !ok {
		t.Fatalf("expected services array, got %T", resp["services"])
	}
	if len(services) != 0 {
		t.Errorf("expected empty services list, got %d items", len(services))
	}
	if resp["total"].(float64) != 0 {
		t.Errorf("expected total=0, got %v", resp["total"])
	}
}

// ===== loadServiceMetadata sqlmock 单测（MySQL 富化路径，不依赖真实 DB）=====

func TestLoadServiceMetadata_SQLConstructionAndScan(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("failed to create sqlmock: %v", err)
	}
	defer db.Close()

	// 期望：IN 子句有 2 个占位符，参数为 frontend/backend
	rows := sqlmock.NewRows([]string{"service_name", "owner", "team", "tier", "description"}).
		AddRow("frontend", "team-a", "sre", "critical", "web frontend")
	mock.ExpectQuery("SELECT service_name, owner, team, tier, description FROM service_metadata WHERE service_name IN").
		WithArgs("frontend", "backend").
		WillReturnRows(rows)

	meta := loadServiceMetadata([]string{"frontend", "backend"}, db)

	if len(meta) != 1 {
		t.Fatalf("expected 1 enriched service, got %d", len(meta))
	}
	frontend, ok := meta["frontend"]
	if !ok {
		t.Fatalf("expected frontend in meta")
	}
	if frontend.Owner != "team-a" || frontend.Team != "sre" ||
		frontend.Tier != "critical" || frontend.Description != "web frontend" {
		t.Errorf("frontend metadata mismatch: %+v", frontend)
	}
	// backend 无对应行 → 不在结果中（LEFT JOIN 语义）
	if _, exists := meta["backend"]; exists {
		t.Errorf("backend should not be present (no row)")
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet sqlmock expectations: %v", err)
	}
}

func TestLoadServiceMetadata_NilDBReturnsEmpty(t *testing.T) {
	meta := loadServiceMetadata([]string{"frontend"}, nil)
	if len(meta) != 0 {
		t.Fatalf("expected empty meta for nil db, got %d", len(meta))
	}
}

func TestLoadServiceMetadata_EmptyServicesReturnsEmpty(t *testing.T) {
	db, _, err := sqlmock.New()
	if err != nil {
		t.Fatalf("failed to create sqlmock: %v", err)
	}
	defer db.Close()
	meta := loadServiceMetadata([]string{}, db)
	if len(meta) != 0 {
		t.Fatalf("expected empty meta for empty services, got %d", len(meta))
	}
}

func TestLoadServiceMetadata_QueryErrorReturnsEmpty(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("failed to create sqlmock: %v", err)
	}
	defer db.Close()

	mock.ExpectQuery("SELECT service_name.*FROM service_metadata").
		WillReturnError(sql.ErrConnDone)

	meta := loadServiceMetadata([]string{"frontend"}, db)
	if len(meta) != 0 {
		t.Fatalf("expected empty meta on query error, got %d", len(meta))
	}
}

// ===== P0-1: VictoriaLogs 数据源归一化（source=victorialogs 不应返回 ClickHouse 数据）=====

func TestNormalizeVictoriaLogsRows(t *testing.T) {
	body := []byte(`{"_msg":"2026-08-12T00:49:23Z","_time":"2026-08-12T00:49:31Z","namespace":"deepflow","pod":"deepflow-clickhouse-0","service":"deepflow/deepflow-clickhouse-0"}
{"_msg":"hello world","_time":"2026-08-12T00:50:00Z","namespace":"observability","pod":"query-api-abc","service":"observability/query-api-abc"}
not-json-line`)
	rows := normalizeVictoriaLogsRows(body)

	if len(rows) != 2 {
		t.Fatalf("expected 2 normalized rows, got %d", len(rows))
	}
	// 第 1 行：service_name 去掉 namespace 前缀
	r0 := rows[0]
	if r0["service_name"] != "deepflow-clickhouse-0" {
		t.Errorf("expected service_name=deepflow-clickhouse-0 (namespace stripped), got %v", r0["service_name"])
	}
	if r0["timestamp"] != "2026-08-12T00:49:31Z" {
		t.Errorf("expected timestamp from _time, got %v", r0["timestamp"])
	}
	if r0["body"] != "2026-08-12T00:49:23Z" {
		t.Errorf("expected body from _msg, got %v", r0["body"])
	}
	if r0["source"] != "victorialogs" {
		t.Errorf("expected source=victorialogs, got %v", r0["source"])
	}
	if r0["severity"] != "info" {
		t.Errorf("expected severity=info (pod events have no level), got %v", r0["severity"])
	}
	// 第 2 行：body 取真实消息
	if rows[1]["body"] != "hello world" {
		t.Errorf("expected body=hello world, got %v", rows[1]["body"])
	}
}

func TestBuildVictoriaLogsSQL(t *testing.T) {
	// 仅时间过滤
	q1 := buildVictoriaLogsSQL("", "", 60)
	if !strings.Contains(q1, "_time:60m") {
		t.Errorf("expected _time:60m in query, got %q", q1)
	}
	// 服务过滤 + 关键词
	q2 := buildVictoriaLogsSQL("query-api", "error", 1440)
	if !strings.Contains(q2, `service:"query-api"*`) {
		t.Errorf("expected service fuzzy match in query, got %q", q2)
	}
	if !strings.Contains(q2, `"error"`) {
		t.Errorf("expected keyword in query, got %q", q2)
	}
}

// ===== P1-1: DashboardStats 边数在 service_topology 为空时回退到 MySQL topology_relations =====

func TestDashboardStatsEdgesFallsBackToMySQL(t *testing.T) {
	// Mock ClickHouse：service_topology count=0（无边数据）。P6.2c DashboardStats 经
	// topology repository（QueryJSON：GET + query 参数 + JSONEachRow）。
	chSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		q := r.URL.Query().Get("query")
		switch {
		case strings.Contains(q, "FROM observability.service_topology"):
			// 返回边数 cnt=0（EdgeCount 解析 cnt 键）
			_, _ = w.Write([]byte(`{"cnt":0}` + "\n"))
		case strings.Contains(q, "FROM observability.trace_spans"):
			// 服务调用 / P95 聚合：给一条空结果即可
			_, _ = w.Write([]byte(""))
		default:
			_, _ = w.Write([]byte(""))
		}
	}))
	defer chSrv.Close()

	h := &Handler{client: &http.Client{}}
	host, port := splitHostPort(chSrv.URL)
	h.repo = *query.NewClickHouseRepo(fmt.Sprintf("http://%s:%d", host, port), &http.Client{Timeout: 5 * time.Second})
	h.topoRepo = query.NewTopologyRepository(&h.repo)

	req := httptest.NewRequest("GET", "/api/v1/dashboard/stats", nil)
	rr := httptest.NewRecorder()
	h.DashboardStats(rr, req)

	// MySQL 不可达（store.GetDB()==nil）时 loadTopologyEdgesFromMySQL 返回 nil，
	// 因此 edges 应为 0（不 panic），且不因 CH 空返回导致错误。
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestListTracesHoursAddsStartTimePredicate(t *testing.T) {
	var gotSQL string
	chSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// P6.2b：统一 repository 用 POST body 传 SQL。
		if b, err := io.ReadAll(r.Body); err == nil {
			gotSQL = string(b)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(""))
	}))
	defer chSrv.Close()

	host, port := splitHostPort(chSrv.URL)
	h := &Handler{client: &http.Client{}}
	h.repo = *query.NewClickHouseRepo(fmt.Sprintf("http://%s:%d", host, port), &http.Client{Timeout: 5 * time.Second})
	h.traceRepo = query.NewTraceRepository(&h.repo)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/traces?hours=6", nil)
	rec := httptest.NewRecorder()

	h.ListTraces(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(gotSQL, "start_time >= now() - INTERVAL 6 HOUR") {
		t.Fatalf("expected six-hour start_time predicate, got query: %s", gotSQL)
	}
}
