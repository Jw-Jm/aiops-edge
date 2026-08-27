//go:build integration

package store

import (
	"database/sql"
	"os"
	"testing"
	"time"
)

// TestEvidenceConsumeReal 真实 MySQL 验证 Evidence 一次消费：
//  1. 插入 eligible=1 未消费的 success ToolRun。
//  2. ConsumeToolRunAsEvidence → 创建 ai_evidence + evidence_consumed_at 标记。
//  3. 再次消费 → ErrEvidenceNotEligible（已消费，防重复转 Evidence）。
//  4. 非 eligible / 跨 cluster 的 ToolRun → ErrEvidenceNotEligible（不跨 epoch/终态进入 Evidence）。
func TestEvidenceConsumeReal(t *testing.T) {
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

	toolRunID := "tr-ev-" + time.Now().Format("150405")
	runID := "run-ev-" + time.Now().Format("150405")
	evID := "11111111-1111-4111-8111-111111111111"
	reqID := "req-" + time.Now().Format("150405000")
	db.Exec(`DELETE FROM ai_evidence WHERE evidence_id=?`, evID)
	db.Exec(`DELETE FROM ai_tool_runs WHERE tool_run_id=?`, toolRunID)
	db.Exec(`DELETE FROM ai_runs WHERE run_id=?`, runID)
	if _, err := db.Exec(`INSERT INTO ai_runs (run_id, request_id, tenant_id, principal,
		principal_type, scope_kind, status, state_version, created_at, updated_at)
		VALUES (?, ?, 't1', 'p1', 'user', 'single_cluster', 'created', 0, NOW(), NOW())`, runID, reqID); err != nil {
		t.Fatalf("create run: %v", err)
	}
	t.Cleanup(func() {
		db.Exec(`DELETE FROM ai_evidence WHERE evidence_id=?`, evID)
		db.Exec(`DELETE FROM ai_tool_runs WHERE tool_run_id=?`, toolRunID)
		db.Exec(`DELETE FROM ai_runs WHERE run_id=?`, runID)
	})
	// eligible=1 未消费 success ToolRun（同 run/tenant/cluster）
	if _, err := db.Exec(`INSERT INTO ai_tool_runs (tool_run_id, run_id, tenant_id, cluster_id,
		tool_name, status, created_at, eligible_for_evidence, result_quality, idempotency_key)
		VALUES (?, ?, 't1', 'c1', 'internal_query', 'success', NOW(), 1, 'complete', ?)`,
		toolRunID, runID, "idem-"+toolRunID); err != nil {
		t.Fatalf("insert tool_run: %v", err)
	}

	dao := &EvidenceDAO{}
	ev := Evidence{
		EvidenceID: evID, RunID: runID, TenantID: "t1", ClusterID: "c1",
		EvidenceType: "metric_anomaly", SourceRef: "toolrun:" + toolRunID,
		RawDigestSHA256: "abc123", CollectedAt: time.Now(),
	}
	// 1) 首次消费 → 成功
	tx1, _ := db.Begin()
	consumed, err := dao.ConsumeToolRunAsEvidence(tx1, ev, toolRunID, "success,partial,no_data")
	if err != nil || !consumed {
		_ = tx1.Rollback()
		t.Fatalf("first consume: consumed=%v err=%v", consumed, err)
	}
	if err := tx1.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
	// 验证 ai_evidence + evidence_consumed_at
	var evCount int
	_ = db.QueryRow(`SELECT COUNT(*) FROM ai_evidence WHERE evidence_id=?`, evID).Scan(&evCount)
	if evCount != 1 {
		t.Fatalf("expected 1 evidence row, got %d", evCount)
	}
	var consumedAt sql.NullTime
	_ = db.QueryRow(`SELECT evidence_consumed_at FROM ai_tool_runs WHERE tool_run_id=?`, toolRunID).Scan(&consumedAt)
	if !consumedAt.Valid {
		t.Fatalf("expected evidence_consumed_at set")
	}

	// 2) 再次消费 → ErrEvidenceNotEligible（已消费）
	tx2, _ := db.Begin()
	consumed2, err := dao.ConsumeToolRunAsEvidence(tx2, ev, toolRunID, "success,partial,no_data")
	_ = tx2.Rollback()
	if err != ErrEvidenceNotEligible || consumed2 {
		t.Fatalf("second consume should be not-eligible: consumed=%v err=%v", consumed2, err)
	}

	// 3) 跨 cluster → ErrEvidenceNotEligible
	toolRunID2 := "tr-ev2-" + time.Now().Format("150405")
	db.Exec(`DELETE FROM ai_tool_runs WHERE tool_run_id=?`, toolRunID2)
	if _, err := db.Exec(`INSERT INTO ai_tool_runs (tool_run_id, run_id, tenant_id, cluster_id,
		tool_name, status, created_at, eligible_for_evidence, result_quality, idempotency_key)
		VALUES (?, ?, 't1', 'OTHER-CLUSTER', 'internal_query', 'success', NOW(), 1, 'complete', ?)`,
		toolRunID2, runID, "idem-"+toolRunID2); err != nil {
		t.Fatalf("insert cross-cluster tool_run: %v", err)
	}
	tx3, _ := db.Begin()
	consumed3, err := dao.ConsumeToolRunAsEvidence(tx3, ev, toolRunID2, "success,partial,no_data")
	_ = tx3.Rollback()
	if err != ErrEvidenceNotEligible || consumed3 {
		t.Fatalf("cross-cluster should be not-eligible: consumed=%v err=%v", consumed3, err)
	}
	t.Logf("Evidence one-time consume integration PASS")
}
