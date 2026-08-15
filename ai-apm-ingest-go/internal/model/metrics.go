package model

import "time"

// ServiceMetric represents RED metrics for a service within a time bucket
type ServiceMetric struct {
	TenantID      string
	ClusterID     string
	ServiceName   string
	CallerService string
	TimeBucket    time.Time
	CallCount     uint64
	ErrorCount    uint64
	DurationSumNs uint64
	DurationCount uint64
	Date          string
}

// TopologyEdge represents a call edge between two services within a time bucket
type TopologyEdge struct {
	TenantID      string
	ClusterID     string
	SourceService string
	TargetService string
	// SourceNamespace / TargetNamespace 为源/目标服务的 K8s namespace（从 deepflow
	// pod_ns_map 映射提取）。service_topology 表当前无 ns 列，仅供内存/未来扩展使用。
	SourceNamespace string
	TargetNamespace string
	TimeBucket      time.Time
	CallCount       uint64
	ErrorCount      uint64
	AvgDurationNs   uint64
	Date            string
}
