package bodycapture

import (
	"testing"

	"github.com/andyjmorgan/sluice-gateway/protocols/anthropic/messages"
	"github.com/andyjmorgan/sluice-gateway/protocols/gemini/content"
	openaichat "github.com/andyjmorgan/sluice-gateway/protocols/openai/chat"
	openairesponses "github.com/andyjmorgan/sluice-gateway/protocols/openai/responses"
)

// TestModel exercises every branch of the centralised type-switch
// that used to live in three places (cmd/gateway.outboundModel,
// rules.extractInboundModel, bodycapture.allocate). A future body
// shape without a case here regresses silently into rules + telemetry
// — this test is the regression guard.
func TestModel(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		body any
		want string
	}{
		{"openai chat", &openaichat.ChatCompletionRequest{Model: "gpt-4o"}, "gpt-4o"},
		{"openai responses", &openairesponses.ResponsesRequest{Model: "gpt-4o-mini"}, "gpt-4o-mini"},
		{"anthropic messages", &messages.MessagesRequest{Model: "claude-haiku-4-5"}, "claude-haiku-4-5"},
		{"gemini generate (model lives on path, not body)", &content.GenerateContentRequest{}, ""},
		{"openai chat with surrounding whitespace", &openaichat.ChatCompletionRequest{Model: "  gpt-4o  "}, "gpt-4o"},
		{"openai chat empty model", &openaichat.ChatCompletionRequest{Model: ""}, ""},
		{"nil body (passthrough endpoints)", nil, ""},
		{"unknown body type (forward-compat fallback)", &unknownBody{}, ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Model(tc.body); got != tc.want {
				t.Errorf("Model(%T) = %q, want %q", tc.body, got, tc.want)
			}
		})
	}
}

type unknownBody struct{}
