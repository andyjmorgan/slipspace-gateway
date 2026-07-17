package store

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestFacets_Success(t *testing.T) {
	// freshRows makes each of the five dimension queries (provider, model,
	// configuration, protocol, tags) scan one row, so every dst slice is filled.
	q := &fakeQuerier{freshRows: 1}
	f, err := newStore(q).Facets(ctx(), time.Time{}, time.Time{})
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
	if _, err := newStore(&fakeQuerier{queryErr: errors.New("q")}).Facets(ctx(), time.Time{}, time.Time{}); err == nil {
		t.Fatal("want scalar query error")
	}
	// Scan error on the first dimension.
	scanErr := &fakeQuerier{query: &fakeRows{scanErrs: []error{errors.New("s")}}}
	if _, err := newStore(scanErr).Facets(ctx(), time.Time{}, time.Time{}); err == nil {
		t.Fatal("want scalar scan error")
	}
}

func TestFacets_TagsError(t *testing.T) {
	// Let the four scalar queries succeed and fail only the fifth (tags) query,
	// covering the dedicated tags error return.
	q := &fakeQuerier{freshRows: 1, queryFailAt: 5}
	if _, err := newStore(q).Facets(ctx(), time.Time{}, time.Time{}); err == nil {
		t.Fatal("want tags query error")
	}
}

func TestFacets_RowsErr(t *testing.T) {
	// A row iteration error surfaces from distinctStrings.
	q := &fakeQuerier{query: &fakeRows{scanErrs: []error{nil}, finalErr: errors.New("rows")}}
	if _, err := newStore(q).Facets(ctx(), time.Time{}, time.Time{}); err == nil {
		t.Fatal("want rows.Err")
	}
}

func TestFacets_WindowBounds(t *testing.T) {
	// With both bounds set, every dimension query (scalar + tags) carries the
	// observed_at window predicates; without bounds, none do.
	from := time.Date(2026, 7, 17, 10, 0, 0, 0, time.UTC)
	to := from.Add(time.Hour)
	q := &fakeQuerier{freshRows: 1}
	if _, err := newStore(q).Facets(ctx(), from, to); err != nil {
		t.Fatalf("facets: %v", err)
	}
	if len(q.querySQL) != 5 {
		t.Fatalf("query calls = %d, want 5", len(q.querySQL))
	}
	for i, sql := range q.querySQL {
		if !strings.Contains(sql, "observed_at >= $1") || !strings.Contains(sql, "observed_at < $2") {
			t.Errorf("query %d missing window bounds: %q", i, sql)
		}
	}

	// From-only: a single open-ended bound.
	q = &fakeQuerier{freshRows: 1}
	if _, err := newStore(q).Facets(ctx(), from, time.Time{}); err != nil {
		t.Fatalf("facets from-only: %v", err)
	}
	for i, sql := range q.querySQL {
		if !strings.Contains(sql, "observed_at >= $1") || strings.Contains(sql, "observed_at < ") {
			t.Errorf("query %d wrong from-only bounds: %q", i, sql)
		}
	}

	// Unbounded: no window predicate at all.
	q = &fakeQuerier{freshRows: 1}
	if _, err := newStore(q).Facets(ctx(), time.Time{}, time.Time{}); err != nil {
		t.Fatalf("facets unbounded: %v", err)
	}
	for i, sql := range q.querySQL {
		if strings.Contains(sql, "observed_at") {
			t.Errorf("query %d has unexpected window bound: %q", i, sql)
		}
	}
}

func TestAppendFilter_SessionCorrelationTags(t *testing.T) {
	where, args := appendFilter(nil, nil, EventFilter{
		SessionID:            "s1",
		CorrelationID:        "c1",
		ConversationID:       "cv1",
		ParentConversationID: "p1",
		AgentID:              "a1",
		UserID:               "u1",
		Tags:                 []string{"eu", "pii"},
	})
	// session_id + correlation_id + conversation_id + parent_conversation_id +
	// agent_id + user_id + the single text[] tags param.
	if len(args) != 7 {
		t.Fatalf("args = %v", args)
	}
	joined := ""
	for _, w := range where {
		joined += w + " "
	}
	if !contains(joined, "session_id =") || !contains(joined, "correlation_id =") ||
		!contains(joined, "conversation_id =") || !contains(joined, "parent_conversation_id =") ||
		!contains(joined, "agent_id =") || !contains(joined, "user_id =") || !contains(joined, "tags @>") {
		t.Errorf("where = %q", joined)
	}
	// The tags arg is the requested set bound directly as a []string (pgx encodes
	// it as text[] for the @> containment).
	tagArg, ok := args[6].([]string)
	if !ok || len(tagArg) != 2 || tagArg[0] != "eu" || tagArg[1] != "pii" {
		t.Errorf("tags arg = %#v", args[6])
	}
}

func TestAppendFilter_EmptyTagsNoPredicate(t *testing.T) {
	where, args := appendFilter(nil, nil, EventFilter{Tags: nil})
	if len(where) != 0 || len(args) != 0 {
		t.Errorf("empty tags added a predicate: where=%v args=%v", where, args)
	}
}

func TestAppendFilter_MultiValueDimensions(t *testing.T) {
	// Each categorical dimension binds its full value set as one []string param
	// behind = ANY($N) (OR within the dimension).
	where, args := appendFilter(nil, nil, EventFilter{
		Providers:      []string{"openai", "anthropic"},
		Models:         []string{"m1"},
		Configurations: []string{"prod", "staging"},
		Protocols:      []string{"chat_completions"},
	})
	if len(args) != 4 {
		t.Fatalf("args = %v", args)
	}
	joined := strings.Join(where, " ")
	for _, want := range []string{"configuration = ANY(", "model = ANY(", "provider = ANY(", "protocol = ANY("} {
		if !contains(joined, want) {
			t.Errorf("where %q missing %q", joined, want)
		}
	}
	// appendFilter emits the dimensions in configuration/model/provider/protocol
	// order; the provider arg is the third and carries both values.
	prov, ok := args[2].([]string)
	if !ok || len(prov) != 2 || prov[0] != "openai" || prov[1] != "anthropic" {
		t.Errorf("provider arg = %#v", args[2])
	}
}

func TestAppendFilter_ScalarFallsBackToAny(t *testing.T) {
	// A lone scalar (legacy construction) degrades to a one-element ANY so its
	// meaning is preserved; a non-empty plural wins over its scalar twin.
	where, args := appendFilter(nil, nil, EventFilter{Provider: "openai"})
	if len(where) != 1 || !contains(where[0], "provider = ANY(") {
		t.Fatalf("where = %v", where)
	}
	if v, ok := args[0].([]string); !ok || len(v) != 1 || v[0] != "openai" {
		t.Errorf("scalar fallback arg = %#v", args[0])
	}

	_, args = appendFilter(nil, nil, EventFilter{Provider: "ignored", Providers: []string{"a", "b"}})
	if v, ok := args[0].([]string); !ok || len(v) != 2 || v[0] != "a" {
		t.Errorf("plural should win over scalar: %#v", args[0])
	}
}
