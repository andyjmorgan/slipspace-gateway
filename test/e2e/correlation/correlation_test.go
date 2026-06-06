//go:build e2e

package correlation_test

import (
	"net/http"
	"testing"

	"github.com/andyjmorgan/sluice-gateway/test/e2e/harness"
)

func TestCorrelation_Generated_WhenAbsent(t *testing.T) {
	t.Parallel()
	h := harness.New(t)

	h.StageMockResponse(harness.CannedResponse{
		Method: http.MethodPost,
		Path:   "/v1/chat/completions",
		Body:   `{"id":"x"}`,
	})

	resp := h.PostJSON("/v1/chat/completions",
		map[string]any{"model": "gpt", "messages": []map[string]string{{"role": "user", "content": "."}}}, nil)

	if got := resp.Header.Get("X-Sluice-Correlation-Id"); got == "" {
		t.Fatalf("expected generated X-Sluice-Correlation-Id, got empty")
	}
}

func TestCorrelation_Honored_WhenInbound(t *testing.T) {
	t.Parallel()
	h := harness.New(t)

	h.StageMockResponse(harness.CannedResponse{
		Method: http.MethodPost,
		Path:   "/v1/chat/completions",
		Body:   `{"id":"x"}`,
	})

	const id = "client-supplied-correlation-7c3"
	resp := h.PostJSON("/v1/chat/completions",
		map[string]any{"model": "gpt", "messages": []map[string]string{{"role": "user", "content": "."}}},
		http.Header{"X-Sluice-Correlation-Id": []string{id}})

	if got := resp.Header.Get("X-Sluice-Correlation-Id"); got != id {
		t.Fatalf("X-Sluice-Correlation-Id=%q want %q", got, id)
	}
}

func TestCorrelation_SessionID_Echoed(t *testing.T) {
	t.Parallel()
	h := harness.New(t)

	h.StageMockResponse(harness.CannedResponse{
		Method: http.MethodPost,
		Path:   "/v1/chat/completions",
		Body:   `{"id":"x"}`,
	})

	const sid = "session-abc-123"
	resp := h.PostJSON("/v1/chat/completions",
		map[string]any{"model": "gpt", "messages": []map[string]string{{"role": "user", "content": "."}}},
		http.Header{"X-Sluice-Session-Id": []string{sid}})

	if got := resp.Header.Get("X-Sluice-Session-Id"); got != sid {
		t.Fatalf("X-Sluice-Session-Id=%q want %q", got, sid)
	}
}

func TestCorrelation_SessionID_NotEchoed_WhenAbsent(t *testing.T) {
	t.Parallel()
	h := harness.New(t)

	h.StageMockResponse(harness.CannedResponse{
		Method: http.MethodPost,
		Path:   "/v1/chat/completions",
		Body:   `{"id":"x"}`,
	})

	resp := h.PostJSON("/v1/chat/completions",
		map[string]any{"model": "gpt", "messages": []map[string]string{{"role": "user", "content": "."}}}, nil)

	if got := resp.Header.Get("X-Sluice-Session-Id"); got != "" {
		t.Errorf("X-Sluice-Session-Id=%q, want empty when not supplied", got)
	}
}

func TestCorrelation_AgentID_Echoed(t *testing.T) {
	t.Parallel()
	h := harness.New(t)

	h.StageMockResponse(harness.CannedResponse{
		Method: http.MethodPost,
		Path:   "/v1/chat/completions",
		Body:   `{"id":"x"}`,
	})

	// Sent under the shipped client default header; the gateway resolves it
	// and echoes it under the authoritative Sluice agent header.
	const aid = "agent-abc-123"
	resp := h.PostJSON("/v1/chat/completions",
		map[string]any{"model": "gpt", "messages": []map[string]string{{"role": "user", "content": "."}}},
		http.Header{"X-Claude-Code-Agent-Id": []string{aid}})

	if got := resp.Header.Get("X-Sluice-Agent-Id"); got != aid {
		t.Fatalf("X-Sluice-Agent-Id=%q want %q", got, aid)
	}
}

func TestCorrelation_AgentID_NotEchoed_WhenAbsent(t *testing.T) {
	t.Parallel()
	h := harness.New(t)

	h.StageMockResponse(harness.CannedResponse{
		Method: http.MethodPost,
		Path:   "/v1/chat/completions",
		Body:   `{"id":"x"}`,
	})

	resp := h.PostJSON("/v1/chat/completions",
		map[string]any{"model": "gpt", "messages": []map[string]string{{"role": "user", "content": "."}}}, nil)

	if got := resp.Header.Get("X-Sluice-Agent-Id"); got != "" {
		t.Errorf("X-Sluice-Agent-Id=%q, want empty when not supplied", got)
	}
}
