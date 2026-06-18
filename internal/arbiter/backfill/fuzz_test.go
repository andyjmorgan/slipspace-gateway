package backfill_test

import (
	"testing"

	"github.com/andyjmorgan/sluice-gateway/contracts/config"
	"github.com/andyjmorgan/sluice-gateway/internal/arbiter/backfill"
)

// FuzzResolveProtocol drives arbitrary request/response byte pairs through the
// resolver. The invariant is total safety — no panic on any input — plus the
// contract that a true result is always one of the four known protocol
// strings and a false result always yields the empty protocol.
func FuzzResolveProtocol(f *testing.F) {
	seeds := []struct {
		req  string
		resp string
	}{
		// One seed per protocol, request-only.
		{`{"messages":[{"role":"user","content":"hi"}],"model":"gpt-4o"}`, ""},
		{`{"messages":[{"role":"user","content":"hi"}],"max_tokens":16,"system":"s"}`, ""},
		{`{"input":[{"role":"user","content":"hi"}],"model":"gpt-4o"}`, ""},
		{`{"contents":[{"parts":[{"text":"hi"}]}],"generationConfig":{"maxOutputTokens":8}}`, ""},
		// Response-driven seeds.
		{"", `{"object":"chat.completion","choices":[]}`},
		{"", `{"object":"response","output":[]}`},
		{"", `{"type":"message","role":"assistant","content":[],"stop_reason":"end_turn"}`},
		{"", `{"candidates":[{"content":{"parts":[{"text":"hi"}]}}]}`},
		// Tool-bearing request bodies that hit the secondary decoders.
		{`{"messages":[{"role":"assistant","tool_calls":[{"id":"c","type":"function","function":{"name":"f","arguments":"{}"}}]}]}`, ""},
		{`{"messages":[{"role":"user","content":[{"type":"tool_result","tool_use_id":"t","content":"x"}]}]}`, ""},
		// Negative / malformed seeds.
		{"", ""},
		{"{}", "{}"},
		{"not json", "}{"},
		{`[1,2,3]`, `42`},
		{`{"messages":"oops"}`, `{"object":"unknown"}`},
		{`{"messages":[],"tools":7}`, ""},
	}
	for _, s := range seeds {
		f.Add([]byte(s.req), []byte(s.resp))
	}

	valid := map[string]bool{
		config.ProtocolChat:            true,
		config.ProtocolResponses:       true,
		config.ProtocolMessages:        true,
		config.ProtocolGenerateContent: true,
	}

	f.Fuzz(func(t *testing.T, req, resp []byte) {
		got, ok := backfill.ResolveProtocol(req, resp)
		if ok {
			if !valid[got] {
				t.Fatalf("ok=true but protocol %q is not a known protocol", got)
			}
		} else if got != "" {
			t.Fatalf("ok=false but protocol %q is non-empty", got)
		}
	})
}
