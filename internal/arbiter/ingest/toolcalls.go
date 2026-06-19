package ingest

import (
	"encoding/json"
	"unicode/utf8"

	"github.com/andyjmorgan/slipspace-gateway/internal/arbiter/store"
)

// maxToolResultBytes caps the tool result text stored per row, cut on a rune
// boundary; the true size rides ResultChars so a reader can tell it was capped.
// The result already lives whole in span_event — this bounds the audit row so a
// single large tool output (a file read, a command dump) can't bloat the table.
// Arguments are left whole: when content capture is on they are valid, typically
// small JSON, and truncating would break JSONB validity.
const maxToolResultBytes = 64 * 1024

// genAIContent mirrors the span_event.gen_ai_content envelope the ingest stores
// ({input_messages, output_messages} of [{role, parts}]) — the same shape the
// session-spans projection decodes. Only the fields the tool-call extraction
// reads are modelled; the blob stays verbatim in the store.
type genAIContent struct {
	InputMessages  []genAIMessage `json:"input_messages"`
	OutputMessages []genAIMessage `json:"output_messages"`
}

type genAIMessage struct {
	Parts []genAIPart `json:"parts"`
}

// genAIPart is one normalized message part. tool_call carries id/name/arguments;
// tool_call_response carries id/result; executor marks server vs client on both.
type genAIPart struct {
	Type      string          `json:"type"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
	Result    string          `json:"result"`
	Executor  string          `json:"executor"`
}

// ToolCallsFromEvent extracts a span's tool-call observations from the
// normalized gen_ai_content blob the OTLP ingest already stored — no
// per-protocol parsing (the gateway normalized OpenAI/Anthropic/Gemini into the
// uniform part shape upstream). The assistant OUTPUT messages yield the call
// halves (and, for provider-executed tools, their same-span result halves); the
// trailing INPUT delta yields the client result halves of prior calls. Parts
// with no join id are skipped (a row needs the id to key + join — notably Gemini
// function calls, which carry none on the wire). Returns nil when the span
// carried no content (content capture off, or the blob was truncated whole).
func ToolCallsFromEvent(e store.RequestEvent) []store.ToolCallObservation {
	if len(e.SpanEvent) == 0 {
		return nil
	}
	var blob struct {
		Content json.RawMessage `json:"gen_ai_content"`
	}
	if json.Unmarshal(e.SpanEvent, &blob) != nil || len(blob.Content) == 0 {
		return nil
	}
	var c genAIContent
	if json.Unmarshal(blob.Content, &c) != nil {
		return nil
	}

	var out []store.ToolCallObservation
	for _, m := range c.OutputMessages {
		for _, p := range m.Parts {
			switch p.Type {
			case "tool_call":
				if p.ID == "" {
					continue
				}
				out = append(out, callObservation(e, p))
			case "tool_call_response":
				if p.ID == "" {
					continue
				}
				out = append(out, resultObservation(e, p))
			}
		}
	}
	for _, m := range c.InputMessages {
		for _, p := range m.Parts {
			if p.Type == "tool_call_response" && p.ID != "" {
				out = append(out, resultObservation(e, p))
			}
		}
	}
	return out
}

// callObservation builds the CALL half from a tool_call part, stamping the
// span's routing facts so the audit list filters without joining request_events.
func callObservation(e store.RequestEvent, p genAIPart) store.ToolCallObservation {
	return store.ToolCallObservation{
		Kind:           store.ToolCallHalf,
		ToolCallID:     p.ID,
		ToolName:       p.Name,
		Arguments:      nonEmptyJSON(p.Arguments),
		ArgumentsChars: utf8.RuneCountInString(string(p.Arguments)),
		Executor:       p.Executor,
		SessionID:      e.SessionID,
		CorrelationID:  e.CorrelationID,
		ObservedAt:     e.ObservedAt,
		Provider:       e.Provider,
		Configuration:  e.Configuration,
		Protocol:       e.Protocol,
		Model:          e.Model,
	}
}

// resultObservation builds the RESPONSE half from a tool_call_response part,
// capping the result text (ResultChars carries the true size).
func resultObservation(e store.RequestEvent, p genAIPart) store.ToolCallObservation {
	return store.ToolCallObservation{
		Kind:          store.ToolResultHalf,
		ToolCallID:    p.ID,
		Result:        capRunes(p.Result, maxToolResultBytes),
		ResultChars:   utf8.RuneCountInString(p.Result),
		Executor:      p.Executor,
		SessionID:     e.SessionID,
		CorrelationID: e.CorrelationID,
		ObservedAt:    e.ObservedAt,
	}
}

// nonEmptyJSON returns the raw argument bytes, or nil for an absent/null value
// so the column lands SQL NULL rather than the literal "null".
func nonEmptyJSON(raw json.RawMessage) []byte {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	return raw
}

// capRunes bounds s to at most maxBytes bytes (<= 0 disables), cutting on a rune
// boundary so the stored prefix is always valid UTF-8.
func capRunes(s string, maxBytes int) string {
	if maxBytes <= 0 || len(s) <= maxBytes {
		return s
	}
	cut := maxBytes
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}
