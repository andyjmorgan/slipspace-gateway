package controlplane

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	adminc "github.com/andyjmorgan/sluice-gateway/contracts/admin"
	connc "github.com/andyjmorgan/sluice-gateway/contracts/connector"
	"github.com/andyjmorgan/sluice-gateway/internal/controlplane/configdb"
)

// fakeRichReader satisfies both eventReader and richReader so it can be passed
// as the store to NewObservabilityHandler and light up the rich routes.
type fakeRichReader struct {
	fakeEventReader

	summary    configdb.DashboardSummary
	summaryErr error
	lastSummP  configdb.DashboardParams

	buckets     []configdb.DashboardSeriesBucket
	bucketsErr  error
	lastSeriesP configdb.DashboardSeriesParams
}

func (f *fakeRichReader) QueryDashboardSummary(_ context.Context, p configdb.DashboardParams) (configdb.DashboardSummary, error) {
	f.lastSummP = p
	return f.summary, f.summaryErr
}

func (f *fakeRichReader) QueryDashboardSeries(_ context.Context, p configdb.DashboardSeriesParams) ([]configdb.DashboardSeriesBucket, error) {
	f.lastSeriesP = p
	return f.buckets, f.bucketsErr
}

// fakeRecordReader satisfies both bodyReader and recordReader.
type fakeRecordReader struct {
	fakeBodyReader

	record    json.RawMessage
	recordErr error
}

func (f *fakeRecordReader) GetRequestRecord(context.Context, string) (json.RawMessage, error) {
	return f.record, f.recordErr
}

func TestParseWindow(t *testing.T) {
	cases := []struct {
		raw     string
		wantLbl string
		wantOK  bool
	}{
		{"", "24h", true},
		{"1h", "1h", true},
		{"7d", "7d", true},
		{"30d", "30d", true},
		{"99y", "", false},
	}
	for _, tc := range cases {
		lbl, _, ok := parseWindow(tc.raw)
		if ok != tc.wantOK || lbl != tc.wantLbl {
			t.Errorf("parseWindow(%q) = (%q,%v), want (%q,%v)", tc.raw, lbl, ok, tc.wantLbl, tc.wantOK)
		}
	}
}

func TestDashboardSummary_Shape(t *testing.T) {
	store := &fakeRichReader{summary: configdb.DashboardSummary{
		Totals: configdb.DashboardTotals{
			Requests: 100, RequestsSuccess: 90, RequestsErrored: 10,
			TokensIn: 1000, TokensOut: 2000, TokensCached: 50, TokensCacheCreation: 5,
		},
		Latency:         configdb.DashboardLatency{P50: 100, P95: 200, P99: 300},
		ByProvider:      []configdb.DashboardDimensionRow{{Key: "openai", Requests: 60, P95LatencyMs: 150, ErrorRate: 0.1}},
		ByEndpoint:      []configdb.DashboardEndpointRow{{Provider: "openai", Endpoint: "chat", Requests: 60, P95LatencyMs: 150}},
		ByConfiguration: []configdb.DashboardDimensionRow{{Key: "production", Requests: 100}},
		ByModel:         []configdb.DashboardModelRow{{Model: "gpt-4o", Provider: "openai", Requests: 60, TokensIn: 1000, TokensOut: 2000}},
		RulesFired:      []configdb.DashboardFiredRow{{Key: "redirect", Count: 7, UsedByConfigurations: []string{"production"}}},
		TagsFired:       []configdb.DashboardFiredRow{{Key: "env:prod", Count: 12, UsedByConfigurations: []string{"production"}}},
		ProviderHealth: []configdb.DashboardProviderHealth{
			{Provider: "openai", Requests: 20, ErrorRate: 0.02},
			{Provider: "anthropic", Requests: 5, ErrorRate: 0.4},
		},
	}}
	h := NewObservabilityHandler(store, nil, nil, nil)

	rec := obsReq(h, "/api/v1/observability/dashboard/summary?window=1h&backend=openai")
	if rec.Code != http.StatusOK {
		t.Fatalf("= %d, want 200: %s", rec.Code, rec.Body)
	}
	if store.lastSummP.Filter.Backend != "openai" {
		t.Errorf("filter not passed through: %+v", store.lastSummP.Filter)
	}
	if store.lastSummP.From.IsZero() || store.lastSummP.To.IsZero() || store.lastSummP.RecentFrom.IsZero() {
		t.Errorf("window not derived: %+v", store.lastSummP)
	}

	var s adminc.DashboardSummary
	if err := json.NewDecoder(rec.Body).Decode(&s); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if s.Window != "1h" {
		t.Errorf("window = %q, want 1h", s.Window)
	}
	if s.Totals.Requests != 100 || s.Totals.TokensCached != 50 {
		t.Errorf("totals = %+v", s.Totals)
	}
	wantRate := 10.0 / 100.0
	if s.Rates.ErrorRate != wantRate {
		t.Errorf("error_rate = %v, want %v", s.Rates.ErrorRate, wantRate)
	}
	// rps over a 1h window = 100/3600.
	if s.Rates.RequestsPerSecond <= 0 {
		t.Errorf("rps = %v, want > 0", s.Rates.RequestsPerSecond)
	}
	if s.LatencyMs.P50 != 100 || s.LatencyMs.P99 != 300 {
		t.Errorf("latency = %+v", s.LatencyMs)
	}
	if len(s.ByProvider) != 1 || s.ByProvider[0].Provider != "openai" {
		t.Errorf("by_provider = %+v", s.ByProvider)
	}
	if len(s.ByConfiguration) != 1 || s.ByConfiguration[0].Configuration != "production" {
		t.Errorf("by_configuration = %+v", s.ByConfiguration)
	}
	if len(s.RulesFired) != 1 || s.RulesFired[0].RuleName != "redirect" || s.RulesFired[0].FireCount != 7 {
		t.Errorf("rules_fired = %+v", s.RulesFired)
	}
	if len(s.TagsFired) != 1 || s.TagsFired[0].Tag != "env:prod" || s.TagsFired[0].ApplyCount != 12 {
		t.Errorf("tags_fired = %+v", s.TagsFired)
	}
	// health: openai 2% -> healthy; anthropic 40% -> unhealthy.
	byProv := map[string]adminc.DashboardProviderHealth{}
	for _, p := range s.ProviderHealth {
		byProv[p.Provider] = p
	}
	if !byProv["openai"].Healthy {
		t.Errorf("openai should be healthy: %+v", byProv["openai"])
	}
	if byProv["anthropic"].Healthy {
		t.Errorf("anthropic should be unhealthy: %+v", byProv["anthropic"])
	}
}

func TestDashboardSummary_BadWindow(t *testing.T) {
	h := NewObservabilityHandler(&fakeRichReader{}, nil, nil, nil)
	if rec := obsReq(h, "/api/v1/observability/dashboard/summary?window=99y"); rec.Code != http.StatusBadRequest {
		t.Fatalf("= %d, want 400", rec.Code)
	}
}

func TestDashboardSummary_StoreError(t *testing.T) {
	h := NewObservabilityHandler(&fakeRichReader{summaryErr: errors.New("db down")}, nil, nil, nil)
	if rec := obsReq(h, "/api/v1/observability/dashboard/summary?window=1h"); rec.Code != http.StatusInternalServerError {
		t.Fatalf("= %d, want 500", rec.Code)
	}
}

func TestDashboardTimeseries_Metrics(t *testing.T) {
	buckets := []configdb.DashboardSeriesBucket{
		{Requests: 60, Errored: 6, TokensIn: 120, TokensOut: 60, P50LatencyMs: 10, P95LatencyMs: 20, P99LatencyMs: 30},
		{Requests: 0, Errored: 0},
	}
	store := &fakeRichReader{buckets: buckets}
	h := NewObservabilityHandler(store, nil, nil, nil)

	cases := []struct {
		series     string
		wantSeries int
		wantUnit   string
	}{
		{"rps", 1, "req/s"},
		{"error_rate", 1, "%"},
		{"latency", 3, "ms"},
		{"tokens_per_second", 2, "tok/s"},
	}
	for _, tc := range cases {
		t.Run(tc.series, func(t *testing.T) {
			rec := obsReq(h, "/api/v1/observability/dashboard/timeseries?window=1h&series="+tc.series)
			if rec.Code != http.StatusOK {
				t.Fatalf("= %d, want 200: %s", rec.Code, rec.Body)
			}
			var ts adminc.DashboardTimeseries
			if err := json.NewDecoder(rec.Body).Decode(&ts); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if len(ts.Series) != tc.wantSeries {
				t.Fatalf("series len = %d, want %d", len(ts.Series), tc.wantSeries)
			}
			if ts.Series[0].Unit != tc.wantUnit {
				t.Errorf("unit = %q, want %q", ts.Series[0].Unit, tc.wantUnit)
			}
			if len(ts.Series[0].Points) != 2 {
				t.Errorf("points = %d, want 2", len(ts.Series[0].Points))
			}
		})
	}
}

func TestDashboardTimeseries_MetricAlias(t *testing.T) {
	// The endpoint accepts ?metric= as an alias for ?series=.
	store := &fakeRichReader{buckets: []configdb.DashboardSeriesBucket{{Requests: 1}}}
	h := NewObservabilityHandler(store, nil, nil, nil)
	if rec := obsReq(h, "/api/v1/observability/dashboard/timeseries?window=1h&metric=rps"); rec.Code != http.StatusOK {
		t.Fatalf("= %d, want 200: %s", rec.Code, rec.Body)
	}
}

func TestDashboardTimeseries_BadSeries(t *testing.T) {
	h := NewObservabilityHandler(&fakeRichReader{}, nil, nil, nil)
	for _, path := range []string{
		"/api/v1/observability/dashboard/timeseries?window=1h&series=bogus",
		"/api/v1/observability/dashboard/timeseries?window=1h",
	} {
		if rec := obsReq(h, path); rec.Code != http.StatusBadRequest {
			t.Fatalf("%s = %d, want 400", path, rec.Code)
		}
	}
}

func TestDashboardTimeseries_BadWindowAndStoreError(t *testing.T) {
	h := NewObservabilityHandler(&fakeRichReader{}, nil, nil, nil)
	if rec := obsReq(h, "/api/v1/observability/dashboard/timeseries?window=99y&series=rps"); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad window = %d, want 400", rec.Code)
	}
	h2 := NewObservabilityHandler(&fakeRichReader{bucketsErr: errors.New("boom")}, nil, nil, nil)
	if rec := obsReq(h2, "/api/v1/observability/dashboard/timeseries?window=1h&series=rps"); rec.Code != http.StatusInternalServerError {
		t.Fatalf("store error = %d, want 500", rec.Code)
	}
}

func TestMessagesRecent_Shape(t *testing.T) {
	store := &fakeRichReader{}
	// ListEventsFiltered returns newest-first; the handler reverses to arrival order.
	store.filtered = []configdb.RequestEvent{
		{CorrelationID: "newest", Backend: "openai", Protocol: "chat", Model: "gpt-4o", Method: "POST", StatusCode: 200, LatencyMs: 42, TokensIn: 5, TokensCached: 2, Streaming: true, Detail: []byte(`{"tags":["env:prod"],"rules_fired":["redirect"]}`)},
		{CorrelationID: "oldest", Backend: "anthropic", Protocol: "messages", StatusCode: 500},
	}
	h := NewObservabilityHandler(store, nil, nil, nil)

	rec := obsReq(h, "/api/v1/observability/messages/recent?limit=50&configuration=production")
	if rec.Code != http.StatusOK {
		t.Fatalf("= %d, want 200: %s", rec.Code, rec.Body)
	}
	if store.lastList.Limit != 50 || store.lastList.Filter.Configuration != "production" {
		t.Errorf("params not passed: %+v", store.lastList)
	}

	var resp adminc.MessagesRecentResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Capacity != 50 || len(resp.Entries) != 2 {
		t.Fatalf("resp = %+v", resp)
	}
	// Reversed into arrival order: oldest first.
	if resp.Entries[0].CorrelationID != "oldest" || resp.Entries[1].CorrelationID != "newest" {
		t.Errorf("order = %q,%q want oldest,newest", resp.Entries[0].CorrelationID, resp.Entries[1].CorrelationID)
	}
	e := resp.Entries[1]
	if e.EventID != "newest" || e.Provider != "openai" || e.Endpoint != "chat" || e.Method != "POST" {
		t.Errorf("entry mapping = %+v", e)
	}
	if e.DurationMs != 42 || e.TokensIn != 5 || e.TokensCached != 2 || !e.Streaming {
		t.Errorf("entry scalars = %+v", e)
	}
	if len(e.Tags) != 1 || e.Tags[0] != "env:prod" {
		t.Errorf("tags = %+v", e.Tags)
	}
	if len(e.RulesMatched) != 1 || e.RulesMatched[0].RuleName != "redirect" {
		t.Errorf("rules = %+v", e.RulesMatched)
	}
	// attempts is intentionally empty in phase-1.
	if len(e.Attempts) != 0 {
		t.Errorf("attempts should be empty, got %+v", e.Attempts)
	}
}

func TestMessagesRecent_BadLimit(t *testing.T) {
	h := NewObservabilityHandler(&fakeRichReader{}, nil, nil, nil)
	if rec := obsReq(h, "/api/v1/observability/messages/recent?limit=-1"); rec.Code != http.StatusBadRequest {
		t.Fatalf("= %d, want 400", rec.Code)
	}
}

func TestMessagesRecent_LimitCapAndDefault(t *testing.T) {
	store := &fakeRichReader{}
	h := NewObservabilityHandler(store, nil, nil, nil)

	if rec := obsReq(h, "/api/v1/observability/messages/recent"); rec.Code != http.StatusOK {
		t.Fatalf("default = %d, want 200", rec.Code)
	}
	if store.lastList.Limit != messagesRecentLimitDefault {
		t.Errorf("default limit = %d, want %d", store.lastList.Limit, messagesRecentLimitDefault)
	}

	if rec := obsReq(h, "/api/v1/observability/messages/recent?limit=99999"); rec.Code != http.StatusOK {
		t.Fatalf("cap = %d, want 200", rec.Code)
	}
	if store.lastList.Limit != messagesRecentLimitMax {
		t.Errorf("capped limit = %d, want %d", store.lastList.Limit, messagesRecentLimitMax)
	}
}

func TestMessagesRecent_StoreError(t *testing.T) {
	h := NewObservabilityHandler(&fakeRichReader{fakeEventReader: fakeEventReader{filterErr: errors.New("db down")}}, nil, nil, nil)
	if rec := obsReq(h, "/api/v1/observability/messages/recent"); rec.Code != http.StatusInternalServerError {
		t.Fatalf("= %d, want 500", rec.Code)
	}
}

func TestMessageBody_Shape(t *testing.T) {
	record := connc.Record{
		CorrelationID: "c1",
		Request: connc.RequestPart{
			Method:    "POST",
			Body:      json.RawMessage(`{"model":"gpt-4o"}`),
			BodyBytes: 18,
			Headers:   map[string]string{"Content-Type": "application/json"},
		},
		Response: connc.ResponsePart{
			Status:    200,
			Body:      json.RawMessage(`{"id":"x"}`),
			BodyBytes: 100, // larger than captured -> truncated
			Headers:   map[string]string{"X-Test": "1"},
		},
	}
	raw, _ := json.Marshal(record)
	store := &fakeRichReader{}
	bodies := &fakeRecordReader{record: raw}
	h := NewObservabilityHandler(store, bodies, nil, nil)

	rec := obsReq(h, "/api/v1/observability/messages/c1/body")
	if rec.Code != http.StatusOK {
		t.Fatalf("= %d, want 200: %s", rec.Code, rec.Body)
	}
	var d adminc.MessageBodyDetail
	if err := json.NewDecoder(rec.Body).Decode(&d); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if d.EventID != "c1" {
		t.Errorf("event_id = %q", d.EventID)
	}
	if d.Request != `{"model":"gpt-4o"}` || d.RequestTotalBytes != 18 || d.RequestTruncated {
		t.Errorf("request = %+v", d)
	}
	if d.Response != `{"id":"x"}` || d.ResponseTotalBytes != 100 || !d.ResponseTruncated {
		t.Errorf("response = %+v", d)
	}
	if len(d.RequestHeaders["Content-Type"]) != 1 || d.RequestHeaders["Content-Type"][0] != "application/json" {
		t.Errorf("req headers = %+v", d.RequestHeaders)
	}
	if len(d.ResponseHeaders["X-Test"]) != 1 {
		t.Errorf("resp headers = %+v", d.ResponseHeaders)
	}
}

func TestMessageBody_TotalBytesFallback(t *testing.T) {
	// A record that omits body_bytes still reports a total from the captured
	// length, on both the request and response side.
	record := connc.Record{
		Request:  connc.RequestPart{Body: json.RawMessage(`{}`)},
		Response: connc.ResponsePart{Body: json.RawMessage(`{"a":1}`)},
	}
	raw, _ := json.Marshal(record)
	h := NewObservabilityHandler(&fakeRichReader{}, &fakeRecordReader{record: raw}, nil, nil)

	rec := obsReq(h, "/api/v1/observability/messages/c1/body")
	var d adminc.MessageBodyDetail
	_ = json.NewDecoder(rec.Body).Decode(&d)
	if d.RequestTotalBytes != 2 {
		t.Errorf("request_total_bytes = %d, want 2", d.RequestTotalBytes)
	}
	if d.ResponseTotalBytes != 7 {
		t.Errorf("response_total_bytes = %d, want 7", d.ResponseTotalBytes)
	}
}

func TestRateHelpers_ZeroGuards(t *testing.T) {
	if perSecond(10, 0) != 0 {
		t.Errorf("perSecond zero window = %v, want 0", perSecond(10, 0))
	}
	if ratio(5, 0) != 0 {
		t.Errorf("ratio zero denom = %v, want 0", ratio(5, 0))
	}
}

func TestMessageBody_NotFound(t *testing.T) {
	h := NewObservabilityHandler(&fakeRichReader{}, &fakeRecordReader{recordErr: configdb.ErrRequestBodyNotFound}, nil, nil)
	if rec := obsReq(h, "/api/v1/observability/messages/ghost/body"); rec.Code != http.StatusNotFound {
		t.Fatalf("= %d, want 404", rec.Code)
	}
}

func TestMessageBody_StoreError(t *testing.T) {
	h := NewObservabilityHandler(&fakeRichReader{}, &fakeRecordReader{recordErr: errors.New("boom")}, nil, nil)
	if rec := obsReq(h, "/api/v1/observability/messages/x/body"); rec.Code != http.StatusInternalServerError {
		t.Fatalf("= %d, want 500", rec.Code)
	}
}

func TestMessageBody_BadRecord(t *testing.T) {
	h := NewObservabilityHandler(&fakeRichReader{}, &fakeRecordReader{record: json.RawMessage(`not-json`)}, nil, nil)
	if rec := obsReq(h, "/api/v1/observability/messages/x/body"); rec.Code != http.StatusInternalServerError {
		t.Fatalf("= %d, want 500", rec.Code)
	}
}

// TestRichRoutes_DisabledWithoutRich confirms that a plain eventReader (not a
// richReader) leaves the rich routes unmounted — the v1 endpoints still work.
func TestRichRoutes_DisabledWithoutRich(t *testing.T) {
	h := NewObservabilityHandler(&fakeEventReader{}, nil, nil, nil)
	if rec := obsReq(h, "/api/v1/observability/dashboard/summary?window=1h"); rec.Code != http.StatusNotFound {
		t.Fatalf("rich route should be unmounted: %d", rec.Code)
	}
	// v1 events route still serves.
	if rec := obsReq(h, "/api/v1/observability/events"); rec.Code != http.StatusOK {
		t.Fatalf("v1 events = %d, want 200", rec.Code)
	}
}
