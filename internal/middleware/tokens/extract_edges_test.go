package tokens_test

import (
	"testing"

	"github.com/andyjmorgan/slipspace-gateway/internal/middleware/tokens"
)

// TestExtract_AnthropicMessages_MalformedFrameSkipped exercises the
// json.Unmarshal-failure branch inside extractAnthropicMessages: a
// non-JSON data line is silently skipped, and a well-formed
// message_delta later in the same stream still lands on the snapshot.
func TestExtract_AnthropicMessages_MalformedFrameSkipped(t *testing.T) {
	t.Parallel()
	raw := []byte(`data: not-json

data: {"type":"message_delta","delta":{},"usage":{"input_tokens":10,"output_tokens":3,"cache_creation_input_tokens":0,"cache_read_input_tokens":0}}

`)
	got := tokens.Extract("anthropic", "messages", raw)
	if got.Input != 10 || got.Output != 3 {
		t.Errorf("snap=%+v; malformed first frame should not block second", got)
	}
}

// TestExtract_AnthropicMessages_UnknownEventTypeSkipped covers the
// switch-default branch: events that aren't message_start /
// message_delta are silently skipped.
func TestExtract_AnthropicMessages_UnknownEventTypeSkipped(t *testing.T) {
	t.Parallel()
	raw := []byte(`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"x"}}

data: {"type":"ping"}

`)
	got := tokens.Extract("anthropic", "messages", raw)
	if got.Recognised {
		t.Errorf("unknown event types: Recognised=true, want false; snap=%+v", got)
	}
}

// TestExtract_AnthropicMessages_MessageStartWithoutMessage covers the
// defensive nil-message branch: a malformed message_start that omits
// the nested `message` object must not crash and must not contribute.
func TestExtract_AnthropicMessages_MessageStartWithoutMessage(t *testing.T) {
	t.Parallel()
	raw := []byte(`data: {"type":"message_start"}

`)
	got := tokens.Extract("anthropic", "messages", raw)
	if got.Recognised {
		t.Errorf("message_start without message: Recognised=true, want false")
	}
}

// TestExtract_OpenAIResponses_MalformedFrameSkipped mirrors the
// Anthropic malformed-skip test for the responses extractor.
func TestExtract_OpenAIResponses_MalformedFrameSkipped(t *testing.T) {
	t.Parallel()
	raw := []byte(`data: not-json

data: {"type":"response.completed","response":{"usage":{"input_tokens":5,"output_tokens":1}}}

`)
	got := tokens.Extract("openai", "responses", raw)
	if got.Input != 5 || got.Output != 1 {
		t.Errorf("snap=%+v; want Input=5 Output=1", got)
	}
}

// TestExtract_OpenAIResponses_StreamNoUsageEvents covers the
// usage-never-arrives stream path: the extractor walks every frame
// but finds no usage anywhere, so Recognised stays false.
func TestExtract_OpenAIResponses_StreamNoUsageEvents(t *testing.T) {
	t.Parallel()
	raw := []byte(`data: {"type":"response.in_progress","response":{"id":"r","status":"in_progress"}}

data: {"type":"response.output_text.delta","delta":"hi"}

`)
	got := tokens.Extract("openai", "responses", raw)
	if got.Recognised {
		t.Errorf("no usage in stream: Recognised=true, want false")
	}
}

// TestExtract_GeminiContent_MalformedFrameSkipped covers the
// json.Unmarshal-failure branch inside extractGeminiContent.
func TestExtract_GeminiContent_MalformedFrameSkipped(t *testing.T) {
	t.Parallel()
	raw := []byte(`data: not-json

data: {"usageMetadata":{"promptTokenCount":3,"totalTokenCount":5}}

`)
	got := tokens.Extract("gemini", "generate_content", raw)
	if got.Input != 3 || got.Output != 2 {
		t.Errorf("snap=%+v; want Input=3 Output=2", got)
	}
}

// TestExtract_GeminiContent_StreamFrameWithoutUsage covers the per-
// chunk skip path: a frame with no usageMetadata is silently passed
// over while a later frame's usage still lands.
func TestExtract_GeminiContent_StreamFrameWithoutUsage(t *testing.T) {
	t.Parallel()
	raw := []byte(`data: {"candidates":[{"content":{"parts":[{"text":"hi"}]}}]}

data: {"candidates":[{"content":{"parts":[{"text":""}]}}],"usageMetadata":{"promptTokenCount":4,"totalTokenCount":7}}

`)
	got := tokens.Extract("gemini", "generate_content", raw)
	if got.Input != 4 || got.Output != 3 {
		t.Errorf("snap=%+v; want Input=4 Output=3", got)
	}
}
