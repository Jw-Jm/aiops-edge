package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/observability-platform/ai-apm-query-go/internal/store"
)

func newTestRunsHandler() (*Handler, sqlmock.Sqlmock, func()) {
	db, mock, err := sqlmock.New()
	if err != nil {
		panic(err)
	}
	prev := store.GetDB()
	store.SetDB(db)
	h := &Handler{
		runDAO:    &store.AIRunDAO{},
		outboxDAO: &store.AIRunOutboxDAO{},
	}
	cleanup := func() {
		db.Close()
		store.SetDB(prev)
	}
	return h, mock, cleanup
}

func authRunRequest(t *testing.T, body interface{}) *http.Request {
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatal(err)
		}
	}
	r := httptest.NewRequest(http.MethodPost, "/api/v1/ai/runs", &buf)
	r = withAuthorizationContext(r, AuthorizationContext{
		UserID:    "91480408-9c2d-11f1-8271-bea176fe9f9f",
		SessionID: "session-1",
		TenantID:  "7ed01afc-cc79-4ecd-8767-a2befa6168ad",
	})
	return r
}

func TestCreateRunPublic(t *testing.T) {
	h, mock, cleanup := newTestRunsHandler()
	defer cleanup()
	// P1-1：cluster 租户归属校验（ClusterDAO.GetByClusterID）
	mock.ExpectQuery(regexp.QuoteMeta("SELECT id, cluster_id, tenant_id, slug")).
		WillReturnRows(sqlmock.NewRows([]string{"id", "cluster_id", "tenant_id", "slug", "name",
			"environment", "region", "credential_ref", "lifecycle_status",
			"kubernetes_identity_uid", "created_at", "updated_at"}).
			AddRow(1, "91771a6e-9c2d-11f1-8271-bea176fe9f9f", "7ed01afc-cc79-4ecd-8767-a2befa6168ad",
				"p65", "p65", "prod", "cn", nil, "active", nil, time.Now(), time.Now()))
	// P0-5：CreateWithOutbox 同事务
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO ai_runs")).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO ai_run_outbox")).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	req := authRunRequest(t, map[string]interface{}{
		"tenant_id":   "7ed01afc-cc79-4ecd-8767-a2befa6168ad",
		"cluster_id":  "91771a6e-9c2d-11f1-8271-bea176fe9f9f",
		"intent":      "investigate",
		"action_mode": "read_only",
	})
	w := httptest.NewRecorder()
	h.CreateRunPublic(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp["status"] != "created" || resp["run_id"] == "" || resp["request_id"] == "" {
		t.Fatalf("bad resp: %v", resp)
	}
}

func TestCreateRunPublicRejectsTenantMismatch(t *testing.T) {
	h, mock, cleanup := newTestRunsHandler()
	defer cleanup()
	// 不产生 DB 调用（先拒绝）
	req := authRunRequest(t, map[string]interface{}{
		"tenant_id": "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee", // 与 JWT tenant 不一致
	})
	w := httptest.NewRecorder()
	h.CreateRunPublic(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", w.Code, w.Body.String())
	}
	if mock.ExpectationsWereMet() != nil {
		// 无 DB 期望应满足
	}
}

func TestCreateRunPublicRejectsUnauthenticated(t *testing.T) {
	h, _, cleanup := newTestRunsHandler()
	defer cleanup()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/ai/runs", nil)
	w := httptest.NewRecorder()
	h.CreateRunPublic(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", w.Code)
	}
}

func TestCreateRunPublicExistingOnDuplicate(t *testing.T) {
	h, mock, cleanup := newTestRunsHandler()
	defer cleanup()
	// P1-1：cluster 租户归属校验
	mock.ExpectQuery(regexp.QuoteMeta("SELECT id, cluster_id, tenant_id, slug")).
		WillReturnRows(sqlmock.NewRows([]string{"id", "cluster_id", "tenant_id", "slug", "name",
			"environment", "region", "credential_ref", "lifecycle_status",
			"kubernetes_identity_uid", "created_at", "updated_at"}).
			AddRow(1, "91771a6e-9c2d-11f1-8271-bea176fe9f9f", "7ed01afc-cc79-4ecd-8767-a2befa6168ad",
				"p65", "p65", "prod", "cn", nil, "active", nil, time.Now(), time.Now()))
	// CreateWithOutbox：事务内 ai_runs 唯一键冲突 → existing
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO ai_runs")).
		WillReturnError(errors.New("Error 1062: Duplicate entry 'x' for key 'uq_ai_runs_tenant_request'"))

	req := authRunRequest(t, map[string]interface{}{
		"tenant_id":  "7ed01afc-cc79-4ecd-8767-a2befa6168ad",
		"cluster_id": "91771a6e-9c2d-11f1-8271-bea176fe9f9f",
		"intent":     "investigate",
	})
	w := httptest.NewRecorder()
	h.CreateRunPublic(w, req)
	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", w.Code, w.Body.String())
	}
}

func TestCreateRunPublicRejectsEmptyCluster(t *testing.T) {
	h, _, cleanup := newTestRunsHandler()
	defer cleanup()
	// P1-1：空 cluster → 422（非法 multi-cluster scope），无 DB 调用。
	req := authRunRequest(t, map[string]interface{}{
		"tenant_id": "7ed01afc-cc79-4ecd-8767-a2befa6168ad",
	})
	w := httptest.NewRecorder()
	h.CreateRunPublic(w, req)
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d: %s", w.Code, w.Body.String())
	}
}
