package api

import (
	"context"
	"log"
	"time"

	"github.com/observability-platform/ai-apm-query-go/internal/store"
)

// ─────────────────────────────────────────────────────────────────────────────
// 27.13 Tool Reconciler：收敛超时/未知的 ToolRun。
//
// 查询候选时不加反向锁（`SELECT running tool_runs WHERE deadline_at < DB_NOW LIMIT N`），
// 逐条收敛时加统一锁序 Run -> ToolRun，recheck 仍 running 才置 timeout/failed_unknown，
// eligible=false（不进入 Evidence），append event（可空）。
// ─────────────────────────────────────────────────────────────────────────────

// RunToolReconcilerLoop 周期性收敛超时 ToolRun（每 reconcileInterval；replicas=1 时单循环即可，
// 多副本由 ConvergeToolRun 的 recheck + 锁保证只收敛一次）。
func (h *Handler) RunToolReconcilerLoop(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 30 * time.Second
	}
	log.Printf("Tool Reconciler loop started (every %s, convergTimeout/failed_unknown)", interval)
	for {
		if err := h.reconcileExpiredTools(); err != nil {
			log.Printf("tool reconciler: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(interval):
		}
	}
}

func (h *Handler) reconcileExpiredTools() error {
	conn := store.GetDB()
	if conn == nil {
		return nil // MySQL 不可用，静默跳过（下次再试）
	}
	candidates, err := h.toolDAO.ScanExpiredRunning(50)
	if err != nil {
		return err
	}
	for _, c := range candidates {
		cp.inc("tool_converged")
		now := time.Now()
		tx, err := conn.Begin()
		if err != nil {
			return err
		}
		changed, err := h.toolDAO.ConvergeToolRun(tx, c.ToolRunID, c.RunID, "timeout", "tool deadline exceeded", now)
		if err != nil {
			_ = tx.Rollback()
			return err
		}
		if changed {
			// 尽力 append event（失败不阻断收敛）。
			_, _, _ = h.eventDAO.AppendTx(tx, store.AIRunEvent{
				RunID: c.RunID, EventID: newUUIDv4(), EventType: "tool_run.timeout",
				Payload: []byte(`{"tool_run_id":"` + c.ToolRunID + `"}`),
			})
			_ = tx.Commit()
			log.Printf("tool reconciler: converged %s (run=%s) -> timeout", c.ToolRunID, c.RunID)
		} else {
			_ = tx.Rollback() // 已是终态或并发收敛，不重复
		}
	}
	return nil
}

func newUUIDv4() string {
	return store.NewUUIDv4()
}
