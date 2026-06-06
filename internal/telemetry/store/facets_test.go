package store

import (
	"errors"
	"testing"
)

func TestFacets_Success(t *testing.T) {
	// freshRows makes each of the five dimension queries (provider, model,
	// configuration, protocol, tags) scan one row, so every dst slice is filled.
	q := &fakeQuerier{freshRows: 1}
	f, err := newStore(q).Facets(ctx())
	if err != nil {
		t.Fatalf("facets: %v", err)
	}
	if len(f.Providers) != 1 || len(f.Models) != 1 || len(f.Configurations) != 1 ||
		len(f.Protocols) != 1 || len(f.Tags) != 1 {
		t.Fatalf("facets = %+v", f)
	}
	if q.queryCalls != 5 {
		t.Errorf("query calls = %d, want 5", q.queryCalls)
	}
}

func TestFacets_ScalarError(t *testing.T) {
	// First (provider) query fails -> the loop returns before the tags scan.
	if _, err := newStore(&fakeQuerier{queryErr: errors.New("q")}).Facets(ctx()); err == nil {
		t.Fatal("want scalar query error")
	}
	// Scan error on the first dimension.
	scanErr := &fakeQuerier{query: &fakeRows{scanErrs: []error{errors.New("s")}}}
	if _, err := newStore(scanErr).Facets(ctx()); err == nil {
		t.Fatal("want scalar scan error")
	}
}

func TestFacets_TagsError(t *testing.T) {
	// Let the four scalar queries succeed and fail only the fifth (tags) query,
	// covering the dedicated tags error return.
	q := &fakeQuerier{freshRows: 1, queryFailAt: 5}
	if _, err := newStore(q).Facets(ctx()); err == nil {
		t.Fatal("want tags query error")
	}
}

func TestFacets_RowsErr(t *testing.T) {
	// A row iteration error surfaces from distinctStrings.
	q := &fakeQuerier{query: &fakeRows{scanErrs: []error{nil}, finalErr: errors.New("rows")}}
	if _, err := newStore(q).Facets(ctx()); err == nil {
		t.Fatal("want rows.Err")
	}
}

func TestAppendFilter_SessionCorrelationTags(t *testing.T) {
	where, args := appendFilter(nil, nil, EventFilter{
		SessionID:     "s1",
		CorrelationID: "c1",
		Tags:          []string{"eu", "pii"},
	})
	// session_id + correlation_id + the single jsonb tags param.
	if len(args) != 3 {
		t.Fatalf("args = %v", args)
	}
	joined := ""
	for _, w := range where {
		joined += w + " "
	}
	if !contains(joined, "session_id =") || !contains(joined, "correlation_id =") ||
		!contains(joined, "detail->'tags' @>") {
		t.Errorf("where = %q", joined)
	}
	// The tags arg is a JSON array string binding both tags.
	tagArg, ok := args[2].(string)
	if !ok || !contains(tagArg, `"eu"`) || !contains(tagArg, `"pii"`) {
		t.Errorf("tags arg = %#v", args[2])
	}
}

func TestAppendFilter_EmptyTagsNoPredicate(t *testing.T) {
	where, args := appendFilter(nil, nil, EventFilter{Tags: nil})
	if len(where) != 0 || len(args) != 0 {
		t.Errorf("empty tags added a predicate: where=%v args=%v", where, args)
	}
}
