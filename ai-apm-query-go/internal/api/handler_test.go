package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/observability-platform/ai-apm-query-go/internal/store"
)

// TestListServicesReturnsCHServicesWithMetadata 验证重写后的 ListServices：
//   - 从 ClickHouse trace_spans 动态发现服务列表（source=trace）
//   - LEFT JOIN MySQL service_metadata 富化 owner/team/tier/description
//   - 响应结构为 {"services":[...], "total": N}
//
// ClickHouse 通过 httptest.Server mock（无需真实 CH）。
// MySQL 不可达时（store.GetDB() 返回 nil）跳过富化断言，但仍验证 CH 发现路径。
func TestListServicesReturnsCHServicesWithMetadata(t *testing.T) {
	// 1. Mock ClickHouse：返回两条 trace_spans 服务（JSONEachRow 格式，每行一个 JSON 对象）
	chSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		q := r.URL.Query().Get("query")
		if !strings.Contains(q, "FROM observability.trace_spans") {
			t.Errorf("unexpected CH query, want trace_spans, got: %s", q)
		}
		if !strings.Contains(q, "DISTINCT service_name") {
			t.Errorf("expected DISTINCT service_name in query, got: %s", q)
		}
		// JSONEachRow：每行一个 JSON 对象
		_, _ = w.Write([]byte(`{"service_name":"frontend"}` + "\n" + `{"service_name":"backend"}` + "\n"))
	}))
	defer chSrv.Close()

	h := &Handler{client: &http.Client{}}
	host, port := splitHostPort(chSrv.URL)
	h.chHost = host
	h.chPort = port

	// 2. MySQL 富化数据：可达时插入测试行，不可达则跳过富化断言
	db := store.GetDB()
	hasMySQL := db != nil
	if hasMySQL {
		// 幂等插入：frontend 有富化数据，backend 无（验证 LEFT JOIN 缺失走默认值）
		_, _ = db.Exec("INSERT IGNORE INTO service_metadata (service_name, owner, tier) VALUES ('frontend', 'team-a', 'critical')")
	}

	req := httptest.NewRequest("GET", "/api/v1/services", nil)
	w := httptest.NewRecorder()
	h.ListServices(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d, body=%s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v, body=%s", err, w.Body.String())
	}

	// 验证响应结构：services 数组 + total
	services, ok := resp["services"].([]interface{})
	if !ok {
		t.Fatalf("expected services []interface{}, got %T: %v", resp["services"], resp["services"])
	}
	if len(services) != 2 {
		t.Fatalf("expected 2 services, got %d: %v", len(services), services)
	}

	total, ok := resp["total"].(float64)
	if !ok {
		t.Fatalf("expected total number, got %T: %v", resp["total"], resp["total"])
	}
	if int(total) != 2 {
		t.Errorf("expected total=2, got %v", total)
	}

	// 验证 source=trace + 默认 tier=standard
	for _, s := range services {
		m, ok := s.(map[string]interface{})
		if !ok {
			t.Fatalf("expected service map, got %T", s)
		}
		if m["source"] != "trace" {
			t.Errorf("expected source=trace, got %v", m["source"])
		}
		name, _ := m["service_name"].(string)
		if name == "" {
			t.Errorf("expected non-empty service_name, got %v", m["service_name"])
		}
	}

	// 验证 frontend 排在最前（CH 返回按 service_name ORDER BY，frontend < backend）
	first := services[0].(map[string]interface{})
	if first["service_name"] != "frontend" {
		t.Errorf("expected first service=frontend (ORDER BY name), got %v", first["service_name"])
	}

	// MySQL 可达时验证富化字段（LEFT JOIN）
	if hasMySQL {
		frontendItem := first
		if frontendItem["owner"] != "team-a" {
			t.Errorf("expected frontend owner=team-a (enriched), got %v", frontendItem["owner"])
		}
		if frontendItem["tier"] != "critical" {
			t.Errorf("expected frontend tier=critical (enriched), got %v", frontendItem["tier"])
		}

		// backend 无富化数据 → 走默认值（tier=standard, owner="")
		backendItem := services[1].(map[string]interface{})
		if backendItem["service_name"] != "backend" {
			t.Errorf("expected second service=backend, got %v", backendItem["service_name"])
		}
		if backendItem["tier"] != "standard" {
			t.Errorf("expected backend tier=standard (default, no enrichment), got %v", backendItem["tier"])
		}
		if backendItem["owner"] != "" {
			t.Errorf("expected backend owner='' (default, no enrichment), got %v", backendItem["owner"])
		}
	} else {
		t.Logf("MySQL not available (store.GetDB()=nil), skipped enrichment assertions")
	}
}

// TestListServicesCHErrorReturns500 验证 ClickHouse 查询失败时返回 500。
func TestListServicesCHErrorReturns500(t *testing.T) {
	// Mock CH 返回 500 错误
	chSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("clickhouse internal error"))
	}))
	defer chSrv.Close()

	h := &Handler{client: &http.Client{}}
	host, port := splitHostPort(chSrv.URL)
	h.chHost = host
	h.chPort = port

	req := httptest.NewRequest("GET", "/api/v1/services", nil)
	w := httptest.NewRecorder()
	h.ListServices(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 on CH error, got %d", w.Code)
	}
}

// TestListServicesEmptyResult 验证 CH 返回空数据时返回空列表（非 nil）。
func TestListServicesEmptyResult(t *testing.T) {
	chSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// 空响应
		_, _ = w.Write([]byte(""))
	}))
	defer chSrv.Close()

	h := &Handler{client: &http.Client{}}
	host, port := splitHostPort(chSrv.URL)
	h.chHost = host
	h.chPort = port

	req := httptest.NewRequest("GET", "/api/v1/services", nil)
	w := httptest.NewRecorder()
	h.ListServices(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 on empty CH result, got %d", w.Code)
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	services, ok := resp["services"].([]interface{})
	if !ok {
		t.Fatalf("expected services array, got %T", resp["services"])
	}
	if len(services) != 0 {
		t.Errorf("expected empty services list, got %d items", len(services))
	}
	if resp["total"].(float64) != 0 {
		t.Errorf("expected total=0, got %v", resp["total"])
	}
}
