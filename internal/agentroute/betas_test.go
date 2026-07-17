package agentroute

import (
	"net/http"
	"reflect"
	"testing"
)

func TestReconcileBetas(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		// in is the inbound Anthropic-Beta header values (nil = header absent).
		in []string
		// model is the pinned model.
		model string
		// want is the expected header values after reconciliation.
		want []string
		// stripped is the expected return.
		stripped bool
	}{
		{
			name:     "haiku pin strips the 1m token, preserves order",
			in:       []string{"claude-code-20250219,oauth-2025-04-20,context-1m-2025-08-07,interleaved-thinking-2025-05-14"},
			model:    "claude-haiku-4-5",
			want:     []string{"claude-code-20250219,oauth-2025-04-20,interleaved-thinking-2025-05-14"},
			stripped: true,
		},
		{
			name:     "non-haiku pin leaves the header byte-verbatim",
			in:       []string{"claude-code-20250219, context-1m-2025-08-07 ,oauth-2025-04-20"},
			model:    "claude-sonnet-5",
			want:     []string{"claude-code-20250219, context-1m-2025-08-07 ,oauth-2025-04-20"},
			stripped: false,
		},
		{
			name:     "haiku pin with no 1m token leaves the header byte-verbatim",
			in:       []string{"claude-code-20250219,oauth-2025-04-20"},
			model:    "claude-haiku-4-5",
			want:     []string{"claude-code-20250219,oauth-2025-04-20"},
			stripped: false,
		},
		{
			name:     "header absent is a no-op",
			in:       nil,
			model:    "claude-haiku-4-5",
			want:     nil,
			stripped: false,
		},
		{
			name:     "whitespace-padded 1m token is stripped",
			in:       []string{"claude-code-20250219, context-1m-2025-08-07 , oauth-2025-04-20"},
			model:    "claude-haiku-4-5",
			want:     []string{"claude-code-20250219, oauth-2025-04-20"},
			stripped: true,
		},
		{
			name:     "value left empty after strip is dropped",
			in:       []string{"context-1m-2025-08-07", "oauth-2025-04-20"},
			model:    "claude-haiku-4-5",
			want:     []string{"oauth-2025-04-20"},
			stripped: true,
		},
		{
			name:     "header removed entirely when no tokens survive",
			in:       []string{"context-1m-2025-08-07"},
			model:    "claude-haiku-4-5",
			want:     nil,
			stripped: true,
		},
		{
			name:     "multiple header values each reconciled",
			in:       []string{"claude-code-20250219,context-1m-2025-08-07", "context-1m-2026-01-01,oauth-2025-04-20"},
			model:    "claude-haiku-4-5",
			want:     []string{"claude-code-20250219", "oauth-2025-04-20"},
			stripped: true,
		},
		{
			name:     "case-insensitive token match",
			in:       []string{"Context-1M-2025-08-07,oauth-2025-04-20"},
			model:    "claude-haiku-4-5",
			want:     []string{"oauth-2025-04-20"},
			stripped: true,
		},
		{
			name:     "future haiku versions match the family prefix",
			in:       []string{"context-1m-2025-08-07"},
			model:    "claude-haiku-5",
			want:     nil,
			stripped: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := http.Header{}
			for _, v := range tc.in {
				h.Add(betaHeader, v)
			}
			got := ReconcileBetas(h, tc.model)
			if got != tc.stripped {
				t.Errorf("ReconcileBetas stripped = %v, want %v", got, tc.stripped)
			}
			if vals := h.Values(betaHeader); !reflect.DeepEqual(vals, tc.want) {
				t.Errorf("header values = %q, want %q", vals, tc.want)
			}
		})
	}
}
