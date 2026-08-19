package api

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	trustedauth "github.com/observability-platform/ai-apm-query-go/internal/auth"
	"github.com/observability-platform/ai-apm-query-go/internal/contract"
	"github.com/observability-platform/ai-apm-query-go/internal/store"
)

const (
	authzUserID    = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	authzSessionID = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	authzTenantID  = "cccccccc-cccc-4ccc-8ccc-cccccccccccc"
)

func TestRequestAuthorizationContextRejectsTenantFallbackAndForgedClaims(t *testing.T) {
	token := generateJWT(authzUserID, "admin", `{"clusters":["all"]}`)

	request := httptest.NewRequest(http.MethodGet, "/api/v1/resources/resolve?type=deployment&name=orders&cluster_id=all&namespace=production", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("X-Internal-Role", "admin")
	request.Header.Set("X-Internal-Approver", "1")
	if _, err := RequestAuthorizationContext(request); !isAuthorizationError(err, "invalid_context") {
		t.Fatalf("RequestAuthorizationContext() error = %v, want invalid_context without an explicit authorized tenant", err)
	}
}

func TestRequestAuthorizationContextDoesNotUseJWTClaimsAsAuthority(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	previous := store.GetDB()
	store.SetDB(db)
	t.Cleanup(func() { store.SetDB(previous) })
	mock.ExpectQuery("SELECT u.user_uuid, u.status, s.status, s.expires_at, s.revoked_at FROM users u JOIN user_sessions s").
		WithArgs(authzUserID, authzSessionID).
		WillReturnRows(sqlmock.NewRows([]string{"user_uuid", "user_status", "session_status", "expires_at", "revoked_at"}).
			AddRow(authzUserID, 1, "active", time.Now().Add(time.Hour), nil))
	mock.ExpectQuery("SELECT t.id FROM tenants t JOIN user_tenants ut").
		WithArgs(authzUserID, authzTenantID).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(authzTenantID))

	token := generateJWTWithSession(authzUserID, authzSessionID, "admin", `{"services":["*"]}`)
	request := httptest.NewRequest(http.MethodGet, "/api/v1/resources/resolve?type=deployment&name=orders&cluster_id=production&namespace=production", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("X-Tenant-ID", authzTenantID)

	context, err := RequestAuthorizationContext(request)
	if err != nil {
		t.Fatalf("RequestAuthorizationContext() error = %v", err)
	}
	if context.UserID != authzUserID || context.SessionID == "" || context.TenantID != authzTenantID {
		t.Fatalf("RequestAuthorizationContext() = %+v, want identity/session and requested tenant only", context)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestResolveResourceReturnsCanonicalReferenceOnlyAfterAuthorization(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	previous := store.GetDB()
	store.SetDB(db)
	t.Cleanup(func() { store.SetDB(previous) })
	expectRequestIdentityAndTenant(mock)
	expectAuthorizationCluster(mock, "production")
	expectRequestIdentityAndTenant(mock)
	expectAuthorizationCluster(mock, "dddddddd-dddd-4ddd-8ddd-dddddddddddd")
	mock.ExpectQuery("SELECT 1 FROM user_roles").
		WithArgs(authzUserID, authzTenantID, "kubernetes.read").
		WillReturnRows(sqlmock.NewRows([]string{"1"}).AddRow(1))
	mock.ExpectQuery("SELECT 1 FROM user_roles ur JOIN role_permissions rp").
		WithArgs(authzUserID, authzTenantID, "dddddddd-dddd-4ddd-8ddd-dddddddddddd", "production", "deployment", "orders", "kubernetes.read", "kubernetes.read").
		WillReturnRows(sqlmock.NewRows([]string{"1"}).AddRow(1))

	request := httptest.NewRequest(http.MethodGet, "/api/v1/resources/resolve?type=deployment&name=orders&cluster_id=production&namespace=production", nil)
	request.Header.Set("Authorization", "Bearer "+generateJWTWithSession(authzUserID, authzSessionID, "admin", `{"services":["*"]}`))
	request.Header.Set("X-Tenant-ID", authzTenantID)
	recorder := httptest.NewRecorder()
	(&Handler{}).ResolveResource(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("ResolveResource() status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Data struct {
			TenantID  string `json:"tenant_id"`
			ClusterID string `json:"cluster_id"`
			Resource  string `json:"resource_id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Data.TenantID != authzTenantID || response.Data.ClusterID != "dddddddd-dddd-4ddd-8ddd-dddddddddddd" || response.Data.Resource != "urn:aiops:cccccccc-cccc-4ccc-8ccc-cccccccccccc:dddddddd-dddd-4ddd-8ddd-dddddddddddd:deployment:production:orders" {
		t.Fatalf("ResolveResource() = %+v, want canonical resource reference", response.Data)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestGetLLMSettingsReturnsOnlyNonSecretHealthStatus(t *testing.T) {
	settingsMu.Lock()
	previous := settings.LLM
	settings.LLM = LLMSettings{Provider: "openai", Model: "gpt", BaseURL: "https://llm.example", APIKey: "encrypted-key"}
	settingsMu.Unlock()
	t.Cleanup(func() {
		settingsMu.Lock()
		settings.LLM = previous
		settingsMu.Unlock()
	})

	recorder := httptest.NewRecorder()
	(&Handler{}).GetLLMSettings(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/settings/llm", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("GetLLMSettings() status = %d", recorder.Code)
	}
	var response map[string]map[string]interface{}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	data := response["data"]
	if _, ok := data["configured"].(bool); !ok {
		t.Fatalf("GetLLMSettings() = %v, want configured health signal", data)
	}
	for _, forbidden := range []string{"provider", "model", "base_url", "api_key_set", "api_key_masked", "api_key"} {
		if _, exists := data[forbidden]; exists {
			t.Fatalf("GetLLMSettings() exposed %q in public status: %v", forbidden, data)
		}
	}
}

func TestRequestAuthorizationContextRequiresSignedInternalContext(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	restore := configureInternalRequestVerifier(trustedauth.VerifyConfig{
		Audience: "ai-apm-query-go", Issuer: "ai-orchestrator", ServiceToken: "service-token",
		PublicKeys: map[string]ed25519.PublicKey{trustedauth.KeyID(publicKey): publicKey}, ReplayCache: trustedauth.NewReplayCache(8),
	})
	t.Cleanup(restore)

	missing := httptest.NewRequest(http.MethodGet, "/api/v1/resources/resolve", nil)
	missing.Header.Set("X-Internal-Token", "service-token")
	if _, err := RequestAuthorizationContext(missing); !isAuthorizationError(err, "permission_denied") {
		t.Fatalf("service token without signed context error = %v, want permission_denied", err)
	}

	invalid := httptest.NewRequest(http.MethodGet, "/api/v1/resources/resolve", nil)
	invalid.Header.Set("X-Internal-Token", "service-token")
	invalid.Header.Set("X-Trusted-Request-Context", "not.a.signature")
	if _, err := RequestAuthorizationContext(invalid); !isAuthorizationError(err, "permission_denied") {
		t.Fatalf("invalid signed context error = %v, want permission_denied", err)
	}

	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	previous := store.GetDB()
	store.SetDB(db)
	t.Cleanup(func() { store.SetDB(previous) })
	expectRequestIdentityAndTenant(mock)

	token, err := trustedauth.SignTrustedRequestContext(testTrustedRequestContext("ai-apm-query-go"), privateKey)
	if err != nil {
		t.Fatal(err)
	}
	valid := httptest.NewRequest(http.MethodGet, "/api/v1/resources/resolve", nil)
	valid.Header.Set("X-Internal-Token", "service-token")
	valid.Header.Set("X-Trusted-Request-Context", token)
	if _, err := RequestAuthorizationContext(valid); err != nil {
		t.Fatalf("valid signed context error = %v", err)
	}
	replay := valid.Clone(valid.Context())
	if _, err := RequestAuthorizationContext(replay); !isAuthorizationError(err, "permission_denied") {
		t.Fatalf("replayed signed context error = %v, want permission_denied", err)
	}

	wrongAudience, err := trustedauth.SignTrustedRequestContext(testTrustedRequestContext("another-api"), privateKey)
	if err != nil {
		t.Fatal(err)
	}
	wrong := httptest.NewRequest(http.MethodGet, "/api/v1/resources/resolve", nil)
	wrong.Header.Set("X-Internal-Token", "service-token")
	wrong.Header.Set("X-Trusted-Request-Context", wrongAudience)
	if _, err := RequestAuthorizationContext(wrong); !isAuthorizationError(err, "permission_denied") {
		t.Fatalf("wrong-audience signed context error = %v, want permission_denied", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestAuthMiddlewareRejectsServiceTokenWithoutSignedContext(t *testing.T) {
	t.Setenv("INTERNAL_TOKEN", "test-service-token")
	called := false
	handler := AuthMiddleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		called = true
	}))

	request := httptest.NewRequest(http.MethodGet, "/api/v1/resources/resolve", nil)
	request.Header.Set("X-Internal-Token", "test-service-token")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusForbidden || called {
		t.Fatalf("service token without signed context: status=%d called=%v, want 403 and no handler call", recorder.Code, called)
	}
}

func TestAuthMiddlewareFailsClosedForLegacyRoute(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	previous := store.GetDB()
	store.SetDB(db)
	t.Cleanup(func() { store.SetDB(previous) })
	expectRequestIdentityAndTenant(mock)

	called := false
	handler := AuthMiddleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		called = true
	}))
	request := httptest.NewRequest(http.MethodGet, "/api/v1/clusters", nil)
	request.Header.Set("Authorization", "Bearer "+generateJWTWithSession(authzUserID, authzSessionID, "admin", `{"clusters":["all"]}`))
	request.Header.Set("X-Tenant-ID", authzTenantID)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusForbidden || called {
		t.Fatalf("legacy route with valid identity: status=%d called=%v, want 403 and no handler call", recorder.Code, called)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func testTrustedRequestContext(audience string) contract.RequestContext {
	now := time.Now().UTC()
	return contract.RequestContext{
		Version: 1, Issuer: "ai-orchestrator", Audience: audience,
		RequestID: "eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee", RunID: "ffffffff-ffff-4fff-8fff-ffffffffffff",
		UserID: authzUserID, SessionID: authzSessionID, TenantID: authzTenantID, ClusterID: "dddddddd-dddd-4ddd-8ddd-dddddddddddd",
		Source: "planner", Capability: "kubernetes.read", IssuedAt: now, ExpiresAt: now.Add(30 * time.Second), Nonce: "11111111-1111-4111-8111-111111111111",
	}
}

func expectRequestIdentityAndTenant(mock sqlmock.Sqlmock) {
	mock.ExpectQuery("SELECT u.user_uuid, u.status, s.status, s.expires_at, s.revoked_at FROM users u JOIN user_sessions s").
		WithArgs(authzUserID, authzSessionID).
		WillReturnRows(sqlmock.NewRows([]string{"user_uuid", "user_status", "session_status", "expires_at", "revoked_at"}).
			AddRow(authzUserID, 1, "active", time.Now().Add(time.Hour), nil))
	mock.ExpectQuery("SELECT t.id FROM tenants t JOIN user_tenants ut").
		WithArgs(authzUserID, authzTenantID).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(authzTenantID))
}

func expectAuthorizationCluster(mock sqlmock.Sqlmock, ref string) {
	mock.ExpectQuery("SELECT id, cluster_id, tenant_id, slug, name, environment, region, credential_ref, lifecycle_status").
		WithArgs(authzTenantID, ref).
		WillReturnRows(sqlmock.NewRows([]string{"id", "cluster_id", "tenant_id", "slug", "name", "environment", "region", "credential_ref", "lifecycle_status", "created_at", "updated_at"}).
			AddRow(int64(1), "dddddddd-dddd-4ddd-8ddd-dddddddddddd", authzTenantID, "production", "orders", "prod", "cn", "secret://orders", "ready", nil, nil))
}
