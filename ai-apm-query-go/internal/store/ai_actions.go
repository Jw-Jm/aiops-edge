package store

import (
	"database/sql"
	"errors"
	"time"
)

// ─────────────────────────────────────────────────────────────────────────────
// AIAction：ai_actions（P10 完整闭环 Plan C）。
// Action 执行幂等记录（idempotency_key 唯一约束），重启恢复不重复执行。
// ─────────────────────────────────────────────────────────────────────────────

// AIAction DB 实体。
type AIAction struct {
	ActionID          string
	RunID             string
	TenantID          string
	ClusterID         string
	ActionType        string
	ActionHash        string
	IdempotencyKey    string
	ProposedRisk      string
	AuthoritativeRisk string
	Status            string
	DryRun            bool
	Params            []byte
	Result            []byte
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

// AIActionDAO 访问 ai_actions 表。
type AIActionDAO struct{}

// Create 幂等创建 Action（同 (run_id, idempotency_key) → existing）。
func (d *AIActionDAO) Create(a AIAction) (bool, error) {
	conn := GetDB()
	if conn == nil {
		return false, errors.New("mysql unavailable")
	}
	dryRun := 1
	if !a.DryRun {
		dryRun = 0
	}
	_, err := conn.Exec(
		`INSERT INTO ai_actions (action_id, run_id, tenant_id, cluster_id, action_type,
		   action_hash, idempotency_key, proposed_risk, authoritative_risk, status, dry_run,
		   params_json, result_json, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		a.ActionID, a.RunID, a.TenantID, a.ClusterID, a.ActionType, a.ActionHash,
		a.IdempotencyKey, firstNonEmptyStr2(a.ProposedRisk, "R0"), firstNonEmptyStr2(a.AuthoritativeRisk, "R0"),
		firstNonEmptyStr2(a.Status, "proposed"), dryRun, a.Params, a.Result, time.Now(), time.Now(),
	)
	if err != nil {
		if isDuplicateKey(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// UpdateByIdemKey 按 (run_id, idempotency_key) 更新状态/结果。
func (d *AIActionDAO) UpdateByIdemKey(a AIAction) error {
	conn := GetDB()
	if conn == nil {
		return errors.New("mysql unavailable")
	}
	_, err := conn.Exec(
		`UPDATE ai_actions SET status = ?, result_json = ?, updated_at = ? WHERE run_id = ? AND idempotency_key = ?`,
		a.Status, a.Result, time.Now(), a.RunID, a.IdempotencyKey,
	)
	return err
}

// ListByRunTx 在给定事务内列出 Run 的全部 Action（恢复一致性快照，P1-4）。
func (d *AIActionDAO) ListByRunTx(tx *sql.Tx, runID string) ([]AIAction, error) {
	rows, err := tx.Query(
		`SELECT action_id, run_id, tenant_id, cluster_id, action_type, action_hash,
		   idempotency_key, proposed_risk, authoritative_risk, status, dry_run,
		   params_json, result_json, created_at, updated_at
		 FROM ai_actions WHERE run_id = ? ORDER BY created_at ASC`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AIAction{}
	for rows.Next() {
		var a AIAction
		var dryRun int
		if err := rows.Scan(&a.ActionID, &a.RunID, &a.TenantID, &a.ClusterID, &a.ActionType,
			&a.ActionHash, &a.IdempotencyKey, &a.ProposedRisk, &a.AuthoritativeRisk,
			&a.Status, &dryRun, &a.Params, &a.Result, &a.CreatedAt, &a.UpdatedAt); err != nil {
			return nil, err
		}
		a.DryRun = dryRun == 1
		out = append(out, a)
	}
	return out, rows.Err()
}

// ListByRun 列出 Run 的全部 Action。
func (d *AIActionDAO) ListByRun(runID string) ([]AIAction, error) {
	conn := GetDB()
	if conn == nil {
		return nil, errors.New("mysql unavailable")
	}
	rows, err := conn.Query(
		`SELECT action_id, run_id, tenant_id, cluster_id, action_type, action_hash,
		   idempotency_key, proposed_risk, authoritative_risk, status, dry_run,
		   params_json, result_json, created_at, updated_at
		 FROM ai_actions WHERE run_id = ? ORDER BY created_at ASC`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AIAction{}
	for rows.Next() {
		var a AIAction
		var dryRun int
		if err := rows.Scan(&a.ActionID, &a.RunID, &a.TenantID, &a.ClusterID, &a.ActionType,
			&a.ActionHash, &a.IdempotencyKey, &a.ProposedRisk, &a.AuthoritativeRisk,
			&a.Status, &dryRun, &a.Params, &a.Result, &a.CreatedAt, &a.UpdatedAt); err != nil {
			return nil, err
		}
		a.DryRun = dryRun == 1
		out = append(out, a)
	}
	return out, rows.Err()
}

func firstNonEmptyStr2(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
