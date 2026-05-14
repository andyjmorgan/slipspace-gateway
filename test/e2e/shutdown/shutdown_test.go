//go:build e2e

package shutdown_test

import (
	"context"
	"errors"
	"net/http"
	"syscall"
	"testing"
	"time"

	"github.com/andyjmorgan/sluice-gateway/test/e2e/harness"
)

// TestShutdown_SIGTERM_ExitsCleanly asserts the gateway responds to SIGTERM
// by closing the listener and exiting within the drain budget, without
// hanging in-flight requests indefinitely. The current server design wires
// BaseContext to the run context so streaming bodies get cancelled on
// shutdown rather than draining to completion; this test pins that
// behaviour so we notice if either the drain semantics or the exit path
// regresses.
func TestShutdown_SIGTERM_ExitsCleanly(t *testing.T) {
	t.Parallel()
	h := harness.NewWithOptions(t, harness.Options{
		DrainTimeoutSeconds: 5,
	})

	h.StageMockResponse(harness.CannedResponse{
		Method:    http.MethodPost,
		Path:      "/v1/chat/completions",
		Streaming: true,
		Headers:   map[string]string{"Content-Type": "text/event-stream"},
		StreamChunks: []harness.CannedStreamChunk{
			{Data: `{"choices":[{"delta":{"role":"assistant"}}]}`},
			{Data: `{"choices":[{"delta":{"content":"a"}}]}`, DelayMs: 500},
			{Data: `{"choices":[{"delta":{"content":"b"}}]}`, DelayMs: 500},
			{Data: "[DONE]"},
		},
	})

	stream := h.PostStream("/v1/chat/completions",
		map[string]any{"model": "x", "stream": true, "messages": []map[string]string{{"role": "user", "content": "."}}},
		nil)

	first, err := stream.Next()
	if err != nil {
		t.Fatalf("first chunk: %v", err)
	}
	if first.IsDone() {
		t.Fatalf("first chunk unexpectedly [DONE]")
	}

	if err := h.SendGatewaySignal(syscall.SIGTERM); err != nil {
		t.Fatalf("send SIGTERM: %v", err)
	}

	_ = stream.Close()

	if err := h.WaitGatewayExit(15 * time.Second); errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("gateway did not exit within 15s after SIGTERM")
	}
}

// TestShutdown_NewRequestsRejected_AfterTerm asserts that once the drain
// has begun, the listener stops accepting new connections promptly.
func TestShutdown_NewRequestsRejected_AfterTerm(t *testing.T) {
	t.Parallel()
	h := harness.NewWithOptions(t, harness.Options{
		DrainTimeoutSeconds: 5,
	})

	if err := h.SendGatewaySignal(syscall.SIGTERM); err != nil {
		t.Fatalf("send SIGTERM: %v", err)
	}
	if err := h.WaitGatewayExit(15 * time.Second); errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("gateway did not exit within 15s after SIGTERM")
	}

	req, err := http.NewRequest(http.MethodGet, h.GatewayURL+"/healthz", http.NoBody)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Do(req)
	if err == nil {
		_ = resp.Body.Close()
		t.Fatalf("expected connect failure post-shutdown; got status %d", resp.StatusCode)
	}
}
