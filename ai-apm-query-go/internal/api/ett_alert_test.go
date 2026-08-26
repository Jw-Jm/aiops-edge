package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// 测试辅助：快照/恢复 alertEvents（避免测试间互相污染包级状态）。
func snapshotAlertEvents() []AlertEvent {
	alertEventsMu.RLock()
	defer alertEventsMu.RUnlock()
	return append([]AlertEvent{}, alertEvents...)
}

func restoreAlertEvents(saved []AlertEvent) {
	alertEventsMu.Lock()
	alertEvents = saved
	alertEventsMu.Unlock()
}

// countCapacityETTEvents 统计 rule_id=capacity-ett-imminent 的事件数（按状态）。
func countCapacityETTEvents(t *testing.T) (firing, resolved int) {
	t.Helper()
	alertEventsMu.RLock()
	defer alertEventsMu.RUnlock()
	for _, e := range alertEvents {
		if e.RuleID == ettAlertRuleID {
			switch e.Status {
			case "firing":
				firing++
			case "resolved":
				resolved++
			}
		}
	}
	return firing, resolved
}

// ettSeries 构造 step=300s、n 个点的线性序列（与 ETT 检查参数一致）。
func ettSeries(base, slope float64, n int) [][2]interface{} {
	vals := make([][2]interface{}, n)
	ts := int64(1710000000)
	for i := 0; i < n; i++ {
		vals[i] = [2]interface{}{ts + int64(i)*300, fmt.Sprintf("%.4f", base+slope*float64(i))}
	}
	return vals
}

// newETTVMServer 构造 mock VM：
//   - /api/v1/query → Categraf agent_hostname ["node-1"]
//   - /api/v1/query_range → 按 PromQL 内容区分：
//     cpu（由 *cpuSeries 动态控制，便于测试触发→恢复）;
//     memory（缓慢上升 10+0.06i，ETT≈73.3h >72h → 不触发）；
//     其余（disk/network，平缓 30 → 不触发）。
func newETTVMServer(t *testing.T, cpuSeries *[][2]interface{}) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/query":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"status": "success",
				"data": map[string]interface{}{
					"resultType": "vector",
					"result": []map[string]interface{}{
						{"metric": map[string]interface{}{"agent_hostname": "node-1"}, "value": []interface{}{1710000000, "1"}},
					},
				},
			})
		case "/api/v1/query_range":
			q := r.URL.Query().Get("query")
			var vals [][2]interface{}
			switch {
			case strings.Contains(q, "cpu_usage_active"):
				vals = *cpuSeries
			case strings.Contains(q, "mem_used_percent"):
				vals = ettSeries(10, 0.06, 288) // ETT≈73.3h > 72h
			default:
				vals = ettSeries(30, 0, 288) // 平缓无增长 → 不触发
			}
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"status": "success",
				"data": map[string]interface{}{
					"resultType": "matrix",
					"result": []map[string]interface{}{
						{"metric": map[string]interface{}{}, "values": vals},
					},
				},
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// ETT≤72h → 触发 warning firing 事件（rule_id=capacity-ett-imminent，消息含 metric/instance）。
func TestCapacityETTAlertFires(t *testing.T) {
	orig := snapshotAlertEvents()
	defer restoreAlertEvents(orig)

	// cpu 快速上升：10+0.15i，当前≈53，斜率 0.15 → ETT=(80-53)/0.15≈180 步
	// = 180×300s = 54000s ≈ 15h ≤ 72h → 触发。
	cpuSeries := ettSeries(10, 0.15, 288)
	srv := newETTVMServer(t, &cpuSeries)
	h := &Handler{vmURL: srv.URL, client: srv.Client()}
	h.checkCapacityETTAlerts()

	firing, resolved := countCapacityETTEvents(t)
	if firing != 1 || resolved != 0 {
		t.Fatalf("firing=%d resolved=%d, want firing=1 resolved=0", firing, resolved)
	}

	alertEventsMu.RLock()
	defer alertEventsMu.RUnlock()
	var ev *AlertEvent
	for i := range alertEvents {
		if alertEvents[i].RuleID == ettAlertRuleID {
			ev = &alertEvents[i]
			break
		}
	}
	if ev == nil {
		t.Fatal("no capacity ETT event found")
	}
	if ev.Service != ettAlertService || ev.Severity != "warning" {
		t.Fatalf("service=%s severity=%s, want %s/warning", ev.Service, ev.Severity, ettAlertService)
	}
	if ev.Object != "node-1" {
		t.Fatalf("object=%s, want node-1", ev.Object)
	}
	if !strings.Contains(ev.Message, "cpu") || !strings.Contains(ev.Message, "node-1") {
		t.Fatalf("message should carry metric/instance: %s", ev.Message)
	}
	if ev.Threshold != float64(72*3600) {
		t.Fatalf("threshold=%v, want %v (72h in seconds)", ev.Threshold, float64(72*3600))
	}
}

// ETT>72h → 不触发（cpu 改缓慢上升 10+0.06i，ETT≈73.3h；其余指标同样不命中）。
func TestCapacityETTAlertNoFireWhenETTAboveLimit(t *testing.T) {
	orig := snapshotAlertEvents()
	defer restoreAlertEvents(orig)

	cpuSeries := ettSeries(10, 0.06, 288)
	srv := newETTVMServer(t, &cpuSeries)
	h := &Handler{vmURL: srv.URL, client: srv.Client()}
	h.checkCapacityETTAlerts()

	firing, resolved := countCapacityETTEvents(t)
	if firing != 0 || resolved != 0 {
		t.Fatalf("firing=%d resolved=%d, want 0/0 (ETT≈73.3h > 72h)", firing, resolved)
	}
}

// 无数据（VM 无 node-exporter 实例）→ 不产生任何事件（无数据不误报）。
func TestCapacityETTAlertNoDataNoFire(t *testing.T) {
	orig := snapshotAlertEvents()
	defer restoreAlertEvents(orig)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"status": "success",
			"data":   map[string]interface{}{"resultType": "vector", "result": []interface{}{}},
		})
	}))
	t.Cleanup(srv.Close)
	h := &Handler{vmURL: srv.URL, client: srv.Client()}
	h.checkCapacityETTAlerts()

	firing, resolved := countCapacityETTEvents(t)
	if firing != 0 || resolved != 0 {
		t.Fatalf("firing=%d resolved=%d, want 0/0 (no data)", firing, resolved)
	}
}

// 恢复：第一轮 cpu 快速上升 → firing；第二轮 cpu 恢复平缓（ETT 不再 imminent）→ 自动 resolved。
func TestCapacityETTAlertResolved(t *testing.T) {
	orig := snapshotAlertEvents()
	defer restoreAlertEvents(orig)

	cpuSeries := ettSeries(10, 0.15, 288)
	srv := newETTVMServer(t, &cpuSeries)
	h := &Handler{vmURL: srv.URL, client: srv.Client()}

	// 第一轮：触发
	h.checkCapacityETTAlerts()
	firing, resolved := countCapacityETTEvents(t)
	if firing != 1 || resolved != 0 {
		t.Fatalf("round 1: firing=%d resolved=%d, want firing=1 resolved=0", firing, resolved)
	}

	// 第二轮：cpu 恢复平缓（无增长）→ ETT 不再 imminent → 自动 resolved
	cpuSeries = ettSeries(30, 0, 288)
	h.checkCapacityETTAlerts()
	firing, resolved = countCapacityETTEvents(t)
	if firing != 0 || resolved != 1 {
		t.Fatalf("round 2: firing=%d resolved=%d, want firing=0 resolved=1", firing, resolved)
	}
}

// ettAlertHours：默认 72，env ETT_ALERT_HOURS 可覆盖。
func TestETTAlertHoursDefaultAndOverride(t *testing.T) {
	t.Setenv("ETT_ALERT_HOURS", "")
	if v := ettAlertHours(); v != 72 {
		t.Fatalf("default=%d, want 72", v)
	}
	t.Setenv("ETT_ALERT_HOURS", "48")
	if v := ettAlertHours(); v != 48 {
		t.Fatalf("override=%d, want 48", v)
	}
	t.Setenv("ETT_ALERT_HOURS", "abc")
	if v := ettAlertHours(); v != 72 {
		t.Fatalf("invalid env=%d, want 72", v)
	}
}
