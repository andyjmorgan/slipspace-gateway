package store

import (
	"context"
	"fmt"
	"time"
)

// rowScanner is the common surface of pgx.Row and pgx.Rows used by the scan
// helpers, so one scan function serves both single-row and iterating queries.
type rowScanner interface {
	Scan(dest ...any) error
}

// nullTime returns nil for the zero time so it lands as SQL NULL (letting a
// COALESCE default apply), otherwise the time itself.
func nullTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t
}

// nullJSON returns nil for empty JSON so the column stays SQL NULL rather than
// an empty value, otherwise the bytes.
func nullJSON(b []byte) any {
	if len(b) == 0 {
		return nil
	}
	return b
}

// commit commits tx, wrapping the error for context.
func commit(ctx context.Context, tx txn) error {
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("store: commit: %w", err)
	}
	return nil
}
