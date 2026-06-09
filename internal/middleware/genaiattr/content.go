package genaiattr

import (
	"encoding/json"
	"strings"
)

// Part is one element of a message's parts[] array in the OTel GenAI message
// shape. Type follows the semconv well-known values — "text", "tool_call",
// "tool_call_response" — with other provider block types (images, audio)
// passing through under their own type. Only the fields relevant to Type are
// populated; the rest stay zero.
type Part struct {
	// Type is the part discriminator: "text", "tool_call",
	// "tool_call_response", or a provider-specific block type passed through
	// verbatim (e.g. "image", "input_audio").
	Type string

	// Content is the text body of a "text" part. Empty for non-text parts —
	// inline media bytes are deliberately not captured (telemetry stays
	// bounded; the full blob rides the connector spool).
	Content string

	// ID correlates a "tool_call" with its later "tool_call_response".
	ID string

	// Name is the function name on a "tool_call" part.
	Name string

	// Arguments is the tool-call argument object as raw JSON on a "tool_call"
	// part, or nil when the model emitted no arguments.
	Arguments json.RawMessage

	// Result is the tool result text on a "tool_call_response" part.
	Result string
}

// Message is one role-tagged turn carrying an ordered parts[] list, matching
// the element shape of gen_ai.input.messages / gen_ai.output.messages.
type Message struct {
	// Role is the turn's author: "user", "assistant", "system", "tool".
	Role string

	// Parts is the ordered content of the turn.
	Parts []Part
}

// ToolDefinition is one entry of gen_ai.tool.definitions, normalised to the
// spec's flat {type,name,description,parameters} shape regardless of the
// provider's native tool encoding (OpenAI nests under "function", Anthropic
// uses "input_schema", Gemini wraps in "functionDeclarations").
type ToolDefinition struct {
	// Type is the tool kind; "function" for every tool we model today.
	Type string

	// Name is the function name.
	Name string

	// Description is the human-readable tool description, may be empty.
	Description string

	// Parameters is the tool's JSON-Schema parameter object as raw JSON, or
	// nil when the tool declared no parameters.
	Parameters json.RawMessage
}

// Content is the bounded prompt content extracted from a request body for the
// GenAI operation-details event and span: the system instructions, the latest
// user turn, and the tool definitions. Bounded by design — the full
// conversation history lives in the connector spool, not telemetry, so the
// 1M-token blob never reaches a span or log record (the spec explicitly
// permits truncating message content).
type Content struct {
	// SystemInstructions is the system prompt as an ordered parts list
	// (gen_ai.system_instructions) — every system/developer source on the
	// request, not just the first, with each block preserved as its own part.
	SystemInstructions []Part

	// InputMessage is the latest user turn only (gen_ai.input.messages,
	// bounded to one turn), carrying its real parts — text, pass-through
	// media blocks, tool results. Nil when the request had no user turn.
	InputMessage *Message

	// ToolDefinitions is the request's tool list normalised to the spec
	// shape, or nil when the request defined no tools.
	ToolDefinitions []ToolDefinition
}

// ExtractContent parses the request body for the bounded content of the named
// endpoint. The request body is always a plain JSON object (no SSE), so this
// is a single unmarshal. Unrecognised endpoints or unparseable bodies yield a
// zero Content.
func ExtractContent(endpoint string, requestRaw []byte) Content {
	if len(requestRaw) == 0 {
		return Content{}
	}
	switch endpoint {
	case "chat_completions", "chat":
		return openAIChatContent(requestRaw)
	case "responses":
		return openAIResponsesContent(requestRaw)
	case "messages":
		return anthropicContent(requestRaw)
	case "generate_content":
		return geminiContent(requestRaw)
	default:
		return Content{}
	}
}

func openAIChatContent(raw []byte) Content {
	var body struct {
		Messages []struct {
			Role       string          `json:"role"`
			Content    json.RawMessage `json:"content"`
			ToolCallID string          `json:"tool_call_id"`
		} `json:"messages"`
		Tools json.RawMessage `json:"tools"`
	}
	if json.Unmarshal(raw, &body) != nil {
		return Content{}
	}
	c := Content{ToolDefinitions: openAIToolDefs(body.Tools)}
	// The latest input turn is either the last user message or the contiguous
	// trailing run of role:"tool" messages (a tool-continuation request, where
	// the input turn the assistant is responding to is that run of tool
	// results). The accumulator keeps the last of whichever appeared most
	// recently — a user message resets any in-progress tool group, and a tool
	// message appends to the group, replacing a prior user turn.
	var toolTurn *Message
	for _, m := range body.Messages {
		switch m.Role {
		case "system", "developer":
			c.SystemInstructions = append(c.SystemInstructions, contentParts(m.Content)...)
		case "user":
			// Latest user turn wins; keep overwriting so the last user
			// message is what survives (bounded to one turn). A user turn ends
			// any trailing tool-message group seen so far.
			c.InputMessage = &Message{Role: "user", Parts: contentParts(m.Content)}
			toolTurn = nil
		case "tool":
			// role:"tool" carries one tool result ({tool_call_id, content}).
			// Accumulate the contiguous trailing run into a single tool turn;
			// the run wins over any earlier user turn (it is the latest input).
			if toolTurn == nil {
				toolTurn = &Message{Role: "tool"}
			}
			toolTurn.Parts = append(toolTurn.Parts, Part{Type: "tool_call_response", ID: m.ToolCallID, Result: textFromContent(m.Content)})
			c.InputMessage = toolTurn
		default:
			// assistant (or any other) message breaks the contiguous tool run
			// without becoming the input turn.
			toolTurn = nil
		}
	}
	return c
}

func openAIResponsesContent(raw []byte) Content {
	var body struct {
		Instructions string          `json:"instructions"`
		Input        json.RawMessage `json:"input"`
		Tools        json.RawMessage `json:"tools"`
	}
	if json.Unmarshal(raw, &body) != nil {
		return Content{}
	}
	c := Content{ToolDefinitions: openAIResponsesToolDefs(body.Tools)}
	if body.Instructions != "" {
		c.SystemInstructions = append(c.SystemInstructions, Part{Type: "text", Content: body.Instructions})
	}
	// The Responses `input` is either a plain string prompt or an array of
	// input items ({role, content}); system/developer items contribute to the
	// system instructions, the latest user item is the input turn.
	var s string
	if json.Unmarshal(body.Input, &s) == nil {
		if s != "" {
			c.InputMessage = &Message{Role: "user", Parts: []Part{{Type: "text", Content: s}}}
		}
		return c
	}
	var items []struct {
		Role    string          `json:"role"`
		Type    string          `json:"type"`
		CallID  string          `json:"call_id"`
		Output  json.RawMessage `json:"output"`
		Content json.RawMessage `json:"content"`
	}
	if json.Unmarshal(body.Input, &items) == nil {
		// As with chat, the latest input turn is either the last user item or
		// the contiguous trailing run of function_call_output items (a tool-
		// continuation request). function_call_output items carry no role —
		// they are identified by type — so they must be matched before the
		// role-keyed branches, which would otherwise clobber the input turn
		// with empty parts.
		var toolTurn *Message
		for _, it := range items {
			if it.Type == "function_call_output" {
				// {type:"function_call_output", call_id, output}.
				if toolTurn == nil {
					toolTurn = &Message{Role: "tool"}
				}
				toolTurn.Parts = append(toolTurn.Parts, Part{Type: "tool_call_response", ID: it.CallID, Result: textFromContent(it.Output)})
				c.InputMessage = toolTurn
				continue
			}
			switch it.Role {
			case "system", "developer":
				c.SystemInstructions = append(c.SystemInstructions, contentParts(it.Content)...)
				toolTurn = nil
			case "user", "":
				c.InputMessage = &Message{Role: "user", Parts: contentParts(it.Content)}
				toolTurn = nil
			default:
				// function_call items (the assistant's tool call) and any other
				// typed item break the contiguous tool-output run.
				toolTurn = nil
			}
		}
	}
	return c
}

func anthropicContent(raw []byte) Content {
	var body struct {
		System   json.RawMessage `json:"system"`
		Messages []struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
		Tools json.RawMessage `json:"tools"`
	}
	if json.Unmarshal(raw, &body) != nil {
		return Content{}
	}
	c := Content{
		SystemInstructions: contentParts(body.System),
		ToolDefinitions:    anthropicToolDefs(body.Tools),
	}
	for _, m := range body.Messages {
		if m.Role == "user" {
			c.InputMessage = &Message{Role: "user", Parts: contentParts(m.Content)}
		}
	}
	return c
}

func geminiContent(raw []byte) Content {
	var body struct {
		SystemInstruction *struct {
			Parts []geminiPart `json:"parts"`
		} `json:"systemInstruction"`
		Contents []struct {
			Role  string       `json:"role"`
			Parts []geminiPart `json:"parts"`
		} `json:"contents"`
		Tools json.RawMessage `json:"tools"`
	}
	if json.Unmarshal(raw, &body) != nil {
		return Content{}
	}
	c := Content{ToolDefinitions: geminiToolDefs(body.Tools)}
	if body.SystemInstruction != nil {
		c.SystemInstructions = geminiParts(body.SystemInstruction.Parts)
	}
	for _, m := range body.Contents {
		// Gemini uses "user" for the human turn and "model" for the model turn.
		// A tool-continuation request's input turn carries functionResponse
		// parts under role "user" (the SDK convention) or, on some clients,
		// the explicit "function" role — accept both so the tool result is
		// captured rather than dropped.
		if m.Role == "user" || m.Role == "" || m.Role == "function" {
			c.InputMessage = &Message{Role: "user", Parts: geminiParts(m.Parts)}
		}
	}
	return c
}

// geminiPart is one Gemini content part: text, or a functionCall/
// functionResponse for tool turns.
type geminiPart struct {
	Text         string `json:"text"`
	FunctionCall *struct {
		Name string          `json:"name"`
		Args json.RawMessage `json:"args"`
	} `json:"functionCall"`
	FunctionResponse *struct {
		Name     string          `json:"name"`
		Response json.RawMessage `json:"response"`
	} `json:"functionResponse"`
}

func geminiParts(in []geminiPart) []Part {
	var parts []Part
	for _, p := range in {
		switch {
		case p.FunctionCall != nil:
			parts = append(parts, Part{Type: "tool_call", Name: p.FunctionCall.Name, Arguments: nonEmptyRaw(p.FunctionCall.Args)})
		case p.FunctionResponse != nil:
			parts = append(parts, Part{Type: "tool_call_response", Name: p.FunctionResponse.Name, Result: string(p.FunctionResponse.Response)})
		case p.Text != "":
			parts = append(parts, Part{Type: "text", Content: p.Text})
		}
	}
	return parts
}

// contentParts converts a message "content" field — a plain string or an
// array of typed blocks — into bounded parts. Text blocks become "text"
// parts; Anthropic tool_use/tool_result map to tool_call/tool_call_response;
// thinking/redacted_thinking map to "reasoning" (redacted carries no text);
// other blocks (images, audio) pass through by type with no inline bytes
// (telemetry stays bounded — the full blob rides the spool).
func contentParts(raw json.RawMessage) []Part {
	if len(raw) == 0 {
		return nil
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		if s == "" {
			return nil
		}
		return []Part{{Type: "text", Content: s}}
	}
	var blocks []struct {
		Type      string          `json:"type"`
		Text      string          `json:"text"`
		ID        string          `json:"id"`
		Name      string          `json:"name"`
		Input     json.RawMessage `json:"input"`
		ToolUseID string          `json:"tool_use_id"`
		Content   json.RawMessage `json:"content"`
		Thinking  string          `json:"thinking"`
	}
	if json.Unmarshal(raw, &blocks) != nil {
		return nil
	}
	var parts []Part
	for _, b := range blocks {
		switch b.Type {
		case "text", "input_text", "output_text":
			if b.Text != "" {
				parts = append(parts, Part{Type: "text", Content: b.Text})
			}
		case "tool_use":
			parts = append(parts, Part{Type: "tool_call", ID: b.ID, Name: b.Name, Arguments: nonEmptyRaw(b.Input)})
		case "tool_result":
			parts = append(parts, Part{Type: "tool_call_response", ID: b.ToolUseID, Result: textFromContent(b.Content)})
		case "thinking":
			parts = append(parts, Part{Type: "reasoning", Content: b.Thinking})
		case "redacted_thinking":
			// Opaque encrypted trace — surface the part, never its bytes.
			parts = append(parts, Part{Type: "reasoning"})
		case "":
			// An untyped block with text is still text (lenient providers).
			if b.Text != "" {
				parts = append(parts, Part{Type: "text", Content: b.Text})
			}
		default:
			// Images/audio/other: record the type, never the inline bytes.
			parts = append(parts, Part{Type: b.Type})
		}
	}
	return parts
}

// openAIToolDefs normalises an OpenAI tools array
// ([{type:"function", function:{name,description,parameters}}]) to the spec's
// flat shape.
func openAIToolDefs(raw json.RawMessage) []ToolDefinition {
	if len(nonEmptyRaw(raw)) == 0 {
		return nil
	}
	var tools []struct {
		Type     string `json:"type"`
		Function struct {
			Name        string          `json:"name"`
			Description string          `json:"description"`
			Parameters  json.RawMessage `json:"parameters"`
		} `json:"function"`
	}
	if json.Unmarshal(raw, &tools) != nil {
		return nil
	}
	var out []ToolDefinition
	for _, t := range tools {
		typ := t.Type
		if typ == "" {
			typ = "function"
		}
		out = append(out, ToolDefinition{
			Type:        typ,
			Name:        t.Function.Name,
			Description: t.Function.Description,
			Parameters:  nonEmptyRaw(t.Function.Parameters),
		})
	}
	return out
}

// openAIResponsesToolDefs normalises a Responses API tools array to the spec's
// flat shape. Unlike Chat Completions, Responses encodes function tools flat —
// {type:"function", name, description, parameters} — not nested under a
// "function" object, and mixes in custom tools ({type:"custom", name,
// description}) and built-in tools ({type:"web_search"}, {type:"image_generation"},
// {type:"tool_search"}, …) that carry only a type. Reusing openAIToolDefs here
// drops every name/description/parameters because they aren't under "function"
// (Codex tool catalogues collapsed to bare {"type":"function"} entries).
func openAIResponsesToolDefs(raw json.RawMessage) []ToolDefinition {
	if len(nonEmptyRaw(raw)) == 0 {
		return nil
	}
	var tools []struct {
		Type        string          `json:"type"`
		Name        string          `json:"name"`
		Description string          `json:"description"`
		Parameters  json.RawMessage `json:"parameters"`
	}
	if json.Unmarshal(raw, &tools) != nil {
		return nil
	}
	var out []ToolDefinition
	for _, t := range tools {
		typ := t.Type
		if typ == "" {
			typ = "function"
		}
		// Built-in tools (web_search, image_generation, …) carry only a type;
		// name/description/parameters stay empty and the entry records the type.
		out = append(out, ToolDefinition{
			Type:        typ,
			Name:        t.Name,
			Description: t.Description,
			Parameters:  nonEmptyRaw(t.Parameters),
		})
	}
	return out
}

// anthropicToolDefs normalises an Anthropic tools array
// ([{name,description,input_schema}]) to the spec's flat shape.
func anthropicToolDefs(raw json.RawMessage) []ToolDefinition {
	if len(nonEmptyRaw(raw)) == 0 {
		return nil
	}
	var tools []struct {
		Name        string          `json:"name"`
		Description string          `json:"description"`
		InputSchema json.RawMessage `json:"input_schema"`
	}
	if json.Unmarshal(raw, &tools) != nil {
		return nil
	}
	var out []ToolDefinition
	for _, t := range tools {
		out = append(out, ToolDefinition{
			Type:        "function",
			Name:        t.Name,
			Description: t.Description,
			Parameters:  nonEmptyRaw(t.InputSchema),
		})
	}
	return out
}

// geminiToolDefs normalises a Gemini tools array — each entry wraps a
// functionDeclarations[] list ([{functionDeclarations:[{name,description,
// parameters}]}]) — flattening every declaration to one spec tool.
func geminiToolDefs(raw json.RawMessage) []ToolDefinition {
	if len(nonEmptyRaw(raw)) == 0 {
		return nil
	}
	var tools []struct {
		FunctionDeclarations []struct {
			Name        string          `json:"name"`
			Description string          `json:"description"`
			Parameters  json.RawMessage `json:"parameters"`
		} `json:"functionDeclarations"`
	}
	if json.Unmarshal(raw, &tools) != nil {
		return nil
	}
	var out []ToolDefinition
	for _, t := range tools {
		for _, d := range t.FunctionDeclarations {
			out = append(out, ToolDefinition{
				Type:        "function",
				Name:        d.Name,
				Description: d.Description,
				Parameters:  nonEmptyRaw(d.Parameters),
			})
		}
	}
	return out
}

// textFromContent flattens a content field that may be a plain string or an
// array of content parts ({text} / {content}) into concatenated text. Used
// for tool-result bodies, which are text in telemetry.
func textFromContent(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var parts []struct {
		Text    string `json:"text"`
		Content string `json:"content"`
	}
	if json.Unmarshal(raw, &parts) == nil {
		var b strings.Builder
		for _, p := range parts {
			switch {
			case p.Text != "":
				b.WriteString(p.Text)
			case p.Content != "":
				b.WriteString(p.Content)
			}
		}
		return b.String()
	}
	return ""
}

func nonEmptyRaw(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	return raw
}
