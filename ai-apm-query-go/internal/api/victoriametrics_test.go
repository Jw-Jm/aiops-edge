package api

import (
	"encoding/json"
	"net/http"
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

func TestVMRangeQueryParsesSeries(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/query_range" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"status": "success",
			"data": map[string]interface{}{
				"resultType": "matrix",
				"result": []map[string]interface{}{
					{"metric": map[string]interface{}{}, "values": [][2]interface{}{{1710000000, "1"}, {1710000060, "2"}, {1710000120, "3"}}},
				},
			},
		})
	}))
	defer srv.Close()
	h := &Handler{vmURL: srv.URL, client: &http.Client{}}
	series, err := h.vmRangeQuery("sum(rate(x[5m]))", 1710000000, 1710000180, 60)
	if err != nil {
		t.Fatalf("vmRangeQuery error: %v", err)
	}
	if len(series) != 3 || series[0] != 1 || series[2] != 3 {
		t.Fatalf("series = %v, want [1 2 3]", series)
	}
}

func TestVMRangeQueryEmptyResult(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"status": "success",
			"data":   map[string]interface{}{"resultType": "matrix", "result": []interface{}{}},
		})
	}))
	defer srv.Close()
	h := &Handler{vmURL: srv.URL, client: &http.Client{}}
	series, err := h.vmRangeQuery("x", 1, 100, 10)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(series) != 0 {
		t.Fatalf("expected empty, got %v", series)
	}
}

func TestVMRangeQueryErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// VM 错误响应通常带 JSON body
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"status":"error","error":"bad query"}`))
	}))
	defer srv.Close()
	h := &Handler{vmURL: srv.URL, client: &http.Client{}}
	_, err := h.vmRangeQuery("x", 1, 100, 10)
	if err == nil {
		t.Fatalf("expected error on 500 (even with JSON body)")
	}
	// 确认错误信息包含状态码
	if !contains(err.Error(), "500") {
		t.Fatalf("error should mention status 500, got: %v", err)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
