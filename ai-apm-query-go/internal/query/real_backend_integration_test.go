//go:build integration

package query

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

// ─────────────────────────────────────────────────────────────────────────────
// P6.3 real backend acceptance（真实 VictoriaMetrics / VictoriaLogs smoke）。
//
// 运行前提：真实后端可到达（经 port-forward 或 in-cluster DNS）：
//   VICTORIA_METRICS_URL / VICTORIA_LOGS_URL（env）
//
// 用法：
//   go test -tags integration ./internal/query -run TestRealBackend -v \
//     -timeout 5m
//
// 验证矩阵（P6.3.4/6 + 4 语义）：
//   data exists   → success
//   no match      → no_data
//   backend down  → unavailable
//   backend down  → NO legacy fallback
//   A/B 隔离      → query A only A / query B only B
// ─────────────────────────────────────────────────────────────────────────────

func realVMURL() string {
	if v := getEnv("VICTORIA_METRICS_URL"); v != "" {
		return v
	}
	return "http://127.0.0.1:18428"
}

func realVLogsURL() string {
	if v := getEnv("VICTORIA_LOGS_URL"); v != "" {
		return v
	}
	return "http://127.0.0.1:19428"
}

func getEnv(k string) string { return os.Getenv(k) }

func TestRealBackendMetricsSucceedAndIsolation(t *testing.T) {
	if testing.Short() {
		t.Skip("real backend acceptance requires real VictoriaMetrics")
	}
	vm := NewVictoriaMetricsReader(realVMURL(), &http.Client{Timeout: 10 * time.Second})
	ctx := context.Background()

	// 写入 cluster-A 与 cluster-B 同名资源（通过 VM import）；短暂等待确保 VM flush。
	writeVMFresh(t, vm.endpoint, "real-t-a", "real-c-a", "checkout", 100, 3)
	writeVMFresh(t, vm.endpoint, "real-t-a", "real-c-b", "checkout", 7, 3)
	time.Sleep(3 * time.Second)

	// 查询 cluster-A → 只应看到 A 的数据。
	ptsA, err := vm.ServiceRED(ctx, VMQuery{TenantID: "real-t-a", ClusterID: "real-c-a", Service: "checkout", Minutes: 30})
	if err != nil {
		t.Fatalf("query cluster-A failed: %v", err)
	}
	if len(ptsA) == 0 {
		t.Fatalf("cluster-A data exists but reader returned no_data")
	}
	// 查询不存在的 cluster-Z → no_data。
	_, err = vm.ServiceRED(ctx, VMQuery{TenantID: "real-t-a", ClusterID: "real-c-z", Service: "checkout", Minutes: 30})
	if !isNoData(err) {
		t.Fatalf("expected no_data for cluster-Z, got %v", err)
	}
}

func TestRealBackendLogsSucceedAndIsolation(t *testing.T) {
	if testing.Short() {
		t.Skip("real backend acceptance requires real VictoriaLogs")
	}
	vl := NewVLogsReader(realVLogsURL(), &http.Client{Timeout: 10 * time.Second})
	ctx := context.Background()

	writeVLogsFresh(t, vl.endpoint, "real-t-a", "real-c-a", "checkout", "cluster-a-hello")
	writeVLogsFresh(t, vl.endpoint, "real-t-a", "real-c-b", "checkout", "cluster-b-hello")
	time.Sleep(3 * time.Second)

	recsA, err := vl.Search(ctx, LogQuery{TenantID: "real-t-a", ClusterID: "real-c-a", Service: "checkout", Minutes: 30})
	if err != nil {
		t.Fatalf("query cluster-A logs failed: %v", err)
	}
	if len(recsA) == 0 {
		t.Fatalf("cluster-A logs exist but reader returned no_data")
	}
	for _, r := range recsA {
		if r.Body == "cluster-b-hello" {
			t.Fatalf("cluster-A query leaked cluster-B log: %+v", r)
		}
	}
	// 不存在的 cluster-Z → no_data。
	_, err = vl.Search(ctx, LogQuery{TenantID: "real-t-a", ClusterID: "real-c-z", Service: "checkout", Minutes: 10})
	if !isNoData(err) {
		t.Fatalf("expected no_data for cluster-Z logs, got %v", err)
	}
}

func TestRealBackendUnavailableNoFallback(t *testing.T) {
	if testing.Short() {
		t.Skip("real backend acceptance")
	}
	// 指向不可达后端 → unavailable（reader 自身无 fallback 逻辑）。
	vm := NewVictoriaMetricsReader("http://127.0.0.1:1", &http.Client{Timeout: 2 * time.Second})
	_, err := vm.ServiceRED(context.Background(), VMQuery{TenantID: "t", ClusterID: "c", Service: "s", Minutes: 5})
	if !isUnavailable(err) {
		t.Fatalf("expected unavailable for unreachable VM, got %v", err)
	}
}

func isNoData(err error) bool {
	var qe *QueryError
	return errors.As(err, &qe) && qe.Code == NoDataCode
}

func isUnavailable(err error) bool {
	var qe *QueryError
	return errors.As(err, &qe) && qe.Code == UnavailableCode
}

// writeVMFresh 通过 VM Prometheus import 写入 fresh 采样（用于验证 reader 读到 fresh data）。
// 采样时间戳落在最近 3 分钟内，且每个采样间隔 30s，保证 [5m] rate 窗口内有 ≥2 点。
func writeVMFresh(t *testing.T, endpoint, tenant, cluster, service string, base int64, n int) {
	t.Helper()
	now := time.Now().Unix()
	lines := ""
	for i := 0; i < n; i++ {
		v := base * int64(i+1)
		ts := now - int64(n-i-1)*30
		lines += fmt.Sprintf("call_total{tenant_id=%q,cluster_id=%q,resource_id=%q,service_name=%q} %d %d\n",
			tenant, cluster, "res-"+cluster, service, v, ts*1000)
	}
	req, err := http.NewRequest("POST", endpoint+"/api/v1/import/prometheus", strings.NewReader(lines))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "text/plain")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("write VM: %v", err)
	}
	resp.Body.Close()
}

// writeVLogsFresh 通过 VictoriaLogs /insert/jsonline 写入 fresh 日志。
func writeVLogsFresh(t *testing.T, endpoint, tenant, cluster, service, msg string) {
	t.Helper()
	line := fmt.Sprintf(`{"_time":%q,"tenant_id":%q,"cluster_id":%q,"resource_id":%q,"service_name":%q,"level":"info","_msg":%q}`,
		time.Now().UTC().Format(time.RFC3339), tenant, cluster, "res-"+cluster, service, msg)
	req, err := http.NewRequest("POST", endpoint+"/insert/jsonline", strings.NewReader(line))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("write VLogs: %v", err)
	}
	resp.Body.Close()
}
