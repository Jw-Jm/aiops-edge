package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// ─────────────────────────────────────────────────────────────────────────────
// AIControlCommand：ai_control_commands（P10 完整闭环 Plan C + 生产收敛 A0-01）。
// control command 幂等持久化（command_id PK + (run_id, idempotency_key) UNIQUE），
// 重启恢复不重复执行 control command。
//
// A0-01（生产收敛）：补齐真正幂等语义——
//   - payload_hash：稳定业务语义 hash（run_id+operation+expected_version+target 等），
//     不含 Authorization/Trusted Context nonce/HTTP 时间戳；
//   - response_json：首次成功 response，响应丢失后重放返回它；
//   - completed_at：command 完成时间。
//   - 新增 ApplyRunControlCommandTx：command 记录 + Run CAS/state mutation 同一事务。
// ─────────────────────────────────────────────────────────────────────────────

// AIControlCommand DB 实体。
type AIControlCommand struct {
	CommandID      string
	RunID          string
	Operation      string
	Payload        []byte
	PayloadHash    string
	ResponseJSON   []byte
	Status         string
	CompletedAt    *time.Time
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
		   payload_hash, response_json, status, idempotency_key, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		c.CommandID, c.RunID, c.Operation, c.Payload, c.PayloadHash,
		c.ResponseJSON, status, c.IdempotencyKey, time.Now(),
	)
	if err != nil {
		if isDuplicateKey(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// CreateTx 在给定事务内幂等创建 control command。
func (d *AIControlCommandDAO) CreateTx(tx *sql.Tx, c AIControlCommand) (bool, error) {
	status := c.Status
	if status == "" {
		status = "pending"
	}
	_, err := tx.Exec(
		`INSERT INTO ai_control_commands (command_id, run_id, operation, payload_json,
		   payload_hash, response_json, status, idempotency_key, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		c.CommandID, c.RunID, c.Operation, c.Payload, c.PayloadHash,
		c.ResponseJSON, status, c.IdempotencyKey, time.Now(),
	)
	if err != nil {
		if isDuplicateKey(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// UpsertDoneTx 在给定事务内以幂等 upsert 方式把 command 置为 done 并保存 response + payload hash。
// command 行可能已存在（首次请求插入的 pending）或不存在，统一收敛为 done。
func (d *AIControlCommandDAO) UpsertDoneTx(tx *sql.Tx, commandID, runID, operation, payloadHash string, response []byte) error {
	_, err := tx.Exec(
		`INSERT INTO ai_control_commands (command_id, run_id, operation, payload_json,
		   payload_hash, response_json, status, idempotency_key, created_at)
		 VALUES (?, ?, ?, NULL, ?, ?, 'done', ?, ?)
		 ON DUPLICATE KEY UPDATE
		   status = 'done', payload_hash = COALESCE(?, payload_hash),
		   response_json = ?, completed_at = ?`,
		commandID, runID, operation, payloadHash, response, commandID, time.Now(),
		payloadHash, response, time.Now(),
	)
	return err
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
	c, err := scanControlCommand(conn.QueryRow(
		`SELECT command_id, run_id, operation, payload_json, payload_hash, response_json,
		   status, idempotency_key, completed_at, created_at
		 FROM ai_control_commands WHERE command_id = ?`, commandID))
	if err != nil {
		return nil, err
	}
	return c, nil
}

// GetTx 在给定事务内按 command_id 读取。
func (d *AIControlCommandDAO) GetTx(tx *sql.Tx, commandID string) (*AIControlCommand, error) {
	c, err := scanControlCommand(tx.QueryRow(
		`SELECT command_id, run_id, operation, payload_json, payload_hash, response_json,
		   status, idempotency_key, completed_at, created_at
		 FROM ai_control_commands WHERE command_id = ?`, commandID))
	if err != nil {
		return nil, err
	}
	return c, nil
}

// ListByRunTx 在给定事务内列出 Run 的全部 control commands（恢复一致性快照，P1-4）。
func (d *AIControlCommandDAO) ListByRunTx(tx *sql.Tx, runID string) ([]AIControlCommand, error) {
	rows, err := tx.Query(
		`SELECT command_id, run_id, operation, payload_json, payload_hash, response_json,
		   status, idempotency_key, completed_at, created_at
		 FROM ai_control_commands WHERE run_id = ? ORDER BY created_at ASC`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanCommands(rows)
}

// ListByRun 列出 Run 的全部 control commands。
func (d *AIControlCommandDAO) ListByRun(runID string) ([]AIControlCommand, error) {
	conn := GetDB()
	if conn == nil {
		return nil, errors.New("mysql unavailable")
	}
	rows, err := conn.Query(
		`SELECT command_id, run_id, operation, payload_json, payload_hash, response_json,
		   status, idempotency_key, completed_at, created_at
		 FROM ai_control_commands WHERE run_id = ? ORDER BY created_at ASC`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanCommands(rows)
}

func scanCommands(rows *sql.Rows) ([]AIControlCommand, error) {
	out := []AIControlCommand{}
	for rows.Next() {
		c, err := scanControlCommand(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *c)
	}
	return out, rows.Err()
}

// scanControlCommand 统一扫描 ai_control_commands 行（处理 NULL 可空列）。
type controlCommandScanner interface {
	Scan(dest ...interface{}) error
}

func scanControlCommand(row controlCommandScanner) (*AIControlCommand, error) {
	var c AIControlCommand
	var payload, payloadHash, response, idem sql.NullString
	var completed sql.NullTime
	if err := row.Scan(&c.CommandID, &c.RunID, &c.Operation, &payload, &payloadHash,
		&response, &c.Status, &idem, &completed, &c.CreatedAt); err != nil {
		return nil, err
	}
	if payload.Valid {
		c.Payload = []byte(payload.String)
	}
	if response.Valid {
		c.ResponseJSON = []byte(response.String)
	}
	c.PayloadHash = payloadHash.String
	c.IdempotencyKey = idem.String
	if completed.Valid {
		c.CompletedAt = &completed.Time
	}
	return &c, nil
}

// ApplyRunControlCommandTx 事务化应用一个 control command（transition/cancel 共用）。
//
// 语义（A0-01）：
//   - 幂等检查：同 command_id 若已 done 且 payload_hash 一致 → 返回已有 response；
//     同 command_id 若 payload_hash 不一致 → IDEMPOTENCY_KEY_REUSED。
//   - command 记录 + Run CAS/state mutation 在同一 MySQL 事务，避免
//     "Run mutation 成功但 MarkDone 前崩溃" 或 "command 已记录但 Run 未变" 的中间态。
//   - mutateFn 在事务内执行真实 Run CAS/mutation，返回成功与否与最终 response。
//
// 返回：
//   - replayed=true 表示命中已有 done command（幂等重放，未执行 mutateFn）；
//   - err=ErrCommandIdempotencyReused 表示同 command_id 不同语义 payload；
//   - 其他 err 来自 DB/事务/mutateFn。
func ApplyRunControlCommandTx(
	ctx context.Context,
	runID string,
	commandID string,
	operation string,
	payloadHash string,
	commandDAO *AIControlCommandDAO,
	mutateFn func(tx *sql.Tx) (response []byte, ok bool, err error),
) (response []byte, replayed bool, err error) {
	conn := GetDB()
	if conn == nil {
		return nil, false, errors.New("mysql unavailable")
	}
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return nil, false, err
	}
	defer tx.Rollback()

	// 1) 幂等检查（事务内锁 command 行）。
	if commandID != "" {
		existing, gerr := commandDAO.GetTx(tx, commandID)
		if gerr == nil && existing != nil {
			if existing.Status == "done" && existing.Operation == operation {
				if existing.PayloadHash != "" && existing.PayloadHash == payloadHash {
					// 幂等重放：返回首次成功 response。
					return existing.ResponseJSON, true, nil
				}
				if existing.PayloadHash != "" && existing.PayloadHash != payloadHash {
					return nil, false, ErrCommandIdempotencyReused
				}
			}
		}
	}

	// 2) 执行真实 mutation（Run CAS + state mutation 在同一事务）。
	resp, ok, merr := mutateFn(tx)
	if merr != nil {
		return nil, false, merr
	}
	if !ok {
		return nil, false, ErrRunControlConflict
	}

	// 3) 记录 command 为 done（payload hash + response + completed_at）。
	if commandID != "" {
		if cerr := commandDAO.UpsertDoneTx(tx, commandID, runID, operation, payloadHash, resp); cerr != nil {
			return nil, false, cerr
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, false, err
	}
	return resp, false, nil
}

// ErrRunControlConflict 表示 Run CAS/state 冲突（state_version 不符 / 非法迁移 / 终态）。
var ErrRunControlConflict = errors.New("run_control_conflict")

// ErrCommandIdempotencyReused 表示同 command_id 被复用于不同语义 payload。
var ErrCommandIdempotencyReused = errors.New("command_idempotency_reused")
