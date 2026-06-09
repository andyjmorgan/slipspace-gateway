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

func TestCorrelation_ClaudeAgentID_EchoedAsConversation(t *testing.T) {
	t.Parallel()
	h := harness.New(t)

	h.StageMockResponse(harness.CannedResponse{
		Method: http.MethodPost,
		Path:   "/v1/chat/completions",
		Body:   `{"id":"x"}`,
	})

	// X-Claude-Code-Agent-Id is a subagent thread, not a named agent: it
	// resolves onto the conversation axis and echoes under X-Sluice-Thread-Id,
	// and must NOT populate the named-agent header.
	const tid = "claude-thread-123"
	resp := h.PostJSON("/v1/chat/completions",
		map[string]any{"model": "gpt", "messages": []map[string]string{{"role": "user", "content": "."}}},
		http.Header{"X-Claude-Code-Agent-Id": []string{tid}})

	if got := resp.Header.Get("X-Sluice-Thread-Id"); got != tid {
		t.Fatalf("X-Sluice-Thread-Id=%q want %q", got, tid)
	}
	if got := resp.Header.Get("X-Sluice-Agent-Id"); got != "" {
		t.Errorf("X-Sluice-Agent-Id=%q, want empty — a subagent thread is not a named agent", got)
	}
}

func TestCorrelation_NamedAgentID_Echoed(t *testing.T) {
	t.Parallel()
	h := harness.New(t)

	h.StageMockResponse(harness.CannedResponse{
		Method: http.MethodPost,
		Path:   "/v1/chat/completions",
		Body:   `{"id":"x"}`,
	})

	// gen_ai.agent.id is reserved for a genuinely named agent: only the
	// authoritative X-Sluice-Agent-Id feeds it.
	const aid = "reviewer"
	resp := h.PostJSON("/v1/chat/completions",
		map[string]any{"model": "gpt", "messages": []map[string]string{{"role": "user", "content": "."}}},
		http.Header{"X-Sluice-Agent-Id": []string{aid}})

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

func TestCorrelation_CodexSubagent_Echoed(t *testing.T) {
	t.Parallel()
	h := harness.New(t)

	h.StageMockResponse(harness.CannedResponse{
		Method: http.MethodPost,
		Path:   "/v1/chat/completions",
		Body:   `{"id":"x"}`,
	})

	// Codex subagent: distinct Thread-Id with an explicit parent. The gateway
	// echoes the session bundle root and the resolved conversation/thread.
	resp := h.PostJSON("/v1/chat/completions",
		map[string]any{"model": "gpt", "messages": []map[string]string{{"role": "user", "content": "."}}},
		http.Header{
			"Session-Id":               []string{"codex-sess-1"},
			"Thread-Id":                []string{"codex-thread-2"},
			"X-Codex-Parent-Thread-Id": []string{"codex-sess-1"},
		})

	if got := resp.Header.Get("X-Sluice-Session-Id"); got != "codex-sess-1" {
		t.Errorf("X-Sluice-Session-Id=%q want codex-sess-1", got)
	}
	if got := resp.Header.Get("X-Sluice-Thread-Id"); got != "codex-thread-2" {
		t.Errorf("X-Sluice-Thread-Id=%q want codex-thread-2", got)
	}
}

func TestCorrelation_UserID_Echoed(t *testing.T) {
	t.Parallel()
	h := harness.New(t)

	h.StageMockResponse(harness.CannedResponse{
		Method: http.MethodPost,
		Path:   "/v1/chat/completions",
		Body:   `{"id":"x"}`,
	})

	// There is no shipped client default for user id, so send it under the
	// authoritative Sluice header; the gateway resolves and echoes it.
	const uid = "user-abc-123"
	resp := h.PostJSON("/v1/chat/completions",
		map[string]any{"model": "gpt", "messages": []map[string]string{{"role": "user", "content": "."}}},
		http.Header{"X-Sluice-User-Id": []string{uid}})

	if got := resp.Header.Get("X-Sluice-User-Id"); got != uid {
		t.Fatalf("X-Sluice-User-Id=%q want %q", got, uid)
	}
}

func TestCorrelation_UserID_NotEchoed_WhenAbsent(t *testing.T) {
	t.Parallel()
	h := harness.New(t)

	h.StageMockResponse(harness.CannedResponse{
		Method: http.MethodPost,
		Path:   "/v1/chat/completions",
		Body:   `{"id":"x"}`,
	})

	resp := h.PostJSON("/v1/chat/completions",
		map[string]any{"model": "gpt", "messages": []map[string]string{{"role": "user", "content": "."}}}, nil)

	if got := resp.Header.Get("X-Sluice-User-Id"); got != "" {
		t.Errorf("X-Sluice-User-Id=%q, want empty when not supplied", got)
	}
}
