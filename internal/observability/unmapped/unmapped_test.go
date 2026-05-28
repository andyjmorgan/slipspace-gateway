package unmapped

import (
	"encoding/json"
	"reflect"
	"testing"

	anthropicmessages "github.com/andyjmorgan/sluice-gateway/providers/anthropic/messages"
)

func TestRequestFields_NilBody(t *testing.T) {
	if got := RequestFields(nil); got != nil {
		t.Fatalf("nil body: got %v", got)
	}
}

func TestRequestFields_TypedBody(t *testing.T) {
	var req anthropicmessages.MessagesRequest
	if err := json.Unmarshal([]byte(`{"model":"claude","max_tokens":1,"future_param":true}`), &req); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	got := RequestFields(&req)
	if !reflect.DeepEqual(got, []string{"future_param"}) {
		t.Fatalf("got %v, want [future_param]", got)
	}
}

func TestResponseFields_ThinkingBlockUnknownField(t *testing.T) {
	// A field Anthropic adds to a thinking content block surfaces under the
	// typed Content array — request-side raw content would miss it, response
	// typed walking catches it.
	raw := []byte(`{
		"id":"msg_1","type":"message","role":"assistant","model":"claude",
		"content":[{"type":"thinking","thinking":"x","signature":"s","reasoning_id":"r1"}]
	}`)
	got := ResponseFields("messages", raw)
	if !reflect.DeepEqual(got, []string{"content.reasoning_id"}) {
		t.Fatalf("got %v, want [content.reasoning_id]", got)
	}
}

func TestResponseFields_NoUnknowns(t *testing.T) {
	raw := []byte(`{"id":"msg_1","type":"message","role":"assistant","model":"claude","content":[{"type":"text","text":"hi"}]}`)
	if got := ResponseFields("messages", raw); got != nil {
		t.Fatalf("got %v, want nil", got)
	}
}

func TestResponseFields_UnrecognisedEndpoint(t *testing.T) {
	if got := ResponseFields("models", []byte(`{"foo":1}`)); got != nil {
		t.Fatalf("got %v, want nil", got)
	}
}

func TestResponseFields_EmptyAndMalformed(t *testing.T) {
	if got := ResponseFields("messages", nil); got != nil {
		t.Fatalf("empty: got %v", got)
	}
	if got := ResponseFields("messages", []byte(`{not json`)); got != nil {
		t.Fatalf("malformed: got %v", got)
	}
}

func TestResponseFields_EachEndpoint(t *testing.T) {
	cases := map[string]string{
		"chat_completions": `{"id":"c","object":"chat.completion","new_top":1}`,
		"responses":        `{"id":"r","object":"response","new_top":1}`,
		"generate_content": `{"new_top":1}`,
	}
	for endpoint, raw := range cases {
		t.Run(endpoint, func(t *testing.T) {
			got := ResponseFields(endpoint, []byte(raw))
			if !reflect.DeepEqual(got, []string{"new_top"}) {
				t.Fatalf("%s: got %v, want [new_top]", endpoint, got)
			}
		})
	}
}
