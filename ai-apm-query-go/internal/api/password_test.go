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

func TestAuthorizationContextHonorsDisabledFirstLoginPasswordChange(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	previous := store.GetDB()
	store.SetDB(db)
	t.Cleanup(func() { store.SetDB(previous) })
	t.Setenv("AUTH_REQUIRE_FIRST_LOGIN_PASSWORD_CHANGE", "false")

	mock.ExpectQuery("SELECT u.user_uuid, u.status, u.must_change_password, s.status, s.expires_at, s.revoked_at, s.token_version FROM users u JOIN auth_sessions s").
		WithArgs(authzUserID, authzSessionID).
		WillReturnRows(sqlmock.NewRows([]string{"user_uuid", "user_status", "must_change_password", "session_status", "expires_at", "revoked_at", "token_version"}).
			AddRow(authzUserID, 1, 1, "active", time.Now().Add(time.Hour), nil, int64(0)))
	mock.ExpectQuery("SELECT t.id FROM tenants t JOIN user_tenants ut").
		WithArgs(authzUserID, authzTenantID).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(authzTenantID))

	authorization, err := resolveMySQLAuthorizationContext(authzUserID, authzSessionID, authzTenantID, 0)
	if err != nil {
		t.Fatalf("resolveMySQLAuthorizationContext() error = %v", err)
	}
	if authorization.MustChangePassword {
		t.Fatal("disabled first-login password change must not block authorization")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestChangePasswordRejectsShortPassword(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/change-password", strings.NewReader(`{"current_password":"admin123","new_password":"short","confirm_password":"short"}`))
	req = withAuthorizationContext(req, AuthorizationContext{UserID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", TenantID: authzTenantID})
	rec := httptest.NewRecorder()
	(&Handler{}).ChangePassword(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("ChangePassword() status = %d, body = %s; want 400", rec.Code, rec.Body.String())
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["error"] != "new password must be at least 8 characters" {
		t.Fatalf("ChangePassword() error = %q", body["error"])
	}
}

func TestChangePasswordRejectsMismatchedConfirmation(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/change-password", strings.NewReader(`{"current_password":"admin123","new_password":"new-pass-123","confirm_password":"different"}`))
	req = withAuthorizationContext(req, AuthorizationContext{UserID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", TenantID: authzTenantID})
	rec := httptest.NewRecorder()
	(&Handler{}).ChangePassword(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("ChangePassword() status = %d, body = %s; want 400", rec.Code, rec.Body.String())
	}
}

func TestChangePasswordRotatesSessionAndClearsForceChange(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	previous := store.GetDB()
	store.SetDB(db)
	t.Cleanup(func() { store.SetDB(previous) })

	oldHash, err := bcrypt.GenerateFromPassword([]byte("admin123"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	mock.ExpectQuery(regexp.QuoteMeta("SELECT password_hash, must_change_password FROM users WHERE user_uuid = ? AND status = 1")).
		WithArgs(authzUserID).
		WillReturnRows(sqlmock.NewRows([]string{"password_hash", "must_change_password"}).AddRow(string(oldHash), 1))
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE users SET password_hash=\\?, must_change_password=0 WHERE user_uuid=\\? AND status=1").
		WithArgs(sqlmock.AnyArg(), authzUserID).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE auth_sessions SET status='revoked', revoked_at=UTC_TIMESTAMP\\(\\), token_version=token_version\\+1 WHERE user_uuid=\\? AND status='active'").
		WithArgs(authzUserID).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO auth_sessions (session_id, user_uuid, status, expires_at, revoked_at) VALUES (?, ?, 'active', ?, NULL)")).
		WithArgs(sqlmock.AnyArg(), authzUserID, sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT token_version FROM auth_sessions WHERE session_id = ?")).
		WithArgs(sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"token_version"}).AddRow(int64(0)))

	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/change-password", strings.NewReader(`{"current_password":"admin123","new_password":"new-pass-123","confirm_password":"new-pass-123"}`))
	req = withAuthorizationContext(req, AuthorizationContext{UserID: authzUserID, TenantID: authzTenantID, MustChangePassword: true})
	rec := httptest.NewRecorder()
	(&Handler{}).ChangePassword(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("ChangePassword() status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var response struct {
		Authenticated      bool `json:"authenticated"`
		MustChangePassword bool `json:"must_change_password"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if !response.Authenticated || response.MustChangePassword {
		t.Fatalf("ChangePassword() response = %+v, want authenticated cookie session and false force flag", response)
	}
	cookies := rec.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != "aiops_access" || cookies[0].Value == "" || !cookies[0].HttpOnly {
		t.Fatalf("ChangePassword() did not issue an HttpOnly rotated session cookie: %#v", cookies)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestAuthMiddlewareBlocksBusinessRoutesUntilPasswordChanges(t *testing.T) {
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
			AddRow(authzUserID, 1, 1, "active", time.Now().Add(time.Hour), nil, int64(0)))
	mock.ExpectQuery("SELECT t.id FROM tenants t JOIN user_tenants ut").
		WithArgs(authzUserID, authzTenantID).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(authzTenantID))

	called := false
	handler := AuthMiddleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }))
	req := httptest.NewRequest(http.MethodGet, "/api/v1/dashboard/stats", nil)
	req.Header.Set("Authorization", "Bearer "+generateJWTWithSession(authzUserID, authzSessionID, "admin", ""))
	req.Header.Set("X-Tenant-ID", authzTenantID)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden || called || !strings.Contains(rec.Body.String(), "password_change_required") {
		t.Fatalf("forced-change business request = status %d called=%v body=%s", rec.Code, called, rec.Body.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestMeProjectsCurrentPasswordChangeState(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	previous := store.GetDB()
	store.SetDB(db)
	t.Cleanup(func() { store.SetDB(previous) })

	mock.ExpectQuery(regexp.QuoteMeta("SELECT id, user_uuid, username, password_hash, display_name, role, email, status, scope, is_approver, created_at FROM users WHERE user_uuid = ?")).
		WithArgs(authzUserID).
		WillReturnRows(sqlmock.NewRows([]string{"id", "user_uuid", "username", "password_hash", "display_name", "role", "email", "status", "scope", "is_approver", "created_at"}).
			AddRow(1, authzUserID, "admin", "", "管理员", "admin", "", 1, "", 1, time.Now()))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/me", nil)
	req = withAuthorizationContext(req, AuthorizationContext{UserID: authzUserID, TenantID: authzTenantID, MustChangePassword: true})
	rec := httptest.NewRecorder()
	(&Handler{}).Me(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("Me() status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var response map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response["must_change_password"] != true {
		t.Fatalf("Me() must_change_password = %v, want true", response["must_change_password"])
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
