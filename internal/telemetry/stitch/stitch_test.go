package stitch

import (
	"testing"

	"github.com/andyjmorgan/sluice-gateway/internal/telemetry/store"
)

func TestLatestPayloadsByKind(t *testing.T) {
	if LatestPayloadsByKind(nil) != nil {
		t.Error("nil payloads -> nil map")
	}
	// store returns ascending order; the last per kind wins.
	got := LatestPayloadsByKind([]store.Payload{
		{Kind: store.KindResponseBody, Body: []byte("old"), TsNs: 1},
		{Kind: store.KindRequestBody, Body: []byte("req"), TsNs: 2},
		{Kind: store.KindResponseBody, Body: []byte("new"), TsNs: 3},
	})
	if string(got[store.KindResponseBody]) != "new" {
		t.Errorf("response = %s, want new", got[store.KindResponseBody])
	}
	if string(got[store.KindRequestBody]) != "req" {
		t.Errorf("request = %s", got[store.KindRequestBody])
	}
}

func TestBuildRequestView(t *testing.T) {
	v := BuildRequestView(store.RequestEvent{CorrelationID: "c"}, []store.Payload{{Kind: store.KindRequestBody, Body: []byte("x")}})
	if v.Event.CorrelationID != "c" {
		t.Error("event not set")
	}
	if string(v.Payloads[store.KindRequestBody]) != "x" {
		t.Error("payload not stitched")
	}
}

func TestBuildSessionView(t *testing.T) {
	events := []store.RequestEvent{
		{CorrelationID: "1", StatusCode: 200, TokensIn: 10, TokensOut: 5},
		{CorrelationID: "2", StatusCode: 500, TokensIn: 3, TokensOut: 0},
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
