package contentredact_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/andyjmorgan/slipspace-gateway/internal/contentredact"
)

// TestRedactBody_MasksMcpAuthorizationToken proves the Anthropic MCP
// connector's mcp_servers[].authorization_token is masked on every element
// that carries it, while siblings and the rest of the body are untouched.
func TestRedactBody_MasksMcpAuthorizationToken(t *testing.T) {
	t.Parallel()
	const secret = "ya29.a0AfH6SMBx-very-secret-oauth-token" //nolint:gosec // G101: fixture token in test
	in := []byte(`{"model":"claude-sonnet-4-5","max_tokens":10,"mcp_servers":[` +
		`{"type":"url","url":"https://a.example/sse","name":"a","authorization_token":"` + secret + `"},` +
		`{"type":"url","url":"https://b.example/sse","name":"b"},` +
		`{"type":"url","url":"https://c.example/sse","name":"c","authorization_token":"` + secret + `-c"}],` +
		`"messages":[{"role":"user","content":"token in prose ` + secret + ` must be left alone here"}]}`)

	got := contentredact.RedactBody(in)

	var body struct {
		Model      string `json:"model"`
		MaxTokens  int    `json:"max_tokens"`
		McpServers []struct {
			Type  string  `json:"type"`
			URL   string  `json:"url"`
			Name  string  `json:"name"`
			Token *string `json:"authorization_token"`
		} `json:"mcp_servers"`
		Messages []struct {
			Content string `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(got, &body); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, got)
	}
	if body.Model != "claude-sonnet-4-5" || body.MaxTokens != 10 || len(body.McpServers) != 3 {
		t.Fatalf("unrelated fields disturbed: %s", got)
	}
	if body.McpServers[0].Token == nil || *body.McpServers[0].Token != "[REDACTED]" {
		t.Errorf("mcp_servers[0].authorization_token = %v, want masked", body.McpServers[0].Token)
	}
	if body.McpServers[1].Token != nil {
		t.Errorf("mcp_servers[1] grew an authorization_token: %v", *body.McpServers[1].Token)
	}
	if body.McpServers[2].Token == nil || *body.McpServers[2].Token != "[REDACTED]" {
		t.Errorf("mcp_servers[2].authorization_token = %v, want masked", body.McpServers[2].Token)
	}
	if body.McpServers[0].URL != "https://a.example/sse" || body.McpServers[0].Name != "a" {
		t.Errorf("siblings disturbed: %+v", body.McpServers[0])
	}
	// RedactBody is key-driven: prose is the free-text Redact's job, so the
	// token inside message content is deliberately left for that layer.
	if !strings.Contains(body.Messages[0].Content, secret) {
		t.Errorf("RedactBody must not rewrite message content; got %q", body.Messages[0].Content)
	}
	if strings.Count(string(got), secret) != 1 {
		t.Errorf("secret survives %d times outside prose, want only the prose occurrence:\n%s", strings.Count(string(got), secret)-1, got)
	}
}

// TestRedactBody_UntouchedWhenAbsent proves bodies without a credential
// field come back byte-identical — and on the same backing slice, so the hot
// path pays only a substring scan.
func TestRedactBody_UntouchedWhenAbsent(t *testing.T) {
	t.Parallel()
	cases := map[string][]byte{
		"plain chat":             []byte(`{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`),
		"mcp_servers no token":   []byte(`{"mcp_servers":[{"type":"url","url":"https://a.example","name":"a"}]}`),
		"key elsewhere in prose": []byte(`{"messages":[{"role":"user","content":"set \"authorization_token\" in the config"}]}`),
		"mcp_servers not array":  []byte(`{"mcp_servers":{"authorization_token":"x"}}`),
		"empty":                  nil,
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got := contentredact.RedactBody(in)
			if string(got) != string(in) {
				t.Fatalf("body changed:\n in: %s\nout: %s", in, got)
			}
			if len(in) > 0 && &got[0] != &in[0] {
				t.Errorf("expected the input slice to be returned as-is")
			}
		})
	}
}

// TestRedactBody_TruncatedBodyStillMasked proves a damaged body (e.g. cut at
// the capture cap) does not become a leak: the token is still masked
// best-effort and the call neither panics nor returns an error.
func TestRedactBody_TruncatedBodyStillMasked(t *testing.T) {
	t.Parallel()
	got := string(contentredact.RedactBody([]byte(`{"mcp_servers":[{"name":"a","authorization_token":"tok-truncated-0123"`)))
	if strings.Contains(got, "tok-truncated-0123") {
		t.Errorf("token leaked from truncated body: %s", got)
	}
}

// TestRedactBody_MasksNonStringValues proves masking is by key, not value
// shape: a token sent as a non-string (or empty) is still replaced.
func TestRedactBody_MasksNonStringValues(t *testing.T) {
	t.Parallel()
	in := []byte(`{"mcp_servers":[{"name":"a","authorization_token":12345},{"name":"b","authorization_token":""}]}`)
	got := string(contentredact.RedactBody(in))
	if strings.Contains(got, "12345") {
		t.Errorf("numeric token survived: %s", got)
	}
	if strings.Count(got, "[REDACTED]") != 2 {
		t.Errorf("want both tokens masked, got %s", got)
	}
}
