// Package contract errors: the canonical Go-side error code surface shared with
// ai-orchestrator/contracts.py and observability-frontend/src/api/contracts.ts.
//
// The Go API historically used literal strings for structured errors; these
// constants centralize the codes so every layer agrees on the exact token,
// including the P3.10c-final CLUSTER_IDENTITY_MISMATCH binding conflict.
package contract

// Unified V9.2 §58 error codes. Keep in sync with contracts.py ErrorCode and
// contracts.ts ErrorCode.
const (
	ErrorCodeAuthRequired          = "AUTH_REQUIRED"
	ErrorCodeSessionRevoked        = "SESSION_REVOKED"
	ErrorCodeServiceAuthFailed     = "SERVICE_AUTH_FAILED"
	ErrorCodeInvalidContext        = "INVALID_CONTEXT"
	ErrorCodeContextExpired        = "CONTEXT_EXPIRED"
	ErrorCodeContextReplayed       = "CONTEXT_REPLAYED"
	ErrorCodeContextScopeMismatch  = "CONTEXT_SCOPE_MISMATCH"
	ErrorCodeTenantAccessDenied    = "TENANT_ACCESS_DENIED"
	ErrorCodeClusterAccessDenied   = "CLUSTER_ACCESS_DENIED"
	ErrorCodeResourceNotFound      = "RESOURCE_NOT_FOUND"
	ErrorCodeResourceAmbiguous     = "RESOURCE_AMBIGUOUS"
	ErrorCodeClusterUnavailable    = "CLUSTER_UNAVAILABLE"
	ErrorCodeClusterIdentityMismatch = "CLUSTER_IDENTITY_MISMATCH"
	ErrorCodeNoData                = "NO_DATA"
	ErrorCodeBackendUnavailable    = "BACKEND_UNAVAILABLE"
	ErrorCodeToolUnavailable       = "TOOL_UNAVAILABLE"
	ErrorCodeToolTimeout           = "TOOL_TIMEOUT"
	ErrorCodeValidationFailed      = "VALIDATION_FAILED"
	ErrorCodeRunStateConflict      = "RUN_STATE_CONFLICT"
	ErrorCodeRunCancelled          = "RUN_CANCELLED"
	ErrorCodeActionNotAllowed      = "ACTION_NOT_ALLOWED"
	ErrorCodeActionConfirmationRequired = "ACTION_CONFIRMATION_REQUIRED"
	ErrorCodeActionApprovalRequired = "ACTION_APPROVAL_REQUIRED"
	ErrorCodeApprovalExpired       = "APPROVAL_EXPIRED"
	ErrorCodeApprovalScopeMismatch = "APPROVAL_SCOPE_MISMATCH"
	ErrorCodeResourceVersionConflict = "RESOURCE_VERSION_CONFLICT"
	ErrorCodeMaintenanceMode       = "MAINTENANCE_MODE"
	// V2 P0 错误码合同（报告 §36 P0-ERROR）：Lease/Tool fencing。
	ErrorCodeRunLeaseLost       = "RUN_LEASE_LOST"
	ErrorCodeClaimIDReused      = "CLAIM_ID_REUSED"
	ErrorCodeClaimIDExpired     = "CLAIM_ID_EXPIRED"
	ErrorCodeToolLeaseLost      = "TOOL_LEASE_LOST"
	ErrorCodeToolResultStale    = "TOOL_RESULT_STALE"
	// Stage D 接线（报告 §29）：executor 执行结果状态机。
	ErrorCodeActionNotApproved      = "ACTION_NOT_APPROVED"      // action 未 approved，拒绝执行
	ErrorCodeActionAlreadyExecuted  = "ACTION_ALREADY_EXECUTED"  // durable idempotency：已执行过
	ErrorCodeExecutorRejected       = "EXECUTOR_REJECTED"        // executor 拒绝（含 disabled 403）
	ErrorCodeExecutionUnknown       = "EXECUTION_UNKNOWN"        // 外部 mutation 状态未知，需 reconcile
	ErrorCodeExecutorUnavailable    = "EXECUTOR_UNAVAILABLE"     // executor 不可达
)

// HTTPStatusCode maps a unified error code to its canonical V9.2 §58 HTTP status.
// CLUSTER_IDENTITY_MISMATCH is a binding conflict → 409, not a backend outage.
func HTTPStatusCode(code string) int {
	switch code {
	case ErrorCodeAuthRequired, ErrorCodeSessionRevoked, ErrorCodeServiceAuthFailed,
		ErrorCodeInvalidContext, ErrorCodeContextExpired, ErrorCodeContextReplayed:
		return 401
	case ErrorCodeTenantAccessDenied, ErrorCodeClusterAccessDenied:
		return 403
	case ErrorCodeResourceNotFound:
		return 404
	case ErrorCodeResourceAmbiguous, ErrorCodeContextScopeMismatch,
		ErrorCodeClusterIdentityMismatch, ErrorCodeRunStateConflict,
		ErrorCodeRunCancelled, ErrorCodeResourceVersionConflict,
		ErrorCodeApprovalScopeMismatch:
		return 409
	case ErrorCodeValidationFailed, ErrorCodeActionNotAllowed,
		ErrorCodeActionConfirmationRequired, ErrorCodeActionApprovalRequired,
		ErrorCodeApprovalExpired:
		return 422
	case ErrorCodeClusterUnavailable, ErrorCodeBackendUnavailable,
		ErrorCodeToolUnavailable, ErrorCodeMaintenanceMode:
		return 503
	case ErrorCodeToolTimeout:
		return 504
	default:
		return 500
	}
}
