package store

import (
	"context"
	"database/sql"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

// controlCmdCols 匹配 ai_control_commands 的查询列。
var controlCmdCols = []string{"command_id", "run_id", "operation", "payload_json",
	"payload_hash", "response_json", "status", "idempotency_key", "completed_at", "created_at"}

func setupControlCmdDB(t *testing.T) (sqlmock.Sqlmock, func()) {
	t.Helper()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	prev := GetDB()
	SetDB(db)
	return mock, func() {
		db.Close()
		SetDB(prev)
	}
}

// mutateNoop 返回一个"无真实 I/O"的 mutateFn，可配置 ok/err。
func mutateNoop(ok bool, merr error) func(tx *sql.Tx) ([]byte, bool, error) {
	return func(tx *sql.Tx) ([]byte, bool, error) {
		return []byte(`{"run":{"status":"cancelled"}}`), ok, merr
	}
}

func TestApplyControlCommandTxCommitSuccess(t *testing.T) {
	mock, cleanup := setupControlCmdDB(t)
	defer cleanup()
	mock.ExpectBegin()
	// 幂等检查 GetTx(command) → 空行（ErrNoRows）
	mock.ExpectQuery(regexp.QuoteMeta("SELECT command_id, run_id, operation")).
		WillReturnRows(sqlmock.NewRows(controlCmdCols))
	// UpsertDoneTx
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO ai_control_commands")).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	resp, replayed, err := ApplyRunControlCommandTx(context.Background(), "run-1", "cmd-1",
		"cancel", "hash-1", &AIControlCommandDAO{}, mutateNoop(true, nil))
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if replayed {
		t.Fatal("expected fresh commit, got replayed")
	}
	if string(resp) == "" {
		t.Fatal("expected response bytes")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations not met: %v", err)
	}
}

func TestApplyControlCommandTxCASConflict(t *testing.T) {
	mock, cleanup := setupControlCmdDB(t)
	defer cleanup()
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta("SELECT command_id, run_id, operation")).
		WillReturnRows(sqlmock.NewRows(controlCmdCols))
	mock.ExpectRollback()

	_, _, err := ApplyRunControlCommandTx(context.Background(), "run-1", "cmd-1",
		"cancel", "hash-1", &AIControlCommandDAO{}, mutateNoop(false, nil))
	if err != ErrRunControlConflict {
		t.Fatalf("expected ErrRunControlConflict, got %v", err)
	}
}

func TestApplyControlCommandTxIdempotentReplay(t *testing.T) {
	mock, cleanup := setupControlCmdDB(t)
	defer cleanup()
	// 幂等重放：同 command_id 已 done + 同 payload_hash → 返回 stored response，不执行 mutation。
	// 注意：重放路径在 return 前走 defer tx.Rollback()（不 Commit）。
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta("SELECT command_id, run_id, operation")).
		WillReturnRows(sqlmock.NewRows(controlCmdCols).
			AddRow("cmd-1", "run-1", "cancel", []byte(`{}`), "hash-1",
				[]byte(`{"run":{"status":"cancelled"}}`), "done", "cmd-1", nil, time.Now()))
	mock.ExpectRollback()

	resp, replayed, err := ApplyRunControlCommandTx(context.Background(), "run-1", "cmd-1",
		"cancel", "hash-1", &AIControlCommandDAO{}, mutateNoop(true, nil))
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !replayed {
		t.Fatal("expected replayed=true")
	}
	if string(resp) != `{"run":{"status":"cancelled"}}` {
		t.Fatalf("expected stored response, got %s", resp)
	}
	// 幂等命中 → mutateFn 不应执行。用能检测的 mutateFn：
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations not met: %v", err)
	}
}

func TestApplyControlCommandTxIdempotencyReused(t *testing.T) {
	mock, cleanup := setupControlCmdDB(t)
	defer cleanup()
	// 同 command_id 但 payload_hash 不同 → 409 IDEMPOTENCY_KEY_REUSED，不执行 mutation。
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta("SELECT command_id, run_id, operation")).
		WillReturnRows(sqlmock.NewRows(controlCmdCols).
			AddRow("cmd-1", "run-1", "cancel", []byte(`{}`), "hash-A",
				[]byte(`{"run":{"status":"cancelled"}}`), "done", "cmd-1", nil, time.Now()))
	mock.ExpectRollback()

	_, _, err := ApplyRunControlCommandTx(context.Background(), "run-1", "cmd-1",
		"cancel", "hash-B", &AIControlCommandDAO{}, mutateNoop(true, nil))
	if err != ErrCommandIdempotencyReused {
		t.Fatalf("expected ErrCommandIdempotencyReused, got %v", err)
	}
}
