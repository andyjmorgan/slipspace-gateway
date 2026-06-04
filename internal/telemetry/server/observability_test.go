package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	adminc "github.com/andyjmorgan/sluice-gateway/contracts/admin"
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
		Latency:         store.DashboardLatency{P50: 1, P95: 9, P99: 20},
		ByProvider:      []store.DashboardDimensionRow{{Key: "anthropic", Requests: 8, P95LatencyMs: 9, ErrorRate: 0.1}},
		ByEndpoint:      []store.DashboardEndpointRow{{Provider: "anthropic", Endpoint: "messages", Requests: 8}},
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
	if got.Window != "24h" {
		t.Errorf("unknown window should default to 24h, got %q", got.Window)
	}
	hErr := newQueryServer(t, &fakeQueries{summaryErr: errors.New("db")})
	if resp := get(t, hErr, "/api/v1/dashboard/summary", true); resp.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", resp.Code)
	}
}

func TestObsTimeseries(t *testing.T) {
	ts := time.Unix(1000, 0)
	q := &fakeQueries{series: []store.DashboardSeriesBucket{{Ts: ts, Requests: 120, Errored: 12, P95LatencyMs: 42}}}
	h := newQueryServer(t, q)
	got := decodeAdmin[adminc.DashboardTimeseries](t, get(t, h, "/api/v1/dashboard/timeseries?series=p95&window=1h", true))
	if len(got.Series) != 1 || got.Series[0].Name != "p95" || got.Series[0].Unit != "ms" {
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
	q := &fakeQueries{events: []store.RequestEvent{
		{CorrelationID: "new", Backend: "anthropic", Protocol: "messages", StatusCode: 200, Detail: []byte(`{"tags":["x"],"rules_fired":["r1"]}`)},
		{CorrelationID: "old", Backend: "openai"},
	}}
	h := newQueryServer(t, q)
	got := decodeAdmin[adminc.MessagesRecentResponse](t, get(t, h, "/api/v1/messages/recent?limit=50", true))
	if got.Capacity != 50 || len(got.Entries) != 2 {
		t.Fatalf("resp = %+v", got)
	}
	if got.Entries[0].EventID != "old" || got.Entries[1].EventID != "new" {
		t.Errorf("not oldest-first: %s,%s", got.Entries[0].EventID, got.Entries[1].EventID)
	}
	newest := got.Entries[1]
	if newest.Provider != "anthropic" || newest.Endpoint != "messages" {
		t.Errorf("entry mapping = %+v", newest)
	}
	if len(newest.Tags) != 1 || newest.Tags[0] != "x" || len(newest.RulesMatched) != 1 || newest.RulesMatched[0].RuleName != "r1" {
		t.Errorf("detail mapping = %+v", newest)
	}
	// error
	hErr := newQueryServer(t, &fakeQueries{eventsErr: errors.New("db")})
	if resp := get(t, hErr, "/api/v1/messages/recent", true); resp.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", resp.Code)
	}
}

func TestObsMessageBody(t *testing.T) {
	q := &fakeQueries{payloads: []store.Payload{
		{Kind: store.KindRequestBody, Body: []byte(`{"req":1}`)},
		{Kind: store.KindResponseBody, Body: []byte(`{"resp":1}`)},
		{Kind: store.KindSSERollup, Body: []byte(`{"assembled":1}`)},
	}}
	h := newQueryServer(t, q)
	got := decodeAdmin[adminc.MessageBodyDetail](t, get(t, h, "/api/v1/messages/c/body", true))
	if got.Request != `{"req":1}` || got.Response != `{"resp":1}` || got.ResponseAssembled != `{"assembled":1}` {
		t.Errorf("body = %+v", got)
	}
	if got.RequestTotalBytes == 0 || got.ResponseTotalBytes == 0 {
		t.Errorf("byte totals not set: %+v", got)
	}
	// not found
	hNF := newQueryServer(t, &fakeQueries{payErr: store.ErrPayloadNotFound})
	if resp := get(t, hNF, "/api/v1/messages/c/body", true); resp.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.Code)
	}
	// hard error
	hErr := newQueryServer(t, &fakeQueries{payErr: errors.New("db")})
	if resp := get(t, hErr, "/api/v1/messages/c/body", true); resp.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", resp.Code)
	}
}

// --- mapper units ---

func TestSeriesValueAndUnit(t *testing.T) {
	b := store.DashboardSeriesBucket{Requests: 60, Errored: 6, TokensIn: 7, TokensOut: 8, P50LatencyMs: 1, P95LatencyMs: 2, P99LatencyMs: 3}
	cases := map[string]float64{
		"rps":        1, // 60 / 60s
		"error_rate": 0.1,
		"p50":        1,
		"p95":        2,
		"p99":        3,
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
	for name, want := range map[string]string{"rps": "req/s", "error_rate": "%", "p95": "ms", "requests": ""} {
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
