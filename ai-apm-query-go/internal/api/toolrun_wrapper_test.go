package api

import (
	"database/sql/driver"
	"encoding/json"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/observability-platform/ai-apm-query-go/internal/store"
)

type int64Argument struct{ want int64 }

func (a int64Argument) Match(value driver.Value) bool {
	n, ok := value.(int64)
	return ok && n == a.want
}

func TestInvestigationToolRequestRequiresLeaseBoundContext(t *testing.T) {
	req := &internalQueryRequest{WorkloadKind: "investigation", RunID: ""}
	if err := validateToolRunRequest(req); err == nil {
		t.Fatal("investigation query without ToolRun/Run lease context must be rejected")
	}
}

func TestInvestigationToolRequestRequiresOrderedFrozenWindow(t *testing.T) {
	req := &internalQueryRequest{
		WorkloadKind: "investigation", RunID: "22222222-2222-4222-8222-222222222222",
		ToolRunID: "33333333-3333-4333-8333-333333333333", IdempotencyKey: "idem-1",
		ExecutorID: "orchestrator:test", LeaseEpoch: 1, LeaseToken: "lease-token",
	}
	if err := validateToolRunRequest(req); err == nil {
		t.Fatal("investigation query without frozen window must be rejected")
	}
	req.QueryWindowStart = "2026-08-27T01:00:00Z"
	req.QueryWindowEnd = "2026-08-27T00:00:00Z"
	if err := validateToolRunRequest(req); err == nil {
		t.Fatal("investigation query with reversed frozen window must be rejected")
	}
	req.QueryWindowEnd = "2026-08-27T02:00:00Z"
	if err := validateToolRunRequest(req); err != nil {
		t.Fatalf("valid frozen window rejected: %v", err)
	}
}

func TestToolArgsHashDistinguishesFullNumericArguments(t *testing.T) {
	a := &internalQueryRequest{Service: "orders", Minutes: 1}
	b := &internalQueryRequest{Service: "orders", Minutes: 257}
	if toolArgsHash(a) == toolArgsHash(b) {
		t.Fatal("args hash must distinguish numeric parameters beyond one byte")
	}
}

func TestWorkloadKindMatchRejectsUnsignedInvestigation(t *testing.T) {
	if err := checkWorkloadKindMatch("platform", "investigation"); err == nil {
		t.Fatal("body-only investigation workload must be rejected")
	}
	if err := checkWorkloadKindMatch("", "investigation"); err == nil {
		t.Fatal("missing signed workload must not authorize investigation")
	}
}

func TestWorkloadKindMatchRejectsInvestigationDowngrade(t *testing.T) {
	if err := checkWorkloadKindMatch("investigation", ""); err == nil {
		t.Fatal("omitted investigation workload must be rejected")
	}
	if err := checkWorkloadKindMatch("investigation", "chat"); err == nil {
		t.Fatal("chat downgrade must be rejected")
	}
}

func TestToolReplayUsesStoredEnvelopeAndRunningStatus(t *testing.T) {
	trc := &toolRunContext{ToolRunID: "tool-1", Existing: &store.AIToolRun{
		ToolRunID: "tool-1", Status: "success", Result: json.RawMessage(`{"points":[1]}`),
		ResultQuality: "complete", ResultCount: 1, ResultDigestSHA256: "stored-digest",
		ResultTruncated: true,
	}}
	env := toolReplayEnvelope(trc)
	if env.Quality != "complete" || env.ToolRunID != "tool-1" || !env.Truncated || env.Digest != "stored-digest" {
		t.Fatalf("stored tool result was not replayed: %+v", env)
	}
	running := &toolRunContext{ToolRunID: "tool-2", Existing: &store.AIToolRun{ToolRunID: "tool-2", Status: "running"}}
	if got := toolReplayEnvelope(running); got.Quality != "partial" || len(got.SourceErrors) != 1 {
		t.Fatalf("running replay must be an explicit in-flight envelope: %+v", got)
	}
}

func TestFinishToolRunFencingFailureNeverEligibleForEvidence(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	previous := store.GetDB()
	store.SetDB(db)
	defer store.SetDB(previous)

	// Simulate a transaction that cannot establish the final fencing check. The
	// fallback must retain an auditable failure but can never mark the result as
	// eligible for Evidence.
	mock.ExpectBegin().WillReturnError(sqlmock.ErrCancelled)
	mock.ExpectExec(".*").
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(),
			sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(),
			sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(),
			int64Argument{want: 0}, sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))

	h := &Handler{toolDAO: &store.AIToolRunDAO{}}
	h.finishToolRun(&toolRunContext{ToolRunID: "tool-1", RunID: "22222222-2222-4222-8222-222222222222"},
		"success", "complete", nil, 0, "")
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("fencing failure fallback must force eligible_for_evidence=0: %v", err)
	}
}

func TestFinishToolRunChecksLeaseBeforeCommit(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	previous := store.GetDB()
	store.SetDB(db)
	defer store.SetDB(previous)

	runID := "22222222-2222-4222-8222-222222222222"
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT run_id FROM ai_runs").
		WithArgs(runID, "exec-a", int64(7), hashToken("lease-token")).
		WillReturnRows(sqlmock.NewRows([]string{"run_id"}))
	mock.ExpectRollback()
	mock.ExpectExec(".*").
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(),
			sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(),
			sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(),
			int64Argument{want: 0}, sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))

	h := &Handler{toolDAO: &store.AIToolRunDAO{}, leaseDAO: &store.RuntimeLeaseDAO{}}
	h.finishToolRun(&toolRunContext{
		ToolRunID: "tool-1", RunID: runID, ExecutorID: "exec-a", LeaseEpoch: 7, LeaseToken: "lease-token",
	}, "success", "complete", nil, 0, "")
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("final lease fencing must run before commit and force ineligible fallback: %v", err)
	}
}
