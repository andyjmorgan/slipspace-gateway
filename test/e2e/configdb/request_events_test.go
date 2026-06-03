//go:build e2e

package configdb_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/andyjmorgan/sluice-gateway/internal/controlplane/configdb"
)

// TestConfigDB_RequestEvents exercises the request_events store against real
// Postgres: insert, get-by-correlation-id, recent-list ordering, and the
// two-phase upsert that converges request-then-response telemetry on one row
// while preserving captured gen_ai content.
func TestConfigDB_RequestEvents(t *testing.T) {
	ctx := context.Background()
	db, err := configdb.Open(ctx, startPostgres(t))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(db.Close)

	// Missing event.
	if _, err := db.GetRequestEvent(ctx, "ghost"); err != configdb.ErrRequestEventNotFound {
		t.Fatalf("get missing: err = %v, want ErrRequestEventNotFound", err)
	}

	// Insert several events (newest last).
	for i := 0; i < 3; i++ {
		e := configdb.RequestEvent{
			CorrelationID: fmt.Sprintf("corr-%d", i),
			GatewayID:     "beta-sluice",
			Configuration: "production",
			Backend:       "openai",
			Model:         "gpt-4o",
			Protocol:      "chat",
			StatusCode:    200,
			LatencyMs:     int64(100 + i),
			TokensIn:      int64(10 + i),
			TokensOut:     int64(20 + i),
			GenAIContent:  []byte(fmt.Sprintf(`{"prompt":"hi %d"}`, i)),
		}
		if err := db.UpsertRequestEvent(ctx, e); err != nil {
			t.Fatalf("upsert %d: %v", i, err)
		}
	}

	got, err := db.GetRequestEvent(ctx, "corr-1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Model != "gpt-4o" || got.TokensOut != 21 || got.ObservedAt.IsZero() {
		t.Fatalf("get = %+v", got)
	}

	// Two-phase update: a later partial event refines metrics but omits content;
	// the captured gen_ai content must survive (COALESCE).
	if err := db.UpsertRequestEvent(ctx, configdb.RequestEvent{
		CorrelationID: "corr-1",
		GatewayID:     "beta-sluice",
		Model:         "gpt-4o",
		StatusCode:    200,
		TokensOut:     99,
		// GenAIContent omitted (nil)
	}); err != nil {
		t.Fatalf("two-phase upsert: %v", err)
	}
	refined, _ := db.GetRequestEvent(ctx, "corr-1")
	if refined.TokensOut != 99 {
		t.Errorf("refined tokens_out = %d, want 99", refined.TokensOut)
	}
	if len(refined.GenAIContent) == 0 {
		t.Error("gen_ai content lost on metric-only update — COALESCE not preserving it")
	}

	// Recent list is newest-first and capped.
	recent, err := db.ListRecentRequestEvents(ctx, 2)
	if err != nil {
		t.Fatalf("list recent: %v", err)
	}
	if len(recent) != 2 {
		t.Fatalf("recent len = %d, want 2", len(recent))
	}
}

// TestConfigDB_RequestEvents_Enrichment exercises the fleet-enrichment columns
// (cache tokens, session, api-key, method, policy, upstream status, streaming,
// and the detail JSONB) through real Postgres, including the two-phase upsert
// preserving captured detail across a metric-only refine (COALESCE).
func TestConfigDB_RequestEvents_Enrichment(t *testing.T) {
	ctx := context.Background()
	db, err := configdb.Open(ctx, startPostgres(t))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(db.Close)

	full := configdb.RequestEvent{
		CorrelationID:       "enrich-1",
		GatewayID:           "beta-sluice",
		Configuration:       "production",
		Backend:             "anthropic",
		Model:               "claude-sonnet",
		Protocol:            "messages",
		Method:              "POST",
		StatusCode:          200,
		UpstreamStatus:      502,
		LatencyMs:           42,
		TokensIn:            100,
		TokensOut:           20,
		TokensCached:        64,
		TokensCacheCreation: 8,
		SessionID:           "bundle-9",
		SessionIDSource:     "X-Agentling-Task-Id",
		APIKeyName:          "internal-svc",
		PolicyRef:           "failover-pool",
		Streaming:           true,
		Detail:              []byte(`{"tags":["env:prod"],"rules_fired":["redirect-claude"]}`),
	}
	if err := db.UpsertRequestEvent(ctx, full); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	got, err := db.GetRequestEvent(ctx, "enrich-1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Method != "POST" || got.UpstreamStatus != 502 || !got.Streaming {
		t.Errorf("scalars = %+v", got)
	}
	if got.TokensCached != 64 || got.TokensCacheCreation != 8 {
		t.Errorf("cache tokens = (%d,%d)", got.TokensCached, got.TokensCacheCreation)
	}
	if got.SessionID != "bundle-9" || got.SessionIDSource != "X-Agentling-Task-Id" {
		t.Errorf("session = (%q,%q)", got.SessionID, got.SessionIDSource)
	}
	if got.APIKeyName != "internal-svc" || got.PolicyRef != "failover-pool" {
		t.Errorf("apikey/policy = (%q,%q)", got.APIKeyName, got.PolicyRef)
	}
	var d configdb.EventDetail
	if err := json.Unmarshal(got.Detail, &d); err != nil {
		t.Fatalf("detail unmarshal: %v (raw %s)", err, got.Detail)
	}
	if len(d.Tags) != 1 || d.Tags[0] != "env:prod" || len(d.RulesFired) != 1 || d.RulesFired[0] != "redirect-claude" {
		t.Errorf("detail = %+v", d)
	}

	// Two-phase: a metric-only refine omitting detail must preserve it (COALESCE).
	if err := db.UpsertRequestEvent(ctx, configdb.RequestEvent{
		CorrelationID: "enrich-1",
		Backend:       "anthropic",
		Model:         "claude-sonnet",
		StatusCode:    200,
		TokensOut:     99,
		// Detail omitted (nil)
	}); err != nil {
		t.Fatalf("two-phase upsert: %v", err)
	}
	refined, _ := db.GetRequestEvent(ctx, "enrich-1")
	if refined.TokensOut != 99 {
		t.Errorf("refined tokens_out = %d, want 99", refined.TokensOut)
	}
	if len(refined.Detail) == 0 {
		t.Error("detail lost on metric-only update — COALESCE not preserving it")
	}
}
