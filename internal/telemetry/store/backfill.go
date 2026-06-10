package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// backfillTokenColumnsName is the backfill_runs key for the migration-v10
// token-column backfill. One row with this name means every request_events row
// that predated v10 has had tokens_in/tokens_out re-projected from span_event.
const backfillTokenColumnsName = "v10_token_columns"

// backfillTokenPageSQL fetches the inclusive upper bound of the next keyset
// batch: the batch-th correlation_id above the cursor ($2 binds batch-1 as the
// OFFSET). No row means fewer than a full batch remain, so the final UPDATE
// runs open-ended above the cursor.
//
//nolint:gosec // "Token" in the name trips the credential heuristic; this is SQL, not a secret
const backfillTokenPageSQL = `
SELECT correlation_id FROM request_events
WHERE correlation_id > $1
ORDER BY correlation_id
OFFSET $2 LIMIT 1`

// backfillTokenUpdateSQL re-projects tokens_in/tokens_out from span_event for
// one (lo, hi] keyset slice — the v10 backfill that migration 10 deferred
// out-of-band. It is the same numeric-guarded cast the inline v10 backfill
// used before #327 dropped it: a malformed or absent usage value lands as 0
// rather than aborting, and rows whose blob carries no usage keys stay 0.
// Touching span_event detoasts the row, which is exactly why this runs in
// bounded batches after the pod is serving, never inside Migrate().
//
// Column names are the live v10 schema: the usage lives under the
// gen_ai.usage.* keys INSIDE span_event (there has been no `streaming` column
// since migration 6 rebuilt the entity, and the record payload column is
// `body`) — see docs/telemetry-database-schema.md.
const backfillTokenUpdateSQL = `
UPDATE request_events SET
  tokens_in  = CASE WHEN span_event->>'gen_ai.usage.input_tokens'  ~ '^-?[0-9]+$'
                    THEN (span_event->>'gen_ai.usage.input_tokens')::bigint  ELSE 0 END,
  tokens_out = CASE WHEN span_event->>'gen_ai.usage.output_tokens' ~ '^-?[0-9]+$'
                    THEN (span_event->>'gen_ai.usage.output_tokens')::bigint ELSE 0 END
WHERE correlation_id > $1 AND ($2 = '' OR correlation_id <= $2)`

// backfillCompletedSQL / backfillRecordSQL read and write the bookkeeping row
// that makes the backfill run-once. ON CONFLICT DO NOTHING keeps a concurrent
// replica's duplicate completion harmless (the work itself is idempotent —
// re-deriving a row writes the same values).
const (
	backfillCompletedSQL = `SELECT name FROM backfill_runs WHERE name = $1`
	backfillRecordSQL    = `INSERT INTO backfill_runs (name) VALUES ($1) ON CONFLICT (name) DO NOTHING`
)

// BackfillTokenColumns re-projects the promoted tokens_in/tokens_out columns
// from span_event for every row that predates migration v10 — the "batched,
// after the pod is serving" operational step that #327 deferred out of the
// migration. It walks the table in correlation_id keyset batches of batch rows
// (defaulted to 500 when <= 0), each batch one bounded UPDATE in its own
// implicit transaction, and records completion in backfill_runs so subsequent
// boots skip the scan entirely. Returns the number of rows updated.
//
// It is safe to run while the service is serving: each batch touches at most
// batch rows, ingest upserts land the same projection, and re-running over an
// already-backfilled slice recomputes identical values. Cancellation between
// batches leaves the table partially backfilled and the completion row
// unwritten, so the next boot resumes from the start (idempotently).
func (s *Store) BackfillTokenColumns(ctx context.Context, batch int) (int64, error) {
	if batch <= 0 {
		batch = 500
	}

	var name string
	err := s.db.QueryRow(ctx, backfillCompletedSQL, backfillTokenColumnsName).Scan(&name)
	switch {
	case err == nil:
		return 0, nil // already completed on a previous boot
	case !errors.Is(err, pgx.ErrNoRows):
		return 0, fmt.Errorf("store: backfill bookkeeping read: %w", err)
	}

	var total int64
	cursor := ""
	for {
		if err := ctx.Err(); err != nil {
			return total, err
		}

		// Inclusive upper bound of this batch (the batch-th row above the
		// cursor); "" means fewer than batch rows remain and the UPDATE below
		// runs open-ended (the final slice).
		hi := ""
		err := s.db.QueryRow(ctx, backfillTokenPageSQL, cursor, batch-1).Scan(&hi)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return total, fmt.Errorf("store: backfill page bound: %w", err)
		}

		tag, uerr := s.db.Exec(ctx, backfillTokenUpdateSQL, cursor, hi)
		if uerr != nil {
			return total, fmt.Errorf("store: backfill token columns: %w", uerr)
		}
		total += tag.RowsAffected()

		if hi == "" {
			break
		}
		cursor = hi
	}

	if _, err := s.db.Exec(ctx, backfillRecordSQL, backfillTokenColumnsName); err != nil {
		return total, fmt.Errorf("store: backfill bookkeeping write: %w", err)
	}
	return total, nil
}
