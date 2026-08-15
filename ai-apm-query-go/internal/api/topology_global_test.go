package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// mockGlobalTopologyCH 构造一个按查询内容返回不同 JSONEachRow 的 mock ClickHouse：
//   - service_topology 查询 → 边行
//   - 含 k8s_namespace 的聚合 → service→ns 行
//   - 其余 trace_spans 节点聚合 → 节点行
// 返回可直接调用 GlobalTopology 的 Handler。
func mockGlobalTopologyCH(t *testing.T, edgeRows, nodeRows, nsRows string) *Handler {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		q := r.URL.Query().Get("query")
		var out string
		switch {
		case strings.Contains(q, "FROM observability.service_topology"):
			out = edgeRows
		case strings.Contains(q, "k8s_namespace"):
			out = nsRows
		case strings.Contains(q, "GROUP BY service_name"):
			out = nodeRows
		default:
			out = ""
		}
		_, _ = w.Write([]byte(out))
	}))
	t.Cleanup(srv.Close)

	h := &Handler{client: &http.Client{}}
	host, port := splitHostPort(srv.URL)
	h.chHost = host
	h.chPort = port
	return h
}

const (
	gtEdgeRows = "" +
		`{"source_service":"frontend","target_service":"backend","calls":10,"errs":0,"avg_ns":1000000}` + "\n" +
		`{"source_service":"frontend","target_service":"payments","calls":5,"errs":0,"avg_ns":2000000}` + "\n" +
		`{"source_service":"backend","target_service":"db","calls":8,"errs":1,"avg_ns":1500000}` + "\n" +
		`{"source_service":"frontend","target_service":"ingest (deleted)","calls":3,"errs":0,"avg_ns":1000000}` + "\n"
	gtNodeRows = "" +
		`{"service":"frontend","calls":100,"errs":1,"avg_ns":1000000}` + "\n" +
		`{"service":"backend","calls":80,"errs":2,"avg_ns":1200000}` + "\n" +
		`{"service":"payments","calls":60,"errs":3,"avg_ns":2000000}` + "\n" +
		`{"service":"db","calls":50,"errs":5,"avg_ns":1500000}` + "\n" +
		`{"service":"ingest (deleted)","calls":70,"errs":0,"avg_ns":900000}` + "\n"
	gtNSRows = "" +
		`{"service":"frontend","ns":"app","calls":100}` + "\n" +
		`{"service":"backend","ns":"app","calls":80}` + "\n" +
		`{"service":"payments","ns":"pay","calls":60}` + "\n" +
		`{"service":"db","ns":"data","calls":50}` + "\n" +
		`{"service":"ingest (deleted)","ns":"system","calls":70}` + "\n" +
		`{"service":"legacy","ns":"","calls":5}` + "\n"
)

// callGlobalTopology 执行 GET /api/v1/topology/global 并解析响应。
func callGlobalTopology(t *testing.T, h *Handler, query string) map[string]interface{} {
	t.Helper()
	path := "/api/v1/topology/global"
	if query != "" {
		path += "?" + query
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	h.GlobalTopology(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return resp
}

// nodeByName 把响应 nodes 转为 name → node 映射（节点顺序不保证）。
func nodeByName(t *testing.T, resp map[string]interface{}) map[string]map[string]interface{} {
	t.Helper()
	raw, ok := resp["nodes"].([]interface{})
	if !ok {
		t.Fatalf("expected nodes array, got %T", resp["nodes"])
	}
	out := map[string]map[string]interface{}{}
	for _, it := range raw {
		n, _ := it.(map[string]interface{})
		name, _ := n["name"].(string)
		out[name] = n
	}
	return out
}

// edgeKeySet 把响应 edges 转为 "src>tgt" 集合。
func edgeKeySet(t *testing.T, resp map[string]interface{}) map[string]bool {
	t.Helper()
	raw, ok := resp["edges"].([]interface{})
	if !ok {
		t.Fatalf("expected edges array, got %T", resp["edges"])
	}
	out := map[string]bool{}
	for _, it := range raw {
		e, _ := it.(map[string]interface{})
		src, _ := e["source_service"].(string)
		tgt, _ := e["target_service"].(string)
		out[src+">"+tgt] = true
	}
	return out
}

// ===== 契约2/3/5：未传 namespace → 全部节点 + namespace 标注 + namespaces 列表 + deleted 剔除 =====

func TestGlobalTopologyNamespacesNoFilter(t *testing.T) {
	h := mockGlobalTopologyCH(t, gtEdgeRows, gtNodeRows, gtNSRows)
	resp := callGlobalTopology(t, h, "")

	nodes := nodeByName(t, resp)
	// deleted 节点剔除
	if _, ok := nodes["ingest (deleted)"]; ok {
		t.Fatal("deleted node should be excluded")
	}
	for _, name := range []string{"frontend", "backend", "payments", "db"} {
		if _, ok := nodes[name]; !ok {
			t.Fatalf("node %q missing", name)
		}
	}
	// namespace 标注
	wantNS := map[string]string{"frontend": "app", "backend": "app", "payments": "pay", "db": "data"}
	for name, want := range wantNS {
		if got, _ := nodes[name]["namespace"].(string); got != want {
			t.Errorf("node %s namespace=%q, want %q", name, got, want)
		}
	}
	// 未传 namespace 时不得出现 external 标记
	for name, n := range nodes {
		if _, ok := n["external"]; ok {
			t.Errorf("node %s should not have external without namespace filter", name)
		}
	}

	// namespaces：非空、去重、排序（system 仅来自 deleted 服务，排除；空串排除）
	nsRaw, ok := resp["namespaces"].([]interface{})
	if !ok {
		t.Fatalf("expected namespaces array, got %T", resp["namespaces"])
	}
	var nss []string
	for _, it := range nsRaw {
		nss = append(nss, it.(string))
	}
	wantList := []string{"app", "data", "pay"}
	if len(nss) != len(wantList) {
		t.Fatalf("namespaces=%v, want %v", nss, wantList)
	}
	for i := range wantList {
		if nss[i] != wantList[i] {
			t.Fatalf("namespaces=%v, want %v", nss, wantList)
		}
	}

	// 边：deleted 边剔除，其余保留
	edges := edgeKeySet(t, resp)
	if edges["frontend>ingest (deleted)"] {
		t.Error("edge to deleted service should be excluded")
	}
	for _, e := range []string{"frontend>backend", "frontend>payments", "backend>db"} {
		if !edges[e] {
			t.Errorf("edge %s missing", e)
		}
	}
	if len(edges) != 3 {
		t.Errorf("edge_count=%d, want 3", len(edges))
	}
	if n, _ := resp["node_count"].(float64); int(n) != 4 {
		t.Errorf("node_count=%v, want 4", n)
	}
}

// ===== 契约4：namespace 过滤 → 选中 ns 节点 + external 邻居，跨 ns 边保留 =====

func TestGlobalTopologyNamespaceFilter(t *testing.T) {
	h := mockGlobalTopologyCH(t, gtEdgeRows, gtNodeRows, gtNSRows)
	resp := callGlobalTopology(t, h, "namespace=app")

	nodes := nodeByName(t, resp)
	for _, name := range []string{"frontend", "backend", "payments", "db"} {
		if _, ok := nodes[name]; !ok {
			t.Fatalf("node %q should be present", name)
		}
	}
	if _, ok := nodes["ingest (deleted)"]; ok {
		t.Fatal("deleted node should be excluded")
	}
	// 选中 ns 节点不标记 external
	for _, name := range []string{"frontend", "backend"} {
		if _, ok := nodes[name]["external"]; ok {
			t.Errorf("node %s (in app) should not be external", name)
		}
	}
	// 外部邻居标记 external:true 且带真实 namespace
	for _, name := range []string{"payments", "db"} {
		n := nodes[name]
		if ext, _ := n["external"].(bool); !ext {
			t.Errorf("node %s should be external:true", name)
		}
	}
	if got, _ := nodes["payments"]["namespace"].(string); got != "pay" {
		t.Errorf("payments namespace=%q, want pay", got)
	}
	if got, _ := nodes["db"]["namespace"].(string); got != "data" {
		t.Errorf("db namespace=%q, want data", got)
	}

	// 跨 ns 边保留：frontend→payments（app→pay）、backend→db（app→data）
	edges := edgeKeySet(t, resp)
	for _, e := range []string{"frontend>backend", "frontend>payments", "backend>db"} {
		if !edges[e] {
			t.Errorf("edge %s should be kept", e)
		}
	}
	if len(edges) != 3 {
		t.Errorf("edge_count=%d, want 3", len(edges))
	}
	// namespaces 列表不受过滤影响（下拉框全量）
	nsRaw, _ := resp["namespaces"].([]interface{})
	if len(nsRaw) != 3 {
		t.Errorf("namespaces=%v, want full list of 3", nsRaw)
	}
}

// ===== 契约4：单节点 ns → 仅自身 + 直接邻居 =====

func TestGlobalTopologyNamespaceFilterIsolated(t *testing.T) {
	h := mockGlobalTopologyCH(t, gtEdgeRows, gtNodeRows, gtNSRows)
	resp := callGlobalTopology(t, h, "namespace=data")

	nodes := nodeByName(t, resp)
	if len(nodes) != 2 {
		t.Fatalf("expected 2 nodes (db + backend), got %d: %v", len(nodes), keysOf(nodes))
	}
	if _, ok := nodes["db"]; !ok {
		t.Error("db node missing")
	}
	if _, ok := nodes["backend"]; !ok {
		t.Error("backend neighbor missing")
	}
	if _, ok := nodes["db"]["external"]; ok {
		t.Error("db (in data) should not be external")
	}
	if ext, _ := nodes["backend"]["external"].(bool); !ext {
		t.Error("backend should be external:true")
	}
	edges := edgeKeySet(t, resp)
	if len(edges) != 1 || !edges["backend>db"] {
		t.Errorf("edges=%v, want only backend>db", edges)
	}
}

// ===== 契约5：ns 聚合查询失败降级（拓扑仍可渲染，namespaces 为空）=====

func TestGlobalTopologyNSQueryDegrades(t *testing.T) {
	// ns 查询返回 CH 错误 → serviceNS 为空映射，节点无 namespace，namespaces 空列表
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		q := r.URL.Query().Get("query")
		switch {
		case strings.Contains(q, "FROM observability.service_topology"):
			_, _ = w.Write([]byte(gtEdgeRows))
		case strings.Contains(q, "k8s_namespace"):
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("unknown column k8s_namespace"))
		case strings.Contains(q, "GROUP BY service_name"):
			_, _ = w.Write([]byte(gtNodeRows))
		default:
			_, _ = w.Write([]byte(""))
		}
	}))
	defer srv.Close()

	h := &Handler{client: &http.Client{}}
	host, port := splitHostPort(srv.URL)
	h.chHost = host
	h.chPort = port

	resp := callGlobalTopology(t, h, "")
	nodes := nodeByName(t, resp)
	if len(nodes) != 4 {
		t.Fatalf("topology should still render without ns data, got %d nodes", len(nodes))
	}
	for name, n := range nodes {
		if ns, _ := n["namespace"].(string); ns != "" {
			t.Errorf("node %s namespace=%q, want empty on ns query failure", name, ns)
		}
	}
	nsRaw, _ := resp["namespaces"].([]interface{})
	if len(nsRaw) != 0 {
		t.Errorf("namespaces=%v, want empty on ns query failure", nsRaw)
	}
}

func keysOf(m map[string]map[string]interface{}) []string {
	var ks []string
	for k := range m {
		ks = append(ks, k)
	}
	return ks
}
