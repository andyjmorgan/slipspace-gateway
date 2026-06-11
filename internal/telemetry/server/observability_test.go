package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	adminc "github.com/andyjmorgan/sluice-gateway/contracts/admin"
	cc "github.com/andyjmorgan/sluice-gateway/contracts/connector"
	"github.com/andyjmorgan/sluice-gateway/internal/telemetry/store"
)

func decodeAdmin[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if err := json.NewDecoder(rec.Body).Decode(&v); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return v
}

func TestObsSummary_ParityShape(t *testing.T) {
	q := &fakeQueries{summary: store.DashboardSummary{
		Totals:          store.DashboardTotals{Requests: 10, RequestsErrored: 2, TokensIn: 100, TokensOut: 50},
		ByProvider:      []store.DashboardDimensionRow{{Key: "anthropic", Requests: 8, ErrorRate: 0.1}},
		ByProtocol:      []store.DashboardProtocolRow{{Provider: "anthropic", Protocol: "messages", Requests: 8}},
		ByConfiguration: []store.DashboardDimensionRow{{Key: "default", Requests: 10}},
		ByModel:         []store.DashboardModelRow{{Model: "claude-x", Provider: "anthropic", Requests: 8, TokensIn: 100}},
		RulesFired:      []store.DashboardFiredRow{{Key: "r1", Count: 3, UsedByConfigurations: []string{"default"}}},
		TagsFired:       []store.DashboardFiredRow{{Key: "t1", Count: 4, UsedByConfigurations: []string{"default"}}},
		ProviderHealth:  []store.DashboardProviderHealth{{Provider: "anthropic", Requests: 8, ErrorRate: 0}},
	}}
	h := newQueryServer(t, q)
	resp := get(t, h, "/api/v1/dashboard/summary?window=1h", true)
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d", resp.Code)
	}
	got := decodeAdmin[adminc.DashboardSummary](t, resp)
	if got.Window != "1h" {
		t.Errorf("window = %q", got.Window)
	}
	if got.Totals.Requests != 10 || got.Totals.RequestsErrored != 2 {
		t.Errorf("totals = %+v", got.Totals)
	}
	if got.Rates.ErrorRate != 0.2 {
		t.Errorf("error rate = %v, want 0.2", got.Rates.ErrorRate)
	}
	if len(got.ByProvider) != 1 || got.ByProvider[0].Provider != "anthropic" {
		t.Errorf("by_provider = %+v", got.ByProvider)
	}
	if len(got.RulesFired) != 1 || got.RulesFired[0].RuleName != "r1" || got.RulesFired[0].FireCount != 3 {
		t.Errorf("rules_fired = %+v", got.RulesFired)
	}
	if len(got.TagsFired) != 1 || got.TagsFired[0].Tag != "t1" {
		t.Errorf("tags_fired = %+v", got.TagsFired)
	}
	if len(got.ProviderHealth) != 1 || !got.ProviderHealth[0].Healthy {
		t.Errorf("provider_health = %+v", got.ProviderHealth)
	}
}

func TestObsSummary_DefaultWindowAndError(t *testing.T) {
	h := newQueryServer(t, &fakeQueries{})
	got := decodeAdmin[adminc.DashboardSummary](t, get(t, h, "/api/v1/dashboard/summary?window=bogus", true))
	if got.Window != "1h" {
		t.Errorf("unknown window should default to 1h, got %q", got.Window)
	}
	hErr := newQueryServer(t, &fakeQueries{summaryErr: errors.New("db")})
	if resp := get(t, hErr, "/api/v1/dashboard/summary", true); resp.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", resp.Code)
	}
}

func TestObsTimeseries(t *testing.T) {
	ts := time.Unix(1000, 0)
	q := &fakeQueries{series: []store.DashboardSeriesBucket{{Ts: ts, Requests: 120, Errored: 12, TokensIn: 42}}}
	h := newQueryServer(t, q)
	got := decodeAdmin[adminc.DashboardTimeseries](t, get(t, h, "/api/v1/dashboard/timeseries?series=tokens_in&window=1h", true))
	if len(got.Series) != 1 || got.Series[0].Name != "tokens_in" {
		t.Fatalf("series = %+v", got.Series)
	}
	if len(got.Series[0].Points) != 1 || got.Series[0].Points[0].Value != 42 {
		t.Errorf("points = %+v", got.Series[0].Points)
	}
	// default series name
	gotDef := decodeAdmin[adminc.DashboardTimeseries](t, get(t, h, "/api/v1/dashboard/timeseries", true))
	if gotDef.Series[0].Name != "requests" {
		t.Errorf("default series = %q", gotDef.Series[0].Name)
	}
	// error
	hErr := newQueryServer(t, &fakeQueries{seriesErr: errors.New("db")})
	if resp := get(t, hErr, "/api/v1/dashboard/timeseries", true); resp.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", resp.Code)
	}
}

func TestObsMessagesRecent(t *testing.T) {
	// fake returns newest-first; handler must emit oldest-first.
	// "rich" exercises the full rule chain + attempts from the Record feed;
	// "flat" exercises the rules_fired-names fallback (no rule_chain).
	q := &fakeQueries{events: []store.RequestEvent{
		{CorrelationID: "rich", Provider: "anthropic", Protocol: "messages", StatusCode: 200, SpanEvent: []byte(`{"tags":["x"],"rule_chain":[{"name":"r1","actions_applied":["changeProvider"],"terminated":true}],"attempts":[{"target":"primary","outcome":"success","status_code":200,"started_at_ns":1000000000}]}`)},
		{CorrelationID: "flat", Provider: "openai", SpanEvent: []byte(`{"rules_fired":["r2"]}`)},
	}}
	h := newQueryServer(t, q)
	got := decodeAdmin[adminc.MessagesRecentResponse](t, get(t, h, "/api/v1/messages/recent?limit=50", true))
	if got.Capacity != 50 || len(got.Entries) != 2 {
		t.Fatalf("resp = %+v", got)
	}
	if got.Entries[0].EventID != "flat" || got.Entries[1].EventID != "rich" {
		t.Errorf("not oldest-first: %s,%s", got.Entries[0].EventID, got.Entries[1].EventID)
	}
	rich := got.Entries[1]
	if rich.Provider != "anthropic" || rich.Protocol != "messages" {
		t.Errorf("entry mapping = %+v", rich)
	}
	if len(rich.Tags) != 1 || rich.Tags[0] != "x" {
		t.Errorf("tags = %+v", rich.Tags)
	}
	if len(rich.RulesMatched) != 1 || rich.RulesMatched[0].RuleName != "r1" || !rich.RulesMatched[0].Terminated ||
		len(rich.RulesMatched[0].ActionsApplied) != 1 || rich.RulesMatched[0].ActionsApplied[0] != "changeProvider" {
		t.Errorf("rule chain mapping = %+v", rich.RulesMatched)
	}
	if len(rich.Attempts) != 1 || rich.Attempts[0].Target != "primary" || rich.Attempts[0].Outcome != "success" || rich.Attempts[0].StatusCode != 200 {
		t.Errorf("attempts mapping = %+v", rich.Attempts)
	}
	// Fallback branch: only rule names, no actions/attempts.
	flat := got.Entries[0]
	if len(flat.RulesMatched) != 1 || flat.RulesMatched[0].RuleName != "r2" || flat.RulesMatched[0].Terminated || len(flat.Attempts) != 0 {
		t.Errorf("flat fallback mapping = %+v / attempts %+v", flat.RulesMatched, flat.Attempts)
	}
	// error
	hErr := newQueryServer(t, &fakeQueries{eventsErr: errors.New("db")})
	if resp := get(t, hErr, "/api/v1/messages/recent", true); resp.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", resp.Code)
	}
}

func TestObsMessageBody(t *testing.T) {
	// The bodies/headers/assembled rollup come from the lazily-joined record
	// blob; the gen_ai content comes from the entity's span_event blob.
	rec := cc.Record{
		CorrelationID: "c",
		Request:       cc.RequestPart{Body: json.RawMessage(`{"req":1}`), Headers: map[string]string{"content-type": "application/json"}},
		Response: cc.ResponsePart{
			Body:            json.RawMessage(`{"resp":1}`),
			Assembled:       json.RawMessage(`{"assembled":1}`),
			AssemblyPartial: true,
		},
	}
	recJSON, _ := json.Marshal(rec)
	q := &fakeQueries{
		recordBody: recJSON,
		event:      store.RequestEvent{CorrelationID: "c", SpanEvent: []byte(`{"gen_ai_content":{"input_messages":[]}}`)},
	}
	h := newQueryServer(t, q)
	got := decodeAdmin[adminc.MessageBodyDetail](t, get(t, h, "/api/v1/messages/c/body", true))
	if got.Request != `{"req":1}` || got.Response != `{"resp":1}` || got.ResponseAssembled != `{"assembled":1}` {
		t.Errorf("body = %+v", got)
	}
	// The partial flag is sourced from the record.
	if !got.AssemblyPartial {
		t.Errorf("AssemblyPartial = false, want true (from record)")
	}
	if got.RequestTotalBytes == 0 || got.ResponseTotalBytes == 0 {
		t.Errorf("byte totals not set: %+v", got)
	}
	if len(got.RequestHeaders["content-type"]) != 1 || got.RequestHeaders["content-type"][0] != "application/json" {
		t.Errorf("request headers = %+v", got.RequestHeaders)
	}
	if got.GenAIContent != `{"input_messages":[]}` {
		t.Errorf("gen_ai content = %q", got.GenAIContent)
	}
	// Record present but no entity: still 200 (body renders from the record).
	hRecOnly := newQueryServer(t, &fakeQueries{recordBody: recJSON, eventErr: store.ErrRequestEventNotFound})
	if resp := get(t, hRecOnly, "/api/v1/messages/c/body", true); resp.Code != http.StatusOK {
		t.Fatalf("record-only status = %d, want 200", resp.Code)
	}
	// Neither entity nor record -> 404.
	hNF := newQueryServer(t, &fakeQueries{eventErr: store.ErrRequestEventNotFound, recordErr: store.ErrRecordNotFound})
	if resp := get(t, hNF, "/api/v1/messages/c/body", true); resp.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.Code)
	}
	// Record hard error.
	hErr := newQueryServer(t, &fakeQueries{recordErr: errors.New("db")})
	if resp := get(t, hErr, "/api/v1/messages/c/body", true); resp.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", resp.Code)
	}
}

// TestObsMessageBody_IncludeSplit covers the per-side projections behind the
// console's bridge tabs: ?include=telemetry must never touch the record,
// ?include=report must never read the span blob, and each side 404s on its
// own absence (not the other's presence).
func TestObsMessageBody_IncludeSplit(t *testing.T) {
	rec := cc.Record{
		CorrelationID: "c",
		Request:       cc.RequestPart{Body: json.RawMessage(`{"req":1}`)},
		Response:      cc.ResponsePart{Body: json.RawMessage(`{"resp":1}`)},
	}
	recJSON, _ := json.Marshal(rec)
	event := store.RequestEvent{CorrelationID: "c", SpanEvent: []byte(`{"gen_ai_content":{"input_messages":[]}}`)}

	t.Run("telemetry side skips the record", func(t *testing.T) {
		// recordErr would 500 any record read — proves the handler never asks.
		h := newQueryServer(t, &fakeQueries{event: event, recordErr: errors.New("must not be read")})
		got := decodeAdmin[adminc.MessageBodyDetail](t, get(t, h, "/api/v1/messages/c/body?include=telemetry", true))
		if got.GenAIContent == "" || len(got.SpanEvent) == 0 {
			t.Errorf("telemetry side missing span fields: %+v", got)
		}
		if got.Request != "" || got.Response != "" {
			t.Errorf("telemetry side leaked record fields: %+v", got)
		}
	})
	t.Run("report side skips the span", func(t *testing.T) {
		// eventErr (hard) would 500 any event read — proves the handler never asks.
		h := newQueryServer(t, &fakeQueries{recordBody: recJSON, eventErr: errors.New("must not be read")})
		got := decodeAdmin[adminc.MessageBodyDetail](t, get(t, h, "/api/v1/messages/c/body?include=report", true))
		if got.Request != `{"req":1}` || got.Response != `{"resp":1}` {
			t.Errorf("report side missing record fields: %+v", got)
		}
		if got.GenAIContent != "" || len(got.SpanEvent) != 0 {
			t.Errorf("report side leaked span fields: %+v", got)
		}
	})
	t.Run("telemetry side 404s without an entity even when a record exists", func(t *testing.T) {
		h := newQueryServer(t, &fakeQueries{recordBody: recJSON, eventErr: store.ErrRequestEventNotFound})
		if resp := get(t, h, "/api/v1/messages/c/body?include=telemetry", true); resp.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", resp.Code)
		}
	})
	t.Run("report side 404s without a record even when an entity exists", func(t *testing.T) {
		h := newQueryServer(t, &fakeQueries{event: event, recordErr: store.ErrRecordNotFound})
		if resp := get(t, h, "/api/v1/messages/c/body?include=report", true); resp.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", resp.Code)
		}
	})
}

// --- mapper units ---

// TestMapBodyDecodesEscapedSSE pins the inverse of the gateway's
// jsonBodyOrEscaped: a non-JSON body (an SSE stream) arrives stored as a JSON
// string token and must be decoded back to raw text — real `event:`/`data:`
// lines, not a quoted, escaped blob — so the inspector's Raw stream tab renders
// it. A JSON object body passes through verbatim. Regression guard for the
// telemetry inspector showing SSE wrapped in quotes with literal "\n".
func TestMapBodyDecodesEscapedSSE(t *testing.T) {
	rawSSE := "event: message_start\ndata: {\"type\":\"message_start\"}\n\n"
	// The gateway wraps non-JSON bodies exactly this way before they ride the
	// Record feed's json.RawMessage Body field.
	wrapped, err := json.Marshal(rawSSE)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	rec := &cc.Record{
		Request:  cc.RequestPart{Body: json.RawMessage(`{"model":"x"}`)},
		Response: cc.ResponsePart{Body: json.RawMessage(wrapped)},
	}
	span := []byte(`{"gen_ai.request.model":"x","sluice.configuration":"production"}`)
	got := mapBody("c", rec, nil, span)

	if got.Response != rawSSE {
		t.Errorf("response = %q, want decoded raw SSE %q", got.Response, rawSSE)
	}
	if got.ResponseTotalBytes != int64(len(rawSSE)) {
		t.Errorf("response bytes = %d, want raw length %d", got.ResponseTotalBytes, len(rawSSE))
	}
	// A native JSON body is left untouched (no quote prefix → no unwrap).
	if got.Request != `{"model":"x"}` {
		t.Errorf("request = %q, want JSON body passed through verbatim", got.Request)
	}
	// The complete span rides through verbatim for the raw telemetry pane.
	if got.SpanEvent != string(span) {
		t.Errorf("span_event = %q, want %q", got.SpanEvent, span)
	}
}

func TestSeriesValueAndUnit(t *testing.T) {
	b := store.DashboardSeriesBucket{Requests: 60, Errored: 6, TokensIn: 7, TokensOut: 8}
	cases := map[string]float64{
		"rps":        1, // 60 / 60s
		"error_rate": 0.1,
		"tokens_in":  7,
		"tokens_out": 8,
		"requests":   60,
		"unknown":    60, // default -> requests
	}
	for name, want := range cases {
		if got := seriesValue(b, name, 60); got != want {
			t.Errorf("seriesValue(%q) = %v, want %v", name, got, want)
		}
	}
	if seriesValue(b, "rps", 0) != 0 {
		t.Error("rps with zero bucket -> 0")
	}
	for name, want := range map[string]string{"rps": "req/s", "error_rate": "%", "requests": ""} {
		if got := seriesUnit(name); got != want {
			t.Errorf("seriesUnit(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestBucketForAndRatios(t *testing.T) {
	if bucketFor(time.Hour) != 60 {
		t.Errorf("bucketFor(1h) = %d, want 60", bucketFor(time.Hour))
	}
	if bucketFor(time.Second) != 1 {
		t.Errorf("bucketFor(1s) = %d, want 1 (floor)", bucketFor(time.Second))
	}
	if ratio(0, 0) != 0 || ratio(1, 4) != 0.25 {
		t.Error("ratio")
	}
	if ratePerSecond(10, 0) != 0 || ratePerSecond(10, 2) != 5 {
		t.Error("ratePerSecond")
	}
}
