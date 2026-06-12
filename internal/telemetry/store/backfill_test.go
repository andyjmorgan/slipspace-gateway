package store

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// stringRow scans v into a single *string dest (the page-bound read); err
// short-circuits the scan.
type stringRow struct {
	v   string
	err error
}

func (r stringRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	if len(dest) == 1 {
		if p, ok := dest[0].(*string); ok {
			*p = r.v
		}
	}
	return nil
}

// backfillQuerier is the hand-rolled boundary fake for BackfillTokenColumns:
// it routes QueryRow by statement (completion check vs page bound), captures
// every Exec, and reports a fixed RowsAffected per UPDATE.
type backfillQuerier struct {
	fakeQuerier
	completed    bool  // completion row already present
	completedErr error // non-ErrNoRows failure on the completion check
	bounds       []string
	boundErr     error // failure on the page-bound read
	boundCalls   int
	boundArgs    [][]any
	execSQL      []string
	execArgs     [][]any
	updateErr    error
	recordErr    error
	affected     int64
}

func (q *backfillQuerier) QueryRow(_ context.Context, sql string, args ...any) rowScanner {
	if strings.Contains(sql, "backfill_runs") {
		if q.completedErr != nil {
			return stringRow{err: q.completedErr}
		}
		if q.completed {
			return stringRow{v: backfillTokenColumnsName}
		}
		return stringRow{err: pgx.ErrNoRows}
	}
	// Page-bound read: yield the next configured bound, then no-more-rows.
	q.boundArgs = append(q.boundArgs, args)
	q.boundCalls++
	if q.boundErr != nil {
		return stringRow{err: q.boundErr}
	}
	if q.boundCalls <= len(q.bounds) {
		return stringRow{v: q.bounds[q.boundCalls-1]}
	}
	return stringRow{err: pgx.ErrNoRows}
}

func (q *backfillQuerier) Exec(_ context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	q.execSQL = append(q.execSQL, sql)
	q.execArgs = append(q.execArgs, args)
	if strings.Contains(sql, "backfill_runs") {
		return pgconn.CommandTag{}, q.recordErr
	}
	if q.updateErr != nil {
		return pgconn.CommandTag{}, q.updateErr
	}
	return pgconn.NewCommandTag(fmt.Sprintf("UPDATE %d", q.affected)), nil
}

func TestBackfillTokenColumns(t *testing.T) {
	cases := map[string]struct {
		q         *backfillQuerier
		batch     int
		wantRows  int64
		wantErr   bool
		wantBatch [][2]string // expected (cursor, hi) arg pairs of the UPDATEs
	}{
		"already completed is a no-op": {
			q:        &backfillQuerier{completed: true},
			wantRows: 0,
		},
		"single final batch": {
			// No page bound -> one open-ended UPDATE above the empty cursor.
			q:         &backfillQuerier{affected: 5},
			wantRows:  5,
			wantBatch: [][2]string{{"", ""}},
		},
		"multiple keyset batches": {
			// Bounds c100/c200 then exhausted -> three slices: ("", c100],
			// (c100, c200], (c200, open).
			q:         &backfillQuerier{bounds: []string{"c100", "c200"}, affected: 2},
			wantRows:  6,
			wantBatch: [][2]string{{"", "c100"}, {"c100", "c200"}, {"c200", ""}},
		},
		"bookkeeping read error": {
			q:       &backfillQuerier{completedErr: errors.New("boom")},
			wantErr: true,
		},
		"page bound error": {
			q:       &backfillQuerier{boundErr: errors.New("boom")},
			wantErr: true,
		},
		"update error": {
			q:       &backfillQuerier{updateErr: errors.New("boom")},
			wantErr: true,
		},
		"bookkeeping write error": {
			q:       &backfillQuerier{recordErr: errors.New("boom")},
			wantErr: true,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := newStore(tc.q).BackfillTokenColumns(ctx(), tc.batch)
			if tc.wantErr {
				if err == nil {
					t.Fatal("want error")
				}
				return
			}
			if err != nil {
				t.Fatalf("err: %v", err)
			}
			if got != tc.wantRows {
				t.Errorf("rows = %d, want %d", got, tc.wantRows)
			}
			var updates [][2]string
			sawRecord := false
			for i, sql := range tc.q.execSQL {
				if strings.Contains(sql, "backfill_runs") {
					sawRecord = true
					continue
				}
				args := tc.q.execArgs[i]
				if len(args) != 2 {
					t.Fatalf("update args = %v", args)
				}
				updates = append(updates, [2]string{args[0].(string), args[1].(string)})
			}
			if len(updates) != len(tc.wantBatch) {
				t.Fatalf("updates = %v, want %v", updates, tc.wantBatch)
			}
			for i := range updates {
				if updates[i] != tc.wantBatch[i] {
					t.Errorf("update %d = %v, want %v", i, updates[i], tc.wantBatch[i])
				}
			}
			// Completion is recorded iff work ran to the end.
			if wantRecord := len(tc.wantBatch) > 0; sawRecord != wantRecord {
				t.Errorf("completion recorded = %v, want %v", sawRecord, wantRecord)
			}
		})
	}
}

func TestBackfillTokenColumns_DefaultBatch(t *testing.T) {
	// batch <= 0 defaults to 500; the page bound binds OFFSET batch-1 = 499.
	q := &backfillQuerier{bounds: []string{"c1"}, affected: 1}
	if _, err := newStore(q).BackfillTokenColumns(ctx(), 0); err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(q.boundArgs) == 0 || len(q.boundArgs[0]) != 2 || q.boundArgs[0][1] != 499 {
		t.Errorf("page-bound args = %v, want OFFSET arg 499", q.boundArgs)
	}
}

func TestBackfillTokenColumns_ContextCancelled(t *testing.T) {
	c, cancel := context.WithCancel(context.Background())
	cancel()
	q := &backfillQuerier{}
	if _, err := newStore(q).BackfillTokenColumns(c, 10); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if len(q.execSQL) != 0 {
		t.Errorf("no statements should run after cancel, got %v", q.execSQL)
	}
}

func TestBackfillTags(t *testing.T) {
	// One open-ended UPDATE (no page bounds) running the tags projection, then
	// the completion record under the tags bookkeeping name.
	q := &backfillQuerier{affected: 7}
	got, err := newStore(q).BackfillTags(ctx(), 0)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != 7 {
		t.Errorf("rows = %d, want 7", got)
	}
	var sawUpdate, sawRecord bool
	for i, sql := range q.execSQL {
		switch {
		case strings.Contains(sql, "backfill_runs"):
			sawRecord = true
			if name, _ := q.execArgs[i][0].(string); name != backfillTagsColumnName {
				t.Errorf("recorded name = %q, want %q", name, backfillTagsColumnName)
			}
		case strings.Contains(sql, "span_event->'tags'"):
			sawUpdate = true
		default:
			t.Errorf("unexpected exec: %q", sql)
		}
	}
	if !sawUpdate {
		t.Error("tags projection UPDATE never ran")
	}
	if !sawRecord {
		t.Error("completion not recorded")
	}
}

// TestStoreSQL_UsesLiveV10Columns is the regression tripwire for the
// 2026-06-10 prod errors (`column "streaming" does not exist`, `column "blob"
// does not exist`): every package SQL constant that touches request_events or
// record must speak the live v10 vocabulary — no pre-migration-6 columns
// (streaming/detail/gen_ai_content live INSIDE span_event now), and the record
// payload column is `body`, never `blob`.
func TestStoreSQL_UsesLiveV10Columns(t *testing.T) {
	stmts := map[string]string{
		"requestEventColumns":    requestEventColumns,
		"insertEventSQL":         insertEventSQL,
		"backfillPageSQL":        backfillPageSQL,
		"backfillTokenUpdateSQL": backfillTokenUpdateSQL,
		"backfillTagsUpdateSQL":  backfillTagsUpdateSQL,
		"backfillCompletedSQL":   backfillCompletedSQL,
		"backfillRecordSQL":      backfillRecordSQL,
		"createSchemaMigrations": createSchemaMigrations,
	}
	dropped := regexp.MustCompile(`\b(streaming|detail|gen_ai_content|latency_ms|blob)\b`)
	for name, sql := range stmts {
		if m := dropped.FindString(sql); m != "" {
			t.Errorf("%s references dropped/nonexistent column %q", name, m)
		}
	}
	// The token backfill must project from the span blob into the promoted columns.
	for _, want := range []string{"tokens_in", "tokens_out", "span_event->>'gen_ai.usage.input_tokens'", "span_event->>'gen_ai.usage.output_tokens'"} {
		if !strings.Contains(backfillTokenUpdateSQL, want) {
			t.Errorf("backfillTokenUpdateSQL missing %q", want)
		}
	}
	// The tags backfill must project the blob's tags array into the tags column.
	for _, want := range []string{"tags =", "span_event->'tags'"} {
		if !strings.Contains(backfillTagsUpdateSQL, want) {
			t.Errorf("backfillTagsUpdateSQL missing %q", want)
		}
	}
}
