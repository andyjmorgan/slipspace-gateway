//go:build e2e

package harness

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

// CannedStreamChunk is the test-side mirror of cmd/mockllm.StreamChunk. Kept
// here so test packages do not depend on the mockllm internal package.
type CannedStreamChunk struct {
	Data string `json:"data"`

	DelayMs int `json:"delay_ms,omitempty"`
}

// CannedResponse is the test-side mirror of cmd/mockllm.CannedResponse. Match
// criteria default to "any" when zero-valued.
type CannedResponse struct {
	Method string `json:"method,omitempty"`

	Path string `json:"path,omitempty"`

	RequestBodyContains string `json:"request_body_contains,omitempty"`

	Status int `json:"status,omitempty"`

	Headers map[string]string `json:"headers,omitempty"`

	Streaming bool `json:"streaming,omitempty"`

	Body string `json:"body,omitempty"`

	StreamChunks []CannedStreamChunk `json:"stream_chunks,omitempty"`
}

// StageMockResponse POSTs a canned response to the mockllm control plane.
// Fails the test on any non-2xx reply.
func (h *Harness) StageMockResponse(c CannedResponse) {
	h.T.Helper()

	body, err := json.Marshal(c)
	if err != nil {
		h.T.Fatalf("harness: marshal canned response: %v", err)
	}

	req, err := http.NewRequest(http.MethodPost, h.MockLLMURL+"/control/responses", bytes.NewReader(body))
	if err != nil {
		h.T.Fatalf("harness: build control request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := h.HTTP.Do(req)
	if err != nil {
		h.T.Fatalf("harness: POST /control/responses: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 300 {
		buf, _ := io.ReadAll(resp.Body)
		h.T.Fatalf("harness: /control/responses status=%d body=%s", resp.StatusCode, string(buf))
	}
}

// ResetMockResponses clears all canned responses from the mockllm registry.
// Tests that stage multiple responses across t.Run subtests should call this
// in t.Cleanup to keep iterations hermetic.
func (h *Harness) ResetMockResponses() {
	h.T.Helper()

	req, err := http.NewRequest(http.MethodDelete, h.MockLLMURL+"/control/responses", http.NoBody)
	if err != nil {
		h.T.Fatalf("harness: build control DELETE: %v", err)
	}
	resp, err := h.HTTP.Do(req)
	if err != nil {
		h.T.Fatalf("harness: DELETE /control/responses: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 300 {
		buf, _ := io.ReadAll(resp.Body)
		h.T.Fatalf("harness: /control/responses delete status=%d body=%s", resp.StatusCode, string(buf))
	}
}

// MockState returns the mockllm's current control state (canned responses +
// request count). Useful for tests that assert "upstream was called".
type MockState struct {
	Responses    []CannedResponse `json:"responses"`
	RequestCount uint64           `json:"request_count"`
}

// FetchMockState reads /control/state from mockllm.
func (h *Harness) FetchMockState() MockState {
	h.T.Helper()

	resp, err := h.HTTP.Get(h.MockLLMURL + "/control/state")
	if err != nil {
		h.T.Fatalf("harness: GET /control/state: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	var state MockState
	if err := json.NewDecoder(resp.Body).Decode(&state); err != nil {
		h.T.Fatalf("harness: decode mock state: %v", err)
	}
	return state
}

// MockRequestCount is a convenience wrapper around FetchMockState.
func (h *Harness) MockRequestCount() uint64 {
	return h.FetchMockState().RequestCount
}

// FetchStashObject reads a payload from the NATS Object Store. Used by tests
// asserting the large-payload stash code path published a fetchable object.
func (h *Harness) FetchStashObject(bucket, key string) ([]byte, error) {
	if h.NATS == nil {
		return nil, fmt.Errorf("harness: nats connection not initialized")
	}
	js, err := jetstream.New(h.NATS)
	if err != nil {
		return nil, fmt.Errorf("harness: jetstream: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	store, err := js.ObjectStore(ctx, bucket)
	if err != nil {
		return nil, fmt.Errorf("harness: object store %s: %w", bucket, err)
	}
	data, err := store.GetBytes(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("harness: object %s/%s: %w", bucket, key, err)
	}
	return data, nil
}
