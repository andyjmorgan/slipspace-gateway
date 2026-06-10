// SessionSpansDTO client. Types mirror span-dto.schema.json (SessionSpansDTO
// v1) — the expected response of GET /api/v1/sessions/{id}/spans: the flat
// gen_ai span list the session lifecycle page renders from. Every view-model
// value (lanes, gaps, the tool ledger, exec stats) is derived from this shape
// at render time per the renderer contract; the page has no knowledge of
// Records or the archive.

import { apiFetch } from "@/lib/api"

// SpanUsage is the per-span token rollup. server_tool_use is the generic
// counter map (gen_ai.usage.server_tool_use.*) for provider-executed tools.
export type SpanUsage = {
  input?: number | null
  output?: number | null
  cache_read?: number | null
  cache_creation?: number | null
  server_tool_use?: Record<string, number> | null
}

// OutputPartType enumerates the normalized assistant-output part envelope.
export type OutputPartType = "text" | "reasoning" | "tool_call" | "tool_call_response" | "unknown"

// OutputPart is one normalized assistant output part, in emission order.
// `args` is the raw arguments JSON, server-capped; it is an opaque vendor
// payload the renderer must never field-pick. `args_chars` carries the
// uncapped size — greater than args.length means the server truncated.
export type OutputPart = {
  type: OutputPartType
  chars?: number
  id?: string
  name?: string
  args?: string
  args_chars?: number
  // text is the result body of a tool_call_response output part (a
  // server-executed tool's result, on the same span as its call). The server
  // serves it whole — chars carries the same total.
  text?: string
}

// InputPart is one part of the trailing input delta (the NEW parts of this
// request only). A tool_call_response's `id` joins to a prior span's
// tool_call id; `chars` is the true total size before any server cap.
export type InputPart = {
  type: "text" | "tool_call_response"
  id?: string
  chars?: number
  text?: string
  is_error?: boolean
}

// SessionSpan is one gen_ai span of the session — one upstream request.
export type SessionSpan = {
  // cid is the correlation id (= span id in the console).
  cid: string
  // at is the request receipt time, RFC3339.
  at: string
  latency_ms?: number | null
  // ttfc_ms is gen_ai.response.time_to_first_chunk in ms (null when
  // non-streaming).
  ttfc_ms?: number | null
  // status is the upstream HTTP status.
  status?: number | null
  model?: string | null
  finish_reason?: string | null
  session_id: string
  // conversation_id = session_id for the main loop, AgentID for sub-agents.
  conversation_id: string
  parent_conversation_id?: string | null
  usage: SpanUsage
  output_parts: OutputPart[]
  input_parts: InputPart[]
  // input_text is the human text of the trailing message when input_parts has
  // a text part, server-capped; input_text_chars carries the uncapped total.
  input_text?: string | null
  input_text_chars?: number | null
  // output_text is the concatenated output text parts, server-capped;
  // output_text_chars carries the uncapped total.
  output_text?: string | null
  output_text_chars?: number | null
}

// SessionSpansPage is the paged wire envelope of GET /sessions/{id}/spans:
// one keyset page plus the cursor for the next (empty on the last page). The
// server pages so a large session never materializes in one response.
export type SessionSpansPage = {
  spans: SessionSpan[]
  next_cursor: string
}

// SessionSpansResult tags the span list with where it came from: "api" is the
// live projection; "fixture" is the bundled synthetic fallback served when the
// backend has no /spans endpoint yet, so the page can label the data honestly.
export type SessionSpansResult = {
  spans: SessionSpan[]
  source: "api" | "fixture"
}

/**
 * Fetches the session's complete gen_ai span list for the lifecycle page by
 * walking the paged endpoint (follows next_cursor until exhausted — the page
 * renders from the full list, so fetching in pages only bounds SERVER memory).
 * A bare-array response (a backend predating paging) is accepted as the full
 * list. When the endpoint 404s (a backend that predates the spans projection
 * entirely), falls back to the bundled synthetic fixture — lazily imported so
 * it stays out of the main chunk — and says so via `source`. Anything else
 * (incl. UnauthorizedError) bubbles up.
 */
export async function fetchSessionSpans(id: string): Promise<SessionSpansResult> {
  try {
    const spans: SessionSpan[] = []
    let cursor = ""
    for (;;) {
      const base = `/api/v1/sessions/${encodeURIComponent(id)}/spans`
      const path = cursor ? `${base}?cursor=${encodeURIComponent(cursor)}` : base
      const page = await apiFetch<SessionSpansPage | SessionSpan[]>(path)
      if (Array.isArray(page)) {
        // Pre-paging backend: the whole session in one array.
        return { spans: page, source: "api" }
      }
      spans.push(...(page?.spans ?? []))
      if (!page?.next_cursor) {
        return { spans, source: "api" }
      }
      cursor = page.next_cursor
    }
  } catch (err) {
    if ((err as { status?: number }).status === 404) {
      const { SESSION_SPANS_FIXTURE } = await import("@/telemetry/mock/session-spans-fixture")
      return { spans: SESSION_SPANS_FIXTURE, source: "fixture" }
    }
    throw err
  }
}
