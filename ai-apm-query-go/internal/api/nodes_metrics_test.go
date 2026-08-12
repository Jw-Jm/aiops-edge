package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestParseQuantity(t *testing.T) {
	cases := map[string]float64{
		"1000m": 1.0, "1536m": 1.536, "2": 2.0, "0.5": 0.5,
		// 内存统一换算到 Ki 基数：1Mi=1024Ki，1Gi=1024Mi=1048576Ki
		"12345Ki": 12345.0, "1Gi": 1048576.0, "500Mi": 512000.0, "3Gi": 3145728.0,
		"": 0.0,
	}
	for in, want := range cases {
		if got := parseQuantity(in); got != want {
			t.Errorf("parseQuantity(%q)=%v, want %v", in, got, want)
		}
	}
}

// 用真实 k8sAPI 返回的 MetricsList 结构测解析与 usage_pct 计算。
func TestParseNodeMetrics(t *testing.T) {
	body := []byte(`{"kind":"NodeMetricsList","items":[
		{"metadata":{"name":"orbstack"},
		 "usage":{"cpu":"250m","memory":"3212084Ki"}},
		{"metadata":{"name":"worker-1"},
		 "usage":{"cpu":"1","memory":"2Gi"}}
	]}`)
	// capacity 来自 k8sNodes：CPU="2" memory="4Gi"
	nodes := parseNodeMetrics(body, map[string]map[string]string{
		"orbstack": {"cpu": "2", "memory": "4Gi"},
		"worker-1": {"cpu": "2", "memory": "8Gi"},
	})
	if len(nodes) != 2 {
		t.Fatalf("expected 2 nodes, got %d", len(nodes))
	}
	for _, m := range nodes {
		name := m["node"].(string)
		cpuPct := m["cpu_usage_pct"].(float64)
		memPct := m["mem_usage_pct"].(float64)
		if name == "orbstack" {
			// 0.25/2*100 = 12.5
			if cpuPct < 12.4 || cpuPct > 12.6 { t.Errorf("orbstack cpu_pct=%v want ~12.5", cpuPct) }
			// 3212084Ki / 4Gi(4194304Ki) *100
			if memPct < 76 || memPct > 77 { t.Errorf("orbstack mem_pct=%v want ~76.6", memPct) }
		}
	}
}

// 验证 handler：注入 k8sAPI 返回 MetricsList，assert usage_pct 计算与 200。
func TestNodesMetricsHandler(t *testing.T) {
	h := &Handler{}
	orig := k8sAPIFn
	k8sAPIFn = func(path string) ([]byte, error) {
		return []byte(`{"items":[{"metadata":{"name":"orbstack"},"usage":{"cpu":"250m","memory":"2Gi"}}]}`), nil
	}
	defer func() { k8sAPIFn = orig }()
	req := httptest.NewRequest("GET", "/api/v1/nodes/metrics", nil)
	w := httptest.NewRecorder()
	h.NodesMetrics(w, req)
	if w.Code != http.StatusOK { t.Fatalf("expected 200, got %d", w.Code) }
	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	nodes := resp["nodes"].([]interface{})
	if len(nodes) != 1 { t.Fatalf("expected 1 node, got %d", len(nodes)) }
}
