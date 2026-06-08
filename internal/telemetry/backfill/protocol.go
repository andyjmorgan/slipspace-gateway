// Package backfill infers gateway metadata that historical telemetry records
// predate. Schema-version-2 S3 artifacts (~93% of the corpus) carry no
// protocol field and an empty request.path, so the protocol must be recovered
// from the captured request/response body JSON shape.
package backfill

import (
	"encoding/json"

	"github.com/andyjmorgan/sluice-gateway/contracts/config"
)

// ResolveProtocol infers the gateway protocol from a captured record's request
// and/or response body bytes. It returns one of config.ProtocolChat,
// config.ProtocolResponses, config.ProtocolMessages, or
// config.ProtocolGenerateContent, with ok=true on a confident match. It
// returns ("", false) when the shape matches none.
//
// The response body is the cleaner signal — providers stamp an unambiguous
// discriminator on responses ("object":"chat.completion", "object":"response",
// "type":"message", a "candidates" array) — so it is consulted first. When no
// response body is present (or it is inconclusive) the request body is used,
// where anthropic-messages and openai-chat both carry a "messages" array and
// must be disambiguated by secondary features.
func ResolveProtocol(requestBody, responseBody []byte) (protocol string, ok bool) {
	if p, matched := fromResponse(responseBody); matched {
		return p, true
	}
	return fromRequest(requestBody)
}

// responseShape is the union of response-body discriminators across the four
// protocols. A field absent from the body unmarshals to its zero value, so a
// single decode classifies every protocol.
type responseShape struct {
	// Object is OpenAI's response-type tag: "chat.completion" /
	// "chat.completion.chunk" for chat, "response" for the Responses API.
	Object string `json:"object"`
	// Type is Anthropic's top-level discriminator ("message" on a Messages
	// response) and also a stream-event tag.
	Type string `json:"type"`
	// Choices is present (and an array) on every OpenAI chat completion.
	Choices json.RawMessage `json:"choices"`
	// Output is the OpenAI Responses output item array.
	Output json.RawMessage `json:"output"`
	// Candidates is the Gemini generateContent response array.
	Candidates json.RawMessage `json:"candidates"`
}

// fromResponse classifies by response-body discriminator. It returns ok=false
// for empty, malformed, or non-object bodies, and for object bodies that carry
// no recognized response discriminator (so the caller falls back to the
// request body).
func fromResponse(body []byte) (string, bool) {
	if !looksLikeJSONObject(body) {
		return "", false
	}
	var s responseShape
	if err := json.Unmarshal(body, &s); err != nil {
		return "", false
	}

	switch s.Object {
	case "chat.completion", "chat.completion.chunk":
		return config.ProtocolChat, true
	case "response":
		return config.ProtocolResponses, true
	}
	// Anthropic stamps type:"message" on the Messages response envelope.
	if s.Type == "message" {
		return config.ProtocolMessages, true
	}
	// Structural fallbacks for bodies that omit the object/type tag (e.g. an
	// error-stripped or partial capture): a "candidates" array is unique to
	// Gemini; "choices" to OpenAI chat; "output" to OpenAI Responses.
	switch {
	case len(s.Candidates) > 0:
		return config.ProtocolGenerateContent, true
	case len(s.Choices) > 0:
		return config.ProtocolChat, true
	case len(s.Output) > 0:
		return config.ProtocolResponses, true
	}
	return "", false
}

// requestShape is the union of request-body discriminators across the four
// protocols.
type requestShape struct {
	// Contents is the Gemini generateContent input array.
	Contents json.RawMessage `json:"contents"`
	// GenerationConfig / SystemInstruction are Gemini-only request fields,
	// used as a fallback when contents is absent.
	GenerationConfig  json.RawMessage `json:"generationConfig"`
	SystemInstruction json.RawMessage `json:"systemInstruction"`
	// Input / Instructions are the OpenAI Responses request fields.
	Input        json.RawMessage `json:"input"`
	Instructions json.RawMessage `json:"instructions"`
	// Messages is shared by anthropic-messages and openai-chat.
	Messages json.RawMessage `json:"messages"`
	// MaxTokens is required on anthropic-messages requests; absent (or named
	// max_completion_tokens) on openai-chat.
	MaxTokens json.RawMessage `json:"max_tokens"`
	// System is the anthropic-messages top-level system prompt.
	System json.RawMessage `json:"system"`
	// Tools is shared; its element shape disambiguates (anthropic tools carry
	// input_schema; openai-chat tools wrap under {"type":"function",...}).
	Tools json.RawMessage `json:"tools"`
}

// fromRequest classifies by request-body shape. Gemini and the OpenAI
// Responses API have uniquely named top-level arrays, so they are matched
// first; the shared "messages" array is then split between anthropic-messages
// and openai-chat by secondary features.
func fromRequest(body []byte) (string, bool) {
	if !looksLikeJSONObject(body) {
		return "", false
	}
	var s requestShape
	if err := json.Unmarshal(body, &s); err != nil {
		return "", false
	}

	// Gemini: a "contents" array, or the Gemini-only config fields.
	if len(s.Contents) > 0 || len(s.GenerationConfig) > 0 || len(s.SystemInstruction) > 0 {
		return config.ProtocolGenerateContent, true
	}
	// OpenAI Responses: a top-level "input" array or "instructions".
	if len(s.Input) > 0 || len(s.Instructions) > 0 {
		return config.ProtocolResponses, true
	}
	// Both anthropic-messages and openai-chat require a non-empty "messages"
	// array. An empty (or absent) array carries no role/content/tool signal
	// and stays unclassified.
	if !hasArrayElements(s.Messages) {
		return "", false
	}
	// Anthropic requires max_tokens and commonly carries a top-level system;
	// its tools declare input_schema. OpenAI chat has none of these and
	// instead nests tools under {"type":"function","function":{...}} and uses
	// role:"tool" / assistant tool_calls.
	if len(s.MaxTokens) > 0 || len(s.System) > 0 || anthropicToolShape(s.Tools) || anthropicMessageShape(s.Messages) {
		return config.ProtocolMessages, true
	}
	return config.ProtocolChat, true
}

// anthropicToolShape reports whether any tool in the array carries an
// "input_schema" field — Anthropic's tool-parameter key. OpenAI chat tools
// instead nest a "function" object with "parameters", so this is a clean
// anthropic-vs-chat split.
func anthropicToolShape(tools json.RawMessage) bool {
	if len(tools) == 0 {
		return false
	}
	var arr []struct {
		InputSchema json.RawMessage `json:"input_schema"`
	}
	if err := json.Unmarshal(tools, &arr); err != nil {
		return false
	}
	for _, t := range arr {
		if len(t.InputSchema) > 0 {
			return true
		}
	}
	return false
}

// anthropicMessageShape reports whether any message uses Anthropic content
// blocks — an array of objects typed "tool_use" / "tool_result" / "text".
// OpenAI chat messages use string content or {"type":"text"|"image_url"}
// parts and signal tools via "tool_calls" / role:"tool" instead, so a
// tool_use/tool_result block is anthropic-exclusive.
func anthropicMessageShape(messages json.RawMessage) bool {
	if len(messages) == 0 {
		return false
	}
	var arr []struct {
		Content json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(messages, &arr); err != nil {
		return false
	}
	for _, m := range arr {
		var blocks []struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(m.Content, &blocks); err != nil {
			// String content (the common openai-chat shape) is not an array.
			continue
		}
		for _, b := range blocks {
			switch b.Type {
			case "tool_use", "tool_result":
				return true
			}
		}
	}
	return false
}

// hasArrayElements reports whether raw is a JSON array with at least one
// element. It guards against an empty "messages":[] — present but carrying no
// role/content/tool signal to disambiguate anthropic from chat.
func hasArrayElements(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return false
	}
	var arr []json.RawMessage
	if err := json.Unmarshal(raw, &arr); err != nil {
		return false
	}
	return len(arr) > 0
}

// looksLikeJSONObject reports whether body is a non-empty JSON object. It skips
// leading ASCII whitespace and checks for an opening brace, cheaply rejecting
// empty input, arrays, scalars, and obvious garbage before a full decode.
func looksLikeJSONObject(body []byte) bool {
	for _, b := range body {
		switch b {
		case ' ', '\t', '\n', '\r':
			continue
		case '{':
			return true
		default:
			return false
		}
	}
	return false
}
