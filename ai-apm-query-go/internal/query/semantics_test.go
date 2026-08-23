package query

import (
	"errors"
	"testing"
)

// 鉴权优先于 no_data 的错误语义矩阵（V9.2 Gate 6：no_data != permission_denied !=
// unavailable != timeout）。
func TestResolveReadResultSemanticsMatrix(t *testing.T) {
	tests := []struct {
		name       string
		authorized bool
		queryErr   error
		wantNil    bool
		wantIs     ErrorCode // 非空则断言 err 类型为 ReadSemanticsError(code)
	}{
		// authorized + empty 数据 => no_data（非 500、非 permission）
		{"authorized empty data", true, NoData(), false, NoDataCode},
		// unauthorized + empty 数据 => permission_denied（掩盖 no_data，防资源存在性侧信道）
		{"unauthorized empty data", false, NoData(), false, PermissionDeniedCode},
		// unauthorized + 后端故障 => 仍 permission_denied（鉴权优先于一切）
		{"unauthorized backend down", false, Unavailable("ch down"), false, PermissionDeniedCode},
		// authorized + backend down => unavailable
		{"authorized backend down", true, Unavailable("ch down"), false, UnavailableCode},
		// authorized + context deadline => timeout
		{"authorized timeout", true, Timeout("deadline"), false, TimeoutCode},
		// authorized + generic backend error => internal error（不回退 no_data）
		{"authorized generic error", true, errors.New("some sql error"), false, ""},
		// authorized + nil => 成功
		{"authorized success", true, nil, true, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ResolveReadResult(tt.authorized, tt.queryErr)
			if tt.wantNil {
				if got != nil {
					t.Fatalf("expected nil, got %v", got)
				}
				return
			}
			if got == nil {
				t.Fatal("expected non-nil error")
			}
			if tt.wantIs != "" {
				var se ReadSemanticsError
				if !errors.As(got, &se) {
					t.Fatalf("expected ReadSemanticsError, got %T: %v", got, got)
				}
				if ErrorCode(se) != tt.wantIs {
					t.Errorf("code = %q, want %q", se, tt.wantIs)
				}
			}
		})
	}
}

// 关键安全断言：unauthorized + 空数据必须返回 permission_denied，绝不能 no_data。
func TestUnauthorizedNeverFoldsToNoData(t *testing.T) {
	got := ResolveReadResult(false, NoData())
	var se ReadSemanticsError
	if !errors.As(got, &se) {
		t.Fatalf("expected ReadSemanticsError, got %T", got)
	}
	if ErrorCode(se) != PermissionDeniedCode {
		t.Fatalf("unauthorized must yield permission_denied, got %q", se)
	}
}

// generic backend error 不得折叠成 no_data（否则 frontend 误判"无数据"而掩盖故障）。
func TestGenericErrorNeverFoldsToNoData(t *testing.T) {
	got := ResolveReadResult(true, errors.New("broken pipe"))
	var se ReadSemanticsError
	if errors.As(got, &se) {
		t.Fatalf("generic error must not be a ReadSemanticsError, got %q", se)
	}
	if got.Error() != "internal query error: broken pipe" {
		t.Fatalf("generic error should be explicit internal error, got: %v", got)
	}
}
