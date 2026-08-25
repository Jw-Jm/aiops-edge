package store

import (
	"database/sql"
	"errors"
	"time"
)

// ─────────────────────────────────────────────────────────────────────────────
// AIApprovalDecision：ai_approval_decisions（V9.3 Phase 11，P10 完整闭环 Phase D）。
// 审批决定持久化（approval_id PK + action_hash 绑定，供审批记录 durable 与恢复）。
// ─────────────────────────────────────────────────────────────────────────────

// AIApprovalDecision DB 实体。
type AIApprovalDecision struct {
	ApprovalID             string
	RunID                  string
	ActionID               string
	ActionHash             string
	ActionVersion          int64
	DecisionIdempotencyKey string
	TenantID               string
	ClusterID              string
	Decision               string // pending|approved|rejected|self_denied|cross_cluster_denied
	Approver               string
	Reason                 string
	DecidedAt              *time.Time
	CreatedAt              time.Time
}

// AIApprovalDecisionDAO 访问 ai_approval_decisions 表。
type AIApprovalDecisionDAO struct{}

// Create 幂等创建审批决定。
func (d *AIApprovalDecisionDAO) Create(a AIApprovalDecision) (bool, error) {
	conn := GetDB()
	if conn == nil {
		return false, errors.New("mysql unavailable")
	}
	decision := a.Decision
	if decision == "" {
		decision = "pending"
	}
	_, err := conn.Exec(
		`INSERT INTO ai_approval_decisions (approval_id, run_id, action_id, action_hash,
		   action_version, decision_idempotency_key, tenant_id, cluster_id, decision, approver, reason, decided_at, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		a.ApprovalID, a.RunID, a.ActionID, a.ActionHash,
		nullableInt64(a.ActionVersion), nullableStr(a.DecisionIdempotencyKey), a.TenantID, a.ClusterID,
		decision, a.Approver, nullableStr(a.Reason), nullableTime(a.DecidedAt), time.Now(),
	)
	if err != nil {
		if isDuplicateKey(err) {
			var existingHash string
			var existingVersion sql.NullInt64
			var existingKey sql.NullString
			if lookupErr := conn.QueryRow(`SELECT action_hash, action_version,
				decision_idempotency_key FROM ai_approval_decisions WHERE approval_id = ?`, a.ApprovalID).
				Scan(&existingHash, &existingVersion, &existingKey); lookupErr != nil {
				return false, lookupErr
			}
			if existingHash != a.ActionHash || existingVersion.Int64 != a.ActionVersion || existingKey.String != a.DecisionIdempotencyKey {
				return false, ErrIdempotencyPayloadMismatch
			}
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func nullableInt64(value int64) interface{} {
	if value == 0 {
		return nil
	}
	return value
}

// ListByRunTx 在给定事务内列出 Run 的审批决定（恢复一致性快照）。
func (d *AIApprovalDecisionDAO) ListByRunTx(tx *sql.Tx, runID string) ([]AIApprovalDecision, error) {
	rows, err := tx.Query(
		`SELECT approval_id, run_id, action_id, action_hash, tenant_id, cluster_id,
		   decision, approver, reason, decided_at, created_at
		 FROM ai_approval_decisions WHERE run_id = ? ORDER BY created_at ASC`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AIApprovalDecision{}
	for rows.Next() {
		var a AIApprovalDecision
		var reason sql.NullString
		var decided sql.NullTime
		if err := rows.Scan(&a.ApprovalID, &a.RunID, &a.ActionID, &a.ActionHash, &a.TenantID,
			&a.ClusterID, &a.Decision, &a.Approver, &reason, &decided, &a.CreatedAt); err != nil {
			return nil, err
		}
		a.Reason = reason.String
		if decided.Valid {
			a.DecidedAt = &decided.Time
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// ListByRun lists approvals for a public Run aggregate view.
func (d *AIApprovalDecisionDAO) ListByRun(runID string) ([]AIApprovalDecision, error) {
	conn := GetDB()
	if conn == nil {
		return nil, errors.New("mysql unavailable")
	}
	rows, err := conn.Query(
		`SELECT approval_id, run_id, action_id, action_hash, tenant_id, cluster_id,
		   decision, approver, reason, decided_at, created_at
		 FROM ai_approval_decisions WHERE run_id = ? ORDER BY created_at ASC`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AIApprovalDecision{}
	for rows.Next() {
		var a AIApprovalDecision
		var reason sql.NullString
		var decided sql.NullTime
		if err := rows.Scan(&a.ApprovalID, &a.RunID, &a.ActionID, &a.ActionHash, &a.TenantID,
			&a.ClusterID, &a.Decision, &a.Approver, &reason, &decided, &a.CreatedAt); err != nil {
			return nil, err
		}
		a.Reason = reason.String
		if decided.Valid {
			a.DecidedAt = &decided.Time
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// GetApprovedApproval 返回该 action 的最新 approved 审批决定（Stage D 执行前置条件）。
// 用于 query-api 签发 ActionExecutionContext 前确认该 action 已被 approved。
func (d *AIApprovalDecisionDAO) GetApprovedApproval(actionID string) (*AIApprovalDecision, error) {
	conn := GetDB()
	if conn == nil {
		return nil, errors.New("mysql unavailable")
	}
	var a AIApprovalDecision
	var reason sql.NullString
	var decided sql.NullTime
	err := conn.QueryRow(
		`SELECT approval_id, run_id, action_id, action_hash, tenant_id, cluster_id,
		   decision, approver, reason, decided_at, created_at
		 FROM ai_approval_decisions WHERE action_id = ? ORDER BY created_at DESC LIMIT 1`, actionID).
		Scan(&a.ApprovalID, &a.RunID, &a.ActionID, &a.ActionHash, &a.TenantID,
			&a.ClusterID, &a.Decision, &a.Approver, &reason, &decided, &a.CreatedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	a.Reason = reason.String
	if decided.Valid {
		t := decided.Time
		a.DecidedAt = &t
	}
	return &a, nil
}
