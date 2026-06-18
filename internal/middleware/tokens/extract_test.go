package tokens_test

import (
	"testing"

	"github.com/andyjmorgan/slipspace-gateway/internal/middleware/tokens"
)

func TestExtract_EmptyRaw(t *testing.T) {
	t.Parallel()
	got := tokens.Extract("openai", "chat_completions", nil)
	if got.Recognised {
		t.Errorf("empty raw: Recognised=true, want false; snap=%+v", got)
	}
}

func TestExtract_UnknownEndpoint(t *testing.T) {
	t.Parallel()
	body := []byte(`{"usage":{"prompt_tokens":1,"completion_tokens":1}}`)
	got := tokens.Extract("openai", "embeddings", body)
	if got.Recognised {
		t.Errorf("unknown endpoint: Recognised=true, want false; snap=%+v", got)
	}
}

func TestExtract_MalformedJSON(t *testing.T) {
	t.Parallel()
	got := tokens.Extract("openai", "chat_completions", []byte(`{not valid json`))
	if got.Recognised {
		t.Errorf("malformed JSON: Recognised=true, want false")
	}
}

func TestExtract_MalformedSSE(t *testing.T) {
	t.Parallel()
	// Frame is shaped like SSE but the data line is not JSON; the
	// extractor should silently skip and report nothing.
	raw := []byte("data: not-json-at-all\n\n")
	got := tokens.Extract("openai", "chat_completions", raw)
	if got.Recognised {
		t.Errorf("malformed SSE: Recognised=true, want false")
	}
}
