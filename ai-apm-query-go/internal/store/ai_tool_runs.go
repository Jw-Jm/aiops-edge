package store

import (
	"database/sql"
	"errors"
	"time"
)

// ─────────────────────────────────────────────────────────────────────────────
// AIToolRun：ai_tool_runs（P10 完整闭环 Plan C）。
// 记录 Tool 执行幂等记录（idempotency_key 唯一），重启恢复不重复执行同一次工具调用。
// ─────────────────────────────────────────────────────────────────────────────

// AIToolRun DB 实体。
type AIToolRun struct {
	ToolRunID     string
	RunID         string
	StepID        string
	TenantID      string
	ClusterID     string
	ToolName      string
	Status        string
	Input         []byte
	Result        []byte
	ErrorCode     string
	ErrorMessage  string
	DurationMS    int64
	StartedAt     *time.Time
	CompletedAt   *time.Time
	CreatedAt     time.Time
	IdempotencyKey string
}

// AIToolRunDAO 访问 ai_tool_runs 表。
type AIToolRunDAO struct{}

// Create 幂等创建 ToolRun（同 (run_id, idempotency_key) → existing）。
func (d *AIToolRunDAO) Create(t AIToolRun) (bool, error) {
	conn := GetDB()
	if conn == nil {
		return false, errors.New("mysql unavailable")
	}
	status := t.Status
	if status == "" {
		status = "pending"
	}
	_, err := conn.Exec(
		`INSERT INTO ai_tool_runs (tool_run_id, run_id, step_id, tenant_id, cluster_id,
		   tool_name, status, input_json, result_json, error_code, error_message,
		   duration_ms, started_at, completed_at, created_at, idempotency_key)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		t.ToolRunID, t.RunID, nullableStr(t.StepID), t.TenantID, t.ClusterID,
		t.ToolName, status, t.Input, t.Result, nullableStr(t.ErrorCode),
		nullableStr(t.ErrorMessage), t.DurationMS, nullableTime(t.StartedAt),
		nullableTime(t.CompletedAt), time.Now(), t.IdempotencyKey,
	)
	if err != nil {
		if isDuplicateKey(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// UpdateByIdemKey 按 (run_id, idempotency_key) 更新结果状态。
func (d *AIToolRunDAO) UpdateByIdemKey(t AIToolRun) error {
	conn := GetDB()
	if conn == nil {
		return errors.New("mysql unavailable")
	}
	_, err := conn.Exec(
		`UPDATE ai_tool_runs SET status = ?, result_json = ?, error_code = ?,
		   error_message = ?, duration_ms = ?, completed_at = ? WHERE run_id = ? AND idempotency_key = ?`,
		t.Status, t.Result, nullableStr(t.ErrorCode), nullableStr(t.ErrorMessage),
		t.DurationMS, nullableTime(t.CompletedAt), t.RunID, t.IdempotencyKey,
	)
	return err
}

// ListByRunTx 在给定事务内列出 Run 的全部 ToolRun（恢复一致性快照，P1-4）。
func (d *AIToolRunDAO) ListByRunTx(tx *sql.Tx, runID string) ([]AIToolRun, error) {
	rows, err := tx.Query(
		`SELECT tool_run_id, run_id, step_id, tenant_id, cluster_id, tool_name, status,
		   input_json, result_json, error_code, error_message, duration_ms, started_at,
		   completed_at, created_at, idempotency_key
		 FROM ai_tool_runs WHERE run_id = ? ORDER BY created_at ASC`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AIToolRun{}
	for rows.Next() {
		var t AIToolRun
		var step, errCode, errMsg sql.NullString
		var started, completed sql.NullTime
		if err := rows.Scan(&t.ToolRunID, &t.RunID, &step, &t.TenantID, &t.ClusterID,
			&t.ToolName, &t.Status, &t.Input, &t.Result, &errCode, &errMsg,
			&t.DurationMS, &started, &completed, &t.CreatedAt, &t.IdempotencyKey); err != nil {
			return nil, err
		}
		t.StepID = step.String
		t.ErrorCode = errCode.String
		t.ErrorMessage = errMsg.String
		if started.Valid {
			t.StartedAt = &started.Time
		}
		if completed.Valid {
			t.CompletedAt = &completed.Time
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// ListByRun 列出 Run 的全部 ToolRun。
func (d *AIToolRunDAO) ListByRun(runID string) ([]AIToolRun, error) {
	conn := GetDB()
	if conn == nil {
		return nil, errors.New("mysql unavailable")
	}
	rows, err := conn.Query(
		`SELECT tool_run_id, run_id, step_id, tenant_id, cluster_id, tool_name, status,
		   input_json, result_json, error_code, error_message, duration_ms, started_at,
		   completed_at, created_at, idempotency_key
		 FROM ai_tool_runs WHERE run_id = ? ORDER BY created_at ASC`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AIToolRun{}
	for rows.Next() {
		var t AIToolRun
		var step, errCode, errMsg sql.NullString
		var started, completed sql.NullTime
		if err := rows.Scan(&t.ToolRunID, &t.RunID, &step, &t.TenantID, &t.ClusterID,
			&t.ToolName, &t.Status, &t.Input, &t.Result, &errCode, &errMsg,
			&t.DurationMS, &started, &completed, &t.CreatedAt, &t.IdempotencyKey); err != nil {
			return nil, err
		}
		t.StepID = step.String
		t.ErrorCode = errCode.String
		t.ErrorMessage = errMsg.String
		if started.Valid {
			t.StartedAt = &started.Time
		}
		if completed.Valid {
			t.CompletedAt = &completed.Time
		}
		out = append(out, t)
	}
	return out, rows.Err()
}
