package backfill_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/andyjmorgan/sluice-gateway/contracts/config"
	"github.com/andyjmorgan/sluice-gateway/internal/telemetry/backfill"
)

// loadFixture reads a committed testdata body or fails the test.
func loadFixture(t *testing.T, name string) []byte {
	t.Helper()
	// nolint:gosec // G304: test-only read of a committed testdata fixture by literal name.
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return b
}

// TestResolveProtocol_RequestFixtures exercises the request-only path against
// the real captured second-turn (tool-result) request bodies plus the
// constructed anthropic body. These are the historical schema-v2 records that
// carry no protocol and no response body, so the request shape is the only
// signal.
func TestResolveProtocol_RequestFixtures(t *testing.T) {
	cases := []struct {
		fixture string
		want    string
	}{
		{"openai_chat.json", config.ProtocolChat},
		{"openai_responses.json", config.ProtocolResponses},
		{"gemini_generate.json", config.ProtocolGenerateContent},
		{"anthropic_messages.json", config.ProtocolMessages},
	}
	for _, tc := range cases {
		t.Run(tc.fixture, func(t *testing.T) {
			body := loadFixture(t, tc.fixture)
			got, ok := backfill.ResolveProtocol(body, nil)
			if !ok || got != tc.want {
				t.Fatalf("ResolveProtocol(%s, nil) = (%q, %v), want (%q, true)", tc.fixture, got, ok, tc.want)
			}
		})
	}
}

// TestResolveProtocol_RequestSynthetic covers request-shape branches the
// fixtures don't reach: bare discriminators, the anthropic-vs-chat split via
// each secondary feature, and the Responses "instructions" fallback.
func TestResolveProtocol_RequestSynthetic(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
		ok   bool
	}{
		// Gemini: contents, generationConfig, systemInstruction each suffice.
		{"gemini contents", `{"contents":[{"parts":[{"text":"hi"}]}]}`, config.ProtocolGenerateContent, true},
		{"gemini generationConfig only", `{"generationConfig":{"maxOutputTokens":8}}`, config.ProtocolGenerateContent, true},
		{"gemini systemInstruction only", `{"systemInstruction":{"parts":[{"text":"sys"}]}}`, config.ProtocolGenerateContent, true},
		// Responses: input array or instructions.
		{"responses input", `{"input":[{"role":"user","content":"hi"}]}`, config.ProtocolResponses, true},
		{"responses input typed items", `{"input":[{"type":"function_call_output","call_id":"c","output":"x"}]}`, config.ProtocolResponses, true},
		{"responses instructions only", `{"instructions":"be terse","model":"gpt-4o"}`, config.ProtocolResponses, true},
		// Anthropic: each secondary feature alone flips a messages body.
		{"anthropic via max_tokens", `{"messages":[{"role":"user","content":"hi"}],"max_tokens":16}`, config.ProtocolMessages, true},
		{"anthropic via system", `{"messages":[{"role":"user","content":"hi"}],"system":"s"}`, config.ProtocolMessages, true},
		{"anthropic via input_schema tool", `{"messages":[{"role":"user","content":"hi"}],"tools":[{"name":"t","input_schema":{"type":"object"}}]}`, config.ProtocolMessages, true},
		{"anthropic via tool_use block", `{"messages":[{"role":"assistant","content":[{"type":"tool_use","id":"t","name":"f","input":{}}]}]}`, config.ProtocolMessages, true},
		{"anthropic via tool_result block", `{"messages":[{"role":"user","content":[{"type":"tool_result","tool_use_id":"t","content":"x"}]}]}`, config.ProtocolMessages, true},
		// OpenAI chat: messages with no anthropic feature.
		{"chat plain messages", `{"messages":[{"role":"user","content":"hi"}],"model":"gpt-4o"}`, config.ProtocolChat, true},
		{"chat tool_calls", `{"messages":[{"role":"assistant","tool_calls":[{"id":"c","type":"function","function":{"name":"f","arguments":"{}"}}]}]}`, config.ProtocolChat, true},
		{"chat role tool", `{"messages":[{"role":"tool","tool_call_id":"c","content":"x"}]}`, config.ProtocolChat, true},
		{"chat function-wrapper tool", `{"messages":[{"role":"user","content":"hi"}],"tools":[{"type":"function","function":{"name":"f","parameters":{}}}]}`, config.ProtocolChat, true},
		{"chat max_completion_tokens not max_tokens", `{"messages":[{"role":"user","content":"hi"}],"max_completion_tokens":16}`, config.ProtocolChat, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := backfill.ResolveProtocol([]byte(tc.body), nil)
			if got != tc.want || ok != tc.ok {
				t.Fatalf("ResolveProtocol(%q, nil) = (%q, %v), want (%q, %v)", tc.body, got, ok, tc.want, tc.ok)
			}
		})
	}
}

// TestResolveProtocol_ResponsePreferred verifies the response body wins over a
// (possibly ambiguous) request body, including the messages collision where
// the response object is the only clean discriminator.
func TestResolveProtocol_ResponsePreferred(t *testing.T) {
	// A shared "messages" request body that the request path would call chat;
	// the response object overrides it to anthropic-messages.
	chatRequest := `{"messages":[{"role":"user","content":"hi"}]}`
	cases := []struct {
		name string
		req  string
		resp string
		want string
	}{
		{"chat.completion", chatRequest, `{"object":"chat.completion","choices":[{"index":0}]}`, config.ProtocolChat},
		{"chat.completion.chunk", chatRequest, `{"object":"chat.completion.chunk","choices":[]}`, config.ProtocolChat},
		{"responses object", `{"input":[]}`, `{"object":"response","output":[]}`, config.ProtocolResponses},
		{"anthropic type message", chatRequest, `{"type":"message","role":"assistant","content":[],"stop_reason":"end_turn"}`, config.ProtocolMessages},
		{"gemini candidates", `{"contents":[]}`, `{"candidates":[{"content":{"parts":[{"text":"hi"}]}}]}`, config.ProtocolGenerateContent},
		// Structural fallbacks when the object/type tag is absent.
		{"choices fallback", chatRequest, `{"choices":[{"index":0}]}`, config.ProtocolChat},
		{"output fallback", `{"input":[]}`, `{"output":[{"type":"message"}]}`, config.ProtocolResponses},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := backfill.ResolveProtocol([]byte(tc.req), []byte(tc.resp))
			if !ok || got != tc.want {
				t.Fatalf("ResolveProtocol = (%q, %v), want (%q, true)", got, ok, tc.want)
			}
		})
	}
}

// TestResolveProtocol_ResponseInconclusiveFallsBack verifies that a response
// body with no recognized discriminator does not short-circuit — the request
// body still classifies the record.
func TestResolveProtocol_ResponseInconclusiveFallsBack(t *testing.T) {
	got, ok := backfill.ResolveProtocol(
		[]byte(`{"contents":[{"parts":[{"text":"hi"}]}]}`),
		[]byte(`{"modelVersion":"m"}`), // gemini response sans candidates
	)
	if !ok || got != config.ProtocolGenerateContent {
		t.Fatalf("ResolveProtocol = (%q, %v), want (generate_content, true)", got, ok)
	}
}

// TestResolveProtocol_NoMatch covers the negative space: empty, garbage,
// non-object, and ambiguous bodies all return ("", false).
func TestResolveProtocol_NoMatch(t *testing.T) {
	cases := []struct {
		name string
		req  string
		resp string
	}{
		{"both empty", "", ""},
		{"nil-ish whitespace", "   \n\t ", "  "},
		{"garbage request", "not json at all", ""},
		{"garbage response only", "", "}{ broken"},
		{"empty object", "{}", ""},
		{"empty object both", "{}", "{}"},
		{"json array request", `[{"role":"user"}]`, ""},
		{"json scalar", `42`, ""},
		{"json string", `"messages"`, ""},
		{"unrelated object", `{"foo":"bar","n":1}`, ""},
		{"empty messages array", `{"messages":[]}`, ""},
		{"truncated object", `{"messages":[{"role":`, ""},
		{"null literal", `null`, ""},
		// Response body opens with '{' (passes the object precheck) but is
		// truncated, so the response decode errors and the empty request
		// stays unclassified.
		{"truncated response object", "", `{"object":"chat.`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := backfill.ResolveProtocol([]byte(tc.req), []byte(tc.resp))
			if ok || got != "" {
				t.Fatalf("ResolveProtocol = (%q, %v), want (\"\", false)", got, ok)
			}
		})
	}
}

// TestResolveProtocol_MalformedToolsDoNotPanic ensures the secondary-feature
// decoders tolerate tools/messages shaped unexpectedly (a string, a number)
// and fall through to the chat default rather than erroring out.
func TestResolveProtocol_MalformedToolsDoNotPanic(t *testing.T) {
	cases := []string{
		`{"messages":[{"role":"user","content":"hi"}],"tools":"oops"}`,
		`{"messages":[{"role":"user","content":42}]}`,
		`{"messages":[{"role":"user","content":["raw","strings"]}]}`,
		// messages is a non-empty array (passes hasArrayElements) but the
		// elements are scalars, so the content-block decode errors and the
		// body falls through to the chat default.
		`{"messages":[1,2,3]}`,
	}
	for _, body := range cases {
		got, ok := backfill.ResolveProtocol([]byte(body), nil)
		if !ok || got != config.ProtocolChat {
			t.Fatalf("ResolveProtocol(%q) = (%q, %v), want (chat, true)", body, got, ok)
		}
	}
}
