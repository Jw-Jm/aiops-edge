package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

// ─────────────────────────────────────────────────────────────────────────────
// A1（0004_runtime_convergence）：Runtime Commit 幂等记录。
//
// 合同（报告 §18.2 / 21.2）：
//   - ai_runtime_commits 以 (run_id, commit_id) 为主键。
//   - Runtime Commit 成功写入 commit 后，若响应丢失，调用方用相同 commit_id 重试
//     → 必须返回首次提交的结果（response_json），不得重复执行（幂等）。
//   - committed_state_version 是提交时的 Run state_version（fencing：old epoch/终态后不提交）。
//   - first/last_event_sequence 记录本次 commit 原子追加的事件区间。
//
// Runtime Commit 由 control-plane 的 commit 端点（A1-03）在同一事务内：
//   commit 记录 + Run 状态推进 + 事件 AppendTx（原子，不留下孤立 event/sequence）。
// ─────────────────────────────────────────────────────────────────────────────

// RuntimeCommit DB 实体。
type RuntimeCommit struct {
	RunID                 string
	CommitID              string
	PayloadHash           string
	CommittedStateVersion int64
	ResultStatus          string
	FirstEventSequence    int64
	LastEventSequence     int64
	ResponseJSON          []byte
	CreatedAt             time.Time
}

// RuntimeCommitDAO 访问 ai_runtime_commits 表。
type RuntimeCommitDAO struct{}

// Get 按 (run_id, commit_id) 读取既有 commit（幂等命中返回首次结果）。不存在 → (nil, ErrCommitNotFound)。
func (d *RuntimeCommitDAO) Get(runID, commitID string) (*RuntimeCommit, error) {
	conn := GetDB()
	if conn == nil {
		return nil, errors.New("mysql unavailable")
	}
	var c RuntimeCommit
	var firstSeq, lastSeq sql.NullInt64
	err := conn.QueryRow(
		`SELECT run_id, commit_id, payload_hash, committed_state_version, result_status,
		   first_event_sequence, last_event_sequence, response_json, created_at
		 FROM ai_runtime_commits WHERE run_id = ? AND commit_id = ?`, runID, commitID,
	).Scan(&c.RunID, &c.CommitID, &c.PayloadHash, &c.CommittedStateVersion, &c.ResultStatus,
		&firstSeq, &lastSeq, &c.ResponseJSON, &c.CreatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrCommitNotFound
		}
		return nil, err
	}
	c.FirstEventSequence = firstSeq.Int64
	c.LastEventSequence = lastSeq.Int64
	return &c, nil
}

// GetTx 在给定事务内读取既有 commit（P0-COMMIT-02：in-tx recheck commit_id + payload hash）。
func (d *RuntimeCommitDAO) GetTx(tx *sql.Tx, runID, commitID string) (*RuntimeCommit, error) {
	var c RuntimeCommit
	var firstSeq, lastSeq sql.NullInt64
	err := tx.QueryRow(
		`SELECT run_id, commit_id, payload_hash, committed_state_version, result_status,
		   first_event_sequence, last_event_sequence, response_json, created_at
		 FROM ai_runtime_commits WHERE run_id = ? AND commit_id = ? FOR UPDATE`, runID, commitID,
	).Scan(&c.RunID, &c.CommitID, &c.PayloadHash, &c.CommittedStateVersion, &c.ResultStatus,
		&firstSeq, &lastSeq, &c.ResponseJSON, &c.CreatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrCommitNotFound
		}
		return nil, err
	}
	c.FirstEventSequence = firstSeq.Int64
	c.LastEventSequence = lastSeq.Int64
	return &c, nil
}

// CreateTx 在给定事务内插入 commit 记录（幂等：同 (run_id, commit_id) 冲突返回 ErrCommitDuplicate）。
// 供 commit 端点与 Run 状态推进 + 事件 AppendTx 同事务使用。
func (d *RuntimeCommitDAO) CreateTx(tx *sql.Tx, c RuntimeCommit) error {
	now := time.Now()
	_, err := tx.Exec(
		`INSERT INTO ai_runtime_commits (run_id, commit_id, payload_hash, committed_state_version,
		   result_status, first_event_sequence, last_event_sequence, response_json, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		c.RunID, c.CommitID, c.PayloadHash, c.CommittedStateVersion, c.ResultStatus,
		nullableInt(c.FirstEventSequence), nullableInt(c.LastEventSequence), c.ResponseJSON, now,
	)
	if err != nil {
		if isDuplicateKey(err) {
			return ErrCommitDuplicate
		}
		return err
	}
	return nil
}

// MarshalCommitResponse 把 commit 结果序列化为 response_json（供响应丢失重试返回）。
func MarshalCommitResponse(status string, payload interface{}) []byte {
	b, err := json.Marshal(payload)
	if err != nil {
		return []byte(`{"error":"serialize_commit_response"}`)
	}
	_ = status
	return b
}

var (
	ErrCommitNotFound  = errors.New("runtime commit not found")
	ErrCommitDuplicate = errors.New("runtime commit already exists")
)

// nullableInt 把 0 值转为 NULL（避免默认 0 覆盖真实 sequence 语义；非 0 原样传）。
func nullableInt(v int64) interface{} {
	if v == 0 {
		return nil
	}
	return v
}
