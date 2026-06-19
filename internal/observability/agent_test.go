package observability_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/andyjmorgan/slipspace-gateway/internal/observability"
)

func TestAgentResolver_Resolve(t *testing.T) {
	t.Parallel()

	// sensitiveSlipSpace reports the SlipSpace agent header as redacted, to exercise
	// the redaction-bypass fall-through.
	sensitiveSlipSpace := func(name string) bool { return name == observability.SlipSpaceAgentHeader }

	cases := []struct {
		name       string
		extra      []string
		headers    map[string]string
		sensitive  func(string) bool
		wantID     string
		wantSource string
	}{
		{
			name:       "slipspace header wins over operator extras",
			extra:      []string{"X-Acme-Agent-Id"},
			headers:    map[string]string{observability.SlipSpaceAgentHeader: "agt-1", "X-Acme-Agent-Id": "acme-3"},
			wantID:     "agt-1",
			wantSource: observability.SlipSpaceAgentHeader,
		},
		{
			name:    "claude code agent header does NOT resolve as a named agent",
			headers: map[string]string{"X-Claude-Code-Agent-Id": "cc-9"},
			// gen_ai.agent.id is reserved for named agents; the Claude Code
			// subagent instance rides the conversation/thread axis instead.
			wantID: "",
		},
		{
			name:       "operator custom header appended after the (empty) defaults",
			extra:      []string{"X-Acme-Agent-Id"},
			headers:    map[string]string{"X-Acme-Agent-Id": "acme-3"},
			wantID:     "acme-3",
			wantSource: "X-Acme-Agent-Id",
		},
		{
			name:       "redacted slipspace header falls through to operator extra",
			extra:      []string{"X-Acme-Agent-Id"},
			headers:    map[string]string{observability.SlipSpaceAgentHeader: "agt-1", "X-Acme-Agent-Id": "acme-3"},
			sensitive:  sensitiveSlipSpace,
			wantID:     "acme-3",
			wantSource: "X-Acme-Agent-Id",
		},
		{
			name:    "whitespace-only value is treated as absent",
			headers: map[string]string{observability.SlipSpaceAgentHeader: "   "},
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
	// The agent chain has no shipped default; a blank extra never matches, so
	// only the authoritative SlipSpace header resolves.
	if id, _ := r.Resolve(hdr(observability.SlipSpaceAgentHeader, "agt"), nil); id != "agt" {
		t.Errorf("id = %q, want agt", id)
	}
}

func TestAgentContext_RoundTrip(t *testing.T) {
	t.Parallel()
	ctx := observability.WithAgentID(context.Background(), "agt-1", observability.SlipSpaceAgentHeader)
	if got := observability.AgentIDFromContext(ctx); got != "agt-1" {
		t.Errorf("id = %q, want agt-1", got)
	}
	if got := observability.AgentIDSourceFromContext(ctx); got != observability.SlipSpaceAgentHeader {
		t.Errorf("source = %q, want %q", got, observability.SlipSpaceAgentHeader)
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
