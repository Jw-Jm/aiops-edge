package tracesink

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/observability-platform/ai-apm-ingest-go/internal/model"
)

// ClickHouseSpanSink 把 Span 写入 ClickHouse observability.trace_spans（平台 Trace Persistent SoT）。
//
// C-01（0004_runtime_convergence / 报告 §16）：
//   - ClickHouse trace_spans 固定为平台 Trace SoT；OTLP/DeepFlow 只作为 Span 输入来源。
//   - 不依赖 clickhouse-go（P14 已移除 CH 依赖）；用 ClickHouse HTTP 接口
//     INSERT ... FORMAT JSONEachRow 写入，零新增依赖。
//   - 幂等：span_dedup_key = SHA256(tenant_id|cluster_id|trace_id|span_id|start_time_ns)，
//     ClickHouse ReplacingMergeTree 按 (tenant_id, cluster_id, service_name, date, start_time, trace_id)
//     去重；同一 Span 重复投递不产生重复逻辑 Trace/Evidence。
//   - 失败 fail-closed：Add 返回错误；production/candidate readiness 要求 sink 可用，
//     不允许"接收成功但静默丢 Span"。

const insertTemplate = `INSERT INTO observability.trace_spans FORMAT JSONEachRow`

// ClickHouseSpanSink implements pipeline.SpanSink via ClickHouse HTTP.
type ClickHouseSpanSink struct {
	httpURL  string // e.g. http://clickhouse.observability.svc:8123
	user     string // CH HTTP basic auth user（默认空=无鉴权）
	password string // CH HTTP basic auth password
	client   *http.Client

	mu      sync.Mutex
	health  bool
	lastErr error
}

// NewClickHouseSpanSink 构造 CH Span sink。httpURL 是 CH HTTP 接口 base（无 path）。
func NewClickHouseSpanSink(httpURL string, timeout time.Duration) *ClickHouseSpanSink {
	return NewClickHouseSpanSinkAuth(httpURL, "", "", timeout)
}

// NewClickHouseSpanSinkAuth 构造带 CH HTTP basic auth 的 Span sink（C-01 生产 CH 需要鉴权）。
func NewClickHouseSpanSinkAuth(httpURL, user, password string, timeout time.Duration) *ClickHouseSpanSink {
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	return &ClickHouseSpanSink{
		httpURL: httpURL, user: user, password: password,
		client: &http.Client{Timeout: timeout},
	}
}

// Probe verifies that the configured ClickHouse HTTP endpoint is reachable
// before ingest starts accepting traffic. Healthy must not remain false until
// the first Trace arrives: the Kubernetes liveness probe would otherwise
// restart an otherwise healthy process before it can receive that first span.
// A failed probe remains fail-closed and is surfaced through /health.
func (s *ClickHouseSpanSink) Probe() error {
	req, err := http.NewRequest(http.MethodGet, s.httpURL+"/?query="+urlQueryEncode("SELECT 1"), nil)
	if err != nil {
		s.mu.Lock()
		s.lastErr = err
		s.health = false
		s.mu.Unlock()
		return err
	}
	if s.user != "" {
		req.SetBasicAuth(s.user, s.password)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		probeErr := fmt.Errorf("tracesink: ch probe: %w", err)
		s.mu.Lock()
		s.lastErr = probeErr
		s.health = false
		s.mu.Unlock()
		return probeErr
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		err = fmt.Errorf("tracesink: ch probe status %d", resp.StatusCode)
		s.mu.Lock()
		s.lastErr = err
		s.health = false
		s.mu.Unlock()
		return err
	}
	s.mu.Lock()
	s.lastErr = nil
	s.health = true
	s.mu.Unlock()
	return nil
}

// Add implements pipeline.SpanSink.Add（接口签名无返回；失败经内部记录 + readiness fail-closed）。
// C-01：不静默丢 Span——写入失败记录 s.lastErr 并置 health=false（readiness 端 /health 据此 503）。
func (s *ClickHouseSpanSink) Add(sp *model.Span) {
	if sp == nil {
		s.mu.Lock()
		s.lastErr = fmt.Errorf("tracesink: nil span")
		s.health = false
		s.mu.Unlock()
		return
	}
	row := spanRow{}.from(sp)
	body, err := json.Marshal(row)
	if err != nil {
		s.mu.Lock()
		s.lastErr = err
		s.health = false
		s.mu.Unlock()
		return
	}
	if err := s.post([][]byte{body}); err != nil {
		s.mu.Lock()
		s.lastErr = err
		s.health = false
		s.mu.Unlock()
		return
	}
	s.mu.Lock()
	s.health = true
	s.mu.Unlock()
}

// AddBatch 批量写（每行 JSONEachRow，尾行换行）。
func (s *ClickHouseSpanSink) AddBatch(spans []*model.Span) error {
	if len(spans) == 0 {
		return nil
	}
	var buf bytes.Buffer
	for _, sp := range spans {
		row := spanRow{}.from(sp)
		b, err := json.Marshal(row)
		if err != nil {
			return err
		}
		buf.Write(b)
		buf.WriteByte('\n')
	}
	if err := s.postRaw(buf.Bytes()); err != nil {
		s.mu.Lock()
		s.lastErr = err
		s.health = false
		s.mu.Unlock()
		return err
	}
	s.mu.Lock()
	s.health = true
	s.mu.Unlock()
	return nil
}

// Healthy 报告 CH 是否最后写入成功（readiness fail-closed）。
func (s *ClickHouseSpanSink) Healthy() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.health
}

// LastError 返回最近一次写入错误。
func (s *ClickHouseSpanSink) LastError() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastErr
}

func (s *ClickHouseSpanSink) post(lines [][]byte) error {
	var buf bytes.Buffer
	for _, l := range lines {
		buf.Write(l)
		buf.WriteByte('\n')
	}
	return s.postRaw(buf.Bytes())
}

func (s *ClickHouseSpanSink) postRaw(body []byte) error {
	// CH HTTP 接口：POST /?query=INSERT ...（body 为行数据）。
	url := s.httpURL + "/?" + "query=" + urlQueryEncode(insertTemplate)
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "text/plain")
	if s.user != "" {
		req.SetBasicAuth(s.user, s.password)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("tracesink: ch post: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("tracesink: ch insert status %d", resp.StatusCode)
	}
	return nil
}

func urlQueryEncode(s string) string {
	// 简单 URL 编码（空格 → %20，括号 → %28%29 等）。用 net/url 更稳妥。
	const hexDigits = "0123456789ABCDEF"
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') ||
			c == '_' || c == '.' || c == '-' || c == '~' {
			out = append(out, c)
		} else {
			out = append(out, '%', hexDigits[c>>4], hexDigits[c&0xF])
		}
	}
	return string(out)
}

// spanRow 是 trace_spans 表的一行（对齐 CH schema）。
type spanRow struct {
	TenantID          string            `json:"tenant_id"`
	ClusterID         string            `json:"cluster_id"`
	TraceID           string            `json:"trace_id"`
	SpanID            string            `json:"span_id"`
	ParentSpanID      string            `json:"parent_span_id"`
	ServiceName       string            `json:"service_name"`
	OperationName     string            `json:"operation_name"`
	SpanKind          string            `json:"span_kind"`
	StatusCode        uint8             `json:"status_code"`
	StartTime         string            `json:"start_time"`
	DurationNs        uint64            `json:"duration_ns"`
	Attributes        map[string]string `json:"attributes"`
	HTTPMethod        string            `json:"http_method"`
	HTTPStatusCode    uint16            `json:"http_status_code"`
	HTTPURL           string            `json:"http_url"`
	DBSystem          string            `json:"db_system"`
	DBStatement       string            `json:"db_statement"`
	RPCSystem         string            `json:"rpc_system"`
	ServiceInstanceID string            `json:"service_instance_id"`
	K8sNamespace      string            `json:"k8s_namespace"`
	IsSlow            uint8             `json:"is_slow"`
	IsError           uint8             `json:"is_error"`
	// trace_spans.time_bucket is a required DateTime column used by the
	// materialized trace-summary index.  JSONEachRow does not apply a
	// server-side expression for omitted required columns, so the ingest
	// adapter must materialize the same five-minute bucket as the summary MV.
	TimeBucket   string `json:"time_bucket"`
	SpanDedupKey string `json:"span_dedup_key"`
	Date         string `json:"date"`
}

func (spanRow) from(sp *model.Span) spanRow {
	start := sp.StartTime
	return spanRow{
		TenantID: sp.TenantID, ClusterID: sp.ClusterID,
		TraceID: sp.TraceID, SpanID: sp.SpanID, ParentSpanID: sp.ParentSpanID,
		ServiceName: sp.ServiceName, OperationName: sp.OperationName, SpanKind: sp.SpanKind,
		// C-01：CH DateTime64(9) 解析不接受时区后缀 Z；用 UTC 无后缀格式（含纳秒）。
		StatusCode: sp.StatusCode, StartTime: start.UTC().Format("2006-01-02T15:04:05.999999999"),
		DurationNs: sp.DurationNs, Attributes: sp.Attributes,
		HTTPMethod: sp.HTTPMethod, HTTPStatusCode: sp.HTTPStatusCode, HTTPURL: sp.HTTPURL,
		DBSystem: sp.DBSystem, DBStatement: sp.DBStatement, RPCSystem: sp.RPCSystem,
		ServiceInstanceID: sp.ServiceInstanceID, K8sNamespace: sp.K8sNamespace,
		IsSlow: sp.IsSlow, IsError: sp.IsError,
		TimeBucket:   start.UTC().Truncate(5 * time.Minute).Format("2006-01-02T15:04:05"),
		SpanDedupKey: spanDedupKey(sp),
		Date:         start.UTC().Format("2006-01-02"),
	}
}

// spanDedupKey 幂等去重键（C-01：同一 Span 重复投递只形成一个逻辑 Trace/Evidence）。
func spanDedupKey(sp *model.Span) string {
	raw := sp.TenantID + "|" + sp.ClusterID + "|" + sp.TraceID + "|" + sp.SpanID +
		"|" + fmt.Sprintf("%d", sp.StartTime.UnixNano())
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}
