package otlpingest

import (
	"context"
	"crypto/ed25519"
	"errors"
	"io"
	"log/slog"
	"testing"

	collectortrace "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"

	"github.com/andyjmorgan/sluice-gateway/internal/controlplane/configdb"
	"github.com/andyjmorgan/sluice-gateway/internal/controlplane/receipt"
)

type fakeEventStore struct {
	events    []configdb.RequestEvent
	records   []receipt.Record
	upsertErr error
	appendErr error
}

func (f *fakeEventStore) UpsertRequestEvent(_ context.Context, e configdb.RequestEvent) error {
	if f.upsertErr != nil {
		return f.upsertErr
	}
	f.events = append(f.events, e)
	return nil
}

func (f *fakeEventStore) AppendReceipt(_ context.Context, rec receipt.Record, _ receipt.Signer) (receipt.Receipt, error) {
	if f.appendErr != nil {
		return receipt.Receipt{}, f.appendErr
	}
	f.records = append(f.records, rec)
	return receipt.Receipt{}, nil
}

func testReceiver(store eventStore) *Receiver {
	seed := make([]byte, ed25519.SeedSize)
	signer := receipt.NewEd25519Signer("k", ed25519.NewKeyFromSeed(seed))
	return NewReceiver(store, signer, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func exportReq(resourceAttrs []*commonpb.KeyValue, spans ...*tracepb.Span) *collectortrace.ExportTraceServiceRequest {
	return &collectortrace.ExportTraceServiceRequest{
		ResourceSpans: []*tracepb.ResourceSpans{{
			Resource:   &resourcepb.Resource{Attributes: resourceAttrs},
			ScopeSpans: []*tracepb.ScopeSpans{{Spans: spans}},
		}},
	}
}

func TestReceiver_Export_StoresEventsAndReceipts(t *testing.T) {
	store := &fakeEventStore{}
	r := testReceiver(store)

	full := &tracepb.Span{Attributes: []*commonpb.KeyValue{
		kvStr(attrCorrelationID, "corr-a"),
		kvStr(attrGatewayID, "gw-1"),
		kvStr(attrModel, "gpt-4o"),
		kvInt(attrOutputTokens, 5),
	}}
	noCorr := &tracepb.Span{Attributes: []*commonpb.KeyValue{kvStr(attrModel, "gpt-4o")}}
	noGateway := &tracepb.Span{Attributes: []*commonpb.KeyValue{kvStr(attrCorrelationID, "corr-c")}}

	if _, err := r.Export(context.Background(), exportReq(nil, full, noCorr, noGateway)); err != nil {
		t.Fatalf("Export: %v", err)
	}

	// Events: the two with a correlation id (full + noGateway); not the no-corr.
	if len(store.events) != 2 {
		t.Fatalf("events = %d, want 2", len(store.events))
	}
	if store.events[0].CorrelationID != "corr-a" || store.events[1].CorrelationID != "corr-c" {
		t.Errorf("events = %+v", store.events)
	}
	// Receipts: only the one with a gateway identity.
	if len(store.records) != 1 || store.records[0].GatewayID != "gw-1" || store.records[0].CorrelationID != "corr-a" {
		t.Fatalf("records = %+v", store.records)
	}
	if len(store.records[0].Payload) == 0 {
		t.Error("receipt payload is empty")
	}
}

func TestReceiver_Export_UpsertErrorSkipsReceipt(t *testing.T) {
	store := &fakeEventStore{upsertErr: errors.New("db down")}
	r := testReceiver(store)

	span := &tracepb.Span{Attributes: []*commonpb.KeyValue{
		kvStr(attrCorrelationID, "c"),
		kvStr(attrGatewayID, "gw-1"),
	}}
	if _, err := r.Export(context.Background(), exportReq(nil, span)); err != nil {
		t.Fatalf("Export: %v", err)
	}
	if len(store.records) != 0 {
		t.Error("receipt appended despite upsert failure")
	}
}

func TestReceiver_Export_AppendErrorIsTolerated(t *testing.T) {
	store := &fakeEventStore{appendErr: errors.New("chain locked")}
	r := testReceiver(store)

	span := &tracepb.Span{Attributes: []*commonpb.KeyValue{
		kvStr(attrCorrelationID, "c"),
		kvStr(attrGatewayID, "gw-1"),
	}}
	// A receipt failure must not fail the export — the event is still stored.
	if _, err := r.Export(context.Background(), exportReq(nil, span)); err != nil {
		t.Fatalf("Export: %v", err)
	}
	if len(store.events) != 1 {
		t.Errorf("event not stored when receipt append failed: %d", len(store.events))
	}
}
