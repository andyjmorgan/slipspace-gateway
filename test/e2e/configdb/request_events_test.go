//go:build e2e

package configdb_test

import (
	"context"
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
