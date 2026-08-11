package clickhouse

import (
	"strings"
	"testing"
	"time"

	"github.com/observability-platform/ai-apm-ingest-go/internal/model"
)

func TestEscapeTSV(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"plain", "plain"},
		{`back\slash`, `back\\slash`},
		{"tab\there", `tab\there`},
		{"new\nline", `new\nline`},
		{"cr\rhere", `cr\rhere`},
		{`mixed\t\n\r\\`, `mixed\\t\\n\\r\\\\`},
		{"", ""},
	}
	for _, c := range cases {
		if got := escapeTSV(c.in); got != c.want {
			t.Errorf("escapeTSV(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// serializeSpans 应把字段内的 \n \t 转义，保证每行恰好是 TabSeparated 预期列数、行间以单个 \n 分隔。
func TestSerializeSpansEscapesSpecialChars(t *testing.T) {
	w := &Writer{}
	spans := []*model.Span{
		{
			TenantID:     "default",
			TraceID:      "trace-1",
			SpanID:       "span-1",
			ServiceName:  "svc-a",
			OperationName: "GET /api\nwith-newline", // 含换行
			SpanKind:     "server",
			StartTime:    time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC),
			DurationNs:   1000,
			Attributes:   map[string]string{"k1": "v1\nv2", "k2": "a\tb"},
			HTTPMethod:   "GET",
			HTTPURL:      "/api",
			DBStatement:  "SELECT 1\nFROM t", // SQL 跨行
			K8sNamespace: "ns-1",
			K8sPodName:   "pod-1",
		},
	}
	rows := string(w.serializeSpans(spans))
	lines := strings.Split(strings.TrimSuffix(rows, "\n"), "\n")
	if len(lines) != 1 {
		t.Fatalf("expected 1 row, got %d", len(lines))
	}
	line := lines[0]
	// 每行字段数应与格式串一致（24 个 \t 分隔 25 字段，含 cluster_id）
	cols := strings.Split(line, "\t")
	if len(cols) != 25 {
		t.Fatalf("expected 25 columns, got %d: %q", len(cols), line)
	}
	// 第 7 列 OperationName 应已转义（无裸换行）（0-based 6）
	if strings.ContainsAny(cols[6], "\n\t") {
		t.Fatalf("OperationName column should be escaped, got: %q", cols[6])
	}
	if cols[6] != `GET /api\nwith-newline` {
		t.Fatalf("OperationName = %q, want escaped %q", cols[6], `GET /api\nwith-newline`)
	}
	// DBStatement 列（0-based 16）应转义
	if strings.ContainsAny(cols[16], "\n") {
		t.Fatalf("DBStatement column should be escaped, got: %q", cols[16])
	}
	// Attributes Map 列（0-based 11）应整体可解析（内部值含换行需转义）
	if strings.Contains(cols[11], "\nv") {
		t.Fatalf("Attributes Map column should have escaped inner newline, got: %q", cols[11])
	}
	// 第 2 列 cluster_id 应为 default（未显式设置时回退）
	if cols[1] != "default" {
		t.Fatalf("cluster_id = %q, want default", cols[1])
	}
}

// serializeSpans 含纯文本字段时不改变输出（无特殊字符原样通过）。
func TestSerializeSpansPlain(t *testing.T) {
	w := &Writer{}
	spans := []*model.Span{
		{
			TenantID: "t", TraceID: "tr", SpanID: "sp", ServiceName: "s",
			OperationName: "op", SpanKind: "server", StatusCode: 200,
			StartTime: time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC), DurationNs: 5,
			HTTPMethod: "GET", HTTPURL: "/", DBSystem: "mysql",
		},
	}
	rows := string(w.serializeSpans(spans))
	if strings.Contains(rows, `\n`) || strings.Contains(rows, `\t`) {
		t.Fatalf("plain span should not introduce escapes: %q", rows)
	}
}
