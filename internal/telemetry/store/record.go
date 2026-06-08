package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// ErrRecordNotFound is returned when a correlation id has no stored record —
// reporting forwarding was off for the request, or the push has not arrived.
var ErrRecordNotFound = errors.New("store: record not found")

// UpsertRecord stores one HMAC-verified cc.Record push verbatim, keyed by
// correlation_id. body is the raw request bytes exactly as received (the
// signature was verified over these bytes); it is immutable and deserialized
// lazily when the record/inspector tab opens. A re-push overwrites in place.
func (s *Store) UpsertRecord(ctx context.Context, correlationID string, receivedAt time.Time, body []byte) error {
	_, err := s.db.Exec(ctx, `
INSERT INTO record (correlation_id, received_at, body)
VALUES ($1, COALESCE($2, now()), $3)
ON CONFLICT (correlation_id) DO UPDATE SET
  received_at = COALESCE(EXCLUDED.received_at, record.received_at),
  body        = EXCLUDED.body`,
		correlationID, nullTime(receivedAt), body)
	if err != nil {
		return fmt.Errorf("store: upsert record: %w", err)
	}
	return nil
}

// GetRecordBody returns the verbatim record body for a correlation id, or
// ErrRecordNotFound when no record was pushed. The caller json.Unmarshals it
// into cc.Record to render the rule lifecycle + raw bodies/headers.
func (s *Store) GetRecordBody(ctx context.Context, correlationID string) ([]byte, error) {
	var body []byte
	err := s.db.QueryRow(ctx,
		`SELECT body FROM record WHERE correlation_id=$1`, correlationID).Scan(&body)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrRecordNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("store: get record body: %w", err)
	}
	return body, nil
}
