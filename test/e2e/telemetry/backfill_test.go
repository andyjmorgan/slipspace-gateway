//go:build e2e

package telemetry_test

import (
	"context"
	"testing"

	"github.com/andyjmorgan/sluice-gateway/internal/telemetry/store"
)

// TestBackfillTokenColumns_Postgres proves the migration-v10 out-of-band token
// backfill through real Postgres: rows whose promoted tokens_in/tokens_out
// columns predate v10 (0) are re-projected from the span_event blob's
// gen_ai.usage keys, malformed usage values are guarded to 0 rather than
// aborting, rows without usage stay 0, and a second run is a bookkeeping no-op
// (backfill_runs short-circuits before any scan).
//
// The keyset batch size is 2 over five seeded rows so the test crosses batch
// boundaries. The shared database may hold other tests' rows; the backfill
// re-derives those to their already-correct values (idempotent projection), so
// assertions read the seeded rows back rather than trusting the global count.
func TestBackfillTokenColumns_Postgres(t *testing.T) {
	st := migratedStore(t)
	ctx := context.Background()

	seed := func(id, span string) {
		t.Helper()
		// TokensIn/TokensOut left 0 — the pre-v10 shape this backfill exists
		// for: usage only inside the blob, columns at their ADD COLUMN default.
		if err := st.UpsertRequestEvent(ctx, store.RequestEvent{
			CorrelationID: id, StatusCode: 200, SpanEvent: []byte(span),
		}); err != nil {
			t.Fatalf("seed %s: %v", id, err)
		}
	}
	seed("bf-1", `{"gen_ai.usage.input_tokens":11,"gen_ai.usage.output_tokens":3}`)
	seed("bf-2", `{"gen_ai.usage.input_tokens":250,"gen_ai.usage.output_tokens":40}`)
	seed("bf-3", `{"gen_ai.usage.input_tokens":"not a number","gen_ai.usage.output_tokens":7}`)
	seed("bf-4", `{"sluice.method":"POST"}`) // no usage keys -> stays 0
	seed("bf-5", `{"gen_ai.usage.input_tokens":1,"gen_ai.usage.output_tokens":1}`)

	n, err := st.BackfillTokenColumns(ctx, 2)
	if err != nil {
		t.Fatalf("BackfillTokenColumns: %v", err)
	}
	if n < 5 {
		t.Errorf("rows updated = %d, want >= 5 (the seeded rows)", n)
	}

	want := map[string][2]int64{
		"bf-1": {11, 3},
		"bf-2": {250, 40},
		"bf-3": {0, 7}, // numeric guard: malformed input lands 0, valid output projects
		"bf-4": {0, 0},
		"bf-5": {1, 1},
	}
	for id, w := range want {
		got, err := st.GetRequestEvent(ctx, id)
		if err != nil {
			t.Fatalf("GetRequestEvent %s: %v", id, err)
		}
		if got.TokensIn != w[0] || got.TokensOut != w[1] {
			t.Errorf("%s tokens = (%d, %d), want (%d, %d)", id, got.TokensIn, got.TokensOut, w[0], w[1])
		}
	}

	// Run-once: the completion row short-circuits the second invocation.
	n2, err := st.BackfillTokenColumns(ctx, 2)
	if err != nil {
		t.Fatalf("second BackfillTokenColumns: %v", err)
	}
	if n2 != 0 {
		t.Errorf("second run updated %d rows, want 0 (bookkeeping no-op)", n2)
	}
}
