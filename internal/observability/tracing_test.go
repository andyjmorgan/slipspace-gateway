package observability_test

import (
	"context"
	"testing"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"github.com/andyjmorgan/sluice-gateway/internal/observability"
)

func TestNewOTLPSpanExporter(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		endpoint string
		protocol string
		wantErr  bool
	}{
		{name: "grpc", endpoint: "127.0.0.1:14317", protocol: "grpc"},
		{name: "http_protobuf", endpoint: "127.0.0.1:14318", protocol: "http/protobuf"},
		{name: "http_bare", endpoint: "127.0.0.1:14318", protocol: "http"},
		{name: "default_empty_defaults_to_grpc", endpoint: "127.0.0.1:14317", protocol: ""},
		{name: "unsupported_protocol_errors", endpoint: "127.0.0.1:14317", protocol: "thrift", wantErr: true},
		// A control character in the gRPC endpoint is rejected
		// synchronously by the exporter constructor, exercising the
		// error-wrap path distinct from the unsupported-protocol guard.
		{name: "exporter_construction_error", endpoint: "\x00", protocol: "grpc", wantErr: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			exp, err := observability.NewOTLPSpanExporterForTest(context.Background(), tc.endpoint, tc.protocol)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error for protocol %q", tc.protocol)
				}
				return
			}
			if err != nil {
				t.Fatalf("newOTLPSpanExporter(%q): %v", tc.protocol, err)
			}
			if exp == nil {
				t.Fatalf("expected non-nil exporter for protocol %q", tc.protocol)
			}
			// Drain the exporter so its background machinery (if any)
			// does not outlive the test. Endpoint is unbound so this
			// returns promptly without a real flush.
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			_ = exp.Shutdown(ctx)
		})
	}
}

func TestSetup_OTLPYieldsRealTracerProvider(t *testing.T) {
	t.Parallel()

	prov, err := observability.Setup(context.Background(), observability.Config{
		OTLPEndpoint: "127.0.0.1:14317",
		OTLPProtocol: "grpc",
		LogFormat:    "json",
		LogLevel:     "info",
	}, build())
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	t.Cleanup(func() { shutdownProv(prov) })

	if prov.TracerProvider == nil {
		t.Fatalf("expected non-nil TracerProvider")
	}
	if _, ok := prov.TracerProvider.(*sdktrace.TracerProvider); !ok {
		t.Fatalf("expected SDK TracerProvider, got %T", prov.TracerProvider)
	}

	tracer := prov.Tracer()
	if tracer == nil {
		t.Fatalf("expected non-nil tracer")
	}
	_, span := tracer.Start(context.Background(), "test-span")
	span.End()
}

func TestSetup_NoOTLPYieldsNoopTracerProvider(t *testing.T) {
	t.Parallel()

	prov, err := observability.Setup(context.Background(), observability.Config{
		LogFormat: "json",
		LogLevel:  "info",
	}, build())
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	t.Cleanup(func() { shutdownProv(prov) })

	if prov.TracerProvider == nil {
		t.Fatalf("expected non-nil TracerProvider even when OTLP disabled")
	}
	if _, ok := prov.TracerProvider.(*sdktrace.TracerProvider); ok {
		t.Fatalf("expected no-op TracerProvider when OTLP disabled, got SDK provider")
	}

	// A span from the no-op provider must be creatable and never record.
	_, span := prov.Tracer().Start(context.Background(), "test-span")
	if span.IsRecording() {
		t.Errorf("no-op span should not record")
	}
	span.End()
}

// TestSetup_TracerProviderShutdownDrainsBatchProcessor exercises the
// Shutdown path for an SDK TracerProvider with a started span in flight,
// so the BatchSpanProcessor's flush + drain runs. Mirrors the
// bounded-deadline shutdown the rest of this package uses against an
// unbound OTLP endpoint (see shutdownProv) rather than asserting goroutine
// counts: the gRPC exporter's dial goroutines outlive the 200ms cap and
// the package deliberately does not run goleak for that reason.
func TestSetup_TracerProviderShutdownDrainsBatchProcessor(t *testing.T) {
	t.Parallel()

	prov, err := observability.Setup(context.Background(), observability.Config{
		OTLPEndpoint: "127.0.0.1:14317",
		OTLPProtocol: "grpc",
		LogFormat:    "json",
		LogLevel:     "info",
	}, build())
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}

	_, span := prov.Tracer().Start(context.Background(), "drain-span")
	span.End()

	shutdownProv(prov)
}

func TestSetup_OTLPHTTPYieldsRealTracerProvider(t *testing.T) {
	t.Parallel()

	prov, err := observability.Setup(context.Background(), observability.Config{
		OTLPEndpoint: "127.0.0.1:14318",
		OTLPProtocol: "http/protobuf",
		LogFormat:    "json",
		LogLevel:     "info",
	}, build())
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	t.Cleanup(func() { shutdownProv(prov) })

	if _, ok := prov.TracerProvider.(*sdktrace.TracerProvider); !ok {
		t.Fatalf("expected SDK TracerProvider for http/protobuf, got %T", prov.TracerProvider)
	}
}
