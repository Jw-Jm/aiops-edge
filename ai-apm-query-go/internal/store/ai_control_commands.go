package store

import (
	"database/sql"
	"errors"
	"time"
)

// ─────────────────────────────────────────────────────────────────────────────
// AIControlCommand：ai_control_commands（P10 完整闭环 Plan C）。
// control command 幂等持久化（command_id PK + (run_id, idempotency_key) UNIQUE），
// 重启恢复不重复执行 control command。
// ─────────────────────────────────────────────────────────────────────────────

// AIControlCommand DB 实体。
type AIControlCommand struct {
	CommandID      string
	RunID          string
	Operation      string
	Payload        []byte
	Status         string
	IdempotencyKey string
	CreatedAt      time.Time
}

// AIControlCommandDAO 访问 ai_control_commands 表。
type AIControlCommandDAO struct{}

// Create 幂等创建 control command（同 (run_id, idempotency_key) → existing）。
func (d *AIControlCommandDAO) Create(c AIControlCommand) (bool, error) {
	conn := GetDB()
	if conn == nil {
		return false, errors.New("mysql unavailable")
	}
	status := c.Status
	if status == "" {
		status = "pending"
	}
	_, err := conn.Exec(
		`INSERT INTO ai_control_commands (command_id, run_id, operation, payload_json,
		   status, idempotency_key, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		c.CommandID, c.RunID, c.Operation, c.Payload, status, c.IdempotencyKey, time.Now(),
	)
	if err != nil {
		if isDuplicateKey(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// MarkDone 标记 control command 为 done（迁移/取消成功落库后调用，供响应丢失重放返回首次结果）。
func (d *AIControlCommandDAO) MarkDone(commandID string) error {
	conn := GetDB()
	if conn == nil {
		return errors.New("mysql unavailable")
	}
	_, err := conn.Exec(
		`UPDATE ai_control_commands SET status = 'done' WHERE command_id = ?`,
		commandID)
	return err
}

// Get 按 command_id 读取。
func (d *AIControlCommandDAO) Get(commandID string) (*AIControlCommand, error) {
	conn := GetDB()
	if conn == nil {
		return nil, errors.New("mysql unavailable")
	}
	var c AIControlCommand
	err := conn.QueryRow(
		`SELECT command_id, run_id, operation, payload_json, status, idempotency_key, created_at
		 FROM ai_control_commands WHERE command_id = ?`, commandID,
	).Scan(&c.CommandID, &c.RunID, &c.Operation, &c.Payload, &c.Status, &c.IdempotencyKey, &c.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// ListByRunTx 在给定事务内列出 Run 的全部 control commands（恢复一致性快照，P1-4）。
func (d *AIControlCommandDAO) ListByRunTx(tx *sql.Tx, runID string) ([]AIControlCommand, error) {
	rows, err := tx.Query(
		`SELECT command_id, run_id, operation, payload_json, status, idempotency_key, created_at
		 FROM ai_control_commands WHERE run_id = ? ORDER BY created_at ASC`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AIControlCommand{}
	for rows.Next() {
		var c AIControlCommand
		if err := rows.Scan(&c.CommandID, &c.RunID, &c.Operation, &c.Payload, &c.Status,
			&c.IdempotencyKey, &c.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ListByRun 列出 Run 的全部 control commands。
func (d *AIControlCommandDAO) ListByRun(runID string) ([]AIControlCommand, error) {
	conn := GetDB()
	if conn == nil {
		return nil, errors.New("mysql unavailable")
	}
	rows, err := conn.Query(
		`SELECT command_id, run_id, operation, payload_json, status, idempotency_key, created_at
		 FROM ai_control_commands WHERE run_id = ? ORDER BY created_at ASC`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AIControlCommand{}
	for rows.Next() {
		var c AIControlCommand
		if err := rows.Scan(&c.CommandID, &c.RunID, &c.Operation, &c.Payload, &c.Status,
			&c.IdempotencyKey, &c.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
