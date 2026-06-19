package observability

import (
	"context"
	"fmt"
	"strings"

	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp"
	sdklog "go.opentelemetry.io/otel/sdk/log"
)

// LoggerName is the OpenTelemetry logger scope under which the gateway's
// GenAI events (operation details, exceptions) are emitted. Mirrors
// MeterName / TracerName so all three signals share one scope identity.
const LoggerName = "slipspace-gateway"

// newOTLPLogExporter builds an OTLP log exporter for the given endpoint,
// selecting the transport from protocol. Mirrors newOTLPSpanExporter:
// empty protocol defaults to gRPC, both transports run WithInsecure, an
// unknown protocol is a hard error. The exporter is wrapped in a batch
// processor by the caller; on its own it performs no I/O until records are
// pushed through.
func newOTLPLogExporter(ctx context.Context, endpoint, protocol string) (sdklog.Exporter, error) {
	proto := strings.ToLower(strings.TrimSpace(protocol))
	if proto == "" {
		proto = OTLPProtocolGRPC
	}

	var (
		exp sdklog.Exporter
		err error
	)
	switch proto {
	case OTLPProtocolGRPC:
		opts := []otlploggrpc.Option{otlploggrpc.WithInsecure()}
		if endpoint != "" {
			opts = append(opts, otlploggrpc.WithEndpoint(endpoint))
		}
		exp, err = otlploggrpc.New(ctx, opts...)
	case OTLPProtocolHTTPProtobuf, "http":
		opts := []otlploghttp.Option{otlploghttp.WithInsecure()}
		if endpoint != "" {
			opts = append(opts, otlploghttp.WithEndpoint(endpoint))
		}
		exp, err = otlploghttp.New(ctx, opts...)
	default:
		return nil, fmt.Errorf("observability: unsupported OTLP protocol %q", protocol)
	}
	if err != nil {
		return nil, fmt.Errorf("observability: create OTLP log exporter: %w", err)
	}
	return exp, nil
}
