package observability_test

import (
	"testing"

	"github.com/andyjmorgan/slipspace-gateway/internal/observability"
)

func TestOperationNameForProtocol(t *testing.T) {
	t.Parallel()

	cases := []struct {
		endpoint string
		want     string
	}{
		{"chat_completions", observability.OperationChat},
		{"messages", observability.OperationChat},
		{"generate_content", observability.OperationGenerateContent},
		{"responses", observability.OperationChat},
		{"embeddings", observability.OperationEmbeddings},
		// Protocols the GenAI spec has no operation for fall through to
		// their own key; the precise route is still emitted as
		// slipspace.protocol, so nothing is lost.
		{"models", "models"},
		{"", ""},
	}

	for _, tc := range cases {
		t.Run(tc.endpoint, func(t *testing.T) {
			t.Parallel()
			if got := observability.OperationNameForProtocol(tc.endpoint); got != tc.want {
				t.Errorf("OperationNameForProtocol(%q) = %q, want %q", tc.endpoint, got, tc.want)
			}
		})
	}
}

func TestGenAIProviderName(t *testing.T) {
	t.Parallel()

	cases := []struct {
		provider string
		want     string
	}{
		// gemini is the one internal key that diverges from the spec enum.
		{"gemini", "gcp.gemini"},
		// openai and anthropic already match the gen_ai.provider.name enum.
		{"openai", "openai"},
		{"anthropic", "anthropic"},
		// Unknown providers pass through verbatim rather than being dropped.
		{"qwen", "qwen"},
		{"", ""},
	}

	for _, tc := range cases {
		t.Run(tc.provider, func(t *testing.T) {
			t.Parallel()
			if got := observability.GenAIProviderName(tc.provider); got != tc.want {
				t.Errorf("GenAIProviderName(%q) = %q, want %q", tc.provider, got, tc.want)
			}
		})
	}
}
