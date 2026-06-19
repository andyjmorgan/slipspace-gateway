package ingest

import (
	collectormetrics "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	collectortrace "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	"google.golang.org/grpc"
)

// NewOTLPServer builds a gRPC server with the OTLP trace + metrics receivers
// registered. The caller owns the lifecycle (Serve on a listener, GracefulStop
// on shutdown). This is the gen_ai + slipspace telemetry ingest surface; gateways
// export to it directly or via an OTel collector.
func NewOTLPServer(trace *TraceReceiver, metrics *MetricsReceiver, opts ...grpc.ServerOption) *grpc.Server {
	srv := grpc.NewServer(opts...)
	collectortrace.RegisterTraceServiceServer(srv, trace)
	collectormetrics.RegisterMetricsServiceServer(srv, metrics)
	return srv
}
