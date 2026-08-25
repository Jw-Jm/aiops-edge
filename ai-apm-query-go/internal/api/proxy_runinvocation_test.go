package api

import (
	"crypto/ed25519"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/observability-platform/ai-apm-query-go/internal/auth"
	"github.com/observability-platform/ai-apm-query-go/internal/store"
)

const (
	proxyClusterID = "dddddddd-dddd-4ddd-8ddd-dddddddddddd"
	proxyClusterSlug = "prod-sg-01"
)

func configureRunInvocationIssuer(t *testing.T) {
	t.Helper()
	_, priv, _ := ed25519.GenerateKey(nil)
	issuer, err := auth.NewRunInvocationIssuer(priv, "query-to-orch-token")
	if err != nil {
		t.Fatal(err)
	}
	ConfigureRunInvocationIssuer(issuer)
	t.Cleanup(func() { ConfigureRunInvocationIssuer(nil) })
}

func setupProxyMySQL(mock sqlmock.Sqlmock) {
	expectRequestIdentityAndTenant(mock)
	// ClusterDAO.ResolveRef — SELECT id, cluster_id, tenant_id, slug, name, ... FROM clusters WHERE tenant_id=? AND slug=?
	mock.ExpectQuery("SELECT id, cluster_id, tenant_id, slug, name, environment, region, credential_ref, lifecycle_status, created_at, updated_at\nFROM clusters WHERE tenant_id = \\? AND slug = \\?").
		WithArgs(authzTenantID, proxyClusterSlug).
		WillReturnRows(sqlmock.NewRows([]string{"id", "cluster_id", "tenant_id", "slug", "name", "environment", "region", "credential_ref", "lifecycle_status", "created_at", "updated_at"}).
			AddRow(1, proxyClusterID, authzTenantID, proxyClusterSlug, "prod-sg", "prod", "cn", "k8s-secret://ns/a", "active", time.Now(), time.Now()))
	// TenantClustersForCluster
	mock.ExpectQuery("SELECT tenant_id FROM tenant_clusters WHERE cluster_id = \\?").
		WithArgs(proxyClusterID).
		WillReturnRows(sqlmock.NewRows([]string{"tenant_id"}).AddRow(authzTenantID))
	// UserDAO.GetByUUID — query-api 权威角色 SoT（在 cluster 解析后、签发前执行）：admin 授予 ai.chat
	mock.ExpectQuery("SELECT id, user_uuid, username, password_hash, display_name, role, email, status, scope, is_approver, created_at FROM users WHERE user_uuid = \\?").
		WithArgs(authzUserID).
		WillReturnRows(sqlmock.NewRows([]string{"id", "user_uuid", "username", "password_hash", "display_name", "role", "email", "status", "scope", "is_approver", "created_at"}).
			AddRow(1, authzUserID, "admin", "x", "admin", "admin", "", 1, "", 0, time.Now()))
}

func TestProxyChatSignsAI_CHATAndForwardsStreaming(t *testing.T) {
	configureRunInvocationIssuer(t)
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	prev := store.GetDB()
	store.SetDB(db)
	t.Cleanup(func() { store.SetDB(prev) })

	var gotInternalToken, gotTrustedContext, gotPath string
	orch := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotInternalToken = r.Header.Get("X-Internal-Token")
		gotTrustedContext = r.Header.Get("X-Trusted-Request-Context")
		// orchestrator 返回 SSE 流：模拟多帧
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("event: progress\ndata: {\"text\":\"analyzing\"}\n\n"))
		w.Write([]byte("event: done\ndata: {\"text\":\"ok\"}\n\n"))
	}))
	defer orch.Close()
	t.Setenv("AI_ORCHESTRATOR_URL", orch.URL)

	setupProxyMySQL(mock)

	h := &Handler{}
	token := generateJWTWithSession(authzUserID, authzSessionID, "admin", `{}`)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/ai/chat",
		strings.NewReader(`{"service":"orders","cluster":"`+proxyClusterSlug+`","message":"diag"}`))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-Tenant-ID", authzTenantID)
	h.ProxyChat(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("ProxyChat() = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if gotPath != "/internal/v1/chat" {
		t.Fatalf("forwarded path = %q, want /internal/v1/chat", gotPath)
	}
	if gotInternalToken != "query-to-orch-token" {
		t.Fatalf("forwarded service token = %q", gotInternalToken)
	}
	if gotTrustedContext == "" {
		t.Fatal("forwarded RunInvocationContext is empty")
	}
	if !strings.Contains(gotTrustedContext, ".") {
		t.Fatalf("forwarded context not JWS: %s", gotTrustedContext)
	}
	// 签名上下文必须带 canonical cluster UUID（非 slug），且 capability=ai.chat（对话型）。
	parts := strings.Split(gotTrustedContext, ".")
	payload := decodeRawURL(parts[1])
	if !strings.Contains(string(payload), proxyClusterID) {
		t.Fatalf("signed context must contain canonical cluster UUID, got %s", payload)
	}
	if strings.Contains(string(payload), proxyClusterSlug) {
		t.Fatalf("signed context must NOT contain raw slug, got %s", payload)
	}
	if !strings.Contains(string(payload), `"capability":"ai.chat"`) {
		t.Fatalf("signed context must carry capability=ai.chat, got %s", payload)
	}
	// SSE 流式透传：recorder 必须收到多帧（含两帧），证明非缓冲透传。
	body := rec.Body.String()
	if !strings.Contains(body, "event: progress") || !strings.Contains(body, "event: done") {
		t.Fatalf("SSE stream not transparently relayed, got: %s", body)
	}
	if rec.Header().Get("Content-Type") != "text/event-stream" {
		t.Fatalf("Content-Type = %q, want text/event-stream", rec.Header().Get("Content-Type"))
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// TestProxyChatAcceptsClusterIDField 验证前端 AiChat 发送的 cluster_id（canonical UUID）字段
// 也能被 ProxyChat 解析（兼容 cluster 与 cluster_id 两种 body 字段），并签名 canonical cluster。
func TestProxyChatAcceptsClusterIDField(t *testing.T) {
	configureRunInvocationIssuer(t)
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	prev := store.GetDB()
	store.SetDB(db)
	t.Cleanup(func() { store.SetDB(prev) })

	var gotTrustedContext string
	orch := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotTrustedContext = r.Header.Get("X-Trusted-Request-Context")
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("event: done\ndata: {\"text\":\"ok\"}\n\n"))
	}))
	defer orch.Close()
	t.Setenv("AI_ORCHESTRATOR_URL", orch.URL)

	// JWT + tenant 身份校验（与 setupProxyMySQL 一致），但 cluster 用 canonical UUID →
	// ResolveRef 走 cluster_id 分支（非 slug 分支）。
	expectRequestIdentityAndTenant(mock)
	mock.ExpectQuery("SELECT id, cluster_id, tenant_id, slug, name, environment, region, credential_ref, lifecycle_status, created_at, updated_at\nFROM clusters WHERE tenant_id = \\? AND cluster_id = \\? AND cluster_id IS NOT NULL AND cluster_id != '' AND lifecycle_status IN \\('active', 'ready'\\)").
		WithArgs(authzTenantID, proxyClusterID).
		WillReturnRows(sqlmock.NewRows([]string{"id", "cluster_id", "tenant_id", "slug", "name", "environment", "region", "credential_ref", "lifecycle_status", "created_at", "updated_at"}).
			AddRow(1, proxyClusterID, authzTenantID, "prod-sg-01", "prod-sg", "prod", "cn", "k8s-secret://ns/a", "active", time.Now(), time.Now()))
	// TenantClustersForCluster ownership
	mock.ExpectQuery("SELECT tenant_id FROM tenant_clusters WHERE cluster_id = \\?").
		WithArgs(proxyClusterID).
		WillReturnRows(sqlmock.NewRows([]string{"tenant_id"}).AddRow(authzTenantID))
	// UserDAO.GetByUUID — 权威角色 SoT（cluster 解析后、签发前执行）：admin 授予 ai.chat
	mock.ExpectQuery("SELECT id, user_uuid, username, password_hash, display_name, role, email, status, scope, is_approver, created_at FROM users WHERE user_uuid = \\?").
		WithArgs(authzUserID).
		WillReturnRows(sqlmock.NewRows([]string{"id", "user_uuid", "username", "password_hash", "display_name", "role", "email", "status", "scope", "is_approver", "created_at"}).
			AddRow(1, authzUserID, "admin", "x", "admin", "admin", "", 1, "", 0, time.Now()))

	h := &Handler{}
	token := generateJWTWithSession(authzUserID, authzSessionID, "admin", `{}`)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/ai/chat",
		strings.NewReader(`{"service":"orders","cluster_id":"`+proxyClusterID+`","message":"diag"}`))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-Tenant-ID", authzTenantID)
	h.ProxyChat(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("ProxyChat(cluster_id) = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	// 解码 JWS payload 校验 canonical cluster UUID 与 ai.chat capability。
	parts := strings.Split(gotTrustedContext, ".")
	payload := decodeRawURL(parts[1])
	if !strings.Contains(string(payload), proxyClusterID) {
		t.Fatalf("signed context must carry canonical cluster UUID, got %s", payload)
	}
	if !strings.Contains(string(payload), `"capability":"ai.chat"`) {
		t.Fatalf("signed context must carry capability=ai.chat, got %s", payload)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// TestProxyChatRejectsUserWithoutAIChatRole：用户 RBAC 权威 SoT —— 用户角色不授予
// ai.chat（inactive status）→ query-api 不签发上下文，fail-closed 403。
func TestProxyChatRejectsUserWithoutAIChatRole(t *testing.T) {
	configureRunInvocationIssuer(t)
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	prev := store.GetDB()
	store.SetDB(db)
	t.Cleanup(func() { store.SetDB(prev) })

	// cluster 解析 + ownership 正常，但用户 status=0（inactive）→ 无 ai.chat 授权
	expectRequestIdentityAndTenant(mock)
	mock.ExpectQuery("SELECT id, cluster_id, tenant_id, slug, name, environment, region, credential_ref, lifecycle_status, created_at, updated_at\nFROM clusters WHERE tenant_id = \\? AND slug = \\?").
		WithArgs(authzTenantID, proxyClusterSlug).
		WillReturnRows(sqlmock.NewRows([]string{"id", "cluster_id", "tenant_id", "slug", "name", "environment", "region", "credential_ref", "lifecycle_status", "created_at", "updated_at"}).
			AddRow(1, proxyClusterID, authzTenantID, proxyClusterSlug, "prod-sg", "prod", "cn", "k8s-secret://ns/a", "active", time.Now(), time.Now()))
	mock.ExpectQuery("SELECT tenant_id FROM tenant_clusters WHERE cluster_id = \\?").
		WithArgs(proxyClusterID).
		WillReturnRows(sqlmock.NewRows([]string{"tenant_id"}).AddRow(authzTenantID))
	// UserDAO.GetByUUID — inactive user (status=0) → authorizeUserChatCapability=false
	mock.ExpectQuery("SELECT id, user_uuid, username, password_hash, display_name, role, email, status, scope, is_approver, created_at FROM users WHERE user_uuid = \\?").
		WithArgs(authzUserID).
		WillReturnRows(sqlmock.NewRows([]string{"id", "user_uuid", "username", "password_hash", "display_name", "role", "email", "status", "scope", "is_approver", "created_at"}).
			AddRow(1, authzUserID, "disabled", "x", "disabled", "admin", "", 0, "", 0, time.Now()))

	h := &Handler{}
	token := generateJWTWithSession(authzUserID, authzSessionID, "admin", `{}`)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/ai/chat",
		strings.NewReader(`{"service":"orders","cluster":"`+proxyClusterSlug+`","message":"diag"}`))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-Tenant-ID", authzTenantID)
	h.ProxyChat(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("ProxyChat(inactive user) = %d, want 403; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "permission_denied") {
		t.Fatalf("want permission_denied, got %s", rec.Body.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestProxyAIMissingClusterFailsClosed(t *testing.T) {
	configureRunInvocationIssuer(t)
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	prev := store.GetDB()
	store.SetDB(db)
	t.Cleanup(func() { store.SetDB(prev) })

	// auth passes, but body has no cluster → fail closed before any cluster query.
	mock.ExpectQuery("SELECT u.user_uuid, u.status, s.status, s.expires_at, s.revoked_at, s.token_version FROM users u JOIN auth_sessions s").
		WithArgs(authzUserID, authzSessionID).
		WillReturnRows(sqlmock.NewRows([]string{"user_uuid", "user_status", "session_status", "expires_at", "revoked_at", "token_version"}).
			AddRow(authzUserID, 1, "active", time.Now().Add(time.Hour), nil, int64(0)))
	mock.ExpectQuery("SELECT t.id FROM tenants t JOIN user_tenants ut").
		WithArgs(authzUserID, authzTenantID).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(authzTenantID))

	h := &Handler{}
	token := generateJWTWithSession(authzUserID, authzSessionID, "admin", `{}`)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/ai/chat",
		strings.NewReader(`{"service":"orders","message":"diag"}`))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-Tenant-ID", authzTenantID)
	h.ProxyChat(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("missing cluster = %d, want 403; body=%s", rec.Code, rec.Body.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestProxyAINonChatRouteStaysFailClosed(t *testing.T) {
	configureRunInvocationIssuer(t)
	db, _, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	prev := store.GetDB()
	store.SetDB(db)
	t.Cleanup(func() { store.SetDB(prev) })

	h := &Handler{}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/ai/skills", nil)
	h.ProxyAI(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("non-chat route = %d, want 403", rec.Code)
	}
}

func decodeRawURL(s string) []byte {
	b, _ := base64.RawURLEncoding.DecodeString(s)
	return b
}
