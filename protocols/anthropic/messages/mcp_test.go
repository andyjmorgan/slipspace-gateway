package messages

import (
	"encoding/json"
	"reflect"
	"testing"
)

// The MCP connector beta (anthropic-beta: mcp-client-2025-11-20) adds
// request-side mcp_servers + mcp_toolset tools and response-side
// mcp_tool_use / mcp_tool_result content blocks. Shapes below follow the
// official doc examples:
// https://platform.claude.com/docs/en/agents-and-tools/mcp-connector
// and the anthropic-sdk-python beta types (GitHub #557).

// TestMessagesRequest_McpServersRoundTrip covers the current-beta request:
// an mcp_servers entry and the mcp_toolset that binds to it decode typed with
// nothing left in Extra at any level, and the explicit false values on the
// tool configs survive the round-trip (they are the denylist primitive).
func TestMessagesRequest_McpServersRoundTrip(t *testing.T) {
	in := []byte(`{"max_tokens":1000,"mcp_servers":[{"authorization_token":"tok-example-not-a-real-credential","name":"example-mcp","type":"url","url":"https://example-server.modelcontextprotocol.io/sse"}],"messages":[{"content":"What tools do you have available?","role":"user"}],"model":"claude-sonnet-4-5","tools":[{"configs":{"specific_tool_name":{"defer_loading":true,"enabled":false}},"default_config":{"defer_loading":false,"enabled":true},"mcp_server_name":"example-mcp","type":"mcp_toolset"}]}`)
	var req MessagesRequest
	if err := json.Unmarshal(in, &req); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(req.Extra) != 0 {
		t.Fatalf("mcp_servers leaked into Extra: %v", req.Extra)
	}
	if len(req.McpServers) != 1 {
		t.Fatalf("mcp_servers = %+v", req.McpServers)
	}
	srv := req.McpServers[0]
	if srv.Type != "url" || srv.Name != "example-mcp" ||
		srv.URL != "https://example-server.modelcontextprotocol.io/sse" ||
		srv.AuthorizationToken != "tok-example-not-a-real-credential" {
		t.Fatalf("mcp_servers[0] = %+v", srv)
	}
	if srv.ToolConfiguration != nil || len(srv.Extra) != 0 {
		t.Fatalf("mcp_servers[0] unexpected tool_configuration/extra: %+v / %v", srv.ToolConfiguration, srv.Extra)
	}

	if len(req.Tools) != 1 {
		t.Fatalf("tools = %+v", req.Tools)
	}
	tool := req.Tools[0]
	if tool.Type != "mcp_toolset" || tool.MCPServerName != "example-mcp" || tool.Name != "" {
		t.Fatalf("tools[0] = %+v", tool)
	}
	if len(tool.Extra) != 0 {
		t.Fatalf("mcp_toolset fields leaked into Extra: %v", tool.Extra)
	}
	if tool.DefaultConfig == nil ||
		tool.DefaultConfig.Enabled == nil || !*tool.DefaultConfig.Enabled ||
		tool.DefaultConfig.DeferLoading == nil || *tool.DefaultConfig.DeferLoading {
		t.Fatalf("default_config = %+v", tool.DefaultConfig)
	}
	if len(tool.DefaultConfig.Extra) != 0 {
		t.Fatalf("default_config leaked: %v", tool.DefaultConfig.Extra)
	}
	cfg, ok := tool.Configs["specific_tool_name"]
	if !ok || len(tool.Configs) != 1 {
		t.Fatalf("configs = %+v", tool.Configs)
	}
	// Explicit false must survive — it is what disables the tool.
	if cfg.Enabled == nil || *cfg.Enabled || cfg.DeferLoading == nil || !*cfg.DeferLoading {
		t.Fatalf("configs.specific_tool_name = %+v", cfg)
	}

	out, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !jsonValueEqual(t, in, out) {
		t.Fatalf("round-trip drift\n in: %s\nout: %s", in, out)
	}
}

// TestMcpServerDefinition_DeprecatedToolConfigurationRoundTrip keeps the
// superseded mcp-client-2025-04-04 shape (tool_configuration nested on the
// server entry) typed and lossless, including an explicit enabled:false.
func TestMcpServerDefinition_DeprecatedToolConfigurationRoundTrip(t *testing.T) {
	in := []byte(`{"name":"example-mcp","tool_configuration":{"allowed_tools":["echo","ping"],"enabled":false},"type":"url","url":"https://example-server.modelcontextprotocol.io/sse"}`)
	var srv McpServerDefinition
	if err := json.Unmarshal(in, &srv); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if srv.ToolConfiguration == nil {
		t.Fatalf("tool_configuration not typed: extra=%v", srv.Extra)
	}
	tc := srv.ToolConfiguration
	if tc.Enabled == nil || *tc.Enabled {
		t.Fatalf("tool_configuration.enabled = %v, want explicit false", tc.Enabled)
	}
	if !reflect.DeepEqual(tc.AllowedTools, []string{"echo", "ping"}) {
		t.Fatalf("allowed_tools = %v", tc.AllowedTools)
	}
	if len(tc.Extra) != 0 || len(srv.Extra) != 0 {
		t.Fatalf("unmapped fields leaked: %v / %v", tc.Extra, srv.Extra)
	}
	if srv.AuthorizationToken != "" {
		t.Fatalf("authorization_token = %q, want empty", srv.AuthorizationToken)
	}
	out, err := json.Marshal(srv)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !jsonValueEqual(t, in, out) {
		t.Fatalf("round-trip drift\n in: %s\nout: %s", in, out)
	}
}

// TestMcpRequestTypes_UnknownSiblingsRoundTrip proves every new request-side
// struct keeps the invariant-#1 safety net: a field Anthropic ships tomorrow
// lands in Extra and comes back out, at each nesting level.
func TestMcpRequestTypes_UnknownSiblingsRoundTrip(t *testing.T) {
	in := []byte(`{"max_tokens":1,"mcp_servers":[{"future_server_field":{"x":1},"name":"s","tool_configuration":{"enabled":true,"future_tc":"y"},"type":"url","url":"https://s.example"}],"messages":[{"content":"hi","role":"user"}],"model":"m","tools":[{"configs":{"t":{"enabled":true,"future_cfg":2}},"default_config":{"enabled":false,"future_default":true},"mcp_server_name":"s","type":"mcp_toolset"}]}`)
	var req MessagesRequest
	if err := json.Unmarshal(in, &req); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	srv := req.McpServers[0]
	if _, ok := srv.Extra["future_server_field"]; !ok {
		t.Fatalf("server extra missing: %v", srv.Extra)
	}
	if _, ok := srv.ToolConfiguration.Extra["future_tc"]; !ok {
		t.Fatalf("tool_configuration extra missing: %v", srv.ToolConfiguration.Extra)
	}
	tool := req.Tools[0]
	if _, ok := tool.DefaultConfig.Extra["future_default"]; !ok {
		t.Fatalf("default_config extra missing: %v", tool.DefaultConfig.Extra)
	}
	if _, ok := tool.Configs["t"].Extra["future_cfg"]; !ok {
		t.Fatalf("configs.t extra missing: %v", tool.Configs["t"].Extra)
	}
	out, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !jsonValueEqual(t, in, out) {
		t.Fatalf("round-trip drift\n in: %s\nout: %s", in, out)
	}
}

// TestTool_McpToolsetOmitsEmptyName pins the omitempty on Tool.Name: an
// mcp_toolset entry has no name on the wire, so re-marshalling must not
// synthesise "name":"" (which Anthropic would reject as an unknown tool).
func TestTool_McpToolsetOmitsEmptyName(t *testing.T) {
	var tool Tool
	if err := json.Unmarshal([]byte(`{"mcp_server_name":"s","type":"mcp_toolset"}`), &tool); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	out, err := json.Marshal(tool)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatalf("remarshalled not an object: %v", err)
	}
	if _, present := m["name"]; present {
		t.Fatalf("empty name must be omitted, got %s", out)
	}
	// A regular tool still carries its name.
	named, _ := json.Marshal(Tool{Name: "get_weather"})
	if string(named) != `{"name":"get_weather"}` {
		t.Fatalf("named tool = %s", named)
	}
}

// TestUnmarshalContentBlock_McpToolUse covers the response block for a
// server-side MCP call; shape from the official doc example.
func TestUnmarshalContentBlock_McpToolUse(t *testing.T) {
	in := []byte(`{"id":"mcptoolu_014Q35RayjACSWkSj4X2yov1","input":{"param1":"value1","param2":"value2"},"name":"echo","server_name":"example-mcp","type":"mcp_tool_use"}`)
	v, err := UnmarshalContentBlock(in)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	b, ok := v.(*McpToolUseBlock)
	if !ok {
		t.Fatalf("got %T", v)
	}
	if b.ID != "mcptoolu_014Q35RayjACSWkSj4X2yov1" || b.Name != "echo" || b.ServerName != "example-mcp" {
		t.Fatalf("mcp_tool_use = %+v", b)
	}
	if string(b.Input) != `{"param1":"value1","param2":"value2"}` {
		t.Fatalf("input = %s", b.Input)
	}
	if b.BlockType() != "mcp_tool_use" {
		t.Fatalf("block type = %q", b.BlockType())
	}
	if len(b.Extra) != 0 {
		t.Fatalf("unmapped fields leaked: %v", b.Extra)
	}
	roundTrip(t, in, v)
}

// TestUnmarshalContentBlock_McpToolResult covers both documented content
// shapes (text-block array and bare string) and proves is_error:false is
// preserved rather than collapsing to absent.
func TestUnmarshalContentBlock_McpToolResult(t *testing.T) {
	cases := []struct {
		name    string
		in      []byte
		content string
		isError bool
	}{
		{
			name:    "array content",
			in:      []byte(`{"content":[{"text":"Hello","type":"text"}],"is_error":false,"tool_use_id":"mcptoolu_014Q35RayjACSWkSj4X2yov1","type":"mcp_tool_result"}`),
			content: `[{"text":"Hello","type":"text"}]`,
		},
		{
			name:    "string content error",
			in:      []byte(`{"content":"upstream timed out","is_error":true,"tool_use_id":"mcptoolu_2","type":"mcp_tool_result"}`),
			content: `"upstream timed out"`,
			isError: true,
		},
		{
			name:    "request echo with cache_control",
			in:      []byte(`{"cache_control":{"type":"ephemeral"},"content":[{"text":"Hello","type":"text"}],"is_error":false,"tool_use_id":"mcptoolu_3","type":"mcp_tool_result"}`),
			content: `[{"text":"Hello","type":"text"}]`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v, err := UnmarshalContentBlock(tc.in)
			if err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			b, ok := v.(*McpToolResultBlock)
			if !ok {
				t.Fatalf("got %T", v)
			}
			if b.BlockType() != "mcp_tool_result" {
				t.Fatalf("block type = %q", b.BlockType())
			}
			if b.IsError == nil || *b.IsError != tc.isError {
				t.Fatalf("is_error = %v, want explicit %v", b.IsError, tc.isError)
			}
			if string(b.Content) != tc.content {
				t.Fatalf("content = %s", b.Content)
			}
			if len(b.Extra) != 0 {
				t.Fatalf("unmapped fields leaked: %v", b.Extra)
			}
			roundTrip(t, tc.in, v)
		})
	}
}

// TestUnmarshalContentBlock_McpBlocksUnknownSiblingsRoundTrip keeps the
// forward-compat net on both new block kinds.
func TestUnmarshalContentBlock_McpBlocksUnknownSiblingsRoundTrip(t *testing.T) {
	for _, in := range [][]byte{
		[]byte(`{"future":{"a":1},"id":"mcptoolu_1","input":{},"name":"echo","server_name":"s","type":"mcp_tool_use"}`),
		[]byte(`{"content":"x","future":true,"is_error":false,"tool_use_id":"mcptoolu_1","type":"mcp_tool_result"}`),
	} {
		v, err := UnmarshalContentBlock(in)
		if err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if _, isUnknown := v.(*UnknownBlock); isUnknown {
			t.Fatalf("%s decoded as UnknownBlock; expected a typed MCP block", in)
		}
		roundTrip(t, in, v)
	}
}

// TestUnmarshalStreamEvent_McpToolUseStart proves an mcp_tool_use header on
// content_block_start dispatches to the typed block — the shape the SSE
// rollup relies on to splice input_json_delta fragments back in.
func TestUnmarshalStreamEvent_McpToolUseStart(t *testing.T) {
	ev, err := UnmarshalStreamEvent([]byte(`{"type":"content_block_start","index":1,"content_block":{"type":"mcp_tool_use","id":"mcptoolu_1","name":"echo","server_name":"example-mcp","input":{}}}`))
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	start, ok := ev.(*ContentBlockStartEvent)
	if !ok {
		t.Fatalf("got %T", ev)
	}
	b, ok := start.ContentBlock.(*McpToolUseBlock)
	if !ok {
		t.Fatalf("content_block = %T, want *McpToolUseBlock", start.ContentBlock)
	}
	if b.ServerName != "example-mcp" || b.Name != "echo" {
		t.Fatalf("block = %+v", b)
	}
}

// TestMcp_AllExportedFieldsHaveJSONTag extends the package meta-test to the
// MCP connector types.
func TestMcp_AllExportedFieldsHaveJSONTag(t *testing.T) {
	types := []reflect.Type{
		reflect.TypeOf(McpServerDefinition{}),
		reflect.TypeOf(McpServerToolConfiguration{}),
		reflect.TypeOf(McpToolConfig{}),
		reflect.TypeOf(McpToolUseBlock{}),
		reflect.TypeOf(McpToolResultBlock{}),
	}
	for _, rt := range types {
		t.Run(rt.Name(), func(t *testing.T) {
			for i := 0; i < rt.NumField(); i++ {
				sf := rt.Field(i)
				if sf.Anonymous || !sf.IsExported() {
					continue
				}
				if _, ok := sf.Tag.Lookup("json"); !ok {
					t.Errorf("%s.%s missing json tag", rt.Name(), sf.Name)
				}
			}
		})
	}
}
