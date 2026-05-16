package observability_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"

	"github.com/andyjmorgan/sluice-gateway/internal/observability"
)

func build() observability.BuildInfo {
	return observability.BuildInfo{Service: "sluice-gateway", Version: "v0.0.0-test"}
}

// shutdownProv bounds the OTel provider Shutdown to a short deadline.
// Without this, tests that point the OTLP gRPC exporter at an unbound
// port (`127.0.0.1:14317` in TestSetup_OTLPOnlyGRPC,
// TestSetup_OTLPDefaultsToGRPC, TestSetup_BothExportersCoexist) block
// for the full gRPC reconnect retry window (~10s) on every cleanup —
// trivial individually but compounds under `-count=N` and times out
// `make test`. Applied to every Setup() callsite for consistency; the
// 200ms cap is well under any real Shutdown but well over the
// "already-closed / no in-flight exports" fast path.
func shutdownProv(prov *observability.Provider) {
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	_ = prov.Shutdown(ctx)
}

func TestSetup_NeitherExporterInstallsNoopProvider(t *testing.T) {
	t.Parallel()

	prov, err := observability.Setup(context.Background(), observability.Config{
		LogFormat: "json",
		LogLevel:  "info",
	}, build())
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	t.Cleanup(func() { shutdownProv(prov) })

	if prov.PromHandler != nil {
		t.Errorf("expected nil PromHandler when neither exporter enabled")
	}
	if _, ok := prov.MeterProvider.(*sdkmetric.MeterProvider); ok {
		t.Errorf("expected noop MeterProvider, got sdk provider")
	}
	if prov.Meters == nil {
		t.Fatalf("expected Meters to be installed even on noop path")
	}

	prov.Meters.RequestsTotal.Add(context.Background(), 1)
	prov.Meters.RequestDuration.Record(context.Background(), 0.1)
	prov.Meters.ActiveRequests.Add(context.Background(), 1)
}

func TestSetup_PrometheusOnly(t *testing.T) {
	t.Parallel()

	prov, err := observability.Setup(context.Background(), observability.Config{
		PrometheusBind: "0.0.0.0:9090",
		LogFormat:      "json",
		LogLevel:       "info",
	}, build())
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	t.Cleanup(func() { shutdownProv(prov) })

	if prov.PromHandler == nil {
		t.Fatalf("expected PromHandler when Prometheus enabled")
	}

	prov.Meters.RequestsTotal.Add(context.Background(), 7)

	srv := httptest.NewServer(prov.PromHandler)
	defer srv.Close()
	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatalf("GET /metrics: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	buf := make([]byte, 65536)
	n, _ := resp.Body.Read(buf)
	body := string(buf[:n])
	if !strings.Contains(body, "gateway_requests_total") {
		t.Errorf("metrics body missing gateway_requests_total:\n%s", body)
	}
}

func TestSetup_OTLPOnlyGRPC(t *testing.T) {
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

	if prov.PromHandler != nil {
		t.Errorf("expected nil PromHandler when only OTLP enabled")
	}
	if _, ok := prov.MeterProvider.(*sdkmetric.MeterProvider); !ok {
		t.Errorf("expected SDK MeterProvider, got %T", prov.MeterProvider)
	}
}

func TestSetup_OTLPHTTPProtocol(t *testing.T) {
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
}

func TestSetup_OTLPDefaultsToGRPC(t *testing.T) {
	t.Parallel()

	prov, err := observability.Setup(context.Background(), observability.Config{
		OTLPEndpoint: "127.0.0.1:14317",
		LogFormat:    "json",
		LogLevel:     "info",
	}, build())
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	t.Cleanup(func() { shutdownProv(prov) })
}

func TestSetup_OTLPUnknownProtocolFails(t *testing.T) {
	t.Parallel()

	_, err := observability.Setup(context.Background(), observability.Config{
		OTLPEndpoint: "127.0.0.1:14317",
		OTLPProtocol: "thrift",
		LogFormat:    "json",
		LogLevel:     "info",
	}, build())
	if err == nil {
		t.Fatalf("expected error for unsupported OTLP protocol")
	}
}

func TestSetup_BothExportersCoexist(t *testing.T) {
	t.Parallel()

	prov, err := observability.Setup(context.Background(), observability.Config{
		PrometheusBind: "0.0.0.0:9090",
		OTLPEndpoint:   "127.0.0.1:14317",
		OTLPProtocol:   "grpc",
		LogFormat:      "json",
		LogLevel:       "info",
	}, build())
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	t.Cleanup(func() { shutdownProv(prov) })

	if prov.PromHandler == nil {
		t.Errorf("expected PromHandler when Prometheus enabled")
	}
}

func TestSetup_LoggingErrorPropagates(t *testing.T) {
	t.Parallel()

	_, err := observability.Setup(context.Background(), observability.Config{
		LogFormat: "json",
		LogLevel:  "shouty",
	}, build())
	if err == nil {
		t.Fatalf("expected error from bad log level")
	}
}

func TestSetup_DeploymentEnvironmentAttribute(t *testing.T) {
	t.Setenv(observability.EnvDeploymentEnvironment, "staging")

	prov, err := observability.Setup(context.Background(), observability.Config{
		PrometheusBind: "0.0.0.0:9090",
		LogFormat:      "json",
		LogLevel:       "info",
	}, build())
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	t.Cleanup(func() { shutdownProv(prov) })
}

func TestProvider_ShutdownIsIdempotent(t *testing.T) {
	t.Parallel()

	prov, err := observability.Setup(context.Background(), observability.Config{
		PrometheusBind: "0.0.0.0:9090",
		LogFormat:      "json",
		LogLevel:       "info",
	}, build())
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}

	if err := prov.Shutdown(context.Background()); err != nil {
		t.Errorf("first Shutdown: %v", err)
	}
	if err := prov.Shutdown(context.Background()); err != nil {
		t.Errorf("second Shutdown: %v", err)
	}
}

// TestProvider_ShutdownCachesResult covers the idempotency contract for
// the path that includes an OTLP exporter whose collector is unreachable:
// the underlying SDK shutdown legitimately returns an error, and the
// idempotent wrapper must surface the same error on every subsequent
// call instead of redoing the work.
func TestProvider_ShutdownCachesResult(t *testing.T) {
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

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	first := prov.Shutdown(ctx)
	second := prov.Shutdown(context.Background())
	if (first == nil) != (second == nil) {
		t.Errorf("Shutdown not idempotent: first=%v second=%v", first, second)
	}
	if first != nil && second != nil && first.Error() != second.Error() {
		t.Errorf("Shutdown result not cached: first=%q second=%q", first.Error(), second.Error())
	}
}

func TestProvider_NoopShutdownIsIdempotent(t *testing.T) {
	t.Parallel()

	prov, err := observability.Setup(context.Background(), observability.Config{
		LogFormat: "json",
		LogLevel:  "info",
	}, build())
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}

	if err := prov.Shutdown(context.Background()); err != nil {
		t.Errorf("first Shutdown: %v", err)
	}
	if err := prov.Shutdown(context.Background()); err != nil {
		t.Errorf("second Shutdown: %v", err)
	}
}
