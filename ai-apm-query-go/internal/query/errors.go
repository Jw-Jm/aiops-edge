package query

import (
	"fmt"

	"github.com/observability-platform/ai-apm-query-go/internal/contract"
)

// ErrorCode 是统一查询层的错误码，复用 V9.2 §58 / internal/contract 的 token，
// 保证 reader 层与 orchestrator / frontend 的契约一致。
type ErrorCode string

const (
	// NoDataCode 查询成功但无数据（≠ backend 故障，≠ permission 拒绝）。
	NoDataCode ErrorCode = ErrorCode(contract.ErrorCodeNoData)
	// PermissionDeniedCode 权限不足/租户集群越权。
	PermissionDeniedCode ErrorCode = ErrorCode(contract.ErrorCodeTenantAccessDenied)
	// UnavailableCode 后端存储不可用（ClickHouse/VM/VLogs down）。
	UnavailableCode ErrorCode = ErrorCode(contract.ErrorCodeBackendUnavailable)
	// TimeoutCode 查询超时。
	TimeoutCode ErrorCode = ErrorCode(contract.ErrorCodeToolTimeout)
)

// QueryError 统一查询层错误。满足 error 接口，并携带稳定错误码与是否可重试。
type QueryError struct {
	Code      ErrorCode
	Message   string
	Retryable bool
}

func (e *QueryError) Error() string { return fmt.Sprintf("%s: %s", e.Code, e.Message) }

// HTTPStatus 返回该错误的规范 HTTP 状态码。
// 查询层语义（对齐 V9.2 §58）：
//   - NO_DATA        → 200（无数据是合法空结果，非错误）
//   - PERMISSION_DENIED → 403
//   - BACKEND_UNAVAILABLE → 503
//   - TOOL_TIMEOUT   → 504
// 不依赖 contract.HTTPStatusCode 的 default(500)，确保四语义不被模糊成 generic 500。
func (e *QueryError) HTTPStatus() int {
	switch e.Code {
	case NoDataCode:
		return 200
	case PermissionDeniedCode:
		return 403
	case UnavailableCode:
		return 503
	case TimeoutCode:
		return 504
	default:
		return 500
	}
}

// NoData 返回无数据错误（NO_DATA，HTTP 200 空列表语义）。
func NoData() error { return &QueryError{Code: NoDataCode, Message: "no data", Retryable: false} }

// PermissionDenied 返回权限不足错误（403）。
func PermissionDenied(msg string) error {
	return &QueryError{Code: PermissionDeniedCode, Message: msg, Retryable: false}
}

// Unavailable 返回后端不可用错误（503，可重试）。
func Unavailable(msg string) error {
	return &QueryError{Code: UnavailableCode, Message: msg, Retryable: true}
}

// Timeout 返回超时错误（504，可重试）。
func Timeout(msg string) error {
	return &QueryError{Code: TimeoutCode, Message: msg, Retryable: true}
}
