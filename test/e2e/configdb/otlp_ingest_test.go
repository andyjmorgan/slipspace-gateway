//go:build e2e

package configdb_test

import (
	"context"
	"crypto/ed25519"
	"io"
	"log/slog"
	"testing"

	collectortrace "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"

	"github.com/andyjmorgan/sluice-gateway/internal/controlplane/configdb"
	"github.com/andyjmorgan/sluice-gateway/internal/controlplane/otlpingest"
	"github.com/andyjmorgan/sluice-gateway/internal/controlplane/receipt"
)

func otlpStr(k, v string) *commonpb.KeyValue {
	return &commonpb.KeyValue{Key: k, Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: v}}}
}

func otlpInt(k string, v int64) *commonpb.KeyValue {
	return &commonpb.KeyValue{Key: k, Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_IntValue{IntValue: v}}}
}

func genAISpan(corr string, tokensOut int64) *tracepb.Span {
	return &tracepb.Span{Attributes: []*commonpb.KeyValue{
		otlpStr("sluice.correlation_id", corr),
		otlpStr("sluice.gateway_id", "gw-1"),
		otlpStr("sluice.configuration", "production"),
		otlpStr("sluice.backend", "openai"),
		otlpStr("gen_ai.request.model", "gpt-4o"),
		otlpStr("sluice.protocol", "chat"),
		otlpInt("gen_ai.usage.input_tokens", 7),
		otlpInt("gen_ai.usage.output_tokens", tokensOut),
		otlpInt("http.response.status_code", 200),
	}}
}

// TestOTLPIngest_EndToEnd drives the OTLP receiver against real Postgres: two
// gen_ai spans become two request_events and a two-link signed receipt chain
// that verifies under the CP's key — the full ingest path through the store.
func TestOTLPIngest_EndToEnd(t *testing.T) {
	ctx := context.Background()
	db, err := configdb.Open(ctx, startPostgres(t))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(db.Close)

	seed := make([]byte, ed25519.SeedSize)
	for i := range seed {
		seed[i] = byte(i + 5)
	}
	signer := receipt.NewEd25519Signer("cp-ed25519", ed25519.NewKeyFromSeed(seed))
	r := otlpingest.NewReceiver(db, signer, slog.New(slog.NewTextHandler(io.Discard, nil)))

	req := &collectortrace.ExportTraceServiceRequest{
		ResourceSpans: []*tracepb.ResourceSpans{{
			Resource: &resourcepb.Resource{},
			ScopeSpans: []*tracepb.ScopeSpans{{
				Spans: []*tracepb.Span{genAISpan("corr-1", 11), genAISpan("corr-2", 22)},
			}},
		}},
	}
	if _, err := r.Export(ctx, req); err != nil {
		t.Fatalf("export: %v", err)
	}

	events, err := db.ListRecentRequestEvents(ctx, 10)
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("events = %d, want 2", len(events))
	}
	if events[0].Model != "gpt-4o" || events[0].Backend != "openai" || events[0].TokensIn != 7 {
		t.Errorf("event = %+v", events[0])
	}

	chain, err := db.ListReceipts(ctx, "gw-1")
	if err != nil {
		t.Fatalf("list receipts: %v", err)
	}
	if len(chain) != 2 {
		t.Fatalf("receipt chain = %d, want 2", len(chain))
	}
	if err := receipt.VerifyChain(chain, signer.Public()); err != nil {
		t.Fatalf("ingested receipt chain failed to verify: %v", err)
	}
}
