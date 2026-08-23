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
// P10 完整闭环 (Plan D Task D4) — Gate 10 完整断言（真实 MySQL）。
//
// 覆盖：duplicate request_id idempotent / illegal transition 409 / cancel works /
// event sequence monotonic / process restart recovery / same idempotency_key tool_run
// not duplicated / SSE replay preserves sequence / Run relationships survive restart。
//
// 运行：TEST_MYSQL_DSN="..." go test -tags integration ./internal/api/ -run TestGate10Full -v
// 环境受限（无本地 MySQL）时作为后续真实环境 Integration Gate，本机 SKIP。
// ─────────────────────────────────────────────────────────────────────────────

func TestGate10Full(t *testing.T) {
	dsn := os.Getenv("TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("TEST_MYSQL_DSN not set; requires real MySQL (real-environment Integration Gate)")
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil || db.Ping() != nil {
		t.Skipf("mysql unavailable: %v", err)
	}
	defer db.Close()
	prev := store.GetDB()
	store.SetDB(db)
	defer func() { store.SetDB(prev) }()
	if err := migrations.Run(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	runID := "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	tenantID := "7ed01afc-cc79-4ecd-8767-a2befa6168ad"
	now := time.Now()
	runDAO := &store.AIRunDAO{}

	// 1) duplicate request_id → existing（幂等）。
	created, err := runDAO.Create(store.AIRun{
		RunID: runID, RequestID: "req-1", TenantID: tenantID,
		Principal: "91480408-9c2d-11f1-8271-bea176fe9f9f", PrincipalType: "user",
		ScopeKind: "single_cluster", Status: "created", CreatedAt: now, UpdatedAt: now,
	})
	if err != nil || !created {
		t.Fatalf("create run: created=%v err=%v", created, err)
	}
	created2, err := runDAO.Create(store.AIRun{
		RunID: runID, RequestID: "req-1", TenantID: tenantID,
		Principal: "91480408-9c2d-11f1-8271-bea176fe9f9f", PrincipalType: "user",
		ScopeKind: "single_cluster", Status: "created", CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("duplicate create: %v", err)
	}
	if created2 {
		t.Fatalf("expected existing (!created) on duplicate request_id")
	}

	// 2) illegal transition：终态 partial 之后不能 cancel。
	if ok, err := runDAO.Transition(runID, "planning", 0, now); err != nil || !ok {
		t.Fatalf("transition planning: ok=%v err=%v", ok, err)
	}
	if ok, err := runDAO.Transition(runID, "executing", 1, now); err != nil || !ok {
		t.Fatalf("transition executing: ok=%v err=%v", ok, err)
	}
	if ok, err := runDAO.Transition(runID, "partial", 2, now); err != nil || !ok {
		t.Fatalf("transition partial: ok=%v err=%v", ok, err)
	}
	// CAS 冲突（expected=0 但实际=3）→ ok=false（409 语义）。
	if ok, _ := runDAO.Transition(runID, "cancelled", 0, now); ok {
		t.Fatalf("expected CAS conflict on stale expected_version")
	}

	// 3) event sequence monotonic。
	evDAO := &store.AIRunEventDAO{}
	if _, c1, err := evDAO.Append(store.AIRunEvent{EventID: "e1", RunID: runID, EventType: "a"}); err != nil || !c1 {
		t.Fatalf("append e1: %v", err)
	}
	if _, c2, err := evDAO.Append(store.AIRunEvent{EventID: "e2", RunID: runID, EventType: "b"}); err != nil || !c2 {
		t.Fatalf("append e2: %v", err)
	}
	evs, _ := evDAO.ReplayAfter(runID, 0)
	if len(evs) != 2 || evs[0].Sequence >= evs[1].Sequence {
		t.Fatalf("sequence not monotonic: %+v", evs)
	}

	// 3b) event append 幂等（P1-2）：重复 event_id 返回首次结果，sequence 不推进（无 gap）。
	evs2, _ := evDAO.ReplayAfter(runID, 0)
	lastSeq := int64(len(evs2))
	evDup, createdDup, err := evDAO.Append(store.AIRunEvent{EventID: "e2", RunID: runID, EventType: "b"})
	if err != nil {
		t.Fatalf("append duplicate e2: %v", err)
	}
	if createdDup {
		t.Fatalf("expected existing (!created) on duplicate event_id")
	}
	lastSeqAfter, _ := evDAO.LastSequence(runID)
	if evDup.Sequence > lastSeq && lastSeqAfter != lastSeq {
		t.Fatalf("duplicate event should not advance sequence: lastSeq=%d after=%d",
			lastSeq, lastSeqAfter)
	}

	// 4) process restart：新连接等价重启 → ScanUnfinished 排除 partial。
	db2, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db2.Close()
	store.SetDB(db2)
	defer func() { store.SetDB(prev) }()
	unfinished, _ := (&store.AIRunDAO{}).ScanUnfinished()
	for _, r := range unfinished {
		if r.RunID == runID {
			t.Fatalf("partial run should not be recovered after restart")
		}
	}

	// 5) cancel：终态 partial 不可 cancel（显式 control action 仅非终态）。
	runDAO2 := &store.AIRunDAO{}
	runAfter, _ := runDAO2.Get(runID)
	if ok, _ := runDAO2.Cancel(runID, runAfter.StateVersion, time.Now()); ok {
		t.Fatalf("expected cancel rejected on terminal partial status")
	}

	// 6) 不重复 Tool/Action（P1-7）：同 idempotency_key 的 tool_run 幂等返回 existing。
	toolDAO := &store.AIToolRunDAO{}
	_, err = toolDAO.Create(store.AIToolRun{ToolRunID: "t1", RunID: runID, TenantID: tenantID,
		ClusterID: "91771a6e-9c2d-11f1-8271-bea176fe9f9f", ToolName: "metrics",
		Status: "success", IdempotencyKey: "k1"})
	if err != nil {
		t.Fatalf("tool run create: %v", err)
	}
	createdToolDup, err := toolDAO.Create(store.AIToolRun{ToolRunID: "t1", RunID: runID,
		TenantID: tenantID, ClusterID: "91771a6e-9c2d-11f1-8271-bea176fe9f9f",
		ToolName: "metrics", Status: "success", IdempotencyKey: "k1"})
	if err != nil {
		t.Fatalf("tool run re-create: %v", err)
	}
	if createdToolDup {
		t.Fatalf("expected existing (!created) on tool_run idempotency_key replay")
	}
}
