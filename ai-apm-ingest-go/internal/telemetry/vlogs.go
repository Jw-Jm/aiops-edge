package telemetry

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/observability-platform/ai-apm-ingest-go/internal/telemetrylabels"
)

// vlogsClient 复用默认 HTTP client（写 VLogs 用）。
var vlogsClient = &http.Client{Timeout: 10 * time.Second}

// post 将 JSON line POST 到 VictoriaLogs /insert/jsonline（生产写入）。
func (w *VictoriaLogsWriter) post(line string) error {
	endpoint := strings.TrimSuffix(w.endpoint, "/") + "/insert/jsonline"
	resp, err := vlogsClient.Post(endpoint, "application/json", bytes.NewBufferString(line+"\n"))
	if err != nil {
		log.Printf("victorialogs write: %v", err)
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		log.Printf("victorialogs write: status %d: %s", resp.StatusCode, string(b))
		return fmt.Errorf("victorialogs write: status %d", resp.StatusCode)
	}
	return nil
}

// VictoriaLogsWriter 写入 VictoriaLogs 的 adapter（/insert/jsonline 风格）。
// Phase 5：实现并可通过单测；默认 ModeDisabled（不生产写），可受控切换 ModeNew。
type VictoriaLogsWriter struct {
	endpoint string
	mode     Mode
}

// NewVictoriaLogsWriter 创建 VLogs writer（默认 ModeDisabled，可受控切换）。
// endpoint 形如 "http://vlogs:9428"。
func NewVictoriaLogsWriter(endpoint string) *VictoriaLogsWriter {
	return &VictoriaLogsWriter{endpoint: endpoint, mode: ModeDisabled}
}

// NewVictoriaLogsWriterMode 以显式模式创建 VLogs writer（供隔离/受控环境真实启用）。
func NewVictoriaLogsWriterMode(endpoint string, mode Mode) *VictoriaLogsWriter {
	return &VictoriaLogsWriter{endpoint: endpoint, mode: mode}
}

// Enable 开启生产写入（受控激活机制；Phase 6 原子 cutover 窗口使用）。
func (w *VictoriaLogsWriter) Enable() { w.mode = ModeNew }

// SetMode 受控切换写入模式（Phase 6 原子窗口由配置驱动调用）。
func (w *VictoriaLogsWriter) SetMode(m Mode) { w.mode = m }

// Enabled 返回当前是否生产写入（默认 legacy=false）。
func (w *VictoriaLogsWriter) Enabled() bool { return w.mode == ModeNew }

// WriteLog 校验 scope labels 后序列化单条日志（自动推断 scope）。
func (w *VictoriaLogsWriter) WriteLog(labels map[string]string, body string, ts time.Time) WriteResult {
	scope := ScopeCluster
	if labels["resource_id"] != "" {
		scope = ScopeResource
	}
	return w.WriteLogScope(labels, scope, body, ts)
}

// WriteLogScope 按显式 scope 校验并序列化单条日志。
// scope=resource 时 resource_id 必须为 canonical UUID；cluster/aggregate 可选。
// legacy 模式校验通过即算准备就绪，不实际发送。
func (w *VictoriaLogsWriter) WriteLogScope(labels map[string]string, scope string, body string, ts time.Time) WriteResult {
	if err := telemetrylabels.ValidateScopeLabels(labels, scope); err != nil {
		return invalidScopeResult()
	}
	if body == "" {
		return WriteResult{Status: "error", ErrorCode: "EMPTY_BODY", Retryable: false}
	}
	if w.mode != ModeNew {
		// legacy：校验通过即算准备就绪；不实际发送（cutover 前）。
		return okResult()
	}
	// new：真实写入 VictoriaLogs（/insert/jsonline）。
	if err := w.post(w.serializeJSONLine(labels, body, ts)); err != nil {
		return WriteResult{Status: "error", ErrorCode: "WRITE_FAILED", Retryable: true, Message: err.Error()}
	}
	return okResult()
}

// serializeJSONLine 将日志序列化为 VictoriaLogs JSON line（仅用于单测/调试，不发送）。
// _msg 为日志正文；labels 经 NormalizeScopeLabels 剔除空值后并入。
func (w *VictoriaLogsWriter) serializeJSONLine(labels map[string]string, body string, ts time.Time) string {
	m := telemetrylabels.NormalizeScopeLabels(labels)
	m["_msg"] = body
	m["_time"] = ts.Format("2006-01-02T15:04:05.000Z")
	b, _ := json.Marshal(m)
	return string(b)
}
