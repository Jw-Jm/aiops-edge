package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/observability-platform/ai-apm-query-go/internal/store"
)

func TestControlPlaneRecoverySnapshot(t *testing.T) {
	c := newCPHandler(t)
	mock, cleanup := setupAPIStore(t)
	defer cleanup()
	// 1) runDAO.Get（authorizeControlPlaneForRun 的 tenant 绑定）
	mock.ExpectQuery(regexp.QuoteMeta("SELECT run_id, request_id")).
		WillReturnRows(airunMockRows("run-1", "planning", 1))
	// 2) recoverySnapshot 开一致性读事务
	mock.ExpectBegin()
	// 事务内 GetTx(run)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT run_id, request_id")).
		WillReturnRows(airunMockRows("run-1", "planning", 1))
	// planDAO.ListByRunTx
	mock.ExpectQuery(regexp.QuoteMeta("SELECT step_id, run_id, parent_step_id")).
		WillReturnRows(sqlmock.NewRows([]string{"step_id", "run_id", "parent_step_id", "seq",
			"step_type", "status", "cluster_id", "description", "budget_used", "depends_on",
			"parameters", "attempt", "outcome", "result_ref", "started_at", "completed_at",
			"created_at", "updated_at"}).
			AddRow("s1", "run-1", nil, 1, "tool", "success", nil, "d", 1,
				[]byte(`["s0"]`), []byte(`{}`), 1, "success", "ev-1", time.Now(), time.Now(),
				time.Now(), time.Now()))
	// toolDAO.ListByRunTx
	mock.ExpectQuery(regexp.QuoteMeta("SELECT tool_run_id, run_id, step_id")).
		WillReturnRows(sqlmock.NewRows([]string{"tool_run_id", "run_id", "step_id", "tenant_id",
			"cluster_id", "tool_name", "status", "input_json", "result_json", "error_code",
			"error_message", "duration_ms", "started_at", "completed_at", "created_at",
			"idempotency_key"}).
			AddRow("t1", "run-1", "s1", "7ed01afc-cc79-4ecd-8767-a2befa6168ad",
				"91771a6e-9c2d-11f1-8271-bea176fe9f9f", "metrics", "success",
				[]byte(`{}`), []byte(`{}`), nil, nil, 100, time.Now(), time.Now(), time.Now(), "k1"))
	// actionDAO.ListByRunTx
	mock.ExpectQuery(regexp.QuoteMeta("SELECT action_id, run_id")).
		WillReturnRows(sqlmock.NewRows([]string{"action_id", "run_id", "tenant_id", "cluster_id",
			"action_type", "action_hash", "idempotency_key", "proposed_risk",
			"authoritative_risk", "status", "dry_run", "params_json", "result_json",
			"created_at", "updated_at"}).
			AddRow("a1", "run-1", "t", "c", "restart", "abc", "k2", "R2", "R3", "approved",
				1, []byte(`{}`), []byte(`{}`), time.Now(), time.Now()))
	// cmdDAO.ListByRunTx
	mock.ExpectQuery(regexp.QuoteMeta("SELECT command_id, run_id, operation")).
		WillReturnRows(sqlmock.NewRows([]string{"command_id", "run_id", "operation",
			"payload_json", "status", "idempotency_key", "created_at"}).
			AddRow("cmd-1", "run-1", "cancel", []byte(`{}`), "pending", "k3", time.Now()))
	// approvalDAO.ListByRunTx（Phase D）
	mock.ExpectQuery(regexp.QuoteMeta("SELECT approval_id, run_id, action_id")).
		WillReturnRows(sqlmock.NewRows([]string{"approval_id", "run_id", "action_id",
			"action_hash", "tenant_id", "cluster_id", "decision", "approver", "reason",
			"decided_at", "created_at"}).
			AddRow("ap-1", "run-1", "a1", "abc", "t", "c", "approved", "admin", "ok",
				time.Now(), time.Now()))
	// eventDAO.LastSequenceTx
	mock.ExpectQuery(regexp.QuoteMeta("SELECT last_event_sequence FROM ai_runs")).
		WillReturnRows(sqlmock.NewRows([]string{"last_event_sequence"}).AddRow(int64(2)))
	mock.ExpectCommit()

	c.h.planDAO = &store.AIPlanStepDAO{}
	c.h.toolDAO = &store.AIToolRunDAO{}
	c.h.actionDAO = &store.AIActionDAO{}
	c.h.cmdDAO = &store.AIControlCommandDAO{}

	req := c.cpReq(t, http.MethodGet, "/internal/v1/control-plane/recovery/snapshot?run_id=run-1",
		"control_plane.runs.recover", "", nil)
	rec := httptest.NewRecorder()
	c.h.InternalControlPlaneRecovery(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var snap map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &snap); err != nil {
		t.Fatal(err)
	}
	if snap["run"] == nil || snap["last_event_sequence"] == nil {
		t.Fatalf("snapshot incomplete: %v", snap)
	}
	steps, _ := snap["plan_steps"].([]interface{})
	if len(steps) != 1 {
		t.Fatalf("expected 1 plan_step, got %v", steps)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations not met: %v", err)
	}
}
