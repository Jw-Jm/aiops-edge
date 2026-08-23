package api

import "testing"

// TestNormalizeVLogsEndpoint 验证 VICTORIA_LOGS_URL 的 endpoint 规范化：
// reader 需要 base URL（会追加 /select/logsql/query），但同一个 env 被
// data_sync.go StartLogShipper 复用作写入 URL（带 /insert/jsonline）。
// 必须把尾部 /insert/jsonline 去掉，避免 reader 查询路径变成
// "/insert/jsonline/select/logsql/query"（400）。
func TestNormalizeVLogsEndpoint(t *testing.T) {
	cases := []struct {
		raw  string
		want string
	}{
		{"http://victoria-logs:9428/insert/jsonline", "http://victoria-logs:9428"},
		{"http://victoria-logs:9428", "http://victoria-logs:9428"},
		{"http://victoria-logs:9428/insert/jsonline/", "http://victoria-logs:9428"},
		{"", ""},
	}
	for _, c := range cases {
		if got := normalizeVLogsEndpoint(c.raw); got != c.want {
			t.Fatalf("normalizeVLogsEndpoint(%q)=%q, want %q", c.raw, got, c.want)
		}
	}
}

// TestNewVLogsReaderFromEnv_DropsInsertSuffix 验证从 env 构造 reader 时
// endpoint 正确去掉 /insert/jsonline 后缀（新 reader 查询路径正确）。
func TestNewVLogsReaderFromEnv_DropsInsertSuffix(t *testing.T) {
	t.Setenv("VICTORIA_LOGS_URL", "http://victoria-logs:9428/insert/jsonline")
	r := newVLogsReaderFromEnv()
	if r == nil {
		t.Fatal("expected non-nil reader")
	}
	if got := r.Endpoint(); got != "http://victoria-logs:9428" {
		t.Fatalf("reader endpoint=%q, want http://victoria-logs:9428", got)
	}
}
