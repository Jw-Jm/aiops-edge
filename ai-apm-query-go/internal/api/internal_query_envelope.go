package api

import (
	"net/http"
	"time"

	trustedauth "github.com/observability-platform/ai-apm-query-go/internal/auth"
	"github.com/observability-platform/ai-apm-query-go/internal/contract"
	"github.com/observability-platform/ai-apm-query-go/internal/store"
)

// ═══════════════════════════════════════════════════════════════════════════
// P6.2e Canonical Internal Query Boundary
//
// 统一 strict internal envelope，供所有 /internal/v1/query/* 端点复用：
//   - service authentication（X-Internal-Token）
//   - EdDSA TrustedRequestContext V2（orchestrator → query-api）——ONLY，无 JWT fallback
//   - nonce / replay / expiry / audience 校验
//   - 要求 cluster scope
//   - capability 校验（route → capability 固定映射）
//   - body tenant/cluster 与 trusted context 完全一致，否则 CONTEXT_SCOPE_MISMATCH
//   - 注入可信 tenant/cluster
//
// 禁止 internal route → JWT fallback；禁止 body 覆盖可信 context。
// ═══════════════════════════════════════════════════════════════════════════

// routeCapability 固定每个 canonical internal query route 的 capability（契约 §31）。
var routeCapability = map[string]string{
	"/internal/v1/query/metrics":            "observability.metrics.read",
	"/internal/v1/query/logs":               "observability.logs.read",
	"/internal/v1/query/traces":             "observability.traces.read",
	"/internal/v1/query/alerts":             "observability.alerts.read",
	"/internal/v1/query/events":             "kubernetes.events.read",
	"/internal/v1/query/topology":           "observability.topology.read",
	"/internal/v1/query/kubernetes":         "kubernetes.resources.read",
	"/internal/v1/query/changes":            "changes.read",
	"/internal/v1/query/knowledge":          "knowledge.search",
	"/internal/v1/query/graph":              "knowledge.graph.read",
	"/internal/v1/query/kubevirt":           "kubevirt.resources.read",
	"/internal/v1/query/hardware/inventory": "hardware.inventory.read",
	"/internal/v1/query/hardware/health":    "hardware.health.read",
	"/internal/v1/query/catalog":            "catalog.read",
	"/internal/v1/query/network-topology":   "network.topology.read",
}

// internalQueryCtx 是 internal query 的可信作用域（服务端注入，body 不得覆盖）。
type internalQueryCtx struct {
	TenantID      string
	ClusterID     string
	Capability    string
	RunID         string
	WorkloadKind  string
	PrincipalType string
	PrincipalID   string
	SessionID     string
}

// internalQueryError 是 internal query 边界的结构化错误（对齐契约 §58 错误码）。
type internalQueryError struct {
	Code    string
	Message string
}

func (e *internalQueryError) Error() string { return e.Code + ": " + e.Message }

// httpStatus 映射内部边界错误到契约 HTTP 状态码。
func (e *internalQueryError) httpStatus() int { return contract.HTTPStatusCode(e.Code) }

// authorizeInternalQuery 统一鉴权 internal query route。
// 仅接受 TrustedRequestContext V2；JWT / 旧 RequestContext 一律 permission_denied。
func authorizeInternalQuery(r *http.Request, capability string) (*internalQueryCtx, error) {
	internalVerifierMu.RLock()
	configured := internalVerifier
	internalVerifierMu.RUnlock()
	if configured == nil {
		return nil, &internalQueryError{Code: contract.ErrorCodeServiceAuthFailed, Message: "internal verifier not configured"}
	}
	if err := trustedauth.VerifyServiceToken(r.Header.Get("X-Internal-Token"), *configured); err != nil {
		return nil, &internalQueryError{Code: contract.ErrorCodeServiceAuthFailed, Message: "service auth failed"}
	}
	// 仅接受 V9.2 TrustedRequestContext；无签名 context 或 JWT 均 fail-closed。
	ctx, err := trustedauth.VerifyTrustedRequestContextV2(r.Header.Get("X-Trusted-Request-Context"), *configured, time.Now())
	if err != nil {
		return nil, mapTrustedContextVerifyError(err)
	}
	// 要求 cluster scope（契约：metrics/logs/traces/alerts/topology/kubernetes/changes/knowledge 均为 cluster scope）。
	if ctx.ScopeKind != "cluster" || ctx.ClusterID == "" {
		return nil, &internalQueryError{Code: contract.ErrorCodeInvalidContext, Message: "internal query requires cluster scope"}
	}
	// capability 校验（route → 固定 capability）。
	if ctx.Capability != capability {
		return nil, &internalQueryError{Code: contract.ErrorCodeTenantAccessDenied, Message: "unauthorized capability: " + ctx.Capability}
	}
	// P1.1 作用域授权校验：cluster 必须已注册且属于该 tenant（tenant_clusters 归属）。
	// 未授权 → 403 TENANT_ACCESS_DENIED（不是 NO_DATA，避免把"身份未授权"伪装成"无数据"）。
	if !internalScopeAuthorized(ctx.TenantID, ctx.ClusterID) {
		return nil, &internalQueryError{Code: contract.ErrorCodeTenantAccessDenied, Message: "unauthorized tenant/cluster scope"}
	}
	return &internalQueryCtx{
		TenantID:      ctx.TenantID,
		ClusterID:     ctx.ClusterID,
		Capability:    ctx.Capability,
		RunID:         ctx.RunID,
		WorkloadKind:  ctx.WorkloadKind,
		PrincipalType: ctx.PrincipalType,
		PrincipalID:   ctx.PrincipalID,
		SessionID:     ctx.SessionID,
	}, nil
}

// internalScopeAuthorized 校验 cluster 属于 tenant（Cluster Registry / tenant_clusters）。
// DB 不可达 / 无归属 → false（fail-closed 403）。
func internalScopeAuthorized(tenantID, clusterID string) bool {
	db := store.GetDB()
	if db == nil {
		return false
	}
	owner, err := (&store.ClusterDAO{}).TenantClustersForCluster(clusterID)
	if err != nil || owner == "" {
		return false
	}
	return owner == tenantID
}

// checkScopeMatch 校验请求体 tenant/cluster 与可信 context 完全一致。
// 不一致 → CONTEXT_SCOPE_MISMATCH（409）；body 不得覆盖可信 context。
func checkScopeMatch(rctx *internalQueryCtx, bodyTenant, bodyCluster string) error {
	if bodyTenant != "" && bodyTenant != rctx.TenantID {
		return &internalQueryError{Code: contract.ErrorCodeContextScopeMismatch, Message: "tenant scope mismatch"}
	}
	if bodyCluster != "" && bodyCluster != rctx.ClusterID {
		return &internalQueryError{Code: contract.ErrorCodeContextScopeMismatch, Message: "cluster scope mismatch"}
	}
	return nil
}

// mapTrustedContextVerifyError 将 TrustedContext 校验错误映射为契约错误码。
func mapTrustedContextVerifyError(err error) error {
	switch err {
	case trustedauth.ErrExpiredContext:
		return &internalQueryError{Code: contract.ErrorCodeContextExpired, Message: "expired trusted context"}
	case trustedauth.ErrReplayedContext:
		return &internalQueryError{Code: contract.ErrorCodeContextReplayed, Message: "replayed trusted context"}
	case trustedauth.ErrWrongAudience, trustedauth.ErrWrongContextType, trustedauth.ErrInvalidContext:
		return &internalQueryError{Code: contract.ErrorCodeInvalidContext, Message: err.Error()}
	default:
		return &internalQueryError{Code: contract.ErrorCodeServiceAuthFailed, Message: "invalid trusted context"}
	}
}

// respondInternalQueryError 统一渲染 internal query 边界错误。
func respondInternalQueryError(w http.ResponseWriter, err error) {
	if ie, ok := err.(*internalQueryError); ok {
		respondJSON(w, ie.httpStatus(), map[string]string{"error": ie.Code, "message": ie.Message})
		return
	}
	respondJSON(w, http.StatusInternalServerError, map[string]string{"error": "INTERNAL_ERROR", "message": err.Error()})
}
