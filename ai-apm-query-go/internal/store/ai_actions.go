package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// ErrIdempotencyPayloadMismatch means an idempotency key was reused with a
// different immutable payload. Callers must surface this as a conflict rather
// than returning the existing projection as if the request were a replay.
var ErrIdempotencyPayloadMismatch = errors.New("idempotency payload mismatch")

// ─────────────────────────────────────────────────────────────────────────────
// AIAction：ai_actions（P10 完整闭环 Plan C）。
// Action 执行幂等记录（idempotency_key 唯一约束），重启恢复不重复执行。
// ─────────────────────────────────────────────────────────────────────────────

// AIAction DB 实体。
type AIAction struct {
	ActionID           string
	RunID              string
	TenantID           string
	ClusterID          string
	ActionType         string
	ActionHash         string
	HashSchemaVersion  int
	ActionVersion      int64
	ProposedBy         string
	PolicyVersion      string
	PreflightStatus    string
	TargetResourceType string
	IdempotencyKey     string
	ProposedRisk       string
	AuthoritativeRisk  string
	Status             string
	DryRun             bool
	Params             []byte
	Result             []byte
	// Stage D 接线（0007）：executor 执行字段。
	TargetName      string
	TargetUID       string
	ResourceVersion string
	Namespace       string
	Operation       string
	ExecutionStatus string
	ExecutedAt      *time.Time
	ErrorCode       string
	CreatedAt       time.Time
	UpdatedAt       time.Time
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
	hashSchemaVersion := a.HashSchemaVersion
	if hashSchemaVersion == 0 {
		hashSchemaVersion = 1
	}
	actionVersion := a.ActionVersion
	if actionVersion == 0 {
		actionVersion = 1
	}
	policyVersion := firstNonEmptyStr2(a.PolicyVersion, "action-policy-v1")
	preflightStatus := firstNonEmptyStr2(a.PreflightStatus, "unresolved")
	resourceType := firstNonEmptyStr2(a.TargetResourceType, "deployment")
	_, err := conn.Exec(
		`INSERT INTO ai_actions (action_id, run_id, tenant_id, cluster_id, action_type,
		   action_hash, hash_schema_version, action_version, proposed_by, policy_version,
		   preflight_status, target_resource_type, idempotency_key, proposed_risk,
		   authoritative_risk, status, dry_run, target_name, target_uid, resource_version,
		   namespace, operation, execution_status, params_json, result_json, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		a.ActionID, a.RunID, a.TenantID, a.ClusterID, a.ActionType, a.ActionHash,
		hashSchemaVersion, actionVersion, nullableStr(a.ProposedBy), policyVersion, preflightStatus,
		resourceType, a.IdempotencyKey, firstNonEmptyStr2(a.ProposedRisk, "R0"), firstNonEmptyStr2(a.AuthoritativeRisk, "R0"),
		firstNonEmptyStr2(a.Status, "proposed"), dryRun,
		a.TargetName, a.TargetUID, a.ResourceVersion, a.Namespace, a.Operation,
		firstNonEmptyStr2(a.ExecutionStatus, "proposed"),
		a.Params, a.Result, time.Now(), time.Now(),
	)
	if err != nil {
		if isDuplicateKey(err) {
			var existingHash string
			lookupErr := conn.QueryRow(`SELECT action_hash FROM ai_actions WHERE run_id = ? AND idempotency_key = ? LIMIT 1`, a.RunID, a.IdempotencyKey).Scan(&existingHash)
			if lookupErr != nil {
				return false, fmt.Errorf("%w: existing action lookup: %v", ErrIdempotencyPayloadMismatch, lookupErr)
			}
			if existingHash != a.ActionHash {
				return false, ErrIdempotencyPayloadMismatch
			}
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
		   target_name, target_uid, resource_version, namespace, operation, execution_status,
		   params_json, result_json, executed_at, error_code, created_at, updated_at
		 FROM ai_actions WHERE run_id = ? ORDER BY created_at ASC`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AIAction{}
	for rows.Next() {
		var a AIAction
		var dryRun int
		var executedAt sql.NullTime
		if err := rows.Scan(&a.ActionID, &a.RunID, &a.TenantID, &a.ClusterID, &a.ActionType,
			&a.ActionHash, &a.IdempotencyKey, &a.ProposedRisk, &a.AuthoritativeRisk,
			&a.Status, &dryRun, &a.TargetName, &a.TargetUID, &a.ResourceVersion, &a.Namespace,
			&a.Operation, &a.ExecutionStatus, &a.Params, &a.Result, &executedAt, &a.ErrorCode,
			&a.CreatedAt, &a.UpdatedAt); err != nil {
			return nil, err
		}
		a.DryRun = dryRun == 1
		if executedAt.Valid {
			t := executedAt.Time
			a.ExecutedAt = &t
		}
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
		   target_name, target_uid, resource_version, namespace, operation, execution_status,
		   params_json, result_json, executed_at, error_code, created_at, updated_at
		 FROM ai_actions WHERE run_id = ? ORDER BY created_at ASC`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AIAction{}
	for rows.Next() {
		var a AIAction
		var dryRun int
		var executedAt sql.NullTime
		if err := rows.Scan(&a.ActionID, &a.RunID, &a.TenantID, &a.ClusterID, &a.ActionType,
			&a.ActionHash, &a.IdempotencyKey, &a.ProposedRisk, &a.AuthoritativeRisk,
			&a.Status, &dryRun, &a.TargetName, &a.TargetUID, &a.ResourceVersion, &a.Namespace,
			&a.Operation, &a.ExecutionStatus, &a.Params, &a.Result, &executedAt, &a.ErrorCode,
			&a.CreatedAt, &a.UpdatedAt); err != nil {
			return nil, err
		}
		a.DryRun = dryRun == 1
		if executedAt.Valid {
			t := executedAt.Time
			a.ExecutedAt = &t
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// GetByID 按 action_id 读取单个 Action（含 executor 执行字段）。
func (d *AIActionDAO) GetByID(actionID string) (*AIAction, error) {
	conn := GetDB()
	if conn == nil {
		return nil, errors.New("mysql unavailable")
	}
	var a AIAction
	var dryRun int
	var executedAt sql.NullTime
	err := conn.QueryRow(
		`SELECT action_id, run_id, tenant_id, cluster_id, action_type, action_hash,
		   idempotency_key, proposed_risk, authoritative_risk, status, dry_run,
		   target_name, target_uid, resource_version, namespace, operation, execution_status,
		   params_json, result_json, executed_at, error_code, created_at, updated_at
		 FROM ai_actions WHERE action_id = ?`, actionID).
		Scan(&a.ActionID, &a.RunID, &a.TenantID, &a.ClusterID, &a.ActionType,
			&a.ActionHash, &a.IdempotencyKey, &a.ProposedRisk, &a.AuthoritativeRisk,
			&a.Status, &dryRun, &a.TargetName, &a.TargetUID, &a.ResourceVersion, &a.Namespace,
			&a.Operation, &a.ExecutionStatus, &a.Params, &a.Result, &executedAt, &a.ErrorCode,
			&a.CreatedAt, &a.UpdatedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	a.DryRun = dryRun == 1
	if executedAt.Valid {
		t := executedAt.Time
		a.ExecutedAt = &t
	}
	return &a, nil
}

// UpdateExecution 持久化 executor 执行结果（durable idempotency，报告 §29）。
// 仅更新 execution_status/result_json/executed_at/error_code，不触碰 immutable 字段。
func (d *AIActionDAO) UpdateExecution(actionID, executionStatus string, result []byte, errorCode string) error {
	conn := GetDB()
	if conn == nil {
		return errors.New("mysql unavailable")
	}
	now := time.Now()
	_, err := conn.Exec(
		`UPDATE ai_actions SET execution_status = ?, result_json = ?, error_code = ?,
		   executed_at = ?, updated_at = ? WHERE action_id = ? AND execution_status NOT IN ('success','failed','rejected','rollback_required')`,
		executionStatus, result, errorCode, now, now, actionID,
	)
	return err
}

func firstNonEmptyStr2(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
