package observability_test

import (
	"testing"

	"github.com/andyjmorgan/sluice-gateway/internal/observability"
)

func TestOperationNameForEndpoint(t *testing.T) {
	t.Parallel()

	cases := []struct {
		endpoint string
		want     string
	}{
		{"chat_completions", observability.OperationChat},
		{"messages", observability.OperationChat},
		{"generate_content", observability.OperationChat},
		{"responses", observability.OperationChat},
		{"embeddings", observability.OperationEmbeddings},
		// Endpoints the GenAI spec has no operation for fall through to
		// their own key; the precise route is still emitted as
		// sluice.endpoint, so nothing is lost.
		{"models", "models"},
		{"", ""},
	}

	for _, tc := range cases {
		t.Run(tc.endpoint, func(t *testing.T) {
			t.Parallel()
			if got := observability.OperationNameForEndpoint(tc.endpoint); got != tc.want {
				t.Errorf("OperationNameForEndpoint(%q) = %q, want %q", tc.endpoint, got, tc.want)
			}
		})
	}
}
