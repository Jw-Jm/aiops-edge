package api

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/observability-platform/ai-apm-query-go/internal/contract"
	"github.com/observability-platform/ai-apm-query-go/internal/store"
)

// ─────────────────────────────────────────────────────────────────────────────
// A1（0004_runtime_convergence）：Run execution Lease + Runtime Commit 端点。
//
// capability：claim/renew/release/commit 均用 run-scoped control_plane.runs.mutate
// （system principal，授权给目标 Run）。claim 之外的操作带 lease epoch + token（fencing）。
// ─────────────────────────────────────────────────────────────────────────────

const defaultLeaseSeconds = 60

// internalControlPlaneRunClaim handles POST /internal/v1/control-plane/runs/{id}/claim。
func (h *Handler) internalControlPlaneRunClaim(w http.ResponseWriter, r *http.Request, runID string) {
	if _, _, err := h.authorizeControlPlaneForRun(r, "control_plane.runs.mutate", "ai-orchestrator", runID); err != nil {
		respondInternalQueryError(w, err)
		return
	}
	var body struct {
		OwnerID      string `json:"owner_id"`
		LeaseSeconds *int   `json:"lease_seconds"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	if body.OwnerID == "" {
		respondJSON(w, http.StatusBadRequest, map[string]interface{}{"error": "MISSING_OWNER_ID"})
		return
	}
	leaseSeconds := defaultLeaseSeconds
	if body.LeaseSeconds != nil && *body.LeaseSeconds > 0 && *body.LeaseSeconds <= 300 {
		leaseSeconds = *body.LeaseSeconds
	}
	holder, err := h.leaseDAO.Claim(runID, body.OwnerID, leaseSeconds)
	if err != nil {
		cp.inc("lease_fencing")
		respondLeaseError(w, err)
		return
	}
	cp.inc("lease_claim")
	respondJSON(w, http.StatusOK, map[string]interface{}{
		"run_id":     holder.RunID,
		"owner_id":   holder.OwnerID,
		"epoch":      holder.Epoch,
		"claim_id":   holder.ClaimID,
		"token":      holder.Token, // 明文只在本响应返回，DB 只存 hash
		"token_hash": holder.TokenHash,
		"expires_at": holder.ExpiresAt.UTC().Format(time.RFC3339),
		"wait_kind":  holder.WaitKind,
	})
}

// internalControlPlaneRunRenew handles POST /internal/v1/control-plane/runs/{id}/renew。
// fencing：owner + epoch + token 匹配才续约。
func (h *Handler) internalControlPlaneRunRenew(w http.ResponseWriter, r *http.Request, runID string) {
	if _, _, err := h.authorizeControlPlaneForRun(r, "control_plane.runs.mutate", "ai-orchestrator", runID); err != nil {
		respondInternalQueryError(w, err)
		return
	}
	var body struct {
		OwnerID      string `json:"owner_id"`
		Epoch        int64  `json:"epoch"`
		Token        string `json:"token"`
		LeaseSeconds *int   `json:"lease_seconds"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	if body.OwnerID == "" || body.Token == "" || body.Epoch <= 0 {
		respondJSON(w, http.StatusBadRequest, map[string]interface{}{"error": "MISSING_LEASE_FENCING"})
		return
	}
	leaseSeconds := defaultLeaseSeconds
	if body.LeaseSeconds != nil && *body.LeaseSeconds > 0 && *body.LeaseSeconds <= 300 {
		leaseSeconds = *body.LeaseSeconds
	}
	expires, err := h.leaseDAO.Renew(runID, body.OwnerID, body.Epoch, hashToken(body.Token), leaseSeconds)
	if err != nil {
		cp.inc("lease_fencing")
		respondLeaseError(w, err)
		return
	}
	cp.inc("lease_renew")
	respondJSON(w, http.StatusOK, map[string]interface{}{
		"expires_at": expires.UTC().Format(time.RFC3339),
	})
}

// internalControlPlaneRunRelease handles POST /internal/v1/control-plane/runs/{id}/release。
// fencing：epoch + token 匹配才释放（防 old owner 释放 new owner）。
func (h *Handler) internalControlPlaneRunRelease(w http.ResponseWriter, r *http.Request, runID string) {
	if _, _, err := h.authorizeControlPlaneForRun(r, "control_plane.runs.mutate", "ai-orchestrator", runID); err != nil {
		respondInternalQueryError(w, err)
		return
	}
	var body struct {
		Epoch int64  `json:"epoch"`
		Token string `json:"token"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	if body.Token == "" || body.Epoch <= 0 {
		respondJSON(w, http.StatusBadRequest, map[string]interface{}{"error": "MISSING_LEASE_FENCING"})
		return
	}
	if err := h.leaseDAO.Release(runID, body.Epoch, hashToken(body.Token)); err != nil {
		respondLeaseError(w, err)
		return
	}
	respondJSON(w, http.StatusOK, map[string]interface{}{"released": true})
}

// controlPlaneBodyCommit 是 Runtime Commit 请求体。
type controlPlaneBodyCommit struct {
	CommitID    string          `json:"commit_id"`
	PayloadHash string          `json:"payload_hash"`
	Target      string          `json:"target"`      // 提交后推进到的 Run 状态（终态或可等待态）
	Result      json.RawMessage `json:"result"`      // 首次成功响应（响应丢失重试返回）
	Events      []commitEvent   `json:"events"`      // 本 commit 原子追加的事件
	ExpectedVersion int64       `json:"expected_version"` // Run CAS version
	OwnerID     string          `json:"owner_id"`
	Epoch       int64           `json:"epoch"`
	Token       string          `json:"token"`
}

type commitEvent struct {
	EventID   string          `json:"event_id"`
	EventType string          `json:"event_type"`
	Payload   json.RawMessage `json:"payload"`
}

// internalControlPlaneRunCommit handles POST /internal/v1/control-plane/runs/{id}/commit。
// 幂等：同 commit_id 已存在 → 返回首次提交的 response_json（响应丢失重试）。
// 原子：commit 记录 + Run 状态推进（CAS）+ 事件 AppendTx 在同一事务。
// fencing：owner + epoch + token 匹配，且 commit 前重新校验 DB-time Lease（防 Lease 过期后提交）。
func (h *Handler) internalControlPlaneRunCommit(w http.ResponseWriter, r *http.Request, runID string) {
	if _, _, err := h.authorizeControlPlaneForRun(r, "control_plane.runs.mutate", "ai-orchestrator", runID); err != nil {
		respondInternalQueryError(w, err)
		return
	}
	var body controlPlaneBodyCommit
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]interface{}{"error": "INVALID_BODY"})
		return
	}
	if body.CommitID == "" || body.OwnerID == "" || body.Token == "" || body.Epoch <= 0 || body.PayloadHash == "" {
		respondJSON(w, http.StatusBadRequest, map[string]interface{}{"error": "MISSING_COMMIT_FIELDS"})
		return
	}
	// 幂等命中：同 commit_id 已存在 → 返回首次结果（不重复执行）。
	if existing, err := h.commitDAO.Get(runID, body.CommitID); err == nil {
		cp.inc("commit_idempotent")
		respondJSON(w, http.StatusOK, map[string]interface{}{
			"idempotent": true, "commit_id": existing.CommitID,
			"result_status": existing.ResultStatus,
			"result":        json.RawMessage(existing.ResponseJSON),
		})
		return
	} else if !errors.Is(err, store.ErrCommitNotFound) {
		respondJSON(w, http.StatusInternalServerError, map[string]interface{}{"error": "commit_lookup_failed"})
		return
	}

	// 事务：Lease fencing 校验 + Run 状态 CAS 推进 + 事件 AppendTx + commit 记录。
	err := h.applyRuntimeCommitTx(runID, body)
	if err != nil {
		cp.inc("commit")
		respondLeaseError(w, err)
		return
	}
	cp.inc("commit")
	respondJSON(w, http.StatusOK, map[string]interface{}{
		"commit_id": body.CommitID, "idempotent": false,
		"result_status": body.Target, "result": body.Result,
	})
}

// applyRuntimeCommitTx 在单事务内完成 Runtime Commit。
func (h *Handler) applyRuntimeCommitTx(runID string, body controlPlaneBodyCommit) error {
	conn := store.GetDB()
	if conn == nil {
		return errors.New("mysql unavailable")
	}
	tx, err := conn.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// 1) Lease fencing：owner + epoch + token 匹配，且 Lease 未过期（DB time）。
	valid, err := store.LeaseFencingTx(tx, runID, body.OwnerID, body.Epoch, hashToken(body.Token))
	if err != nil {
		return err
	}
	if !valid {
		return store.ErrLeaseFencing
	}

	// 2) Run 状态 CAS 推进（expected_version）。
	now := time.Now()
	ok, err := h.runDAO.TransitionTx(tx, runID, body.Target, body.ExpectedVersion, now)
	if err != nil {
		return err
	}
	if !ok {
		return &runStateConflictError{}
	}

	// 3) 事件 AppendTx（原子，不留下孤立 event/sequence）。
	firstSeq, lastSeq := int64(0), int64(0)
	for _, ev := range body.Events {
		appended, created, err := h.eventDAO.AppendTx(tx, store.AIRunEvent{
			RunID: runID, EventID: ev.EventID, EventType: ev.EventType, Payload: ev.Payload,
		})
		if err != nil {
			return err
		}
		if created {
			if firstSeq == 0 || appended.Sequence < firstSeq {
				firstSeq = appended.Sequence
			}
			if appended.Sequence > lastSeq {
				lastSeq = appended.Sequence
			}
		}
	}

	// 4) commit 记录。
	commit := store.RuntimeCommit{
		RunID: runID, CommitID: body.CommitID, PayloadHash: body.PayloadHash,
		CommittedStateVersion: body.ExpectedVersion + 1,
		ResultStatus:          body.Target,
		FirstEventSequence:    firstSeq, LastEventSequence: lastSeq,
		ResponseJSON: body.Result,
	}
	if err := h.commitDAO.CreateTx(tx, commit); err != nil {
		if errors.Is(err, store.ErrCommitDuplicate) {
			// 竞态：另一并发已提交同 commit_id → 回滚并返回幂等命中（不重复执行）。
			return &commitDuplicateError{}
		}
		return err
	}
	return tx.Commit()
}

// internalControlPlaneToolEvidenceConsume handles
// POST /internal/v1/control-plane/tools/{id}/evidence/consume（27.14 Evidence 一次消费）。
// capability=control_plane.evidence.consume，system principal，run-scoped。
func (h *Handler) internalControlPlaneToolEvidenceConsume(w http.ResponseWriter, r *http.Request, toolRunID string) {
	if _, _, err := h.authorizeControlPlaneForRun(r, "control_plane.evidence.consume", "ai-orchestrator", toolRunID); err != nil {
		respondInternalQueryError(w, err)
		return
	}
	var body struct {
		RunID                string          `json:"run_id"`
		TenantID             string          `json:"tenant_id"`
		ClusterID            string          `json:"cluster_id"`
		EvidenceID           string          `json:"evidence_id"`
		EvidenceType         string          `json:"evidence_type"`
		SourceRef            string          `json:"source_ref"`
		RawRef               string          `json:"raw_ref"`
		RawDigestSHA256      string          `json:"raw_digest_sha256"`
		Summary              string          `json:"summary"`
		Metadata             json.RawMessage `json:"metadata"`
		ProvenanceFingerprint string         `json:"provenance_fingerprint"`
		AllowedStatuses      string          `json:"allowed_statuses"` // 逗号分隔（success,partial,no_data）
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]interface{}{"error": "INVALID_BODY"})
		return
	}
	if body.RunID == "" || body.TenantID == "" || body.ClusterID == "" || body.EvidenceID == "" {
		respondJSON(w, http.StatusBadRequest, map[string]interface{}{"error": "MISSING_EVIDENCE_FIELDS"})
		return
	}
	conn := store.GetDB()
	if conn == nil {
		respondJSON(w, http.StatusInternalServerError, map[string]interface{}{"error": "mysql_unavailable"})
		return
	}
	tx, err := conn.Begin()
	if err != nil {
		respondJSON(w, http.StatusInternalServerError, map[string]interface{}{"error": "tx_begin_failed"})
		return
	}
	defer tx.Rollback()
	consumed, err := h.evidenceDAO.ConsumeToolRunAsEvidence(tx, store.Evidence{
		EvidenceID: body.EvidenceID, RunID: body.RunID, TenantID: body.TenantID,
		ClusterID: body.ClusterID, EvidenceType: body.EvidenceType, SourceRef: body.SourceRef,
		RawRef: body.RawRef, RawDigestSHA256: body.RawDigestSHA256, Summary: body.Summary,
		MetadataJSON: body.Metadata, ProvenanceFingerprint: body.ProvenanceFingerprint,
		CollectedAt: time.Now(),
	}, toolRunID, body.AllowedStatuses)
	if err != nil {
		if errors.Is(err, store.ErrEvidenceNotEligible) {
			respondJSON(w, http.StatusConflict, map[string]interface{}{"error": "EVIDENCE_NOT_ELIGIBLE"})
			return
		}
		respondJSON(w, http.StatusInternalServerError, map[string]interface{}{"error": "evidence_consume_failed"})
		return
	}
	if !consumed {
		respondJSON(w, http.StatusConflict, map[string]interface{}{"error": "EVIDENCE_NOT_ELIGIBLE"})
		return
	}
	if err := tx.Commit(); err != nil {
		respondJSON(w, http.StatusInternalServerError, map[string]interface{}{"error": "tx_commit_failed"})
		return
	}
	respondJSON(w, http.StatusOK, map[string]interface{}{
		"evidence_id": body.EvidenceID, "consumed": true, "tool_run_id": toolRunID,
	})
}

// ── helpers ───────────────────────────────────────────────────────────────

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func respondLeaseError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrLeaseHeld):
		respondJSON(w, http.StatusConflict, map[string]interface{}{"error": "RUN_LEASE_HELD"})
	case errors.Is(err, store.ErrLeaseFencing):
		respondJSON(w, http.StatusConflict, map[string]interface{}{"error": "RUN_LEASE_FENCING"})
	case errors.Is(err, store.ErrRunTerminal):
		respondJSON(w, http.StatusConflict, map[string]interface{}{"error": contract.ErrorCodeRunStateConflict})
	case errors.Is(err, store.ErrRunNotFound):
		respondJSON(w, http.StatusNotFound, map[string]interface{}{"error": contract.ErrorCodeResourceNotFound})
	default:
		var rbe *store.RetryBackoffError
		if errors.As(err, &rbe) {
			respondJSON(w, http.StatusConflict, map[string]interface{}{"error": "RUN_RETRY_BACKOFF"})
			return
		}
		var rsc *runStateConflictError
		if errors.As(err, &rsc) {
			respondJSON(w, http.StatusConflict, map[string]interface{}{"error": contract.ErrorCodeRunStateConflict})
			return
		}
		var cde *commitDuplicateError
		if errors.As(err, &cde) {
			respondJSON(w, http.StatusOK, map[string]interface{}{"error": "IDEMPOTENT_COMMIT", "idempotent": true})
			return
		}
		respondJSON(w, http.StatusInternalServerError, map[string]interface{}{"error": "lease_error"})
	}
}

type runStateConflictError struct{}
type commitDuplicateError struct{}

func (e *runStateConflictError) Error() string { return "run state version conflict" }
func (e *commitDuplicateError) Error() string  { return "commit already exists" }
