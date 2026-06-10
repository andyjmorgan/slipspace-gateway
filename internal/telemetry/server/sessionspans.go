package server

import (
	"encoding/json"
	"math"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	adminc "github.com/andyjmorgan/sluice-gateway/contracts/admin"
	"github.com/andyjmorgan/sluice-gateway/internal/telemetry/store"
)

// This file is the span -> SessionSpansDTO v1 projection behind
// GET /api/v1/sessions/{id}/spans (the session lifecycle page's only feed).
// Everything is derived from the request_events columns + the span_event blob
// — the gen_ai span the OTLP ingest stored verbatim — never from Records
// (invariant #4; renderer contract: "spans only, no Record pulls").
//
// Truncation policy: full fidelity. Text and tool arguments are served whole;
// the contract's *_chars fields are populated with the true size (so they
// always equal the served length and the renderer's "showing first N of M"
// cap notice never fires). The gateway's own emission caps
// (SLUICE_OTEL_CAPTURE_CONTENT) and the ingest content bound already keep the
// blob finite, so no additional server-side cap is applied here.

// span_event blob keys the projection reads beyond what store.SpanFields
// models. The names are the OTel attribute keys the gateway emits (see
// internal/observability/genai.go); the blob stores them verbatim.
const (
	blobKeyLatencyMs        = "sluice.latency_ms"
	blobKeyUpstreamStatus   = "sluice.upstream_status"
	blobKeyTTFC             = "gen_ai.response.time_to_first_chunk"
	blobKeyFinishReasons    = "gen_ai.response.finish_reasons"
	blobKeyInputTokens      = "gen_ai.usage.input_tokens"                //nolint:gosec // attribute key, not a credential
	blobKeyOutputTokens     = "gen_ai.usage.output_tokens"               //nolint:gosec // attribute key, not a credential
	blobKeyCacheReadTokens  = "gen_ai.usage.cache_read.input_tokens"     //nolint:gosec // attribute key, not a credential
	blobKeyCacheCreateToken = "gen_ai.usage.cache_creation.input_tokens" //nolint:gosec // attribute key, not a credential
	blobKeyServerToolPrefix = "gen_ai.usage.server_tool_use."            //nolint:gosec // attribute key, not a credential
	blobKeyGenAIContent     = "gen_ai_content"
)

// handleSessionSpans serves the SessionSpansDTO v1 array for a session: one
// element per request event, oldest-first (ordered by `at` — EventsBySession's
// (observed_at, correlation_id) order). 404 mirrors the sibling
// /sessions/{id} when no event carries the session id.
func (s *Server) handleSessionSpans(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	events, err := s.queries.EventsBySession(r.Context(), id)
	if err != nil {
		s.queryError(w, "session spans", err)
		return
	}
	if len(events) == 0 {
		writeError(w, http.StatusNotFound, "session not found")
		return
	}
	writeJSON(w, http.StatusOK, mapSessionSpans(events))
}

// mapSessionSpans projects a session's events (assumed oldest-first) onto the
// DTO array. Always returns a non-nil slice so the wire shape is `[]`, never
// `null`.
func mapSessionSpans(events []store.RequestEvent) []adminc.SessionSpan {
	out := make([]adminc.SessionSpan, 0, len(events))
	for _, e := range events {
		out = append(out, sessionSpanFromEvent(e))
	}
	return out
}

// sessionSpanFromEvent projects one request event onto its DTO element. The
// identity/scalar fields come from the promoted columns; latency, upstream
// status, time-to-first-chunk, finish reasons, usage, and the content
// envelopes are decoded from the span_event blob. A missing or malformed blob
// degrades to nulls/empty envelopes — never an error — matching how the rest
// of the console renders a column-only row.
func sessionSpanFromEvent(e store.RequestEvent) adminc.SessionSpan {
	var blob map[string]json.RawMessage
	if len(e.SpanEvent) > 0 {
		// A malformed blob leaves the map nil; every lookup below tolerates that.
		_ = json.Unmarshal(e.SpanEvent, &blob)
	}

	span := adminc.SessionSpan{
		CID:            e.CorrelationID,
		At:             e.ObservedAt.UTC(),
		SessionID:      e.SessionID,
		ConversationID: e.ConversationID,
		OutputParts:    []adminc.SessionSpanOutputPart{},
		InputParts:     []adminc.SessionSpanInputPart{},
	}
	// conversation_id = session_id for the main loop; older spans may omit the
	// conversation attribute entirely, which means "no subagent thread".
	if span.ConversationID == "" {
		span.ConversationID = e.SessionID
	}
	if e.ParentConversationID != "" {
		span.ParentConversationID = &e.ParentConversationID
	}
	if e.Model != "" {
		span.Model = &e.Model
	}
	if v, ok := blobInt(blob, blobKeyLatencyMs); ok {
		span.LatencyMs = &v
	}
	if sec, ok := blobFloat(blob, blobKeyTTFC); ok {
		ms := int64(math.Round(sec * 1000))
		span.TTFCMs = &ms
	}
	if v, ok := blobInt(blob, blobKeyUpstreamStatus); ok && v != 0 {
		status := int(v)
		span.Status = &status
	} else if e.StatusCode != 0 {
		status := e.StatusCode
		span.Status = &status
	}
	if reasons := blobStrings(blob, blobKeyFinishReasons); len(reasons) > 0 {
		span.FinishReason = &reasons[0]
	}
	span.Usage = usageFromBlob(blob)

	if raw, ok := blob[blobKeyGenAIContent]; ok {
		var c spanContent
		if json.Unmarshal(raw, &c) == nil {
			span.OutputParts, span.OutputText, span.OutputTextChars = outputEnvelope(c.OutputMessages)
			span.InputParts, span.InputText, span.InputTextChars = inputEnvelope(c.InputMessages)
		}
	}
	return span
}

// usageFromBlob reads the token rollup. Presence-keyed: a count is null when
// the span carried no such attribute (vs. an explicit zero), and the
// server-tool counter map collects every gen_ai.usage.server_tool_use.*
// attribute under the provider's own counter suffix.
func usageFromBlob(blob map[string]json.RawMessage) adminc.SessionSpanUsage {
	var u adminc.SessionSpanUsage
	if v, ok := blobInt(blob, blobKeyInputTokens); ok {
		u.Input = &v
	}
	if v, ok := blobInt(blob, blobKeyOutputTokens); ok {
		u.Output = &v
	}
	if v, ok := blobInt(blob, blobKeyCacheReadTokens); ok {
		u.CacheRead = &v
	}
	if v, ok := blobInt(blob, blobKeyCacheCreateToken); ok {
		u.CacheCreation = &v
	}
	for k := range blob {
		if !strings.HasPrefix(k, blobKeyServerToolPrefix) {
			continue
		}
		if v, ok := blobInt(blob, k); ok {
			if u.ServerToolUse == nil {
				u.ServerToolUse = make(map[string]int64)
			}
			u.ServerToolUse[strings.TrimPrefix(k, blobKeyServerToolPrefix)] = v
		}
	}
	return u
}

// spanContent / spanMessage / spanPart mirror the gen_ai_content envelope the
// ingest stores: {input_messages, output_messages} of [{role, parts}], each
// part in the gateway normalizer's uniform shape (text/reasoning carry
// content, tool_call carries id/name/arguments, tool_call_response carries
// id/result). Only the fields this projection reads are modelled — the blob
// itself stays verbatim in the store.
type spanContent struct {
	InputMessages  []spanMessage `json:"input_messages"`
	OutputMessages []spanMessage `json:"output_messages"`
}

type spanMessage struct {
	Role  string     `json:"role"`
	Parts []spanPart `json:"parts"`
}

type spanPart struct {
	Type      string          `json:"type"`
	Content   string          `json:"content"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
	Result    string          `json:"result"`
}

// outputEnvelope flattens the assistant output messages into the DTO's
// normalized part envelope (emission order preserved) plus the concatenated
// output text. Server-executed tools appear as a tool_call followed by a
// tool_call_response on the same span — both ride through with their join id.
// Part types outside the contract enum (pass-through media blocks) map to
// "unknown" so the renderer can count them without guessing.
func outputEnvelope(msgs []spanMessage) ([]adminc.SessionSpanOutputPart, *string, *int) {
	parts := []adminc.SessionSpanOutputPart{}
	var text strings.Builder
	hasText := false
	for _, m := range msgs {
		for _, p := range m.Parts {
			switch p.Type {
			case "text":
				n := charCount(p.Content)
				parts = append(parts, adminc.SessionSpanOutputPart{Type: "text", Chars: &n})
				text.WriteString(p.Content)
				hasText = true
			case "reasoning":
				n := charCount(p.Content)
				parts = append(parts, adminc.SessionSpanOutputPart{Type: "reasoning", Chars: &n})
			case "tool_call":
				part := adminc.SessionSpanOutputPart{Type: "tool_call", ID: p.ID, Name: p.Name}
				if len(p.Arguments) > 0 {
					args := string(p.Arguments)
					n := charCount(args)
					part.Args = args
					part.ArgsChars = &n
				}
				parts = append(parts, part)
			case "tool_call_response":
				n := charCount(p.Result)
				parts = append(parts, adminc.SessionSpanOutputPart{Type: "tool_call_response", ID: p.ID, Chars: &n, Text: p.Result})
			default:
				parts = append(parts, adminc.SessionSpanOutputPart{Type: "unknown"})
			}
		}
	}
	if !hasText {
		return parts, nil, nil
	}
	s := text.String()
	n := charCount(s)
	return parts, &s, &n
}

// inputEnvelope flattens the trailing input delta (the gateway captures only
// the NEW turn of each request) into the DTO's input parts plus the
// concatenated human text. Only the contract's part types survive — text and
// tool_call_response — since media blocks carry no text to render.
func inputEnvelope(msgs []spanMessage) ([]adminc.SessionSpanInputPart, *string, *int) {
	parts := []adminc.SessionSpanInputPart{}
	var text strings.Builder
	hasText := false
	for _, m := range msgs {
		for _, p := range m.Parts {
			switch p.Type {
			case "text":
				n := charCount(p.Content)
				parts = append(parts, adminc.SessionSpanInputPart{Type: "text", Chars: &n, Text: p.Content})
				text.WriteString(p.Content)
				hasText = true
			case "tool_call_response":
				n := charCount(p.Result)
				parts = append(parts, adminc.SessionSpanInputPart{Type: "tool_call_response", ID: p.ID, Chars: &n, Text: p.Result})
			}
		}
	}
	if !hasText {
		return parts, nil, nil
	}
	s := text.String()
	n := charCount(s)
	return parts, &s, &n
}

// charCount is the *_chars unit: Unicode code points. Always <= a JS string's
// .length for the same text, so the renderer's `chars > text.length`
// truncation guard can never false-positive on multibyte content.
func charCount(s string) int {
	return utf8.RuneCountInString(s)
}

// blobInt reads an integer-valued blob key leniently — JSON number (int or
// float) or a numeric string — mirroring how the ingest stores native OTLP
// values and how store.SpanFields documents its numeric reads.
func blobInt(blob map[string]json.RawMessage, key string) (int64, bool) {
	raw, ok := blob[key]
	if !ok {
		return 0, false
	}
	var i int64
	if json.Unmarshal(raw, &i) == nil {
		return i, true
	}
	var f float64
	if json.Unmarshal(raw, &f) == nil {
		return int64(f), true
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		if n, err := strconv.ParseInt(s, 10, 64); err == nil {
			return n, true
		}
	}
	return 0, false
}

// blobFloat reads a float-valued blob key (JSON number or numeric string).
func blobFloat(blob map[string]json.RawMessage, key string) (float64, bool) {
	raw, ok := blob[key]
	if !ok {
		return 0, false
	}
	var f float64
	if json.Unmarshal(raw, &f) == nil {
		return f, true
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		if n, err := strconv.ParseFloat(s, 64); err == nil {
			return n, true
		}
	}
	return 0, false
}

// blobStrings reads a string-array blob key, accepting a single string as a
// one-element array (the same leniency the ingest applies to tags).
func blobStrings(blob map[string]json.RawMessage, key string) []string {
	raw, ok := blob[key]
	if !ok {
		return nil
	}
	var arr []string
	if json.Unmarshal(raw, &arr) == nil {
		return nonEmpty(arr)
	}
	var s string
	if json.Unmarshal(raw, &s) == nil && s != "" {
		return []string{s}
	}
	return nil
}
