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

// backfillTagsColumnName is the backfill_runs key for the migration-v12 tags
// column backfill. One row with this name means every request_events row that
// predated v12 has had its tags text[] column re-projected from span_event.
const backfillTagsColumnName = "v12_tags_column"

// backfillCostColumnName is the backfill_runs key for the migration-v20 cost
// column backfill. One row with this name means every request_events row that
// predated v20 has had cost_usd re-projected from span_event.
const backfillCostColumnName = "v20_cost_column"

// backfillPageSQL fetches the inclusive upper bound of the next keyset batch:
// the batch-th correlation_id above the cursor ($2 binds batch-1 as the OFFSET).
// No row means fewer than a full batch remain, so the final UPDATE runs
// open-ended above the cursor. Shared by every column backfill (keyset is over
// correlation_id, independent of which column is re-projected).
const backfillPageSQL = `
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
// `body`) — see docs/arbiter-database-schema.md.
const backfillTokenUpdateSQL = `
UPDATE request_events SET
  tokens_in  = CASE WHEN span_event->>'gen_ai.usage.input_tokens'  ~ '^-?[0-9]+$'
                    THEN (span_event->>'gen_ai.usage.input_tokens')::bigint  ELSE 0 END,
  tokens_out = CASE WHEN span_event->>'gen_ai.usage.output_tokens' ~ '^-?[0-9]+$'
                    THEN (span_event->>'gen_ai.usage.output_tokens')::bigint ELSE 0 END
WHERE correlation_id > $1 AND ($2 = '' OR correlation_id <= $2)`

// backfillTagsUpdateSQL re-projects the tags text[] column from span_event for
// one (lo, hi] keyset slice — the v12 backfill that migration 12 deferred
// out-of-band. It flattens the blob's tags array to a sorted, de-duplicated,
// empty-string-free text[]; a row whose blob carries no tags lands the empty
// array (the column default), so re-running is idempotent. Touching span_event
// detoasts the row, which is why this runs in bounded batches after the pod is
// serving, never inside Migrate() — the exact 30 s scan the column exists to
// avoid.
const backfillTagsUpdateSQL = `
UPDATE request_events SET
  tags = COALESCE(
    (SELECT array_agg(DISTINCT t ORDER BY t)
     FROM jsonb_array_elements_text(span_event->'tags') AS t WHERE t <> ''),
    '{}')
WHERE correlation_id > $1 AND ($2 = '' OR correlation_id <= $2)`

// backfillCostUpdateSQL re-projects cost_usd from span_event for one (lo, hi]
// keyset slice — the v20 backfill that migration 20 deferred out-of-band. The
// gateway stamps slipspace.cost.usd as a JSON number; jsonb_typeof guards the
// cast so a malformed value (or an unpriced/uncosted row with no key) lands as
// 0 rather than aborting the batch. Same detoast rationale as the token
// backfill: bounded batches after the pod is serving, never inside Migrate().
const backfillCostUpdateSQL = `
UPDATE request_events SET
  cost_usd = CASE WHEN jsonb_typeof(span_event->'slipspace.cost.usd') = 'number'
                  THEN (span_event->>'slipspace.cost.usd')::double precision ELSE 0 END
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
// migration. Returns the number of rows updated. See backfillColumn for the
// keyset/idempotency contract.
func (s *Store) BackfillTokenColumns(ctx context.Context, batch int) (int64, error) {
	return s.backfillColumn(ctx, batch, backfillTokenColumnsName, backfillTokenUpdateSQL)
}

// BackfillTags re-projects the promoted tags text[] column from span_event for
// every row that predates migration v12 — the out-of-band step migration 12
// deferred so the metadata-only ADD COLUMN never detoasts inside Migrate().
// Returns the number of rows updated. See backfillColumn for the contract.
func (s *Store) BackfillTags(ctx context.Context, batch int) (int64, error) {
	return s.backfillColumn(ctx, batch, backfillTagsColumnName, backfillTagsUpdateSQL)
}

// BackfillCost re-projects the promoted cost_usd column from span_event for
// every row that predates migration v20 — the out-of-band step migration 20
// deferred so the metadata-only ADD COLUMN never detoasts inside Migrate().
// Returns the number of rows updated. See backfillColumn for the contract.
func (s *Store) BackfillCost(ctx context.Context, batch int) (int64, error) {
	return s.backfillColumn(ctx, batch, backfillCostColumnName, backfillCostUpdateSQL)
}

// backfillColumn walks request_events in correlation_id keyset batches of batch
// rows (defaulted to 500 when <= 0), running updateSQL as one bounded UPDATE per
// batch in its own implicit transaction, and records completion under name in
// backfill_runs so subsequent boots skip the scan entirely. Returns the number
// of rows updated.
//
// It is safe to run while the service is serving: each batch touches at most
// batch rows, ingest upserts land the same projection, and re-running over an
// already-backfilled slice recomputes identical values (updateSQL must be
// idempotent). Cancellation between batches leaves the table partially
// backfilled and the completion row unwritten, so the next boot resumes from the
// start (idempotently).
func (s *Store) backfillColumn(ctx context.Context, batch int, name, updateSQL string) (int64, error) {
	if batch <= 0 {
		batch = 500
	}

	var done string
	err := s.db.QueryRow(ctx, backfillCompletedSQL, name).Scan(&done)
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
		err := s.db.QueryRow(ctx, backfillPageSQL, cursor, batch-1).Scan(&hi)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return total, fmt.Errorf("store: backfill page bound: %w", err)
		}

		tag, uerr := s.db.Exec(ctx, updateSQL, cursor, hi)
		if uerr != nil {
			return total, fmt.Errorf("store: backfill %s: %w", name, uerr)
		}
		total += tag.RowsAffected()

		if hi == "" {
			break
		}
		cursor = hi
	}

	if _, err := s.db.Exec(ctx, backfillRecordSQL, name); err != nil {
		return total, fmt.Errorf("store: backfill bookkeeping write: %w", err)
	}
	return total, nil
}
