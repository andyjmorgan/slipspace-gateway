package tokens_test

import (
	"testing"

	"github.com/andyjmorgan/sluice-gateway/internal/middleware/tokens"
)

// TestExtract_GeminiContent_NonStreamFixture runs the extractor against
// a real captured prod non-streaming response. The model burned all
// its output budget on hidden thoughts (finishReason=MAX_TOKENS); the
// extractor derives Output = totalTokenCount - promptTokenCount =
// 23 - 6 = 17, which correctly captures the chargeable thoughts even
// though no candidatesTokenCount field was emitted.
func TestExtract_GeminiContent_NonStreamFixture(t *testing.T) {
	t.Parallel()
	raw := readFixture(t, "gemini_content_nonstream.json")

	got := tokens.Extract("gemini", "generate_content", raw)
	want := tokens.Snapshot{Input: 6, Output: 17, Cached: 0, CacheCreation: 0, Recognised: true}
	if got != want {
		t.Errorf("snapshot = %+v, want %+v", got, want)
	}
}

// TestExtract_GeminiContent_NonStreamWithCached exercises the path
// where a portion of the prompt was served from the (Vertex
// CachedContent feature) cache and reported separately.
func TestExtract_GeminiContent_NonStreamWithCached(t *testing.T) {
	t.Parallel()
	body := []byte(`{
		"candidates": [{"content": {"role": "model", "parts": [{"text": "pong"}]}, "finishReason": "STOP"}],
		"usageMetadata": {
			"promptTokenCount": 500,
			"cachedContentTokenCount": 450,
			"candidatesTokenCount": 1,
			"totalTokenCount": 501
		},
		"modelVersion": "gemini-2.5-flash"
	}`)

	got := tokens.Extract("gemini", "generate_content", body)
	want := tokens.Snapshot{Input: 500, Output: 1, Cached: 450, CacheCreation: 0, Recognised: true}
	if got != want {
		t.Errorf("snapshot = %+v, want %+v", got, want)
	}
}

// TestExtract_GeminiContent_Stream covers a synthetic SSE stream
// (matching the alt=sse wire shape) where usageMetadata appears only
// on the final chunk. The Sluice gateway does not currently route
// :streamGenerateContent — this case exists so the extractor is
// ready the moment that route is added (separate, in-flight gap).
func TestExtract_GeminiContent_Stream(t *testing.T) {
	t.Parallel()
	raw := []byte(`data: {"candidates":[{"content":{"role":"model","parts":[{"text":"p"}]}}]}

data: {"candidates":[{"content":{"role":"model","parts":[{"text":"ong"}]}}]}

data: {"candidates":[{"content":{"role":"model","parts":[{"text":""}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":6,"candidatesTokenCount":2,"totalTokenCount":8}}

`)
	got := tokens.Extract("gemini", "generate_content", raw)
	want := tokens.Snapshot{Input: 6, Output: 2, Cached: 0, CacheCreation: 0, Recognised: true}
	if got != want {
		t.Errorf("snapshot = %+v, want %+v", got, want)
	}
}

// TestExtract_GeminiContent_StreamPerChunkCumulative covers the
// alternate streaming shape where every chunk carries a running
// usageMetadata total. LastWins must yield the final chunk's values,
// not the cumulative growth observed mid-stream.
func TestExtract_GeminiContent_StreamPerChunkCumulative(t *testing.T) {
	t.Parallel()
	raw := []byte(`data: {"candidates":[{"content":{"parts":[{"text":"p"}]}}],"usageMetadata":{"promptTokenCount":6,"candidatesTokenCount":1,"totalTokenCount":7}}

data: {"candidates":[{"content":{"parts":[{"text":"o"}]}}],"usageMetadata":{"promptTokenCount":6,"candidatesTokenCount":2,"totalTokenCount":8}}

data: {"candidates":[{"content":{"parts":[{"text":"ng"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":6,"candidatesTokenCount":4,"totalTokenCount":10}}

`)
	got := tokens.Extract("gemini", "generate_content", raw)
	want := tokens.Snapshot{Input: 6, Output: 4, Recognised: true}
	if got != want {
		t.Errorf("snapshot = %+v, want %+v", got, want)
	}
}

// TestExtract_GeminiContent_MissingUsageMetadata covers the
// known-buggy-model case (e.g. gemini-2.5-pro-preview) where the
// response omits usageMetadata entirely. Recognised must stay false.
func TestExtract_GeminiContent_MissingUsageMetadata(t *testing.T) {
	t.Parallel()
	body := []byte(`{"candidates":[{"content":{"role":"model","parts":[{"text":"pong"}]}}]}`)
	got := tokens.Extract("gemini", "generate_content", body)
	if got.Recognised {
		t.Errorf("missing usageMetadata: Recognised=true, want false; snap=%+v", got)
	}
}

// TestExtract_GeminiContent_TotalLessThanPrompt guards the derived
// output formula: when totalTokenCount somehow undershoots
// promptTokenCount (defensive — shouldn't happen, but we don't want
// a negative count subtracting silently), Output stays zero.
func TestExtract_GeminiContent_TotalLessThanPrompt(t *testing.T) {
	t.Parallel()
	body := []byte(`{"usageMetadata":{"promptTokenCount":100,"totalTokenCount":50}}`)
	got := tokens.Extract("gemini", "generate_content", body)
	if got.Output != 0 {
		t.Errorf("Output=%d, want 0 when total<prompt", got.Output)
	}
	if got.Input != 100 {
		t.Errorf("Input=%d, want 100", got.Input)
	}
	if !got.Recognised {
		t.Errorf("Recognised=false; want true because Input was reported")
	}
}
