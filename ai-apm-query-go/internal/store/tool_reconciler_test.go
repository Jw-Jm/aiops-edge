//go:build integration

package store

import (
	"database/sql"
	"os"
	"testing"
	"time"
)

// TestToolReconcilerReal 真实 MySQL 验证 Tool Reconciler：
//   1. 插入一个 deadline 已过的 running ToolRun。
//   2. ScanExpiredRunning 应返回该候选。
//   3. ConvergeToolRun 收敛为 timeout，eligible=false。
//   4. 再次 Converge 不再重复收敛（已是终态）。
func TestToolReconcilerReal(t *testing.T) {
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

	toolRunID := "tr-recon-1"
	runID := "run-recon-1"
	db.Exec(`DELETE FROM ai_tool_runs WHERE tool_run_id=?`, toolRunID)
	db.Exec(`DELETE FROM ai_runs WHERE run_id=?`, runID)
	// 创建 run
	if _, err := db.Exec(`INSERT INTO ai_runs (run_id, request_id, tenant_id, principal,
		principal_type, scope_kind, status, state_version, created_at, updated_at)
		VALUES (?, 'r1', 't1', 'p1', 'user', 'single_cluster', 'created', 0, NOW(), NOW())`, runID); err != nil {
		t.Fatalf("create run: %v", err)
	}
	t.Cleanup(func() {
		db.Exec(`DELETE FROM ai_tool_runs WHERE tool_run_id=?`, toolRunID)
		db.Exec(`DELETE FROM ai_runs WHERE run_id=?`, runID)
	})
	// 插入 deadline 已过 5 分钟的 running tool_run
	deadline := time.Now().Add(-5 * time.Minute)
	if _, err := db.Exec(`INSERT INTO ai_tool_runs (tool_run_id, run_id, tenant_id, cluster_id,
		tool_name, status, created_at, deadline_at, eligible_for_evidence)
		VALUES (?, ?, 't1', 'c1', 'internal_query', 'running', NOW(), ?, 0)`,
		toolRunID, runID, deadline); err != nil {
		t.Fatalf("insert tool_run: %v", err)
	}

	dao := &AIToolRunDAO{}
	// 1) ScanExpiredRunning 返回候选
	cands, err := dao.ScanExpiredRunning(10)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	found := false
	for _, c := range cands {
		if c.ToolRunID == toolRunID {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected expired running candidate %s in scan", toolRunID)
	}

	// 2) ConvergeToolRun 收敛为 timeout
	tx, _ := db.Begin()
	changed, err := dao.ConvergeToolRun(tx, toolRunID, runID, "timeout", "deadline exceeded", time.Now())
	if err != nil {
		_ = tx.Rollback()
		t.Fatalf("converge: %v", err)
	}
	if !changed {
		_ = tx.Rollback()
		t.Fatalf("expected changed=true")
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	// 3) 状态 + eligible=false
	var status string
	var eligible int
	_ = db.QueryRow(`SELECT status, eligible_for_evidence FROM ai_tool_runs WHERE tool_run_id=?`, toolRunID).
		Scan(&status, &eligible)
	if status != "timeout" || eligible != 0 {
		t.Fatalf("expected status=timeout eligible=0, got status=%s eligible=%d", status, eligible)
	}

	// 4) 再次 Converge → 不再重复（已是终态）
	tx2, _ := db.Begin()
	changed2, err := dao.ConvergeToolRun(tx2, toolRunID, runID, "timeout", "again", time.Now())
	_ = tx2.Rollback()
	if err != nil {
		t.Fatalf("re-converge: %v", err)
	}
	if changed2 {
		t.Fatalf("expected changed=false for terminal tool_run (no repeat convergence)")
	}
	t.Logf("Tool Reconciler integration PASS")
}
