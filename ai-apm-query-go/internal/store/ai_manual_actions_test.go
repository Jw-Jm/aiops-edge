package store

import (
	"context"
	"errors"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestCreateManualActionIsAtomic(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	previous := GetDB()
	SetDB(db)
	t.Cleanup(func() { SetDB(previous) })

	now := time.Unix(100, 0)
	run := AIRun{RunID: "run-1", RequestID: "request-1", TenantID: "tenant-1", Principal: "user-1", PrincipalType: "user", ScopeKind: "single_cluster", PrimaryClusterID: "cluster-1", Intent: "scale", ActionMode: "manual", TargetType: "deployment", TargetResourceID: "orders", Status: "awaiting_approval", CreatedAt: now, UpdatedAt: now}
	action := AIAction{ActionID: "action-1", RunID: "run-1", TenantID: "tenant-1", ClusterID: "cluster-1", ActionType: "kubernetes", ActionHash: "hash-1", HashSchemaVersion: 2, ActionVersion: 1, ProposedBy: "user-1", PolicyVersion: "action-policy-v1", PreflightStatus: "passed", TargetResourceType: "deployment", IdempotencyKey: "request-1", ProposedRisk: "R2", AuthoritativeRisk: "R2", Status: "proposed", Params: []byte(`{"replicas":2}`), TargetName: "orders", TargetUID: "uid-1", ResourceVersion: "rv-1", Namespace: "prod", Operation: "scale"}

	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO ai_runs")).WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO ai_actions")).WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	created, err := (&AIRunDAO{}).CreateManualAction(context.Background(), run, action)
	if err != nil || !created {
		t.Fatalf("CreateManualAction() = created:%v err:%v", created, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestCreateManualActionRollsBackWhenActionInsertFails(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	previous := GetDB()
	SetDB(db)
	t.Cleanup(func() { SetDB(previous) })

	now := time.Unix(100, 0)
	run := AIRun{RunID: "run-1", RequestID: "request-1", TenantID: "tenant-1", Principal: "user-1", PrincipalType: "user", ScopeKind: "single_cluster", PrimaryClusterID: "cluster-1", Status: "awaiting_approval", CreatedAt: now, UpdatedAt: now}
	action := AIAction{ActionID: "action-1", RunID: "run-1", TenantID: "tenant-1", ClusterID: "cluster-1", ActionHash: "hash-1", IdempotencyKey: "request-1", Params: []byte(`{}`)}

	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO ai_runs")).WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO ai_actions")).WillReturnError(sqlmock.ErrCancelled)
	mock.ExpectRollback()

	created, err := (&AIRunDAO{}).CreateManualAction(context.Background(), run, action)
	if err == nil || created {
		t.Fatalf("CreateManualAction() = created:%v err:%v, want rollback error", created, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestCreateManualActionReplaysSameImmutableHash(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	previous := GetDB()
	SetDB(db)
	t.Cleanup(func() { SetDB(previous) })

	run := AIRun{RunID: "new-run", RequestID: "request-1", TenantID: "tenant-1"}
	action := AIAction{ActionID: "new-action", ActionHash: "hash-1"}
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO ai_runs")).WillReturnError(errors.New("Duplicate entry 'tenant-1-request-1' for key 'uq_ai_runs_tenant_request'"))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT run_id FROM ai_runs WHERE tenant_id = ? AND request_id = ? FOR UPDATE")).
		WithArgs("tenant-1", "request-1").
		WillReturnRows(sqlmock.NewRows([]string{"run_id"}).AddRow("existing-run"))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT action_hash FROM ai_actions WHERE run_id = ? LIMIT 1")).
		WithArgs("existing-run").
		WillReturnRows(sqlmock.NewRows([]string{"action_hash"}).AddRow("hash-1"))
	mock.ExpectCommit()

	created, err := (&AIRunDAO{}).CreateManualAction(context.Background(), run, action)
	if err != nil || created {
		t.Fatalf("replay = created:%v err:%v", created, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestCreateManualActionRejectsReplayWithDifferentHash(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	previous := GetDB()
	SetDB(db)
	t.Cleanup(func() { SetDB(previous) })

	run := AIRun{RunID: "new-run", RequestID: "request-1", TenantID: "tenant-1"}
	action := AIAction{ActionID: "new-action", ActionHash: "hash-2"}
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO ai_runs")).WillReturnError(errors.New("Duplicate entry for key uq_ai_runs_tenant_request"))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT run_id FROM ai_runs WHERE tenant_id = ? AND request_id = ? FOR UPDATE")).
		WithArgs("tenant-1", "request-1").
		WillReturnRows(sqlmock.NewRows([]string{"run_id"}).AddRow("existing-run"))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT action_hash FROM ai_actions WHERE run_id = ? LIMIT 1")).
		WithArgs("existing-run").
		WillReturnRows(sqlmock.NewRows([]string{"action_hash"}).AddRow("hash-1"))
	mock.ExpectRollback()

	created, err := (&AIRunDAO{}).CreateManualAction(context.Background(), run, action)
	if !errors.Is(err, ErrIdempotencyPayloadMismatch) || created {
		t.Fatalf("mismatch = created:%v err:%v", created, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
