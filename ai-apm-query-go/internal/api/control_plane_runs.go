package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/observability-platform/ai-apm-query-go/internal/contract"
	"github.com/observability-platform/ai-apm-query-go/internal/store"
)

// ─────────────────────────────────────────────────────────────────────────────
// P10 (V9.3 Phase 10) — control-plane runs 端点（P10 完整闭环 Plan B）。
//
// orchestrator（system principal）经此对已存在 Run 做状态迁移/取消/读取/恢复。
// **不含业务 Run 创建**（创建仅在 query-api 公共 /api/v1/ai/runs，R1）。
// capability：mutate=transition/cancel，recover=get/list/unfinished。
// ─────────────────────────────────────────────────────────────────────────────

const controlPlaneRunsPrefix = "/internal/v1/control-plane/runs"

// InternalControlPlaneRunRouter 路由 control-plane runs 请求。
func (h *Handler) InternalControlPlaneRunRouter(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	rest := strings.TrimPrefix(path, controlPlaneRunsPrefix)
	// rest 形如 "/{id}/transition"、"/{id}/cancel"、"/{id}"、""、"/unfinished"、"?tenant_id="
	parts := strings.Split(strings.Trim(rest, "/"), "/")

	// 列表 / 未完成
	if len(parts) == 1 && parts[0] == "" {
		h.internalControlPlaneRunList(w, r)
		return
	}
	if len(parts) == 1 && parts[0] == "unfinished" {
		h.internalControlPlaneRunUnfinished(w, r)
		return
	}
	if len(parts) == 2 {
		id := parts[0]
		switch parts[1] {
		case "transition":
			h.internalControlPlaneRunTransition(w, r, id)
			return
		case "cancel":
			h.internalControlPlaneRunCancel(w, r, id)
			return
		case "events":
			// events 由 events 端点处理（复用同一 handler 分派）
			h.internalControlPlaneEventRouter(w, r, id)
			return
		case "actions":
			h.internalControlPlaneActionAppend(w, r, id)
			return
		case "approvals":
			h.internalControlPlaneApprovalAppend(w, r, id)
			return
		}
		h.internalControlPlaneRunGet(w, r, id)
		return
	}
	respondJSON(w, http.StatusNotFound, map[string]interface{}{"error": contract.ErrorCodeResourceNotFound})
}

// controlPlaneBodyTransition 是 transition 请求体。
type controlPlaneBodyTransition struct {
	ExpectedVersion int64  `json:"expected_version"`
	Target          string `json:"target"`
	CommandID       string `json:"command_id"`
}

// controlPlaneBodyCancel 是 cancel 请求体（P1-3：cancel 也必须带 command_id + expected_version）。
type controlPlaneBodyCancel struct {
	ExpectedVersion int64  `json:"expected_version"`
	CommandID       string `json:"command_id"`
}

// runTransitions 服务端 Run 状态机（与 orchestrator RunStateMachine.RUN_TRANSITIONS 对齐，
// P1-3：状态迁移在 query-api 服务端校验合法性，不能只信任 orchestrator 传入的任意 target）。
var runTransitions = map[string][]string{
	"created":             {"planning", "cancelled"},
	"planning":            {"investigating", "awaiting_confirmation", "failed", "cancelled"},
	"investigating":       {"awaiting_confirmation", "awaiting_approval", "failed", "cancelled"},
	"awaiting_confirmation": {"investigating", "awaiting_approval", "cancelled"},
	"awaiting_approval":   {"executing", "cancelled", "failed"},
	"executing":           {"verifying", "success", "partial", "failed", "regressed", "cancelled"},
	"verifying":           {"success", "partial", "failed", "regressed", "cancelled"},
}

// runTerminal 终态集合。
var runTerminal = map[string]bool{
	"success": true, "partial": true, "failed": true, "regressed": true, "cancelled": true,
}

// validRunTransition 校验 (current → target) 是否合法（终态不可迁；目标须在允许集）。
func validRunTransition(current, target string) bool {
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

// internalControlPlaneRunTransition 处理 POST .../runs/{id}/transition。
func (h *Handler) internalControlPlaneRunTransition(w http.ResponseWriter, r *http.Request, runID string) {
	rctx, err := authorizeInternalControlPlane(r, "control_plane.runs.mutate", "ai-orchestrator")
	if err != nil {
		respondInternalQueryError(w, err)
		return
	}
	var body controlPlaneBodyTransition
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]interface{}{"error": contract.ErrorCodeValidationFailed})
		return
	}
	run, err := h.runDAO.Get(runID)
	if err != nil {
		respondJSON(w, http.StatusInternalServerError, map[string]interface{}{"error": "run_get_failed"})
		return
	}
	if run == nil {
		respondJSON(w, http.StatusNotFound, map[string]interface{}{"error": contract.ErrorCodeResourceNotFound})
		return
	}
	if rctx.TenantID != run.TenantID {
		respondJSON(w, http.StatusForbidden, map[string]interface{}{"error": contract.ErrorCodeTenantAccessDenied})
		return
	}
	// P1-3：服务端校验迁移合法性（非法 target → 400，不落库）。
	if !validRunTransition(run.Status, body.Target) {
		respondJSON(w, http.StatusBadRequest, map[string]interface{}{
			"error": "ILLEGAL_RUN_TRANSITION", "detail": run.Status + " → " + body.Target,
		})
		return
	}
	// P1-3：幂等——command_id 已执行过则返回首次结果（不重复 transition）。
	if body.CommandID != "" && h.cmdDAO != nil {
		existing, err := h.cmdDAO.Get(body.CommandID)
		if err == nil && existing != nil && existing.Operation == "transition" && existing.Status == "done" {
			replayed, _ := h.runDAO.Get(runID)
			respondJSON(w, http.StatusOK, map[string]interface{}{"run": airunToMap(replayed)})
			return
		}
		_ = h.recordControlCommand(runID, "transition", body.CommandID)
	}
	ok, err := h.runDAO.Transition(runID, body.Target, body.ExpectedVersion, time.Now())
	if err != nil {
		respondJSON(w, http.StatusInternalServerError, map[string]interface{}{"error": "run_transition_failed"})
		return
	}
	if !ok {
		respondJSON(w, http.StatusConflict, map[string]interface{}{"error": contract.ErrorCodeRunStateConflict})
		return
	}
	// P1-3：记录 command 为 done（响应丢失后重放返回首次结果）。
	if body.CommandID != "" {
		_ = h.cmdDAO.MarkDone(body.CommandID)
	}
	updated, _ := h.runDAO.Get(runID)
	respondJSON(w, http.StatusOK, map[string]interface{}{"run": airunToMap(updated)})
}

// internalControlPlaneRunCancel 处理 POST .../runs/{id}/cancel。
func (h *Handler) internalControlPlaneRunCancel(w http.ResponseWriter, r *http.Request, runID string) {
	rctx, err := authorizeInternalControlPlane(r, "control_plane.runs.mutate", "ai-orchestrator")
	if err != nil {
		respondInternalQueryError(w, err)
		return
	}
	var body controlPlaneBodyCancel
	_ = json.NewDecoder(r.Body).Decode(&body)
	run, err := h.runDAO.Get(runID)
	if err != nil || run == nil {
		respondJSON(w, http.StatusNotFound, map[string]interface{}{"error": contract.ErrorCodeResourceNotFound})
		return
	}
	if rctx.TenantID != run.TenantID {
		respondJSON(w, http.StatusForbidden, map[string]interface{}{"error": contract.ErrorCodeTenantAccessDenied})
		return
	}
	// P1-3：cancel 是显式 control action，终态不可 cancel（服务端校验）。
	if !validRunTransition(run.Status, "cancelled") {
		respondJSON(w, http.StatusBadRequest, map[string]interface{}{"error": "ILLEGAL_CANCEL", "detail": run.Status})
		return
	}
	// P1-3：幂等——同 command_id 已 cancel 过则返回首次结果。
	if body.CommandID != "" && h.cmdDAO != nil {
		existing, err := h.cmdDAO.Get(body.CommandID)
		if err == nil && existing != nil && existing.Operation == "cancel" && existing.Status == "done" {
			replayed, _ := h.runDAO.Get(runID)
			respondJSON(w, http.StatusOK, map[string]interface{}{"run": airunToMap(replayed)})
			return
		}
		_ = h.recordControlCommand(runID, "cancel", body.CommandID)
	}
	// P1-3：用请求体 expected_version（若提供）做 CAS；否则用当前 version。
	exp := run.StateVersion
	if body.CommandID != "" && body.ExpectedVersion >= 0 {
		exp = body.ExpectedVersion
	}
	ok, err := h.runDAO.Cancel(runID, exp, time.Now())
	if err != nil {
		respondJSON(w, http.StatusInternalServerError, map[string]interface{}{"error": "run_cancel_failed"})
		return
	}
	if !ok {
		respondJSON(w, http.StatusConflict, map[string]interface{}{"error": contract.ErrorCodeRunCancelled})
		return
	}
	if body.CommandID != "" {
		_ = h.cmdDAO.MarkDone(body.CommandID)
	}
	updated, _ := h.runDAO.Get(runID)
	respondJSON(w, http.StatusOK, map[string]interface{}{"run": airunToMap(updated)})
}

// internalControlPlaneRunGet 处理 GET .../runs/{id}。
func (h *Handler) internalControlPlaneRunGet(w http.ResponseWriter, r *http.Request, runID string) {
	rctx, err := authorizeInternalControlPlane(r, "control_plane.runs.recover", "ai-orchestrator")
	if err != nil {
		respondInternalQueryError(w, err)
		return
	}
	run, err := h.runDAO.Get(runID)
	if err != nil || run == nil {
		respondJSON(w, http.StatusNotFound, map[string]interface{}{"error": contract.ErrorCodeResourceNotFound})
		return
	}
	if rctx.TenantID != run.TenantID {
		respondJSON(w, http.StatusForbidden, map[string]interface{}{"error": contract.ErrorCodeTenantAccessDenied})
		return
	}
	respondJSON(w, http.StatusOK, map[string]interface{}{"run": airunToMap(run)})
}

// internalControlPlaneRunList 处理 GET .../runs?tenant_id=。
func (h *Handler) internalControlPlaneRunList(w http.ResponseWriter, r *http.Request) {
	rctx, err := authorizeInternalControlPlane(r, "control_plane.runs.recover", "ai-orchestrator")
	if err != nil {
		respondInternalQueryError(w, err)
		return
	}
	runs, err := h.runDAO.List(rctx.TenantID)
	if err != nil {
		respondJSON(w, http.StatusInternalServerError, map[string]interface{}{"error": "run_list_failed"})
		return
	}
	out := make([]map[string]interface{}, 0, len(runs))
	for _, rn := range runs {
		out = append(out, airunToMap(&rn))
	}
	respondJSON(w, http.StatusOK, map[string]interface{}{"runs": out, "total": len(out)})
}

// internalControlPlaneRunUnfinished 处理 GET .../runs/unfinished（重启恢复）。
func (h *Handler) internalControlPlaneRunUnfinished(w http.ResponseWriter, r *http.Request) {
	if _, err := authorizeInternalControlPlane(r, "control_plane.runs.recover", "ai-orchestrator"); err != nil {
		respondInternalQueryError(w, err)
		return
	}
	runs, err := h.runDAO.ScanUnfinished()
	if err != nil {
		respondJSON(w, http.StatusInternalServerError, map[string]interface{}{"error": "run_scan_failed"})
		return
	}
	out := make([]map[string]interface{}, 0, len(runs))
	for _, rn := range runs {
		out = append(out, airunToMap(&rn))
	}
	respondJSON(w, http.StatusOK, map[string]interface{}{"runs": out, "total": len(out)})
}

// recordControlCommand 幂等记录 control command（command_id 唯一，供重启恢复）。
func (h *Handler) recordControlCommand(runID, operation, commandID string) error {
	if h.cmdDAO == nil || commandID == "" {
		return nil
	}
	_, err := h.cmdDAO.Create(store.AIControlCommand{
		CommandID: commandID, RunID: runID, Operation: operation, IdempotencyKey: commandID,
	})
	return err
}

// airunToMap 把 AIRun 转为 JSON map。
func airunToMap(r *store.AIRun) map[string]interface{} {
	if r == nil {
		return map[string]interface{}{}
	}
	return map[string]interface{}{
		"run_id":             r.RunID,
		"request_id":         r.RequestID,
		"tenant_id":          r.TenantID,
		"principal":          r.Principal,
		"principal_type":     r.PrincipalType,
		"scope_kind":         r.ScopeKind,
		"primary_cluster_id": nullableStringValue(r.PrimaryClusterID),
		"intent":             r.Intent,
		"action_mode":        r.ActionMode,
		"status":             r.Status,
		"state_version":      r.StateVersion,
		"created_at":         r.CreatedAt.Format(time.RFC3339),
		"updated_at":         r.UpdatedAt.Format(time.RFC3339),
	}
}
