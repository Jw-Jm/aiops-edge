package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"
)

// ─── AIRun ────────────────────────────────────────────────────
// AIOps Run 持久化实体（ai_runs 表，query-go migrations 冻结 schema）。
// 语义：Run 状态机（created→planning→...→终态）+ optimistic state_version。
// orchestrator 经 query-api 持久化（P0-3：orchestrator 不直连 DB）。
//
// 权威映射：与 orchestrator contracts.Run 对齐（P10 Plan A）。
// 不可变字段（Create 后不得改写）：run_id/request_id/tenant_id/principal_type/
// principal_id/principal_type/scope_kind/primary_cluster_id/intent/action_mode/
// target_type/target_resource_id/time_range_start/time_range_end/parent_run_id/created_at。

// AIRun DB 实体。
type AIRun struct {
	RunID            string
	RequestID        string
	TenantID         string
	Principal        string // principal_id
	PrincipalType    string // user|system
	SessionID        string
	ScopeKind        string // single_cluster | multi_cluster
	PrimaryClusterID string
	Intent           string
	ActionMode       string
	TargetType       string
	TargetResourceID string
	TimeRangeStart   *time.Time
	TimeRangeEnd     *time.Time
	Status           string
	StateVersion     int64
	ParentRunID      string
	CreatedAt        time.Time
	UpdatedAt        time.Time
	FinishedAt       *time.Time
	LastEventSeq     int64
	// A1（0004）execution Lease / runtime-wait（正交化，非第二套 RunStatus）。
	LeaseOwnerID    string
	LeaseEpoch      int64
	LeaseClaimID    string
	LeaseTokenHash  string
	LeaseExpiresAt  *time.Time
	HeartbeatAt     *time.Time
	RuntimeWaitKind string // none | retry | waiting
	RetryNotBefore  *time.Time
	RetryAttempt    int
	LastFailureCode string
	RuntimeMetadata []byte // JSON
}

// AIRunDAO 访问 ai_runs 表。
type AIRunDAO struct{}

// isDuplicateKey 判断是否唯一键冲突（MySQL 1062）。
func isDuplicateKey(err error) bool {
	var myErr *mysql.MySQLError
	if errors.As(err, &myErr) && myErr.Number == 1062 {
		return true
	}
	// sqlmock 不产生真实 MySQL 错误，按字符串匹配兜底。
	return strings.Contains(strings.ToLower(err.Error()), "duplicate entry")
}

// Create 幂等创建 Run，返回 created(ok) / existing(!ok)。
// 唯一域 (tenant_id, request_id)：相同 request_id 不同不可变参数时由调用方判 fail-closed。
// 绝不通过 ON DUPLICATE KEY UPDATE 改写原 run_id。
func (d *AIRunDAO) Create(r AIRun) (bool, error) {
	conn := GetDB()
	if conn == nil {
		return false, errors.New("mysql unavailable")
	}
	scope := r.ScopeKind
	if scope == "" {
		scope = "single_cluster"
	}
	status := r.Status
	if status == "" {
		status = "created" // 权威起点（对齐 contracts.RunStatus.CREATED，非 pending）
	}
	_, err := conn.Exec(
		`INSERT INTO ai_runs (run_id, request_id, tenant_id, principal, principal_type,
		   session_id, scope_kind, primary_cluster_id, intent, action_mode,
		   target_type, target_resource_id, time_range_start, time_range_end,
		   status, state_version, parent_run_id, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		r.RunID, r.RequestID, r.TenantID, r.Principal, r.PrincipalType,
		nullableStr(r.SessionID), scope, nullableStr(r.PrimaryClusterID), r.Intent, r.ActionMode,
		nullableStr(r.TargetType), nullableStr(r.TargetResourceID),
		nullableTime(r.TimeRangeStart), nullableTime(r.TimeRangeEnd),
		status, r.StateVersion, nullableStr(r.ParentRunID), r.CreatedAt, r.UpdatedAt,
	)
	if err != nil {
		if isDuplicateKey(err) {
			return false, nil // existing（幂等）
		}
		return false, err
	}
	return true, nil
}

// CreateWithOutbox 在**同一事务**内创建 Run 并写 outbox（P0-5：outbox 写入失败则回滚，
// 避免"Run 已建但缺失 outbox → 永不派发"。返回 created(ok)/existing(!ok)）。
func (d *AIRunDAO) CreateWithOutbox(r AIRun, o AIRunOutbox) (bool, error) {
	conn := GetDB()
	if conn == nil {
		return false, errors.New("mysql unavailable")
	}
	tx, err := conn.BeginTx(context.Background(), nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	scope := r.ScopeKind
	if scope == "" {
		scope = "single_cluster"
	}
	status := r.Status
	if status == "" {
		status = "created"
	}
	_, err = tx.Exec(
		`INSERT INTO ai_runs (run_id, request_id, tenant_id, principal, principal_type,
		   session_id, scope_kind, primary_cluster_id, intent, action_mode,
		   target_type, target_resource_id, time_range_start, time_range_end,
		   status, state_version, parent_run_id, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		r.RunID, r.RequestID, r.TenantID, r.Principal, r.PrincipalType,
		nullableStr(r.SessionID), scope, nullableStr(r.PrimaryClusterID), r.Intent, r.ActionMode,
		nullableStr(r.TargetType), nullableStr(r.TargetResourceID),
		nullableTime(r.TimeRangeStart), nullableTime(r.TimeRangeEnd),
		status, r.StateVersion, nullableStr(r.ParentRunID), r.CreatedAt, r.UpdatedAt,
	)
	if err != nil {
		if isDuplicateKey(err) {
			return false, nil
		}
		return false, err
	}
	obs := o.Status
	if obs == "" {
		obs = "pending"
	}
	if o.CreatedAt.IsZero() {
		o.CreatedAt = r.CreatedAt
	}
	if o.UpdatedAt.IsZero() {
		o.UpdatedAt = r.UpdatedAt
	}
	if _, err := tx.Exec(
		`INSERT INTO ai_run_outbox (invocation_id, run_id, status, dispatch_count, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		o.InvocationID, r.RunID, obs, o.DispatchCount, o.CreatedAt, o.UpdatedAt); err != nil {
		return false, err // outbox 失败 → 回滚 run，不留下"永不派发"的 Run
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

// Get 按 run_id 读取。
func (d *AIRunDAO) Get(runID string) (*AIRun, error) {
	conn := GetDB()
	if conn == nil {
		return nil, errors.New("mysql unavailable")
	}
	row := conn.QueryRow(
		`SELECT run_id, request_id, tenant_id, principal, principal_type, session_id,
		   scope_kind, primary_cluster_id, intent, action_mode, target_type,
		   target_resource_id, time_range_start, time_range_end, status, state_version,
		   parent_run_id, created_at, updated_at, finished_at, last_event_sequence
		 FROM ai_runs WHERE run_id = ?`, runID)
	return scanAIRun(row)
}

// GetByTenantRequestID reads the canonical idempotency record for a tenant.
// The tenant predicate is part of the lookup so a retry cannot reveal or
// replay another tenant's Run.
func (d *AIRunDAO) GetByTenantRequestID(tenantID, requestID string) (*AIRun, error) {
	conn := GetDB()
	if conn == nil {
		return nil, errors.New("mysql unavailable")
	}
	row := conn.QueryRow(
		`SELECT run_id, request_id, tenant_id, principal, principal_type, session_id,
		   scope_kind, primary_cluster_id, intent, action_mode, target_type,
		   target_resource_id, time_range_start, time_range_end, status, state_version,
		   parent_run_id, created_at, updated_at, finished_at, last_event_sequence
		 FROM ai_runs WHERE tenant_id = ? AND request_id = ?`, tenantID, requestID)
	return scanAIRun(row)
}

// GetTx 在给定事务内按 run_id 读取（供恢复一致性快照，P1-4）。
func (d *AIRunDAO) GetTx(tx *sql.Tx, runID string) (*AIRun, error) {
	row := tx.QueryRow(
		`SELECT run_id, request_id, tenant_id, principal, principal_type, session_id,
		   scope_kind, primary_cluster_id, intent, action_mode, target_type,
		   target_resource_id, time_range_start, time_range_end, status, state_version,
		   parent_run_id, created_at, updated_at, finished_at, last_event_sequence
		 FROM ai_runs WHERE run_id = ?`, runID)
	return scanAIRun(row)
}

// List 列出 tenant 的 Run（按 created_at 倒序）。
func (d *AIRunDAO) List(tenantID string) ([]AIRun, error) {
	conn := GetDB()
	if conn == nil {
		return nil, errors.New("mysql unavailable")
	}
	rows, err := conn.Query(
		`SELECT run_id, request_id, tenant_id, principal, principal_type, session_id,
		   scope_kind, primary_cluster_id, intent, action_mode, target_type,
		   target_resource_id, time_range_start, time_range_end, status, state_version,
		   parent_run_id, created_at, updated_at, finished_at, last_event_sequence
		 FROM ai_runs WHERE tenant_id = ? ORDER BY created_at DESC`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AIRun{}
	for rows.Next() {
		var r AIRun
		if err := scanAIRunRow(rows, &r); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Transition 乐观 CAS 状态迁移（state_version 不符则失败）。
func (d *AIRunDAO) Transition(runID, target string, expectedVersion int64, now time.Time) (bool, error) {
	conn := GetDB()
	if conn == nil {
		return false, errors.New("mysql unavailable")
	}
	var finishedAt interface{} = nil
	if isTerminalStatus(target) {
		finishedAt = now
	}
	res, err := conn.Exec(
		`UPDATE ai_runs SET status = ?, state_version = state_version + 1, updated_at = ?, finished_at = ?
		 WHERE run_id = ? AND state_version = ?`,
		target, now, finishedAt, runID, expectedVersion)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

// Cancel 显式 cancel（control action，终态不可再 cancel）。
func (d *AIRunDAO) Cancel(runID string, expectedVersion int64, now time.Time) (bool, error) {
	conn := GetDB()
	if conn == nil {
		return false, errors.New("mysql unavailable")
	}
	res, err := conn.Exec(
		`UPDATE ai_runs SET status = 'cancelled', state_version = state_version + 1,
		   updated_at = ?, finished_at = ? WHERE run_id = ? AND state_version = ?
		   AND status NOT IN ('success','partial','failed','regressed','cancelled')`,
		now, now, runID, expectedVersion)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

// TransitionTx 在给定事务内乐观 CAS 状态迁移（供 ApplyRunControlCommandTx mutateFn 使用）。
func (d *AIRunDAO) TransitionTx(tx *sql.Tx, runID, target string, expectedVersion int64, now time.Time) (bool, error) {
	var finishedAt interface{} = nil
	if isTerminalStatus(target) {
		finishedAt = now
	}
	res, err := tx.Exec(
		`UPDATE ai_runs SET status = ?, state_version = state_version + 1, updated_at = ?, finished_at = ?
		 WHERE run_id = ? AND state_version = ?`,
		target, now, finishedAt, runID, expectedVersion)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

// TransitionTxValidated 在给定事务内做合法状态迁移（P0#2/#10）：锁 Run → 读当前 status →
// ValidateRunTransition（终态不可复活）→ CAS state_version 推进。返回 (ok, err)。
// 供 Runtime Commit / Public Cancel / Internal transition 共用同一合法性权威。
func (d *AIRunDAO) TransitionTxValidated(tx *sql.Tx, runID, target string, expectedVersion int64, now time.Time) (bool, error) {
	var current string
	if err := tx.QueryRow(`SELECT status FROM ai_runs WHERE run_id = ? FOR UPDATE`, runID).Scan(&current); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, ErrRunNotFound
		}
		return false, err
	}
	if !ValidateRunTransition(current, target) {
		return false, &IllegalTransitionError{Current: current, Target: target}
	}
	var finishedAt interface{} = nil
	if isTerminalStatus(target) {
		finishedAt = now
	}
	res, err := tx.Exec(
		`UPDATE ai_runs SET status = ?, state_version = state_version + 1, updated_at = ?, finished_at = ?
		 WHERE run_id = ? AND state_version = ?`,
		target, now, finishedAt, runID, expectedVersion)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

// TransitionTxValidatedWithLease performs the final Runtime Commit CAS while
// rechecking the lease predicate in the UPDATE statement itself. The prior
// LeaseFencingTx row lock prevents concurrent ownership changes; this second
// predicate also rejects a lease that naturally expires between the check and
// the state transition.
func (d *AIRunDAO) TransitionTxValidatedWithLease(tx *sql.Tx, runID, target string, expectedVersion int64, now time.Time,
	ownerID string, epoch int64, tokenHash string) (bool, error) {
	var current string
	if err := tx.QueryRow(`SELECT status FROM ai_runs WHERE run_id = ? FOR UPDATE`, runID).Scan(&current); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, ErrRunNotFound
		}
		return false, err
	}
	if !ValidateRunTransition(current, target) {
		return false, &IllegalTransitionError{Current: current, Target: target}
	}
	var finishedAt interface{} = nil
	if isTerminalStatus(target) {
		finishedAt = now
	}
	res, err := tx.Exec(
		`UPDATE ai_runs SET status = ?, state_version = state_version + 1, updated_at = ?, finished_at = ?
		   WHERE run_id = ? AND state_version = ?
		     AND lease_owner_id = ? AND lease_epoch = ? AND lease_token_hash = ?
		     AND lease_expires_at IS NOT NULL AND lease_expires_at >= CURRENT_TIMESTAMP(3)
		     AND status NOT IN ('success','partial','failed','regressed','cancelled')`,
		target, now, finishedAt, runID, expectedVersion, ownerID, epoch, tokenHash)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

// CancelTx 在给定事务内显式 cancel（供 ApplyRunControlCommandTx mutateFn 使用）。
func (d *AIRunDAO) CancelTx(tx *sql.Tx, runID string, expectedVersion int64, now time.Time) (bool, error) {
	res, err := tx.Exec(
		`UPDATE ai_runs SET status = 'cancelled', state_version = state_version + 1,
		   updated_at = ?, finished_at = ? WHERE run_id = ? AND state_version = ?
		   AND status NOT IN ('success','partial','failed','regressed','cancelled')`,
		now, now, runID, expectedVersion)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

// ScanUnfinished 扫描非终态 Run（重启恢复）。终态含 partial。
func (d *AIRunDAO) ScanUnfinished() ([]AIRun, error) {
	return d.ScanUnfinishedLimit(0)
}

// ScanUnfinishedLimit 扫描非终态 Run（重启恢复）并支持 limit 分页（limit<=0 不限制）。
// A0-05（F-18）：供 control_plane.runs.recover.global 全局恢复扫描使用。
func (d *AIRunDAO) ScanUnfinishedLimit(limit int) ([]AIRun, error) {
	conn := GetDB()
	if conn == nil {
		return nil, errors.New("mysql unavailable")
	}
	q := `SELECT run_id, request_id, tenant_id, principal, principal_type, session_id,
		   scope_kind, primary_cluster_id, intent, action_mode, target_type,
		   target_resource_id, time_range_start, time_range_end, status, state_version,
		   parent_run_id, created_at, updated_at, finished_at, last_event_sequence
		 FROM ai_runs WHERE status NOT IN ('success','partial','failed','regressed','cancelled')`
	var rows *sql.Rows
	var err error
	if limit > 0 {
		rows, err = conn.Query(q+` ORDER BY created_at ASC LIMIT ?`, limit)
	} else {
		rows, err = conn.Query(q)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AIRun{}
	for rows.Next() {
		var r AIRun
		if err := scanAIRunRow(rows, &r); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// GetWithRuntime 读取单个 Run 的完整行（含 A1 lease/runtime-wait 列），用于 recovery snapshot
// 与 runtime metadata 恢复。与 Get 不同，这里扫描全列。
func (d *AIRunDAO) GetWithRuntime(runID string) (*AIRun, error) {
	conn := GetDB()
	if conn == nil {
		return nil, errors.New("mysql unavailable")
	}
	row := conn.QueryRow(
		`SELECT run_id, request_id, tenant_id, principal, principal_type, session_id,
		   scope_kind, primary_cluster_id, intent, action_mode, target_type,
		   target_resource_id, time_range_start, time_range_end, status, state_version,
		   parent_run_id, created_at, updated_at, finished_at, last_event_sequence,
		   lease_owner_id, lease_epoch, lease_claim_id, lease_token_hash, lease_expires_at,
		   heartbeat_at, runtime_wait_kind, retry_not_before, retry_attempt,
		   last_failure_code, runtime_metadata_json
		 FROM ai_runs WHERE run_id = ?`, runID)
	var r AIRun
	if err := scanAIRunRowFull(row, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

// runTransitions 服务端唯一合法状态迁移表（与 orchestrator RunStateMachine.RUN_TRANSITIONS
// 对齐）。P0#2/#10：由 Internal transition、Runtime Commit、Public Cancel、Admin control 共同使用，
// 终态不可复活。
var runTransitions = map[string][]string{
	"created":               {"planning", "cancelled"},
	"planning":              {"investigating", "awaiting_confirmation", "failed", "cancelled"},
	"investigating":         {"awaiting_confirmation", "awaiting_approval", "verifying", "failed", "cancelled"},
	"awaiting_confirmation": {"investigating", "awaiting_approval", "cancelled"},
	"awaiting_approval":     {"executing", "cancelled", "failed"},
	"executing":             {"verifying", "success", "partial", "failed", "regressed", "cancelled"},
	"verifying":             {"success", "partial", "failed", "regressed", "cancelled"},
}

// runTerminal 终态集合。
var runTerminal = map[string]bool{
	"success": true, "partial": true, "failed": true, "regressed": true, "cancelled": true,
}

// ValidateRunTransition 校验 (current → target) 是否合法（P0#2/#10）。
// 终态不可迁（不可复活）；目标须在允许集。
func ValidateRunTransition(current, target string) bool {
	if runTerminal[current] {
		return false
	}
	for _, allowed := range runTransitions[current] {
		if allowed == target {
			return true
		}
	}
	return false
}

// IllegalTransitionError 表示非法状态迁移（P0#2/#10：Commit/Cancel/Transition 共用）。
type IllegalTransitionError struct{ Current, Target string }

func (e *IllegalTransitionError) Error() string {
	return "illegal run transition: " + e.Current + " -> " + e.Target
}

// IsTerminalStatus 判断是否终态（导出，供 RunControlService 等使用）。
func IsTerminalStatus(status string) bool { return isTerminalStatus(status) }

// isTerminalStatus 判断是否终态。
func isTerminalStatus(status string) bool {
	switch status {
	case "success", "partial", "failed", "regressed", "cancelled":
		return true
	}
	return false
}

// scanAIRun 扫描单行（QueryRow 版）。
func scanAIRun(row *sql.Row) (*AIRun, error) {
	var r AIRun
	if err := scanAIRunRow(row, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

// scanAIRunRowFull 扫描完整行（含 A1 lease/runtime-wait 列）。供 GetWithRuntime / recovery。
func scanAIRunRowFull(row *sql.Row, r *AIRun) error {
	var primary, parent, principalType, session, targetType, targetResource sql.NullString
	var timeStart, timeEnd, finished sql.NullTime
	var leaseOwner, leaseClaim, leaseTokenHash, waitKind, failureCode sql.NullString
	var leaseEpoch, retryAttempt sql.NullInt64
	var leaseExpires, heartbeat, retryBefore sql.NullTime
	var runtimeMeta []byte
	if err := row.Scan(&r.RunID, &r.RequestID, &r.TenantID, &r.Principal, &principalType,
		&session, &r.ScopeKind, &primary, &r.Intent, &r.ActionMode, &targetType,
		&targetResource, &timeStart, &timeEnd, &r.Status, &r.StateVersion,
		&parent, &r.CreatedAt, &r.UpdatedAt, &finished, &r.LastEventSeq,
		&leaseOwner, &leaseEpoch, &leaseClaim, &leaseTokenHash, &leaseExpires,
		&heartbeat, &waitKind, &retryBefore, &retryAttempt, &failureCode, &runtimeMeta); err != nil {
		return err
	}
	r.PrincipalType = principalType.String
	r.SessionID = session.String
	r.PrimaryClusterID = primary.String
	r.ParentRunID = parent.String
	r.TargetType = targetType.String
	r.TargetResourceID = targetResource.String
	if timeStart.Valid {
		r.TimeRangeStart = &timeStart.Time
	}
	if timeEnd.Valid {
		r.TimeRangeEnd = &timeEnd.Time
	}
	if finished.Valid {
		r.FinishedAt = &finished.Time
	}
	r.LeaseOwnerID = leaseOwner.String
	r.LeaseEpoch = leaseEpoch.Int64
	r.LeaseClaimID = leaseClaim.String
	r.LeaseTokenHash = leaseTokenHash.String
	if leaseExpires.Valid {
		r.LeaseExpiresAt = &leaseExpires.Time
	}
	if heartbeat.Valid {
		r.HeartbeatAt = &heartbeat.Time
	}
	r.RuntimeWaitKind = waitKind.String
	if retryBefore.Valid {
		r.RetryNotBefore = &retryBefore.Time
	}
	r.RetryAttempt = int(retryAttempt.Int64)
	r.LastFailureCode = failureCode.String
	if len(runtimeMeta) > 0 {
		r.RuntimeMetadata = runtimeMeta
	}
	return nil
}

// scanAIRunRow 扫描一行到 AIRun（QueryRow / Rows 共用）。
type rowScanner interface {
	Scan(dest ...interface{}) error
}

func scanAIRunRow(row rowScanner, r *AIRun) error {
	var primary, parent, principalType, session, targetType, targetResource sql.NullString
	var timeStart, timeEnd, finished sql.NullTime
	if err := row.Scan(&r.RunID, &r.RequestID, &r.TenantID, &r.Principal, &principalType,
		&session, &r.ScopeKind, &primary, &r.Intent, &r.ActionMode, &targetType,
		&targetResource, &timeStart, &timeEnd, &r.Status, &r.StateVersion,
		&parent, &r.CreatedAt, &r.UpdatedAt, &finished, &r.LastEventSeq); err != nil {
		return err
	}
	// 将 Null 列写回结构体（此前漏赋值导致 PrincipalType/SessionID/PrimaryClusterID 为空）。
	r.PrincipalType = principalType.String
	r.SessionID = session.String
	r.PrimaryClusterID = primary.String
	r.ParentRunID = parent.String
	r.TargetType = targetType.String
	r.TargetResourceID = targetResource.String
	if timeStart.Valid {
		r.TimeRangeStart = &timeStart.Time
	}
	if timeEnd.Valid {
		r.TimeRangeEnd = &timeEnd.Time
	}
	if finished.Valid {
		r.FinishedAt = &finished.Time
	}
	return nil
}

// nullableStr / nullableTime 辅助。
func nullableStr(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}

func nullableTime(t *time.Time) interface{} {
	if t == nil {
		return nil
	}
	return *t
}
