package api

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/observability-platform/ai-apm-query-go/internal/contract"
	"github.com/observability-platform/ai-apm-query-go/internal/store"
)

func actionReadRows(executionStatus string) *sqlmock.Rows {
	return sqlmock.NewRows([]string{"action_id", "run_id", "tenant_id", "cluster_id", "action_type", "action_hash",
		"idempotency_key", "proposed_risk", "authoritative_risk", "status", "dry_run", "target_name", "target_uid",
		"resource_version", "namespace", "operation", "execution_status", "params_json", "result_json", "executed_at",
		"error_code", "created_at", "updated_at"}).
		AddRow("action-1", "run-1", "tenant-1", "cluster-1", "scale", "hash-1", "action-key", "R1", "R1",
			"approved", 0, "orders", "uid-1", "rv-1", "prod", "scale", executionStatus,
			[]byte(`{"replicas":2}`), nil, nil, "", time.Now(), time.Now())
}

func approvalReadRows() *sqlmock.Rows {
	return sqlmock.NewRows([]string{"approval_id", "run_id", "action_id", "action_hash", "tenant_id", "cluster_id",
		"decision", "approver", "reason", "decided_at", "created_at"}).
		AddRow("approval-1", "run-1", "action-1", "hash-1", "tenant-1", "cluster-1", "approved", "approver-1", nil, time.Now(), time.Now())
}

func expectActionClaim(mock sqlmock.Sqlmock) {
	mock.ExpectExec(regexp.QuoteMeta("UPDATE ai_action_outbox")).
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), int64(30), int64(30), "cmd-1").
		WillReturnResult(sqlmock.NewResult(0, 1))
}

func TestActionDispatcherDeliversExecutionResultAndMovesRunToVerification(t *testing.T) {
	h := &Handler{actionDAO: &store.AIActionDAO{}, actionOutboxDAO: &store.AIActionOutboxDAO{}, approvalDAO: &store.AIApprovalDecisionDAO{}}
	mock, cleanup := setupAPIStore(t)
	defer cleanup()
	expectActionClaim(mock)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT action_id, run_id")).WillReturnRows(actionReadRows("queued"))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT approval_id, run_id, action_id, action_hash")).WillReturnRows(approvalReadRows())
	h.actionDispatchExecute = func(*store.AIAction, *store.AIApprovalDecision) (contract.ActionResult, error) {
		return contract.ActionResult{ActionID: "action-1", Status: "success"}, nil
	}
	mock.ExpectExec(regexp.QuoteMeta("UPDATE ai_runs SET status = ?, state_version = state_version + 1")).
		WithArgs("verifying", nil, "run-1").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta("UPDATE ai_action_outbox SET status = 'delivered'")).
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))

	h.dispatchActionOne(store.AIActionOutbox{CommandID: "cmd-1", ActionID: "action-1", ActionHash: "hash-1",
		RunID: "run-1", TenantID: "tenant-1", ClusterID: "cluster-1", DispatchCount: 1})
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestActionDispatcherReconcilesUnknownBeforeAnyRetry(t *testing.T) {
	h := &Handler{actionDAO: &store.AIActionDAO{}, actionOutboxDAO: &store.AIActionOutboxDAO{}, approvalDAO: &store.AIApprovalDecisionDAO{}}
	mock, cleanup := setupAPIStore(t)
	defer cleanup()
	calledExecute, calledReconcile := 0, 0
	h.actionDispatchExecute = func(action *store.AIAction, _ *store.AIApprovalDecision) (contract.ActionResult, error) {
		calledExecute++
		action.ExecutionStatus = "execution_unknown"
		return contract.ActionResult{ActionID: action.ActionID, Status: "execution_unknown"}, nil
	}
	h.actionDispatchReconcile = func(*store.AIAction) (contract.ActionResult, error) {
		calledReconcile++
		return contract.ActionResult{ActionID: "action-1", Status: "success"}, nil
	}
	// First delivery produces execution_unknown and only schedules a retry.
	expectActionClaim(mock)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT action_id, run_id")).WillReturnRows(actionReadRows("queued"))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT approval_id, run_id, action_id, action_hash")).WillReturnRows(approvalReadRows())
	mock.ExpectExec(regexp.QuoteMeta("UPDATE ai_action_outbox SET status = 'pending'")).
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))
	h.dispatchActionOne(store.AIActionOutbox{CommandID: "cmd-1", ActionID: "action-1", ActionHash: "hash-1",
		RunID: "run-1", TenantID: "tenant-1", ClusterID: "cluster-1", DispatchCount: 1})

	// Redelivery sees execution_unknown and reconciles; it never calls Execute again.
	expectActionClaim(mock)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT action_id, run_id")).WillReturnRows(actionReadRows("execution_unknown"))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT approval_id, run_id, action_id, action_hash")).WillReturnRows(approvalReadRows())
	mock.ExpectExec(regexp.QuoteMeta("UPDATE ai_runs SET status = ?, state_version = state_version + 1")).
		WithArgs("verifying", nil, "run-1").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta("UPDATE ai_action_outbox SET status = 'delivered'")).
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))
	h.dispatchActionOne(store.AIActionOutbox{CommandID: "cmd-1", ActionID: "action-1", ActionHash: "hash-1",
		RunID: "run-1", TenantID: "tenant-1", ClusterID: "cluster-1", DispatchCount: 2})
	if calledExecute != 1 || calledReconcile != 1 {
		t.Fatalf("unexpected calls: execute=%d reconcile=%d", calledExecute, calledReconcile)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestListActionsIsTenantScopedReadModel(t *testing.T) {
	h := &Handler{}
	mock, cleanup := setupAPIStore(t)
	defer cleanup()
	mock.ExpectQuery(regexp.QuoteMeta("SELECT action_id, run_id, cluster_id, action_type, action_hash, hash_schema_version,")).
		WithArgs("tenant-1", "proposed", 10).
		WillReturnRows(sqlmock.NewRows([]string{"action_id", "run_id", "cluster_id", "action_type", "action_hash", "hash_schema_version",
			"action_version", "proposed_by", "policy_version", "preflight_status", "target_resource_type", "status", "dry_run",
			"target_name", "target_uid", "resource_version", "namespace", "operation", "execution_status", "error_code", "params_json", "created_at", "updated_at"}).
			AddRow("action-1", "run-1", "cluster-1", "scale", "hash-1", 2, 1, "owner-1", "action-policy-v1", "passed", "deployment",
				"proposed", 0, "orders", "uid-1", "rv-1", "prod", "scale", "proposed", "", []byte(`{"replicas":2}`), time.Now(), time.Now()))
	req := httptest.NewRequest(http.MethodGet, "/api/v1/ai/actions?status=proposed&limit=10", strings.NewReader(""))
	req = withAuthorizationContext(req, AuthorizationContext{UserID: "approver-1", TenantID: "tenant-1"})
	rec := httptest.NewRecorder()
	h.ActionPublicHandler(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "action-1") || !strings.Contains(rec.Body.String(), `"replicas":2`) {
		t.Fatalf("unexpected list response %d: %s", rec.Code, rec.Body.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
