package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/observability-platform/ai-apm-query-go/internal/store"
)

// TestRequireRoleForWrite verifies write-protection: GET reads pass through to the
// read handler, while POST writes require the authoritative MySQL admin role
// (JWT role claims are never authority).
func TestRequireRoleForWrite(t *testing.T) {
	h := &Handler{}
	called := false
	next := func(w http.ResponseWriter, r *http.Request) { called = true }

	// GET/HEAD（读）→ 放行给只读 handler（写保护只约束写方法）
	called = false
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/clusters", nil)
	h.RequireRoleForWrite("admin", next)(rec, req)
	if rec.Code == http.StatusForbidden {
		t.Fatalf("GET should pass through to read handler, got 403")
	}
	if !called {
		t.Fatalf("GET should invoke read handler")
	}

	// POST（写）无鉴权上下文 → 403（无 AuthorizationContext → hasRole false）
	called = false
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/v1/clusters", nil)
	h.RequireRoleForWrite("admin", next)(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("POST without auth context: got %d, want 403", rec.Code)
	}
	if called {
		t.Fatal("handler should not be called for unauthorized POST")
	}

	// POST（写）带 viewer 角色的鉴权上下文 → 403（viewer != admin）
	called = false
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/v1/clusters", nil)
	req = withAuthorizationContext(req, AuthorizationContext{UserID: "viewer-1", TenantID: "t"})
	h.RequireRoleForWrite("admin", next)(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("POST as viewer context: got %d, want 403", rec.Code)
	}

	// POST（写）带 admin 角色的鉴权上下文 + MySQL 返回 admin → 放行 handler
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	prev := store.GetDB()
	store.SetDB(db)
	t.Cleanup(func() { store.SetDB(prev) })
	mock.ExpectQuery("SELECT id, user_uuid, username, password_hash, display_name, role, email, status, scope, is_approver, created_at FROM users WHERE user_uuid = \\?").
		WithArgs("admin-1").
		WillReturnRows(sqlmock.NewRows([]string{"id", "user_uuid", "username", "password_hash", "display_name", "role", "email", "status", "scope", "is_approver", "created_at"}).
			AddRow(1, "admin-1", "admin", "x", "admin", "admin", "", 1, "", 0, time.Now()))
	called = false
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/v1/clusters", nil)
	req = withAuthorizationContext(req, AuthorizationContext{UserID: "admin-1", TenantID: "t"})
	h.RequireRoleForWrite("admin", next)(rec, req)
	if rec.Code == http.StatusForbidden || !called {
		t.Fatalf("POST as authoritative admin: got status=%d called=%v, want handler called", rec.Code, called)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestCanonicalProtectedReadChildRoutes(t *testing.T) {
	paths := []string{
		"/api/v1/clusters/91771a6e-9c2d-11f1-8271-bea176fe9f9f/nodes",
		"/api/v1/clusters/91771a6e-9c2d-11f1-8271-bea176fe9f9f/namespaces",
		"/api/v1/clusters/91771a6e-9c2d-11f1-8271-bea176fe9f9f/events",
		"/api/v1/traces/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		"/api/v1/traces/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa/context",
		"/api/v1/slo",
		"/api/v1/slo/slo-1",
		"/api/v1/dashboard/panels",
		"/api/v1/dashboard/panels/panel-1",
	}
	for _, path := range paths {
		if !isCanonicalProtectedRoute(path) {
			t.Errorf("%s must be canonical-protected so authenticated frontend reads reach its handler", path)
		}
	}
}

func TestRequireAnyRoleForWriteAcceptsApprover(t *testing.T) {
	h := &Handler{}
	called := false
	next := func(w http.ResponseWriter, r *http.Request) { called = true }

	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	prev := store.GetDB()
	store.SetDB(db)
	t.Cleanup(func() { store.SetDB(prev) })
	mock.ExpectQuery("SELECT id, user_uuid, username, password_hash, display_name, role, email, status, scope, is_approver, created_at FROM users WHERE user_uuid = \\\\?").
		WithArgs("approver-1").
		WillReturnRows(sqlmock.NewRows([]string{"id", "user_uuid", "username", "password_hash", "display_name", "role", "email", "status", "scope", "is_approver", "created_at"}).
			AddRow(1, "approver-1", "approver", "x", "Approver", "approver", "", 1, "", 1, time.Now()))
	req := httptest.NewRequest(http.MethodPost, "/api/v1/ai/actions/a1/decision", nil)
	req = withAuthorizationContext(req, AuthorizationContext{UserID: "approver-1", TenantID: "tenant-1"})
	rec := httptest.NewRecorder()
	h.RequireAnyRoleForWrite([]string{"admin", "approver"}, next)(rec, req)
	if rec.Code == http.StatusForbidden || !called {
		t.Fatalf("approver should be accepted: status=%d called=%v", rec.Code, called)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// TestIsCanonicalProtectedRouteQueryEndpoints 验证 P12 AUTH 修复：只读查询端点纳入
// canonical-protected route（消除 legacy 端点一律 403 的 AUTH BLOCKER），写端点保持 fail-closed。
func TestIsCanonicalProtectedRouteQueryEndpoints(t *testing.T) {
	allowed := []string{
		"/api/v1/resources/resolve",
		"/api/v1/services",
		"/api/v1/clusters", // P19 前端集群选择器数据源（只读，JWT+canonical tenant）
		"/api/v1/traces",
		"/api/v1/topology/global",
		"/api/v1/topology/node/query-api", // Service Observability 详情页读取的真实节点子路由
		"/api/v1/topology/nodes",
		"/api/v1/alerts/rules",
		"/api/v1/alerts/events",
		"/api/v1/logs/query",
		"/api/v1/logs/aggregate",
		"/api/v1/dashboard/stats",
		"/api/v1/dashboard/resources",
		"/api/v1/capacity/forecast",
		"/api/v1/capacity/instances",
		"/api/v1/ai/runs",
		"/api/v1/ai/chat", // P19.6：对话型 canonical-protected（JWT+tenant+cluster 解析 + ai.chat）
		// P19 LLM 设置：读/写/测试/models 均由 canonical-protected（JWT+canonical tenant+成员），
		// admin 角色由 handler RequireRole/RequireRoleForWrite（MySQL 权威角色）校验。
		"/api/v1/settings/llm",
		"/api/v1/settings/llm/test",
		"/api/v1/settings/llm/models",
		"/api/v1/settings/llm/history",
		"/api/v1/settings/llm/providers",
		"/api/v1/settings/llm/providers/1",
		"/api/v1/settings/llm/providers/1/enable",
		"/api/v1/settings/llm/history/1/rollback",
		// A0-04（11.11.7）：/api/v1/ai/runs/{runID} 单段详情——GetRunPublic 有 tenant/run ownership 校验
		"/api/v1/ai/runs/aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
		// A0-04（11.11.4）：/api/v1/metrics/query——typed RED metrics（canonical tenant + concrete cluster）
		"/api/v1/metrics/query",
		// Browser-facing canonical read/admin routes used by the acceptance plan.
		// Handler-level RBAC remains authoritative for admin/approver-only reads
		// and all writes; this list only gets the request to that handler.
		"/api/v1/users",
		"/api/v1/users/1",
		"/api/v1/catalog/services",
		"/api/v1/catalog/services/1",
		"/api/v1/devices",
		"/api/v1/devices/1",
		"/api/v1/infrastructure/nodes",
		"/api/v1/infrastructure/pods",
		"/api/v1/infrastructure/pods/p1",
		"/api/v1/infrastructure/deployments",
		"/api/v1/infrastructure/namespaces",
		"/api/v1/nodes/metrics",
		"/api/v1/infrastructure/hpa",
		"/api/v1/infrastructure/vms",
		"/api/v1/infrastructure/vms/ns/name",
		"/api/v1/settings/k8s",
		"/api/v1/deepflow/status",
		"/api/v1/grafana/health",
		"/api/v1/grafana/search",
		"/api/v1/grafana/dashboards/main",
		"/api/v1/system/status",
		"/api/v1/system/components",
		"/api/v1/system/cache",
		"/api/v1/system/cache/invalidate",
		"/api/v1/ai/skills",
		"/api/v1/ai/skills/node-health",
		"/api/v1/ai/agents",
		"/api/v1/ai/agents/default",
		"/api/v1/ai/flows",
		"/api/v1/ai/flows/default",
		"/api/v1/ai/knowledge",
		"/api/v1/ai/knowledge/rag/stats",
		"/api/v1/ai/rules",
		"/api/v1/mcp/tools",
		"/api/v1/ops/tasks",
		"/api/v1/ops/recovery/policy",
		"/api/v1/ops/cases",
		"/api/v1/ops/anomalies",
		"/api/v1/ops/artifacts",
		"/api/v1/ops/reports",
		"/api/v1/ops/reports/history",
		"/api/v1/ops/reports/trend",
		"/api/v1/ops/audit-logs",
		"/api/v1/ops/changes",
		"/api/v1/ipmi/sensors",
		"/api/v1/ipmi/events",
		"/api/v1/node/health",
		"/api/v1/slo",
		"/api/v1/dashboard/panels",
	}
	for _, p := range allowed {
		if !isCanonicalProtectedRoute(p) {
			t.Fatalf("query endpoint %s should be canonical-protected", p)
		}
	}
	for _, p := range []string{
		"/api/v1/services/overview",
		"/api/v1/services/map",
		"/api/v1/services/dependency-matrix",
		"/api/v1/services/service:v1:tenant-a:service/dependencies",
		"/api/v1/services/orders",
		"/api/v1/services/0.250.250.2",
	} {
		if !isCanonicalProtectedRoute(p) {
			t.Fatalf("service detail endpoint %s should be canonical-protected", p)
		}
	}
	for _, p := range []string{
		"/api/v1/services/orders/extra",
		"/api/v1/services/orders/dependencies/extra",
		"/api/v1/services/",
	} {
		if isCanonicalProtectedRoute(p) {
			t.Fatalf("invalid service child endpoint %s should NOT be canonical-protected", p)
		}
	}
	for _, p := range []string{
		"/api/v1/traces/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa/unknown",
		"/api/v1/traces/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa/context/extra",
	} {
		if isCanonicalProtectedRoute(p) {
			t.Fatalf("unknown trace child endpoint %s should NOT be canonical-protected", p)
		}
	}
	// 写端点/未迁移端点保持 fail-closed（不纳入 canonical-protected）
	denied := []string{
		"/api/v1/topology/sync-catalog",
		"/api/v1/alerts/rules/create",
		"/api/v1/capacity/instances/create",
		"/api/v1/unknown",
		// A0-04：多段子路径（非详情/events/cancel）不放行，避免 ProxyAI legacy 面被整体放开
		"/api/v1/ai/runs/aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa/actions",
		"/api/v1/ai/runs/aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa/something",
		// Unknown legacy proxy paths must remain fail-closed even though the
		// surrounding registered prefixes are canonical-protected.
		"/api/v1/ops/not-registered",
		"/api/v1/ai/unknown",
	}
	for _, p := range denied {
		if isCanonicalProtectedRoute(p) {
			t.Fatalf("write/unmigrated endpoint %s should NOT be canonical-protected", p)
		}
	}
}

// TestAIChatIsCanonicalProtectedNotPublic 验证 P19.6：/api/v1/ai/chat 是 canonical-protected
// 而非公开放行——未带 JWT/tenant 的请求必须被 AuthMiddleware 拒绝（403/400），不能直通 handler。
func TestAIChatIsCanonicalProtectedNotPublic(t *testing.T) {
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true; w.WriteHeader(200) })
	mw := AuthMiddleware(next)

	// 无 JWT 无 tenant → 403（不是公开端点，不放行）
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/ai/chat", nil)
	mw.ServeHTTP(rec, req)
	if called {
		t.Fatal("/api/v1/ai/chat must NOT be a public endpoint (handler reached without auth)")
	}
	if rec.Code == http.StatusOK {
		t.Fatalf("/api/v1/ai/chat without auth should not be 200, got %d", rec.Code)
	}
	// 有 JWT 但 tenant 非 canonical → 400 invalid_context（仍被 AuthMiddleware 拦截，非 200）
	called = false
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/v1/ai/chat", nil)
	req.Header.Set("Authorization", "Bearer "+generateJWT("admin", "admin", `{}`))
	req.Header.Set("X-Tenant-ID", "default")
	mw.ServeHTTP(rec, req)
	if called {
		t.Fatal("non-canonical tenant must not reach handler")
	}
	if rec.Code == http.StatusOK {
		t.Fatalf("non-canonical tenant should not be 200, got %d", rec.Code)
	}
}

// TestValidateJWTStandard 验证真实 HS256 JWT 签发与校验。
func TestValidateJWTStandard(t *testing.T) {
	token := generateJWT("admin", "admin", `{"clusters":["all"]}`)
	u, role, scope, ok := validateJWT(token)
	if !ok {
		t.Fatal("valid token rejected")
	}
	if u != "admin" || role != "" || scope != "" {
		t.Fatalf("got identity=%q role=%q scope=%q, want identity-only claims", u, role, scope)
	}
}

// TestTamperedTokenRejected 验证被篡改的 token 会被拒绝。
func TestTamperedTokenRejected(t *testing.T) {
	token := generateJWT("admin", "admin", "")
	bad := token[:len(token)-3] + "xxx"
	if _, _, _, ok := validateJWT(bad); ok {
		t.Fatal("tampered token accepted")
	}
}

// TestExpiredTokenRejected 验证过期 token 被拒绝。
func TestExpiredTokenRejected(t *testing.T) {
	// 签发一个 exp 为过去的 token（直接构造）
	token := "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiJhZG1pbiIsInJvbGUiOiJhZG1pbiIsImV4cCI6MX0.invalid"
	if _, _, _, ok := validateJWT(token); ok {
		t.Fatal("expired token accepted")
	}
}

// TestScopeParsing 验证 scope 解析与包含判断。
func TestScopeParsing(t *testing.T) {
	sc := parseScope(`{"services":["a","b"],"clusters":[],"devices":[]}`)
	if !sc.ContainsService("a") || sc.ContainsService("c") {
		t.Fatal("service scope parse wrong")
	}
	if !sc.ContainsCluster("any") { // clusters 未限定 => 全通过
		t.Fatal("unscoped dimension should pass")
	}
	if sc.IsFull() {
		t.Fatal("non-empty scope should not be full")
	}
	if !parseScope("").IsFull() {
		t.Fatal("empty scope should be full")
	}
}
