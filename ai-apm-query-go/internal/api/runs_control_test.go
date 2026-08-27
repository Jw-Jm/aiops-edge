package api

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestPublicCancelRun(t *testing.T) {
	h, mock, cleanup := newTestRunsHandler()
	defer cleanup()
	// runDAO.Get → run（created）——handler 预检 tenant
	mock.ExpectQuery(regexp.QuoteMeta("SELECT run_id, request_id")).
		WillReturnRows(airunMockRows("run-1", "created", 0))
	// P0#1：RunControlService.CancelTx（无 command_id）：
	// Begin → SELECT status,state_version FOR UPDATE (created,0) →
	//   UPDATE cancelled + lease_epoch++ (1) →
	//   AppendTx event (SELECT empty → UPDATE last_event_sequence → INSERT event) → Commit
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta("SELECT status, state_version FROM ai_runs WHERE run_id = ? FOR UPDATE")).
		WillReturnRows(sqlmock.NewRows([]string{"status", "state_version"}).AddRow("created", 0))
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
	mock.ExpectCommit()
	// Get updated → cancelled
	mock.ExpectQuery(regexp.QuoteMeta("SELECT run_id, request_id")).
		WillReturnRows(airunMockRows("run-1", "cancelled", 1))

	req := httptest.NewRequest(http.MethodPost, "/api/v1/ai/runs/run-1/cancel", nil)
	req = withAuthorizationContext(req, AuthorizationContext{
		UserID:    "91480408-9c2d-11f1-8271-bea176fe9f9f",
		TenantID:  "7ed01afc-cc79-4ecd-8767-a2befa6168ad",
		SessionID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
	})
	rec := httptest.NewRecorder()
	h.PublicCancelRun(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations not met: %v", err)
	}
}

func TestPublicCancelRunRejectsUnauthenticated(t *testing.T) {
	h, _, cleanup := newTestRunsHandler()
	defer cleanup()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/ai/runs/run-1/cancel", nil)
	rec := httptest.NewRecorder()
	h.PublicCancelRun(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", rec.Code)
	}
}

func TestPublicCancelRunRejectsCrossTenant(t *testing.T) {
	h, mock, cleanup := newTestRunsHandler()
	defer cleanup()
	mock.ExpectQuery(regexp.QuoteMeta("SELECT run_id, request_id")).
		WillReturnRows(airunMockRows("run-1", "created", 0))
	req := httptest.NewRequest(http.MethodPost, "/api/v1/ai/runs/run-1/cancel", nil)
	req = withAuthorizationContext(req, AuthorizationContext{
		UserID: "91480408-9c2d-11f1-8271-bea176fe9f9f", TenantID: "other-tenant",
	})
	rec := httptest.NewRecorder()
	h.PublicCancelRun(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", rec.Code)
	}
}
