//go:build e2e

package configdb_test

import (
	"context"
	"crypto/ed25519"
	"io"
	"log/slog"
	"net"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"
	oteltrace "go.opentelemetry.io/otel/trace"
	collectortrace "go.opentelemetry.io/proto/otlp/collector/trace/v1"

	"github.com/andyjmorgan/sluice-gateway/internal/controlplane"
	"github.com/andyjmorgan/sluice-gateway/internal/controlplane/configdb"
	"github.com/andyjmorgan/sluice-gateway/internal/controlplane/otlpingest"
	"github.com/andyjmorgan/sluice-gateway/internal/controlplane/receipt"
	"github.com/andyjmorgan/sluice-gateway/internal/observability"
)

// startCPTraceServer stands up the token-gated fleet gRPC server with the OTLP
// trace receiver registered — the same wiring as cmd/api — on a loopback
// listener, and returns its address. The server is torn down on test cleanup.
func startCPTraceServer(t *testing.T, db *configdb.DB, token string) string {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	seed := make([]byte, ed25519.SeedSize)
	for i := range seed {
		seed[i] = byte(i + 5)
	}
	signer := receipt.NewEd25519Signer("cp-ed25519", ed25519.NewKeyFromSeed(seed))

	srv := controlplane.NewGRPCServer(controlplane.NewDBRegistry(db), logger, controlplane.GRPCServerOptions{Token: token})
	collectortrace.RegisterTraceServiceServer(srv, otlpingest.NewReceiver(db, signer, logger))

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.GracefulStop)
	return lis.Addr().String()
}

// emitGenAISpan records one gen_ai span through prov's tracer carrying the
// attributes the CP receiver turns into a request_event, then flushes the
// exporter so the export completes before the test asserts.
func emitGenAISpan(t *testing.T, prov *observability.Provider, corr string) {
	t.Helper()
	_, span := prov.Tracer().Start(context.Background(), "chat openai", oteltrace.WithSpanKind(oteltrace.SpanKindClient))
	span.SetAttributes(
		attribute.String("sluice.correlation_id", corr),
		attribute.String("sluice.gateway_id", "gw-export"),
		attribute.String("sluice.configuration", "production"),
		attribute.String("sluice.backend", "openai"),
		attribute.String("gen_ai.request.model", "gpt-4o"),
		attribute.String("sluice.protocol", "chat"),
		attribute.Int64("gen_ai.usage.input_tokens", 7),
		attribute.Int64("gen_ai.usage.output_tokens", 11),
		attribute.Int64("http.response.status_code", 200),
	)
	span.End()

	// Shutdown flushes the batch processor synchronously. A flush error is
	// expected and tolerated in the rejected-token case — the assertion that
	// matters is whether a request_event landed, not the export status.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = prov.Shutdown(ctx)
}

// TestOTLPExport_GatewayToControlPlane proves the gateway-side wiring: a fleet
// gateway configured with CPEndpoint + CPToken exports its spans over the
// token-gated fleet gRPC channel, and the CP admits them and writes a
// request_event. This is the transport-level complement to
// TestOTLPIngest_EndToEnd (which drives the receiver in-process).
func TestOTLPExport_GatewayToControlPlane(t *testing.T) {
	ctx := context.Background()
	db, err := configdb.Open(ctx, startPostgres(t))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(db.Close)

	const token = "fleet-bootstrap-token"
	addr := startCPTraceServer(t, db, token)

	prov, err := observability.Setup(ctx, observability.Config{
		CPEndpoint: addr,
		CPToken:    token,
		LogFormat:  "json",
		LogLevel:   "error",
	}, observability.BuildInfo{Service: "gateway", Version: "test"})
	if err != nil {
		t.Fatalf("observability setup: %v", err)
	}

	emitGenAISpan(t, prov, "corr-export-1")

	events, err := db.ListRecentRequestEvents(ctx, 10)
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("events = %d, want 1 (the exported span)", len(events))
	}
	if events[0].CorrelationID != "corr-export-1" || events[0].Model != "gpt-4o" || events[0].Backend != "openai" {
		t.Errorf("exported event = %+v", events[0])
	}
}

// TestOTLPExport_WrongTokenRejected proves the channel is actually gated: an
// exporter presenting the wrong bearer is refused by the CP interceptor, so no
// request_event is written.
func TestOTLPExport_WrongTokenRejected(t *testing.T) {
	ctx := context.Background()
	db, err := configdb.Open(ctx, startPostgres(t))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(db.Close)

	addr := startCPTraceServer(t, db, "the-real-token")

	prov, err := observability.Setup(ctx, observability.Config{
		CPEndpoint: addr,
		CPToken:    "the-wrong-token",
		LogFormat:  "json",
		LogLevel:   "error",
	}, observability.BuildInfo{Service: "gateway", Version: "test"})
	if err != nil {
		t.Fatalf("observability setup: %v", err)
	}

	emitGenAISpan(t, prov, "corr-rejected")

	events, err := db.ListRecentRequestEvents(ctx, 10)
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("events = %d, want 0 (export should be rejected on a bad token)", len(events))
	}
}
