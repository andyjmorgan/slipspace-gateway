package stitch

import (
	"testing"

	cc "github.com/andyjmorgan/sluice-gateway/contracts/connector"
	"github.com/andyjmorgan/sluice-gateway/internal/telemetry/store"
)

func TestBuildRequestView(t *testing.T) {
	rec := &cc.Record{CorrelationID: "c", Provider: "anthropic"}
	v := BuildRequestView(store.RequestEvent{CorrelationID: "c"}, rec)
	if v.Event.CorrelationID != "c" {
		t.Error("event not set")
	}
	if v.Record == nil || v.Record.Provider != "anthropic" {
		t.Errorf("record not stitched: %+v", v.Record)
	}
	// A nil record (reporting off / not arrived) is omitted.
	v2 := BuildRequestView(store.RequestEvent{CorrelationID: "c"}, nil)
	if v2.Record != nil {
		t.Error("nil record should stay nil")
	}
}

func TestBuildSessionView(t *testing.T) {
	// Token totals come out of each event's span_event blob.
	events := []store.RequestEvent{
		{CorrelationID: "1", StatusCode: 200, SpanEvent: []byte(`{"gen_ai.usage.input_tokens":10,"gen_ai.usage.output_tokens":5}`)},
		{CorrelationID: "2", StatusCode: 500, SpanEvent: []byte(`{"gen_ai.usage.input_tokens":3}`)},
	}
	v := BuildSessionView("sess", events)
	if v.SessionID != "sess" || len(v.Requests) != 2 {
		t.Fatalf("view = %+v", v)
	}
	if v.Totals.Requests != 2 || v.Totals.Errors != 1 {
		t.Errorf("totals = %+v", v.Totals)
	}
	if v.Totals.TokensIn != 13 || v.Totals.TokensOut != 5 {
		t.Errorf("token totals = %+v", v.Totals)
	}
}
