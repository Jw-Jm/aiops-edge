package model

import "time"

// Span is the internal span model
type Span struct {
	TenantID      string            `json:"tenant_id"`
	TraceID       string            `json:"trace_id"`
	SpanID        string            `json:"span_id"`
	ParentSpanID  string            `json:"parent_span_id"`
	ServiceName   string            `json:"service_name"`
	OperationName string            `json:"operation_name"`
	SpanKind      string            `json:"span_kind"`
	StatusCode    uint8             `json:"status_code"`
	StartTime     time.Time         `json:"start_time"`
	DurationNs    uint64            `json:"duration_ns"`
	Attributes    map[string]string `json:"attributes"`

	HTTPMethod     string `json:"http_method,omitempty"`
	HTTPStatusCode uint16 `json:"http_status_code,omitempty"`
	HTTPURL        string `json:"http_url,omitempty"`

	DBSystem    string `json:"db_system,omitempty"`
	DBStatement string `json:"db_statement,omitempty"`

	RPCSystem string `json:"rpc_system,omitempty"`

	ServiceInstanceID string `json:"service_instance_id,omitempty"`
	K8sNamespace      string `json:"k8s_namespace,omitempty"`
	K8sPodName        string `json:"k8s_pod_name,omitempty"`

	IsSlow  uint8 `json:"is_slow"`
	IsError uint8 `json:"is_error"`
}

// OTLPTraceRequest is the OTLP JSON trace request
type OTLPTraceRequest struct {
	ResourceSpans []struct {
		Resource struct {
			Attributes []struct {
				Key   string      `json:"key"`
				Value interface{} `json:"value"`
			} `json:"attributes"`
		} `json:"resource"`
		ScopeSpans []struct {
			Spans []struct {
				TraceID       string `json:"traceId"`
				SpanID        string `json:"spanId"`
				ParentSpanID  string `json:"parentSpanId"`
				Name          string `json:"name"`
				Kind          int    `json:"kind"`
				StartTimeUnix string `json:"startTimeUnixNano"`
				EndTimeUnix   string `json:"endTimeUnixNano"`
				Status        struct {
					Code int `json:"code"`
				} `json:"status"`
				Attributes []struct {
					Key   string      `json:"key"`
					Value interface{} `json:"value"`
				} `json:"attributes"`
			} `json:"spans"`
		} `json:"scopeSpans"`
	} `json:"resourceSpans"`
}

// KindMap maps OTel span kind integers to strings
var KindMap = map[int]string{
	0: "INTERNAL",
	1: "SERVER",
	2: "CLIENT",
	3: "PRODUCER",
	4: "CONSUMER",
}
