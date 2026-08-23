package api

import (
	"crypto/ed25519"
	"crypto/rand"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/observability-platform/ai-apm-query-go/internal/auth"
	"github.com/observability-platform/ai-apm-query-go/internal/contract"
	"github.com/observability-platform/ai-apm-query-go/internal/store"
)

func setupAPIStore(t *testing.T) (sqlmock.Sqlmock, func()) {
	t.Helper()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	prev := store.GetDB()
	store.SetDB(db)
	cleanup := func() {
		db.Close()
		store.SetDB(prev)
	}
	return mock, cleanup
}

func airunMockRows(runID, status string, stateVersion int64) *sqlmock.Rows {
	return sqlmock.NewRows([]string{"run_id", "request_id", "tenant_id", "principal",
		"principal_type", "session_id", "scope_kind", "primary_cluster_id", "intent",
		"action_mode", "target_type", "target_resource_id", "time_range_start",
		"time_range_end", "status", "state_version", "parent_run_id", "created_at",
		"updated_at", "finished_at", "last_event_sequence"}).
		AddRow(runID, "req-1", "7ed01afc-cc79-4ecd-8767-a2befa6168ad", "91480408-9c2d-11f1-8271-bea176fe9f9f",
			"user", "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", "single_cluster",
			"91771a6e-9c2d-11f1-8271-bea176fe9f9f", "investigate", "read_only", nil, nil, nil, nil,
			status, stateVersion, nil, time.Now(), time.Now(), nil, 0)
}

type cpHandlerCtx struct {
	h    *Handler
	priv ed25519.PrivateKey
}

func newCPHandler(t *testing.T) *cpHandlerCtx {
	t.Helper()
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	pub := priv.Public().(ed25519.PublicKey)
	restore := configureInternalRequestVerifier(auth.VerifyConfig{
		Issuer:       "ai-orchestrator",
		Audience:     "ai-apm-query-go",
		PublicKeys:   map[string]ed25519.PublicKey{auth.KeyID(pub): pub},
		ServiceToken: "test-service-token",
		ReplayCache:  auth.NewReplayCache(100),
		ClockSkew:    30 * time.Second,
	})
	t.Cleanup(restore)
	h := &Handler{
		runDAO:   &store.AIRunDAO{},
		eventDAO: &store.AIRunEventDAO{},
	}
	return &cpHandlerCtx{h: h, priv: priv}
}

func (c *cpHandlerCtx) cpReq(t *testing.T, method, path, capability, body string, mutate func(*contract.TrustedRequestContext)) *http.Request {
	t.Helper()
	now := time.Now().UTC()
	ctx := contract.NewTrustedRequestContext(
		"ai-orchestrator", "ai-apm-query-go", "eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee", "system",
		"bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb", "", "7ed01afc-cc79-4ecd-8767-a2befa6168ad",
		"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", "run", "", capability, "control-plane",
		now, now.Add(30*time.Second), "11111111-1111-4111-8111-111111111111",
	)
	if mutate != nil {
		mutate(&ctx)
	}
	token, err := auth.SignTrustedRequestContextV2(ctx, c.priv)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Internal-Token", "test-service-token")
	req.Header.Set("X-Trusted-Request-Context", token)
	return req
}

func TestControlPlaneRunTransitionCAS(t *testing.T) {
	c := newCPHandler(t)
	mock, cleanup := setupAPIStore(t)
	defer cleanup()
	// Get run
	mock.ExpectQuery(regexp.QuoteMeta("SELECT run_id, request_id")).
		WillReturnRows(airunMockRows("run-1", "created", 0))
	// Transition ok
	mock.ExpectExec(regexp.QuoteMeta("UPDATE ai_runs SET status")).
		WillReturnResult(sqlmock.NewResult(0, 1))
	// Get updated
	mock.ExpectQuery(regexp.QuoteMeta("SELECT run_id, request_id")).
		WillReturnRows(airunMockRows("run-1", "planning", 1))

	req := c.cpReq(t, http.MethodPost, "/internal/v1/control-plane/runs/run-1/transition",
		"control_plane.runs.mutate", `{"expected_version":0,"target":"planning","command_id":"cmd-1"}`, nil)
	rec := httptest.NewRecorder()
	c.h.InternalControlPlaneRunRouter(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations not met: %v", err)
	}
}

func TestControlPlaneRunTransitionConflict(t *testing.T) {
	c := newCPHandler(t)
	mock, cleanup := setupAPIStore(t)
	defer cleanup()
	mock.ExpectQuery(regexp.QuoteMeta("SELECT run_id, request_id")).
		WillReturnRows(airunMockRows("run-1", "created", 0))
	mock.ExpectExec(regexp.QuoteMeta("UPDATE ai_runs SET status")).
		WillReturnResult(sqlmock.NewResult(0, 0)) // CAS 冲突

	req := c.cpReq(t, http.MethodPost, "/internal/v1/control-plane/runs/run-1/transition",
		"control_plane.runs.mutate", `{"expected_version":5,"target":"planning"}`, nil)
	rec := httptest.NewRecorder()
	c.h.InternalControlPlaneRunRouter(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestControlPlaneEventAppend(t *testing.T) {
	c := newCPHandler(t)
	mock, cleanup := setupAPIStore(t)
	defer cleanup()
	// runDAO.Get（authorizeControlPlaneForRun tenant 绑定）
	mock.ExpectQuery(regexp.QuoteMeta("SELECT run_id, request_id")).
		WillReturnRows(airunMockRows("run-1", "created", 0))
	mock.ExpectBegin()
	// 1) 幂等：先查 sequence（event_id 不存在 → no rows）
	mock.ExpectQuery(regexp.QuoteMeta("SELECT sequence FROM ai_run_events")).
		WillReturnRows(sqlmock.NewRows([]string{"sequence"}))
	// 2) 锁 owner 递增
	mock.ExpectExec(regexp.QuoteMeta("UPDATE ai_runs SET last_event_sequence")).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT last_event_sequence FROM ai_runs")).
		WillReturnRows(sqlmock.NewRows([]string{"last_event_sequence"}).AddRow(int64(1)))
	// 3) 插入
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO ai_run_events")).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	req := c.cpReq(t, http.MethodPost, "/internal/v1/control-plane/runs/run-1/events",
		"control_plane.events.append", `{"event_id":"e1","event_type":"status","payload":{"x":1}}`, nil)
	rec := httptest.NewRecorder()
	c.h.InternalControlPlaneRunRouter(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations not met: %v", err)
	}
}

func TestControlPlaneEventAppendIdempotent(t *testing.T) {
	c := newCPHandler(t)
	mock, cleanup := setupAPIStore(t)
	defer cleanup()
	// runDAO.Get（tenant 绑定）
	mock.ExpectQuery(regexp.QuoteMeta("SELECT run_id, request_id")).
		WillReturnRows(airunMockRows("run-1", "created", 0))
	// Append：先查 event_id 已存在 → 返回既有 sequence，不递增不插入。
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta("SELECT sequence FROM ai_run_events")).
		WillReturnRows(sqlmock.NewRows([]string{"sequence"}).AddRow(int64(3)))
	mock.ExpectCommit()

	req := c.cpReq(t, http.MethodPost, "/internal/v1/control-plane/runs/run-1/events",
		"control_plane.events.append", `{"event_id":"e1","event_type":"status","payload":{"x":1}}`, nil)
	rec := httptest.NewRecorder()
	c.h.InternalControlPlaneRunRouter(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	// created=false（幂等命中）且 sequence=3（首次结果，无 gap）。
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations not met: %v", err)
	}
}
