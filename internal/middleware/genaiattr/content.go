package genaiattr

import (
	"encoding/json"
	"strings"
)

// Content is the bounded prompt content extracted from a request body for
// the GenAI operation-details event: the system instructions, the latest
// user turn, and the tool definitions. Bounded by design — the full
// conversation history lives in the connector spool, not telemetry, so the
// 1M-token blob never reaches a span or log record (the spec explicitly
// permits truncating message content).
type Content struct {
	// SystemInstructions is the system prompt text (gen_ai.system_instructions).
	SystemInstructions string

	// LatestUserText is the text of the most recent user message only
	// (gen_ai.input.messages, truncated to the latest turn).
	LatestUserText string

	// ToolDefinitions is the request's tool/function list verbatim
	// (gen_ai.tool.definitions), or nil when the request defined no tools.
	ToolDefinitions json.RawMessage
}

// ExtractContent parses the request body for the bounded content of the
// named endpoint. The request body is always a plain JSON object (no SSE),
// so this is a single unmarshal. Unrecognised endpoints or unparseable
// bodies yield a zero Content.
func ExtractContent(endpoint string, requestRaw []byte) Content {
	if len(requestRaw) == 0 {
		return Content{}
	}
	switch endpoint {
	case "chat_completions":
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

func openAIResponsesContent(raw []byte) Content {
	var body struct {
		Instructions string          `json:"instructions"`
		Input        json.RawMessage `json:"input"`
		Tools        json.RawMessage `json:"tools"`
	}
	if json.Unmarshal(raw, &body) != nil {
		return Content{}
	}
	return Content{
		SystemInstructions: body.Instructions,
		LatestUserText:     responsesInputText(body.Input),
		ToolDefinitions:    nonEmptyRaw(body.Tools),
	}
}

// responsesInputText flattens the Responses API `input` field, which is
// either a plain string prompt or an array of input items
// ({role, content:[{type:"input_text", text}]}). The latest user item wins.
func responsesInputText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var items []struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	}
	if json.Unmarshal(raw, &items) == nil {
		var latest string
		for _, it := range items {
			if it.Role == "user" || it.Role == "" {
				latest = textFromContent(it.Content)
			}
		}
		return latest
	}
	return ""
}

func openAIChatContent(raw []byte) Content {
	var body struct {
		Messages []struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
		Tools json.RawMessage `json:"tools"`
	}
	if json.Unmarshal(raw, &body) != nil {
		return Content{}
	}
	c := Content{ToolDefinitions: nonEmptyRaw(body.Tools)}
	for _, m := range body.Messages {
		switch m.Role {
		case "system", "developer":
			if c.SystemInstructions == "" {
				c.SystemInstructions = textFromContent(m.Content)
			}
		case "user":
			// Latest user turn wins; keep overwriting so the last user
			// message is what survives.
			c.LatestUserText = textFromContent(m.Content)
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
		SystemInstructions: textFromContent(body.System),
		ToolDefinitions:    nonEmptyRaw(body.Tools),
	}
	for _, m := range body.Messages {
		if m.Role == "user" {
			c.LatestUserText = textFromContent(m.Content)
		}
	}
	return c
}

func geminiContent(raw []byte) Content {
	var body struct {
		SystemInstruction *struct {
			Parts []struct {
				Text string `json:"text"`
			} `json:"parts"`
		} `json:"systemInstruction"`
		Contents []struct {
			Role  string `json:"role"`
			Parts []struct {
				Text string `json:"text"`
			} `json:"parts"`
		} `json:"contents"`
		Tools json.RawMessage `json:"tools"`
	}
	if json.Unmarshal(raw, &body) != nil {
		return Content{}
	}
	c := Content{ToolDefinitions: nonEmptyRaw(body.Tools)}
	if body.SystemInstruction != nil {
		var b strings.Builder
		for _, p := range body.SystemInstruction.Parts {
			b.WriteString(p.Text)
		}
		c.SystemInstructions = b.String()
	}
	for _, m := range body.Contents {
		// Gemini uses "user" for the human turn; the model turn is "model".
		if m.Role == "user" || m.Role == "" {
			var b strings.Builder
			for _, p := range m.Parts {
				b.WriteString(p.Text)
			}
			c.LatestUserText = b.String()
		}
	}
	return c
}

// textFromContent flattens a message content field that may be a plain
// string or an array of content parts ({type,text} / {type,content}) into
// the concatenated text. Non-text parts (images, tool calls) are skipped —
// telemetry content is text-only and bounded.
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
