package sseframe_test

import (
	"testing"

	"github.com/andyjmorgan/sluice-gateway/internal/middleware/sseframe"
)

func TestCollate_JSONBody(t *testing.T) {
	t.Parallel()
	got := sseframe.Collate([]byte(`  {"a":1}`))
	if len(got) != 1 || string(got[0]) != `  {"a":1}` {
		t.Fatalf("JSON body should collate to one frame, got %d: %q", len(got), got)
	}
	// Array body also counts as JSON.
	if got := sseframe.Collate([]byte(`[1,2]`)); len(got) != 1 {
		t.Fatalf("array body should be one frame, got %d", len(got))
	}
}

func TestCollate_SSE(t *testing.T) {
	t.Parallel()
	raw := []byte("event: message_start\ndata: {\"type\":\"message_start\"}\n\n" +
		"data: {\"type\":\"message_delta\"}\n\n" +
		": this is a comment\n\n" +
		"data: [DONE]\n\n")
	got := sseframe.Collate(raw)
	if len(got) != 2 {
		t.Fatalf("want 2 frames (comment + [DONE] dropped), got %d: %q", len(got), got)
	}
	if string(got[0]) != `{"type":"message_start"}` || string(got[1]) != `{"type":"message_delta"}` {
		t.Errorf("frames = %q", got)
	}
}

func TestCollate_MultilineData(t *testing.T) {
	t.Parallel()
	// SSE permits multiple data: lines per event, joined by \n.
	raw := []byte("data: {\"a\":\ndata: 1}\n\n")
	got := sseframe.Collate(raw)
	if len(got) != 1 || string(got[0]) != "{\"a\":\n1}" {
		t.Fatalf("multiline data join failed: %q", got)
	}
}

func TestCollate_EmptyAndBlank(t *testing.T) {
	t.Parallel()
	if got := sseframe.Collate(nil); got != nil {
		t.Errorf("nil input should yield nil, got %q", got)
	}
	if got := sseframe.Collate([]byte("")); got != nil {
		t.Errorf("empty input should yield nil, got %q", got)
	}
	// SSE with only a [DONE] sentinel yields no frames.
	if got := sseframe.Collate([]byte("data: [DONE]\n\n")); len(got) != 0 {
		t.Errorf("only-[DONE] should yield no frames, got %q", got)
	}
}
