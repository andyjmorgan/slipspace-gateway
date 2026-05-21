package tokens_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/andyjmorgan/sluice-gateway/internal/middleware/tokens"
)

// TestExtract_OpenAIChat_StreamFixture runs the extractor against a
// real captured prod streaming response. The asserted counts mirror
// the terminal chunk's usage block byte-for-byte; if OpenAI changes
// where they put the usage object in the stream, this test catches it.
func TestExtract_OpenAIChat_StreamFixture(t *testing.T) {
	t.Parallel()
	raw := readFixture(t, "openai_chat_stream.sse")

	got := tokens.Extract("openai", "chat_completions", raw)
	want := tokens.Snapshot{Input: 13, Output: 3, Cached: 0, CacheCreation: 0, Recognised: true}
	if got != want {
		t.Errorf("snapshot = %+v, want %+v", got, want)
	}
}

// TestExtract_OpenAIChat_NonStream covers the static-body shape that
// every non-streaming /v1/chat/completions response carries — same
// usage block as the terminal chunk, no SSE framing.
func TestExtract_OpenAIChat_NonStream(t *testing.T) {
	t.Parallel()
	body := []byte(`{
		"id": "chatcmpl-1",
		"object": "chat.completion",
		"choices": [{"index": 0, "message": {"role": "assistant", "content": "pong"}, "finish_reason": "stop"}],
		"usage": {
			"prompt_tokens": 25,
			"completion_tokens": 1,
			"total_tokens": 26,
			"prompt_tokens_details": {"cached_tokens": 12, "audio_tokens": 0},
			"completion_tokens_details": {"reasoning_tokens": 0}
		}
	}`)

	got := tokens.Extract("openai", "chat_completions", body)
	want := tokens.Snapshot{Input: 25, Output: 1, Cached: 12, CacheCreation: 0, Recognised: true}
	if got != want {
		t.Errorf("snapshot = %+v, want %+v", got, want)
	}
}

// TestExtract_OpenAIChat_StreamInterrupted models the client-disconnect
// case: the stream stops before the terminal usage chunk arrives. The
// extractor must return Recognised=false rather than zeros that would
// be indistinguishable from a real zero count.
func TestExtract_OpenAIChat_StreamInterrupted(t *testing.T) {
	t.Parallel()
	// Two content chunks but no terminal usage chunk and no [DONE].
	raw := []byte(`data: {"id":"x","choices":[{"index":0,"delta":{"content":"hi"}}],"usage":null}

data: {"id":"x","choices":[{"index":0,"delta":{"content":"!"}}],"usage":null}

`)
	got := tokens.Extract("openai", "chat_completions", raw)
	if got.Recognised {
		t.Errorf("interrupted stream: Recognised=true, want false; snap=%+v", got)
	}
}

// TestExtract_OpenAIChat_StreamWithoutIncludeUsage models a stream
// initiated without stream_options.include_usage=true. Every chunk
// carries usage=null and no terminal usage chunk arrives. Recognised
// must stay false.
func TestExtract_OpenAIChat_StreamWithoutIncludeUsage(t *testing.T) {
	t.Parallel()
	raw := []byte(`data: {"id":"x","choices":[{"index":0,"delta":{"content":"hi"}}]}

data: {"id":"x","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}

data: [DONE]

`)
	got := tokens.Extract("openai", "chat_completions", raw)
	if got.Recognised {
		t.Errorf("stream without include_usage: Recognised=true, want false; snap=%+v", got)
	}
}

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	path := filepath.Join("testdata", name)
	b, err := os.ReadFile(path) //nolint:gosec // testdata path is hard-coded in-tree, not an external input
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return b
}
