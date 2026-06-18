package observability_test

import (
	"context"
	"testing"

	sdklog "go.opentelemetry.io/otel/sdk/log"

	"github.com/andyjmorgan/slipspace-gateway/internal/observability"
)

func TestNewOTLPLogExporter(t *testing.T) {
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
		{name: "exporter_construction_error", endpoint: "\x00", protocol: "grpc", wantErr: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			exp, err := observability.NewOTLPLogExporterForTest(context.Background(), tc.endpoint, tc.protocol)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error for protocol %q", tc.protocol)
				}
				return
			}
			if err != nil {
				t.Fatalf("newOTLPLogExporter(%q): %v", tc.protocol, err)
			}
			if exp == nil {
				t.Fatalf("expected non-nil exporter for protocol %q", tc.protocol)
			}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			_ = exp.Shutdown(ctx)
		})
	}
}

func TestSetup_OTLPYieldsRealLoggerProvider(t *testing.T) {
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

	if prov.LoggerProvider == nil {
		t.Fatalf("expected non-nil LoggerProvider")
	}
	if _, ok := prov.LoggerProvider.(*sdklog.LoggerProvider); !ok {
		t.Fatalf("expected SDK LoggerProvider, got %T", prov.LoggerProvider)
	}
	if prov.EventLogger() == nil {
		t.Fatalf("expected non-nil EventLogger")
	}
}

func TestSetup_NoOTLPYieldsNoopLoggerProvider(t *testing.T) {
	t.Parallel()

	prov, err := observability.Setup(context.Background(), observability.Config{
		LogFormat: "json",
		LogLevel:  "info",
	}, build())
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	t.Cleanup(func() { shutdownProv(prov) })

	if prov.LoggerProvider == nil {
		t.Fatalf("expected non-nil (no-op) LoggerProvider")
	}
	if _, ok := prov.LoggerProvider.(*sdklog.LoggerProvider); ok {
		t.Fatalf("expected a no-op LoggerProvider when OTLP is disabled, got the SDK provider")
	}
	// The no-op logger is still safe to use.
	if prov.EventLogger() == nil {
		t.Fatalf("expected non-nil EventLogger even with OTLP disabled")
	}
}
