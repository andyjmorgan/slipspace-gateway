//go:build e2e

package admin_test

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	openairesponses "github.com/andyjmorgan/slipspace-gateway/protocols/openai/responses"
	"github.com/andyjmorgan/slipspace-gateway/test/e2e/harness"
)

// TestAdmin_ResponsesStream_OutputItemsFolded drives a streamed OpenAI
// /v1/responses response whose terminal `response.completed` ships an empty
// output[] (the store:false / gpt-5-class shape) while the real items arrive
// as `response.output_item.done` events. It asserts the admin body store's
// ResponseAssembled folds those finalized items back into output[], proving
// streaming↔non-streaming parity end to end through the binary (#362).
func TestAdmin_ResponsesStream_OutputItemsFolded(t *testing.T) {
	t.Parallel()
	h := harness.NewWithOptions(t, harness.Options{AdminEnabled: true})

	h.StageMockResponse(harness.CannedResponse{
		Method:    http.MethodPost,
		Path:      "/v1/responses",
		Streaming: true,
		Headers:   map[string]string{"Content-Type": "text/event-stream"},
		StreamChunks: []harness.CannedStreamChunk{
			{Data: `{"type":"response.created","response":{"id":"resp_e2e","object":"response","status":"in_progress","model":"gpt-5"}}`},
			{Data: `{"type":"response.output_item.done","output_index":0,"item":{"type":"reasoning","id":"rs_1","summary":[],"encrypted_content":"ENC=="}}`},
			{Data: `{"type":"response.output_item.done","output_index":1,"item":{"type":"function_call","id":"fc_1","status":"completed","name":"get_weather","arguments":"{\"city\":\"SF\"}","call_id":"call_1"}}`},
			{Data: `{"type":"response.completed","response":{"id":"resp_e2e","object":"response","status":"completed","model":"gpt-5","output":[]}}`},
		},
	})

	stream := h.PostStream("/v1/responses",
		map[string]any{"model": "gpt-5", "input": "weather in SF?", "stream": true}, nil)
	_ = stream.CollectAll(5 * time.Second)

	detail := pollMessageBody(t, h, "responses")
	if detail.ResponseAssembled == "" {
		t.Fatalf("ResponseAssembled empty; accumulator produced nothing")
	}

	var resp openairesponses.ResponsesResponse
	if err := json.Unmarshal([]byte(detail.ResponseAssembled), &resp); err != nil {
		t.Fatalf("assembled does not parse as ResponsesResponse: %v\n%s", err, detail.ResponseAssembled)
	}
	if resp.Status != "completed" {
		t.Errorf("Status=%q want completed", resp.Status)
	}
	if len(resp.Output) != 2 {
		t.Fatalf("assembled Output=%d want 2 (folded from output_item.done)\n%s", len(resp.Output), detail.ResponseAssembled)
	}
	if resp.Output[0].Type != "reasoning" {
		t.Errorf("Output[0].Type=%q want reasoning (ordered by output_index)", resp.Output[0].Type)
	}
	if _, ok := resp.Output[0].Extra["encrypted_content"]; !ok {
		t.Errorf("reasoning encrypted_content dropped; Extra=%v", resp.Output[0].Extra)
	}
	if resp.Output[1].Type != "function_call" || resp.Output[1].Name != "get_weather" {
		t.Errorf("Output[1] not the function_call item: %+v", resp.Output[1])
	}
	if resp.Output[1].Arguments != `{"city":"SF"}` {
		t.Errorf("function_call arguments=%q want preserved JSON string", resp.Output[1].Arguments)
	}
}
