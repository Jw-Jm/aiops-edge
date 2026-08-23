package query

import (
	"errors"
	"testing"
)

// Gate 6 关键语义：no_data / permission_denied / unavailable / timeout 必须可区分，
// 不得被统一模糊成 generic 500。
func TestQueryErrorDistinguishesFourSemantics(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantCode   ErrorCode
		wantStatus int
	}{
		{"no_data", NoData(), NoDataCode, 200},
		{"permission_denied", PermissionDenied("tenant denied"), PermissionDeniedCode, 403},
		{"unavailable", Unavailable("clickhouse down"), UnavailableCode, 503},
		{"timeout", Timeout("query deadline"), TimeoutCode, 504},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var qe *QueryError
			if !errors.As(tt.err, &qe) {
				t.Fatalf("expected *QueryError, got %T", tt.err)
			}
			if qe.Code != tt.wantCode {
				t.Errorf("code = %q, want %q", qe.Code, tt.wantCode)
			}
			if qe.HTTPStatus() != tt.wantStatus {
				t.Errorf("status = %d, want %d", qe.HTTPStatus(), tt.wantStatus)
			}
		})
	}
}

func TestQueryErrorNotGeneric500(t *testing.T) {
	// no_data 绝不应映射为 500（否则 frontend 无法区分"无数据"与"后端故障"）。
	if st := NoData().(*QueryError).HTTPStatus(); st == 500 {
		t.Fatal("no_data must not map to generic 500")
	}
	// unavailable 应映射 503（backend down），而非 generic 500。
	if st := Unavailable("x").(*QueryError).HTTPStatus(); st != 503 {
		t.Fatalf("unavailable should map to 503, got %d", st)
	}
	// timeout 应映射 504，而非 generic 500。
	if st := Timeout("x").(*QueryError).HTTPStatus(); st != 504 {
		t.Fatalf("timeout should map to 504, got %d", st)
	}
}

func TestQueryErrorIsRetryable(t *testing.T) {
	if !Unavailable("x").(*QueryError).Retryable {
		t.Error("unavailable should be retryable")
	}
	if !Timeout("x").(*QueryError).Retryable {
		t.Error("timeout should be retryable")
	}
	if NoData().(*QueryError).Retryable {
		t.Error("no_data should not be retryable")
	}
	if PermissionDenied("x").(*QueryError).Retryable {
		t.Error("permission_denied should not be retryable")
	}
}
