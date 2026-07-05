package server

import (
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"

	adminc "github.com/andyjmorgan/slipspace-gateway/contracts/admin"
	"github.com/andyjmorgan/slipspace-gateway/internal/arbiter/store"
	"github.com/andyjmorgan/slipspace-gateway/internal/safego"
)

// This file is the span -> SessionSpansDTO v1 projection behind
// GET /api/v1/sessions/{id}/spans (the session lifecycle page's only feed).
// Everything is derived from the request_events columns + the span_event blob
// — the gen_ai span the OTLP ingest stored verbatim — never from Records
// (invariant #4; renderer contract: "spans only, no Record pulls").
//
// Memory discipline: the endpoint is keyset-paged on the wire
// ({spans, next_cursor}; default 200 spans, max 500) and the store streams
// rows through a callback into a bounded worker pool, so the handler holds at
// most ~2x spanProjectWorkers full span_event blobs plus one page of capped
// DTOs — never the whole session. The pre-paging single-shot array peaked at
// 600 MB - 1 GB on big sessions and OOM-killed the 512 Mi prod pod
// (2026-06-10); the bounded pool keeps the worst case to a few MB while
// cutting the page's serial decode latency by ~the worker count.
//
// Projection modes: ?include=structure serves the ENVELOPE ONLY — every id,
// name, *_chars size, timing, and usage field, but none of the content
// bodies (text, tool args, input_text, output_text). The lifecycle dashboard
// renders lanes/ledger/stats from sizes alone, so its pages shrink from MBs
// to KBs; the span inspector lazy-fetches the one span it needs in full via
// GET /sessions/{id}/spans/{cid}. Any other ?include value (or none) serves
// the full bodies — the pre-split wire shape, kept as the default for older
// clients.
//
// Truncation policy: each served content field (text, tool args, input_text,
// output_text) is capped at Server.spanFieldCap bytes (span_field_max_bytes,
// default 64 KiB; 0 disables), cut on a rune boundary. The contract's *_chars
// fields carry the TRUE uncapped sizes, so the renderer's "showing first N of
// M" notice fires exactly when the server truncated. This revises the v1
// "full fidelity" decision: serving unbounded fields is unsafe while ingest
// content caps are disabled (content_max_bytes: 0), and 64 KiB per field
// keeps any realistic page to tens of MB.

// span_event blob keys the projection reads beyond what store.SpanFields
// models. The names are the OTel attribute keys the gateway emits (see
// internal/observability/genai.go); the blob stores them verbatim.
const (
	blobKeyLatencyMs        = "slipspace.latency_ms"
	blobKeyUpstreamStatus   = "slipspace.upstream_status"
	blobKeyTTFC             = "gen_ai.response.time_to_first_chunk"
	blobKeyFinishReasons    = "gen_ai.response.finish_reasons"
	blobKeyInputTokens      = "gen_ai.usage.input_tokens"                //nolint:gosec // attribute key, not a credential
	blobKeyOutputTokens     = "gen_ai.usage.output_tokens"               //nolint:gosec // attribute key, not a credential
	blobKeyCacheReadTokens  = "gen_ai.usage.cache_read.input_tokens"     //nolint:gosec // attribute key, not a credential
	blobKeyCacheCreateToken = "gen_ai.usage.cache_creation.input_tokens" //nolint:gosec // attribute key, not a credential
	blobKeyServerToolPrefix = "gen_ai.usage.server_tool_use."            //nolint:gosec // attribute key, not a credential
	blobKeyCostUSD          = "slipspace.cost.usd"
	blobKeyGenAIContent     = "gen_ai_content"
)

// handleSessionSpans serves one SessionSpansDTO v1 page for a session: one
// element per request event, oldest-first ((observed_at, correlation_id)
// order), wrapped in {spans, next_cursor}. ?cursor= pages forward (the same
// keyset cursor convention as the event list); ?limit= sizes the page
// (default 200, max 500, store-enforced). Each row is projected to its capped
// DTO inside the store's streaming callback so the full blob of only one span
// is live at a time. 404 mirrors the sibling /sessions/{id} when the FIRST
// page of a session is empty; a later page going empty (rows pruned between
// pages) is a normal end-of-list, not a 404.
func (s *Server) handleSessionSpans(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	cursor := r.URL.Query().Get("cursor")
	// Envelope-only mode: bodies stay false only for the exact "structure"
	// value; anything else keeps the full pre-split shape (lenient, like the
	// rest of the query params).
	bodies := r.URL.Query().Get("include") != "structure"
	limit := limitParam(r, 0)

	// Order-preserving bounded fan-out: the streaming callback assigns each row
	// a slot in a pre-sized buffer and hands the raw event to a small worker
	// pool, which projects rows concurrently. The serial form paid the whole
	// page's blob decode on one goroutine (~28 ms/span on prod-sized blobs =
	// 3.5 s per 125-span page); the pool divides that by ~the worker count.
	// In-flight full blobs stay bounded at workers (processing) + cap(jobs)
	// (queued) + 1 (in the callback) — a few MB worst case, preserving the
	// OOM-fix discipline. buf is sized to the store's effective page limit and
	// never reallocated, so the callback's slot writes and the workers' element
	// writes touch disjoint memory (no slice-header sharing).
	ctx := r.Context()
	workers := spanProjectWorkers()
	jobs := make(chan spanProjectJob, workers)
	buf := make([]adminc.SessionSpan, store.ClampSessionPageLimit(limit))
	var wg sync.WaitGroup
	wg.Add(workers)
	for range workers {
		safego.Go(ctx, "telemetry.sessionspans.project", s.log, nil, func() {
			defer wg.Done()
			for j := range jobs {
				// Per-row recover keeps the worker draining even if one row's
				// projection panics (the slot stays a zero-value DTO), so the
				// producer can never block on a dead pool.
				safego.Run(ctx, "telemetry.sessionspans.project_row", s.log, nil, func() {
					buf[j.idx] = sessionSpanFromEvent(j.e, s.spanFieldCap, bodies)
				})
			}
		})
	}
	n := 0
	next, err := s.queries.EventsBySessionPage(ctx, store.SessionPageParams{
		SessionID: id,
		Cursor:    cursor,
		Limit:     limit,
	}, func(e store.RequestEvent) error {
		jobs <- spanProjectJob{idx: n, e: e}
		n++
		return nil
	})
	// Always drain the pool — also on the error path, where workers may still
	// hold queued jobs.
	close(jobs)
	wg.Wait()
	if err != nil {
		if errors.Is(err, store.ErrInvalidCursor) {
			writeError(w, http.StatusBadRequest, "invalid cursor")
			return
		}
		s.queryError(w, "session spans", err)
		return
	}
	if n == 0 && cursor == "" {
		writeError(w, http.StatusNotFound, "session not found")
		return
	}
	writeJSON(w, http.StatusOK, adminc.SessionSpansPage{Spans: buf[:n], NextCursor: next})
}

// spanProjectJob is one row handed to the projection pool: the raw event and
// its order-preserving slot in the page buffer.
type spanProjectJob struct {
	idx int
	e   store.RequestEvent
}

// spanProjectWorkers sizes the projection pool: one worker per CPU, capped so
// the in-flight blob bound (and the goroutine count per request) stays small
// even on large hosts.
func spanProjectWorkers() int {
	n := runtime.GOMAXPROCS(0)
	if n > 8 {
		n = 8
	}
	if n < 1 {
		n = 1
	}
	return n
}

// handleSessionSpan serves ONE span's full DTO element (content bodies
// included, still per-field capped) — the lazy fetch behind the lifecycle
// page's span inspector, which renders the dashboard from envelope-only
// structure pages and pulls a single full span on modal open. 404 when the
// correlation id is unknown OR belongs to a different session, so the
// session-scoped route never leaks a span across sessions.
func (s *Server) handleSessionSpan(w http.ResponseWriter, r *http.Request) {
	e, err := s.queries.GetRequestEvent(r.Context(), r.PathValue("cid"))
	if err != nil {
		if errors.Is(err, store.ErrRequestEventNotFound) {
			writeError(w, http.StatusNotFound, "span not found")
			return
		}
		s.queryError(w, "session span", err)
		return
	}
	if e.SessionID != r.PathValue("id") {
		writeError(w, http.StatusNotFound, "span not found")
		return
	}
	writeJSON(w, http.StatusOK, sessionSpanFromEvent(e, s.spanFieldCap, true))
}

// handleEventSpan serves one correlation id's full DTO element without a
// session scope — the message browser's inspector feed. The browser lists
// events by arbitrary filters (a row may carry no session id at all), so it
// cannot route through /sessions/{id}/spans/{cid}; this is the same
// projection keyed by the id alone. 404 when no span was captured for the id
// (a record-only event) so the client can degrade to the Report tab.
func (s *Server) handleEventSpan(w http.ResponseWriter, r *http.Request) {
	e, err := s.queries.GetRequestEvent(r.Context(), r.PathValue("id"))
	if err != nil {
		if errors.Is(err, store.ErrRequestEventNotFound) {
			writeError(w, http.StatusNotFound, "span not found")
			return
		}
		s.queryError(w, "event span", err)
		return
	}
	writeJSON(w, http.StatusOK, sessionSpanFromEvent(e, s.spanFieldCap, true))
}

// sessionSpanFromEvent projects one request event onto its DTO element. The
// identity/scalar fields come from the promoted columns; latency, upstream
// status, time-to-first-chunk, finish reasons, usage, and the content
// envelopes are decoded from the span_event blob. fieldCap bounds each served
// content field in bytes (<= 0 disables); *_chars always carry the true
// sizes. bodies=false is the structure projection: the part envelopes, ids,
// names, and every *_chars size survive, but the content bodies (part text,
// tool args, input_text, output_text) are omitted entirely. A missing or
// malformed blob degrades to nulls/empty envelopes — never an error —
// matching how the rest of the console renders a column-only row.
func sessionSpanFromEvent(e store.RequestEvent, fieldCap int, bodies bool) adminc.SessionSpan {
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
			span.OutputParts, span.OutputText, span.OutputTextChars = outputEnvelope(c.OutputMessages, fieldCap, bodies)
			span.InputParts, span.InputText, span.InputTextChars = inputEnvelope(c.InputMessages, fieldCap, bodies)
		}
	}
	return span
}

// capText bounds s to at most maxBytes bytes (<= 0 disables), cutting on a
// rune boundary so a capped field is always valid UTF-8. Callers pair it with
// the true charCount so the renderer can tell a cap fired.
func capText(s string, maxBytes int) string {
	if maxBytes <= 0 || len(s) <= maxBytes {
		return s
	}
	cut := maxBytes
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
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
	if v, ok := blobFloat(blob, blobKeyCostUSD); ok {
		u.CostUSD = &v
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
	Executor  string          `json:"executor"`
}

// outputEnvelope flattens the assistant output messages into the DTO's
// normalized part envelope (emission order preserved) plus the concatenated
// output text. Server-executed tools appear as a tool_call followed by a
// tool_call_response on the same span — both ride through with their join id.
// Part types outside the contract enum (pass-through media blocks) map to
// "unknown" so the renderer can count them without guessing. fieldCap bounds
// each served field; *_chars carry the true sizes. bodies=false omits every
// content body (args, result text, output_text) while keeping the sizes —
// including output_text_chars, which is summed without ever building the
// concatenation.
func outputEnvelope(msgs []spanMessage, fieldCap int, bodies bool) ([]adminc.SessionSpanOutputPart, *string, *int) {
	parts := []adminc.SessionSpanOutputPart{}
	var text strings.Builder
	totalChars := 0
	hasText := false
	for _, m := range msgs {
		for _, p := range m.Parts {
			switch p.Type {
			case "text":
				n := charCount(p.Content)
				parts = append(parts, adminc.SessionSpanOutputPart{Type: "text", Chars: &n})
				if bodies {
					text.WriteString(p.Content)
				}
				totalChars += n
				hasText = true
			case "reasoning":
				n := charCount(p.Content)
				parts = append(parts, adminc.SessionSpanOutputPart{Type: "reasoning", Chars: &n})
			case "tool_call":
				part := adminc.SessionSpanOutputPart{Type: "tool_call", ID: p.ID, Name: p.Name, Executor: p.Executor}
				if len(p.Arguments) > 0 {
					args := string(p.Arguments)
					n := charCount(args)
					if bodies {
						part.Args = capText(args, fieldCap)
					}
					part.ArgsChars = &n
				}
				parts = append(parts, part)
			case "tool_call_response":
				n := charCount(p.Result)
				part := adminc.SessionSpanOutputPart{Type: "tool_call_response", ID: p.ID, Chars: &n, Executor: p.Executor}
				if bodies {
					part.Text = capText(p.Result, fieldCap)
				}
				parts = append(parts, part)
			default:
				parts = append(parts, adminc.SessionSpanOutputPart{Type: "unknown"})
			}
		}
	}
	if !hasText {
		return parts, nil, nil
	}
	if !bodies {
		return parts, nil, &totalChars
	}
	s := capText(text.String(), fieldCap)
	return parts, &s, &totalChars
}

// inputEnvelope flattens the trailing input delta (the gateway captures only
// the NEW turn of each request) into the DTO's input parts plus the
// concatenated human text. Only the contract's part types survive — text and
// tool_call_response — since media blocks carry no text to render. fieldCap
// bounds each served field; *_chars carry the true sizes. bodies=false omits
// the part text and input_text bodies while keeping every size.
func inputEnvelope(msgs []spanMessage, fieldCap int, bodies bool) ([]adminc.SessionSpanInputPart, *string, *int) {
	parts := []adminc.SessionSpanInputPart{}
	var text strings.Builder
	totalChars := 0
	hasText := false
	for _, m := range msgs {
		for _, p := range m.Parts {
			switch p.Type {
			case "text":
				n := charCount(p.Content)
				part := adminc.SessionSpanInputPart{Type: "text", Chars: &n}
				if bodies {
					part.Text = capText(p.Content, fieldCap)
					text.WriteString(p.Content)
				}
				parts = append(parts, part)
				totalChars += n
				hasText = true
			case "tool_call_response":
				n := charCount(p.Result)
				part := adminc.SessionSpanInputPart{Type: "tool_call_response", ID: p.ID, Chars: &n}
				if bodies {
					part.Text = capText(p.Result, fieldCap)
				}
				parts = append(parts, part)
			}
		}
	}
	if !hasText {
		return parts, nil, nil
	}
	if !bodies {
		return parts, nil, &totalChars
	}
	s := capText(text.String(), fieldCap)
	return parts, &s, &totalChars
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
