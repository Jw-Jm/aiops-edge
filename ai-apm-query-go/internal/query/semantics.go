package query

import (
	"context"
	"errors"
)

// ReadSemanticsError 是查询错误分类后的统一错误码。
type ReadSemanticsError ErrorCode

func (e ReadSemanticsError) Error() string { return string(e) }

// ResolveReadResult 统一查询读取结果，编码"鉴权优先于 no_data"语义（V9.2 Gate 6：
// no_data != permission_denied）。
//
// 执行顺序：authorize → resolve canonical scope → query backend → no_data。
// 若调用方在查询前已判定 unauthorized，则无论底层是否命中空数据，都必须返回
// permission_denied，绝不掩盖权限（否则形成资源存在性侧信道）。
//
// 参数：
//   - authorized: 调用方在查询前完成的鉴权判定（false 立即返回 permission_denied）
//   - err: 底层查询返回的错误（nil=成功，no_data/unavailable/timeout/generic）
func ResolveReadResult(authorized bool, err error) error {
	if !authorized {
		// 鉴权优先：unauthorized 恒返回 permission_denied（ReadSemanticsError），
		// 即使底层是 no_data/unavailable 也绝不折叠。
		return ReadSemanticsError(PermissionDeniedCode)
	}
	if err == nil {
		return nil
	}
	var qe *QueryError
	if errors.As(err, &qe) {
		switch qe.Code {
		case NoDataCode:
			return ReadSemanticsError(NoDataCode)
		case PermissionDeniedCode:
			return ReadSemanticsError(PermissionDeniedCode)
		case UnavailableCode:
			return ReadSemanticsError(UnavailableCode)
		case TimeoutCode:
			return ReadSemanticsError(TimeoutCode)
		default:
			return errors.New("internal query error: " + qe.Message)
		}
	}
	// 通用底层错误（如 db/sql 错误）：显式 internal error，不回退 no_data。
	return errors.New("internal query error: " + err.Error())
}

// IsContextDeadline 判断错误是否为 context deadline exceeded（供分类）。
func IsContextDeadline(err error) bool { return errors.Is(err, context.DeadlineExceeded) }
