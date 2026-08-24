package api

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strconv"
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
		case "claim":
			// A1-02：执行 Lease claim（capability=control_plane.runs.mutate，run-scoped system principal）
			h.internalControlPlaneRunClaim(w, r, id)
			return
		case "renew":
			// A1-02：续约 Lease（fencing：owner+epoch+token 匹配）
			h.internalControlPlaneRunRenew(w, r, id)
			return
		case "release":
			// A1-02：主动释放 Lease（fencing）
			h.internalControlPlaneRunRelease(w, r, id)
			return
		case "commit":
			// A1-03：Runtime Commit（commit 幂等 + Run 状态推进 + 事件原子）
			h.internalControlPlaneRunCommit(w, r, id)
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

// controlPlaneBodyCancel 是 cancel 请求体（P1-3 + A0-01：cancel 也必须带 command_id + expected_version）。
// ExpectedVersion 用 *int64：能区分"未提供"（nil，fail-closed 400）与合法数值（含 0），
// 不能依赖 Go int64 零值猜测调用方 expected version（报告 8.2）。
type controlPlaneBodyCancel struct {
	ExpectedVersion *int64 `json:"expected_version"`
	CommandID       string `json:"command_id"`
}

// controlPlaneBodyTransition 请求体（A0-01：expected_version 必填，缺省 400 fail-closed）。
type controlPlaneBodyTransition struct {
	ExpectedVersion *int64 `json:"expected_version"`
	Target          string `json:"target"`
	CommandID       string `json:"command_id"`
}

// controlCommandPayloadHash 计算 control command 的稳定业务语义 hash。
// 覆盖 run_id + operation + expected_version + target（transition）。不含
// Authorization / Trusted Context nonce / HTTP 时间戳（报告 27.2 / 5.6）。
func controlCommandPayloadHash(runID, operation string, expectedVersion *int64, target string) string {
	h := sha256.New()
	h.Write([]byte(runID))
	h.Write([]byte{0})
	h.Write([]byte(operation))
	h.Write([]byte{0})
	if expectedVersion != nil {
		h.Write([]byte{byte('v')})
		h.Write([]byte(timeToBytes(*expectedVersion)))
	} else {
		h.Write([]byte{byte('n')})
	}
	h.Write([]byte{0})
	h.Write([]byte(target))
	return hex.EncodeToString(h.Sum(nil))
}

func timeToBytes(n int64) []byte {
	b := make([]byte, 8)
	for i := 0; i < 8; i++ {
		b[i] = byte(n >> (8 * (7 - i)))
	}
	return b
}

// runToResponseJSON 把 AIRun 编码为 control-plane response 的 JSON bytes（供 command 存储 response_json）。
func runToResponseJSON(r *store.AIRun) []byte {
	out, err := json.Marshal(map[string]interface{}{"run": airunToMap(r)})
	if err != nil {
		// Marshal 失败仅发生在不可序列化类型，实际不会；保守返回空 object。
		return []byte(`{"run":{}}`)
	}
	return out
}

// respondRunConflictError 统一处理 control command 幂等/冲突错误。
func respondRunControlError(w http.ResponseWriter, err error) {
	switch err {
	case store.ErrRunControlConflict:
		respondJSON(w, http.StatusConflict, map[string]interface{}{"error": contract.ErrorCodeRunStateConflict})
	case store.ErrCommandIdempotencyReused:
		respondJSON(w, http.StatusConflict, map[string]interface{}{"error": "IDEMPOTENCY_KEY_REUSED"})
	default:
		respondJSON(w, http.StatusInternalServerError, map[string]interface{}{"error": "control_command_failed"})
	}
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
// A0-01：统一走 ApplyRunControlCommandTx（command 幂等 + Run CAS + response 同一事务），
// expected_version 必填（缺省 400），payload hash 语义化幂等。
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
	// A0-01：expected_version 必填（用 *int64 区分 nil），缺省 fail-closed 400，
	// 不得"先读当前 version 再当作 caller expected version"。
	if body.ExpectedVersion == nil {
		respondJSON(w, http.StatusBadRequest, map[string]interface{}{"error": "MISSING_EXPECTED_VERSION"})
		return
	}
	if body.Target == "" {
		respondJSON(w, http.StatusBadRequest, map[string]interface{}{"error": contract.ErrorCodeValidationFailed})
		return
	}
	payloadHash := controlCommandPayloadHash(runID, "transition", body.ExpectedVersion, body.Target)
	cmdDAO := h.cmdDAO
	if cmdDAO == nil {
		cmdDAO = &store.AIControlCommandDAO{}
	}
	resp, _, err := store.ApplyRunControlCommandTx(r.Context(), runID, body.CommandID,
		"transition", payloadHash, cmdDAO, func(tx *sql.Tx) ([]byte, bool, error) {
			run, gerr := h.runDAO.GetTx(tx, runID)
			if gerr != nil {
				return nil, false, gerr
			}
			if run == nil {
				return nil, false, nil
			}
			if rctx.TenantID != run.TenantID {
				return nil, false, nil
			}
			if !validRunTransition(run.Status, body.Target) {
				return nil, false, nil
			}
			ok, terr := h.runDAO.TransitionTx(tx, runID, body.Target, *body.ExpectedVersion, time.Now())
			if terr != nil {
				return nil, false, terr
			}
			if !ok {
				return nil, false, nil
			}
			updated, uerr := h.runDAO.GetTx(tx, runID)
			if uerr != nil {
				return nil, false, uerr
			}
			return runToResponseJSON(updated), true, nil
		})
	if err != nil {
		if err == store.ErrRunControlConflict || err == store.ErrCommandIdempotencyReused {
			respondRunControlError(w, err)
			return
		}
		respondJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"error": "run_transition_failed", "detail": err.Error()})
		return
	}
	// replayed 或首次成功都返回最终 response（幂等重放返回 stored response）。
	respondJSON(w, http.StatusOK, parseRunResponse(resp))
}

// parseRunResponse 把 stored response JSON bytes 解析为 map 响应（幂等重放/首次共用）。
func parseRunResponse(b []byte) map[string]interface{} {
	var out map[string]interface{}
	if err := json.Unmarshal(b, &out); err != nil || out == nil {
		return map[string]interface{}{}
	}
	return out
}

// internalControlPlaneRunCancel 处理 POST .../runs/{id}/cancel。
// A0-01：统一走 ApplyRunControlCommandTx；expected_version 必填（*int64 区分 nil，缺省 400），
// command_id + expected_version 端到端传参（修复 F-02 客户端丢失参数 + 服务端"先读当前 version"缺陷）。
func (h *Handler) internalControlPlaneRunCancel(w http.ResponseWriter, r *http.Request, runID string) {
	rctx, err := authorizeInternalControlPlane(r, "control_plane.runs.mutate", "ai-orchestrator")
	if err != nil {
		respondInternalQueryError(w, err)
		return
	}
	var body controlPlaneBodyCancel
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]interface{}{"error": contract.ErrorCodeValidationFailed})
		return
	}
	// A0-01：expected_version 必填（缺省 fail-closed 400，不能读当前 version 当 caller expected）。
	if body.ExpectedVersion == nil {
		respondJSON(w, http.StatusBadRequest, map[string]interface{}{"error": "MISSING_EXPECTED_VERSION"})
		return
	}
	payloadHash := controlCommandPayloadHash(runID, "cancel", body.ExpectedVersion, "cancelled")
	cmdDAO := h.cmdDAO
	if cmdDAO == nil {
		cmdDAO = &store.AIControlCommandDAO{}
	}
	resp, _, err := store.ApplyRunControlCommandTx(r.Context(), runID, body.CommandID,
		"cancel", payloadHash, cmdDAO, func(tx *sql.Tx) ([]byte, bool, error) {
			run, gerr := h.runDAO.GetTx(tx, runID)
			if gerr != nil {
				return nil, false, gerr
			}
			if run == nil {
				return nil, false, nil
			}
			if rctx.TenantID != run.TenantID {
				return nil, false, nil
			}
			if !validRunTransition(run.Status, "cancelled") {
				return nil, false, nil
			}
			ok, terr := h.runDAO.CancelTx(tx, runID, *body.ExpectedVersion, time.Now())
			if terr != nil {
				return nil, false, terr
			}
			if !ok {
				return nil, false, nil
			}
			updated, uerr := h.runDAO.GetTx(tx, runID)
			if uerr != nil {
				return nil, false, uerr
			}
			return runToResponseJSON(updated), true, nil
		})
	if err != nil {
		if err == store.ErrRunControlConflict || err == store.ErrCommandIdempotencyReused {
			respondRunControlError(w, err)
			return
		}
		respondJSON(w, http.StatusInternalServerError, map[string]interface{}{"error": "run_cancel_failed"})
		return
	}
	respondJSON(w, http.StatusOK, parseRunResponse(resp))
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
// A0-05（F-18）：全局扫描跨所有 tenant 的非终态 Run，要求独立 system capability
// control_plane.runs.recover.global（与单 Run recover 的 control_plane.runs.recover 分离，
// 防止普通恢复身份枚举全量非终态 Run）。支持 limit 分页（默认 200）。
func (h *Handler) internalControlPlaneRunUnfinished(w http.ResponseWriter, r *http.Request) {
	if _, err := authorizeInternalControlPlane(r, "control_plane.runs.recover.global", "ai-orchestrator"); err != nil {
		respondInternalQueryError(w, err)
		return
	}
	limit := 200
	if l, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && l > 0 && l <= 1000 {
		limit = l
	}
	// A2-02：Recovery Scanner——只返回需要恢复的候选（无活跃 Lease / 不在 retry backoff），
	// 有活跃 Lease 的 Run 由当前 owner 继续，不列为候选（避免双 executor 抢同一活跃 Run）。
	candidates, err := h.leaseDAO.ScanRecoveryCandidates(limit)
	if err != nil {
		respondJSON(w, http.StatusInternalServerError, map[string]interface{}{"error": "run_scan_failed"})
		return
	}
	cp.inc("recovery_scan")
	out := make([]map[string]interface{}, 0, len(candidates))
	for _, c := range candidates {
		out = append(out, map[string]interface{}{
			"run_id": c.RunID, "owner_id": c.OwnerID, "epoch": c.Epoch,
			"wait_kind": c.WaitKind, "retry_attempt": c.RetryAttempt,
		})
	}
	respondJSON(w, http.StatusOK, map[string]interface{}{"runs": out, "total": len(out), "recovery_candidates": true})
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
