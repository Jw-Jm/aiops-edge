package api

import (
	"crypto/ed25519"
	"database/sql"
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
	proxyClusterID   = "dddddddd-dddd-4ddd-8ddd-dddddddddddd"
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
	expectActiveSessionScope(mock, authzTenantID, "")
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
	expectNewChatSessionPersistence(mock)
}

func expectNewChatSessionPersistence(mock sqlmock.Sqlmock) {
	// The first request creates a fresh, canonical UUID session.  History is
	// queried before EnsureSession and must be an explicit no-row result.
	mock.ExpectQuery(`(?s)SELECT session_id,intent,service,created_at,updated_at.*FROM ai_chat_sessions WHERE session_id=\? AND user_uuid=\? AND tenant_id=\? AND cluster_id=\?`).
		WithArgs(sqlmock.AnyArg(), authzUserID, authzTenantID, proxyClusterID).
		WillReturnError(sql.ErrNoRows)
	// EnsureSession uses an atomic no-op upsert so concurrent Query replicas do
	// not race on the session primary key.
	mock.ExpectExec(`INSERT INTO ai_chat_sessions[\s\S]*ON DUPLICATE KEY UPDATE session_id = session_id`).
		WithArgs(sqlmock.AnyArg(), authzUserID, authzTenantID, proxyClusterID, "", "orders").
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectQuery("SELECT user_uuid,tenant_id,cluster_id FROM ai_chat_sessions WHERE session_id=\\?").
		WithArgs(sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"user_uuid", "tenant_id", "cluster_id"}).AddRow(authzUserID, authzTenantID, proxyClusterID))
	mock.ExpectExec("UPDATE ai_chat_sessions SET intent=\\?,service=\\?,updated_at=CURRENT_TIMESTAMP\\(3\\) WHERE session_id=\\?").
		WithArgs("", "orders", sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(`(?s)SELECT user_uuid FROM ai_chat_sessions.*WHERE session_id=\? AND user_uuid=\? AND tenant_id=\? AND cluster_id=\?`).
		WithArgs(sqlmock.AnyArg(), authzUserID, authzTenantID, proxyClusterID).
		WillReturnRows(sqlmock.NewRows([]string{"user_uuid"}).AddRow(authzUserID))
	mock.ExpectExec(`INSERT INTO ai_chat_messages\(session_id,turn_id,role,kind,content,metadata_json\)[\s\S]*ON DUPLICATE KEY UPDATE id = id`).
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), "user", "", "diag", nil).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectQuery(`(?s)SELECT role,kind,content,metadata_json FROM ai_chat_messages.*WHERE session_id=\? AND turn_id=\? AND role=\? AND kind=\?`).
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), "user", "").
		WillReturnRows(sqlmock.NewRows([]string{"role", "kind", "content", "metadata_json"}).AddRow("user", "", "diag", nil))
	// ProxyChat checks the durable turn before invoking the orchestrator.  A
	// fresh turn contains only the user card, so the lookup must return no rows.
	mock.ExpectQuery(`(?s)SELECT user_uuid FROM ai_chat_sessions.*WHERE session_id=\? AND user_uuid=\? AND tenant_id=\? AND cluster_id=\?`).
		WithArgs(sqlmock.AnyArg(), authzUserID, authzTenantID, proxyClusterID).
		WillReturnRows(sqlmock.NewRows([]string{"user_uuid"}).AddRow(authzUserID))
	mock.ExpectQuery(`(?s)SELECT id,role,kind,content,metadata_json,created_at FROM ai_chat_messages WHERE session_id=\? AND turn_id=\? ORDER BY id`).
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"id", "role", "kind", "content", "metadata_json", "created_at"}))
	mock.ExpectQuery(`(?s)SELECT user_uuid FROM ai_chat_sessions.*WHERE session_id=\? AND user_uuid=\? AND tenant_id=\? AND cluster_id=\?`).
		WithArgs(sqlmock.AnyArg(), authzUserID, authzTenantID, proxyClusterID).
		WillReturnRows(sqlmock.NewRows([]string{"user_uuid"}).AddRow(authzUserID))
	mock.ExpectExec(`INSERT INTO ai_chat_messages\(session_id,turn_id,role,kind,content,metadata_json\)[\s\S]*ON DUPLICATE KEY UPDATE id = id`).
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), "assistant", "", "ok", nil).
		WillReturnResult(sqlmock.NewResult(2, 1))
	mock.ExpectQuery(`(?s)SELECT role,kind,content,metadata_json FROM ai_chat_messages.*WHERE session_id=\? AND turn_id=\? AND role=\? AND kind=\?`).
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), "assistant", "").
		WillReturnRows(sqlmock.NewRows([]string{"role", "kind", "content", "metadata_json"}).AddRow("assistant", "", "ok", nil))
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

func TestReplayChatTurnEmitsDurableCardsOnly(t *testing.T) {
	rec := httptest.NewRecorder()
	messages := []store.ChatMessage{
		{Role: "user", Content: "diag"},
		{Role: "assistant", Kind: "suggestion", Metadata: map[string]any{"script": "kubectl get deploy"}},
		{Role: "assistant", Content: "root cause summary"},
	}
	if !replayChatTurn(rec, "11111111-1111-4111-8111-111111111111", "55555555-5555-4555-8555-555555555555", messages) {
		t.Fatal("replayChatTurn() = false, want completed turn")
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("replay status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "id: 1\nevent: suggestion") || !strings.Contains(body, "id: 2\nevent: done") {
		t.Fatalf("durable SSE cards missing: %s", body)
	}
	if strings.Contains(body, "diag") {
		t.Fatalf("replay must not emit the user card: %s", body)
	}
	if rec.Header().Get("X-Chat-Turn-Id") == "" || rec.Header().Get("X-Session-Id") != "11111111-1111-4111-8111-111111111111" {
		t.Fatalf("replay identity headers missing: %#v", rec.Header())
	}
}

func TestPersistChatSSEFramesReturnsPersistenceError(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	prev := store.GetDB()
	store.SetDB(db)
	t.Cleanup(func() { store.SetDB(prev) })

	const (
		sessionID = "11111111-1111-4111-8111-111111111111"
		turnID    = "22222222-2222-4222-8222-222222222222"
	)
	mock.ExpectQuery(`(?s)SELECT user_uuid FROM ai_chat_sessions.*WHERE session_id=\? AND user_uuid=\? AND tenant_id=\? AND cluster_id=\?`).
		WithArgs(sessionID, authzUserID, authzTenantID, proxyClusterID).
		WillReturnRows(sqlmock.NewRows([]string{"user_uuid"}).AddRow(authzUserID))
	mock.ExpectExec(`INSERT INTO ai_chat_messages\(session_id,turn_id,role,kind,content,metadata_json\)[\s\S]*ON DUPLICATE KEY UPDATE id = id`).
		WithArgs(sessionID, turnID, "assistant", "", "answer", nil).
		WillReturnError(sql.ErrConnDone)

	remaining, err := persistChatSSEFrames(
		&store.AIChatSessionDAO{}, sessionID, turnID,
		AuthorizationContext{UserID: authzUserID, TenantID: authzTenantID, ActiveClusterID: proxyClusterID},
		"event: done\ndata: {\"text\":\"answer\"}\n\n")
	if err == nil {
		t.Fatal("persistChatSSEFrames() error = nil, want persistence failure")
	}
	if remaining != "" {
		t.Fatalf("persistChatSSEFrames() remaining = %q, want empty after failed frame", remaining)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestReplayChatTurnDoesNotReplayIncompleteTurn(t *testing.T) {
	rec := httptest.NewRecorder()
	if replayChatTurn(rec, "11111111-1111-4111-8111-111111111111", "55555555-5555-4555-8555-555555555555", []store.ChatMessage{{Role: "user", Content: "diag"}}) {
		t.Fatal("replayChatTurn() = true for incomplete turn")
	}
	if rec.Code != http.StatusOK || rec.Body.Len() != 0 {
		t.Fatalf("incomplete replay mutated response: status=%d body=%q", rec.Code, rec.Body.String())
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
	expectActiveSessionScope(mock, authzTenantID, "")
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
	expectNewChatSessionPersistence(mock)

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
	expectActiveSessionScope(mock, authzTenantID, "")
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
	expectActiveSessionScope(mock, authzTenantID, "")
	mock.ExpectQuery("SELECT u.user_uuid, u.status, u.must_change_password, s.status, s.expires_at, s.revoked_at, s.token_version FROM users u JOIN auth_sessions s").
		WithArgs(authzUserID, authzSessionID).
		WillReturnRows(sqlmock.NewRows([]string{"user_uuid", "user_status", "must_change_password", "session_status", "expires_at", "revoked_at", "token_version"}).
			AddRow(authzUserID, 1, 0, "active", time.Now().Add(time.Hour), nil, int64(0)))
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
