package controlplane

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

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

	provBuckets    []configdb.DashboardProviderSeriesBucket
	provBucketsErr error
	lastProvP      configdb.DashboardSeriesParams
}

func (f *fakeRichReader) QueryDashboardSummary(_ context.Context, p configdb.DashboardParams) (configdb.DashboardSummary, error) {
	f.lastSummP = p
	return f.summary, f.summaryErr
}

func (f *fakeRichReader) QueryDashboardSeries(_ context.Context, p configdb.DashboardSeriesParams) ([]configdb.DashboardSeriesBucket, error) {
	f.lastSeriesP = p
	return f.buckets, f.bucketsErr
}

func (f *fakeRichReader) QueryDashboardSeriesByProvider(_ context.Context, p configdb.DashboardSeriesParams) ([]configdb.DashboardProviderSeriesBucket, error) {
	f.lastProvP = p
	return f.provBuckets, f.provBucketsErr
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

func TestDashboardTimeseries_PerProvider(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	t1 := t0.Add(time.Minute)
	// openai is busiest overall; anthropic only appears in the second bucket.
	provBuckets := []configdb.DashboardProviderSeriesBucket{
		{Ts: t0, Provider: "openai", Requests: 120, Errored: 12},
		{Ts: t1, Provider: "openai", Requests: 60, Errored: 0},
		{Ts: t1, Provider: "anthropic", Requests: 30, Errored: 15},
	}
	store := &fakeRichReader{provBuckets: provBuckets}
	h := NewObservabilityHandler(store, nil, nil, nil)

	t.Run("rps_by_provider_top5", func(t *testing.T) {
		ts := decodeTimeseries(t, h, "rps_by_provider_top5")
		if len(ts.Series) != 2 {
			t.Fatalf("series len = %d, want 2", len(ts.Series))
		}
		// Ranked by total requests: openai (180) before anthropic (30).
		if ts.Series[0].Name != "openai" || ts.Series[1].Name != "anthropic" {
			t.Fatalf("order = %q,%q, want openai,anthropic", ts.Series[0].Name, ts.Series[1].Name)
		}
		for _, s := range ts.Series {
			if s.Unit != "req/s" {
				t.Errorf("%s unit = %q, want req/s", s.Name, s.Unit)
			}
			if s.Labels["provider"] != s.Name {
				t.Errorf("%s label provider = %q, want %q", s.Name, s.Labels["provider"], s.Name)
			}
			// Both lines zero-filled across the same two distinct buckets.
			if len(s.Points) != 2 {
				t.Errorf("%s points = %d, want 2", s.Name, len(s.Points))
			}
		}
		// 1h window over 60 buckets = 60s buckets; 120 req / 60s = 2 req/s.
		if got := ts.Series[0].Points[0].Value; got != 2 {
			t.Errorf("openai bucket0 = %v, want 2", got)
		}
		// anthropic absent in bucket0 → zero-filled.
		if got := ts.Series[1].Points[0].Value; got != 0 {
			t.Errorf("anthropic bucket0 = %v, want 0", got)
		}
	})

	t.Run("error_rate_by_provider_top5", func(t *testing.T) {
		ts := decodeTimeseries(t, h, "error_rate_by_provider_top5")
		if len(ts.Series) != 2 {
			t.Fatalf("series len = %d, want 2", len(ts.Series))
		}
		for _, s := range ts.Series {
			if s.Unit != "%" {
				t.Errorf("%s unit = %q, want %%", s.Name, s.Unit)
			}
		}
		// openai bucket0: 12/120 = 10%.
		if got := ts.Series[0].Points[0].Value; got != 10 {
			t.Errorf("openai bucket0 err rate = %v, want 10", got)
		}
		// anthropic bucket1: 15/30 = 50%.
		if got := ts.Series[1].Points[1].Value; got != 50 {
			t.Errorf("anthropic bucket1 err rate = %v, want 50", got)
		}
	})
}

func TestDashboardTimeseries_PerProviderTopNCap(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	// Seven providers in one bucket; only the five busiest should surface, and
	// the zero-request provider is dropped entirely.
	provBuckets := []configdb.DashboardProviderSeriesBucket{
		{Ts: t0, Provider: "p1", Requests: 70},
		{Ts: t0, Provider: "p2", Requests: 60},
		{Ts: t0, Provider: "p3", Requests: 50},
		{Ts: t0, Provider: "p4", Requests: 40},
		{Ts: t0, Provider: "p5", Requests: 30},
		{Ts: t0, Provider: "p6", Requests: 20},
		{Ts: t0, Provider: "p0", Requests: 0},
	}
	store := &fakeRichReader{provBuckets: provBuckets}
	h := NewObservabilityHandler(store, nil, nil, nil)
	ts := decodeTimeseries(t, h, "rps_by_provider_top5")
	if len(ts.Series) != 5 {
		t.Fatalf("series len = %d, want 5 (top-N cap)", len(ts.Series))
	}
	want := []string{"p1", "p2", "p3", "p4", "p5"}
	for i, s := range ts.Series {
		if s.Name != want[i] {
			t.Errorf("series[%d] = %q, want %q", i, s.Name, want[i])
		}
	}
}

func TestDashboardTimeseries_PerProviderEmpty(t *testing.T) {
	h := NewObservabilityHandler(&fakeRichReader{}, nil, nil, nil)
	ts := decodeTimeseries(t, h, "rps_by_provider_top5")
	if len(ts.Series) != 0 {
		t.Fatalf("series len = %d, want 0 for empty window", len(ts.Series))
	}
}

func TestDashboardTimeseries_PerProviderStoreError(t *testing.T) {
	h := NewObservabilityHandler(&fakeRichReader{provBucketsErr: errors.New("boom")}, nil, nil, nil)
	if rec := obsReq(h, "/api/v1/observability/dashboard/timeseries?window=1h&series=rps_by_provider_top5"); rec.Code != http.StatusInternalServerError {
		t.Fatalf("= %d, want 500", rec.Code)
	}
}

func decodeTimeseries(t *testing.T, h http.Handler, series string) adminc.DashboardTimeseries {
	t.Helper()
	rec := obsReq(h, "/api/v1/observability/dashboard/timeseries?window=1h&series="+series)
	if rec.Code != http.StatusOK {
		t.Fatalf("%s = %d, want 200: %s", series, rec.Code, rec.Body)
	}
	var ts adminc.DashboardTimeseries
	if err := json.NewDecoder(rec.Body).Decode(&ts); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return ts
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
			Status:        200,
			Body:          json.RawMessage(`{"id":"x"}`),
			BodyBytes:     100,
			BodyTruncated: true,
			Headers:       map[string]string{"X-Test": "1"},
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

func TestRecordToBodyDetail_Truncation(t *testing.T) {
	tests := []struct {
		name      string
		req       connc.RequestPart
		resp      connc.ResponsePart
		wantReqTr bool
		wantResTr bool
	}{
		{
			// The live repro: a complete body whose inline JSON is compacted
			// relative to the raw wire bytes BodyBytes counts. BodyBytes (1089)
			// exceeds len(Body) (744-ish here, scaled) with no truncation flag
			// set -> must NOT be reported as truncated.
			name:      "complete body, BodyBytes > len(Body), no flag",
			req:       connc.RequestPart{Body: json.RawMessage(`{"model":"gpt-4o"}`), BodyBytes: 64},
			resp:      connc.ResponsePart{Body: json.RawMessage(`{"id":"x","object":"chat.completion"}`), BodyBytes: 1089},
			wantReqTr: false,
			wantResTr: false,
		},
		{
			name:      "actually cap-truncated body, flag set",
			req:       connc.RequestPart{Body: json.RawMessage(`"{\"model\":\"gpt-4o"`), BodyBytes: 4096, BodyTruncated: true},
			resp:      connc.ResponsePart{Body: json.RawMessage(`"{\"id\":\"x"`), BodyBytes: 8192, BodyTruncated: true},
			wantReqTr: true,
			wantResTr: true,
		},
		{
			name:      "omitted (metadata_only) body",
			req:       connc.RequestPart{BodyBytes: 12_000_000, BodyOmitted: true},
			resp:      connc.ResponsePart{BodyBytes: 9_000_000, BodyOmitted: true},
			wantReqTr: true,
			wantResTr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw, err := json.Marshal(connc.Record{Request: tt.req, Response: tt.resp})
			if err != nil {
				t.Fatalf("marshal record: %v", err)
			}
			d, err := recordToBodyDetail("c1", raw)
			if err != nil {
				t.Fatalf("recordToBodyDetail: %v", err)
			}
			if d.RequestTruncated != tt.wantReqTr {
				t.Errorf("RequestTruncated = %v, want %v", d.RequestTruncated, tt.wantReqTr)
			}
			if d.ResponseTruncated != tt.wantResTr {
				t.Errorf("ResponseTruncated = %v, want %v", d.ResponseTruncated, tt.wantResTr)
			}
		})
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

func TestMessageBody_MergesGenAIContent(t *testing.T) {
	// Both a connector Record and telemetry-native gen_ai_content are present:
	// the response carries the spool body fields and the gen_ai_content object.
	record := connc.Record{
		Request:  connc.RequestPart{Body: json.RawMessage(`{"model":"gpt-4o"}`)},
		Response: connc.ResponsePart{Body: json.RawMessage(`{"id":"x"}`)},
	}
	raw, _ := json.Marshal(record)
	content := json.RawMessage(`{"input_messages":[{"role":"user","parts":[{"type":"text","content":"hi"}]}],"output_messages":[{"role":"assistant","parts":[{"type":"tool_call","name":"get_weather"}]}]}`)
	store := &fakeRichReader{}
	store.one = configdb.RequestEvent{GenAIContent: content}
	h := NewObservabilityHandler(store, &fakeRecordReader{record: raw}, nil, nil)

	rec := obsReq(h, "/api/v1/observability/messages/c1/body")
	if rec.Code != http.StatusOK {
		t.Fatalf("= %d, want 200: %s", rec.Code, rec.Body)
	}
	var d adminc.MessageBodyDetail
	if err := json.NewDecoder(rec.Body).Decode(&d); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if d.Request != `{"model":"gpt-4o"}` {
		t.Errorf("body lost: %+v", d)
	}
	if len(d.GenAIContent) == 0 {
		t.Fatal("gen_ai_content missing")
	}
	var gc map[string]json.RawMessage
	if err := json.Unmarshal(d.GenAIContent, &gc); err != nil {
		t.Fatalf("gen_ai_content not an object: %v", err)
	}
	if _, ok := gc["output_messages"]; !ok {
		t.Errorf("output_messages absent: %s", d.GenAIContent)
	}
}

func TestMessageBody_GenAIContentOnlyNoRecord(t *testing.T) {
	// Telemetry-only deployment: no connector Record, but the event carries
	// gen_ai_content. The body endpoint serves the content rather than 404ing.
	content := json.RawMessage(`{"input_messages":[{"role":"user","parts":[{"type":"text","content":"hi"}]}]}`)
	store := &fakeRichReader{}
	store.one = configdb.RequestEvent{GenAIContent: content}
	h := NewObservabilityHandler(store, &fakeRecordReader{recordErr: configdb.ErrRequestBodyNotFound}, nil, nil)

	rec := obsReq(h, "/api/v1/observability/messages/c1/body")
	if rec.Code != http.StatusOK {
		t.Fatalf("= %d, want 200: %s", rec.Code, rec.Body)
	}
	var d adminc.MessageBodyDetail
	if err := json.NewDecoder(rec.Body).Decode(&d); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if d.Request != "" {
		t.Errorf("want no body, got %q", d.Request)
	}
	if len(d.GenAIContent) == 0 {
		t.Error("gen_ai_content missing")
	}
}

func TestMessageBody_GenAIContentLookupErrorIgnored(t *testing.T) {
	// A gen_ai_content lookup failure must not fail the request when the
	// connector Record still supplies bodies.
	record := connc.Record{Request: connc.RequestPart{Body: json.RawMessage(`{}`)}}
	raw, _ := json.Marshal(record)
	store := &fakeRichReader{}
	store.getErr = errors.New("event lookup down")
	h := NewObservabilityHandler(store, &fakeRecordReader{record: raw}, nil, nil)

	rec := obsReq(h, "/api/v1/observability/messages/c1/body")
	if rec.Code != http.StatusOK {
		t.Fatalf("= %d, want 200: %s", rec.Code, rec.Body)
	}
	var d adminc.MessageBodyDetail
	_ = json.NewDecoder(rec.Body).Decode(&d)
	if len(d.GenAIContent) != 0 {
		t.Errorf("want no content on lookup error, got %s", d.GenAIContent)
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
