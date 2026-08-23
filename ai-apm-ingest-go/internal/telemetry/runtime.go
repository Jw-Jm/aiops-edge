package telemetry

import (
	"fmt"
	"os"
	"time"
)

// Runtime 是 new 后端（VictoriaMetrics / VictoriaLogs）的生产接线层（V9.2 P6.5）。
//
// 它只负责 new 链的构造与写入控制；【legacy ClickHouse path 由调用方（cmd/ingest/main.go）
// 独立保留】，本类型绝不回退 ClickHouse，也绝不把 new 写失败伪装成成功。
//
// 语义（对齐 writer.go）：Mode 是 new writer 自身的启用/停用开关，不是跨 writer 选择器。
//   - ModeDisabled / "legacy"：new 链停用，不发送；
//   - ModeNew：new 链生产写入。
type Runtime struct {
	Mode  Mode
	VM    *VictoriaMetricsWriter
	VLogs *VictoriaLogsWriter
}

// NewRuntime 以显式模式构造 new 链（供测试/受控隔离环境直接启用）。
func NewRuntime(mode Mode, vmURL, vlogsURL string) *Runtime {
	return &Runtime{
		Mode:  mode,
		VM:    NewVictoriaMetricsWriterMode(vmURL, mode),
		VLogs: NewVictoriaLogsWriterMode(vlogsURL, mode),
	}
}

// NewRuntimeFromEnv 从环境变量构造生产接线，fail-closed：
//   - TELEMETRY_WRITER_MODE：合法值 disabled/"legacy"/new；非法值拒绝启动；
//   - VICTORIA_METRICS_URL / VICTORIA_LOGS_URL：ModeNew 时必填（缺任一拒绝启动），
//     避免 new 链配置不完整却静默不写。
func NewRuntimeFromEnv() (*Runtime, error) {
	raw := os.Getenv("TELEMETRY_WRITER_MODE")
	mode, err := ParseMode(raw)
	if err != nil {
		return nil, fmt.Errorf("TELEMETRY_WRITER_MODE=%q invalid: %w", raw, err)
	}
	vmURL := os.Getenv("VICTORIA_METRICS_URL")
	vlogsURL := os.Getenv("VICTORIA_LOGS_URL")
	if mode == ModeNew {
		if vmURL == "" {
			return nil, fmt.Errorf("TELEMETRY_WRITER_MODE=new requires VICTORIA_METRICS_URL (fail-closed)")
		}
		if vlogsURL == "" {
			return nil, fmt.Errorf("TELEMETRY_WRITER_MODE=new requires VICTORIA_LOGS_URL (fail-closed)")
		}
	}
	return NewRuntime(mode, vmURL, vlogsURL), nil
}

// Enabled 返回 new 链是否进入生产写入。
func (r *Runtime) Enabled() bool { return r.Mode == ModeNew }

// WriteLog 把一条日志写入 VictoriaLogs（ModeNew 时真实发送；disabled/legacy 仅校验不发送）。
// labels 含 tenant_id / cluster_id / service_name / level，字段名对齐 new reader
// （query-go internal/query/logs.go 的 VLogsReader：LogsQL 用 tenant_id/cluster_id/service_name，
// vlogsRecord 解析 service_name / level）。三字段 contract 由 writer 内部校验。
func (r *Runtime) WriteLog(tenantID, clusterID, service, level, body string, ts time.Time) WriteResult {
	labels := map[string]string{
		"tenant_id":   tenantID,
		"cluster_id":  clusterID,
		"service_name": service,
		"level":       level,
	}
	return r.VLogs.WriteLogScope(labels, ScopeCluster, body, ts)
}

// WriteRED 把一条服务 RED 调用量（call_total）写入 VictoriaMetrics（ModeNew 时真实发送）。
// 指标名对齐 new reader（query-go internal/query/vmetrics.go）的 `sum(rate(call_total{...}))`。
// 标签含 tenant_id / cluster_id / service_name；三字段 contract 由 writer 内部校验。
func (r *Runtime) WriteRED(tenantID, clusterID, service string, value float64, ts time.Time) WriteResult {
	labels := map[string]string{
		"__name__":     "call_total",
		"tenant_id":    tenantID,
		"cluster_id":   clusterID,
		"service_name": service,
	}
	return r.VM.Write(labels, value, ts)
}
