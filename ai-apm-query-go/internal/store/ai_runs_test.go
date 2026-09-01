package store

import (
	"database/sql"
	"errors"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

func setupAIRunsDB(t *testing.T) (sqlmock.Sqlmock, func()) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	prev := GetDB()
	SetDB(db)
	cleanup := func() {
		db.Close()
		SetDB(prev)
	}
	return mock, cleanup
}

func TestAIRunDAOCreate(t *testing.T) {
	mock, cleanup := setupAIRunsDB(t)
	defer cleanup()
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO ai_runs")).WillReturnResult(sqlmock.NewResult(1, 1))
	d := &AIRunDAO{}
	created, err := d.Create(AIRun{
		RunID: "a", RequestID: "r", TenantID: "t", Principal: "user",
		PrincipalType: "user", ScopeKind: "single_cluster", Intent: "diag",
		ActionMode: "read_only", Status: "created", StateVersion: 0,
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if !created {
		t.Fatalf("expected created=true")
	}
}

func TestAIRunDAOCreateReturnsExistingOnDuplicate(t *testing.T) {
	mock, cleanup := setupAIRunsDB(t)
	defer cleanup()
	// 唯一键冲突 → 返回 existing(!ok)，不报错
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO ai_runs")).
		WillReturnError(errors.New("Error 1062: Duplicate entry 'x' for key 'uq_ai_runs_tenant_request'"))
	d := &AIRunDAO{}
	created, err := d.Create(AIRun{
		RunID: "a", RequestID: "r", TenantID: "t", Principal: "user",
		Status: "created", CreatedAt: time.Now(), UpdatedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created {
		t.Fatalf("expected existing (!ok) on duplicate")
	}
}

func TestAIRunDAOTransitionTxValidatedWithLeaseChecksLeaseAtFinalCAS(t *testing.T) {
	mock, cleanup := setupAIRunsDB(t)
	defer cleanup()
	now := time.Date(2026, 9, 1, 3, 4, 5, 0, time.UTC)
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT status FROM ai_runs WHERE run_id = \\?.*FOR UPDATE").
		WithArgs("run-1").
		WillReturnRows(sqlmock.NewRows([]string{"status"}).AddRow("investigating"))
	mock.ExpectExec("UPDATE ai_runs SET status = \\?, state_version = state_version \\+ 1.*lease_owner_id = \\?.*lease_expires_at >= CURRENT_TIMESTAMP\\(3\\)").
		WithArgs("verifying", now, nil, "run-1", int64(2), "owner-1", int64(3), "hash-1").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	tx, err := GetDB().Begin()
	if err != nil {
		t.Fatal(err)
	}
	ok, err := (&AIRunDAO{}).TransitionTxValidatedWithLease(tx, "run-1", "verifying", 2, now, "owner-1", 3, "hash-1")
	if err != nil || !ok {
		t.Fatalf("TransitionTxValidatedWithLease() = ok %v err %v, want true", ok, err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestAIRunDAOGet(t *testing.T) {
	mock, cleanup := setupAIRunsDB(t)
	defer cleanup()
	rows := sqlmock.NewRows([]string{"run_id", "request_id", "tenant_id", "principal",
		"principal_type", "session_id", "scope_kind", "primary_cluster_id", "intent",
		"action_mode", "target_type", "target_resource_id", "time_range_start",
		"time_range_end", "status", "state_version", "parent_run_id", "created_at",
		"updated_at", "finished_at", "last_event_sequence"}).
		AddRow("a", "r", "t", "user", "user", nil, "single_cluster", nil, "diag",
			"read_only", nil, nil, nil, nil, "created", 0, nil, time.Now(), time.Now(),
			nil, 0)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT run_id, request_id")).WillReturnRows(rows)
	d := &AIRunDAO{}
	r, err := d.Get("a")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if r.RunID != "a" || r.Status != "created" {
		t.Fatalf("got %+v", r)
	}
}

func TestAIRunDAOTransitionCAS(t *testing.T) {
	mock, cleanup := setupAIRunsDB(t)
	defer cleanup()
	mock.ExpectExec(regexp.QuoteMeta("UPDATE ai_runs SET status")).WillReturnResult(sqlmock.NewResult(0, 1))
	d := &AIRunDAO{}
	ok, err := d.Transition("a", "planning", 0, time.Now())
	if err != nil || !ok {
		t.Fatalf("Transition: ok=%v err=%v", ok, err)
	}
}

func TestAIRunDAOTransitionConflict(t *testing.T) {
	mock, cleanup := setupAIRunsDB(t)
	defer cleanup()
	mock.ExpectExec(regexp.QuoteMeta("UPDATE ai_runs SET status")).WillReturnResult(sqlmock.NewResult(0, 0))
	d := &AIRunDAO{}
	ok, err := d.Transition("a", "planning", 5, time.Now())
	if err != nil {
		t.Fatalf("Transition: %v", err)
	}
	if ok {
		t.Fatalf("expected CAS conflict (ok=false)")
	}
}

func TestAIRunDAOCancel(t *testing.T) {
	mock, cleanup := setupAIRunsDB(t)
	defer cleanup()
	mock.ExpectExec(regexp.QuoteMeta("UPDATE ai_runs SET status = 'cancelled'")).WillReturnResult(sqlmock.NewResult(0, 1))
	d := &AIRunDAO{}
	ok, err := d.Cancel("a", 0, time.Now())
	if err != nil || !ok {
		t.Fatalf("Cancel: ok=%v err=%v", ok, err)
	}
}

func TestAIRunDAOListAndScan(t *testing.T) {
	mock, cleanup := setupAIRunsDB(t)
	defer cleanup()
	rows := sqlmock.NewRows([]string{"run_id", "request_id", "tenant_id", "principal",
		"principal_type", "session_id", "scope_kind", "primary_cluster_id", "intent",
		"action_mode", "target_type", "target_resource_id", "time_range_start",
		"time_range_end", "status", "state_version", "parent_run_id", "created_at",
		"updated_at", "finished_at", "last_event_sequence"}).
		AddRow("a", "r", "t", "user", "user", nil, "single_cluster", nil, "diag",
			"read_only", nil, nil, nil, nil, "created", 0, nil, time.Now(), time.Now(),
			nil, 0)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT run_id, request_id")).WillReturnRows(rows)
	d := &AIRunDAO{}
	runs, err := d.List("t")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(runs) != 1 || runs[0].RunID != "a" {
		t.Fatalf("got %+v", runs)
	}
	_ = sql.ErrNoRows
}
