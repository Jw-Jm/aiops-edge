package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/observability-platform/ai-apm-query-go/internal/contract"
)

func TestValidateActionDecisionRequiresVersionAndIdempotency(t *testing.T) {
	if err := validateActionDecision(ActionDecisionRequest{Decision: "approved"}); err == nil {
		t.Fatal("missing action version and idempotency key must fail")
	}
	if err := validateActionDecision(ActionDecisionRequest{
		Decision: "approved", ActionVersion: 1, IdempotencyKey: "decision-1",
	}); err != nil {
		t.Fatalf("valid approval rejected: %v", err)
	}
}

func TestValidateActionDecisionRequiresReasonForRejection(t *testing.T) {
	err := validateActionDecision(ActionDecisionRequest{
		Decision: "rejected", ActionVersion: 1, IdempotencyKey: "decision-1",
	})
	if err == nil {
		t.Fatal("rejection without reason must fail")
	}
}

func TestActionDecisionEndpointAtomicallyQueuesApprovedAction(t *testing.T) {
	h := &Handler{}
	mock, cleanup := setupAPIStore(t)
	defer cleanup()
	actionHash, err := contract.CanonicalActionHash(contract.CanonicalActionPayloadV2{
		Version: 1, ActionType: "kubernetes", ResourceType: "deployment", Namespace: "prod",
		TargetName: "orders", TargetUID: "uid-1", ResourceVersion: "rv-7", Operation: "scale",
		Params: []byte(`{"replicas":2}`), PolicyVersion: "action-policy-v1",
	})
	if err != nil {
		t.Fatal(err)
	}

	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta("SELECT approval_id, action_id, COALESCE(action_version, 0), decision,")+
		"\\s+approver, COALESCE\\(reason, ''\\) FROM ai_approval_decisions").
		WithArgs("action-1", "decision-1").
		WillReturnRows(sqlmock.NewRows([]string{"approval_id", "action_id", "action_version", "decision", "approver", "reason"}))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT action_id, run_id, tenant_id, cluster_id, action_hash,") +
		"\\s+hash_schema_version, action_version, proposed_by, preflight_status, dry_run, status,").
		WithArgs("action-1").
		WillReturnRows(sqlmock.NewRows([]string{"action_id", "run_id", "tenant_id", "cluster_id", "action_hash",
			"hash_schema_version", "action_version", "proposed_by", "preflight_status", "dry_run", "status",
			"target_resource_type", "target_name", "target_uid", "resource_version", "namespace", "operation", "params_json"}).
			AddRow("action-1", "run-1", "tenant-1", "cluster-1", actionHash, 2, 1, "owner-1", "passed", 0, "proposed",
				"deployment", "orders", "uid-1", "rv-7", "prod", "scale", []byte(`{"replicas":2}`)))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT run_id, tenant_id, principal, status, state_version,") +
		"\\s+COALESCE\\(primary_cluster_id, ''\\) FROM ai_runs").
		WithArgs("run-1").
		WillReturnRows(sqlmock.NewRows([]string{"run_id", "tenant_id", "principal", "status", "state_version", "primary_cluster_id"}).
			AddRow("run-1", "tenant-1", "owner-1", "awaiting_approval", 3, "cluster-1"))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO ai_approval_decisions")).
		WithArgs(sqlmock.AnyArg(), "run-1", "action-1", actionHash, int64(1), "decision-1", "tenant-1", "cluster-1",
			"approved", "approver-1", nil, sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO ai_action_outbox")).
		WithArgs(sqlmock.AnyArg(), "action-1", int64(1), actionHash, "run-1", "tenant-1", "cluster-1", sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec(regexp.QuoteMeta("UPDATE ai_actions SET status = ?, execution_status = ?, updated_at = ? WHERE action_id = ? AND status = 'proposed'")).
		WithArgs("approved", "queued", sqlmock.AnyArg(), "action-1").
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec(regexp.QuoteMeta("UPDATE ai_runs SET status = ?, state_version = state_version + 1,")).
		WithArgs("executing", sqlmock.AnyArg(), nil, "run-1", int64(3)).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	req := httptest.NewRequest(http.MethodPost, "/api/v1/ai/actions/action-1/decision", bytes.NewBufferString(`{"decision":"approved","action_version":1,"idempotency_key":"decision-1"}`))
	req = withAuthorizationContext(req, AuthorizationContext{UserID: "approver-1", TenantID: "tenant-1"})
	rec := httptest.NewRecorder()
	h.ActionPublicHandler(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("decision status = %d, want 202: %s", rec.Code, rec.Body.String())
	}
	var got ActionDecisionResult
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.ActionID != "action-1" || got.Decision != "approved" || got.RunStatus != "executing" || got.CommandID == "" {
		t.Fatalf("unexpected decision result: %+v", got)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
