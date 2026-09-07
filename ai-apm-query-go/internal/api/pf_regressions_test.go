package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/observability-platform/ai-apm-query-go/internal/store"
	"golang.org/x/crypto/bcrypt"
)

// ── PF-LOGIC-004：/api/v1/alerts/rules/{id} 的 PUT/DELETE 必须能到达 handler ──

// TestAlertRuleByIDChildRouteIsCanonicalProtected 验证规则详情子路由纳入
// canonical-protected 白名单（否则 AuthMiddleware 一律 403，规则编辑/删除不可用），
// 同时保持 "create" 字面量与多段子路径 fail-closed。
func TestAlertRuleByIDChildRouteIsCanonicalProtected(t *testing.T) {
	if !isCanonicalProtectedRoute("/api/v1/alerts/rules/rule-to-update") {
		t.Fatal("PUT/DELETE /api/v1/alerts/rules/{id} must be canonical-protected so authorized writes reach the handler")
	}
	if isCanonicalProtectedRoute("/api/v1/alerts/rules/create") {
		t.Fatal("/api/v1/alerts/rules/create must stay fail-closed (create uses POST /api/v1/alerts/rules)")
	}
	if isCanonicalProtectedRoute("/api/v1/alerts/rules/rule-1/extra") {
		t.Fatal("multi-segment alerts/rules child path must stay fail-closed")
	}
}

func seedAlertRuleForTest(t *testing.T) {
	t.Helper()
	alertRulesMu.Lock()
	originalRules := append([]AlertRule(nil), alertRules...)
	alertRules = []AlertRule{{
		ID: "rule-to-update", Name: "old name", Service: "checkout", Type: "threshold",
		Metric: "error_rate", Threshold: 5, Duration: 5, Severity: "warning",
	}}
	alertRulesMu.Unlock()
	t.Cleanup(func() {
		alertRulesMu.Lock()
		alertRules = originalRules
		alertRulesMu.Unlock()
	})
}

// TestAuthMiddlewareRoutesAlertRuleWriteToHandler 验证带 canonical 会话（JWT cookie
// + tenant 成员）的 PUT /api/v1/alerts/rules/{id} 不再被 AuthMiddleware 以
// permission_denied 拦截，能到达 handler（admin 可写由 handler 权威校验）。
func TestAuthMiddlewareRoutesAlertRuleWriteToHandler(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	previous := store.GetDB()
	store.SetDB(db)
	t.Cleanup(func() { store.SetDB(previous) })

	expectActiveSessionScope(mock, authzTenantID, "")
	mock.ExpectQuery("SELECT u.user_uuid, u.status, u.must_change_password, s.status, s.expires_at, s.revoked_at, s.token_version FROM users u JOIN auth_sessions s").
		WithArgs(authzUserID, authzSessionID).
		WillReturnRows(sqlmock.NewRows([]string{"user_uuid", "user_status", "must_change_password", "session_status", "expires_at", "revoked_at", "token_version"}).
			AddRow(authzUserID, 1, 0, "active", time.Now().Add(time.Hour), nil, int64(0)))
	mock.ExpectQuery("SELECT t.id FROM tenants t JOIN user_tenants ut").
		WithArgs(authzUserID, authzTenantID).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(authzTenantID))

	called := false
	handler := AuthMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodPut, "/api/v1/alerts/rules/rule-to-update",
		strings.NewReader(`{"name":"updated","service":"checkout","type":"threshold"}`))
	req.Header.Set("Authorization", "Bearer "+generateJWTWithSession(authzUserID, authzSessionID, "admin", ""))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if !called || rec.Code == http.StatusForbidden {
		t.Fatalf("PUT /api/v1/alerts/rules/{id} through middleware: status=%d called=%v body=%s, want handler reached",
			rec.Code, called, rec.Body.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// TestAlertRuleUpdateDeleteReachHandlerWithAuthoritativeAdmin 验证 handler 端
// MySQL 权威 admin 角色通过后，PUT/DELETE 真正生效（规则被更新/删除）。
func TestAlertRuleUpdateDeleteReachHandlerWithAuthoritativeAdmin(t *testing.T) {
	h := &Handler{}
	seedAlertRuleForTest(t)

	run := func(method, body string, verify func(t *testing.T)) func(t *testing.T) {
		return func(t *testing.T) {
			db, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			previous := store.GetDB()
			store.SetDB(db)
			t.Cleanup(func() { store.SetDB(previous) })
			// authoritativeUser → GetByUUID（admin 权威角色）
			mock.ExpectQuery(regexp.QuoteMeta("SELECT id, user_uuid, username, password_hash, display_name, role, email, status, scope, is_approver, created_at FROM users WHERE user_uuid = ?")).
				WithArgs(authzUserID).
				WillReturnRows(sqlmock.NewRows([]string{"id", "user_uuid", "username", "password_hash", "display_name", "role", "email", "status", "scope", "is_approver", "created_at"}).
					AddRow(1, authzUserID, "admin", "x", "管理员", "admin", "", 1, "", 0, time.Now()))

			req := httptest.NewRequest(method, "/api/v1/alerts/rules/rule-to-update", strings.NewReader(body))
			req = withAuthorizationContext(req, AuthorizationContext{UserID: authzUserID, TenantID: authzTenantID})
			rec := httptest.NewRecorder()
			h.AlertRuleByID(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("%s alert rule with authoritative admin: status=%d body=%s, want 200", method, rec.Code, rec.Body.String())
			}
			verify(t)
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		}
	}

	t.Run("put", run(http.MethodPut,
		`{"name":"checkout errors","service":"checkout","type":"threshold","metric":"error_rate","threshold":2,"duration":10,"severity":"critical"}`,
		func(t *testing.T) {
			alertRulesMu.RLock()
			updated := alertRules[0]
			alertRulesMu.RUnlock()
			if updated.Name != "checkout errors" || updated.Threshold != 2 || updated.Duration != 10 {
				t.Fatalf("rule not updated: %+v", updated)
			}
		}))

	t.Run("delete", run(http.MethodDelete, "", func(t *testing.T) {
		alertRulesMu.RLock()
		defer alertRulesMu.RUnlock()
		if len(alertRules) != 0 {
			t.Fatalf("rule not deleted: %+v", alertRules)
		}
	}))
}

// ── PF-PAGE-002：错误当前密码返回 400（不是 401，避免被前端会话拦截器误登出）──

func TestChangePasswordWrongCurrentPasswordReturns400(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	previous := store.GetDB()
	store.SetDB(db)
	t.Cleanup(func() { store.SetDB(previous) })

	storedHash, err := bcrypt.GenerateFromPassword([]byte("correct-current"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	mock.ExpectQuery(regexp.QuoteMeta("SELECT password_hash, must_change_password FROM users WHERE user_uuid = ? AND status = 1")).
		WithArgs(authzUserID).
		WillReturnRows(sqlmock.NewRows([]string{"password_hash", "must_change_password"}).AddRow(string(storedHash), 0))

	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/change-password",
		strings.NewReader(`{"current_password":"wrong-current","new_password":"new-pass-123","confirm_password":"new-pass-123"}`))
	req = withAuthorizationContext(req, AuthorizationContext{UserID: authzUserID, TenantID: authzTenantID})
	rec := httptest.NewRecorder()
	(&Handler{}).ChangePassword(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("ChangePassword() wrong current password: status = %d, body = %s; want 400", rec.Code, rec.Body.String())
	}
	var body map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["error"] != "invalid_current_password" {
		t.Fatalf("ChangePassword() error = %v, want invalid_current_password", body["error"])
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// ── PF-UI-004 / LOGIC-001：/auth/logout 吊销服务端会话 ──

func TestLogoutRevokesSessionAndClearsCookie(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	previous := store.GetDB()
	store.SetDB(db)
	t.Cleanup(func() { store.SetDB(previous) })

	mock.ExpectExec("UPDATE auth_sessions SET status='revoked', revoked_at=UTC_TIMESTAMP").
		WithArgs(authzSessionID, authzUserID).
		WillReturnResult(sqlmock.NewResult(0, 1))

	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
	req = withAuthorizationContext(req, AuthorizationContext{UserID: authzUserID, SessionID: authzSessionID, TenantID: authzTenantID})
	rec := httptest.NewRecorder()
	(&Handler{}).Logout(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("Logout() status = %d, body = %s; want 200", rec.Code, rec.Body.String())
	}
	var body map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["ok"] != true {
		t.Fatalf("Logout() body = %v, want ok=true", body)
	}
	cookies := rec.Result().Cookies()
	found := false
	for _, c := range cookies {
		if c.Name == "aiops_access" && c.MaxAge < 0 {
			found = true
		}
	}
	if !found {
		t.Fatalf("Logout() did not clear the aiops_access cookie: %#v", cookies)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// TestLogoutWithoutTenantScopeStillReachesHandler 验证尚未选择 canonical tenant 的
// 会话也能退出登录（identity-only 校验路径），并真正吊销 auth_sessions。
func TestLogoutWithoutTenantScopeStillReachesHandler(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	previous := store.GetDB()
	store.SetDB(db)
	t.Cleanup(func() { store.SetDB(previous) })

	// 尚未选择 tenant：active_tenant_id 为空
	mock.ExpectQuery("SELECT COALESCE\\(active_tenant_id, ''\\), COALESCE\\(active_cluster_id, ''\\), authorization_version").
		WithArgs(authzSessionID).
		WillReturnRows(sqlmock.NewRows([]string{"tenant_id", "cluster_id", "authorization_version"}).AddRow("", "", int64(0)))
	// resolveMySQLAuthorizationIdentity：仅身份/会话/token_version 校验
	mock.ExpectQuery("SELECT u.user_uuid, u.status, u.must_change_password, s.status, s.expires_at, s.revoked_at, s.token_version FROM users u JOIN auth_sessions s").
		WithArgs(authzUserID, authzSessionID).
		WillReturnRows(sqlmock.NewRows([]string{"user_uuid", "user_status", "must_change_password", "session_status", "expires_at", "revoked_at", "token_version"}).
			AddRow(authzUserID, 1, 0, "active", time.Now().Add(time.Hour), nil, int64(0)))
	// Logout 的会话吊销
	mock.ExpectExec("UPDATE auth_sessions SET status='revoked', revoked_at=UTC_TIMESTAMP").
		WithArgs(authzSessionID, authzUserID).
		WillReturnResult(sqlmock.NewResult(0, 1))

	handler := AuthMiddleware(http.HandlerFunc((&Handler{}).Logout))
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
	req.Header.Set("Authorization", "Bearer "+generateJWTWithSession(authzUserID, authzSessionID, "admin", ""))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("Logout() without selected tenant: status = %d, body = %s; want 200", rec.Code, rec.Body.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// ── PF-LOGIC-013：创建用户写入 user_tenants 成员关系 ──

func expectUserCreateInsert(mock sqlmock.Sqlmock) {
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO users (user_uuid, username, password_hash, display_name, role, email, status, is_approver) VALUES (LOWER(UUID()), ?, ?, ?, ?, ?, ?, ?)")).
		WithArgs("bob", sqlmock.AnyArg(), "", "user", "", 1, 0).
		WillReturnResult(sqlmock.NewResult(42, 1))
}

func expectCreatedUserLookup(mock sqlmock.Sqlmock) {
	// assignUserTenant 按 id 取回 MySQL LOWER(UUID()) 生成的 canonical UUID
	// （users 创建返回值只有自增 id，GetByID 不投影 user_uuid）。
	mock.ExpectQuery(regexp.QuoteMeta("SELECT user_uuid FROM users WHERE id = ?")).
		WithArgs(int64(42)).
		WillReturnRows(sqlmock.NewRows([]string{"user_uuid"}).AddRow("user-uuid-bob"))
}

func TestUserCreateWritesTenantMembership(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	previous := store.GetDB()
	store.SetDB(db)
	t.Cleanup(func() { store.SetDB(previous) })

	expectUserCreateInsert(mock)
	expectCreatedUserLookup(mock)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT t.id FROM tenants t WHERE t.id=? AND t.enabled=1 LIMIT 1")).
		WithArgs(authzTenantID).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(authzTenantID))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO user_tenants (user_uuid, tenant_id, status) VALUES (?, ?, 'active')")).
		WithArgs("user-uuid-bob", authzTenantID).
		WillReturnResult(sqlmock.NewResult(1, 1))

	// 未显式传 tenant_id：默认写入创建者（admin）会话的激活租户。
	req := httptest.NewRequest(http.MethodPost, "/api/v1/users", strings.NewReader(`{"username":"bob","password":"secret123","display_name":"Bob"}`))
	req = withAuthorizationContext(req, AuthorizationContext{UserID: authzUserID, TenantID: authzTenantID})
	rec := httptest.NewRecorder()
	(&Handler{}).UserCreate(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("UserCreate() status = %d, body = %s; want 200", rec.Code, rec.Body.String())
	}
	var body map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["tenant_id"] != authzTenantID {
		t.Fatalf("UserCreate() tenant_id = %v, want %q", body["tenant_id"], authzTenantID)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestUserCreateHonorsExplicitTenantID(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	previous := store.GetDB()
	store.SetDB(db)
	t.Cleanup(func() { store.SetDB(previous) })

	const explicitTenant = "dddddddd-dddd-4ddd-8ddd-dddddddddddd"
	expectUserCreateInsert(mock)
	expectCreatedUserLookup(mock)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT t.id FROM tenants t WHERE t.id=? AND t.enabled=1 LIMIT 1")).
		WithArgs(explicitTenant).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(explicitTenant))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO user_tenants (user_uuid, tenant_id, status) VALUES (?, ?, 'active')")).
		WithArgs("user-uuid-bob", explicitTenant).
		WillReturnResult(sqlmock.NewResult(1, 1))

	req := httptest.NewRequest(http.MethodPost, "/api/v1/users",
		strings.NewReader(`{"username":"bob","password":"secret123","tenant_id":"`+explicitTenant+`"}`))
	rec := httptest.NewRecorder()
	(&Handler{}).UserCreate(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("UserCreate() status = %d, body = %s; want 200", rec.Code, rec.Body.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// TestUserCreateRollsBackWhenMembershipWriteFails 验证成员关系写入失败时回滚刚
// 创建的用户（不留半创建状态），且创建失败不静默成功。
func TestUserCreateRollsBackWhenMembershipWriteFails(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	previous := store.GetDB()
	store.SetDB(db)
	t.Cleanup(func() { store.SetDB(previous) })

	expectUserCreateInsert(mock)
	expectCreatedUserLookup(mock)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT t.id FROM tenants t WHERE t.id=? AND t.enabled=1 LIMIT 1")).
		WithArgs(authzTenantID).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(authzTenantID))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO user_tenants (user_uuid, tenant_id, status) VALUES (?, ?, 'active')")).
		WithArgs("user-uuid-bob", authzTenantID).
		WillReturnError(errMembershipWrite{})
	mock.ExpectExec("DELETE FROM users WHERE id=\\?").
		WithArgs(int64(42)).
		WillReturnResult(sqlmock.NewResult(0, 1))

	req := httptest.NewRequest(http.MethodPost, "/api/v1/users", strings.NewReader(`{"username":"bob","password":"secret123"}`))
	req = withAuthorizationContext(req, AuthorizationContext{UserID: authzUserID, TenantID: authzTenantID})
	rec := httptest.NewRecorder()
	(&Handler{}).UserCreate(rec, req)

	if rec.Code == http.StatusOK {
		t.Fatalf("UserCreate() with failed membership write: status = %d body = %s; want non-200", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "tenant membership write failed") {
		t.Fatalf("UserCreate() body = %s, want tenant membership failure detail", rec.Body.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

type errMembershipWrite struct{}

func (errMembershipWrite) Error() string { return "membership insert failed" }
