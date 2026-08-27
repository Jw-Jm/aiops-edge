//go:build integration

package store

import (
	"database/sql"
	"os"
	"testing"
	"time"
)

// TestP0CommitHashConflictReal 真实 MySQL 验证 P0#2/#10 Runtime Commit 幂等 hash 冲突：
//  1. 同 commit_id + 同 payload_hash → 首次结果（幂等命中）。
//  2. 同 commit_id + 不同 payload_hash → 409 IDEMPOTENCY_KEY_REUSED。
func TestP0CommitHashConflictReal(t *testing.T) {
	dsn := os.Getenv("TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("TEST_MYSQL_DSN not set")
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil || db.Ping() != nil {
		t.Skipf("mysql unavailable: %v", err)
	}
	defer db.Close()
	prev := GetDB()
	SetDB(db)
	defer func() { SetDB(prev) }()

	runID := "run-cmh-" + time.Now().Format("150405")
	reqID := "req-" + time.Now().Format("150405009")
	db.Exec(`DELETE FROM ai_runtime_commits WHERE run_id=?`, runID)
	db.Exec(`DELETE FROM ai_runs WHERE run_id=?`, runID)
	if _, err := db.Exec(`INSERT INTO ai_runs (run_id, request_id, tenant_id, principal,
		principal_type, scope_kind, status, state_version, created_at, updated_at)
		VALUES (?, ?, 't1', 'p1', 'user', 'single_cluster', 'planning', 0, NOW(), NOW())`,
		runID, reqID); err != nil {
		t.Fatalf("create run: %v", err)
	}
	t.Cleanup(func() {
		db.Exec(`DELETE FROM ai_runtime_commits WHERE run_id=?`, runID)
		db.Exec(`DELETE FROM ai_runs WHERE run_id=?`, runID)
	})
	commitDAO := &RuntimeCommitDAO{}
	tx1, _ := db.Begin()
	if err := commitDAO.CreateTx(tx1, RuntimeCommit{
		RunID: runID, CommitID: "c-hash-1", PayloadHash: "ph-a",
		CommittedStateVersion: 1, ResultStatus: "investigating", ResponseJSON: []byte(`{"ok":true}`),
	}); err != nil {
		_ = tx1.Rollback()
		t.Fatalf("create commit: %v", err)
	}
	if err := tx1.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
	// 1) 同 commit_id + 同 hash → 幂等命中（返回既有）
	got, err := commitDAO.Get(runID, "c-hash-1")
	if err != nil || got.PayloadHash != "ph-a" {
		t.Fatalf("get commit: %v", err)
	}
	// 2) 同 commit_id + 不同 hash → CreateTx 幂等冲突（应用层映射为 IDEMPOTENCY_KEY_REUSED）
	tx2, _ := db.Begin()
	err2 := commitDAO.CreateTx(tx2, RuntimeCommit{
		RunID: runID, CommitID: "c-hash-1", PayloadHash: "ph-b",
		CommittedStateVersion: 2, ResultStatus: "investigating", ResponseJSON: []byte(`{"ok":false}`),
	})
	_ = tx2.Rollback()
	if err2 != ErrCommitDuplicate {
		t.Fatalf("diff hash same commit_id should be duplicate (mapped to IDEMPOTENCY_KEY_REUSED), got %v", err2)
	}
	t.Logf("P0 Commit hash-conflict integration PASS")
}

// TestP0ToolPreIOFencingReal 真实 MySQL 验证 P0-TOOL-02：Tool 执行前 Lease fencing。
func TestP0ToolPreIOFencingReal(t *testing.T) {
	dsn := os.Getenv("TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("TEST_MYSQL_DSN not set")
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil || db.Ping() != nil {
		t.Skipf("mysql unavailable: %v", err)
	}
	defer db.Close()
	prev := GetDB()
	SetDB(db)
	defer func() { SetDB(prev) }()

	runID := "run-fence-" + time.Now().Format("150405")
	reqID := "req-" + time.Now().Format("150405008")
	db.Exec(`DELETE FROM ai_runs WHERE run_id=?`, runID)
	if _, err := db.Exec(`INSERT INTO ai_runs (run_id, request_id, tenant_id, principal,
		principal_type, scope_kind, status, state_version, lease_owner_id, lease_epoch,
		lease_token_hash, lease_expires_at, created_at, updated_at)
		VALUES (?, ?, 't1', 'p1', 'user', 'single_cluster', 'running', 0,
		'exec-a', 3, 'tokhash-a', DATE_ADD(CURRENT_TIMESTAMP(3), INTERVAL 60 SECOND), NOW(), NOW())`,
		runID, reqID); err != nil {
		t.Fatalf("create run: %v", err)
	}
	t.Cleanup(func() {
		db.Exec(`DELETE FROM ai_runs WHERE run_id=?`, runID)
	})
	leaseDAO := &RuntimeLeaseDAO{}
	// 1) 正确 owner/epoch/token → fencing 通过
	tx1, _ := db.Begin()
	err = leaseDAO.FenceToolExecutionTx(tx1, runID, "exec-a", 3, "tokhash-a")
	_ = tx1.Rollback()
	if err != nil {
		t.Fatalf("fence should pass: %v", err)
	}
	// 2) 错误 token → fencing 拒绝
	tx2, _ := db.Begin()
	err = leaseDAO.FenceToolExecutionTx(tx2, runID, "exec-a", 3, "wrong")
	_ = tx2.Rollback()
	if err != ErrLeaseFencing {
		t.Fatalf("wrong token should fence, got %v", err)
	}
	t.Logf("P0 Tool pre-I/O fencing integration PASS")
}
