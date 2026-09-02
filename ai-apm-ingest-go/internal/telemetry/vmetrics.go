package telemetry

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/observability-platform/ai-apm-ingest-go/internal/telemetrylabels"
)

// vmetricsClient 复用默认 HTTP client（写 VM 用）。
var vmetricsClient = &http.Client{Timeout: 10 * time.Second}

// post 将 Prometheus text 行 POST 到 VM /api/v1/import/prometheus（生产写入）。
func (w *VictoriaMetricsWriter) post(line string) error {
	endpoint := strings.TrimSuffix(w.endpoint, "/") + "/api/v1/import/prometheus"
	resp, err := vmetricsClient.Post(endpoint, "text/plain", bytes.NewBufferString(line+"\n"))
	if err != nil {
		log.Printf("victoriametrics write: %v", err)
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		log.Printf("victoriametrics write: status %d: %s", resp.StatusCode, string(b))
		return fmt.Errorf("victoriametrics write: status %d", resp.StatusCode)
	}
	return nil
}

// VictoriaMetricsWriter 写入 VictoriaMetrics 的 adapter（remote-write /api/v1/import 或
// /insert/<account>:<project>/metrics 的 plaintext 格式）。
// Phase 5：实现并可通过单测；默认 ModeDisabled（不生产写），可受控切换 ModeNew。
type VictoriaMetricsWriter struct {
	endpoint string
	mode     Mode
}

// NewVictoriaMetricsWriter 创建 VM writer（默认 ModeDisabled，可受控切换）。
// endpoint 形如 "http://vm:8428"。
func NewVictoriaMetricsWriter(endpoint string) *VictoriaMetricsWriter {
	return &VictoriaMetricsWriter{endpoint: endpoint, mode: ModeDisabled}
}

// NewVictoriaMetricsWriterMode 以显式模式创建 VM writer（供隔离/受控环境真实启用）。
func NewVictoriaMetricsWriterMode(endpoint string, mode Mode) *VictoriaMetricsWriter {
	return &VictoriaMetricsWriter{endpoint: endpoint, mode: mode}
}

// Enable 开启生产写入（受控激活机制；Phase 6 原子 cutover 窗口使用）。
func (w *VictoriaMetricsWriter) Enable() { w.mode = ModeNew }

// SetMode 受控切换写入模式（Phase 6 原子窗口由配置驱动调用）。
func (w *VictoriaMetricsWriter) SetMode(m Mode) { w.mode = m }

// Enabled 返回当前是否生产写入（默认 legacy=false）。
func (w *VictoriaMetricsWriter) Enabled() bool { return w.mode == ModeNew }

// Write 校验 scope labels 后序列化单条 VM metric（自动推断 scope：带 resource_id 视为
// resource，否则视为 cluster）。legacy 模式仅校验+序列化，不发送。
func (w *VictoriaMetricsWriter) Write(labels map[string]string, value float64, ts time.Time) WriteResult {
	scope := ScopeCluster
	if labels["resource_id"] != "" {
		scope = ScopeResource
	}
	return w.WriteScope(labels, scope, value, ts)
}

// WriteScope 按显式 scope 校验并序列化单条 VM metric。
// scope 为 resource 时 resource_id 必须为 canonical UUID；cluster/aggregate 可选。
// legacy 模式校验通过即算准备就绪，不实际发送。
func (w *VictoriaMetricsWriter) WriteScope(labels map[string]string, scope string, value float64, ts time.Time) WriteResult {
	if err := telemetrylabels.ValidateScopeLabels(labels, scope); err != nil {
		return invalidScopeResult()
	}
	if labels["__name__"] == "" {
		return WriteResult{Status: "error", ErrorCode: "MISSING_NAME", Retryable: false}
	}
	if w.mode != ModeNew {
		// legacy：校验通过即算准备就绪；不实际发送（cutover 前）。
		return okResult()
	}
	// new：真实写入 VictoriaMetrics（/api/v1/import/prometheus，Prometheus text 格式）。
	if err := w.post(w.serializeLine(labels, value, ts)); err != nil {
		return WriteResult{Status: "error", ErrorCode: "WRITE_FAILED", Retryable: true, Message: err.Error()}
	}
	return okResult()
}

// WriteBatch validates and writes a group of samples in one import request.
// A RED flush is a single logical observation; sending its call, error and
// duration counters together avoids partially publishing a batch where the
// RCA reader could observe only one component of the signal.
func (w *VictoriaMetricsWriter) WriteBatch(points []MetricPoint) WriteResult {
	if len(points) == 0 {
		return okResult()
	}
	lines := make([]string, 0, len(points))
	for _, point := range points {
		scope := ScopeCluster
		if point.Labels["resource_id"] != "" {
			scope = ScopeResource
		}
		if err := telemetrylabels.ValidateScopeLabels(point.Labels, scope); err != nil {
			return invalidScopeResult()
		}
		if point.Labels["__name__"] == "" {
			return WriteResult{Status: "error", ErrorCode: "MISSING_NAME", Retryable: false}
		}
		lines = append(lines, w.serializeLine(point.Labels, point.Value, point.TS))
	}
	if w.mode != ModeNew {
		return okResult()
	}
	if err := w.post(strings.Join(lines, "\n")); err != nil {
		return WriteResult{Status: "error", ErrorCode: "WRITE_FAILED", Retryable: true, Message: err.Error()}
	}
	return okResult()
}

// serializeLine 将单条 metric 序列化为 VM plaintext line（仅用于单测/调试，不发送）。
// 格式：<name>{<k>="<v>",...} <value> <unix_ms>
func (w *VictoriaMetricsWriter) serializeLine(labels map[string]string, value float64, ts time.Time) string {
	var b strings.Builder
	b.WriteString(labels["__name__"])
	b.WriteByte('{')
	keys := make([]string, 0, len(labels))
	for k := range labels {
		if k == "__name__" {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for i, k := range keys {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(k)
		b.WriteString(`="`)
		b.WriteString(labels[k])
		b.WriteByte('"')
	}
	b.WriteByte('}')
	b.WriteByte(' ')
	b.WriteString(strconv.FormatFloat(value, 'g', -1, 64))
	b.WriteByte(' ')
	b.WriteString(strconv.FormatInt(ts.UnixMilli(), 10))
	return b.String()
}
