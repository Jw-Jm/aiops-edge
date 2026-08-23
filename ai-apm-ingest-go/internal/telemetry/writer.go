// Package telemetry 提供 VictoriaMetrics / VictoriaLogs 的写入 adapter（V9.2 Phase 5）。
// 两者共享统一的 WriteResult 错误语义与 scope label 校验（复用 telemetrylabels）。
//
// Phase 5 定位：adapter 已实现、可通过单测、可由配置显式切换到生产写入（受控），但
// 默认 PRODUCTION_ACTIVE=false（ModeDisabled）。生产启用（ModeNew）跟随 Phase 6 reader
// cutover 的同一受控原子窗口（R2 §71/§72）——部署侧改配置即切换，无需改源码重建。
//
// 语义澄清（P6.5 semantic sanity）：Mode 是【new 后端 writer 自身的启用/停用开关】，
// 不是"选择 legacy 还是 new 的跨 writer 选择器"。
//   - ModeDisabled = new writer 停用（不发送）
//   - ModeNew      = new writer 启用（生产写）
//
// 停止【legacy ClickHouse writer】是另一个独立控制面（见 cutover coordinator 的
// LegacyWriterControl.Stop），绝不能通过把 new writer 设回 ModeDisabled 来实现。
package telemetry

import (
	"errors"
	"time"

	"github.com/observability-platform/ai-apm-ingest-go/internal/telemetrylabels"
)

// Mode 表示 new 后端 telemetry writer 的启用/停用状态（非跨 writer 选择器）。
type Mode string

const (
	// ModeDisabled 默认：new writer 停用，不实际发送，仅做 scope 校验与序列化（Phase 5 生产不切）。
	ModeDisabled Mode = "disabled"
	// ModeNew 生产写入（Phase 6 原子 cutover 窗口由部署配置切为 new）。
	ModeNew Mode = "new"
)

var errUnknownMode = errors.New("unknown telemetry writer mode")

// ParseMode 解析写入模式。合法值 "disabled"/"new"；"legacy" 作为 disabled 的历史别名（Phase 5
// 配置 TELEMETRY_WRITER_MODE=legacy 兼容）；非法值返回错误（调用方应 fail-closed 拒绝启动，
// 避免误配置静默进入错误模式）。
func ParseMode(s string) (Mode, error) {
	switch Mode(s) {
	case ModeDisabled, "legacy":
		return ModeDisabled, nil
	case ModeNew:
		return ModeNew, nil
	default:
		return ModeDisabled, errUnknownMode
	}
}

// WriteResult 一次写入调用的结构化结果。
// ErrorCode 空表示成功；非空为稳定错误码（供重试/告警判定）。
type WriteResult struct {
	Status    string // "ok" | "error"
	ErrorCode string // "" | "INVALID_SCOPE" | "NETWORK" | "HTTP" | "WRITE_FAILED"
	Retryable bool   // 是否可安全重试（网络类可重试；INVALID_SCOPE 不可）
	Message   string // 失败时的错误详情（诊断用，不参与语义判断）
}

func okResult() WriteResult { return WriteResult{Status: "ok"} }

func invalidScopeResult() WriteResult {
	return WriteResult{Status: "error", ErrorCode: "INVALID_SCOPE", Retryable: false}
}

// MetricPoint 一条带 scope labels 的指标写入点。
type MetricPoint struct {
	Labels map[string]string // 含 __name__、tenant_id、cluster_id、可选 resource_id、可选 service 等
	Value  float64
	TS     time.Time
}

// MetricsWriter 指标写入抽象（VictoriaMetrics remote-write 风格）。
type MetricsWriter interface {
	// Write 校验 scope labels 并写入单条 metric。legacy 模式仅校验+序列化，不发送。
	Write(labels map[string]string, value float64, ts time.Time) WriteResult
	// Enabled 表示是否进入生产写入（默认 legacy=false；切 new 后为 true）。
	Enabled() bool
	// SetMode 受控切换写入模式（Phase 6 原子窗口由配置驱动调用）。
	SetMode(m Mode)
}

// Scope 常量透传 telemetrylabels 的 scope 枚举，供调用方引用。
const (
	ScopeResource  = telemetrylabels.ScopeResource
	ScopeCluster   = telemetrylabels.ScopeCluster
	ScopeAggregate = telemetrylabels.ScopeAggregate
)
