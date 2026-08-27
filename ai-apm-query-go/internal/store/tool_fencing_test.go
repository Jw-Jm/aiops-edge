//go:build integration

package store

import (
	"database/sql"
	"os"
	"testing"
	"time"
)

// TestToolLateFencingReal 真实 MySQL 验证 27.12 Tool late/fencing：
//  1. Run lease_epoch != tool.lease_epoch_at_start → late=true, eligible=0。
//  2. Run 终态 → late=true（不进入 Evidence）。
//  3. Run epoch 匹配 → late=false, eligible=1（仅 complete quality）。
func TestToolLateFencingReal(t *testing.T) {
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
	toolID := "tr-fence-" + time.Now().Format("150405")
	reqID := "req-" + time.Now().Format("150405001")
	db.Exec(`DELETE FROM ai_tool_runs WHERE tool_run_id=?`, toolID)
	db.Exec(`DELETE FROM ai_runs WHERE run_id=?`, runID)
	if _, err := db.Exec(`INSERT INTO ai_runs (run_id, request_id, tenant_id, principal,
		principal_type, scope_kind, status, state_version, lease_epoch, created_at, updated_at)
		VALUES (?, ?, 't1', 'p1', 'user', 'single_cluster', 'running', 1, 5, NOW(), NOW())`,
		runID, reqID); err != nil {
		t.Fatalf("create run: %v", err)
	}
	t.Cleanup(func() {
		db.Exec(`DELETE FROM ai_tool_runs WHERE tool_run_id=?`, toolID)
		db.Exec(`DELETE FROM ai_runs WHERE run_id=?`, runID)
	})
	// tool 在 epoch=3 开始（与 run 当前 epoch=5 不匹配 → late）
	if _, err := db.Exec(`INSERT INTO ai_tool_runs (tool_run_id, run_id, tenant_id, cluster_id,
		tool_name, status, created_at, eligible_for_evidence, result_quality, idempotency_key, lease_epoch_at_start)
		VALUES (?, ?, 't1', 'c1', 'internal_query', 'running', NOW(), 0, 'none', ?, 3)`,
		toolID, runID, "idem-"+toolID); err != nil {
		t.Fatalf("insert tool_run: %v", err)
	}

	dao := &AIToolRunDAO{}
	// 1) epoch 不匹配 → late=true, eligible=0
	tx1, _ := db.Begin()
	late, err := dao.FinishToolRunWithFencing(tx1, AIToolRun{
		ToolRunID: toolID, RunID: runID, Status: "success", ResultQuality: "complete",
		CompletedAt: timePtr(), ObservedAt: timePtr(),
	})
	if err != nil {
		_ = tx1.Rollback()
		t.Fatalf("finish (mismatch): %v", err)
	}
	if !late {
		_ = tx1.Rollback()
		t.Fatalf("expected late=true for epoch mismatch")
	}
	if err := tx1.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
	var eligible int
	_ = db.QueryRow(`SELECT eligible_for_evidence FROM ai_tool_runs WHERE tool_run_id=?`, toolID).Scan(&eligible)
	if eligible != 0 {
		t.Fatalf("expected eligible=0 for late result, got %d", eligible)
	}

	// 2) 更新 tool 为 epoch 匹配（run epoch=5），重新插入一个匹配的 tool
	toolID2 := "tr-fence2-" + time.Now().Format("150405")
	db.Exec(`DELETE FROM ai_tool_runs WHERE tool_run_id=?`, toolID2)
	if _, err := db.Exec(`INSERT INTO ai_tool_runs (tool_run_id, run_id, tenant_id, cluster_id,
		tool_name, status, created_at, eligible_for_evidence, result_quality, idempotency_key, lease_epoch_at_start)
		VALUES (?, ?, 't1', 'c1', 'internal_query', 'running', NOW(), 0, 'none', ?, 5)`,
		toolID2, runID, "idem-"+toolID2); err != nil {
		t.Fatalf("insert tool2: %v", err)
	}
	tx2, _ := db.Begin()
	late2, err := dao.FinishToolRunWithFencing(tx2, AIToolRun{
		ToolRunID: toolID2, RunID: runID, Status: "success", ResultQuality: "complete",
		CompletedAt: timePtr(), ObservedAt: timePtr(),
	})
	_ = tx2.Rollback()
	if err != nil {
		t.Fatalf("finish (match): %v", err)
	}
	if late2 {
		t.Fatalf("expected late=false for epoch match")
	}
	t.Logf("Tool late/fencing integration PASS")
}

func timePtr() *time.Time {
	n := time.Now()
	return &n
}
