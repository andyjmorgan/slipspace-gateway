//go:build e2e

package configdb_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	connc "github.com/andyjmorgan/sluice-gateway/contracts/connector"
	"github.com/andyjmorgan/sluice-gateway/internal/controlplane/configdb"
)

// detailJSON marshals an EventDetail envelope for the request_events.detail
// column so the fired-row aggregates have tags + rules to unroll.
func detailJSON(t *testing.T, tags, rules []string) []byte {
	t.Helper()
	b, err := json.Marshal(configdb.EventDetail{Tags: tags, RulesFired: rules})
	if err != nil {
		t.Fatalf("marshal detail: %v", err)
	}
	return b
}

// TestConfigDB_QueryDashboardSummary_Totals exercises the headline totals,
// latency quantiles, and the four dimension breakdowns over real Postgres.
func TestConfigDB_QueryDashboardSummary_Totals(t *testing.T) {
	ctx := context.Background()
	db, err := configdb.Open(ctx, startPostgres(t))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(db.Close)

	base := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	seedEvents(t, db, []configdb.RequestEvent{
		{CorrelationID: "s1", Configuration: "prod", Backend: "openai", Protocol: "chat", Model: "gpt-4o", StatusCode: 200, LatencyMs: 10, TokensIn: 100, TokensOut: 200, TokensCached: 10, TokensCacheCreation: 1, ObservedAt: base.Add(1 * time.Minute)},
		{CorrelationID: "s2", Configuration: "prod", Backend: "openai", Protocol: "chat", Model: "gpt-4o", StatusCode: 200, LatencyMs: 20, TokensIn: 100, TokensOut: 200, ObservedAt: base.Add(2 * time.Minute)},
		{CorrelationID: "s3", Configuration: "dev", Backend: "anthropic", Protocol: "messages", Model: "claude", StatusCode: 500, LatencyMs: 30, TokensIn: 50, TokensOut: 0, ObservedAt: base.Add(3 * time.Minute)},
		{CorrelationID: "s4", Configuration: "dev", Backend: "anthropic", Protocol: "messages", Model: "claude", StatusCode: 404, LatencyMs: 40, ObservedAt: base.Add(4 * time.Minute)},
	})

	s, err := db.QueryDashboardSummary(ctx, configdb.DashboardParams{
		From:       base,
		To:         base.Add(10 * time.Minute),
		RecentFrom: base,
	})
	if err != nil {
		t.Fatalf("summary: %v", err)
	}

	if s.Totals.Requests != 4 || s.Totals.RequestsSuccess != 2 || s.Totals.RequestsErrored != 2 {
		t.Errorf("totals = %+v, want 4/2/2", s.Totals)
	}
	if s.Totals.TokensIn != 250 || s.Totals.TokensOut != 400 || s.Totals.TokensCached != 10 || s.Totals.TokensCacheCreation != 1 {
		t.Errorf("token totals = %+v", s.Totals)
	}
	// p95 over [10,20,30,40] is 38.5 -> 39 (rounded).
	if s.Latency.P95 != 39 {
		t.Errorf("p95 = %d, want 39", s.Latency.P95)
	}

	// by_provider: openai (2 req, 0 err), anthropic (2 req, 2 err).
	provReq := map[string]configdb.DashboardDimensionRow{}
	for _, r := range s.ByProvider {
		provReq[r.Key] = r
	}
	if provReq["openai"].Requests != 2 || provReq["openai"].ErrorRate != 0 {
		t.Errorf("openai provider row = %+v", provReq["openai"])
	}
	if provReq["anthropic"].Requests != 2 || provReq["anthropic"].ErrorRate != 1.0 {
		t.Errorf("anthropic provider row = %+v", provReq["anthropic"])
	}

	// by_endpoint keyed on (backend, protocol).
	if len(s.ByEndpoint) != 2 {
		t.Fatalf("by_endpoint len = %d, want 2", len(s.ByEndpoint))
	}
	epByProv := map[string]configdb.DashboardEndpointRow{}
	for _, r := range s.ByEndpoint {
		epByProv[r.Provider] = r
	}
	if epByProv["openai"].Endpoint != "chat" || epByProv["openai"].Requests != 2 {
		t.Errorf("openai endpoint row = %+v", epByProv["openai"])
	}
	if epByProv["anthropic"].Endpoint != "messages" || epByProv["anthropic"].Requests != 2 {
		t.Errorf("anthropic endpoint row = %+v", epByProv["anthropic"])
	}

	// by_configuration: prod (2), dev (2).
	if len(s.ByConfiguration) != 2 {
		t.Fatalf("by_configuration len = %d, want 2", len(s.ByConfiguration))
	}

	// by_model: gpt-4o (2, 200 in / 400 out), claude (2).
	modelByName := map[string]configdb.DashboardModelRow{}
	for _, m := range s.ByModel {
		modelByName[m.Model] = m
	}
	if modelByName["gpt-4o"].Requests != 2 || modelByName["gpt-4o"].TokensIn != 200 || modelByName["gpt-4o"].Provider != "openai" {
		t.Errorf("gpt-4o model row = %+v", modelByName["gpt-4o"])
	}
}

// TestConfigDB_QueryDashboardSummary_FiredAndHealth exercises the rules/tags
// fired aggregates (unrolled from detail JSONB) and the recent provider-health
// snapshot windowed by RecentFrom.
func TestConfigDB_QueryDashboardSummary_FiredAndHealth(t *testing.T) {
	ctx := context.Background()
	db, err := configdb.Open(ctx, startPostgres(t))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(db.Close)

	base := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	to := base.Add(20 * time.Minute)
	seedEvents(t, db, []configdb.RequestEvent{
		// Two events fire "redirect" + tag "env:prod" under config prod; one under dev.
		{CorrelationID: "f1", Configuration: "prod", Backend: "openai", StatusCode: 200, ObservedAt: base.Add(1 * time.Minute), Detail: detailJSON(t, []string{"env:prod"}, []string{"redirect"})},
		{CorrelationID: "f2", Configuration: "prod", Backend: "openai", StatusCode: 200, ObservedAt: base.Add(2 * time.Minute), Detail: detailJSON(t, []string{"env:prod"}, []string{"redirect"})},
		{CorrelationID: "f3", Configuration: "dev", Backend: "openai", StatusCode: 200, ObservedAt: base.Add(3 * time.Minute), Detail: detailJSON(t, []string{"env:prod", "team:core"}, []string{"redirect", "rate-limit"})},
		// An event with no detail must not break the unroll.
		{CorrelationID: "f4", Configuration: "prod", Backend: "openai", StatusCode: 200, ObservedAt: base.Add(4 * time.Minute)},
		// A recent (within 5m of `to`) error on anthropic to drive provider health.
		{CorrelationID: "h1", Configuration: "prod", Backend: "anthropic", StatusCode: 500, ObservedAt: to.Add(-1 * time.Minute)},
		{CorrelationID: "h2", Configuration: "prod", Backend: "anthropic", StatusCode: 200, ObservedAt: to.Add(-2 * time.Minute)},
		// An OLD openai event outside the recent window must not count toward health.
		{CorrelationID: "h3", Configuration: "prod", Backend: "openai", StatusCode: 500, ObservedAt: base.Add(1 * time.Minute)},
	})

	s, err := db.QueryDashboardSummary(ctx, configdb.DashboardParams{
		From:       base,
		To:         to,
		RecentFrom: to.Add(-5 * time.Minute),
	})
	if err != nil {
		t.Fatalf("summary: %v", err)
	}

	// rules_fired: redirect x3, rate-limit x1.
	ruleByName := map[string]configdb.DashboardFiredRow{}
	for _, r := range s.RulesFired {
		ruleByName[r.Key] = r
	}
	if ruleByName["redirect"].Count != 3 {
		t.Errorf("redirect count = %d, want 3", ruleByName["redirect"].Count)
	}
	if len(ruleByName["redirect"].UsedByConfigurations) != 2 {
		t.Errorf("redirect configs = %v, want [dev prod]", ruleByName["redirect"].UsedByConfigurations)
	}
	if ruleByName["rate-limit"].Count != 1 {
		t.Errorf("rate-limit count = %d, want 1", ruleByName["rate-limit"].Count)
	}

	// tags_fired: env:prod x3, team:core x1.
	tagByName := map[string]configdb.DashboardFiredRow{}
	for _, r := range s.TagsFired {
		tagByName[r.Key] = r
	}
	if tagByName["env:prod"].Count != 3 || tagByName["team:core"].Count != 1 {
		t.Errorf("tags = %+v", s.TagsFired)
	}

	// provider_health over the recent window: anthropic 2 req / 1 err = 0.5;
	// openai must NOT appear (its only recent-window events... h3 is old, f1-f4
	// are old too) — only anthropic falls inside [to-5m, to).
	health := map[string]configdb.DashboardProviderHealth{}
	for _, h := range s.ProviderHealth {
		health[h.Provider] = h
	}
	if health["anthropic"].Requests != 2 || health["anthropic"].ErrorRate != 0.5 {
		t.Errorf("anthropic health = %+v, want 2 req / 0.5", health["anthropic"])
	}
	if _, ok := health["openai"]; ok {
		t.Errorf("openai should not be in recent health window: %+v", health["openai"])
	}
}

// TestConfigDB_QueryDashboardSummary_Filter confirms the shared Filter narrows
// every panel.
func TestConfigDB_QueryDashboardSummary_Filter(t *testing.T) {
	ctx := context.Background()
	db, err := configdb.Open(ctx, startPostgres(t))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(db.Close)

	base := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	seedEvents(t, db, []configdb.RequestEvent{
		{CorrelationID: "p1", Configuration: "prod", Backend: "openai", StatusCode: 200, ObservedAt: base.Add(1 * time.Minute)},
		{CorrelationID: "d1", Configuration: "dev", Backend: "openai", StatusCode: 200, ObservedAt: base.Add(2 * time.Minute)},
	})

	s, err := db.QueryDashboardSummary(ctx, configdb.DashboardParams{
		From:       base,
		To:         base.Add(10 * time.Minute),
		RecentFrom: base,
		Filter:     configdb.EventFilter{Configuration: "prod"},
	})
	if err != nil {
		t.Fatalf("summary: %v", err)
	}
	if s.Totals.Requests != 1 {
		t.Errorf("filtered requests = %d, want 1", s.Totals.Requests)
	}
}

// TestConfigDB_QueryDashboardSeries_Buckets exercises the zero-filled per-bucket
// timeseries scan: request volume, errored count, token sums, and the latency
// quantile set.
func TestConfigDB_QueryDashboardSeries_Buckets(t *testing.T) {
	ctx := context.Background()
	db, err := configdb.Open(ctx, startPostgres(t))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(db.Close)

	base := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	seedEvents(t, db, []configdb.RequestEvent{
		// Bucket 0 [12:00,12:05): 2 req, 1 error, tokens.
		{CorrelationID: "b0-1", Backend: "openai", StatusCode: 200, LatencyMs: 10, TokensIn: 5, TokensOut: 7, ObservedAt: base.Add(1 * time.Minute)},
		{CorrelationID: "b0-2", Backend: "openai", StatusCode: 500, LatencyMs: 30, TokensIn: 3, TokensOut: 0, ObservedAt: base.Add(2 * time.Minute)},
		// Bucket 1 [12:05,12:10): empty -> zero-fill.
		// Bucket 2 [12:10,12:15): 1 req.
		{CorrelationID: "b2-1", Backend: "openai", StatusCode: 200, LatencyMs: 50, TokensIn: 1, TokensOut: 1, ObservedAt: base.Add(11 * time.Minute)},
	})

	buckets, err := db.QueryDashboardSeries(ctx, configdb.DashboardSeriesParams{
		From:          base,
		To:            base.Add(15 * time.Minute),
		BucketSeconds: 300,
	})
	if err != nil {
		t.Fatalf("series: %v", err)
	}
	if len(buckets) != 3 {
		t.Fatalf("buckets = %d, want 3", len(buckets))
	}
	if buckets[0].Requests != 2 || buckets[0].Errored != 1 || buckets[0].TokensIn != 8 || buckets[0].TokensOut != 7 {
		t.Errorf("bucket 0 = %+v", buckets[0])
	}
	if !buckets[0].Ts.Equal(base) {
		t.Errorf("bucket 0 ts = %v, want %v", buckets[0].Ts, base)
	}
	if buckets[1].Requests != 0 || buckets[1].P95LatencyMs != 0 {
		t.Errorf("bucket 1 (empty) = %+v, want zero-fill", buckets[1])
	}
	if buckets[2].Requests != 1 {
		t.Errorf("bucket 2 = %+v, want 1 req", buckets[2])
	}
}

// TestConfigDB_QueryDashboardSeries_BadBucket rejects a non-positive bucket.
func TestConfigDB_QueryDashboardSeries_BadBucket(t *testing.T) {
	ctx := context.Background()
	db, err := configdb.Open(ctx, startPostgres(t))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(db.Close)

	if _, err := db.QueryDashboardSeries(ctx, configdb.DashboardSeriesParams{
		From: time.Now(), To: time.Now().Add(time.Hour), BucketSeconds: 0,
	}); err == nil {
		t.Fatal("want error for zero bucket")
	}
}

// TestConfigDB_QueryDashboardSeriesByProvider groups the per-bucket request /
// error scan by backend, emitting one row per (bucket, provider) pair that saw
// traffic. Buckets with no traffic for a provider are absent (the handler
// zero-fills them), and the empty-backend event is excluded.
func TestConfigDB_QueryDashboardSeriesByProvider(t *testing.T) {
	ctx := context.Background()
	db, err := configdb.Open(ctx, startPostgres(t))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(db.Close)

	base := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	seedEvents(t, db, []configdb.RequestEvent{
		// Bucket 0 [12:00,12:05): openai 2 req / 1 err, anthropic 1 req / 0 err.
		{CorrelationID: "p0-1", Backend: "openai", StatusCode: 200, ObservedAt: base.Add(1 * time.Minute)},
		{CorrelationID: "p0-2", Backend: "openai", StatusCode: 500, ObservedAt: base.Add(2 * time.Minute)},
		{CorrelationID: "p0-3", Backend: "anthropic", StatusCode: 200, ObservedAt: base.Add(3 * time.Minute)},
		// Bucket 2 [12:10,12:15): openai only.
		{CorrelationID: "p2-1", Backend: "openai", StatusCode: 429, ObservedAt: base.Add(11 * time.Minute)},
		// Empty backend is excluded from the per-provider scan.
		{CorrelationID: "nob", Backend: "", StatusCode: 200, ObservedAt: base.Add(1 * time.Minute)},
	})

	buckets, err := db.QueryDashboardSeriesByProvider(ctx, configdb.DashboardSeriesParams{
		From:          base,
		To:            base.Add(15 * time.Minute),
		BucketSeconds: 300,
	})
	if err != nil {
		t.Fatalf("series by provider: %v", err)
	}
	// 3 rows: (bucket0,openai), (bucket0,anthropic), (bucket2,openai).
	if len(buckets) != 3 {
		t.Fatalf("rows = %d, want 3: %+v", len(buckets), buckets)
	}
	type key struct {
		unix int64
		prov string
	}
	mk := func(ts time.Time, prov string) key { return key{ts.Unix(), prov} }
	got := map[key]configdb.DashboardProviderSeriesBucket{}
	for _, b := range buckets {
		got[mk(b.Ts, b.Provider)] = b
	}
	if b := got[mk(base, "openai")]; b.Requests != 2 || b.Errored != 1 {
		t.Errorf("bucket0 openai = %+v, want req=2 err=1", b)
	}
	if b := got[mk(base, "anthropic")]; b.Requests != 1 || b.Errored != 0 {
		t.Errorf("bucket0 anthropic = %+v, want req=1 err=0", b)
	}
	if b := got[mk(base.Add(10*time.Minute), "openai")]; b.Requests != 1 || b.Errored != 1 {
		t.Errorf("bucket2 openai = %+v, want req=1 err=1 (429)", b)
	}
	if _, ok := got[mk(base.Add(10*time.Minute), "anthropic")]; ok {
		t.Error("bucket2 anthropic present, want absent (no traffic)")
	}
}

// TestConfigDB_QueryDashboardSeriesByProvider_BadBucket rejects a non-positive
// bucket.
func TestConfigDB_QueryDashboardSeriesByProvider_BadBucket(t *testing.T) {
	ctx := context.Background()
	db, err := configdb.Open(ctx, startPostgres(t))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(db.Close)

	if _, err := db.QueryDashboardSeriesByProvider(ctx, configdb.DashboardSeriesParams{
		From: time.Now(), To: time.Now().Add(time.Hour), BucketSeconds: 0,
	}); err == nil {
		t.Fatal("want error for zero bucket")
	}
}

// TestConfigDB_GetRequestRecord_LatestWins confirms the body drill-in returns
// the highest-(ts_ns,seq) captured Record for a correlation id, and 404s when
// none exist.
func TestConfigDB_GetRequestRecord_LatestWins(t *testing.T) {
	ctx := context.Background()
	db, err := configdb.Open(ctx, startPostgres(t))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(db.Close)

	older := connc.Record{CorrelationID: "c1", Response: connc.ResponsePart{Status: 500}}
	newer := connc.Record{CorrelationID: "c1", Response: connc.ResponsePart{Status: 200}}
	olderJSON, _ := json.Marshal(older)
	newerJSON, _ := json.Marshal(newer)

	if err := db.UpsertRequestBody(ctx, configdb.RequestBody{CorrelationID: "c1", InstanceID: "gw", Seq: 1, TsNs: 100, Body: olderJSON}); err != nil {
		t.Fatalf("upsert older: %v", err)
	}
	if err := db.UpsertRequestBody(ctx, configdb.RequestBody{CorrelationID: "c1", InstanceID: "gw", Seq: 2, TsNs: 200, Body: newerJSON}); err != nil {
		t.Fatalf("upsert newer: %v", err)
	}

	raw, err := db.GetRequestRecord(ctx, "c1")
	if err != nil {
		t.Fatalf("get record: %v", err)
	}
	var got connc.Record
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Response.Status != 200 {
		t.Errorf("latest record status = %d, want 200 (newest)", got.Response.Status)
	}

	if _, err := db.GetRequestRecord(ctx, "ghost"); err != configdb.ErrRequestBodyNotFound {
		t.Errorf("missing record err = %v, want ErrRequestBodyNotFound", err)
	}
}
