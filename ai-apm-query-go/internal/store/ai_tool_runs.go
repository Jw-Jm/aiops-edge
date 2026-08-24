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
	// B1（0004）：data-quality / time-window / result-limit / Lease 绑定。
	ArgsHash          string
	ExecutorID        string
	LeaseEpochAtStart int64
	DeadlineAt        *time.Time
	ObservedAt        *time.Time
	QueryWindowStart  *time.Time
	QueryWindowEnd    *time.Time
	ResultQuality     string // complete | partial | failed | none
	ResultComplete    bool
	ResultTruncated   bool
	ResultCount       int64
	ResultDigestSHA256 string
	EligibleForEvidence bool
	EvidenceConsumedAt  *time.Time
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

// CreateWithQuality 幂等创建 ToolRun 并写入 B1 data-quality / time-window / Lease 绑定字段。
// 同 (run_id, idempotency_key) → existing（不重复真实查询）。
func (d *AIToolRunDAO) CreateWithQuality(t AIToolRun) (bool, error) {
	conn := GetDB()
	if conn == nil {
		return false, errors.New("mysql unavailable")
	}
	status := t.Status
	if status == "" {
		status = "pending"
	}
	// result_quality 列 NOT NULL（0004）：创建时未定结果 → "none"（running/pending 阶段合法值）。
	quality := t.ResultQuality
	if quality == "" {
		quality = "none"
	}
	_, err := conn.Exec(
		`INSERT INTO ai_tool_runs (tool_run_id, run_id, step_id, tenant_id, cluster_id,
		   tool_name, status, input_json, result_json, error_code, error_message,
		   duration_ms, started_at, completed_at, created_at, idempotency_key,
		   args_hash, executor_id, lease_epoch_at_start, deadline_at, observed_at,
		   query_window_start, query_window_end, result_quality, result_complete,
		   result_truncated, result_count, result_digest_sha256, eligible_for_evidence)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		t.ToolRunID, t.RunID, nullableStr(t.StepID), t.TenantID, t.ClusterID,
		t.ToolName, status, t.Input, t.Result, nullableStr(t.ErrorCode),
		nullableStr(t.ErrorMessage), t.DurationMS, nullableTime(t.StartedAt),
		nullableTime(t.CompletedAt), time.Now(), t.IdempotencyKey,
		nullableStr(t.ArgsHash), nullableStr(t.ExecutorID), t.LeaseEpochAtStart,
		nullableTime(t.DeadlineAt), nullableTime(t.ObservedAt),
		nullableTime(t.QueryWindowStart), nullableTime(t.QueryWindowEnd),
		nullableStr(quality), boolInt(t.ResultComplete), boolInt(t.ResultTruncated),
		t.ResultCount, nullableStr(t.ResultDigestSHA256), boolInt(t.EligibleForEvidence),
	)
	if err != nil {
		if isDuplicateKey(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// UpdateQuality 更新 ToolRun 的 B1 data-quality / result 字段（按 tool_run_id）。
func (d *AIToolRunDAO) UpdateQuality(t AIToolRun) error {
	conn := GetDB()
	if conn == nil {
		return errors.New("mysql unavailable")
	}
	_, err := conn.Exec(
		`UPDATE ai_tool_runs SET status = ?, result_json = ?, error_code = ?, error_message = ?,
		   duration_ms = ?, completed_at = ?, observed_at = ?, result_quality = ?,
		   result_complete = ?, result_truncated = ?, result_count = ?,
		   result_digest_sha256 = ?, eligible_for_evidence = ?
		 WHERE tool_run_id = ?`,
		t.Status, t.Result, nullableStr(t.ErrorCode), nullableStr(t.ErrorMessage),
		t.DurationMS, nullableTime(t.CompletedAt), nullableTime(t.ObservedAt),
		nullableStr(t.ResultQuality), boolInt(t.ResultComplete), boolInt(t.ResultTruncated),
		t.ResultCount, nullableStr(t.ResultDigestSHA256), boolInt(t.EligibleForEvidence),
		t.ToolRunID,
	)
	return err
}

// FinishToolRunWithFencing 在统一锁序（Run -> ToolRun）下完成 ToolRun 并做 late/fencing 判定。
// 27.12：若 Run 终态 或 Run.lease_epoch != tool.lease_epoch_at_start（迟到的旧 epoch 结果），
// 则存储结果但 eligible_for_evidence=false + 返回 late=true（调用方 append TOOL_RESULT_LATE event）；
// 否则 eligible=true。
func (d *AIToolRunDAO) FinishToolRunWithFencing(tx *sql.Tx, t AIToolRun) (late bool, err error) {
	// 锁 Run（统一锁序第一）
	var runStatus string
	var runLeaseEpoch sql.NullInt64
	if err := tx.QueryRow(
		`SELECT status, lease_epoch FROM ai_runs WHERE run_id = ? FOR UPDATE`, t.RunID,
	).Scan(&runStatus, &runLeaseEpoch); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			runStatus = "" // Run 不存在 → 视为 terminal（不进入 Evidence）
		} else {
			return false, err
		}
	}
	// 锁 ToolRun 并读取其 lease_epoch_at_start
	var toolEpoch sql.NullInt64
	if err := tx.QueryRow(
		`SELECT lease_epoch_at_start FROM ai_tool_runs WHERE tool_run_id = ? FOR UPDATE`, t.ToolRunID,
	).Scan(&toolEpoch); err != nil {
		return false, err
	}
	// 判定 late/fencing：Run 终态 或 epoch 不匹配
	runTerminal := runStatus == "success" || runStatus == "partial" || runStatus == "failed" ||
		runStatus == "regressed" || runStatus == "cancelled"
	epochMismatch := runLeaseEpoch.Valid && toolEpoch.Valid && runLeaseEpoch.Int64 != toolEpoch.Int64
	if runTerminal || epochMismatch || runStatus == "" {
		late = true
	}
	eligible := 0
	if !late && t.ResultQuality == "complete" {
		eligible = 1
	}
	if _, err := tx.Exec(
		`UPDATE ai_tool_runs SET status = ?, result_json = ?, error_code = ?, error_message = ?,
		   duration_ms = ?, completed_at = ?, observed_at = ?, result_quality = ?,
		   result_complete = ?, result_truncated = ?, result_count = ?,
		   result_digest_sha256 = ?, eligible_for_evidence = ?
		 WHERE tool_run_id = ?`,
		t.Status, t.Result, nullableStr(t.ErrorCode), nullableStr(t.ErrorMessage),
		t.DurationMS, nullableTime(t.CompletedAt), nullableTime(t.ObservedAt),
		nullableStr(t.ResultQuality), boolInt(t.ResultComplete), boolInt(t.ResultTruncated),
		t.ResultCount, nullableStr(t.ResultDigestSHA256), eligible, t.ToolRunID,
	); err != nil {
		return false, err
	}
	return late, nil
}

// ScanExpiredRunning 扫描已过期仍为 running 的 ToolRun（deadline_at < DB_NOW，无反向锁查询）。
// 返回候选供 Tool Reconciler 收敛（27.13）。
func (d *AIToolRunDAO) ScanExpiredRunning(limit int) ([]AIToolRun, error) {
	conn := GetDB()
	if conn == nil {
		return nil, errors.New("mysql unavailable")
	}
	q := `SELECT tool_run_id, run_id, step_id, tenant_id, cluster_id, tool_name, status,
		   deadline_at, lease_epoch_at_start, started_at
		 FROM ai_tool_runs WHERE status = 'running' AND deadline_at IS NOT NULL
		   AND deadline_at < CURRENT_TIMESTAMP(3)`
	var rows *sql.Rows
	var err error
	if limit > 0 {
		rows, err = conn.Query(q+` ORDER BY deadline_at ASC LIMIT ?`, limit)
	} else {
		rows, err = conn.Query(q)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AIToolRun{}
	for rows.Next() {
		var t AIToolRun
		var step, deadline sql.NullString
		var leaseEpoch sql.NullInt64
		var started sql.NullTime
		if err := rows.Scan(&t.ToolRunID, &t.RunID, &step, &t.TenantID, &t.ClusterID,
			&t.ToolName, &t.Status, &deadline, &leaseEpoch, &started); err != nil {
			return nil, err
		}
		t.StepID = step.String
		if dl, err := time.Parse("2006-01-02 15:04:05.999999", deadline.String); err == nil {
			t.DeadlineAt = &dl
		}
		t.LeaseEpochAtStart = leaseEpoch.Int64
		if started.Valid {
			t.StartedAt = &started.Time
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// ConvergeToolRun 收敛一个超时 ToolRun：recheck 仍 running → 置 timeout/failed_unknown，
// eligible=false（不进入 Evidence）。统一锁序 Run -> ToolRun（27.13，避免与 Commit/Finish 相反）。
// 返回 true 表示已收敛（改变状态）；false 表示已是终态（不重复收敛）。
func (d *AIToolRunDAO) ConvergeToolRun(tx *sql.Tx, toolRunID, runID string, status, errMsg string, now time.Time) (bool, error) {
	// 锁 Run（统一锁序第一）
	if _, err := tx.Exec(`SELECT run_id FROM ai_runs WHERE run_id = ? FOR UPDATE`, runID); err != nil {
		return false, err
	}
	// 锁 ToolRun 并 recheck 仍 running
	var cur string
	if err := tx.QueryRow(
		`SELECT status FROM ai_tool_runs WHERE tool_run_id = ? FOR UPDATE`, toolRunID,
	).Scan(&cur); err != nil {
		return false, err
	}
	if cur != "running" {
		return false, nil // 已是终态，不重复收敛
	}
	_, err := tx.Exec(
		`UPDATE ai_tool_runs SET status = ?, result_quality = 'failed', error_message = ?,
		   eligible_for_evidence = 0, completed_at = ? WHERE tool_run_id = ?`,
		status, nullableStr(errMsg), now, toolRunID,
	)
	if err != nil {
		return false, err
	}
	return true, nil
}

// GetByIdemKey 按 idempotency_key 查询 ToolRun（幂等命中判断）。
func (d *AIToolRunDAO) GetByIdemKey(idemKey string) (*AIToolRun, error) {
	conn := GetDB()
	if conn == nil {
		return nil, errors.New("mysql unavailable")
	}
	row := conn.QueryRow(
		`SELECT tool_run_id, run_id, status, idempotency_key FROM ai_tool_runs WHERE idempotency_key = ?`,
		idemKey)
	var t AIToolRun
	if err := row.Scan(&t.ToolRunID, &t.RunID, &t.Status, &t.IdempotencyKey); err != nil {
		return nil, err
	}
	return &t, nil
}

// MarkEvidenceConsumed 标记 ToolRun 的 Evidence 已被消费（B1-03：一次消费，防止重复转 Evidence）。
func (d *AIToolRunDAO) MarkEvidenceConsumed(toolRunID string) error {
	conn := GetDB()
	if conn == nil {
		return errors.New("mysql unavailable")
	}
	_, err := conn.Exec(
		`UPDATE ai_tool_runs SET evidence_consumed_at = ? WHERE tool_run_id = ? AND evidence_consumed_at IS NULL`,
		time.Now(), toolRunID,
	)
	return err
}

// IsEvidenceConsumed 判断 ToolRun 的 Evidence 是否已被消费。
func (d *AIToolRunDAO) IsEvidenceConsumed(toolRunID string) (bool, error) {
	conn := GetDB()
	if conn == nil {
		return false, errors.New("mysql unavailable")
	}
	var n int64
	if err := conn.QueryRow(
		`SELECT COUNT(*) FROM ai_tool_runs WHERE tool_run_id = ? AND evidence_consumed_at IS NOT NULL`,
		toolRunID,
	).Scan(&n); err != nil {
		return false, err
	}
	return n > 0, nil
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
