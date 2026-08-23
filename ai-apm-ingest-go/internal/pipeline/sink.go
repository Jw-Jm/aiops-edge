package pipeline

import "github.com/observability-platform/ai-apm-ingest-go/internal/model"

// 中立 sink 接口（V9.3 Phase 14 依赖倒置）。
//
// 背景：此前 ingest 的 span/edge/log 落盘 sink 直接引用 internal/clickhouse 的具体
// 实现类型（*clickhouse.Writer / *clickhouse.MetricsWriter / *clickhouse.LogWriter）。
// 该依赖方向是错的——DeepFlowSyncer 与 Pipeline 只需要"能 Add / AddEdge"的最小能力，
// 不必知道底层是 ClickHouse、VictoriaLogs 还是 no-op。这导致 legacy ClickHouse writer
// 因 DeepFlow 构造参数而无法删除。
//
// 解法：把最小写能力定义为下列接口，放回本包（pipeline 是二者的共同消费方）。任何
// 实现方（ClickHouse 已被移除；未来可为 VLogs/VM 提供实现）只要满足方法集即可注入。
// 传 nil（无 sink）是合法状态，调用方必须按 nil 安全跳过。

// SpanSink 落盘单个 Span。
type SpanSink interface {
	Add(*model.Span)
}

// EdgeSink 落盘一条拓扑边。
type EdgeSink interface {
	AddEdge(*model.TopologyEdge)
}

// LogSink 落盘一条日志记录。
type LogSink interface {
	Add(*model.LogRecord)
}
