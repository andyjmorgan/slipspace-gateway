package arbiter

import (
	"context"
	"testing"
	"time"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/andyjmorgan/slipspace-gateway/internal/arbiter/config"
	"github.com/andyjmorgan/slipspace-gateway/internal/arbiter/store"
)

func TestOTelEmitter_EmitVerdict(t *testing.T) {
	exp := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exp))
	e := newOTelEmitter(tp)

	e.EmitVerdict(context.Background(), "corr-9", store.Verdict{
		State: StateFlagged, MaxScore: 0.91, TopCategory: "injection.jailbreak",
		FindingCount: 2, Inconclusive: []string{"toxicity"},
	})

	spans := exp.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("emitted %d spans, want 1", len(spans))
	}
	got := map[string]any{}
	for _, a := range spans[0].Attributes {
		got[string(a.Key)] = a.Value.AsInterface()
	}
	if got[AttrEnriched] != true {
		t.Errorf("enriched flag = %v, want true", got[AttrEnriched])
	}
	if got["slipspace.correlation_id"] != "corr-9" {
		t.Errorf("correlation_id = %v", got["slipspace.correlation_id"])
	}
	if got["slipspace.security.verdict"] != StateFlagged {
		t.Errorf("verdict = %v", got["slipspace.security.verdict"])
	}
	if got["slipspace.security.top_category"] != "injection.jailbreak" {
		t.Errorf("top_category = %v", got["slipspace.security.top_category"])
	}
	e.shutdown(context.Background())
}

func TestNoopEmitter(t *testing.T) {
	var e verdictEmitter = noopEmitter{}
	e.EmitVerdict(context.Background(), "c", store.Verdict{}) // no panic
	e.shutdown(context.Background())
}

func TestNewEmitter(t *testing.T) {
	// disabled -> noop
	em, err := newEmitter(context.Background(), config.EnrichedExport{})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := em.(noopEmitter); !ok {
		t.Errorf("empty endpoint should yield noopEmitter, got %T", em)
	}
	// grpc + http build a real emitter (lazy dial; constructing does no I/O).
	for _, proto := range []string{"", "grpc", "http"} {
		em, err := newEmitter(context.Background(), config.EnrichedExport{Endpoint: "localhost:4317", Protocol: proto})
		if err != nil {
			t.Fatalf("proto %q: %v", proto, err)
		}
		if _, ok := em.(*otelEmitter); !ok {
			t.Errorf("proto %q: want *otelEmitter, got %T", proto, em)
		}
		sctx, cancel := context.WithTimeout(context.Background(), time.Second)
		em.shutdown(sctx)
		cancel()
	}
	// bad protocol -> error
	if _, err := newEmitter(context.Background(), config.EnrichedExport{Endpoint: "x", Protocol: "carrier-pigeon"}); err == nil {
		t.Error("want error for unsupported protocol")
	}
}

// capEmitter records EmitVerdict calls for the reduce-path test.
type capEmitter struct {
	calls int
	corr  string
	last  store.Verdict
}

func (c *capEmitter) EmitVerdict(_ context.Context, corr string, v store.Verdict) {
	c.calls++
	c.corr = corr
	c.last = v
}
func (c *capEmitter) shutdown(context.Context) {}

func TestReduce_EmitsVerdict(t *testing.T) {
	ctx := context.Background()
	fake := newFakeStore()
	fake.findings = append(fake.findings, store.Finding{CorrelationID: "c", Category: "pii.email", Score: 0.8})
	s := testScanner(fake, "")
	ce := &capEmitter{}
	s.emitter = ce

	s.reduce(ctx, "c")

	if ce.calls != 1 || ce.corr != "c" {
		t.Fatalf("emit calls = %d, corr = %q", ce.calls, ce.corr)
	}
	if ce.last.State != StateFlagged {
		t.Errorf("emitted verdict state = %q, want flagged", ce.last.State)
	}
}

func TestReduce_NoEmitOnUpsertError(t *testing.T) {
	fake := newFakeStore()
	s := testScanner(fake, "")
	ce := &capEmitter{}
	s.emitter = ce
	s.store = errStore{fakeStore: fake, failList: false} // list ok, but make upsert fail:
	// use a store whose UpsertVerdict errors
	s.store = upsertErrStore{fake}
	s.reduce(context.Background(), "c")
	if ce.calls != 0 {
		t.Errorf("emit called %d times despite upsert error", ce.calls)
	}
}

type upsertErrStore struct{ *fakeStore }

func (u upsertErrStore) UpsertVerdict(context.Context, store.Verdict) error {
	return context.DeadlineExceeded
}
