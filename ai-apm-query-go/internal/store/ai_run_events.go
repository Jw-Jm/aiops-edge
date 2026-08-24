package store

import (
	"database/sql"
	"errors"
	"time"
)

// ─────────────────────────────────────────────────────────────────────────────
// AIRunEvent：ai_run_events 事件（P10 完整闭环 Plan B）。
// query-api 是事件持久化 + replay owner。
//
// Append 事务顺序（评审钦定，D3）：
//   1. 先锁 Run sequence owner：UPDATE ai_runs SET last_event_sequence=last_event_sequence+1
//   2. 再查相同 event_id 是否已存在（幂等：响应丢失重试返回首次结果，不追加）
//   3. 分配 sequence（= owner 新值）并 INSERT
// 单调不争抢（owner 行锁）+ event_id 唯一（去重）。
// ─────────────────────────────────────────────────────────────────────────────

// AIRunEvent DB 实体。
type AIRunEvent struct {
	EventID   string
	RunID     string
	Sequence  int64
	EventType string
	Payload   []byte
	CreatedAt time.Time
}

// AIRunEventDAO 访问 ai_run_events 表。
type AIRunEventDAO struct{}

// Append 幂等追加事件，返回 (event, created, error)。created=false 表示已存在（响应丢失重试命中）。
// P1-2 修正：先查 event_id（幂等）再锁 sequence owner 递增，避免重复请求制造 sequence gap。
func (d *AIRunEventDAO) Append(ev AIRunEvent) (AIRunEvent, bool, error) {
	conn := GetDB()
	if conn == nil {
		return AIRunEvent{}, false, errors.New("mysql unavailable")
	}
	tx, err := conn.Begin()
	if err != nil {
		return AIRunEvent{}, false, err
	}
	defer tx.Rollback()

	// 1) 幂等：同 event_id 已存在 → 返回既有事件（含真实 sequence），不递增 sequence，无 gap。
	var existingSeq int64
	err = tx.QueryRow(
		`SELECT sequence FROM ai_run_events WHERE run_id = ? AND event_id = ?`,
		ev.RunID, ev.EventID,
	).Scan(&existingSeq)
	if err == nil {
		_ = tx.Commit()
		prev := AIRunEvent{EventID: ev.EventID, RunID: ev.RunID, Sequence: existingSeq, EventType: ev.EventType}
		return prev, false, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return AIRunEvent{}, false, err
	}

	// 2) 锁 Run sequence owner，原子递增分配 sequence。
	var seq int64
	if _, err := tx.Exec(
		`UPDATE ai_runs SET last_event_sequence = last_event_sequence + 1 WHERE run_id = ?`,
		ev.RunID,
	); err != nil {
		return AIRunEvent{}, false, err
	}
	if err := tx.QueryRow(`SELECT last_event_sequence FROM ai_runs WHERE run_id = ?`, ev.RunID).Scan(&seq); err != nil {
		return AIRunEvent{}, false, err
	}

	// 3) 插入（sequence = owner 新值）。并发同 event_id 竞态由 UNIQUE(run_id,event_id) 兜底：
	//    命中重复键 → 回滚 sequence 递增并返回既有。
	now := time.Now()
	if _, err := tx.Exec(
		`INSERT INTO ai_run_events (run_id, sequence, event_id, event_type, payload_json, created_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		ev.RunID, seq, ev.EventID, ev.EventType, ev.Payload, now,
	); err != nil {
		if isDuplicateKey(err) {
			_ = tx.Rollback()
			var existing int64
			_ = conn.QueryRow(
				`SELECT sequence FROM ai_run_events WHERE run_id = ? AND event_id = ?`,
				ev.RunID, ev.EventID,
			).Scan(&existing)
			prev := AIRunEvent{EventID: ev.EventID, RunID: ev.RunID, Sequence: existing, EventType: ev.EventType}
			return prev, false, nil
		}
		return AIRunEvent{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return AIRunEvent{}, false, err
	}
	ev.Sequence = seq
	ev.CreatedAt = now
	return ev, true, nil
}

// ReplayAfter 返回 sequence 严格大于 afterSeq 的事件（升序），用于重启恢复/SSE replay。
func (d *AIRunEventDAO) ReplayAfter(runID string, afterSeq int64) ([]AIRunEvent, error) {
	conn := GetDB()
	if conn == nil {
		return nil, errors.New("mysql unavailable")
	}
	rows, err := conn.Query(
		`SELECT run_id, sequence, event_id, event_type, payload_json, created_at
		 FROM ai_run_events WHERE run_id = ? AND sequence > ? ORDER BY sequence ASC`,
		runID, afterSeq,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AIRunEvent{}
	for rows.Next() {
		var e AIRunEvent
		if err := rows.Scan(&e.RunID, &e.Sequence, &e.EventID, &e.EventType, &e.Payload, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// AppendTx 在给定事务内幂等追加事件（供 Runtime Commit 与 control-command 同事务使用，
// 保证"事件+状态+commit"原子，不会留下孤立 event/sequence）。
// 与 Append 相同的 owner-lock 顺序 + event_id 幂等去重，但所有操作在外部 tx 内。
func (d *AIRunEventDAO) AppendTx(tx *sql.Tx, ev AIRunEvent) (AIRunEvent, bool, error) {
	// 1) 幂等：同 event_id 已存在 → 返回既有事件（含真实 sequence），不递增 sequence，无 gap。
	var existingSeq int64
	err := tx.QueryRow(
		`SELECT sequence FROM ai_run_events WHERE run_id = ? AND event_id = ?`,
		ev.RunID, ev.EventID,
	).Scan(&existingSeq)
	if err == nil {
		prev := AIRunEvent{EventID: ev.EventID, RunID: ev.RunID, Sequence: existingSeq, EventType: ev.EventType}
		return prev, false, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return AIRunEvent{}, false, err
	}

	// 2) 锁 Run sequence owner，原子递增分配 sequence。
	var seq int64
	if _, err := tx.Exec(
		`UPDATE ai_runs SET last_event_sequence = last_event_sequence + 1 WHERE run_id = ?`,
		ev.RunID,
	); err != nil {
		return AIRunEvent{}, false, err
	}
	if err := tx.QueryRow(`SELECT last_event_sequence FROM ai_runs WHERE run_id = ?`, ev.RunID).Scan(&seq); err != nil {
		return AIRunEvent{}, false, err
	}

	// 3) 插入。并发同 event_id 竞态由 UNIQUE(run_id,event_id) 兜底（调用方事务回滚不留下 sequence 推进）。
	now := time.Now()
	if _, err := tx.Exec(
		`INSERT INTO ai_run_events (run_id, sequence, event_id, event_type, payload_json, created_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		ev.RunID, seq, ev.EventID, ev.EventType, ev.Payload, now,
	); err != nil {
		if isDuplicateKey(err) {
			// 竞态：另一并发已写入 → 查回既有 sequence（不推进）。
			var existing int64
			_ = tx.QueryRow(
				`SELECT sequence FROM ai_run_events WHERE run_id = ? AND event_id = ?`,
				ev.RunID, ev.EventID,
			).Scan(&existing)
			prev := AIRunEvent{EventID: ev.EventID, RunID: ev.RunID, Sequence: existing, EventType: ev.EventType}
			return prev, false, nil
		}
		return AIRunEvent{}, false, err
	}
	ev.Sequence = seq
	ev.CreatedAt = now
	return ev, true, nil
}

// LastSequenceTx 在给定事务内返回 Run 的当前最后事件 sequence（恢复一致性快照，P1-4）。
func (d *AIRunEventDAO) LastSequenceTx(tx *sql.Tx, runID string) (int64, error) {
	var seq int64
	if err := tx.QueryRow(`SELECT last_event_sequence FROM ai_runs WHERE run_id = ?`, runID).Scan(&seq); err != nil {
		return 0, err
	}
	return seq, nil
}

// LastSequence 返回 Run 的当前最后事件 sequence。
func (d *AIRunEventDAO) LastSequence(runID string) (int64, error) {
	conn := GetDB()
	if conn == nil {
		return 0, errors.New("mysql unavailable")
	}
	var seq int64
	if err := conn.QueryRow(`SELECT last_event_sequence FROM ai_runs WHERE run_id = ?`, runID).Scan(&seq); err != nil {
		return 0, err
	}
	return seq, nil
}
