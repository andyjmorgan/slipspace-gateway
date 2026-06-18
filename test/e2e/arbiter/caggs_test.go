//go:build e2e

package arbiter_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// TestContinuousAggregates_BuildRefreshAggregate proves migration 0007 lands on
// the real TimescaleDB engine: the four continuous aggregates build (migratedStore
// fatals otherwise), materialize on refresh, and project the meter labels into the
// dashboard's count/sum rollups. It seeds metric_points across the four meter
// names, refreshes each CAGG, and asserts the aggregated rows — the contract @100's
// dashboard queries depend on.
func TestContinuousAggregates_BuildRefreshAggregate(t *testing.T) {
	// migratedStore applies all migrations including 0007 (CREATE EXTENSION +
	// the four CAGGs); a malformed continuous-aggregate DDL fails here.
	_ = migratedStore(t)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Simple protocol so CALL refresh_continuous_aggregate (which must not run
	// inside an extended-protocol implicit transaction) executes cleanly.
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

	// Seed inside one bucket, comfortably in the past so it falls within the
	// refresh window (end_offset is 1 minute).
	at := time.Now().Add(-10 * time.Minute).UTC()
	insert := func(name, labels string, value float64) {
		t.Helper()
		if _, err := conn.Exec(ctx,
			`INSERT INTO metric_points (metric_name, labels, value, observed_at) VALUES ($1, $2::jsonb, $3, $4)`,
			name, labels, value, at); err != nil {
			t.Fatalf("insert %s: %v", name, err)
		}
	}

	const base = `"gen_ai.provider.name":"openai","gen_ai.request.model":"gpt-4o","slipspace.configuration":"production","slipspace.protocol":"chat"`
	// requests: 3 ok + 1 errored, same dimension set.
	insert("slipspace.requests.total", `{`+base+`,"http.response.status_code":"200"}`, 3)
	insert("slipspace.requests.total", `{`+base+`,"http.response.status_code":"500"}`, 1)
	// tokens: input + output counters.
	insert("slipspace.tokens.input.total", `{`+base+`}`, 100)
	insert("slipspace.tokens.output.total", `{`+base+`}`, 40)
	// rule + tag meters.
	insert("slipspace.rule.fired", `{"rule_name":"tag-claude-code","slipspace.configuration":"production"}`, 2)
	insert("gateway.tags.applied.total", `{"tag":"Claude-Code","slipspace.configuration":"production"}`, 2)

	for _, cagg := range []string{"cagg_requests_1m", "cagg_tokens_1m", "cagg_rules_1m", "cagg_tags_1m"} {
		if _, err := conn.Exec(ctx, `CALL refresh_continuous_aggregate($1, NULL, NULL)`, cagg); err != nil {
			t.Fatalf("refresh %s: %v", cagg, err)
		}
	}

	// requests: total over the dimension, and the errored subset by status_code.
	var total, errored float64
	if err := conn.QueryRow(ctx,
		`SELECT COALESCE(sum(requests),0) FROM cagg_requests_1m WHERE provider='openai' AND model='gpt-4o' AND configuration='production' AND protocol='chat'`).
		Scan(&total); err != nil {
		t.Fatalf("query requests total: %v", err)
	}
	if total != 4 {
		t.Errorf("requests total = %v, want 4", total)
	}
	if err := conn.QueryRow(ctx,
		`SELECT COALESCE(sum(requests),0) FROM cagg_requests_1m WHERE status_code >= '400'`).Scan(&errored); err != nil {
		t.Fatalf("query requests errored: %v", err)
	}
	if errored != 1 {
		t.Errorf("errored requests = %v, want 1", errored)
	}

	// tokens: pivot by metric_name.
	tokens := map[string]float64{}
	rows, err := conn.Query(ctx,
		`SELECT metric_name, sum(tokens) FROM cagg_tokens_1m WHERE provider='openai' GROUP BY metric_name`)
	if err != nil {
		t.Fatalf("query tokens: %v", err)
	}
	for rows.Next() {
		var name string
		var v float64
		if err := rows.Scan(&name, &v); err != nil {
			rows.Close()
			t.Fatalf("scan tokens: %v", err)
		}
		tokens[name] = v
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		t.Fatalf("tokens rows: %v", err)
	}
	if tokens["slipspace.tokens.input.total"] != 100 {
		t.Errorf("input tokens = %v, want 100", tokens["slipspace.tokens.input.total"])
	}
	if tokens["slipspace.tokens.output.total"] != 40 {
		t.Errorf("output tokens = %v, want 40", tokens["slipspace.tokens.output.total"])
	}

	// rules + tags.
	var fired, applied float64
	if err := conn.QueryRow(ctx,
		`SELECT COALESCE(sum(fired),0) FROM cagg_rules_1m WHERE rule_name='tag-claude-code'`).Scan(&fired); err != nil {
		t.Fatalf("query rules: %v", err)
	}
	if fired != 2 {
		t.Errorf("rule fired = %v, want 2", fired)
	}
	if err := conn.QueryRow(ctx,
		`SELECT COALESCE(sum(applied),0) FROM cagg_tags_1m WHERE tag='Claude-Code'`).Scan(&applied); err != nil {
		t.Fatalf("query tags: %v", err)
	}
	if applied != 2 {
		t.Errorf("tag applied = %v, want 2", applied)
	}
}
