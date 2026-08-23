package telemetry

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// canonicalTestTenant / canonicalTestCluster 满足 telemetrylabels canonical UUID 约束。
const (
	canonicalTestTenant  = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	canonicalTestCluster = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
)

// TestRuntimeFromEnv_ModeNew_WriteLogAndRED 验证 TELEMETRY_WRITER_MODE=new 时，
// WriteLog 真实写入 VLogs、WriteRED 真实写入 VM（HTTP 请求到达后端）。
func TestRuntimeFromEnv_ModeNew_WriteLogAndRED(t *testing.T) {
	var vlogReqs, vmReqs atomic.Int32
	vlogsSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/insert/jsonline" {
			t.Errorf("unexpected vlogs path: %s", r.URL.Path)
		}
		vlogReqs.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer vlogsSrv.Close()

	vmSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/import/prometheus" {
			t.Errorf("unexpected vm path: %s", r.URL.Path)
		}
		vmReqs.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer vmSrv.Close()

	t.Setenv("TELEMETRY_WRITER_MODE", "new")
	t.Setenv("VICTORIA_METRICS_URL", vmSrv.URL)
	t.Setenv("VICTORIA_LOGS_URL", vlogsSrv.URL)

	rt, err := NewRuntimeFromEnv()
	if err != nil {
		t.Fatalf("NewRuntimeFromEnv: %v", err)
	}
	if !rt.Enabled() {
		t.Fatalf("expected Enabled=true for ModeNew")
	}

	ts := time.Now().UTC()
	// logs → VLogs
	res := rt.WriteLog(canonicalTestTenant, canonicalTestCluster, "checkout", "INFO", "hello world", ts)
	if res.Status != "ok" {
		t.Fatalf("WriteLog expected ok, got %+v", res)
	}
	// RED → VM（call_total）
	res = rt.WriteRED(canonicalTestTenant, canonicalTestCluster, "checkout", 1.0, ts)
	if res.Status != "ok" {
		t.Fatalf("WriteRED expected ok, got %+v", res)
	}

	if vlogReqs.Load() != 1 {
		t.Fatalf("expected 1 vlogs write, got %d", vlogReqs.Load())
	}
	if vmReqs.Load() != 1 {
		t.Fatalf("expected 1 vm write, got %d", vmReqs.Load())
	}
}

// TestRuntime_Disabled_NoTransport 验证 disabled/legacy 模式不发出任何 HTTP 请求。
func TestRuntime_Disabled_NoTransport(t *testing.T) {
	var vlogReqs, vmReqs atomic.Int32
	vlogsSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		vlogReqs.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer vlogsSrv.Close()
	vmSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		vmReqs.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer vmSrv.Close()

	for _, mode := range []string{"disabled", "legacy"} {
		vlogReqs.Store(0)
		vmReqs.Store(0)
		t.Setenv("TELEMETRY_WRITER_MODE", mode)
		t.Setenv("VICTORIA_METRICS_URL", vmSrv.URL)
		t.Setenv("VICTORIA_LOGS_URL", vlogsSrv.URL)
		rt, err := NewRuntimeFromEnv()
		if err != nil {
			t.Fatalf("mode %s: NewRuntimeFromEnv: %v", mode, err)
		}
		if rt.Enabled() {
			t.Fatalf("mode %s: expected Enabled=false", mode)
		}
		ts := time.Now().UTC()
		_ = rt.WriteLog(canonicalTestTenant, canonicalTestCluster, "svc", "INFO", "body", ts)
		_ = rt.WriteRED(canonicalTestTenant, canonicalTestCluster, "svc", 1.0, ts)
		if vlogReqs.Load() != 0 {
			t.Fatalf("mode %s: disabled must not send vlogs, got %d", mode, vlogReqs.Load())
		}
		if vmReqs.Load() != 0 {
			t.Fatalf("mode %s: disabled must not send vm, got %d", mode, vmReqs.Load())
		}
	}
}

// TestRuntimeFromEnv_InvalidMode_FailClosed 验证非法 mode 导致启动失败（fail-closed）。
func TestRuntimeFromEnv_InvalidMode_FailClosed(t *testing.T) {
	t.Setenv("TELEMETRY_WRITER_MODE", "bogus")
	t.Setenv("VICTORIA_METRICS_URL", "http://vm:8428")
	t.Setenv("VICTORIA_LOGS_URL", "http://vlogs:9428")
	if _, err := NewRuntimeFromEnv(); err == nil {
		t.Fatalf("expected error for invalid mode, got nil")
	}
}

// TestRuntimeFromEnv_ModeNew_MissingURL_FailClosed 验证 ModeNew 但 backend URL 缺失时
// 启动失败（fail-closed），避免 new 链配置不完整却静默不写。
func TestRuntimeFromEnv_ModeNew_MissingURL_FailClosed(t *testing.T) {
	t.Setenv("TELEMETRY_WRITER_MODE", "new")
	t.Setenv("VICTORIA_METRICS_URL", "")
	t.Setenv("VICTORIA_LOGS_URL", "http://vlogs:9428")
	if _, err := NewRuntimeFromEnv(); err == nil {
		t.Fatalf("expected error for ModeNew with missing VM URL, got nil")
	}
}

// TestRuntime_BackendDown_NoFallback 验证后端 down 时返回可观测 error，不假装成功、不 fallback。
func TestRuntime_BackendDown_NoFallback(t *testing.T) {
	// 两个 server 启动后立即关闭（down）。
	downSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	downURL := downSrv.URL
	downSrv.Close()

	rt := NewRuntime(ModeNew, downURL, downURL)
	ts := time.Now().UTC()
	if res := rt.WriteLog(canonicalTestTenant, canonicalTestCluster, "svc", "INFO", "body", ts); res.Status != "error" {
		t.Fatalf("expected error when backend down, got %+v", res)
	}
	if res := rt.WriteRED(canonicalTestTenant, canonicalTestCluster, "svc", 1.0, ts); res.Status != "error" {
		t.Fatalf("expected error when backend down, got %+v", res)
	}
}

// TestRuntime_WriteLog_InvalidScope 验证非 canonical tenant 被拒绝（不写入），保持三字段 contract。
func TestRuntime_WriteLog_InvalidScope(t *testing.T) {
	var reqs atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqs.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	rt := NewRuntime(ModeNew, srv.URL, srv.URL)
	ts := time.Now().UTC()
	res := rt.WriteLog("not-a-uuid", "default", "svc", "INFO", "body", ts)
	if res.Status != "error" || res.ErrorCode != "INVALID_SCOPE" {
		t.Fatalf("expected INVALID_SCOPE, got %+v", res)
	}
	if reqs.Load() != 0 {
		t.Fatalf("invalid scope must not reach backend, got %d requests", reqs.Load())
	}
}

// TestRuntime_WriteLog_SetsCanonicalFields 验证写入 VLogs 的 JSON 行含 new reader 契约字段
// （tenant_id / cluster_id / service_name / level / _msg），确保 ingest new writer 与
// query-go VLogsReader（logs.go 用 tenant_id/cluster_id/service_name，vlogsRecord 解析 level）对齐。
func TestRuntime_WriteLog_SetsCanonicalFields(t *testing.T) {
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(buf)
		gotBody = string(buf)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	rt := NewRuntime(ModeNew, srv.URL, srv.URL)
	ts := time.Now().UTC()
	res := rt.WriteLog(canonicalTestTenant, canonicalTestCluster, "checkout", "ERROR", "boom", ts)
	if res.Status != "ok" {
		t.Fatalf("WriteLog: %+v", res)
	}
	var m map[string]string
	if err := json.Unmarshal([]byte(gotBody), &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if m["tenant_id"] != canonicalTestTenant {
		t.Fatalf("tenant_id=%q", m["tenant_id"])
	}
	if m["cluster_id"] != canonicalTestCluster {
		t.Fatalf("cluster_id=%q", m["cluster_id"])
	}
	if m["service_name"] != "checkout" {
		t.Fatalf("service_name=%q, want checkout", m["service_name"])
	}
	if m["level"] != "ERROR" {
		t.Fatalf("level=%q, want ERROR", m["level"])
	}
	if m["_msg"] != "boom" {
		t.Fatalf("_msg=%q, want boom", m["_msg"])
	}
}

// TestRuntime_WriteRED_UsesCallTotal 验证 WriteRED 写 VM 的指标名为 call_total（对齐 new reader）。
func TestRuntime_WriteRED_UsesCallTotal(t *testing.T) {
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(buf)
		gotBody = string(buf)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	rt := NewRuntime(ModeNew, srv.URL, srv.URL)
	ts := time.Now().UTC()
	res := rt.WriteRED(canonicalTestTenant, canonicalTestCluster, "checkout", 5.0, ts)
	if res.Status != "ok" {
		t.Fatalf("WriteRED: %+v", res)
	}
	if !strings.HasPrefix(gotBody, "call_total{") {
		t.Fatalf("expected call_total metric line, got: %s", gotBody)
	}
	if !strings.Contains(gotBody, `service_name="checkout"`) {
		t.Fatalf("expected service_name label, got: %s", gotBody)
	}
}
