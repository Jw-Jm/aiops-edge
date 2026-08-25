package api

import (
	"crypto/ed25519"
	"crypto/rand"
	"database/sql"
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
	c.h.cmdDAO = &store.AIControlCommandDAO{}
	// 事务化：Begin → GetTx(command 幂等，无行) → GetTx(run) → TransitionTx → GetTx(updated) → UpsertDoneTx → Commit
	mock.ExpectBegin()
	// command 幂等检查（GetTx，无行）
	mock.ExpectQuery(regexp.QuoteMeta("SELECT command_id, run_id, operation")).
		WillReturnRows(sqlmock.NewRows([]string{"command_id", "run_id", "operation",
			"payload_json", "payload_hash", "response_json", "status", "idempotency_key",
			"completed_at", "created_at"}))
	// GetTx(run)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT run_id, request_id")).
		WillReturnRows(airunMockRows("run-1", "created", 0))
	// TransitionTx ok
	mock.ExpectExec(regexp.QuoteMeta("UPDATE ai_runs SET status")).
		WillReturnResult(sqlmock.NewResult(0, 1))
	// GetTx(updated)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT run_id, request_id")).
		WillReturnRows(airunMockRows("run-1", "planning", 1))
	// UpsertDoneTx
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO ai_control_commands")).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

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

func TestControlPlaneRunTransitionMissingVersion(t *testing.T) {
	c := newCPHandler(t)
	_, cleanup := setupAPIStore(t)
	defer cleanup()
	c.h.cmdDAO = &store.AIControlCommandDAO{}
	// expected_version 缺失 → 400（fail-closed，不执行任何 SQL）
	req := c.cpReq(t, http.MethodPost, "/internal/v1/control-plane/runs/run-1/transition",
		"control_plane.runs.mutate", `{"target":"planning","command_id":"cmd-1"}`, nil)
	rec := httptest.NewRecorder()
	c.h.InternalControlPlaneRunRouter(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 missing expected_version, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestControlPlaneRunTransitionCASConflict(t *testing.T) {
	c := newCPHandler(t)
	mock, cleanup := setupAPIStore(t)
	defer cleanup()
	c.h.cmdDAO = &store.AIControlCommandDAO{}
	// 事务化（无 command_id → 跳过幂等检查）：GetTx(run, created, v5) → TransitionTx CAS 冲突(0 rows)
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta("SELECT run_id, request_id")).
		WillReturnRows(airunMockRows("run-1", "created", 5))
	mock.ExpectExec(regexp.QuoteMeta("UPDATE ai_runs SET status")).
		WillReturnResult(sqlmock.NewResult(0, 0)) // CAS 冲突
	mock.ExpectRollback()

	req := c.cpReq(t, http.MethodPost, "/internal/v1/control-plane/runs/run-1/transition",
		"control_plane.runs.mutate", `{"expected_version":5,"target":"planning"}`, nil)
	rec := httptest.NewRecorder()
	c.h.InternalControlPlaneRunRouter(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestControlPlaneRunUnfinishedRequiresGlobalCapability(t *testing.T) {
	c := newCPHandler(t)
	_, cleanup := setupAPIStore(t)
	defer cleanup()
	// 用单 Run recover capability 请求全局 unfinished → 403（F-18：全局扫描需独立 system capability）。
	req := c.cpReq(t, http.MethodGet, "/internal/v1/control-plane/runs/unfinished",
		"control_plane.runs.recover", "", nil)
	rec := httptest.NewRecorder()
	c.h.InternalControlPlaneRunRouter(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 with wrong capability, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestControlPlaneRunUnfinishedGlobalCapability(t *testing.T) {
	c := newCPHandler(t)
	mock, cleanup := setupAPIStore(t)
	defer cleanup()
	// 正确 capability + ScanRecoveryCandidates（A2-02）返回空列表（sqlmock 需匹配查询）。
	mock.ExpectQuery(regexp.QuoteMeta("SELECT run_id, lease_owner_id")).
		WillReturnRows(sqlmock.NewRows([]string{"run_id", "lease_owner_id", "lease_epoch",
			"lease_claim_id", "lease_token_hash", "lease_expires_at", "runtime_wait_kind",
			"retry_not_before", "retry_attempt"}))
	req := c.cpReq(t, http.MethodGet, "/internal/v1/control-plane/runs/unfinished?limit=10",
		"control_plane.runs.recover.global", "", nil)
	rec := httptest.NewRecorder()
	c.h.InternalControlPlaneRunRouter(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestControlPlaneRunCancelCAS(t *testing.T) {
	c := newCPHandler(t)
	mock, cleanup := setupAPIStore(t)
	defer cleanup()
	c.h.cmdDAO = &store.AIControlCommandDAO{}
	c.h.eventDAO = &store.AIRunEventDAO{}
	// P0#1：RunControlService.CancelTx（统一事务）：
	// handler 预检 Get(run-1 planning v0) → Begin →
	//   cmdDAO.GetTx(command empty) →
	//   SELECT status,state_version FOR UPDATE (planning,0) →
	//   UPDATE cancelled + lease_epoch++ (1) →
	//   AppendTx event (SELECT sequence empty → UPDATE last_event_sequence → INSERT event) →
	//   cmdDAO.CreateTx (INSERT command) → Commit
	mock.ExpectQuery(regexp.QuoteMeta("SELECT run_id, request_id")).
		WillReturnRows(airunMockRows("run-1", "planning", 0))
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta("SELECT command_id, run_id, operation")).
		WillReturnRows(sqlmock.NewRows([]string{"command_id", "run_id", "operation",
			"payload_json", "payload_hash", "response_json", "status", "idempotency_key",
			"completed_at", "created_at"}))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT status, state_version FROM ai_runs WHERE run_id = ? FOR UPDATE")).
		WillReturnRows(sqlmock.NewRows([]string{"status", "state_version"}).AddRow("planning", 0))
	mock.ExpectExec(regexp.QuoteMeta("UPDATE ai_runs SET status = 'cancelled'")).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT sequence FROM ai_run_events")).
		WillReturnError(sql.ErrNoRows)
	mock.ExpectExec(regexp.QuoteMeta("UPDATE ai_runs SET last_event_sequence")).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT last_event_sequence FROM ai_runs")).
		WillReturnRows(sqlmock.NewRows([]string{"last_event_sequence"}).AddRow(1))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO ai_run_events")).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO ai_control_commands")).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	req := c.cpReq(t, http.MethodPost, "/internal/v1/control-plane/runs/run-1/cancel",
		"control_plane.runs.mutate", `{"expected_version":0,"command_id":"cancel-1"}`, nil)
	rec := httptest.NewRecorder()
	c.h.InternalControlPlaneRunRouter(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations not met: %v", err)
	}
}

func TestControlPlaneRunCancelMissingVersion(t *testing.T) {
	c := newCPHandler(t)
	_, cleanup := setupAPIStore(t)
	defer cleanup()
	c.h.cmdDAO = &store.AIControlCommandDAO{}
	// expected_version 缺失 → 400（fail-closed，不执行 SQL / 不"读当前 version 当 caller expected"）
	req := c.cpReq(t, http.MethodPost, "/internal/v1/control-plane/runs/run-1/cancel",
		"control_plane.runs.mutate", `{"command_id":"cancel-1"}`, nil)
	rec := httptest.NewRecorder()
	c.h.InternalControlPlaneRunRouter(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 missing expected_version, got %d: %s", rec.Code, rec.Body.String())
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
