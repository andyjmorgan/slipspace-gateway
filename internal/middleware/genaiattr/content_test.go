package genaiattr_test

import (
	"strings"
	"testing"

	"github.com/andyjmorgan/sluice-gateway/internal/middleware/genaiattr"
)

func TestExtractContent_OpenAIChat(t *testing.T) {
	t.Parallel()
	raw := []byte(`{
		"messages": [
			{"role": "system", "content": "Be terse."},
			{"role": "user", "content": "first question"},
			{"role": "assistant", "content": "an answer"},
			{"role": "user", "content": "latest question"}
		],
		"tools": [{"type": "function", "function": {"name": "get_weather"}}]
	}`)
	c := genaiattr.ExtractContent("chat_completions", raw)
	if c.SystemInstructions != "Be terse." {
		t.Errorf("system = %q", c.SystemInstructions)
	}
	// Only the latest user turn is kept (bounded).
	if c.LatestUserText != "latest question" {
		t.Errorf("latest user = %q, want 'latest question'", c.LatestUserText)
	}
	if !strings.Contains(string(c.ToolDefinitions), "get_weather") {
		t.Errorf("tools = %s", c.ToolDefinitions)
	}
}

func TestExtractContent_OpenAIChat_ArrayContent(t *testing.T) {
	t.Parallel()
	// content as an array of parts (vision-style) flattens to its text.
	raw := []byte(`{"messages":[{"role":"user","content":[{"type":"text","text":"hello "},{"type":"text","text":"world"}]}]}`)
	c := genaiattr.ExtractContent("chat_completions", raw)
	if c.LatestUserText != "hello world" {
		t.Errorf("latest user = %q, want 'hello world'", c.LatestUserText)
	}
	if c.ToolDefinitions != nil {
		t.Errorf("tools should be nil when absent, got %s", c.ToolDefinitions)
	}
}

func TestExtractContent_Anthropic(t *testing.T) {
	t.Parallel()
	raw := []byte(`{"system":"You are Claude.","messages":[{"role":"user","content":"hi"},{"role":"assistant","content":"hello"},{"role":"user","content":"bye"}],"tools":[{"name":"t"}]}`)
	c := genaiattr.ExtractContent("messages", raw)
	if c.SystemInstructions != "You are Claude." {
		t.Errorf("system = %q", c.SystemInstructions)
	}
	if c.LatestUserText != "bye" {
		t.Errorf("latest user = %q, want 'bye'", c.LatestUserText)
	}
	if !strings.Contains(string(c.ToolDefinitions), "\"t\"") {
		t.Errorf("tools = %s", c.ToolDefinitions)
	}
}

func TestExtractContent_Anthropic_SystemBlocks(t *testing.T) {
	t.Parallel()
	// Anthropic system may be an array of text blocks.
	raw := []byte(`{"system":[{"type":"text","text":"part one "},{"type":"text","text":"part two"}],"messages":[{"role":"user","content":"q"}]}`)
	c := genaiattr.ExtractContent("messages", raw)
	if c.SystemInstructions != "part one part two" {
		t.Errorf("system = %q", c.SystemInstructions)
	}
}

func TestExtractContent_Gemini(t *testing.T) {
	t.Parallel()
	raw := []byte(`{"systemInstruction":{"parts":[{"text":"sys"}]},"contents":[{"role":"user","parts":[{"text":"q1"}]},{"role":"model","parts":[{"text":"a1"}]},{"role":"user","parts":[{"text":"q2"}]}],"tools":[{"functionDeclarations":[]}]}`)
	c := genaiattr.ExtractContent("generate_content", raw)
	if c.SystemInstructions != "sys" {
		t.Errorf("system = %q", c.SystemInstructions)
	}
	if c.LatestUserText != "q2" {
		t.Errorf("latest user = %q, want q2", c.LatestUserText)
	}
	if len(c.ToolDefinitions) == 0 {
		t.Errorf("tools should be present")
	}
}

func TestExtractContent_OpenAIResponses(t *testing.T) {
	t.Parallel()
	// Responses API: instructions = system, input = prompt (string form).
	c := genaiattr.ExtractContent("responses", []byte(`{"instructions":"be brief","input":"hello there","tools":[{"type":"function"}]}`))
	if c.SystemInstructions != "be brief" {
		t.Errorf("system = %q", c.SystemInstructions)
	}
	if c.LatestUserText != "hello there" {
		t.Errorf("latest user (string input) = %q", c.LatestUserText)
	}
	if len(c.ToolDefinitions) == 0 {
		t.Errorf("tools should be present")
	}
	// input as an array of items: latest user item's text wins.
	c2 := genaiattr.ExtractContent("responses", []byte(`{"input":[{"role":"user","content":[{"type":"input_text","text":"q1"}]},{"role":"user","content":[{"type":"input_text","text":"q2"}]}]}`))
	if c2.LatestUserText != "q2" {
		t.Errorf("latest user (array input) = %q, want q2", c2.LatestUserText)
	}
}

func TestExtractContent_EdgeCases(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		endpoint string
		raw      string
	}{
		{"empty", "chat_completions", ""},
		{"unrecognised endpoint", "embeddings", `{"input":"x"}`},
		{"models endpoint", "models", `{}`},
		{"malformed", "messages", `{nope`},
		{"gemini empty", "generate_content", `{}`},
		{"null tools", "chat_completions", `{"tools":null,"messages":[]}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c := genaiattr.ExtractContent(tc.endpoint, []byte(tc.raw))
			if c.SystemInstructions != "" || c.LatestUserText != "" || c.ToolDefinitions != nil {
				t.Errorf("expected zero Content, got %+v", c)
			}
		})
	}
}
