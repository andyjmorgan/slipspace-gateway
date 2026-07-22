//go:build e2e

package providers_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/andyjmorgan/slipspace-gateway/test/e2e/harness"
)

// TestAnthropic_Messages_ToolSearch_NonStreaming proves the tool-search wire
// contract through the binary: a request carrying a tool_search server tool
// plus defer_loading tool definitions reaches the upstream intact, and a
// response carrying server_tool_use + tool_search_tool_result (with nested
// tool_reference blocks) round-trips back to the client unmangled.
func TestAnthropic_Messages_ToolSearch_NonStreaming(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
	s := h.NewSession(t)

	s.Stage(harness.CannedResponse{
		Method: http.MethodPost,
		Path:   "/v1/messages",
		Body: `{"id":"msg_ts1","type":"message","role":"assistant","model":"claude-opus-4-6","content":[` +
			`{"type":"text","text":"Searching for a weather tool."},` +
			`{"type":"server_tool_use","id":"srvtoolu_01ABC123","name":"tool_search_tool_regex","input":{"pattern":"weather"}},` +
			`{"type":"tool_search_tool_result","tool_use_id":"srvtoolu_01ABC123","content":{"type":"tool_search_tool_search_result","tool_references":[{"type":"tool_reference","tool_name":"get_weather"}]}},` +
			`{"type":"tool_use","id":"toolu_01XYZ789","name":"get_weather","input":{"location":"San Francisco"}}` +
			`],"stop_reason":"tool_use","usage":{"input_tokens":10,"output_tokens":20}}`,
	})

	body := map[string]any{
		"model":      "claude-opus-4-6",
		"max_tokens": 64,
		"messages":   []map[string]string{{"role": "user", "content": "What is the weather in San Francisco?"}},
		"tools": []map[string]any{
			{"type": "tool_search_tool_regex_20251119", "name": "tool_search_tool_regex"},
			{
				"name":          "get_weather",
				"description":   "Get the weather at a specific location",
				"input_schema":  map[string]any{"type": "object", "properties": map[string]any{"location": map[string]any{"type": "string"}}, "required": []string{"location"}},
				"defer_loading": true,
			},
		},
	}
	resp := s.Post("/v1/messages", body, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d body=%s", resp.StatusCode, resp.Body)
	}

	// Client-side: the tool-search blocks survive the response path verbatim.
	for _, want := range []string{
		`"tool_search_tool_result"`,
		`"tool_search_tool_search_result"`,
		`"tool_reference"`,
		`"srvtoolu_01ABC123"`,
	} {
		if !strings.Contains(string(resp.Body), want) {
			t.Errorf("response missing %s: %s", want, resp.Body)
		}
	}

	// Destination-side: the upstream saw defer_loading and the tool_search
	// tool definition exactly as sent.
	captured := s.Captured()
	if len(captured) == 0 {
		t.Fatal("no captured upstream request")
	}
	var sent struct {
		Tools []map[string]any `json:"tools"`
	}
	if err := json.Unmarshal([]byte(captured[len(captured)-1].Body), &sent); err != nil {
		t.Fatalf("decode captured body: %v", err)
	}
	if len(sent.Tools) != 2 {
		t.Fatalf("upstream tools = %+v, want 2", sent.Tools)
	}
	if sent.Tools[0]["type"] != "tool_search_tool_regex_20251119" {
		t.Errorf("upstream tools[0].type = %v", sent.Tools[0]["type"])
	}
	if sent.Tools[1]["defer_loading"] != true {
		t.Errorf("upstream tools[1].defer_loading = %v, want true", sent.Tools[1]["defer_loading"])
	}
}

// TestAnthropic_Messages_ToolSearch_Streaming proves the streamed form: the
// tool_search_tool_result block arrives whole on content_block_start and the
// gateway forwards every frame untouched.
func TestAnthropic_Messages_ToolSearch_Streaming(t *testing.T) {
	t.Parallel()
	h := harness.New(t)

	h.StageMockResponse(harness.CannedResponse{
		Method:    http.MethodPost,
		Path:      "/v1/messages",
		Streaming: true,
		Headers:   map[string]string{"Content-Type": "text/event-stream"},
		StreamChunks: []harness.CannedStreamChunk{
			{Data: `{"type":"message_start","message":{"id":"msg_ts2","role":"assistant","content":[]}}`},
			{Data: `{"type":"content_block_start","index":0,"content_block":{"type":"server_tool_use","id":"srvtoolu_xyz789","name":"tool_search_tool_regex"}}`},
			{Data: `{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"pattern\":\"weather\"}"}}`},
			{Data: `{"type":"content_block_start","index":1,"content_block":{"type":"tool_search_tool_result","tool_use_id":"srvtoolu_xyz789","content":{"type":"tool_search_tool_search_result","tool_references":[{"type":"tool_reference","tool_name":"get_weather"}]}}}`},
			{Data: `{"type":"message_stop"}`},
		},
	})

	body := map[string]any{
		"model":      "claude-opus-4-6",
		"max_tokens": 64,
		"stream":     true,
		"messages":   []map[string]string{{"role": "user", "content": "weather?"}},
		"tools": []map[string]any{
			{"type": "tool_search_tool_regex_20251119", "name": "tool_search_tool_regex"},
			{"name": "get_weather", "description": "d", "input_schema": map[string]any{"type": "object"}, "defer_loading": true},
		},
	}
	stream := h.PostStream("/v1/messages", body, nil)
	chunks := stream.CollectAll(5 * time.Second)
	if len(chunks) != 5 {
		t.Fatalf("chunks=%d want 5", len(chunks))
	}
	joined := ""
	for _, c := range chunks {
		joined += c.Data
	}
	if !strings.Contains(joined, `"tool_search_tool_result"`) || !strings.Contains(joined, `"tool_reference"`) {
		t.Fatalf("stream missing tool-search blocks: %s", joined)
	}
}
