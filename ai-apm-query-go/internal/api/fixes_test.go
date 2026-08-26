package api

import (
	"database/sql/driver"
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

// ===== P2-1: SLO 数值边界校验 =====

func TestValidateSLOInput(t *testing.T) {
	// 缺省值（未传 target/window_seconds → 保持默认行为）
	s := &store.SLOTarget{}
	if msg := validateSLOInput(s, map[string]interface{}{}); msg != "" {
		t.Fatalf("defaults should pass, got %q", msg)
	}
	if s.SLOType != "availability" || s.Target != 99.9 || s.WindowSeconds != 2592000 {
		t.Fatalf("defaults wrong: %+v", s)
	}
	// 合法边界值
	s = &store.SLOTarget{SLOType: "availability", Target: 100, WindowSeconds: 3600}
	if msg := validateSLOInput(s, map[string]interface{}{"target": float64(100), "window_seconds": float64(3600)}); msg != "" {
		t.Fatalf("lower bound should be valid, got %q", msg)
	}
	s = &store.SLOTarget{SLOType: "latency", Target: 0.5, WindowSeconds: 7776000}
	if msg := validateSLOInput(s, map[string]interface{}{"target": float64(0.5), "window_seconds": float64(7776000)}); msg != "" {
		t.Fatalf("upper bound should be valid, got %q", msg)
	}
	// window_seconds 越界（负数/超界）→ 拒绝
	for _, w := range []int{3599, 7776001, -1, -3600} {
		s = &store.SLOTarget{SLOType: "availability", Target: 99.9, WindowSeconds: w}
		if msg := validateSLOInput(s, map[string]interface{}{"window_seconds": float64(w)}); msg == "" {
			t.Errorf("window_seconds=%d should be rejected", w)
		}
	}
	// availability target ∈ (0,100]：越界拒绝
	for _, tg := range []float64{101, 0, -1} {
		s = &store.SLOTarget{SLOType: "availability", Target: tg}
		if msg := validateSLOInput(s, map[string]interface{}{"target": tg}); msg == "" {
			t.Errorf("availability target=%v should be rejected", tg)
		}
	}
	// latency target > 0：0/负数拒绝
	s = &store.SLOTarget{SLOType: "latency", Target: 0}
	if msg := validateSLOInput(s, map[string]interface{}{"target": float64(0)}); msg == "" {
		t.Error("latency target=0 should be rejected")
	}
	// 非法 slo_type → 拒绝
	s = &store.SLOTarget{SLOType: "weird"}
	if msg := validateSLOInput(s, map[string]interface{}{}); msg == "" {
		t.Error("invalid slo_type should be rejected")
	}
}

func TestCreateSLORejectsJWTAdminClaim(t *testing.T) {
	h := &Handler{client: &http.Client{}}
	adminToken := generateJWT("admin", "admin", "")
	cases := []string{
		`{"name":"x","service":"y","window_seconds":-5}`,             // 负数
		`{"name":"x","service":"y","window_seconds":3599}`,           // 低于下界
		`{"name":"x","service":"y","window_seconds":7776001}`,        // 超上界
		`{"name":"x","service":"y","target":101}`,                    // availability target > 100
		`{"name":"x","service":"y","target":0}`,                      // availability target <= 0
		`{"name":"x","service":"y","slo_type":"latency","target":0}`, // latency target <= 0
		`{"name":"x","service":"y","slo_type":"weird"}`,              // 非法 slo_type
	}
	for _, body := range cases {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/v1/slo", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+adminToken)
		h.SLORouter(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Errorf("body=%s: expected 403, got %d: %s", body, rec.Code, rec.Body.String())
		}
	}
}

// ===== P3-2: 用户角色白名单 =====

func TestUserCreateRejectsInvalidRole(t *testing.T) {
	h := &Handler{client: &http.Client{}}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/users", strings.NewReader(`{"username":"u1","password":"pw","role":"superadmin"}`))
	h.UserCreate(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid role, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if msg, _ := resp["error"].(string); msg != "role must be admin, approver, or user" {
		t.Fatalf("expected role whitelist error, got %q", msg)
	}
}

func TestUserRoleWhitelistIncludesApprover(t *testing.T) {
	for _, role := range []string{"admin", "approver", "user"} {
		if msg := validateUserRole(role); msg != "" {
			t.Fatalf("role %q should be accepted, got %q", role, msg)
		}
	}
	if msg := validateUserRole("superadmin"); msg == "" {
		t.Fatal("invalid role should be rejected")
	}
}

// ===== P3-3: PUT /users 类型错误不静默零值 =====

func TestUserUpdateRejectsInvalidJSON(t *testing.T) {
	h := &Handler{client: &http.Client{}}
	// status 是 int 字段，传入字符串触发类型错误 → 400（而非静默零值覆盖）
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/v1/users/1", strings.NewReader(`{"status": "notanumber"}`))
	h.UserUpdate(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for type-mismatch JSON, got %d: %s", rec.Code, rec.Body.String())
	}
}

// ===== P3-5: POST /me 返回 405 =====

func TestMeRejectsNonGET(t *testing.T) {
	h := &Handler{client: &http.Client{}}
	for _, m := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(m, "/api/v1/me", nil)
		h.Me(rec, req)
		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("method=%s: expected 405, got %d", m, rec.Code)
		}
	}
}

// ===== P3-1: DELETE 不存在的资源返回 404 =====

// TestDeleteReturns404ForMissingResource 验证各 DELETE 处理器对不存在资源返回 404。
// 用 sqlmock 注入 store DB（Get 无行 → nil），不需要真实 MySQL。
func TestDeleteReturns404ForMissingResource(t *testing.T) {
	userCols := []string{"id", "username", "password_hash", "display_name", "role", "email", "status", "scope", "is_approver", "created_at"}
	clusterCols := []string{"id", "name", "provider", "region", "version", "node_count", "status", "api_server", "kubeconfig", "created_at", "updated_at"}
	catalogCols := []string{"id", "service_name", "display_name", "description", "owner", "team", "tags", "status", "created_at", "updated_at"}
	deviceCols := []string{"id", "hostname", "ip", "os", "cpu_cores", "memory_mb", "status", "role", "location", "tags", "created_at", "updated_at"}
	nodeCols := []string{"id", "type", "name", "props_json", "created_at", "updated_at"}
	relCols := []string{"id", "src_id", "dst_id", "type", "props_json", "created_at"}
	sloCols := []string{"id", "name", "service", "slo_type", "target", "window_seconds", "enabled"}
	panelCols := []string{"id", "title", "query", "chart_type", "grid_x", "grid_y", "grid_w", "grid_h", "span", "sort", "enabled"}

	cases := []struct {
		name    string
		pattern string
		args    []driver.Value
		cols    []string
		handler func(h *Handler, w http.ResponseWriter, r *http.Request)
		path    string
	}{
		{"users", "SELECT id, username.*FROM users WHERE id", []driver.Value{int64(999)}, userCols, func(h *Handler, w http.ResponseWriter, r *http.Request) { h.UserDelete(w, r) }, "/api/v1/users/999"},
		{"clusters", "SELECT id, name, provider.*FROM clusters WHERE id", []driver.Value{int64(999)}, clusterCols, func(h *Handler, w http.ResponseWriter, r *http.Request) { h.clusterDelete(w, r, 999) }, "/api/v1/clusters/999"},
		{"catalog", "SELECT id, service_name, display_name.*FROM service_catalog WHERE id", []driver.Value{int64(999)}, catalogCols, func(h *Handler, w http.ResponseWriter, r *http.Request) { h.catalogDelete(w, r, 999) }, "/api/v1/catalog/services/999"},
		{"devices", "SELECT id, hostname.*FROM devices WHERE id", []driver.Value{int64(999)}, deviceCols, func(h *Handler, w http.ResponseWriter, r *http.Request) { h.deviceDelete(w, r, 999) }, "/api/v1/devices/999"},
		{"topology_nodes", "SELECT id, type, name.*FROM topology_nodes WHERE id", []driver.Value{int64(999)}, nodeCols, func(h *Handler, w http.ResponseWriter, r *http.Request) { h.topologyNodeDelete(w, r, 999) }, "/api/v1/topology/nodes/999"},
		{"topology_relations", "SELECT id, src_id, dst_id, type.*FROM topology_relations WHERE id", []driver.Value{int64(999)}, relCols, func(h *Handler, w http.ResponseWriter, r *http.Request) { h.topologyRelationDelete(w, r, 999) }, "/api/v1/topology/relations/999"},
		{"slo", "SELECT id, name, service, slo_type.*FROM slo_targets WHERE id", []driver.Value{"missing-slo"}, sloCols, func(h *Handler, w http.ResponseWriter, r *http.Request) { h.deleteSLO(w, r, "missing-slo") }, "/api/v1/slo/missing-slo"},
		{"dashboard_panels", "SELECT id, title.*FROM dashboard_panels WHERE id", []driver.Value{"missing-panel"}, panelCols, func(h *Handler, w http.ResponseWriter, r *http.Request) { h.deletePanel(w, r, "missing-panel") }, "/api/v1/dashboard/panels/missing-panel"},
	}

	h := &Handler{client: &http.Client{}}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			if err != nil {
				t.Fatalf("sqlmock: %v", err)
			}
			prev := store.GetDB()
			store.SetDB(db)
			t.Cleanup(func() { store.SetDB(prev) })

			mock.ExpectQuery(c.pattern).
				WithArgs(c.args...).
				WillReturnRows(sqlmock.NewRows(c.cols))

			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodDelete, c.path, nil)
			c.handler(h, rec, req)
			if rec.Code != http.StatusNotFound {
				t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatalf("unmet expectations: %v", err)
			}
		})
	}
}

// ===== P1-10: 告警静默写接口 admin 门禁 =====

func TestAlertSilenceCreateRequiresAdmin(t *testing.T) {
	h := &Handler{client: &http.Client{}}
	// No token → 403: the legacy route has no canonical authorization mapping.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/alerts/silences", strings.NewReader(`{"service":"svc"}`))
	h.AlertSilences(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("no token: code=%d, want 403", rec.Code)
	}
	// 非 admin → 403
	userToken := generateJWT("alice", "user", "")
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodPost, "/api/v1/alerts/silences", strings.NewReader(`{"service":"svc"}`))
	req2.Header.Set("Authorization", "Bearer "+userToken)
	h.AlertSilences(rec2, req2)
	if rec2.Code != http.StatusForbidden {
		t.Fatalf("non-admin: code=%d, want 403", rec2.Code)
	}
}

// ===== P2-2: 租户写接口 admin 门禁 =====

func TestTenantCreateRequiresAdmin(t *testing.T) {
	h := &Handler{client: &http.Client{}}
	// No token → 403: the legacy route has no canonical authorization mapping.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/tenants", strings.NewReader(`{"id":"t1","name":"T"}`))
	h.CreateTenant(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("no token: code=%d, want 403", rec.Code)
	}
	// 非 admin → 403
	userToken := generateJWT("alice", "user", "")
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodPost, "/api/v1/tenants", strings.NewReader(`{"id":"t2","name":"T"}`))
	req2.Header.Set("Authorization", "Bearer "+userToken)
	h.CreateTenant(rec2, req2)
	if rec2.Code != http.StatusForbidden {
		t.Fatalf("non-admin: code=%d, want 403", rec2.Code)
	}
}

// ===== P2-5: /logs/query keyword 别名 =====

func TestQueryLogsKeywordAlias(t *testing.T) {
	var lastSQL string
	chSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// P6.2b：统一 repository 用 POST body 传 SQL。
		if b, err := io.ReadAll(r.Body); err == nil {
			lastSQL = string(b)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(""))
	}))
	defer chSrv.Close()

	host, port := splitHostPort(chSrv.URL)
	h := &Handler{client: &http.Client{}}
	h.chHost = host
	h.chPort = port
	h.repo = *query.NewClickHouseRepo(fmt.Sprintf("http://%s:%d", host, port), &http.Client{Timeout: 5 * time.Second})
	h.logRepo = query.NewLogRepository(&h.repo, nil, query.NewSourceRouter(query.ModeLegacy))

	// keyword 非空、query 为空 → keyword 作为过滤条件
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/logs/query?keyword=payments+error", nil)
	h.QueryLogs(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(lastSQL, "body LIKE") || !strings.Contains(lastSQL, "payments error") {
		t.Fatalf("keyword should be used as body filter, sql=%s", lastSQL)
	}

	// 两者都传 → query 优先
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodGet, "/api/v1/logs/query?query=primary&keyword=secondary", nil)
	h.QueryLogs(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec2.Code, rec2.Body.String())
	}
	if !strings.Contains(lastSQL, "primary") {
		t.Fatalf("query param should take precedence, sql=%s", lastSQL)
	}
	if strings.Contains(lastSQL, "secondary") {
		t.Fatalf("keyword should be ignored when query is set, sql=%s", lastSQL)
	}
}

// ===== P2-6: /metrics/query PromQL 透传（VM instant query）=====

func TestQueryMetricsPromQLPassthrough(t *testing.T) {
	// A0-04（11.11.4）：任意 PromQL 直通已关闭（无 tenant/cluster matcher，跨租户泄漏风险）。
	// query 有、service 无 → 生产 fail-closed 400，不代理到 VM。
	vmSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("passthrough disabled: should never reach VM, got %s", r.URL.Path)
	}))
	defer vmSrv.Close()

	h := &Handler{client: &http.Client{}}
	h.vmURL = vmSrv.URL

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/metrics/query?query=rate(foo%5B5m%5D)", nil)
	h.QueryMetrics(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 (passthrough disabled), got %d: %s", rec.Code, rec.Body.String())
	}

	// query 有、service 无、VM 未配置 → 仍 400（不因 VM 状态改变行为）
	h.vmURL = ""
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodGet, "/api/v1/metrics/query?query=rate(foo%5B5m%5D)", nil)
	h.QueryMetrics(rec2, req2)
	if rec2.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 (passthrough disabled regardless of VM), got %d", rec2.Code)
	}
}

func TestQueryMetricsWithServiceKeepsCHPath(t *testing.T) {
	// A0-04：service 存在时走 CH RED typed 路径（不代理 VM）；需 concrete cluster + canonical tenant。
	vmHit := false
	vmSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		vmHit = true
		w.WriteHeader(http.StatusOK)
	}))
	defer vmSrv.Close()

	h := newTestHandler(t)
	h.vmURL = vmSrv.URL

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet,
		"/api/v1/metrics/query?service=frontend&cluster_id=91771a6e-9c2d-11f1-8271-bea176fe9f9f", nil)
	req = withAuthorizationContext(req, AuthorizationContext{
		TenantID: "7ed01afc-cc79-4ecd-8767-a2befa6168ad", UserID: "u", SessionID: "s",
	})
	h.QueryMetrics(rec, req)
	if rec.Code == http.StatusBadGateway || rec.Code == http.StatusServiceUnavailable ||
		rec.Code == http.StatusBadRequest || rec.Code == http.StatusForbidden {
		t.Fatalf("service+cluster present should keep CH path, got %d: %s", rec.Code, rec.Body.String())
	}
	if vmHit {
		t.Fatal("service present should NOT proxy to VM")
	}
}

func TestQueryMetricsRejectsAllCluster(t *testing.T) {
	// A0-04（11.11.3）：cluster_id=all 不当作空过滤，metrics 必须 concrete canonical cluster。
	h := newTestHandler(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/metrics/query?service=frontend&cluster_id=all", nil)
	req = withAuthorizationContext(req, AuthorizationContext{
		TenantID: "7ed01afc-cc79-4ecd-8767-a2befa6168ad", UserID: "u", SessionID: "s",
	})
	h.QueryMetrics(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for cluster_id=all, got %d: %s", rec.Code, rec.Body.String())
	}
}

// ===== /metrics/query 透传加固：响应大小上限 / 限流 429 =====

// TestQueryMetricsPromQLResponseTooLarge 验证任意 PromQL 直通已关闭（400 fail-closed），
// 不再有 VM 大响应/502 路径。
func TestQueryMetricsPromQLResponseTooLarge(t *testing.T) {
	vmSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("passthrough disabled: should never reach VM")
	}))
	defer vmSrv.Close()

	h := &Handler{client: &http.Client{}}
	h.vmURL = vmSrv.URL

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/metrics/query?query=rate(foo%5B5m%5D)", nil)
	req.RemoteAddr = "203.0.113.9:1234" // 独立 IP，避免与限流测试共享计数
	h.QueryMetrics(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 (passthrough disabled), got %d: %s", rec.Code, rec.Body.String())
	}
}

// TestQueryMetricsPromQLRateLimit 验证任意 PromQL 直通已关闭——即使超限前也恒 400，
// 不产生 VM 请求/限流路径（passthrough 在限流前 fail-closed）。
func TestQueryMetricsPromQLRateLimit(t *testing.T) {
	vmSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("passthrough disabled: should never reach VM")
	}))
	defer vmSrv.Close()

	h := &Handler{client: &http.Client{}}
	h.vmURL = vmSrv.URL

	ip := "203.0.113.10:1234" // 独立 IP，不影响其他测试
	for i := 0; i < metricsQueryLimit+1; i++ {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/metrics/query?query=rate(foo%5B5m%5D)", nil)
		req.RemoteAddr = ip
		h.QueryMetrics(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("call %d: expected 400 (passthrough disabled), got %d: %s", i, rec.Code, rec.Body.String())
		}
	}
}

// ===== ProxyAI 剥离客户端伪造 X-Internal-* 头 =====

type countingTransport func(*http.Request) (*http.Response, error)

func (transport countingTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return transport(request)
}

// TestProxyAIFailsClosedWithoutNewlySignedContext verifies token-only proxying
// cannot reach the orchestrator and a forged browser context is never forwarded.
func TestProxyAIFailsClosedWithoutNewlySignedContext(t *testing.T) {
	requests := 0
	client := &http.Client{Transport: countingTransport(func(r *http.Request) (*http.Response, error) {
		requests++
		return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody, Header: make(http.Header)}, nil
	})}
	t.Setenv("AI_ORCHESTRATOR_URL", "http://orchestrator.invalid")
	t.Setenv("INTERNAL_TOKEN", "test-service-token")

	h := &Handler{client: client}

	// A forged browser context and configured service token must not create a
	// token-only internal request.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/ai/chat", strings.NewReader(`{"msg":"hi"}`))
	req.Header.Set("X-Internal-User", "forged-user")
	req.Header.Set("X-Internal-Role", "admin")
	req.Header.Set("X-Internal-Approver", "1")
	req.Header.Set("X-Internal-Scope", "all")
	req.Header.Set("X-Trusted-Request-Context", "forged")
	h.ProxyAI(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("forged context: expected 403, got %d: %s", rec.Code, rec.Body.String())
	}
	if requests != 0 {
		t.Fatalf("forged context reached orchestrator %d times", requests)
	}

	// A JWT containing a forged role/scope still cannot construct a signed
	// context or use the service token by itself.
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/v1/ai/chat", strings.NewReader(`{"msg":"hi"}`))
	req.Header.Set("Authorization", "Bearer "+generateJWT("alice", "admin", `{"services":["*"]}`))
	req.Header.Set("X-Internal-User", "forged-user")
	req.Header.Set("X-Internal-Role", "admin")
	h.ProxyAI(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("token-only proxy: expected 403, got %d: %s", rec.Code, rec.Body.String())
	}
	if requests != 0 {
		t.Fatalf("token-only proxy reached orchestrator %d times", requests)
	}
}

func TestProxyShellWSRemainsManualAndRejectsBrowserAuthorization(t *testing.T) {
	t.Setenv("AI_ORCHESTRATOR_URL", "http://orchestrator.invalid")
	t.Setenv("INTERNAL_TOKEN", "service-token")

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/shell/ws?token=browser-jwt", nil)
	(&Handler{}).ProxyShellWS(recorder, request)

	if recorder.Code != http.StatusForbidden {
		t.Fatalf("ProxyShellWS() status = %d, want fail-closed 403", recorder.Code)
	}
}
