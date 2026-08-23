package api

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	trustedauth "github.com/observability-platform/ai-apm-query-go/internal/auth"
	"github.com/observability-platform/ai-apm-query-go/internal/contract"
	"github.com/observability-platform/ai-apm-query-go/internal/query"
	"github.com/observability-platform/ai-apm-query-go/internal/store"
)

// internalQueryTestCtx 保存 internal query 测试的 verifier 与签名私钥。
type internalQueryTestCtx struct {
	h    *Handler
	priv ed25519.PrivateKey
}

// newInternalQueryTestHandler 构造带 strict internal verifier 与全 repository 的 handler。
func newInternalQueryTestHandler(t *testing.T, chDispatch map[string]string) *internalQueryTestCtx {
	t.Helper()

	// strict internal verifier：service-token + EdDSA TrustedRequestContext。
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	pub := priv.Public().(ed25519.PublicKey)
	restore := configureInternalRequestVerifier(trustedauth.VerifyConfig{
		Issuer:       "ai-orchestrator",
		Audience:     "ai-apm-query-go",
		PublicKeys:   map[string]ed25519.PublicKey{trustedauth.KeyID(pub): pub},
		ServiceToken: "test-service-token",
		ReplayCache:  trustedauth.NewReplayCache(100),
		ClockSkew:    30 * time.Second,
	})
	t.Cleanup(restore)

	// P1.1 作用域授权：mock DB 返回 tenant_clusters 归属（testClusterID → authzTenantID）。
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	prevDB := store.GetDB()
	store.SetDB(db)
	t.Cleanup(func() { store.SetDB(prevDB) })
	// 默认 testClusterID → authzTenantID（授权）；其它 cluster → 空（未授权，P1.1 403）。
	mock.ExpectQuery("SELECT tenant_id FROM tenant_clusters WHERE cluster_id = \\?").
		WithArgs(testClusterID).
		WillReturnRows(sqlmock.NewRows([]string{"tenant_id"}).AddRow(authzTenantID))

	// repository：mock ClickHouse。
	h := &Handler{client: &http.Client{}}
	chSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		q := r.URL.Query().Get("query")
		if q == "" { // ClickHouseRepo.Query 走 POST body；QueryJSON 走 URL query。
			if b, err := io.ReadAll(r.Body); err == nil {
				q = string(b)
			}
		}
		for needle, out := range chDispatch {
			if strings.Contains(q, needle) {
				_, _ = w.Write([]byte(out))
				return
			}
		}
		_, _ = w.Write([]byte(""))
	}))
	t.Cleanup(chSrv.Close)
	host, port := splitHostPort(chSrv.URL)
	h.repo = *query.NewClickHouseRepo(fmt.Sprintf("http://%s:%d", host, port), &http.Client{Timeout: 5 * time.Second})
	h.metricsRepo = query.NewMetricsRepository(&h.repo)
	h.logRepo = query.NewLogRepository(&h.repo, nil, query.NewSourceRouter(query.ModeLegacy))
	h.traceRepo = query.NewTraceRepository(&h.repo)
	h.alertRepo = query.NewAlertRepository(&h.repo)
	h.topoRepo = query.NewTopologyRepository(&h.repo)
	h.resourceRepo = query.NewResourceRepository(&h.repo)
	h.changeRepo = query.NewChangeRepository(&h.repo)
	return &internalQueryTestCtx{h: h, priv: priv}
}

// trustedRequest 构造一个指向指定路径/tenant/cluster/capability 的签名 internal 请求。
func (c *internalQueryTestCtx) trustedRequest(t *testing.T, method, path string, body string) *http.Request {
	t.Helper()
	return c.signedRequest(t, method, path, body, func(ctx *contract.TrustedRequestContext) {})
}

func (c *internalQueryTestCtx) signedRequest(t *testing.T, method, path, body string, mutate func(*contract.TrustedRequestContext)) *http.Request {
	t.Helper()
	now := time.Now().UTC()
	ctx := contract.NewTrustedRequestContext(
		"ai-orchestrator", "ai-apm-query-go", "eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee", "user", authzUserID, authzSessionID,
		authzTenantID, "ffffffff-ffff-4fff-8fff-ffffffffffff", "cluster", testClusterID,
		"observability.metrics.read", "planner", now, now.Add(30*time.Second), "11111111-1111-4111-8111-111111111111",
	)
	if mutate != nil {
		mutate(&ctx)
	}
	token, err := trustedauth.SignTrustedRequestContextV2(ctx, c.priv)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Internal-Token", "test-service-token")
	req.Header.Set("X-Trusted-Request-Context", token)
	return req
}

const testClusterID = "dddddddd-dddd-4ddd-8ddd-dddddddddddd"

// TestInternalQueryUnauthorizedClusterScope 验证 P1.1：cluster 未注册/不属于该 tenant → 403
// TENANT_ACCESS_DENIED（不是 NO_DATA），证明身份边界。
func TestInternalQueryUnauthorizedClusterScope(t *testing.T) {
	c := newInternalQueryTestHandler(t, nil)
	// 用未注册 cluster（不在 tenant_clusters 归属）→ internalScopeAuthorized=false → 403
	req := c.signedRequest(t, http.MethodPost, "/internal/v1/query/metrics", `{"service":"checkout","minutes":60}`, func(ctx *contract.TrustedRequestContext) {
		ctx.ClusterID = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa" // 未注册 cluster
	})
	rec := httptest.NewRecorder()
	c.h.InternalQueryMetrics(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 on unauthorized cluster scope, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "TENANT_ACCESS_DENIED") {
		t.Fatalf("expected TENANT_ACCESS_DENIED, got %s", rec.Body.String())
	}
}

// TestInternalQueryUnauthorizedTenantScope 验证 P1.1：tenant 不属于该 cluster → 403。
func TestInternalQueryUnauthorizedTenantScope(t *testing.T) {
	c := newInternalQueryTestHandler(t, nil)
	req := c.signedRequest(t, http.MethodPost, "/internal/v1/query/metrics", `{"service":"checkout","minutes":60}`, func(ctx *contract.TrustedRequestContext) {
		ctx.TenantID = "0ac2c45e-cebc-5ab3-ad84-a54f1ee0a0aa" // 非 canonical/未授权 tenant
	})
	rec := httptest.NewRecorder()
	c.h.InternalQueryMetrics(rec, req)
	// tenant_clusters 归属返回 authzTenantID ≠ 该 tenant → 403
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 on unauthorized tenant scope, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestInternalQueryChangesSuccess(t *testing.T) {
	rows := "" +
		`{"change_id":"ch-1","service_name":"checkout","change_type":"deploy","start_time":"2026-08-20 10:00:00","actor":"alice","summary":"v1.2.3","revision":"abc123"}` + "\n"
	c := newInternalQueryTestHandler(t, map[string]string{"FROM observability.change_records": rows})
	// capability 需与 changes.read 一致
	req := c.signedRequest(t, http.MethodPost, "/internal/v1/query/changes", `{"service":"checkout"}`, func(ctx *contract.TrustedRequestContext) {
		ctx.Capability = "changes.read"
	})
	rec := httptest.NewRecorder()
	c.h.InternalQueryChanges(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if total, _ := resp["total"].(float64); int(total) != 1 {
		t.Fatalf("expected total=1, got %v", resp["total"])
	}
}

func TestInternalQueryJWTOnlyRejected(t *testing.T) {
	c := newInternalQueryTestHandler(t, nil)
	// JWT-only（无 TrustedRequestContext）→ 拒绝，不能走 JWT fallback。
	req := httptest.NewRequest(http.MethodPost, "/internal/v1/query/changes", strings.NewReader(`{"service":"checkout"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+generateJWT(authzUserID, "admin", `{"services":["*"]}`))
	rec := httptest.NewRecorder()
	c.h.InternalQueryChanges(rec, req)
	if rec.Code == http.StatusOK {
		t.Fatalf("JWT-only on internal route must NOT be accepted, got 200")
	}
}

func TestInternalQueryMissingContextRejected(t *testing.T) {
	c := newInternalQueryTestHandler(t, nil)
	req := httptest.NewRequest(http.MethodPost, "/internal/v1/query/changes", strings.NewReader(`{"service":"checkout"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Internal-Token", "test-service-token") // service token but NO trusted context
	rec := httptest.NewRecorder()
	c.h.InternalQueryChanges(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without trusted context, got %d", rec.Code)
	}
}

func TestInternalQueryExpiredContextRejected(t *testing.T) {
	c := newInternalQueryTestHandler(t, nil)
	// 用一个 now 派生 IssuedAt/ExpiresAt，避免 lifetime=60s+δ 越界使签名失败（flaky 根因）。
	req := c.signedRequest(t, http.MethodPost, "/internal/v1/query/changes", `{"service":"checkout"}`, func(ctx *contract.TrustedRequestContext) {
		ctx.Capability = "changes.read"
		expiredNow := time.Now().UTC()
		ctx.IssuedAt = expiredNow.Add(-5 * time.Minute)
		ctx.ExpiresAt = expiredNow.Add(-4 * time.Minute)
	})
	rec := httptest.NewRecorder()
	c.h.InternalQueryChanges(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 on expired context, got %d", rec.Code)
	}
}

func TestInternalQueryWrongAudienceRejected(t *testing.T) {
	c := newInternalQueryTestHandler(t, nil)
	req := c.signedRequest(t, http.MethodPost, "/internal/v1/query/changes", `{"service":"checkout"}`, func(ctx *contract.TrustedRequestContext) {
		ctx.Capability = "changes.read"
		ctx.Audience = "another-api"
	})
	rec := httptest.NewRecorder()
	c.h.InternalQueryChanges(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 on wrong audience, got %d", rec.Code)
	}
}

func TestInternalQueryReplayNonceRejected(t *testing.T) {
	// 首次用某 nonce → 成功（或至少进入 handler）；重复用同一 nonce → CONTEXT_REPLAYED（401）。
	rows := "" +
		`{"change_id":"ch-1","service_name":"checkout","change_type":"deploy","start_time":"2026-08-20 10:00:00","actor":"alice","summary":"v1.2.3","revision":"abc123"}` + "\n"
	// 重建 handler 让 dispatch 带数据，且 verifier 的 ReplayCache 独立。
	c2 := newInternalQueryTestHandler(t, map[string]string{"FROM observability.change_records": rows})
	first := c2.signedRequest(t, http.MethodPost, "/internal/v1/query/changes", `{"service":"checkout"}`, func(ctx *contract.TrustedRequestContext) {
		ctx.Capability = "changes.read"
		ctx.Nonce = "22222222-2222-4222-8222-222222222222"
	})
	rec1 := httptest.NewRecorder()
	c2.h.InternalQueryChanges(rec1, first)
	if rec1.Code != http.StatusOK {
		t.Fatalf("first nonce should pass, got %d: %s", rec1.Code, rec1.Body.String())
	}
	// 同一 nonce 重放 → 401 CONTEXT_REPLAYED。
	replay := c2.signedRequest(t, http.MethodPost, "/internal/v1/query/changes", `{"service":"checkout"}`, func(ctx *contract.TrustedRequestContext) {
		ctx.Capability = "changes.read"
		ctx.Nonce = "22222222-2222-4222-8222-222222222222"
	})
	rec2 := httptest.NewRecorder()
	c2.h.InternalQueryChanges(rec2, replay)
	if rec2.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 on replayed nonce, got %d: %s", rec2.Code, rec2.Body.String())
	}
}

func TestInternalQueryTenantMismatchScopeMismatch(t *testing.T) {
	c := newInternalQueryTestHandler(t, nil)
	// body tenant 与 trusted context（authzTenantID）不一致 → CONTEXT_SCOPE_MISMATCH（409）。
	req := c.signedRequest(t, http.MethodPost, "/internal/v1/query/changes", `{"tenant_id":"99999999-9999-4999-8999-999999999999","service":"checkout"}`, func(ctx *contract.TrustedRequestContext) {
		ctx.Capability = "changes.read"
	})
	rec := httptest.NewRecorder()
	c.h.InternalQueryChanges(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409 CONTEXT_SCOPE_MISMATCH on tenant mismatch, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestInternalQueryClusterMismatchScopeMismatch(t *testing.T) {
	c := newInternalQueryTestHandler(t, nil)
	// body cluster 与 trusted context（testClusterID）不一致 → CONTEXT_SCOPE_MISMATCH（409）。
	req := c.signedRequest(t, http.MethodPost, "/internal/v1/query/changes", `{"cluster_id":"99999999-9999-4999-8999-999999999999","service":"checkout"}`, func(ctx *contract.TrustedRequestContext) {
		ctx.Capability = "changes.read"
	})
	rec := httptest.NewRecorder()
	c.h.InternalQueryChanges(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409 CONTEXT_SCOPE_MISMATCH on cluster mismatch, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestInternalQueryRunScopeRejected(t *testing.T) {
	c := newInternalQueryTestHandler(t, nil)
	// internal query 要求 cluster scope；run scope（ScopeKind=run）→ INVALID_CONTEXT（401）。
	req := c.signedRequest(t, http.MethodPost, "/internal/v1/query/changes", `{"service":"checkout"}`, func(ctx *contract.TrustedRequestContext) {
		ctx.Capability = "control_plane.run.read"
		ctx.ScopeKind = "run"
		ctx.ClusterID = ""
	})
	rec := httptest.NewRecorder()
	c.h.InternalQueryChanges(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 on run scope, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestInternalQueryMetricsSuccess(t *testing.T) {
	rows := `{"t":"2026-08-20 10:00:00","call_count":10,"error_count":1,"avg_ms":5.5}` + "\n"
	c := newInternalQueryTestHandler(t, map[string]string{"FROM observability.trace_spans": rows})
	req := c.trustedRequest(t, http.MethodPost, "/internal/v1/query/metrics", `{"service":"checkout","minutes":60}`)
	rec := httptest.NewRecorder()
	c.h.InternalQueryMetrics(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 on metrics, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestInternalQueryUnauthorizedCapability(t *testing.T) {
	c := newInternalQueryTestHandler(t, nil)
	// capability 不匹配 route（changes 需要 changes.read，提供 metrics.read）→ permission_denied。
	req := c.signedRequest(t, http.MethodPost, "/internal/v1/query/changes", `{"service":"checkout"}`, func(ctx *contract.TrustedRequestContext) {
		ctx.Capability = "observability.metrics.read"
	})
	rec := httptest.NewRecorder()
	c.h.InternalQueryChanges(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 on unauthorized capability, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestInternalQueryKnowledgeUnconfigured(t *testing.T) {
	c := newInternalQueryTestHandler(t, nil)
	// knowledgeRepo 后端未配置（nil）→ unavailable（503），绝不回退 ProxyAI。
	req := c.signedRequest(t, http.MethodPost, "/internal/v1/query/knowledge", `{"query":"x"}`, func(ctx *contract.TrustedRequestContext) {
		ctx.Capability = "knowledge.search"
	})
	rec := httptest.NewRecorder()
	c.h.InternalQueryKnowledge(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 when backend unconfigured, got %d: %s", rec.Code, rec.Body.String())
	}
}

// fakeKnowledgeBackend 注入 test 的 knowledge 检索后端。
type fakeKnowledgeBackend struct {
	hits []query.KnowledgeHit
}

func (f *fakeKnowledgeBackend) Search(ctx context.Context, q string, topK int) ([]query.KnowledgeHit, error) {
	return f.hits, nil
}

func TestInternalQueryKnowledgeSuccess(t *testing.T) {
	c := newInternalQueryTestHandler(t, nil)
	c.h.knowledgeRepo = query.NewKnowledgeRepository(&fakeKnowledgeBackend{hits: []query.KnowledgeHit{
		{DocumentID: "doc-1", Source: "runbook", Version: "v3", Similarity: 0.92, Applicability: "checkout"},
	}})
	req := c.signedRequest(t, http.MethodPost, "/internal/v1/query/knowledge", `{"query":"checkout crashloop"}`, func(ctx *contract.TrustedRequestContext) {
		ctx.Capability = "knowledge.search"
	})
	rec := httptest.NewRecorder()
	c.h.InternalQueryKnowledge(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if total, _ := resp["total"].(float64); int(total) != 1 {
		t.Fatalf("expected total=1, got %v", resp["total"])
	}
}
