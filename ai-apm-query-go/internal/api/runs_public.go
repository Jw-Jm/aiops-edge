package api

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/observability-platform/ai-apm-query-go/internal/contract"
	"github.com/observability-platform/ai-apm-query-go/internal/store"
)

// ─────────────────────────────────────────────────────────────────────────────
// P10 (V9.3 Phase 10) — query-api 公共 Run 创建/列表（P10 完整闭环 Plan A）。
//
// Browser 只连 query-api。POST /api/v1/ai/runs 经 AuthMiddleware（JWT + tenant +
// canonical-protected route，capability 语义 = ai.investigate）鉴权后，由 query-api
// 作为 Run 持久化 owner 创建并持久化 Run，并写入 ai_run_outbox 供 dispatcher 可靠
// 派发可信 RunInvocation 给 orchestrator。
//
// 边界：system principal 不能经此创建业务 Run（仅 JWT user 可）；orchestrator 的
// control-plane 仅做后续 transition/event/recovery。
// ─────────────────────────────────────────────────────────────────────────────

// randomUUID 生成 canonical v4 UUID（crypto/rand，避免额外依赖）。
func randomUUID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return ""
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10
	dst := make([]byte, 36)
	hex.Encode(dst[0:8], b[0:4])
	dst[8] = '-'
	hex.Encode(dst[9:13], b[4:6])
	dst[13] = '-'
	hex.Encode(dst[14:18], b[6:8])
	dst[18] = '-'
	hex.Encode(dst[19:23], b[8:10])
	dst[23] = '-'
	hex.Encode(dst[24:36], b[10:16])
	return string(dst)
}

// createRunPublicRequest 是公共 Run 创建请求体。
// idempotency_key（客户端幂等键）非必填；提供则映射为 request_id（唯一域
// (tenant_id, request_id)），使同一 idempotency_key 重试返回首次创建结果（P1-1）。
type createRunPublicRequest struct {
	TenantID       string `json:"tenant_id"`
	ClusterID      string `json:"cluster_id"`      // 必填：调查必须有目标 cluster（P1-1 禁空 cluster 非法 multi-cluster scope）
	IdempotencyKey string `json:"idempotency_key"` // 客户端幂等键 → request_id
	Intent         string `json:"intent"`
	ActionMode     string `json:"action_mode"`
	Service        string `json:"service"`
	ResourceID     string `json:"resource_id"`
	Message        string `json:"message"`
}

// CreateRunPublic handles POST /api/v1/ai/runs（JWT 鉴权 + 创建 + 同事务写 outbox）。
func (h *Handler) CreateRunPublic(w http.ResponseWriter, r *http.Request) {
	auth, ok := requestAuthorizationContext(r)
	if !ok || auth.UserID == "" {
		respondJSON(w, http.StatusForbidden, map[string]interface{}{"error": "permission_denied"})
		return
	}
	if h.runDAO == nil || h.outboxDAO == nil {
		respondJSON(w, http.StatusServiceUnavailable, map[string]interface{}{"error": "persistence_unavailable"})
		return
	}
	var req createRunPublicRequest
	if err := decodeBody(r, &req); err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]interface{}{"error": "invalid body"})
		return
	}
	// 租户一致性：body tenant 必须与 JWT 当前 user 有效 tenant 一致。
	if req.TenantID != "" && req.TenantID != auth.TenantID {
		respondJSON(w, http.StatusForbidden, map[string]interface{}{"error": "TENANT_ACCESS_DENIED"})
		return
	}
	// P1-1：cluster 必填（拒绝空 cluster → 非法 multi-cluster scope），且必须属于当前 tenant。
	if req.ClusterID == "" {
		respondJSON(w, http.StatusUnprocessableEntity, map[string]interface{}{"error": "INVALID_SCOPE", "detail": "cluster_id required"})
		return
	}
	cluster, err := (&store.ClusterDAO{}).GetByClusterID(req.ClusterID)
	if err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]interface{}{"error": "INVALID_CLUSTER"})
		return
	}
	if cluster.TenantID != auth.TenantID {
		respondJSON(w, http.StatusForbidden, map[string]interface{}{"error": "TENANT_ACCESS_DENIED", "detail": "cluster not owned by tenant"})
		return
	}
	// 仅认证 user 显式创建业务 Run（ManualBoundary 语义：人工显式触发）。
	now := time.Now()
	runID := randomUUID()
	requestID := req.IdempotencyKey
	if requestID == "" {
		requestID = randomUUID()
	}
	scopeKind := "single_cluster"
	intent := firstNonEmpty(req.Intent, req.Message)
	// 同事务创建 Run + 写 outbox（P0-5：outbox 失败则回滚，不留下"永不派发"的 Run）。
	created, err := h.runDAO.CreateWithOutbox(
		store.AIRun{
			RunID:            runID,
			RequestID:        requestID,
			TenantID:         auth.TenantID,
			Principal:        auth.UserID,
			PrincipalType:    "user",
			SessionID:        auth.SessionID,
			ScopeKind:        scopeKind,
			PrimaryClusterID: req.ClusterID,
			Intent:           intent,
			ActionMode:       firstNonEmpty(req.ActionMode, "read_only"),
			TargetType:       "service",
			TargetResourceID: firstNonEmpty(req.ResourceID, req.Service),
			Status:           "created",
			StateVersion:     0,
			CreatedAt:        now,
			UpdatedAt:        now,
		},
		store.AIRunOutbox{
			InvocationID: randomUUID(),
			RunID:        runID,
			Status:       "pending",
			CreatedAt:    now,
			UpdatedAt:    now,
		},
	)
	if err != nil {
		respondJSON(w, http.StatusInternalServerError, map[string]interface{}{"error": "run_create_failed"})
		return
	}
	if !created {
		respondJSON(w, http.StatusConflict, map[string]interface{}{"error": "RUN_ALREADY_EXISTS"})
		return
	}
	respondJSON(w, http.StatusCreated, map[string]interface{}{
		"run_id":     runID,
		"request_id": requestID,
		"status":     "created",
		"created_at": now.Format(time.RFC3339),
	})
}

// ListRunsPublic handles GET /api/v1/ai/runs（当前 JWT user 的 tenant Run 列表）。
func (h *Handler) ListRunsPublic(w http.ResponseWriter, r *http.Request) {
	auth, ok := requestAuthorizationContext(r)
	if !ok || auth.UserID == "" {
		respondJSON(w, http.StatusForbidden, map[string]interface{}{"error": "permission_denied"})
		return
	}
	if h.runDAO == nil {
		respondJSON(w, http.StatusServiceUnavailable, map[string]interface{}{"error": "persistence_unavailable"})
		return
	}
	runs, err := h.runDAO.List(auth.TenantID)
	if err != nil {
		respondJSON(w, http.StatusInternalServerError, map[string]interface{}{"error": "run_list_failed"})
		return
	}
	out := make([]map[string]interface{}, 0, len(runs))
	for _, rn := range runs {
		out = append(out, map[string]interface{}{
			"run_id":             rn.RunID,
			"request_id":         rn.RequestID,
			"status":             rn.Status,
			"tenant_id":          rn.TenantID,
			"primary_cluster_id": nullableStringValue(rn.PrimaryClusterID),
			"intent":             rn.Intent,
			"action_mode":        rn.ActionMode,
			"target_type":        nullableStringValue(rn.TargetType),
			"target_resource_id": nullableStringValue(rn.TargetResourceID),
			"created_at":         rn.CreatedAt.Format(time.RFC3339),
		})
	}
	respondJSON(w, http.StatusOK, map[string]interface{}{"runs": out, "total": len(out)})
}

// GetRunPublic handles GET /api/v1/ai/runs/{id}（P1-6：详情直接读 MySQL，消除与
// orchestrator 内存 RunStore 的 split-brain；不再代理到 orchestrator）。
func (h *Handler) GetRunPublic(w http.ResponseWriter, r *http.Request) {
	auth, ok := requestAuthorizationContext(r)
	if !ok || auth.UserID == "" {
		respondJSON(w, http.StatusForbidden, map[string]interface{}{"error": "permission_denied"})
		return
	}
	runID := extractRunIDFromPath(r.URL.Path)
	if runID == "" {
		respondJSON(w, http.StatusBadRequest, map[string]interface{}{"error": contract.ErrorCodeValidationFailed})
		return
	}
	if h.runDAO == nil {
		respondJSON(w, http.StatusServiceUnavailable, map[string]interface{}{"error": "persistence_unavailable"})
		return
	}
	run, err := h.runDAO.Get(runID)
	if err != nil || run == nil {
		respondJSON(w, http.StatusNotFound, map[string]interface{}{"error": contract.ErrorCodeResourceNotFound})
		return
	}
	if run.TenantID != auth.TenantID {
		respondJSON(w, http.StatusForbidden, map[string]interface{}{"error": contract.ErrorCodeTenantAccessDenied})
		return
	}
	respondJSON(w, http.StatusOK, map[string]interface{}{"run": airunToMap(run)})
}

// GetRunToolsPublic handles GET /api/v1/ai/runs/{id}/tools（C2-4：UI Tool activity 只展示
// 真实 ToolRun/Event，不用图节点推断冒充真实工具调用）。
// 返回 Run 的真实 ai_tool_runs（tool_run_id/step/tool/status/result_quality/executor/
// lease_epoch/eligible_for_evidence/digest/window），无则空数组。
func (h *Handler) GetRunToolsPublic(w http.ResponseWriter, r *http.Request) {
	auth, ok := requestAuthorizationContext(r)
	if !ok || auth.UserID == "" {
		respondJSON(w, http.StatusForbidden, map[string]interface{}{"error": "permission_denied"})
		return
	}
	runID := extractRunIDFromPath(r.URL.Path)
	if runID == "" {
		respondJSON(w, http.StatusBadRequest, map[string]interface{}{"error": contract.ErrorCodeValidationFailed})
		return
	}
	if h.runDAO == nil || h.toolDAO == nil {
		respondJSON(w, http.StatusServiceUnavailable, map[string]interface{}{"error": "persistence_unavailable"})
		return
	}
	run, err := h.runDAO.Get(runID)
	if err != nil || run == nil {
		respondJSON(w, http.StatusNotFound, map[string]interface{}{"error": contract.ErrorCodeResourceNotFound})
		return
	}
	if run.TenantID != auth.TenantID {
		respondJSON(w, http.StatusForbidden, map[string]interface{}{"error": contract.ErrorCodeTenantAccessDenied})
		return
	}
	tools, err := h.toolDAO.ListByRun(runID)
	if err != nil {
		respondJSON(w, http.StatusInternalServerError, map[string]interface{}{"error": "tool_list_failed"})
		return
	}
	out := make([]map[string]interface{}, 0, len(tools))
	for _, t := range tools {
		out = append(out, map[string]interface{}{
			"tool_run_id": t.ToolRunID, "step_id": nullableStringValue(t.StepID),
			"tool_name": t.ToolName, "status": t.Status,
			"result_quality":        nullableStringValue(t.ResultQuality),
			"executor_id":           nullableStringValue(t.ExecutorID),
			"lease_epoch_at_start":  t.LeaseEpochAtStart,
			"eligible_for_evidence": t.EligibleForEvidence,
			"result_digest_sha256":  nullableStringValue(t.ResultDigestSHA256),
			"result_truncated":      t.ResultTruncated, "result_count": t.ResultCount,
			"error_message": nullableStringValue(t.ErrorMessage),
		})
	}
	respondJSON(w, http.StatusOK, map[string]interface{}{"tools": out, "total": len(out)})
}

// GetRunEvidencesPublic returns query-api-owned Evidence for a Run.
func (h *Handler) GetRunEvidencesPublic(w http.ResponseWriter, r *http.Request) {
	auth, ok := requestAuthorizationContext(r)
	if !ok || auth.UserID == "" {
		respondJSON(w, http.StatusForbidden, map[string]interface{}{"error": "permission_denied"})
		return
	}
	runID := extractRunIDFromPath(r.URL.Path)
	if h.runDAO == nil || h.evidenceDAO == nil || runID == "" {
		respondJSON(w, http.StatusServiceUnavailable, map[string]interface{}{"error": "persistence_unavailable"})
		return
	}
	run, err := h.runDAO.Get(runID)
	if err != nil || run == nil {
		respondJSON(w, http.StatusNotFound, map[string]interface{}{"error": contract.ErrorCodeResourceNotFound})
		return
	}
	if run.TenantID != auth.TenantID {
		respondJSON(w, http.StatusForbidden, map[string]interface{}{"error": contract.ErrorCodeTenantAccessDenied})
		return
	}
	evs, err := h.evidenceDAO.ListByRun(runID, run.TenantID, run.PrimaryClusterID)
	if err != nil {
		respondJSON(w, http.StatusInternalServerError, map[string]interface{}{"error": "evidence_list_failed"})
		return
	}
	out := make([]map[string]interface{}, 0, len(evs))
	for _, ev := range evs {
		out = append(out, evidenceToMap(&ev))
	}
	respondJSON(w, http.StatusOK, map[string]interface{}{"run_id": runID, "evidences": out, "count": len(out)})
}

// GetRunEvidencePublic returns one Evidence under run + tenant + cluster scope.
func (h *Handler) GetRunEvidencePublic(w http.ResponseWriter, r *http.Request) {
	auth, ok := requestAuthorizationContext(r)
	if !ok || auth.UserID == "" {
		respondJSON(w, http.StatusForbidden, map[string]interface{}{"error": "permission_denied"})
		return
	}
	runID := extractRunIDFromPath(r.URL.Path)
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	evidenceID := ""
	for i := range parts {
		if parts[i] == "evidences" && i+1 < len(parts) {
			evidenceID = parts[i+1]
		}
	}
	if h.runDAO == nil || h.evidenceDAO == nil || runID == "" || evidenceID == "" {
		respondJSON(w, http.StatusServiceUnavailable, map[string]interface{}{"error": "persistence_unavailable"})
		return
	}
	run, err := h.runDAO.Get(runID)
	if err != nil || run == nil {
		respondJSON(w, http.StatusNotFound, map[string]interface{}{"error": contract.ErrorCodeResourceNotFound})
		return
	}
	if run.TenantID != auth.TenantID {
		respondJSON(w, http.StatusForbidden, map[string]interface{}{"error": contract.ErrorCodeTenantAccessDenied})
		return
	}
	ev, err := h.evidenceDAO.GetByID(evidenceID, runID, run.TenantID, run.PrimaryClusterID)
	if err != nil || ev == nil {
		respondJSON(w, http.StatusNotFound, map[string]interface{}{"error": contract.ErrorCodeResourceNotFound})
		return
	}
	respondJSON(w, http.StatusOK, map[string]interface{}{"evidence": evidenceToMap(ev)})
}

func evidenceToMap(ev *store.Evidence) map[string]interface{} {
	return map[string]interface{}{
		"evidence_id": ev.EvidenceID, "run_id": ev.RunID, "tenant_id": ev.TenantID,
		"cluster_id": ev.ClusterID, "evidence_type": ev.EvidenceType,
		"source_ref": ev.SourceRef, "raw_ref": ev.RawRef,
		"raw_digest_sha256": ev.RawDigestSHA256, "summary": ev.Summary,
		"metadata":               json.RawMessage(ev.MetadataJSON),
		"provenance_fingerprint": ev.ProvenanceFingerprint,
		"collected_at":           ev.CollectedAt.Format(time.RFC3339),
	}
}

func nullableStringValue(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}
