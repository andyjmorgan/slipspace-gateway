package observability_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/andyjmorgan/slipspace-gateway/internal/observability"
)

func hdr(pairs ...string) http.Header {
	h := http.Header{}
	for i := 0; i+1 < len(pairs); i += 2 {
		h.Set(pairs[i], pairs[i+1])
	}
	return h
}

func TestSessionResolver_Resolve(t *testing.T) {
	t.Parallel()

	// sensitiveSlipSpace reports the SlipSpace header as redacted, to exercise
	// the redaction-bypass fall-through.
	sensitiveSlipSpace := func(name string) bool { return name == observability.SlipSpaceSessionHeader }

	cases := []struct {
		name       string
		extra      []string
		headers    http.Header
		sensitive  func(string) bool
		wantID     string
		wantSource string
	}{
		{
			name:       "slipspace header wins over client fallbacks",
			headers:    hdr(observability.SlipSpaceSessionHeader, "sess-1", "Session-Id", "codex-9"),
			wantID:     "sess-1",
			wantSource: observability.SlipSpaceSessionHeader,
		},
		{
			name:       "codex Session-Id beats claude header by order",
			headers:    hdr("Session-Id", "codex-9", "X-Claude-Code-Session-Id", "cc-9"),
			wantID:     "codex-9",
			wantSource: "Session-Id",
		},
		{
			name:       "claude code session header fallback",
			headers:    hdr("X-Claude-Code-Session-Id", "cc-7"),
			wantID:     "cc-7",
			wantSource: "X-Claude-Code-Session-Id",
		},
		{
			name:       "operator custom header appended after defaults",
			extra:      []string{"X-Acme-Conversation-Id"},
			headers:    hdr("X-Acme-Conversation-Id", "acme-3"),
			wantID:     "acme-3",
			wantSource: "X-Acme-Conversation-Id",
		},
		{
			name:       "redacted slipspace header falls through to Session-Id",
			headers:    hdr(observability.SlipSpaceSessionHeader, "sess-1", "Session-Id", "codex-9"),
			sensitive:  sensitiveSlipSpace,
			wantID:     "codex-9",
			wantSource: "Session-Id",
		},
		{
			name:    "whitespace-only value is treated as absent",
			headers: hdr("Session-Id", "   "),
			wantID:  "",
		},
		{
			name:    "nothing matches",
			headers: hdr("X-Unrelated", "x"),
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
			r := observability.NewSessionResolver(tc.extra)
			id, source := r.Resolve(tc.headers, tc.sensitive)
			if id != tc.wantID {
				t.Errorf("id = %q, want %q", id, tc.wantID)
			}
			if id != "" && source != tc.wantSource {
				t.Errorf("source = %q, want %q", source, tc.wantSource)
			}
		})
	}
}

func TestSessionResolver_BlankExtraDropped(t *testing.T) {
	t.Parallel()
	r := observability.NewSessionResolver([]string{"  ", ""})
	// Only the built-in defaults remain usable; a blank extra never matches.
	if id, _ := r.Resolve(hdr("Session-Id", "t"), nil); id != "t" {
		t.Errorf("id = %q, want t", id)
	}
}

func TestSessionContext_RoundTrip(t *testing.T) {
	t.Parallel()
	ctx := observability.WithSessionID(context.Background(), "sess-1", observability.SlipSpaceSessionHeader)
	if got := observability.SessionIDFromContext(ctx); got != "sess-1" {
		t.Errorf("id = %q, want sess-1", got)
	}
	if got := observability.SessionIDSourceFromContext(ctx); got != observability.SlipSpaceSessionHeader {
		t.Errorf("source = %q, want %q", got, observability.SlipSpaceSessionHeader)
	}
}

func TestSessionContext_EmptyAndNil(t *testing.T) {
	t.Parallel()
	// Empty id leaves ctx unchanged.
	ctx := observability.WithSessionID(context.Background(), "", "X-Whatever")
	if got := observability.SessionIDFromContext(ctx); got != "" {
		t.Errorf("id = %q, want empty", got)
	}
	if got := observability.SessionIDSourceFromContext(ctx); got != "" {
		t.Errorf("source = %q, want empty", got)
	}
	// Nil ctx is safe.
	if got := observability.SessionIDFromContext(context.TODO()); got != "" {
		t.Errorf("id from empty ctx = %q, want empty", got)
	}
}

func TestSessionContext_IDWithoutSource(t *testing.T) {
	t.Parallel()
	// An id with an empty source still stores the id.
	ctx := observability.WithSessionID(context.Background(), "sess-2", "")
	if got := observability.SessionIDFromContext(ctx); got != "sess-2" {
		t.Errorf("id = %q, want sess-2", got)
	}
	if got := observability.SessionIDSourceFromContext(ctx); got != "" {
		t.Errorf("source = %q, want empty", got)
	}
}
