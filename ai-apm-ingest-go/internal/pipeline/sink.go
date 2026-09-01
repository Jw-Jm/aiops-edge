package pipeline

import "github.com/observability-platform/ai-apm-ingest-go/internal/model"

// 中立 sink 接口（V9.3 Phase 14 依赖倒置）。
//
// 背景：此前 ingest 的 span/edge/log 落盘 sink 直接引用 internal/clickhouse 的具体
// 实现类型（*clickhouse.Writer / *clickhouse.MetricsWriter / *clickhouse.LogWriter）。
// 该依赖方向是错的——Pipeline 只需要"能 Add / AddEdge"的最小能力，不必知道底层是
// ClickHouse、VictoriaLogs 还是 no-op。这样平台 Trace SoT 与 DeepFlow OTLP 输入保持解耦。
//
// 解法：把最小写能力定义为下列接口，放回本包（pipeline 是二者的共同消费方）。任何
// 实现方（ClickHouse 已被移除；未来可为 VLogs/VM 提供实现）只要满足方法集即可注入。
// 传 nil（无 sink）是合法状态，调用方必须按 nil 安全跳过。

// SpanSink 落盘单个 Span。平台 ClickHouse 是当前 Trace Persistent SoT。
type SpanSink interface {
	Add(*model.Span)
}

// DurableSpanSink optionally exposes an atomic/error-returning batch path.
// OTLP/gRPC receivers use this interface when present so they do not ACK a
// batch before the platform Span source of truth has accepted it. The legacy
// single-span interface remains for compatibility with lightweight sinks.
type DurableSpanSink interface {
	AddBatch([]*model.Span) error
}

// EdgeSink 落盘一条拓扑边。
type EdgeSink interface {
	AddEdge(*model.TopologyEdge)
}

// DurableEdgeSink optionally exposes an atomic/error-returning batch path. A
// batch sink prevents a single flush from issuing one ClickHouse request per
// edge and lets the caller surface derived-projection failures.
type DurableEdgeSink interface {
	AddEdges([]*model.TopologyEdge) error
}

// LogSink 落盘一条日志记录。
type LogSink interface {
	Add(*model.LogRecord)
}
