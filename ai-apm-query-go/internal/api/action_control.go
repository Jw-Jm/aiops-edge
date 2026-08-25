package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/observability-platform/ai-apm-query-go/internal/contract"
)

// ─────────────────────────────────────────────────────────────────────────────
// Stage D 接线（报告 §29）：query-api 公共 action 执行端点。
//
// POST /api/v1/ai/actions/{action_id}/execute
//   - 从 ai_actions 读取 action，要求已 approved（ai_approval_decisions 有 approved 记录）
//     + 非 dry_run。
//   - durable idempotency：execution_status 已 terminal → 返回已记录结果，不重复执行。
//   - 用 query-api 私钥签发 signed ActionExecutionContext → POST ai-action-executor
//     /v1/executor/execute → 把 ActionResult 持久化回 ai_actions。
//   - executor 不可达 / disabled → 持久化 rejected / execution_unknown（不伪装成功）。
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
	respondJSON(w, http.StatusOK, map[string]interface{}{
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
	})
}

// executeActionPublic 执行一个已批准 action（经 executor，Stage D）。
func (h *Handler) executeActionPublic(w http.ResponseWriter, r *http.Request, actionID string) {
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
	// 执行闭环（签发 + 转发 executor + durable 持久化）。
	result, execErr := h.executeApprovedAction(action, approval)
	if execErr != nil {
		respondJSON(w, http.StatusServiceUnavailable, map[string]interface{}{
			"error":     contract.ErrorCodeExecutorUnavailable,
			"message":   execErr.Error(),
			"action_id": actionID,
		})
		return
	}
	respondJSON(w, http.StatusOK, map[string]interface{}{
		"action_id": actionID,
		"status":    result.Status,
		"message":   result.Message,
	})
}
