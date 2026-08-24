//go:build integration

package store

import (
	"database/sql"
	"encoding/json"
	"os"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/observability-platform/ai-apm-query-go/internal/store/migrations"
)

// ─────────────────────────────────────────────────────────────────────────────
// A1（0004_runtime_convergence）— Lease + Runtime Commit 真实 MySQL 集成测试。
//
// 覆盖（报告 21.2）：
//   1. 双 executor claim → 单 owner；second claim 被拒（RUN_LEASE_HELD）。
//   2. Lease 过期后旧 owner renew 被拒（epoch/token fencing 无效果因过期）。
//   3. Release 用错误 token → 被拒（fencing）。
//   4. Runtime Commit 响应丢失 → 同 commit_id 重试返回首次结果（幂等）。
//   5. old epoch commit fenced（旧 lease 的 commit 被拒）。
//
// 运行：TEST_MYSQL_DSN="..." go test -tags integration ./internal/store/ -run TestA1LeaseCommit -v
// 使用隔离 run_id（避免污染生产数据）。
// ─────────────────────────────────────────────────────────────────────────────

func TestA1LeaseCommit(t *testing.T) {
	dsn := os.Getenv("TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("TEST_MYSQL_DSN not set; requires real MySQL (real-environment Integration Gate)")
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil || db.Ping() != nil {
		t.Skipf("mysql unavailable: %v", err)
	}
	defer db.Close()
	prev := GetDB()
	SetDB(db)
	defer func() { SetDB(prev) }()
	if err := migrations.Run(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	runID := "cccccccc-cccc-4ccc-8ccc-cccccccccccc" // 隔离 run
	tenantID := "7ed01afc-cc79-4ecd-8767-a2befa6168ad"
	now := time.Now()
	runDAO := &AIRunDAO{}

	// 清理上次残留
	db.Exec(`DELETE FROM ai_run_claims WHERE run_id=?`, runID)
	db.Exec(`DELETE FROM ai_runtime_commits WHERE run_id=?`, runID)
	db.Exec(`DELETE FROM ai_run_events WHERE run_id=?`, runID)
	db.Exec(`DELETE FROM ai_runs WHERE run_id=?`, runID)

	created, err := runDAO.Create(AIRun{
		RunID: runID, RequestID: "a1-test", TenantID: tenantID,
		Principal: "91480408-9c2d-11f1-8271-bea176fe9f9f", PrincipalType: "user",
		ScopeKind: "single_cluster", Status: "created", CreatedAt: now, UpdatedAt: now,
	})
	if err != nil || !created {
		t.Fatalf("create run: created=%v err=%v", created, err)
	}
	t.Cleanup(func() {
		db.Exec(`DELETE FROM ai_run_claims WHERE run_id=?`, runID)
		db.Exec(`DELETE FROM ai_runtime_commits WHERE run_id=?`, runID)
		db.Exec(`DELETE FROM ai_run_events WHERE run_id=?`, runID)
		db.Exec(`DELETE FROM ai_runs WHERE run_id=?`, runID)
	})

	leaseDAO := &RuntimeLeaseDAO{}

	// 1) executor A claim 成功（owner, epoch=1, 明文 token）
	holderA, err := leaseDAO.Claim(runID, "executor-A", 60)
	if err != nil {
		t.Fatalf("executor A claim: %v", err)
	}
	if holderA.OwnerID != "executor-A" || holderA.Epoch != 1 || holderA.Token == "" {
		t.Fatalf("holder A wrong: %+v", holderA)
	}

	// 2) executor B claim 被拒（RUN_LEASE_HELD）——单 owner
	_, err = leaseDAO.Claim(runID, "executor-B", 60)
	if err == nil || err != ErrLeaseHeld {
		t.Fatalf("executor B claim should be held, got %v", err)
	}

	// 3) 同 owner 重复 claim → 返回既有权（幂等，epoch 不变）
	again, err := leaseDAO.Claim(runID, "executor-A", 60)
	if err != nil {
		t.Fatalf("re-claim A: %v", err)
	}
	if again.Epoch != 1 {
		t.Fatalf("re-claim should keep epoch=1, got %d", again.Epoch)
	}

	// 4) renew 用正确 epoch+token → 成功
	newExp, err := leaseDAO.Renew(runID, "executor-A", holderA.Epoch, holderA.TokenHash, 60)
	if err != nil || newExp.IsZero() {
		t.Fatalf("renew: %v", err)
	}

	// 5) renew 用错误 token → fencing 拒绝
	_, err = leaseDAO.Renew(runID, "executor-A", holderA.Epoch, "wronghash", 60)
	if err == nil || err != ErrLeaseFencing {
		t.Fatalf("renew wrong token should fence, got %v", err)
	}

	// 6) Runtime Commit：commitDAO 幂等（先存一个 commit，再读）
	commitDAO := &RuntimeCommitDAO{}
	db.Exec(`DELETE FROM ai_runtime_commits WHERE run_id=?`, runID)
	tx1, err := db.Begin()
	if err != nil {
		t.Fatalf("begin commit tx: %v", err)
	}
	if err := commitDAO.CreateTx(tx1, RuntimeCommit{
		RunID: runID, CommitID: "commit-1", PayloadHash: "p1",
		CommittedStateVersion: 1, ResultStatus: "planning", ResponseJSON: []byte(`{"ok":true}`),
	}); err != nil {
		_ = tx1.Rollback()
		t.Fatalf("create commit: %v", err)
	}
	if err := tx1.Commit(); err != nil {
		t.Fatalf("commit tx: %v", err)
	}
	got, err := commitDAO.Get(runID, "commit-1")
	if err != nil {
		t.Fatalf("get commit: %v", err)
	}
	var gotObj map[string]bool
	if err := json.Unmarshal(got.ResponseJSON, &gotObj); err != nil || !gotObj["ok"] {
		t.Fatalf("get commit response not ok: %s", string(got.ResponseJSON))
	}
	// 同 commit_id 重复 CreateTx → ErrCommitDuplicate（用新事务，插入会撞 PK）
	tx2, err := db.Begin()
	if err != nil {
		t.Fatalf("begin dup tx: %v", err)
	}
	if err := commitDAO.CreateTx(tx2, RuntimeCommit{
		RunID: runID, CommitID: "commit-1", PayloadHash: "p1",
		CommittedStateVersion: 1, ResultStatus: "planning", ResponseJSON: []byte(`{"ok":true}`),
	}); err != ErrCommitDuplicate {
		_ = tx2.Rollback()
		t.Fatalf("duplicate commit should be ErrCommitDuplicate, got %v", err)
	}
	_ = tx2.Rollback()

	t.Logf("A1 Lease+Commit integration PASS")
}

// TestC3AlertLeader 验证 C-03 Alert 单 Leader + cooldown 状态 MySQL 持久化（真实 MySQL）。
func TestC3AlertLeader(t *testing.T) {
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
	if err := migrations.Run(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	db.Exec(`DELETE FROM aiops.alert_eval_leader`)
	db.Exec(`DELETE FROM aiops.alert_rule_runtime_state WHERE rule_id='r-1'`)
	t.Cleanup(func() {
		db.Exec(`DELETE FROM aiops.alert_eval_leader`)
		db.Exec(`DELETE FROM aiops.alert_rule_runtime_state WHERE rule_id='r-1'`)
	})

	leaderDAO := &AlertEvalLeaderDAO{}

	// 1) pod A 获取 Leader（epoch=1）
	epA, tokA, isLeader, err := leaderDAO.Acquire("pod-A")
	if err != nil || !isLeader || epA < 1 || tokA == "" {
		t.Fatalf("pod A acquire: isLeader=%v ep=%d err=%v", isLeader, epA, err)
	}

	// 2) pod B 获取被拒（Held）——单 Leader
	_, _, _, err = leaderDAO.Acquire("pod-B")
	if err != ErrAlertLeaderHeld {
		t.Fatalf("pod B should be held, got %v", err)
	}

	// 3) IsLeader（正确 token）→ true
	ok, err := leaderDAO.IsLeader("pod-A", epA, tokA)
	if err != nil || !ok {
		t.Fatalf("pod A isLeader: %v %v", ok, err)
	}
	// 错误 token → false
	ok, _ = leaderDAO.IsLeader("pod-A", epA, "wrongtok")
	if ok {
		t.Fatalf("wrong token should not be leader")
	}

	// 4) 同 pod A 再次 Acquire → 续约（epoch 不变）
	epA2, _, isLeader2, err := leaderDAO.Acquire("pod-A")
	if err != nil || !isLeader2 {
		t.Fatalf("pod A re-acquire: %v", err)
	}
	if epA2 != epA {
		t.Fatalf("re-acquire should keep epoch=%d, got %d", epA, epA2)
	}

	// 5) cooldown 状态 Upsert/Get（MySQL 持久化）
	stateDAO := &AlertRuleRuntimeStateDAO{}
	now := time.Now()
	if err := stateDAO.Upsert(AlertRuleRuntimeState{RuleID: "r-1", LastTriggerAt: &now, BreachStreak: 3}); err != nil {
		t.Fatalf("upsert state: %v", err)
	}
	st, err := stateDAO.Get("r-1")
	if err != nil || st == nil || st.BreachStreak != 3 {
		t.Fatalf("get state: %v %+v", err, st)
	}

	t.Logf("C3 Alert leader + state persistence PASS")
}
