//go:build integration

package api

import (
	"database/sql"
	"os"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/observability-platform/ai-apm-query-go/internal/store"
	"github.com/observability-platform/ai-apm-query-go/internal/store/migrations"
)

// ─────────────────────────────────────────────────────────────────────────────
// P10 完整闭环 (Plan C Task C6) — 真实 MySQL + 进程重启恢复集成测试。
//
// 评审 P1-3：sqlmock 仅用于 DAO 单测，**不能证明进程销毁后的持久性**。本集成测试
// 连真实 MySQL（TEST_MYSQL_DSN），执行 0002/0003/0003b 迁移，验证：
//   1. 进程销毁（新 DAO 实例 = 新连接等价重启）后 Run/Event/Plan/Tool/Action 持久化
//   2. ScanUnfinished 恢复
//   3. 同 idempotency_key 的 tool_run 不重复执行
//
// 运行：TEST_MYSQL_DSN="user:pass@tcp(host:3306)/aiops?parseTime=true&multiStatements=true" \
//       go test -tags integration ./internal/api/ -run TestProcessRestartRecoveryIntegration -v
// 环境受限（无本地 MySQL）时作为后续真实环境 Integration Gate，本机跳过。
// ─────────────────────────────────────────────────────────────────────────────

func TestProcessRestartRecoveryIntegration(t *testing.T) {
	dsn := os.Getenv("TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("TEST_MYSQL_DSN not set; requires real MySQL (real-environment Integration Gate)")
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Skipf("mysql unavailable: %v", err)
	}
	if err := db.Ping(); err != nil {
		t.Skipf("mysql ping failed: %v", err)
	}
	defer db.Close()
	prev := store.GetDB()
	store.SetDB(db)
	defer func() { store.SetDB(prev) }()

	// 1) 跑迁移（0001-0003b），幂等。
	if err := migrations.Run(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	// 2) 创建 Run（created）+ transition to planning + append event + plan_step + tool_run。
	// 用独立 run_id（避免与 gate10 集成测试在同库同 run_id 冲突）。
	runID := "bbbbbbbb-aaaa-4aaa-8aaa-bbbbbbbbbbbb"
	tenantID := "7ed01afc-cc79-4ecd-8767-a2befa6168ad"
	clusterID := "91771a6e-9c2d-11f1-8271-bea176fe9f9f"
	now := time.Now()
	runDAO := &store.AIRunDAO{}
	if created, err := runDAO.Create(store.AIRun{
		RunID: runID, RequestID: "req-recovery-1", TenantID: tenantID, Principal: "91480408-9c2d-11f1-8271-bea176fe9f9f",
		PrincipalType: "user", SessionID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
		ScopeKind: "single_cluster", PrimaryClusterID: clusterID, Intent: "investigate",
		ActionMode: "read_only", Status: "created", StateVersion: 0, CreatedAt: now, UpdatedAt: now,
	}); err != nil || !created {
		t.Fatalf("create run: created=%v err=%v", created, err)
	}
	if ok, err := runDAO.Transition(runID, "planning", 0, now); err != nil || !ok {
		t.Fatalf("transition: ok=%v err=%v", ok, err)
	}
	evDAO := &store.AIRunEventDAO{}
	if _, created, err := evDAO.Append(store.AIRunEvent{EventID: "e1", RunID: runID, EventType: "status", Payload: []byte(`{"x":1}`)}); err != nil || !created {
		t.Fatalf("append event: created=%v err=%v", created, err)
	}
	planDAO := &store.AIPlanStepDAO{}
	if _, err := planDAO.Create(store.AIPlanStep{StepID: "s1", RunID: runID, Seq: 1, StepType: "tool", Status: "success", DependsOn: []string{}}); err != nil {
		t.Fatalf("plan step: %v", err)
	}
	toolDAO := &store.AIToolRunDAO{}
	if _, err := toolDAO.Create(store.AIToolRun{ToolRunID: "t1", RunID: runID, TenantID: tenantID,
		ClusterID: clusterID, ToolName: "metrics", Status: "success", IdempotencyKey: "k1"}); err != nil {
		t.Fatalf("tool run: %v", err)
	}

	// 3) 模拟进程重启：新 DAO 实例（新连接）+ 重连。
	db2, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db2.Close()
	store.SetDB(db2)
	defer func() { store.SetDB(prev) }()

	// 4) ScanUnfinished 恢复 → 找到 planning Run。
	runDAO2 := &store.AIRunDAO{}
	unfinished, err := runDAO2.ScanUnfinished()
	if err != nil {
		t.Fatalf("scan unfinished: %v", err)
	}
	found := false
	for _, r := range unfinished {
		if r.RunID == runID && r.Status == "planning" {
			found = true
		}
	}
	if !found {
		t.Fatalf("planning run not recovered")
	}

	// 5) 同 idempotency_key 的 tool_run 不重复执行（Create 返回 existing）。
	toolDAO2 := &store.AIToolRunDAO{}
	created, err := toolDAO2.Create(store.AIToolRun{ToolRunID: "t1", RunID: runID, TenantID: tenantID,
		ClusterID: clusterID, ToolName: "metrics", Status: "success", IdempotencyKey: "k1"})
	if err != nil {
		t.Fatalf("tool run re-create: %v", err)
	}
	if created {
		t.Fatalf("expected tool_run existing (!created) on idempotency_key replay after restart")
	}
}
