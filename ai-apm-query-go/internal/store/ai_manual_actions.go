package store

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// CreateManualAction atomically persists the canonical manual Action and its
// awaiting-approval Run. Unlike CreateWithOutbox it intentionally does not
// create a run outbox entry: an unapproved K8s mutation must never be
// dispatched to the orchestrator or executor.
func (d *AIRunDAO) CreateManualAction(ctx context.Context, run AIRun, action AIAction) (bool, error) {
	conn := GetDB()
	if conn == nil {
		return false, errors.New("mysql unavailable")
	}
	if run.RunID == "" || run.RequestID == "" || run.TenantID == "" || action.ActionID == "" || action.ActionHash == "" {
		return false, errors.New("manual action run/action identity is required")
	}
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()

	now := run.CreatedAt
	if now.IsZero() {
		now = time.Now()
	}
	updatedAt := run.UpdatedAt
	if updatedAt.IsZero() {
		updatedAt = now
	}
	scope := firstNonEmptyStr2(run.ScopeKind, "single_cluster")
	status := firstNonEmptyStr2(run.Status, "awaiting_approval")
	_, err = tx.ExecContext(ctx,
		`INSERT INTO ai_runs (run_id, request_id, tenant_id, principal, principal_type,
		   session_id, scope_kind, primary_cluster_id, intent, action_mode,
		   target_type, target_resource_id, time_range_start, time_range_end,
		   status, state_version, parent_run_id, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		run.RunID, run.RequestID, run.TenantID, run.Principal, firstNonEmptyStr2(run.PrincipalType, "user"),
		nullableStr(run.SessionID), scope, nullableStr(run.PrimaryClusterID), run.Intent, firstNonEmptyStr2(run.ActionMode, "manual"),
		nullableStr(run.TargetType), nullableStr(run.TargetResourceID), nullableTime(run.TimeRangeStart), nullableTime(run.TimeRangeEnd),
		status, run.StateVersion, nullableStr(run.ParentRunID), now, updatedAt,
	)
	if err != nil {
		if !isDuplicateKey(err) {
			return false, err
		}
		// A duplicate request_id is a client retry. Lock and compare the
		// existing immutable Action hash before returning a replay.
		var existingRunID string
		if lookupErr := tx.QueryRowContext(ctx,
			`SELECT run_id FROM ai_runs WHERE tenant_id = ? AND request_id = ? FOR UPDATE`,
			run.TenantID, run.RequestID).Scan(&existingRunID); lookupErr != nil {
			return false, fmt.Errorf("manual action existing run lookup: %w", lookupErr)
		}
		var existingHash string
		if lookupErr := tx.QueryRowContext(ctx,
			`SELECT action_hash FROM ai_actions WHERE run_id = ? LIMIT 1`, existingRunID).Scan(&existingHash); lookupErr != nil {
			return false, fmt.Errorf("manual action existing action lookup: %w", lookupErr)
		}
		if existingHash != action.ActionHash {
			return false, ErrIdempotencyPayloadMismatch
		}
		if err := tx.Commit(); err != nil {
			return false, err
		}
		return false, nil
	}

	dryRun := 0
	if action.DryRun {
		dryRun = 1
	}
	hashSchemaVersion := action.HashSchemaVersion
	if hashSchemaVersion == 0 {
		hashSchemaVersion = 2
	}
	actionVersion := action.ActionVersion
	if actionVersion == 0 {
		actionVersion = 1
	}
	_, err = tx.ExecContext(ctx,
		`INSERT INTO ai_actions (action_id, run_id, tenant_id, cluster_id, action_type,
		   action_hash, hash_schema_version, action_version, proposed_by, policy_version,
		   preflight_status, target_resource_type, idempotency_key, proposed_risk,
		   authoritative_risk, status, dry_run, target_name, target_uid, resource_version,
		   namespace, operation, execution_status, params_json, result_json, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		action.ActionID, run.RunID, run.TenantID, action.ClusterID, firstNonEmptyStr2(action.ActionType, "kubernetes"),
		action.ActionHash, hashSchemaVersion, actionVersion, nullableStr(action.ProposedBy),
		firstNonEmptyStr2(action.PolicyVersion, "action-policy-v1"), firstNonEmptyStr2(action.PreflightStatus, "passed"),
		firstNonEmptyStr2(action.TargetResourceType, "deployment"), action.IdempotencyKey,
		firstNonEmptyStr2(action.ProposedRisk, "R0"), firstNonEmptyStr2(action.AuthoritativeRisk, "R0"),
		firstNonEmptyStr2(action.Status, "proposed"), dryRun, action.TargetName, action.TargetUID,
		action.ResourceVersion, action.Namespace, action.Operation, firstNonEmptyStr2(action.ExecutionStatus, "proposed"),
		action.Params, action.Result, now, updatedAt,
	)
	if err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}
