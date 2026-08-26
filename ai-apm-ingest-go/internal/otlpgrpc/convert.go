package otlpgrpc

import (
	"encoding/hex"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	commonv1 "go.opentelemetry.io/proto/otlp/common/v1"
	tracev1 "go.opentelemetry.io/proto/otlp/trace/v1"

	"github.com/observability-platform/ai-apm-ingest-go/internal/model"
)

// ConvertRequest converts the official OTLP protobuf representation into the
// platform's internal model. It deliberately contains no persistence logic so
// HTTP JSON and gRPC receivers can share the same durable Pipeline path.
func ConvertRequest(tenantID, clusterID string, req interface {
	GetResourceSpans() []*tracev1.ResourceSpans
}) ([]*model.Span, error) {
	if strings.TrimSpace(tenantID) == "" {
		return nil, fmt.Errorf("tenant id is required")
	}
	if strings.TrimSpace(clusterID) == "" {
		return nil, fmt.Errorf("cluster id is required")
	}
	if req == nil {
		return nil, fmt.Errorf("request is nil")
	}

	var out []*model.Span
	for _, resourceSpans := range req.GetResourceSpans() {
		if resourceSpans == nil {
			continue
		}
		resourceAttrs := attributeMap(nil)
		if resourceSpans.Resource != nil {
			resourceAttrs = attributeMap(resourceSpans.Resource.Attributes)
		}
		serviceName := resourceAttrs["service.name"]
		if serviceName == "" {
			serviceName = "unknown"
		}

		for _, scopeSpans := range resourceSpans.ScopeSpans {
			if scopeSpans == nil {
				continue
			}
			for _, raw := range scopeSpans.Spans {
				span, err := convertSpan(tenantID, clusterID, serviceName, resourceAttrs, raw)
				if err != nil {
					return nil, err
				}
				out = append(out, span)
			}
		}
	}
	return out, nil
}

func convertSpan(tenantID, clusterID, serviceName string, resourceAttrs map[string]string, raw *tracev1.Span) (*model.Span, error) {
	if raw == nil {
		return nil, fmt.Errorf("nil span")
	}
	if len(raw.TraceId) != 16 {
		return nil, fmt.Errorf("trace id must be 16 bytes, got %d", len(raw.TraceId))
	}
	if len(raw.SpanId) != 8 {
		return nil, fmt.Errorf("span id must be 8 bytes, got %d", len(raw.SpanId))
	}
	if len(raw.ParentSpanId) != 0 && len(raw.ParentSpanId) != 8 {
		return nil, fmt.Errorf("parent span id must be empty or 8 bytes, got %d", len(raw.ParentSpanId))
	}
	if raw.StartTimeUnixNano == 0 || raw.StartTimeUnixNano > math.MaxInt64 {
		return nil, fmt.Errorf("invalid start timestamp %d", raw.StartTimeUnixNano)
	}
	if raw.EndTimeUnixNano > math.MaxInt64 {
		return nil, fmt.Errorf("invalid end timestamp %d", raw.EndTimeUnixNano)
	}
	if raw.EndTimeUnixNano != 0 && raw.EndTimeUnixNano < raw.StartTimeUnixNano {
		return nil, fmt.Errorf("end timestamp precedes start timestamp")
	}

	spanAttrs := attributeMap(raw.Attributes)
	merged := make(map[string]string, len(resourceAttrs)+len(spanAttrs))
	for key, value := range resourceAttrs {
		merged[key] = value
	}
	for key, value := range spanAttrs {
		merged[key] = value
	}

	statusCode := tracev1.Status_STATUS_CODE_UNSET
	if raw.Status != nil {
		statusCode = raw.Status.Code
	}
	durationNs := uint64(0)
	if raw.EndTimeUnixNano > raw.StartTimeUnixNano {
		durationNs = raw.EndTimeUnixNano - raw.StartTimeUnixNano
	}

	span := &model.Span{
		TenantID:          tenantID,
		ClusterID:         clusterID,
		TraceID:           hex.EncodeToString(raw.TraceId),
		SpanID:            hex.EncodeToString(raw.SpanId),
		ParentSpanID:      hex.EncodeToString(raw.ParentSpanId),
		ServiceName:       serviceName,
		OperationName:     raw.Name,
		SpanKind:          spanKindName(raw.Kind),
		StatusCode:        uint8(statusCode),
		StartTime:         time.Unix(0, int64(raw.StartTimeUnixNano)).UTC(),
		DurationNs:        durationNs,
		Attributes:        merged,
		HTTPMethod:        merged["http.method"],
		HTTPURL:           merged["http.url"],
		DBSystem:          merged["db.system"],
		DBStatement:       merged["db.statement"],
		RPCSystem:         merged["rpc.system"],
		ServiceInstanceID: merged["service.instance.id"],
		K8sNamespace:      merged["k8s.namespace.name"],
		K8sPodName:        merged["k8s.pod.name"],
		IsSlow:            0,
		IsError:           0,
	}
	if statusCode == tracev1.Status_STATUS_CODE_ERROR {
		span.IsError = 1
	}
	if durationNs > 500_000_000 {
		span.IsSlow = 1
	}
	if httpCode, err := strconv.Atoi(merged["http.status_code"]); err == nil && httpCode > 0 {
		span.HTTPStatusCode = uint16(httpCode)
		if httpCode >= 400 {
			span.IsError = 1
		}
	}
	return span, nil
}

func spanKindName(kind tracev1.Span_SpanKind) string {
	switch kind {
	case tracev1.Span_SPAN_KIND_SERVER:
		return "SERVER"
	case tracev1.Span_SPAN_KIND_CLIENT:
		return "CLIENT"
	case tracev1.Span_SPAN_KIND_PRODUCER:
		return "PRODUCER"
	case tracev1.Span_SPAN_KIND_CONSUMER:
		return "CONSUMER"
	default:
		return "INTERNAL"
	}
}

func attributeMap(attrs []*commonv1.KeyValue) map[string]string {
	result := make(map[string]string, len(attrs))
	for _, attr := range attrs {
		if attr == nil || attr.Key == "" {
			continue
		}
		if value := anyValueString(attr.Value); value != "" {
			result[attr.Key] = value
		}
	}
	return result
}

func anyValueString(value *commonv1.AnyValue) string {
	if value == nil {
		return ""
	}
	switch v := value.Value.(type) {
	case *commonv1.AnyValue_StringValue:
		return v.StringValue
	case *commonv1.AnyValue_BoolValue:
		return strconv.FormatBool(v.BoolValue)
	case *commonv1.AnyValue_IntValue:
		return strconv.FormatInt(v.IntValue, 10)
	case *commonv1.AnyValue_DoubleValue:
		return strconv.FormatFloat(v.DoubleValue, 'g', -1, 64)
	case *commonv1.AnyValue_BytesValue:
		return hex.EncodeToString(v.BytesValue)
	default:
		return ""
	}
}
