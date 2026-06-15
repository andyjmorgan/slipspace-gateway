package store

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestQueryDashboardSecurity_OK(t *testing.T) {
	q := &fakeQuerier{freshRows: 3}
	out, err := newStore(q).QueryDashboardSecurity(ctx(), time.Time{}, time.Now())
	if err != nil {
		t.Fatalf("QueryDashboardSecurity: %v", err)
	}
	if len(out.ByCheckType) != 3 || len(out.TopCategories) != 3 {
		t.Fatalf("want 3 rows each, got %d / %d", len(out.ByCheckType), len(out.TopCategories))
	}
	// Posture is one QueryRow; the two breakdowns are two Query calls.
	if q.rowCalls != 1 {
		t.Fatalf("want 1 posture QueryRow, got %d", q.rowCalls)
	}
	if q.queryCalls != 2 {
		t.Fatalf("want 2 breakdown Query calls, got %d", q.queryCalls)
	}
	// The top-categories query carries the LIMIT cap; by-check-type does not.
	if strings.Contains(q.querySQL[0], "LIMIT") {
		t.Fatalf("by-check-type query should not be LIMITed: %q", q.querySQL[0])
	}
	if !strings.Contains(q.querySQL[1], "LIMIT") {
		t.Fatalf("top-categories query should be LIMITed: %q", q.querySQL[1])
	}
}

func TestQueryDashboardSecurity_Errors(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name string
		q    *fakeQuerier
	}{
		{"posture row error", &fakeQuerier{rowFailAt: 1}},
		{"by-check-type query error", &fakeQuerier{queryFailAt: 1}},
		{"top-categories query error", &fakeQuerier{queryFailAt: 2}},
		{"breakdown scan error", &fakeQuerier{query: &fakeRows{scanErrs: []error{errors.New("s")}}}},
		{"breakdown rows.Err", &fakeQuerier{query: &fakeRows{scanErrs: []error{nil}, finalErr: errors.New("rows")}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := newStore(c.q).QueryDashboardSecurity(ctx(), time.Time{}, now); err == nil {
				t.Fatal("want error, got nil")
			}
		})
	}
}

func TestSecurityFindingCol(t *testing.T) {
	for _, c := range []string{"check_type", "category"} {
		if got := securityFindingCol(c); got != c {
			t.Fatalf("securityFindingCol(%q)=%q", c, got)
		}
	}
	defer func() {
		if recover() == nil {
			t.Fatal("securityFindingCol with an unknown column should panic")
		}
	}()
	_ = securityFindingCol("evil; DROP TABLE finding")
}
