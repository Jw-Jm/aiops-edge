package api

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/observability-platform/ai-apm-query-go/internal/query"
)

// insertAlertEvents 生成的 INSERT 应包含正确字段与转义（单引号/反斜杠），且 version 使 ReplacingMergeTree 保留新版本。
func TestInsertAlertEventsEscapesSQL(t *testing.T) {
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(buf)
		gotBody = string(buf)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	h := &Handler{chHost: srv.URL, chPort: 0, client: &http.Client{}}
	// 手动构造 CH URL（httptest 用完整 URL）
	host, port := splitHostPort(srv.URL)
	h.chHost = host
	h.chPort = port

	events := []AlertEvent{
		{
			ID: "evt-1", RuleID: "r1", RuleName: "CPU 高", Service: "svc-a",
			Severity: "critical", Message: "CPU 超阈值 with 'quote' and \\back",
			Value: 90, Threshold: 80, Timestamp: "2026-08-09T12:00:00Z",
			Count: 1, FirstTimestamp: "2026-08-09T12:00:00Z", LastTimestamp: "2026-08-09T12:05:00Z",
			Status: "firing", Signature: "sig-1",
		},
	}
	if err := h.insertAlertEvents(events); err != nil {
		t.Fatalf("insertAlertEvents: %v", err)
	}
	if !strings.HasPrefix(gotBody, "INSERT INTO observability.alert_events") {
		t.Fatalf("not an INSERT, got: %.100s", gotBody)
	}
	// 单引号被转义（message 里的 ' 变 \'）
	if !strings.Contains(gotBody, `超阈值 with \'quote\'`) {
		t.Fatalf("message single quote not escaped, got: %.200s", gotBody)
	}
	// 反斜杠被转义
	if !strings.Contains(gotBody, `\\back`) {
		t.Fatalf("backslash not escaped, got: %.200s", gotBody)
	}
	// 时间戳转 CH 格式
	if !strings.Contains(gotBody, `'2026-08-09 12:00:00.000'`) {
		t.Fatalf("timestamp not formatted for CH, got: %.200s", gotBody)
	}
}

// queryAlertEvents 应正确解析 CH JSONEachRow 响应，并按 last_timestamp 倒序返回。
func TestQueryAlertEventsParses(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		q := r.URL.Query().Get("query")
		if !strings.Contains(q, "FROM observability.alert_events") {
			t.Errorf("unexpected query: %s", q)
		}
		if !strings.Contains(q, "ORDER BY last_timestamp DESC") {
			t.Errorf("missing ORDER BY: %s", q)
		}
		// JSONEachRow 格式：每行一个 JSON 对象（非数组）
		_, _ = w.Write([]byte(`{"id":"evt-2","rule_id":"r2","rule_name":"内存高","service":"svc-b","severity":"warning","message":"mem","value":85.0,"threshold":80.0,"timestamp":"2026-08-09 12:00:00.000","count":3,"first_timestamp":"2026-08-09 11:00:00.000","last_timestamp":"2026-08-09 12:00:00.000","status":"firing","acknowledged_at":null,"acknowledged_by":"","resolved_at":null,"resolved_by":"","timeline":"","investigation":"","signature":"sig-2"}`))
	}))
	defer srv.Close()
	h := &Handler{chHost: srv.URL, chPort: 0, client: &http.Client{}}
	host, port := splitHostPort(srv.URL)
	h.chHost = host
	h.chPort = port
	h.repo = *query.NewClickHouseRepo(fmt.Sprintf("http://%s:%d", host, port), &http.Client{Timeout: 5 * time.Second})
	h.alertRepo = query.NewAlertRepository(&h.repo)

	rows, err := h.queryAlertEvents("", 0, 100)
	if err != nil {
		t.Fatalf("queryAlertEvents: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("len=%d, want 1", len(rows))
	}
	if rows[0].ID != "evt-2" || rows[0].Service != "svc-b" || rows[0].Value != 85.0 {
		t.Fatalf("row mismatch: %+v", rows[0])
	}
	if rows[0].Timestamp != "2026-08-09T12:00:00Z" {
		t.Fatalf("timestamp not converted back to RFC3339, got %q", rows[0].Timestamp)
	}
}

// splitHostPort 把 http://host:port 拆成 host, port（供 Handler 使用）。
func splitHostPort(u string) (string, int) {
	s := strings.TrimPrefix(u, "http://")
	i := strings.LastIndex(s, ":")
	port := 0
	if i >= 0 {
		port = atoiSafe(s[i+1:])
	}
	return strings.TrimSuffix(s, ":"+s[i+1:]), port
}

func atoiSafe(s string) int {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			break
		}
		n = n*10 + int(c-'0')
	}
	return n
}
