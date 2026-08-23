package api

import (
	"net/http"
	"time"

	trustedauth "github.com/observability-platform/ai-apm-query-go/internal/auth"
	"github.com/observability-platform/ai-apm-query-go/internal/contract"
	"github.com/observability-platform/ai-apm-query-go/internal/store"
)

// ─────────────────────────────────────────────────────────────────────────────
// P10 (V9.3 Phase 10) — control-plane 专用鉴权器（P10 完整闭环 Plan B）。
//
// orchestrator（system principal）经 /internal/v1/control-plane/* 让 query-api
// （persistence owner）做 CAS + 持久化 Run/Event。鉴权要求：
//   - X-Internal-Token 服务凭证（同 internal query 底座）
//   - TrustedRequestContext V2（orchestrator 签发，issuer=ai-orchestrator）
//   - principal_type == system（非用户/Agent）
//   - 精确 capability（route → control-plane.* 固定映射）
//   - issuer == expectedIssuer（调用方向：orchestrator → query-api）
//
// 与 authorizeInternalQuery 的区别：不要求 cluster scope（Run 可 multi_cluster）。
// capability 域为 control-plane.*（独立内部服务能力域，不进用户 Tool Registry）。
// ─────────────────────────────────────────────────────────────────────────────

// authorizeInternalControlPlane 校验 control-plane 请求（system principal + 精确 capability）。
func authorizeInternalControlPlane(r *http.Request, capability, expectedIssuer string) (*internalQueryCtx, error) {
	internalVerifierMu.RLock()
	configured := internalVerifier
	internalVerifierMu.RUnlock()
	if configured == nil {
		return nil, &internalQueryError{Code: contract.ErrorCodeServiceAuthFailed, Message: "internal verifier not configured"}
	}
	if err := trustedauth.VerifyServiceToken(r.Header.Get("X-Internal-Token"), *configured); err != nil {
		return nil, &internalQueryError{Code: contract.ErrorCodeServiceAuthFailed, Message: "service auth failed"}
	}
	ctx, err := trustedauth.VerifyTrustedRequestContextV2(r.Header.Get("X-Trusted-Request-Context"), *configured, time.Now())
	if err != nil {
		return nil, mapTrustedContextVerifyError(err)
	}
	// control-plane 仅接受 system principal（orchestrator 服务身份），拒绝 user/Agent。
	if ctx.PrincipalType != "system" {
		return nil, &internalQueryError{Code: contract.ErrorCodeTenantAccessDenied, Message: "control-plane requires system principal"}
	}
	// issuer 校验（调用方向 orchestrator → query-api）。
	if expectedIssuer != "" && ctx.Issuer != expectedIssuer {
		return nil, &internalQueryError{Code: contract.ErrorCodeTenantAccessDenied, Message: "unexpected issuer: " + ctx.Issuer}
	}
	// capability 精确匹配（route → control-plane.* 固定映射）。
	if ctx.Capability != capability {
		return nil, &internalQueryError{Code: contract.ErrorCodeTenantAccessDenied, Message: "unauthorized capability: " + ctx.Capability}
	}
	return &internalQueryCtx{
		TenantID:   ctx.TenantID,
		ClusterID:  ctx.ClusterID,
		Capability: ctx.Capability,
	}, nil
}

// authorizeControlPlaneForRun 校验 control-plane 请求并把签名 tenant 绑定到目标 Run
// （P0-3：合法 system context 也不能跨租户读写 Run/Event/恢复数据）。
// 返回 (internalQueryCtx, run) 供 handler 使用；tenant 不匹配 → ErrTenantAccessDenied。
func (h *Handler) authorizeControlPlaneForRun(r *http.Request, capability, expectedIssuer, runID string) (*internalQueryCtx, *store.AIRun, error) {
	ictx, err := authorizeInternalControlPlane(r, capability, expectedIssuer)
	if err != nil {
		return nil, nil, err
	}
	if runID == "" {
		return nil, nil, &internalQueryError{Code: contract.ErrorCodeValidationFailed, Message: "missing run_id"}
	}
	run, err := h.runDAO.Get(runID)
	if err != nil || run == nil {
		return nil, nil, &internalQueryError{Code: contract.ErrorCodeResourceNotFound, Message: "run not found"}
	}
	// 签名 tenant（非空时）必须与目标 Run 的 tenant 一致，防跨租户越权。
	if ictx.TenantID != "" && run.TenantID != ictx.TenantID {
		return nil, nil, &internalQueryError{Code: contract.ErrorCodeTenantAccessDenied, Message: "run tenant does not match signed tenant"}
	}
	return ictx, run, nil
}
