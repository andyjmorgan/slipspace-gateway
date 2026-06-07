package observability_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/andyjmorgan/sluice-gateway/internal/observability"
)

func TestUserResolver_Resolve(t *testing.T) {
	t.Parallel()

	// sensitiveSluice reports the Sluice user header as redacted, to exercise
	// the redaction-bypass fall-through.
	sensitiveSluice := func(name string) bool { return name == observability.SluiceUserHeader }

	cases := []struct {
		name       string
		extra      []string
		headers    map[string]string
		sensitive  func(string) bool
		wantID     string
		wantSource string
	}{
		{
			name:       "sluice header wins over operator fallbacks",
			extra:      []string{"X-Acme-User-Id"},
			headers:    map[string]string{observability.SluiceUserHeader: "usr-1", "X-Acme-User-Id": "acme-9"},
			wantID:     "usr-1",
			wantSource: observability.SluiceUserHeader,
		},
		{
			name:       "sluice header alone (no shipped default chain)",
			headers:    map[string]string{observability.SluiceUserHeader: "usr-1"},
			wantID:     "usr-1",
			wantSource: observability.SluiceUserHeader,
		},
		{
			name:       "operator custom header resolves when sluice absent",
			extra:      []string{"X-Acme-User-Id"},
			headers:    map[string]string{"X-Acme-User-Id": "acme-3"},
			wantID:     "acme-3",
			wantSource: "X-Acme-User-Id",
		},
		{
			name:       "operator extras honoured in order",
			extra:      []string{"X-First-User", "X-Second-User"},
			headers:    map[string]string{"X-First-User": "first-1", "X-Second-User": "second-2"},
			wantID:     "first-1",
			wantSource: "X-First-User",
		},
		{
			name:       "redacted sluice header falls through to operator extra",
			extra:      []string{"X-Acme-User-Id"},
			headers:    map[string]string{observability.SluiceUserHeader: "usr-1", "X-Acme-User-Id": "acme-9"},
			sensitive:  sensitiveSluice,
			wantID:     "acme-9",
			wantSource: "X-Acme-User-Id",
		},
		{
			name:    "whitespace-only value is treated as absent",
			headers: map[string]string{observability.SluiceUserHeader: "   "},
			wantID:  "",
		},
		{
			name:    "nothing matches (empty default chain, no extras)",
			headers: map[string]string{"X-Unrelated": "x"},
			wantID:  "",
		},
		{
			name:    "nil headers",
			headers: nil,
			wantID:  "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var headers http.Header
			if tc.headers != nil {
				pairs := make([]string, 0, len(tc.headers)*2)
				for k, v := range tc.headers {
					pairs = append(pairs, k, v)
				}
				headers = hdr(pairs...)
			}
			r := observability.NewUserResolver(tc.extra)
			id, source := r.Resolve(headers, tc.sensitive)
			if id != tc.wantID {
				t.Errorf("id = %q, want %q", id, tc.wantID)
			}
			if id != "" && source != tc.wantSource {
				t.Errorf("source = %q, want %q", source, tc.wantSource)
			}
		})
	}
}

func TestUserResolver_BlankExtraDropped(t *testing.T) {
	t.Parallel()
	r := observability.NewUserResolver([]string{"  ", ""})
	// The default chain is empty and blank extras never match, so only the
	// authoritative Sluice header can resolve.
	if id, _ := r.Resolve(hdr(observability.SluiceUserHeader, "usr"), nil); id != "usr" {
		t.Errorf("id = %q, want usr", id)
	}
	if id, _ := r.Resolve(hdr("X-Unrelated", "x"), nil); id != "" {
		t.Errorf("id = %q, want empty", id)
	}
}

func TestUserContext_RoundTrip(t *testing.T) {
	t.Parallel()
	ctx := observability.WithUserID(context.Background(), "usr-1", observability.SluiceUserHeader)
	if got := observability.UserIDFromContext(ctx); got != "usr-1" {
		t.Errorf("id = %q, want usr-1", got)
	}
	if got := observability.UserIDSourceFromContext(ctx); got != observability.SluiceUserHeader {
		t.Errorf("source = %q, want %q", got, observability.SluiceUserHeader)
	}
}

func TestUserContext_EmptyAndNil(t *testing.T) {
	t.Parallel()
	// Empty id leaves ctx unchanged.
	ctx := observability.WithUserID(context.Background(), "", "X-Whatever")
	if got := observability.UserIDFromContext(ctx); got != "" {
		t.Errorf("id = %q, want empty", got)
	}
	if got := observability.UserIDSourceFromContext(ctx); got != "" {
		t.Errorf("source = %q, want empty", got)
	}
	// Nil ctx is safe.
	if got := observability.UserIDFromContext(context.TODO()); got != "" {
		t.Errorf("id from empty ctx = %q, want empty", got)
	}
}

func TestUserContext_IDWithoutSource(t *testing.T) {
	t.Parallel()
	// An id with an empty source still stores the id.
	ctx := observability.WithUserID(context.Background(), "usr-2", "")
	if got := observability.UserIDFromContext(ctx); got != "usr-2" {
		t.Errorf("id = %q, want usr-2", got)
	}
	if got := observability.UserIDSourceFromContext(ctx); got != "" {
		t.Errorf("source = %q, want empty", got)
	}
}
