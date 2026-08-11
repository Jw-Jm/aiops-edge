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
	TimeBucket    time.Time
	CallCount     uint64
	ErrorCount    uint64
	AvgDurationNs uint64
	Date          string
}
