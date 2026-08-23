package api

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestPublicCancelRun(t *testing.T) {
	h, mock, cleanup := newTestRunsHandler()
	defer cleanup()
	// runDAO.Get → run（created）
	mock.ExpectQuery(regexp.QuoteMeta("SELECT run_id, request_id")).
		WillReturnRows(airunMockRows("run-1", "created", 0))
	// Cancel ok
	mock.ExpectExec(regexp.QuoteMeta("UPDATE ai_runs SET status = 'cancelled'")).
		WillReturnResult(sqlmock.NewResult(0, 1))
	// Get updated → cancelled
	mock.ExpectQuery(regexp.QuoteMeta("SELECT run_id, request_id")).
		WillReturnRows(airunMockRows("run-1", "cancelled", 1))

	req := httptest.NewRequest(http.MethodPost, "/api/v1/ai/runs/run-1/cancel", nil)
	req = withAuthorizationContext(req, AuthorizationContext{
		UserID: "91480408-9c2d-11f1-8271-bea176fe9f9f",
		TenantID: "7ed01afc-cc79-4ecd-8767-a2befa6168ad",
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
