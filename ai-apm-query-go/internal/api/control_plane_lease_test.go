package api

import (
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/observability-platform/ai-apm-query-go/internal/store"
)

func TestClaimRequestFromBodyPreservesIncompleteCallerPairForValidation(t *testing.T) {
	requests := claimRequestFromBody("", "caller-token", "LIVE_INVOCATION")
	if len(requests) != 1 {
		t.Fatalf("claim request count = %d, want one request for DAO validation", len(requests))
	}
	if requests[0].ClaimID != "" || requests[0].LeaseToken != "caller-token" {
		t.Fatalf("claim request = %+v, want token-only pair preserved", requests[0])
	}
}

func TestClaimRequestFromBodyOmitsEmptyCallerPair(t *testing.T) {
	if requests := claimRequestFromBody("", "", ""); len(requests) != 0 {
		t.Fatalf("empty caller pair = %+v, want no caller request", requests)
	}
}

func TestRuntimeCommitRechecksCommitAfterLockingRun(t *testing.T) {
	mock, cleanup := setupAPIStore(t)
	defer cleanup()
	h := &Handler{commitDAO: &store.RuntimeCommitDAO{}}
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT run_id FROM ai_runs WHERE run_id = \\?.*FOR UPDATE").
		WithArgs("run-1").
		WillReturnRows(sqlmock.NewRows([]string{"run_id"}).AddRow("run-1"))
	mock.ExpectQuery("SELECT run_id, commit_id, payload_hash").
		WithArgs("run-1", "commit-1").
		WillReturnRows(sqlmock.NewRows([]string{
			"run_id", "commit_id", "payload_hash", "committed_state_version", "result_status",
			"first_event_sequence", "last_event_sequence", "response_json", "created_at",
		}).AddRow("run-1", "commit-1", "hash-1", int64(3), "success", nil, nil,
			[]byte(`{"ok":true}`), time.Now()))
	mock.ExpectRollback()

	err := h.applyRuntimeCommitTx("run-1", controlPlaneBodyCommit{
		CommitID: "commit-1", PayloadHash: "hash-1", OwnerID: "owner-1", Epoch: 1, Token: "token",
	})
	var hit *commitIdempotentHit
	if err == nil || !asCommitIdempotentHit(err, &hit) {
		t.Fatalf("applyRuntimeCommitTx() error = %v, want idempotent hit", err)
	}
	if hit.Commit.CommitID != "commit-1" {
		t.Fatalf("idempotent commit = %+v, want commit-1", hit.Commit)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func asCommitIdempotentHit(err error, out **commitIdempotentHit) bool {
	hit, ok := err.(*commitIdempotentHit)
	if ok {
		*out = hit
	}
	return ok
}
