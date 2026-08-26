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

func TestLoginPersistsCanonicalSessionUsedByAuthorization(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	previous := store.GetDB()
	store.SetDB(db)
	t.Cleanup(func() { store.SetDB(previous) })

	passwordHash, err := bcrypt.GenerateFromPassword([]byte("correct-password"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	const canonicalUserID = "11111111-1111-4111-8111-111111111111"
	mock.ExpectQuery(regexp.QuoteMeta("SELECT id, user_uuid, username, password_hash, display_name, role, email, status, scope, is_approver, must_change_password, created_at FROM users WHERE username = ?")).
		WithArgs("alice").
		WillReturnRows(sqlmock.NewRows([]string{"id", "user_uuid", "username", "password_hash", "display_name", "role", "email", "status", "scope", "is_approver", "must_change_password", "created_at"}).
			AddRow(int64(7), canonicalUserID, "alice", string(passwordHash), "Alice", "admin", "alice@example.com", 1, `{"clusters":["all"]}`, 0, 1, time.Now()))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO auth_sessions (session_id, user_uuid, status, expires_at, revoked_at) VALUES (?, ?, 'active', ?, NULL)")).
		WithArgs(sqlmock.AnyArg(), canonicalUserID, sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))
	// JWT issuance reads the authoritative token_version from auth_sessions (V9.2 §8).
	mock.ExpectQuery(regexp.QuoteMeta("SELECT token_version FROM auth_sessions WHERE session_id = ?")).
		WithArgs(sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"token_version"}).AddRow(int64(0)))

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"username":"alice","password":"correct-password"}`))
	(&Handler{}).Login(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("Login() status = %d, body = %s, sql = %v", recorder.Code, recorder.Body.String(), mock.ExpectationsWereMet())
	}
	var response struct {
		Token              string `json:"token"`
		MustChangePassword bool   `json:"must_change_password"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	userID, sessionID, ok := validateJWTIdentity(response.Token)
	if !ok || userID != canonicalUserID || sessionID == "" {
		t.Fatalf("Login() token identity = %q/%q valid=%v, want canonical user UUID and persisted session ID", userID, sessionID, ok)
	}
	if !response.MustChangePassword {
		t.Fatal("Login() must_change_password=false, want true")
	}

	mock.ExpectQuery("SELECT u.user_uuid, u.status, u.must_change_password, s.status, s.expires_at, s.revoked_at, s.token_version FROM users u JOIN auth_sessions s").
		WithArgs(canonicalUserID, sessionID).
		WillReturnRows(sqlmock.NewRows([]string{"user_uuid", "user_status", "must_change_password", "session_status", "expires_at", "revoked_at", "token_version"}).
			AddRow(canonicalUserID, 1, 1, "active", time.Now().Add(time.Hour), nil, int64(0)))
	mock.ExpectQuery("SELECT t.id FROM tenants t JOIN user_tenants ut").
		WithArgs(canonicalUserID, authzTenantID).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(authzTenantID))
	authorized := httptest.NewRequest(http.MethodGet, "/api/v1/resources/resolve", nil)
	authorized.Header.Set("Authorization", "Bearer "+response.Token)
	authorized.Header.Set("X-Tenant-ID", authzTenantID)
	context, err := RequestAuthorizationContext(authorized)
	if err != nil || context.UserID != canonicalUserID || context.SessionID != sessionID {
		t.Fatalf("RequestAuthorizationContext() = %+v, %v; want persisted canonical login identity", context, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
