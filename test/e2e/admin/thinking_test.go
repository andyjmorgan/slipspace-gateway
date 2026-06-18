//go:build e2e

package admin_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	adminc "github.com/andyjmorgan/slipspace-gateway/contracts/admin"
	anthropicmessages "github.com/andyjmorgan/slipspace-gateway/protocols/anthropic/messages"
	"github.com/andyjmorgan/slipspace-gateway/test/e2e/harness"
)

// TestAdmin_ThinkingStream_ReassembledIntoBody drives a streamed Anthropic
// /v1/messages response that interleaves a thinking block (thinking_delta +
// signature_delta) with a text block through the real binary, then asserts the
// admin body store's ResponseAssembled carries the reconstructed thinking block
// — proving the live-feed accumulator wires thinking + signature back together
// end to end.
func TestAdmin_ThinkingStream_ReassembledIntoBody(t *testing.T) {
	t.Parallel()
	h := harness.NewWithOptions(t, harness.Options{AdminEnabled: true})

	h.StageMockResponse(harness.CannedResponse{
		Method:    http.MethodPost,
		Path:      "/v1/messages",
		Streaming: true,
		Headers:   map[string]string{"Content-Type": "text/event-stream"},
		StreamChunks: []harness.CannedStreamChunk{
			{Data: `{"type":"message_start","message":{"id":"msg_think","type":"message","role":"assistant","content":[],"model":"claude-opus-4-7","usage":{"input_tokens":5,"output_tokens":0}}}`},
			{Data: `{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":"","signature":""}}`},
			{Data: `{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"Let me ","estimated_tokens":12}}`},
			{Data: `{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"reason it out."}}`},
			{Data: `{"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"EpkECmMIDhgC"}}`},
			{Data: `{"type":"content_block_stop","index":0}`},
			{Data: `{"type":"content_block_start","index":1,"content_block":{"type":"text","text":""}}`},
			{Data: `{"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"The answer is 42."}}`},
			{Data: `{"type":"content_block_stop","index":1}`},
			{Data: `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"input_tokens":5,"output_tokens":9}}`},
			{Data: `{"type":"message_stop"}`},
		},
	})

	stream := h.PostStream("/v1/messages",
		map[string]any{
			"model":      "claude-opus-4-7",
			"max_tokens": 100,
			"stream":     true,
			"thinking":   map[string]any{"type": "enabled", "budget_tokens": 1024},
			"messages":   []map[string]string{{"role": "user", "content": "what is the answer?"}},
		}, nil)
	_ = stream.CollectAll(5 * time.Second)

	detail := pollMessageBody(t, h, "messages")
	if detail.ResponseAssembled == "" {
		t.Fatalf("ResponseAssembled empty; accumulator produced nothing")
	}

	var resp anthropicmessages.MessagesResponse
	if err := json.Unmarshal([]byte(detail.ResponseAssembled), &resp); err != nil {
		t.Fatalf("assembled does not parse as MessagesResponse: %v\n%s", err, detail.ResponseAssembled)
	}
	if len(resp.Content) != 2 {
		t.Fatalf("assembled Content=%d want 2 (thinking, text)", len(resp.Content))
	}
	tb, ok := resp.Content[0].(*anthropicmessages.ThinkingBlock)
	if !ok {
		t.Fatalf("block[0]=%T want *ThinkingBlock", resp.Content[0])
	}
	if tb.Thinking != "Let me reason it out." {
		t.Errorf("reassembled thinking=%q", tb.Thinking)
	}
	if tb.Signature != "EpkECmMIDhgC" {
		t.Errorf("reassembled signature=%q", tb.Signature)
	}
	if txt, ok := resp.Content[1].(*anthropicmessages.TextBlock); !ok || txt.Text != "The answer is 42." {
		t.Errorf("block[1]=%T %+v", resp.Content[1], resp.Content[1])
	}
}

// pollMessageBody waits for a live-feed entry on endpoint to appear, then
// fetches and returns its captured body detail.
func pollMessageBody(t *testing.T, h *harness.Harness, endpoint string) adminc.MessageBodyDetail {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		eventID := recentEventID(t, h, endpoint)
		if eventID != "" {
			req, _ := http.NewRequest(http.MethodGet, h.AdminURL+"/api/v1/messages/"+eventID+"/body", nil)
			req.SetBasicAuth(adminc.Username, h.AdminPassword)
			resp, err := h.HTTP.Do(req)
			if err != nil {
				t.Fatalf("GET body: %v", err)
			}
			if resp.StatusCode == http.StatusOK {
				var detail adminc.MessageBodyDetail
				derr := json.NewDecoder(resp.Body).Decode(&detail)
				_ = resp.Body.Close()
				if derr != nil {
					t.Fatalf("decode body detail: %v", derr)
				}
				if detail.ResponseAssembled != "" {
					return detail
				}
			} else {
				_ = resp.Body.Close()
			}
		}
		time.Sleep(150 * time.Millisecond)
	}
	t.Fatalf("no %s body with assembled response appeared", endpoint)
	return adminc.MessageBodyDetail{}
}

func recentEventID(t *testing.T, h *harness.Harness, endpoint string) string {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, h.AdminURL+"/api/v1/messages/recent", nil)
	req.SetBasicAuth(adminc.Username, h.AdminPassword)
	resp, err := h.HTTP.Do(req)
	if err != nil {
		t.Fatalf("GET messages/recent: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	var got adminc.MessagesRecentResponse
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode messages/recent: %v", err)
	}
	for i := len(got.Entries) - 1; i >= 0; i-- {
		if strings.EqualFold(got.Entries[i].Protocol, endpoint) {
			return got.Entries[i].EventID
		}
	}
	return ""
}
