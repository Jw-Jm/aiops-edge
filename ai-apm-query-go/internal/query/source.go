package query

import (
	"errors"
)

// ReaderMode 表示 reader 的数据源读取模式。
type ReaderMode string

const (
	// ModeLegacy 默认：reader 读 ClickHouse 旧 schema（trace_spans/log_records/service_topology/alert_events）。
	// Phase 6 reader readiness 阶段保持，不切生产。
	ModeLegacy ReaderMode = "legacy"
	// ModeNew reader 读 VM/VLogs（Raw Logs SoT=VictoriaLogs、Raw Metrics SoT=VictoriaMetrics），
	// trace/edge 仍读 ClickHouse。Phase 6 原子 cutover 窗口由部署配置切为 new。
	ModeNew ReaderMode = "new"
)

var errUnknownReaderMode = errors.New("unknown query reader mode")

// ParseReaderMode 解析 reader 模式。合法值 "legacy"/"new"；非法返回错误（调用方应
// fail-closed 拒绝启动，避免误配置静默进入错误读取模式）。
func ParseReaderMode(s string) (ReaderMode, error) {
	switch ReaderMode(s) {
	case ModeLegacy:
		return ModeLegacy, nil
	case ModeNew:
		return ModeNew, nil
	default:
		return ModeLegacy, errUnknownReaderMode
	}
}

// Reader 是某类数据当前应使用的读取后端。
type Reader string

const (
	// ReaderLegacy 读 ClickHouse 旧 schema。
	ReaderLegacy Reader = "legacy"
	// ReaderNew 读 VM/VLogs 新 SoT。
	ReaderNew Reader = "new"
)

// SourceRouter 按 reader 模式将 9 类资源路由到 legacy/new 后端。
type SourceRouter struct {
	mode ReaderMode
}

// NewSourceRouter 构造 SourceRouter。
func NewSourceRouter(mode ReaderMode) *SourceRouter {
	return &SourceRouter{mode: mode}
}

// ReaderFor 返回某类资源应使用的读取后端。
// traces 的 SoT 固定为 ClickHouse（V9.2），无论 mode 均走 legacy；kubernetes 直读 K8s API。
func (r *SourceRouter) ReaderFor(resource string) Reader {
	switch resource {
	case "traces":
		return ReaderLegacy // trace/edge SoT 固定 ClickHouse
	case "kubernetes":
		return ReaderLegacy // K8s API 直读，无 SoT 切换
	case "logs", "metrics":
		if r.mode == ModeNew {
			return ReaderNew
		}
		return ReaderLegacy
	default:
		// resource/alerts/topology/changes/knowledge：当前默认 legacy 读 CH/MySQL。
		return ReaderLegacy
	}
}

// Mode 返回当前 reader 模式。
func (r *SourceRouter) Mode() ReaderMode { return r.mode }
