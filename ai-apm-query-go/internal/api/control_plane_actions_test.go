package api

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestControlPlaneActionAppend(t *testing.T) {
	c := newCPHandler(t)
	mock, cleanup := setupAPIStore(t)
	defer cleanup()
	// runDAO.Get（authorizeControlPlaneForRun）
	mock.ExpectQuery(regexp.QuoteMeta("SELECT run_id, request_id")).
		WillReturnRows(airunMockRows("run-1", "planning", 1))
	// actionDAO.Create
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO ai_actions")).
		WillReturnResult(sqlmock.NewResult(1, 1))

	req := c.cpReq(t, http.MethodPost, "/internal/v1/control-plane/runs/run-1/actions",
		"control_plane.runs.mutate",
		`{"action_id":"act-1","action_type":"restart","action_hash":"abc123","idempotency_key":"k1","proposed_risk":"R2","authoritative_risk":"R3","status":"proposed","dry_run":true,"params":{"x":1}}`,
		nil)
	rec := httptest.NewRecorder()
	c.h.InternalControlPlaneRunRouter(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations not met: %v", err)
	}
}

func TestControlPlaneApprovalAppend(t *testing.T) {
	c := newCPHandler(t)
	mock, cleanup := setupAPIStore(t)
	defer cleanup()
	mock.ExpectQuery(regexp.QuoteMeta("SELECT run_id, request_id")).
		WillReturnRows(airunMockRows("run-1", "planning", 1))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO ai_approval_decisions")).
		WillReturnResult(sqlmock.NewResult(1, 1))

	req := c.cpReq(t, http.MethodPost, "/internal/v1/control-plane/runs/run-1/approvals",
		"control_plane.runs.mutate",
		`{"approval_id":"ap-1","action_id":"act-1","action_hash":"abc123","decision":"approved","approver":"admin","reason":"ok"}`,
		nil)
	rec := httptest.NewRecorder()
	c.h.InternalControlPlaneRunRouter(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations not met: %v", err)
	}
}
