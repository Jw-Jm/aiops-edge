package api

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/observability-platform/ai-apm-query-go/internal/store"
)

func TestProxyAILegacyReadForwardsCanonicalIdentity(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	previous := store.GetDB()
	store.SetDB(db)
	t.Cleanup(func() { store.SetDB(previous) })

	var gotPath, gotToken, gotTenant, gotRole, gotUser string
	client := &http.Client{Transport: countingTransport(func(r *http.Request) (*http.Response, error) {
		gotPath = r.URL.Path
		gotToken = r.Header.Get("X-Internal-Token")
		gotTenant = r.Header.Get("X-Tenant-ID")
		gotRole = r.Header.Get("X-Internal-Role")
		gotUser = r.Header.Get("X-Internal-User")
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"skills":[]}`)), Header: http.Header{"Content-Type": []string{"application/json"}}}, nil
	})}
	t.Setenv("AI_ORCHESTRATOR_URL", "http://orchestrator.test")
	t.Setenv("INTERNAL_TOKEN", "proxy-secret")
	expectActiveSessionScope(mock, authzTenantID, "")
	expectRequestIdentityAndTenant(mock)
	expectProxyUserRole(mock, "admin", 0)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/ai/skills", nil)
	req.Header.Set("Authorization", "Bearer "+generateJWTWithSession(authzUserID, authzSessionID, "viewer", `{}`))
	req.Header.Set("X-Tenant-ID", authzTenantID)
	rec := httptest.NewRecorder()
	(&Handler{client: client}).ProxyAI(rec, req)

	if rec.Code != http.StatusOK || rec.Body.String() != `{"skills":[]}` {
		t.Fatalf("status=%d body=%s, want forwarded response", rec.Code, rec.Body.String())
	}
	if gotPath != "/api/v1/ai/skills" || gotToken != "proxy-secret" || gotTenant != authzTenantID {
		t.Fatalf("forwarded identity path=%q token=%q tenant=%q", gotPath, gotToken, gotTenant)
	}
	if gotRole != "admin" || gotUser != authzUserID {
		t.Fatalf("forwarded authoritative operator role=%q user=%q", gotRole, gotUser)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestProxyAILegacyWriteForwardsBodyAndRole(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	previous := store.GetDB()
	store.SetDB(db)
	t.Cleanup(func() { store.SetDB(previous) })

	var gotMethod, gotBody, gotApprover string
	client := &http.Client{Transport: countingTransport(func(r *http.Request) (*http.Response, error) {
		gotMethod = r.Method
		body, _ := io.ReadAll(r.Body)
		gotBody = string(body)
		gotApprover = r.Header.Get("X-Internal-Approver")
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"ok":true}`)), Header: http.Header{"Content-Type": []string{"application/json"}}}, nil
	})}
	t.Setenv("AI_ORCHESTRATOR_URL", "http://orchestrator.test")
	t.Setenv("INTERNAL_TOKEN", "proxy-secret")
	expectActiveSessionScope(mock, authzTenantID, "")
	expectRequestIdentityAndTenant(mock)
	expectProxyUserRole(mock, "admin", 0)

	body := `{"service":"checkout","change_type":"deploy","content":"test"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/ops/changes", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+generateJWTWithSession(authzUserID, authzSessionID, "admin", `{}`))
	req.Header.Set("X-Tenant-ID", authzTenantID)
	rec := httptest.NewRecorder()
	(&Handler{client: client}).ProxyAI(rec, req)

	if rec.Code != http.StatusOK || gotMethod != http.MethodPost || gotBody != body {
		t.Fatalf("status=%d method=%q body=%q", rec.Code, gotMethod, gotBody)
	}
	if gotApprover != "1" {
		t.Fatalf("admin write must carry authoritative approver marker, got %q", gotApprover)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func expectProxyUserRole(mock sqlmock.Sqlmock, role string, approver int) {
	mock.ExpectQuery("SELECT id, user_uuid.*FROM users WHERE user_uuid").
		WithArgs(authzUserID).
		WillReturnRows(sqlmock.NewRows([]string{"id", "user_uuid", "username", "password_hash", "display_name", "role", "email", "status", "scope", "is_approver", "created_at"}).
			AddRow(1, authzUserID, "admin", "x", "admin", role, "", 1, "", approver, time.Now()))
}

func TestProxyAILegacyUnknownPathFailsClosed(t *testing.T) {
	forwarded := false
	client := &http.Client{Transport: countingTransport(func(r *http.Request) (*http.Response, error) {
		forwarded = true
		return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody, Header: make(http.Header)}, nil
	})}
	t.Setenv("AI_ORCHESTRATOR_URL", "http://orchestrator.test")
	t.Setenv("INTERNAL_TOKEN", "proxy-secret")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/ops/not-registered", nil)
	rec := httptest.NewRecorder()
	(&Handler{client: client}).ProxyAI(rec, req)

	if rec.Code != http.StatusForbidden || forwarded {
		t.Fatalf("unknown proxy path status=%d forwarded=%v, want fail-closed", rec.Code, forwarded)
	}
}
