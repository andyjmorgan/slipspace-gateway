package observability_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/andyjmorgan/sluice-gateway/internal/observability"
)

// hdrMap builds an http.Header from a name→value map (nil map → nil header).
func hdrMap(m map[string]string) http.Header {
	if m == nil {
		return nil
	}
	h := http.Header{}
	for k, v := range m {
		h.Set(k, v)
	}
	return h
}

func TestConversationResolver_Resolve(t *testing.T) {
	t.Parallel()

	sensitiveSluice := func(name string) bool { return name == observability.SluiceThreadHeader }

	cases := []struct {
		name       string
		extra      []string
		headers    map[string]string
		sensitive  func(string) bool
		wantID     string
		wantSource string
	}{
		{
			name:       "sluice thread header wins",
			headers:    map[string]string{observability.SluiceThreadHeader: "t-1", "Thread-Id": "codex-9"},
			wantID:     "t-1",
			wantSource: observability.SluiceThreadHeader,
		},
		{
			name:       "codex Thread-Id beats claude agent header by order",
			headers:    map[string]string{"Thread-Id": "codex-9", "X-Claude-Code-Agent-Id": "cc-9"},
			wantID:     "codex-9",
			wantSource: "Thread-Id",
		},
		{
			name:       "claude code agent header is the conversation/thread",
			headers:    map[string]string{"X-Claude-Code-Agent-Id": "cc-9"},
			wantID:     "cc-9",
			wantSource: "X-Claude-Code-Agent-Id",
		},
		{
			name:       "operator extra appended after defaults",
			extra:      []string{"X-Acme-Thread-Id"},
			headers:    map[string]string{"X-Acme-Thread-Id": "acme-3"},
			wantID:     "acme-3",
			wantSource: "X-Acme-Thread-Id",
		},
		{
			name:       "redacted sluice header falls through to Thread-Id",
			headers:    map[string]string{observability.SluiceThreadHeader: "t-1", "Thread-Id": "codex-9"},
			sensitive:  sensitiveSluice,
			wantID:     "codex-9",
			wantSource: "Thread-Id",
		},
		{
			name:    "nothing matches",
			headers: map[string]string{"X-Unrelated": "x"},
			wantID:  "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var h = hdrMap(tc.headers)
			r := observability.NewConversationResolver(tc.extra)
			id, source := r.Resolve(h, tc.sensitive)
			if id != tc.wantID {
				t.Errorf("id = %q, want %q", id, tc.wantID)
			}
			if id != "" && source != tc.wantSource {
				t.Errorf("source = %q, want %q", source, tc.wantSource)
			}
		})
	}
}

func TestParentResolver_Resolve(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		extra      []string
		headers    map[string]string
		wantID     string
		wantSource string
	}{
		{
			name:       "sluice parent header wins",
			headers:    map[string]string{observability.SluiceParentHeader: "p-1", "X-Codex-Parent-Thread-Id": "codex-p"},
			wantID:     "p-1",
			wantSource: observability.SluiceParentHeader,
		},
		{
			name:       "codex parent header",
			headers:    map[string]string{"X-Codex-Parent-Thread-Id": "codex-p"},
			wantID:     "codex-p",
			wantSource: "X-Codex-Parent-Thread-Id",
		},
		{
			name:    "absent",
			headers: map[string]string{"Thread-Id": "t"},
			wantID:  "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := observability.NewParentResolver(tc.extra)
			id, source := r.Resolve(hdrMap(tc.headers), nil)
			if id != tc.wantID {
				t.Errorf("id = %q, want %q", id, tc.wantID)
			}
			if id != "" && source != tc.wantSource {
				t.Errorf("source = %q, want %q", source, tc.wantSource)
			}
		})
	}
}

func TestConversationContext_RoundTrip(t *testing.T) {
	t.Parallel()
	ctx := observability.WithConversationID(context.Background(), "thread-1", "Thread-Id")
	if got := observability.ConversationIDFromContext(ctx); got != "thread-1" {
		t.Errorf("id = %q, want thread-1", got)
	}
	if got := observability.ConversationIDSourceFromContext(ctx); got != "Thread-Id" {
		t.Errorf("source = %q, want Thread-Id", got)
	}
	// Empty id leaves ctx unchanged.
	if got := observability.ConversationIDFromContext(observability.WithConversationID(context.Background(), "", "X")); got != "" {
		t.Errorf("empty id should not store, got %q", got)
	}
}

func TestConversationContext_EmptyNilAndNoSource(t *testing.T) {
	t.Parallel()
	// Nil context is safe for every getter.
	if got := observability.ConversationIDFromContext(nil); got != "" { //nolint:staticcheck // exercising the nil-ctx guard
		t.Errorf("conversation id from nil ctx = %q, want empty", got)
	}
	if got := observability.ConversationIDSourceFromContext(nil); got != "" { //nolint:staticcheck // exercising the nil-ctx guard
		t.Errorf("conversation source from nil ctx = %q, want empty", got)
	}
	if got := observability.ParentConversationIDFromContext(nil); got != "" { //nolint:staticcheck // exercising the nil-ctx guard
		t.Errorf("parent from nil ctx = %q, want empty", got)
	}
	// A bare context carries none of the values.
	if got := observability.ConversationIDSourceFromContext(context.Background()); got != "" {
		t.Errorf("conversation source from bare ctx = %q, want empty", got)
	}
	// An id stored without a source still reads an empty source.
	ctx := observability.WithConversationID(context.Background(), "thread-1", "")
	if got := observability.ConversationIDSourceFromContext(ctx); got != "" {
		t.Errorf("conversation source = %q, want empty when none stored", got)
	}
	if got := observability.ConversationIDFromContext(ctx); got != "thread-1" {
		t.Errorf("conversation id = %q, want thread-1", got)
	}
}

func TestParentContext_RoundTrip(t *testing.T) {
	t.Parallel()
	ctx := observability.WithParentConversationID(context.Background(), "sess-1")
	if got := observability.ParentConversationIDFromContext(ctx); got != "sess-1" {
		t.Errorf("parent = %q, want sess-1", got)
	}
	if got := observability.ParentConversationIDFromContext(observability.WithParentConversationID(context.Background(), "")); got != "" {
		t.Errorf("empty parent should not store, got %q", got)
	}
	if got := observability.ParentConversationIDFromContext(context.TODO()); got != "" {
		t.Errorf("parent from bare ctx = %q, want empty", got)
	}
}
