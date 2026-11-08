//go:build e2e

package reporting_test

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/andyjmorgan/slipspace-gateway/test/e2e/harness"
)

// TestReporting_McpAuthorizationTokenMaskedInRecord proves, through the real
// binary, that a credential embedded in an Anthropic Messages request body —
// the MCP connector's mcp_servers[].authorization_token (GitHub #557) — is
// masked on the connector Record exactly like a credential header, while the
// upstream provider still receives the client's bytes verbatim (the token is
// what the API needs to reach the MCP server).
func TestReporting_McpAuthorizationTokenMaskedInRecord(t *testing.T) {
	t.Parallel()
	h := harness.New(t)

	h.StageMockResponse(harness.CannedResponse{
		Method:  http.MethodPost,
		Path:    "/v1/messages",
		Status:  http.StatusOK,
		Headers: map[string]string{"Content-Type": "application/json"},
		Body:    `{"id":"msg_mcp","type":"message","role":"assistant","model":"claude-sonnet-4-5","content":[{"type":"mcp_tool_use","id":"mcptoolu_1","name":"echo","server_name":"example-mcp","input":{"param1":"value1"}},{"type":"mcp_tool_result","tool_use_id":"mcptoolu_1","is_error":false,"content":[{"type":"text","text":"Hello"}]},{"type":"text","text":"done"}],"stop_reason":"end_turn","usage":{"input_tokens":5,"output_tokens":3}}`,
	})

	const secret = "ya29.e2e-mcp-oauth-token-must-not-leak"
	resp := h.PostJSON("/v1/messages",
		map[string]any{
			"model":      "claude-sonnet-4-5",
			"max_tokens": 64,
			"mcp_servers": []map[string]any{{
				"type":                "url",
				"url":                 "https://example-server.modelcontextprotocol.io/sse",
				"name":                "example-mcp",
				"authorization_token": secret,
			}},
			"tools": []map[string]any{{
				"type":            "mcp_toolset",
				"mcp_server_name": "example-mcp",
				"default_config":  map[string]any{"enabled": true, "defer_loading": false},
				"configs":         map[string]any{"echo": map[string]any{"enabled": false}},
			}},
			"messages": []map[string]any{{"role": "user", "content": "What tools do you have?"}},
		},
		http.Header{
			"Anthropic-Beta":             []string{"mcp-client-2025-11-20"},
			"X-Slipspace-Correlation-Id": []string{"mcp-token-mask"},
		})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("gateway status=%d body=%s", resp.StatusCode, resp.Body)
	}
	// The typed response blocks round-trip to the client intact.
	if !strings.Contains(string(resp.Body), `"server_name":"example-mcp"`) || !strings.Contains(string(resp.Body), `"is_error":false`) {
		t.Errorf("mcp_tool_use / mcp_tool_result not forwarded intact: %s", resp.Body)
	}

	// Upstream leg: the provider saw the real token (and the toolset config).
	up := h.LastCapturedRequest()
	if up == nil {
		t.Fatal("mock LLM captured no upstream request")
	}
	if !strings.Contains(up.Body, `"authorization_token":"`+secret+`"`) {
		t.Errorf("upstream body does not carry the client's token verbatim: %s", up.Body)
	}
	if !strings.Contains(up.Body, `"mcp_server_name":"example-mcp"`) || !strings.Contains(up.Body, `"enabled":false`) {
		t.Errorf("upstream body lost mcp_toolset configuration: %s", up.Body)
	}

	// Operator leg: the connector Record masks it.
	env := h.ExpectEvent("gateway.request", 5*time.Second)
	if env.Record == nil {
		t.Fatal("request envelope carried no raw Record")
	}
	body := string(env.Record.Request.Body)
	if strings.Contains(body, secret) {
		t.Errorf("Record.Request.Body leaks the MCP authorization_token: %s", body)
	}
	if !strings.Contains(body, `"authorization_token":"[REDACTED]"`) {
		t.Errorf("Record.Request.Body not masked: %s", body)
	}
	if !strings.Contains(body, `"mcp_server_name":"example-mcp"`) {
		t.Errorf("Record.Request.Body lost non-credential MCP fields: %s", body)
	}
}
