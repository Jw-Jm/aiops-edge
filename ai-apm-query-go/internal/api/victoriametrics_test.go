package api

import (
	"net/http/httptest"
	"testing"
)

// buildQueryRangeURL 应正确拼接 VM query_range 端点并 URL 编码 PromQL。
func TestBuildQueryRangeURL(t *testing.T) {
	h := &Handler{vmURL: "http://vm:8428"}
	got := h.buildQueryRangeURL("sum(rate(x[5m]))", "1710000000", "1710000360", "60")
	// url.Values.Encode() 按 key 字典序排序(end<query<start<step)
	want := "http://vm:8428/api/v1/query_range?end=1710000360&query=sum%28rate%28x%5B5m%5D%29%29&start=1710000000&step=60"
	if got != want {
		t.Fatalf("buildQueryRangeURL = %q, want %q", got, want)
	}
}

// 缺少 query 参数应返回 400。
func TestQueryRangeMissingQuery(t *testing.T) {
	h := &Handler{}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/metrics/query_range?start=1&end=2&step=60", nil)
	h.QueryRange(rec, req)
	if rec.Code != 400 {
		t.Fatalf("code = %d, want 400 (missing query)", rec.Code)
	}
}
