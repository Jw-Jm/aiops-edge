package otlpgrpc

import (
	"context"
	"fmt"
	"strings"

	coltrace "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/observability-platform/ai-apm-ingest-go/internal/pipeline"
)

// Metrics is the small counter surface needed by the protocol adapter. It is
// intentionally defined here to keep the receiver independent of the metrics
// implementation package.
type Metrics interface {
	IncOTLPGRPCReceived()
	AddOTLPGRPCAccepted(int64)
	IncOTLPGRPCRejected()
	IncOTLPGRPCFailed()
}

// Receiver implements the official OTLP TraceService and forwards only
// authenticated, validated batches to the shared Pipeline.
type Receiver struct {
	coltrace.UnimplementedTraceServiceServer
	pipeline *pipeline.Pipeline
	tenantID string
	metrics  Metrics
}

func NewReceiver(p *pipeline.Pipeline, tenantID string) *Receiver {
	return &Receiver{pipeline: p, tenantID: strings.TrimSpace(tenantID)}
}

func (r *Receiver) SetMetrics(m Metrics) *Receiver {
	r.metrics = m
	return r
}

// Export implements coltrace.TraceServiceServer.Export.
func (r *Receiver) Export(ctx context.Context, req *coltrace.ExportTraceServiceRequest) (*coltrace.ExportTraceServiceResponse, error) {
	if r.metrics != nil {
		r.metrics.IncOTLPGRPCReceived()
	}
	if r.pipeline == nil {
		return nil, status.Error(codes.FailedPrecondition, "pipeline is not configured")
	}
	if r.tenantID == "" {
		return nil, status.Error(codes.FailedPrecondition, "OTLP tenant is not configured")
	}
	if !hasExpectedTenant(ctx, r.tenantID) {
		if r.metrics != nil {
			r.metrics.IncOTLPGRPCRejected()
		}
		return nil, status.Error(codes.Unauthenticated, "invalid x-tenant-id metadata")
	}

	spans, err := ConvertRequest(r.tenantID, r.pipeline.ClusterID(), req)
	if err != nil {
		if r.metrics != nil {
			r.metrics.IncOTLPGRPCRejected()
		}
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	count, err := r.pipeline.ProcessSpans(r.tenantID, spans)
	if err != nil {
		if r.metrics != nil {
			r.metrics.IncOTLPGRPCFailed()
		}
		return nil, status.Error(codes.Unavailable, fmt.Sprintf("platform span sink unavailable: %v", err))
	}
	if r.metrics != nil {
		r.metrics.AddOTLPGRPCAccepted(int64(count))
	}
	return &coltrace.ExportTraceServiceResponse{}, nil
}

func hasExpectedTenant(ctx context.Context, expected string) bool {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return false
	}
	values := md.Get("x-tenant-id")
	return len(values) == 1 && strings.TrimSpace(values[0]) == expected
}
