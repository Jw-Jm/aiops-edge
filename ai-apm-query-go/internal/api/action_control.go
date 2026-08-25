package api

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/observability-platform/ai-apm-query-go/internal/contract"
	"github.com/observability-platform/ai-apm-query-go/internal/store"
)

// ─────────────────────────────────────────────────────────────────────────────
// Stage D 接线（报告 §29）：query-api 公共 action 控制端点。
//
// POST /api/v1/ai/actions/{action_id}/execute
//   - 从 ai_actions 读取 action，要求已 approved（ai_approval_decisions 有 approved 记录）
//     + 非 dry_run。
//   - durable idempotency：execution_status 已 terminal → 返回已记录结果，不重复执行。
//   - approved decision 已原子写入 Action outbox；dispatcher 才能签发上下文并执行。
//   - 兼容 execute 入口只返回队列/状态，不在 HTTP 请求中跨越 mutation boundary。
//
// GET /api/v1/ai/actions/{action_id}
//   - 返回 action 详情（含执行状态/结果）。
//
// 安全：执行是写操作，要求 admin 角色（RequireRole 在 main.go 注册时套用）；
// canonical 鉴权（JWT + tenant 成员）由 AuthMiddleware 完成。
// ─────────────────────────────────────────────────────────────────────────────

const actionExecPrefix = "/api/v1/ai/actions"

// ActionPublicHandler 路由 /api/v1/ai/actions 下请求。
func (h *Handler) ActionPublicHandler(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	if path == actionExecPrefix && r.Method == http.MethodGet {
		h.listActionsPublic(w, r)
		return
	}
	rest := strings.TrimPrefix(path, actionExecPrefix)
	parts := strings.Split(strings.Trim(rest, "/"), "/")
	// /api/v1/ai/actions/{id}/decision
	if len(parts) == 2 && parts[1] == "decision" && r.Method == http.MethodPost {
		h.decideActionPublic(w, r, parts[0])
		return
	}
	// /api/v1/ai/actions/{id}/execute
	if len(parts) == 2 && parts[1] == "execute" && r.Method == http.MethodPost {
		if !hasRole(r, "admin") {
			respondJSON(w, http.StatusForbidden, map[string]interface{}{"error": "permission_denied"})
			return
		}
		h.executeActionPublic(w, r, parts[0])
		return
	}
	// /api/v1/ai/actions/{id}
	if len(parts) == 1 && parts[0] != "" && r.Method == http.MethodGet {
		h.getActionPublic(w, r, parts[0])
		return
	}
	respondJSON(w, http.StatusNotFound, map[string]interface{}{"error": contract.ErrorCodeResourceNotFound})
}

func (h *Handler) listActionsPublic(w http.ResponseWriter, r *http.Request) {
	authCtx, ok := requestAuthorizationContext(r)
	if !ok || authCtx.TenantID == "" {
		respondJSON(w, http.StatusForbidden, map[string]interface{}{"error": "permission_denied"})
		return
	}
	status := strings.TrimSpace(r.URL.Query().Get("status"))
	if status != "" && status != "proposed" && status != "approved" && status != "rejected" {
		respondJSON(w, http.StatusBadRequest, map[string]interface{}{"error": contract.ErrorCodeValidationFailed})
		return
	}
	limit := 100
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 && parsed <= 200 {
			limit = parsed
		}
	}
	db := store.GetDB()
	if db == nil {
		respondJSON(w, http.StatusServiceUnavailable, map[string]interface{}{"error": "persistence_unavailable"})
		return
	}
	query := `SELECT action_id, run_id, cluster_id, action_type, action_hash, hash_schema_version,
		action_version, proposed_by, policy_version, preflight_status, target_resource_type,
		status, dry_run, target_name, target_uid, resource_version, namespace, operation,
		execution_status, error_code, created_at, updated_at
		FROM ai_actions WHERE tenant_id = ?`
	args := []interface{}{authCtx.TenantID}
	if status != "" {
		query += " AND status = ?"
		args = append(args, status)
	}
	query += " ORDER BY created_at DESC LIMIT ?"
	args = append(args, limit)
	rows, err := db.Query(query, args...)
	if err != nil {
		respondJSON(w, http.StatusServiceUnavailable, map[string]interface{}{"error": "action_read_failed"})
		return
	}
	defer rows.Close()
	items := make([]map[string]interface{}, 0)
	for rows.Next() {
		var actionID, runID, clusterID, actionType, hash, policy, preflight, resourceType string
		var proposedBy sql.NullString
		var actionVersion int64
		var hashSchema, dryRun int
		var statusValue, targetName, targetUID, resourceVersion, namespace, operation, executionStatus, errorCode string
		var createdAt, updatedAt interface{}
		if err := rows.Scan(&actionID, &runID, &clusterID, &actionType, &hash, &hashSchema, &actionVersion,
			&proposedBy, &policy, &preflight, &resourceType, &statusValue, &dryRun, &targetName, &targetUID,
			&resourceVersion, &namespace, &operation, &executionStatus, &errorCode, &createdAt, &updatedAt); err != nil {
			respondJSON(w, http.StatusServiceUnavailable, map[string]interface{}{"error": "action_read_failed"})
			return
		}
		items = append(items, map[string]interface{}{
			"action_id": actionID, "run_id": runID, "cluster_id": clusterID, "action_type": actionType,
			"action_hash": hash, "hash_schema_version": hashSchema, "action_version": actionVersion,
			"proposed_by": proposedBy.String, "policy_version": policy, "preflight_status": preflight,
			"target_resource_type": resourceType, "status": statusValue, "dry_run": dryRun != 0,
			"target_name": targetName, "target_uid": targetUID, "resource_version": resourceVersion,
			"namespace": namespace, "operation": operation, "execution_status": executionStatus,
			"error_code": errorCode, "created_at": createdAt, "updated_at": updatedAt,
		})
	}
	if err := rows.Err(); err != nil {
		respondJSON(w, http.StatusServiceUnavailable, map[string]interface{}{"error": "action_read_failed"})
		return
	}
	respondJSON(w, http.StatusOK, map[string]interface{}{"actions": items, "count": len(items)})
}

// getActionPublic 返回 action 详情（含执行状态/结果）。
func (h *Handler) getActionPublic(w http.ResponseWriter, r *http.Request, actionID string) {
	action, err := h.actionDAO.GetByID(actionID)
	if err != nil {
		respondJSON(w, http.StatusInternalServerError, map[string]interface{}{"error": "action_read_failed"})
		return
	}
	if action == nil {
		respondJSON(w, http.StatusNotFound, map[string]interface{}{"error": contract.ErrorCodeResourceNotFound})
		return
	}
	// canonical 鉴权：校验请求 tenant 与 action tenant 一致。
	if authCtx, ok := requestAuthorizationContext(r); ok && authCtx.TenantID != "" && authCtx.TenantID != action.TenantID {
		respondJSON(w, http.StatusForbidden, map[string]interface{}{"error": contract.ErrorCodeTenantAccessDenied})
		return
	}
	projection := map[string]interface{}{
		"action_id":          action.ActionID,
		"run_id":             action.RunID,
		"tenant_id":          action.TenantID,
		"cluster_id":         action.ClusterID,
		"action_type":        action.ActionType,
		"action_hash":        action.ActionHash,
		"idempotency_key":    action.IdempotencyKey,
		"proposed_risk":      action.ProposedRisk,
		"authoritative_risk": action.AuthoritativeRisk,
		"status":             action.Status,
		"dry_run":            action.DryRun,
		"target_name":        action.TargetName,
		"target_uid":         action.TargetUID,
		"resource_version":   action.ResourceVersion,
		"namespace":          action.Namespace,
		"operation":          action.Operation,
		"execution_status":   action.ExecutionStatus,
		"error_code":         action.ErrorCode,
		"result":             json.RawMessage(action.Result),
	}
	// The legacy ActionDAO projection is retained for old rows. Enrich the
	// public detail from the v2 columns so the browser never has to reconstruct
	// hash/version/preflight state from a proposal or local cache.
	var hashSchema int
	var actionVersion int64
	var proposedBy sql.NullString
	var policyVersion, preflightStatus, resourceType string
	if err := store.GetDB().QueryRow(`SELECT hash_schema_version, action_version,
		proposed_by, policy_version, preflight_status, target_resource_type
		FROM ai_actions WHERE action_id = ?`, actionID).Scan(&hashSchema, &actionVersion,
		&proposedBy, &policyVersion, &preflightStatus, &resourceType); err == nil {
		projection["hash_schema_version"] = hashSchema
		projection["action_version"] = actionVersion
		projection["proposed_by"] = proposedBy.String
		projection["policy_version"] = policyVersion
		projection["preflight_status"] = preflightStatus
		projection["target_resource_type"] = resourceType
	}
	respondJSON(w, http.StatusOK, projection)
}

// executeActionPublic is a compatibility acknowledgement for older callers.
// It never crosses the mutation boundary synchronously; approval enqueues the
// durable command and RunActionDispatchLoop is the sole dispatcher.
func (h *Handler) executeActionPublic(w http.ResponseWriter, r *http.Request, actionID string) {
	w.Header().Set("Deprecation", "true")
	w.Header().Set("Sunset", "2026-12-31T00:00:00Z")
	action, err := h.actionDAO.GetByID(actionID)
	if err != nil {
		respondJSON(w, http.StatusInternalServerError, map[string]interface{}{"error": "action_read_failed"})
		return
	}
	if action == nil {
		respondJSON(w, http.StatusNotFound, map[string]interface{}{"error": contract.ErrorCodeResourceNotFound})
		return
	}
	// canonical 鉴权：tenant 一致。
	if authCtx, ok := requestAuthorizationContext(r); ok && authCtx.TenantID != "" && authCtx.TenantID != action.TenantID {
		respondJSON(w, http.StatusForbidden, map[string]interface{}{"error": contract.ErrorCodeTenantAccessDenied})
		return
	}
	// 前置条件：已 approved 审批记录。
	approval, err := h.approvalDAO.GetApprovedApproval(actionID)
	if err != nil {
		respondJSON(w, http.StatusInternalServerError, map[string]interface{}{"error": "approval_read_failed"})
		return
	}
	if approval == nil || approval.Decision != "approved" {
		respondJSON(w, http.StatusUnprocessableEntity, map[string]interface{}{
			"error": contract.ErrorCodeActionNotApproved,
		})
		return
	}
	if isTerminalExecutionStatus(action.ExecutionStatus) {
		respondJSON(w, http.StatusOK, map[string]interface{}{
			"action_id": actionID, "status": action.ExecutionStatus, "replay": true,
		})
		return
	}
	db := store.GetDB()
	if db == nil {
		respondJSON(w, http.StatusServiceUnavailable, map[string]interface{}{"error": "persistence_unavailable"})
		return
	}
	var commandID string
	err = db.QueryRow(`SELECT command_id FROM ai_action_outbox
		WHERE action_id = ? ORDER BY created_at DESC LIMIT 1`, action.ActionID).Scan(&commandID)
	if err != nil {
		respondJSON(w, http.StatusServiceUnavailable, map[string]interface{}{
			"error": "ACTION_OUTBOX_MISSING", "action_id": actionID,
		})
		return
	}
	respondJSON(w, http.StatusAccepted, map[string]interface{}{
		"action_id": actionID, "command_id": commandID, "status": "queued",
		"message": "action is queued for the durable dispatcher",
	})
}
