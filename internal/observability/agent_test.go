package observability_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/andyjmorgan/sluice-gateway/internal/observability"
)

func TestAgentResolver_Resolve(t *testing.T) {
	t.Parallel()

	// sensitiveSluice reports the Sluice agent header as redacted, to exercise
	// the redaction-bypass fall-through.
	sensitiveSluice := func(name string) bool { return name == observability.SluiceAgentHeader }

	cases := []struct {
		name       string
		extra      []string
		headers    map[string]string
		sensitive  func(string) bool
		wantID     string
		wantSource string
	}{
		{
			name:       "sluice header wins over client fallbacks",
			headers:    map[string]string{observability.SluiceAgentHeader: "agt-1", "X-Claude-Code-Agent-Id": "cc-9"},
			wantID:     "agt-1",
			wantSource: observability.SluiceAgentHeader,
		},
		{
			name:       "claude code default header",
			headers:    map[string]string{"X-Claude-Code-Agent-Id": "cc-9"},
			wantID:     "cc-9",
			wantSource: "X-Claude-Code-Agent-Id",
		},
		{
			name:       "operator custom header appended after defaults",
			extra:      []string{"X-Acme-Agent-Id"},
			headers:    map[string]string{"X-Acme-Agent-Id": "acme-3"},
			wantID:     "acme-3",
			wantSource: "X-Acme-Agent-Id",
		},
		{
			name:       "default beats operator extra by order",
			extra:      []string{"X-Acme-Agent-Id"},
			headers:    map[string]string{"X-Claude-Code-Agent-Id": "cc-9", "X-Acme-Agent-Id": "acme-3"},
			wantID:     "cc-9",
			wantSource: "X-Claude-Code-Agent-Id",
		},
		{
			name:       "redacted sluice header falls through to claude code default",
			headers:    map[string]string{observability.SluiceAgentHeader: "agt-1", "X-Claude-Code-Agent-Id": "cc-9"},
			sensitive:  sensitiveSluice,
			wantID:     "cc-9",
			wantSource: "X-Claude-Code-Agent-Id",
		},
		{
			name:    "whitespace-only value is treated as absent",
			headers: map[string]string{"X-Claude-Code-Agent-Id": "   "},
			wantID:  "",
		},
		{
			name:    "nothing matches",
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
			r := observability.NewAgentResolver(tc.extra)
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

func TestAgentResolver_BlankExtraDropped(t *testing.T) {
	t.Parallel()
	r := observability.NewAgentResolver([]string{"  ", ""})
	// Only the built-in defaults remain usable; a blank extra never matches.
	if id, _ := r.Resolve(hdr("X-Claude-Code-Agent-Id", "cc"), nil); id != "cc" {
		t.Errorf("id = %q, want cc", id)
	}
}

func TestAgentContext_RoundTrip(t *testing.T) {
	t.Parallel()
	ctx := observability.WithAgentID(context.Background(), "agt-1", observability.SluiceAgentHeader)
	if got := observability.AgentIDFromContext(ctx); got != "agt-1" {
		t.Errorf("id = %q, want agt-1", got)
	}
	if got := observability.AgentIDSourceFromContext(ctx); got != observability.SluiceAgentHeader {
		t.Errorf("source = %q, want %q", got, observability.SluiceAgentHeader)
	}
}

func TestAgentContext_EmptyAndNil(t *testing.T) {
	t.Parallel()
	// Empty id leaves ctx unchanged.
	ctx := observability.WithAgentID(context.Background(), "", "X-Whatever")
	if got := observability.AgentIDFromContext(ctx); got != "" {
		t.Errorf("id = %q, want empty", got)
	}
	if got := observability.AgentIDSourceFromContext(ctx); got != "" {
		t.Errorf("source = %q, want empty", got)
	}
	// Nil ctx is safe.
	if got := observability.AgentIDFromContext(context.TODO()); got != "" {
		t.Errorf("id from empty ctx = %q, want empty", got)
	}
}

func TestAgentContext_IDWithoutSource(t *testing.T) {
	t.Parallel()
	// An id with an empty source still stores the id.
	ctx := observability.WithAgentID(context.Background(), "agt-2", "")
	if got := observability.AgentIDFromContext(ctx); got != "agt-2" {
		t.Errorf("id = %q, want agt-2", got)
	}
	if got := observability.AgentIDSourceFromContext(ctx); got != "" {
		t.Errorf("source = %q, want empty", got)
	}
}
