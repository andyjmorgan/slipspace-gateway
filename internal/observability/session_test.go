package observability_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/andyjmorgan/sluice-gateway/internal/observability"
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

	// sensitiveSluice reports the Sluice header as redacted, to exercise
	// the redaction-bypass fall-through.
	sensitiveSluice := func(name string) bool { return name == observability.SluiceSessionHeader }

	cases := []struct {
		name       string
		extra      []string
		headers    http.Header
		sensitive  func(string) bool
		wantID     string
		wantSource string
	}{
		{
			name:       "sluice header wins over client fallbacks",
			headers:    hdr(observability.SluiceSessionHeader, "sess-1", "Thread_id", "thread-9"),
			wantID:     "sess-1",
			wantSource: observability.SluiceSessionHeader,
		},
		{
			name:       "thread_id beats session_id by order",
			headers:    hdr("Thread_id", "thread-9", "Session_id", "session-9"),
			wantID:     "thread-9",
			wantSource: "Thread_id",
		},
		{
			name:       "session_id fallback when no thread_id",
			headers:    hdr("Session_id", "session-9"),
			wantID:     "session-9",
			wantSource: "Session_id",
		},
		{
			name:       "claude code default header",
			headers:    hdr("x-claude-code-session-id", "cc-7"),
			wantID:     "cc-7",
			wantSource: "x-claude-code-session-id",
		},
		{
			name:       "operator custom header appended after defaults",
			extra:      []string{"X-Acme-Conversation-Id"},
			headers:    hdr("X-Acme-Conversation-Id", "acme-3"),
			wantID:     "acme-3",
			wantSource: "X-Acme-Conversation-Id",
		},
		{
			name:       "redacted sluice header falls through to thread_id",
			headers:    hdr(observability.SluiceSessionHeader, "sess-1", "Thread_id", "thread-9"),
			sensitive:  sensitiveSluice,
			wantID:     "thread-9",
			wantSource: "Thread_id",
		},
		{
			name:    "whitespace-only value is treated as absent",
			headers: hdr("Thread_id", "   "),
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
	if id, _ := r.Resolve(hdr("Thread_id", "t"), nil); id != "t" {
		t.Errorf("id = %q, want t", id)
	}
}

func TestSessionContext_RoundTrip(t *testing.T) {
	t.Parallel()
	ctx := observability.WithSessionID(context.Background(), "sess-1", observability.SluiceSessionHeader)
	if got := observability.SessionIDFromContext(ctx); got != "sess-1" {
		t.Errorf("id = %q, want sess-1", got)
	}
	if got := observability.SessionIDSourceFromContext(ctx); got != observability.SluiceSessionHeader {
		t.Errorf("source = %q, want %q", got, observability.SluiceSessionHeader)
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
	// A literal nil context hits the explicit guard in both getters.
	//nolint:staticcheck // SA1012: deliberately passing nil to exercise the nil-ctx guard
	if got := observability.SessionIDFromContext(nil); got != "" {
		t.Errorf("id from nil ctx = %q, want empty", got)
	}
	//nolint:staticcheck // SA1012: deliberately passing nil to exercise the nil-ctx guard
	if got := observability.SessionIDSourceFromContext(nil); got != "" {
		t.Errorf("source from nil ctx = %q, want empty", got)
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
