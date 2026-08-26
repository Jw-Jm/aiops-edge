package otlpgrpc

import (
	"context"
	"errors"
	"testing"

	coltrace "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonv1 "go.opentelemetry.io/proto/otlp/common/v1"
	resourcev1 "go.opentelemetry.io/proto/otlp/resource/v1"
	tracev1 "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/observability-platform/ai-apm-ingest-go/internal/model"
	"github.com/observability-platform/ai-apm-ingest-go/internal/pipeline"
)

type captureDurableSink struct {
	spans []*model.Span
	err   error
}

func (s *captureDurableSink) Add(span *model.Span) {
	s.spans = append(s.spans, span)
}

func (s *captureDurableSink) AddBatch(spans []*model.Span) error {
	if s.err != nil {
		return s.err
	}
	s.spans = append(s.spans, spans...)
	return nil
}

func TestExportRejectsMissingTenantMetadata(t *testing.T) {
	sink := &captureDurableSink{}
	p := pipeline.New(sink, nil)
	defer p.Close()
	receiver := NewReceiver(p, "tenant-1")

	_, err := receiver.Export(context.Background(), testRequest())
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("missing tenant metadata code = %s, want %s", status.Code(err), codes.Unauthenticated)
	}
	if len(sink.spans) != 0 {
		t.Fatalf("missing tenant metadata wrote %d spans", len(sink.spans))
	}
}

func TestExportRejectsUnexpectedTenantMetadata(t *testing.T) {
	sink := &captureDurableSink{}
	p := pipeline.New(sink, nil)
	defer p.Close()
	receiver := NewReceiver(p, "tenant-1")
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("x-tenant-id", "tenant-2"))

	_, err := receiver.Export(ctx, testRequest())
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("unexpected tenant metadata code = %s, want %s", status.Code(err), codes.Unauthenticated)
	}
	if len(sink.spans) != 0 {
		t.Fatalf("unexpected tenant metadata wrote %d spans", len(sink.spans))
	}
}

func TestExportConvertsResourceAndSpanAttributes(t *testing.T) {
	sink := &captureDurableSink{}
	p := pipeline.New(sink, nil)
	p.SetClusterID("cluster-1")
	defer p.Close()
	receiver := NewReceiver(p, "tenant-1")
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("x-tenant-id", "tenant-1"))

	resp, err := receiver.Export(ctx, testRequest())
	if err != nil {
		t.Fatalf("Export() error = %v", err)
	}
	if resp == nil {
		t.Fatal("Export() response is nil")
	}
	if len(sink.spans) != 1 {
		t.Fatalf("captured spans = %d, want 1", len(sink.spans))
	}
	span := sink.spans[0]
	if span.TenantID != "tenant-1" || span.ClusterID != "cluster-1" {
		t.Fatalf("identity = tenant=%q cluster=%q, want tenant-1/cluster-1", span.TenantID, span.ClusterID)
	}
	if span.ServiceName != "checkout" || span.ServiceInstanceID != "checkout-1" {
		t.Fatalf("service identity = %q/%q", span.ServiceName, span.ServiceInstanceID)
	}
	if span.K8sNamespace != "observability" || span.K8sPodName != "checkout-abc" {
		t.Fatalf("k8s identity = %q/%q", span.K8sNamespace, span.K8sPodName)
	}
	if span.HTTPMethod != "GET" || span.HTTPURL != "/health" || span.HTTPStatusCode != 500 {
		t.Fatalf("http fields = method=%q url=%q status=%d", span.HTTPMethod, span.HTTPURL, span.HTTPStatusCode)
	}
	if span.IsError != 1 || span.StatusCode != uint8(tracev1.Status_STATUS_CODE_ERROR) {
		t.Fatalf("error fields = is_error=%d status=%d", span.IsError, span.StatusCode)
	}
}

func TestExportPreservesTraceSpanAndParentIDs(t *testing.T) {
	spans, err := ConvertRequest("tenant-1", "cluster-1", testRequest())
	if err != nil {
		t.Fatalf("ConvertRequest() error = %v", err)
	}
	if len(spans) != 1 {
		t.Fatalf("converted spans = %d, want 1", len(spans))
	}
	got := spans[0]
	if got.TraceID != "00112233445566778899aabbccddeeff" {
		t.Fatalf("trace id = %q", got.TraceID)
	}
	if got.SpanID != "0102030405060708" {
		t.Fatalf("span id = %q", got.SpanID)
	}
	if got.ParentSpanID != "1112131415161718" {
		t.Fatalf("parent span id = %q", got.ParentSpanID)
	}
	if got.OperationName != "GET /health" {
		t.Fatalf("operation = %q", got.OperationName)
	}
}

func TestExportReturnsUnavailableWhenSpanSinkFails(t *testing.T) {
	sink := &captureDurableSink{err: errors.New("platform sink unavailable")}
	p := pipeline.New(sink, nil)
	defer p.Close()
	receiver := NewReceiver(p, "tenant-1")
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("x-tenant-id", "tenant-1"))

	_, err := receiver.Export(ctx, testRequest())
	if status.Code(err) != codes.Unavailable {
		t.Fatalf("sink failure code = %s, want %s", status.Code(err), codes.Unavailable)
	}
	if len(sink.spans) != 0 {
		t.Fatalf("failed sink captured %d spans", len(sink.spans))
	}
}

func TestExportRejectsMalformedTimestampsAndIDs(t *testing.T) {
	req := testRequest()
	req.ResourceSpans[0].ScopeSpans[0].Spans[0].TraceId = nil
	if _, err := ConvertRequest("tenant-1", "cluster-1", req); err == nil {
		t.Fatal("ConvertRequest() accepted empty trace id")
	}

	req = testRequest()
	req.ResourceSpans[0].ScopeSpans[0].Spans[0].StartTimeUnixNano = 0
	if _, err := ConvertRequest("tenant-1", "cluster-1", req); err == nil {
		t.Fatal("ConvertRequest() accepted zero start time")
	}
}

func testRequest() *coltrace.ExportTraceServiceRequest {
	return &coltrace.ExportTraceServiceRequest{
		ResourceSpans: []*tracev1.ResourceSpans{{
			Resource: &resourcev1.Resource{Attributes: []*commonv1.KeyValue{
				stringAttr("service.name", "checkout"),
				stringAttr("service.instance.id", "checkout-1"),
				stringAttr("k8s.namespace.name", "observability"),
				stringAttr("k8s.pod.name", "checkout-abc"),
			}},
			ScopeSpans: []*tracev1.ScopeSpans{{Spans: []*tracev1.Span{{
				TraceId:           []byte{0x00, 0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x88, 0x99, 0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff},
				SpanId:            []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08},
				ParentSpanId:      []byte{0x11, 0x12, 0x13, 0x14, 0x15, 0x16, 0x17, 0x18},
				Name:              "GET /health",
				Kind:              tracev1.SpanKind_SPAN_KIND_SERVER,
				StartTimeUnixNano: 1_725_000_000_000_000_000,
				EndTimeUnixNano:   1_725_000_000_250_000_000,
				Attributes: []*commonv1.KeyValue{
					stringAttr("http.method", "GET"),
					stringAttr("http.url", "/health"),
					stringAttr("http.status_code", "500"),
				},
				Status: &tracev1.Status{Code: tracev1.Status_STATUS_CODE_ERROR},
			}}}},
		}},
	}
}

func stringAttr(key, value string) *commonv1.KeyValue {
	return &commonv1.KeyValue{
		Key:   key,
		Value: &commonv1.AnyValue{Value: &commonv1.AnyValue_StringValue{StringValue: value}},
	}
}
