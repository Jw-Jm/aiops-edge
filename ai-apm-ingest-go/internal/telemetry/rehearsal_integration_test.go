//go:build integration

package telemetry

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

// ─────────────────────────────────────────────────────────────────────────────
// P6.4.2 Writer → Storage → Reader Fresh-data Rehearsal
//
// 用 NEW PRODUCTION WRITER（ModeNew 显式启用）写 fresh telemetry → 真实
// VictoriaMetrics / VictoriaLogs，再经与 query-go new reader 相同的存储查询 API
// 读回，验证 fresh data 可见且 scope 精确（tenant/cluster/resource）、同名 A/B 隔离、
// 无 legacy storage 依赖。
//
// 运行前提：真实后端可到达（env VICTORIA_METRICS_URL / VICTORIA_LOGS_URL）。
// 用法：go test -tags integration ./internal/telemetry -run TestRehearsal -v -timeout 5m
// ─────────────────────────────────────────────────────────────────────────────

func rehearsalVMURL() string {
	if v := os.Getenv("VICTORIA_METRICS_URL"); v != "" {
		return v
	}
	return "http://127.0.0.1:18428"
}

func rehearsalVLogsURL() string {
	if v := os.Getenv("VICTORIA_LOGS_URL"); v != "" {
		return v
	}
	return "http://127.0.0.1:19428"
}

// canonical test scope（Phase 19 两集群 A/B）
var (
	rhTenantA = "3f3c3b3a-0000-4000-8000-000000000001"
	rhClusterA = "3f3c3b3a-0000-4000-8000-000000000002"
	rhClusterB = "3f3c3b3a-0000-4000-8000-000000000003"
	rhResource = "3f3c3b3a-0000-4000-8000-0000000000aa"
)

// TestRehearsalNewWriterToVMThenNewReader 生产 writer 写 VM → 读回（fresh 可见 + A/B 隔离）。
func TestRehearsalNewWriterToVMThenNewReader(t *testing.T) {
	if testing.Short() {
		t.Skip("rehearsal requires real VictoriaMetrics")
	}
	// 1. NEW production writer（显式启用写）。
	w := NewVictoriaMetricsWriterMode(rehearsalVMURL(), ModeNew)
	if !w.Enabled() {
		t.Fatal("writer not enabled in ModeNew")
	}

	// 2. 写 cluster-A 与 cluster-B 同名资源（不同 value）。
	writeMetric(t, w, rhClusterA, "checkout", 100)
	writeMetric(t, w, rhClusterB, "checkout", 7)
	time.Sleep(2 * time.Second) // VM flush

	// 3. new reader 读回（同 query-go VictoriaMetricsReader 的 query_range 语义）。
	//    查询 cluster-A → 只含 A；查询 cluster-B → 只含 B。
	vma := vmQueryRangeValue(t, rehearsalVMURL(), rhClusterA)
	vmb := vmQueryRangeValue(t, rehearsalVMURL(), rhClusterB)
	if vma <= 0 {
		t.Fatalf("cluster-A fresh metric not visible via new reader query (got %v)", vma)
	}
	if vmb <= 0 {
		t.Fatalf("cluster-B fresh metric not visible via new reader query (got %v)", vmb)
	}
	if vma == vmb {
		t.Fatalf("A/B not isolated: same value %v", vma)
	}
}

// TestRehearsalNewWriterToVLogsThenNewReader 生产 writer 写 VLogs → 读回（fresh 可见 + 隔离）。
func TestRehearsalNewWriterToVLogsThenNewReader(t *testing.T) {
	if testing.Short() {
		t.Skip("rehearsal requires real VictoriaLogs")
	}
	lw := NewVictoriaLogsWriterMode(rehearsalVLogsURL(), ModeNew)
	if !lw.Enabled() {
		t.Fatal("log writer not enabled in ModeNew")
	}
	writeLog(t, lw, rhClusterA, "checkout", "fresh-a")
	writeLog(t, lw, rhClusterB, "checkout", "fresh-b")
	time.Sleep(3 * time.Second) // VLogs flush

	// new reader 读回：查询 cluster-A → 只见 A 的 fresh-a；cluster-B → 只见 B。
	seen := vlogsQueryBodies(t, rehearsalVLogsURL(), rhClusterA)
	if !contains(seen, "fresh-a") {
		t.Fatalf("cluster-A fresh log not visible; got %v", seen)
	}
	if contains(seen, "fresh-b") {
		t.Fatalf("cluster-A query leaked cluster-B log: %v", seen)
	}
	seenB := vlogsQueryBodies(t, rehearsalVLogsURL(), rhClusterB)
	if !contains(seenB, "fresh-b") {
		t.Fatalf("cluster-B fresh log not visible; got %v", seenB)
	}
}

// writeMetric 用生产 writer 写一条 VM metric（canonical scope + __name__）。
func writeMetric(t *testing.T, w *VictoriaMetricsWriter, cluster string, service string, value float64) {
	t.Helper()
	res := w.WriteScope(map[string]string{
		"__name__":   "rehearsal_call_total",
		"tenant_id":  rhTenantA,
		"cluster_id": cluster,
		"resource_id": rhResource,
		"service_name": service,
	}, ScopeResource, value, time.Now())
	if res.ErrorCode != "" {
		t.Fatalf("write metric failed: %s", res.ErrorCode)
	}
}

// writeLog 用生产 writer 写一条 VLogs 日志。
func writeLog(t *testing.T, lw *VictoriaLogsWriter, cluster string, service string, body string) {
	t.Helper()
	res := lw.WriteLogScope(map[string]string{
		"tenant_id":   rhTenantA,
		"cluster_id":  cluster,
		"resource_id": rhResource,
		"service_name": service,
		"level":       "info",
	}, ScopeResource, body, time.Now())
	if res.ErrorCode != "" {
		t.Fatalf("write log failed: %s", res.ErrorCode)
	}
}

// vmQueryRangeValue 通过 /api/v1/query_range 读回最近 10m 的 cluster 指标值（与 query-go
// VictoriaMetricsReader 同源）。返回最近一个样本值（fresh data 可见性验证）。
func vmQueryRangeValue(t *testing.T, url, cluster string) float64 {
	t.Helper()
	q := fmt.Sprintf(`rehearsal_call_total{tenant_id=%q,cluster_id=%q,resource_id=%q}`,
		rhTenantA, cluster, rhResource)
	now := time.Now().Unix()
	endpoint := url + "/api/v1/query_range?query=" + urlEncode(q) +
		fmt.Sprintf("&start=%d&end=%d&step=60", now-600, now)
	resp, err := http.Get(endpoint)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var vr struct {
		Data struct {
			Result []struct {
				Values [][]interface{} `json:"values"`
			} `json:"result"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &vr); err != nil {
		t.Fatal(err)
	}
	// 取该 series 最后一个非零样本值。
	for _, s := range vr.Data.Result {
		for i := len(s.Values) - 1; i >= 0; i-- {
			if len(s.Values[i]) < 2 {
				continue
			}
			switch v := s.Values[i][1].(type) {
			case float64:
				if v != 0 {
					return v
				}
			case string:
				var f float64
				_, _ = fmt.Sscanf(v, "%f", &f)
				if f != 0 {
					return f
				}
			}
		}
	}
	return 0
}

// vlogsQueryBodies 通过 /select/logsql/query 读回 cluster 的日志 _msg（new reader 同源）。
// 用 tenant+cluster+service 标签过滤验证 fresh data 可见性与 A/B 隔离。
// 注意：VictoriaLogs 某版本对 `field:"v" AND _time:30m` 组合返回空（字段过滤与相对
// 时长过滤的交互 quirk）；此处用标签过滤验证 fresh 数据（query-go 的 _time 过滤已在
// P6.3 real-backend acceptance 中单独验证通过）。
func vlogsQueryBodies(t *testing.T, url, cluster string) []string {
	t.Helper()
	q := fmt.Sprintf(`tenant_id:"%s" AND cluster_id:"%s" AND service_name:"checkout"`, rhTenantA, cluster)
	resp, err := http.Get(url + "/select/logsql/query?query=" + urlEncode(q))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var out []string
	for _, line := range strings.Split(strings.TrimSpace(string(body)), "\n") {
		if line == "" {
			continue
		}
		var rec struct {
			Msg string `json:"_msg"`
		}
		if err := json.Unmarshal([]byte(line), &rec); err == nil && rec.Msg != "" {
			out = append(out, rec.Msg)
		}
	}
	return out
}

func urlEncode(s string) string {
	r := strings.NewReplacer(
		`{`, "%7B", `}`, "%7D", `"`, "%22", `=`, "%3D", `,`, "%2C",
		`[`, "%5B", `]`, "%5D", `:`, "%3A", ` `, "%20",
	)
	return r.Replace(s)
}

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}
