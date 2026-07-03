//go:build e2e

package arbiter_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/andyjmorgan/slipspace-gateway/internal/arbiter/store"
)

// TestDashboardRollupsReadCaggs proves the @100 dashboard contract end-to-end on
// the real TimescaleDB engine: QueryDashboardSummary + QueryDashboardSeries read
// the continuous aggregates (never request_events), the outcome split is computed
// from the TEXT status_code, token totals come from cagg_tokens_1m, the fired-row
// panels come from cagg_rules_1m / cagg_tags_1m, and the dashboard-scoped filter
// honors only the CAGG dimensions (a provider filter narrows; a tag filter is
// ignored). It seeds metric_points in one minute bucket, refreshes every CAGG,
// then asserts the store rollups.
func TestDashboardRollupsReadCaggs(t *testing.T) {
	st := migratedStore(t)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Simple protocol so CALL refresh_continuous_aggregate runs outside an
	// extended-protocol implicit transaction (mirrors caggs_test.go).
	cfg, err := pgx.ParseConfig(sharedDSN)
	if err != nil {
		t.Fatalf("parse dsn: %v", err)
	}
	cfg.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
	conn, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer func() { _ = conn.Close(ctx) }()

	// Seed in one bucket comfortably in the past so it falls within the refresh
	// window. A dedicated time window + dimension values isolate this test from
	// the shared container's other seeds.
	at := time.Now().Add(-30 * time.Minute).UTC()
	insert := func(name, labels string, value float64) {
		t.Helper()
		if _, err := conn.Exec(ctx,
			`INSERT INTO metric_points (metric_name, labels, value, observed_at) VALUES ($1, $2::jsonb, $3, $4)`,
			name, labels, value, at); err != nil {
			t.Fatalf("insert %s: %v", name, err)
		}
	}

	// Two providers under one configuration so the provider filter can narrow.
	const (
		cfgName = "dash-e2e"
		oaiBase = `"gen_ai.provider.name":"dash-openai","gen_ai.request.model":"gpt-dash","slipspace.configuration":"dash-e2e","slipspace.protocol":"chat"`
		antBase = `"gen_ai.provider.name":"dash-anthropic","gen_ai.request.model":"claude-dash","slipspace.configuration":"dash-e2e","slipspace.protocol":"messages"`
	)
	// requests: openai 5 ok + 2 errored; anthropic 3 ok.
	insert("slipspace.requests.total", `{`+oaiBase+`,"http.response.status_code":"200"}`, 5)
	insert("slipspace.requests.total", `{`+oaiBase+`,"http.response.status_code":"500"}`, 2)
	insert("slipspace.requests.total", `{`+antBase+`,"http.response.status_code":"200"}`, 3)
	// tokens (openai only).
	insert("slipspace.tokens.input.total", `{`+oaiBase+`}`, 1000)
	insert("slipspace.tokens.output.total", `{`+oaiBase+`}`, 250)
	insert("slipspace.tokens.cached.total", `{`+oaiBase+`}`, 80)
	insert("slipspace.tokens.cache_creation.total", `{`+oaiBase+`}`, 40)
	// rule + tag meters.
	insert("slipspace.rule.fired", `{"rule_name":"dash-rule","slipspace.configuration":"dash-e2e"}`, 4)
	insert("gateway.tags.applied.total", `{"tag":"dash-tag","slipspace.configuration":"dash-e2e"}`, 4)
	// cost meter: per-category rows for both models (the gateway emits one
	// Add per non-zero charge category).
	insert("slipspace.cost.usd.total", `{`+oaiBase+`,"slipspace.cost.category":"input"}`, 0.5)
	insert("slipspace.cost.usd.total", `{`+oaiBase+`,"slipspace.cost.category":"output"}`, 1.25)
	insert("slipspace.cost.usd.total", `{`+antBase+`,"slipspace.cost.category":"output"}`, 0.75)

	for _, cagg := range []string{"cagg_requests_1m", "cagg_tokens_1m", "cagg_rules_1m", "cagg_tags_1m", "cagg_cost_1m"} {
		// A freshly created CAGG's own refresh policy can hold the
		// materialization lock (SQLSTATE 55P03, "concurrent refresh");
		// retry briefly instead of failing the run.
		var rerr error
		for attempt := 0; attempt < 10; attempt++ {
			if _, rerr = conn.Exec(ctx, `CALL refresh_continuous_aggregate($1, NULL, NULL)`, cagg); rerr == nil {
				break
			}
			if !strings.Contains(rerr.Error(), "concurrent refresh") {
				break
			}
			time.Sleep(500 * time.Millisecond)
		}
		if rerr != nil {
			t.Fatalf("refresh %s: %v", cagg, rerr)
		}
	}

	// Narrow window bracketing the single seed bucket, scoped to the test
	// configuration so the totals see only this test's rows.
	from := at.Add(-time.Minute)
	to := at.Add(time.Minute)
	params := store.DashboardParams{
		From:       from,
		To:         to,
		RecentFrom: from,
		Filter:     store.EventFilter{Configuration: cfgName},
	}

	sum, err := st.QueryDashboardSummary(ctx, params)
	if err != nil {
		t.Fatalf("QueryDashboardSummary: %v", err)
	}

	// Totals: 10 requests, 8 success (200-299), 2 errored (>=400).
	if sum.Totals.Requests != 10 || sum.Totals.RequestsSuccess != 8 || sum.Totals.RequestsErrored != 2 {
		t.Errorf("totals = %+v, want requests=10 success=8 errored=2", sum.Totals)
	}
	// Token totals from cagg_tokens_1m (openai only).
	if sum.Totals.TokensIn != 1000 || sum.Totals.TokensOut != 250 ||
		sum.Totals.TokensCached != 80 || sum.Totals.TokensCacheCreation != 40 {
		t.Errorf("token totals = %+v, want in=1000 out=250 cached=80 cacheCreation=40", sum.Totals)
	}
	// Cost totals from cagg_cost_1m: summed across models with the
	// per-category split.
	if sum.Totals.CostUSD < 2.499 || sum.Totals.CostUSD > 2.501 {
		t.Errorf("CostUSD = %v, want 2.50", sum.Totals.CostUSD)
	}
	if got := sum.Totals.CostByCategory["output"]; got < 1.999 || got > 2.001 {
		t.Errorf("CostByCategory[output] = %v, want 2.00 (map=%v)", got, sum.Totals.CostByCategory)
	}
	if got := sum.Totals.CostByCategory["input"]; got < 0.499 || got > 0.501 {
		t.Errorf("CostByCategory[input] = %v, want 0.50", got)
	}

	// ByProvider: error rate computed from status_code (openai 2/7).
	byProv := map[string]store.DashboardDimensionRow{}
	for _, r := range sum.ByProvider {
		byProv[r.Key] = r
	}
	if got := byProv["dash-openai"]; got.Requests != 7 || got.ErrorRate < 0.28 || got.ErrorRate > 0.29 {
		t.Errorf("ByProvider[dash-openai] = %+v, want requests=7 errorRate~0.2857", got)
	}
	if got := byProv["dash-anthropic"]; got.Requests != 3 || got.ErrorRate != 0 {
		t.Errorf("ByProvider[dash-anthropic] = %+v, want requests=3 errorRate=0", got)
	}

	// ByModel: requests + provider from cagg_requests_1m, tokens from cagg_tokens_1m.
	byModel := map[string]store.DashboardModelRow{}
	for _, r := range sum.ByModel {
		byModel[r.Model] = r
	}
	if got := byModel["gpt-dash"]; got.Requests != 7 || got.Provider != "dash-openai" || got.TokensIn != 1000 || got.TokensOut != 250 {
		t.Errorf("ByModel[gpt-dash] = %+v, want requests=7 provider=dash-openai in=1000 out=250", got)
	}
	if got := byModel["gpt-dash"]; got.CostUSD < 1.749 || got.CostUSD > 1.751 {
		t.Errorf("ByModel[gpt-dash].CostUSD = %v, want 1.75", got.CostUSD)
	}
	if got := byModel["claude-dash"]; got.Requests != 3 || got.TokensIn != 0 {
		t.Errorf("ByModel[claude-dash] = %+v, want requests=3 tokensIn=0", got)
	}
	if got := byModel["claude-dash"]; got.CostUSD < 0.749 || got.CostUSD > 0.751 {
		t.Errorf("ByModel[claude-dash].CostUSD = %v, want 0.75", got.CostUSD)
	}

	// RulesFired / TagsFired from their CAGGs, with the configuration list.
	if len(sum.RulesFired) != 1 || sum.RulesFired[0].Key != "dash-rule" || sum.RulesFired[0].Count != 4 {
		t.Errorf("RulesFired = %+v, want one dash-rule count=4", sum.RulesFired)
	}
	if len(sum.RulesFired) == 1 {
		cfgs := sum.RulesFired[0].UsedByConfigurations
		if len(cfgs) != 1 || cfgs[0] != cfgName {
			t.Errorf("RulesFired configs = %v, want [%s]", cfgs, cfgName)
		}
	}
	if len(sum.TagsFired) != 1 || sum.TagsFired[0].Key != "dash-tag" || sum.TagsFired[0].Count != 4 {
		t.Errorf("TagsFired = %+v, want one dash-tag count=4", sum.TagsFired)
	}

	// ProviderHealth over the recent window: 2 providers, openai with 2 errors.
	health := map[string]store.DashboardProviderHealth{}
	for _, h := range sum.ProviderHealth {
		health[h.Provider] = h
	}
	if got := health["dash-openai"]; got.Requests != 7 || got.TotalErrors != 2 {
		t.Errorf("ProviderHealth[dash-openai] = %+v, want requests=7 errors=2", got)
	}

	// Filter scope: a provider filter narrows the totals to just that provider.
	provParams := params
	provParams.Filter = store.EventFilter{Configuration: cfgName, Provider: "dash-anthropic"}
	provSum, err := st.QueryDashboardSummary(ctx, provParams)
	if err != nil {
		t.Fatalf("QueryDashboardSummary (provider filter): %v", err)
	}
	if provSum.Totals.Requests != 3 || provSum.Totals.RequestsErrored != 0 {
		t.Errorf("provider-filtered totals = %+v, want requests=3 errored=0", provSum.Totals)
	}
	if provSum.Totals.CostUSD < 0.749 || provSum.Totals.CostUSD > 0.751 {
		t.Errorf("provider-filtered CostUSD = %v, want 0.75", provSum.Totals.CostUSD)
	}

	// Filter scope: a tag filter is message-browser-only — the CAGGs carry no
	// tag dimension, so it must be IGNORED for dashboard rollups (totals
	// unchanged from the unfiltered case).
	tagParams := params
	tagParams.Filter = store.EventFilter{Configuration: cfgName, Tags: []string{"dash-tag"}}
	tagSum, err := st.QueryDashboardSummary(ctx, tagParams)
	if err != nil {
		t.Fatalf("QueryDashboardSummary (tag filter): %v", err)
	}
	if tagSum.Totals.Requests != 10 {
		t.Errorf("tag-filtered totals requests = %d, want 10 (tag filter must be ignored)", tagSum.Totals.Requests)
	}

	// Time series: a 1-minute-bucket series over the window must carry the seed
	// bucket's request/error/token sums and zero-fill the rest.
	series, err := st.QueryDashboardSeries(ctx, store.DashboardSeriesParams{
		From:          from,
		To:            to,
		BucketSeconds: 60,
		Filter:        store.EventFilter{Configuration: cfgName},
	})
	if err != nil {
		t.Fatalf("QueryDashboardSeries: %v", err)
	}
	var sawSeed bool
	for _, b := range series {
		if b.Requests == 0 && b.TokensIn == 0 {
			continue // zero-filled empty bucket
		}
		sawSeed = true
		if b.Requests != 10 || b.Errored != 2 || b.TokensIn != 1000 || b.TokensOut != 250 {
			t.Errorf("series seed bucket = %+v, want requests=10 errored=2 in=1000 out=250", b)
		}
		if b.CostUSD < 2.499 || b.CostUSD > 2.501 {
			t.Errorf("series seed bucket CostUSD = %v, want 2.50", b.CostUSD)
		}
	}
	if !sawSeed {
		t.Errorf("series carried no non-empty bucket over the seeded window (got %d buckets)", len(series))
	}
}
