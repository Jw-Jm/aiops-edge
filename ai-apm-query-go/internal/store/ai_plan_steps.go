package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

// ─────────────────────────────────────────────────────────────────────────────
// AIPlanStep：ai_plan_steps（P10 完整闭环 Plan C）。
// 记录 Plan DAG 与每步运行态（depends_on/parameters/attempt/outcome/result_ref），
// 供 orchestrator 重启后重建可继续步骤（不重复 Tool/Action）。
// ─────────────────────────────────────────────────────────────────────────────

// AIPlanStep DB 实体。
type AIPlanStep struct {
	StepID      string
	RunID       string
	ParentStepID string
	Seq         int
	StepType    string
	Status      string
	ClusterID   string
	Description string
	BudgetUsed  int
	DependsOn   []string
	Parameters  []byte
	Attempt     int
	Outcome     string
	ResultRef   string
	StartedAt   *time.Time
	CompletedAt *time.Time
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// AIPlanStepDAO 访问 ai_plan_steps 表。
type AIPlanStepDAO struct{}

// Create 幂等创建 plan step。
func (d *AIPlanStepDAO) Create(s AIPlanStep) (bool, error) {
	conn := GetDB()
	if conn == nil {
		return false, errors.New("mysql unavailable")
	}
	depends, _ := json.Marshal(s.DependsOn)
	status := s.Status
	if status == "" {
		status = "pending"
	}
	_, err := conn.Exec(
		`INSERT INTO ai_plan_steps (step_id, run_id, parent_step_id, seq, step_type, status,
		   cluster_id, description, budget_used, depends_on, parameters, attempt, outcome,
		   result_ref, started_at, completed_at, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		s.StepID, s.RunID, nullableStr(s.ParentStepID), s.Seq, s.StepType,
		status, nullableStr(s.ClusterID), s.Description,
		s.BudgetUsed, depends, s.Parameters, s.Attempt, nullableStr(s.Outcome),
		nullableStr(s.ResultRef), nullableTime(s.StartedAt), nullableTime(s.CompletedAt),
		time.Now(), time.Now(),
	)
	if err != nil {
		if isDuplicateKey(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// Update 更新步骤运行态（status/outcome/attempt/result_ref/completed_at）。
func (d *AIPlanStepDAO) Update(s AIPlanStep) error {
	conn := GetDB()
	if conn == nil {
		return errors.New("mysql unavailable")
	}
	_, err := conn.Exec(
		`UPDATE ai_plan_steps SET status = ?, outcome = ?, attempt = ?, result_ref = ?,
		   completed_at = ?, updated_at = ? WHERE step_id = ?`,
		s.Status, nullableStr(s.Outcome), s.Attempt, nullableStr(s.ResultRef),
		nullableTime(s.CompletedAt), time.Now(), s.StepID,
	)
	return err
}

// ListByRunTx 在给定事务内列出 Run 的全部 plan steps（恢复一致性快照，P1-4）。
func (d *AIPlanStepDAO) ListByRunTx(tx *sql.Tx, runID string) ([]AIPlanStep, error) {
	rows, err := tx.Query(
		`SELECT step_id, run_id, parent_step_id, seq, step_type, status, cluster_id,
		   description, budget_used, depends_on, parameters, attempt, outcome, result_ref,
		   started_at, completed_at, created_at, updated_at
		 FROM ai_plan_steps WHERE run_id = ? ORDER BY seq ASC`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AIPlanStep{}
	for rows.Next() {
		var s AIPlanStep
		var parent, cluster, outcome, resultRef sql.NullString
		var depends, params []byte
		var started, completed sql.NullTime
		if err := rows.Scan(&s.StepID, &s.RunID, &parent, &s.Seq, &s.StepType, &s.Status,
			&cluster, &s.Description, &s.BudgetUsed, &depends, &params, &s.Attempt,
			&outcome, &resultRef, &started, &completed, &s.CreatedAt, &s.UpdatedAt); err != nil {
			return nil, err
		}
		s.ParentStepID = parent.String
		s.ClusterID = cluster.String
		s.Outcome = outcome.String
		s.ResultRef = resultRef.String
		_ = json.Unmarshal(depends, &s.DependsOn)
		s.Parameters = params
		if started.Valid {
			s.StartedAt = &started.Time
		}
		if completed.Valid {
			s.CompletedAt = &completed.Time
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// ListByRun 列出 Run 的全部 plan steps（按 seq 升序）。
func (d *AIPlanStepDAO) ListByRun(runID string) ([]AIPlanStep, error) {
	conn := GetDB()
	if conn == nil {
		return nil, errors.New("mysql unavailable")
	}
	rows, err := conn.Query(
		`SELECT step_id, run_id, parent_step_id, seq, step_type, status, cluster_id,
		   description, budget_used, depends_on, parameters, attempt, outcome, result_ref,
		   started_at, completed_at, created_at, updated_at
		 FROM ai_plan_steps WHERE run_id = ? ORDER BY seq ASC`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AIPlanStep{}
	for rows.Next() {
		var s AIPlanStep
		var parent, cluster, outcome, resultRef sql.NullString
		var depends, params []byte
		var started, completed sql.NullTime
		if err := rows.Scan(&s.StepID, &s.RunID, &parent, &s.Seq, &s.StepType, &s.Status,
			&cluster, &s.Description, &s.BudgetUsed, &depends, &params, &s.Attempt,
			&outcome, &resultRef, &started, &completed, &s.CreatedAt, &s.UpdatedAt); err != nil {
			return nil, err
		}
		s.ParentStepID = parent.String
		s.ClusterID = cluster.String
		s.Outcome = outcome.String
		s.ResultRef = resultRef.String
		_ = json.Unmarshal(depends, &s.DependsOn)
		s.Parameters = params
		if started.Valid {
			s.StartedAt = &started.Time
		}
		if completed.Valid {
			s.CompletedAt = &completed.Time
		}
		out = append(out, s)
	}
	return out, rows.Err()
}
