package model

import "time"

// OTLPLogRequest is the OTLP JSON log request
type OTLPLogRequest struct {
	ResourceLogs []struct {
		Resource struct {
			Attributes []struct {
				Key   string      `json:"key"`
				Value interface{} `json:"value"`
			} `json:"attributes"`
		} `json:"resource"`
		ScopeLogs []struct {
			LogRecords []struct {
				TimeUnixNano string `json:"timeUnixNano"`
				SeverityText string `json:"severityText"`
				Body         struct {
					StringValue string `json:"stringValue"`
				} `json:"body"`
				TraceID    string `json:"traceId"`
				SpanID     string `json:"spanId"`
				Attributes []struct {
					Key   string      `json:"key"`
					Value interface{} `json:"value"`
				} `json:"attributes"`
			} `json:"logRecords"`
		} `json:"scopeLogs"`
	} `json:"resourceLogs"`
}

// LogRecord is the internal log record model for ClickHouse
type LogRecord struct {
	TenantID    string
	ClusterID   string
	Timestamp   time.Time
	ServiceName string
	Severity    string
	Body        string
	Attributes  map[string]string
	TraceID     string
	SpanID      string
	TimeBucket  string
	Date        string
}
