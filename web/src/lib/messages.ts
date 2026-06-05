// Live-messages client. Mirrors contracts/admin/messages.go on the Go
// side and drives the messages pane in the SPA.
//
// Two transport shapes:
//   - one-shot fetch of /api/v1/messages/recent (uses apiFetch like
//     every other admin call)
//   - streaming fetch of /api/v1/messages/stream, parsed as SSE
//
// EventSource does not allow custom request headers, so the SPA cannot
// use it directly against the BasicAuth-protected /messages/stream
// route. We do SSE-over-fetch instead — same wire protocol, but we get
// to attach Authorization: Basic … by hand.

import { apiFetch } from "@/lib/api"
import { auth } from "@/lib/auth"

export type RuleHit = {
  rule_name: string
  actions_applied?: string[]
  terminated?: boolean
  error_message?: string
}

export type AttemptHit = {
  target: string
  started_at: string
  duration_ms?: number
  status_code?: number
  error?: string
  outcome: string
}

export type MessageEntry = {
  event_id: string
  at: string
  correlation_id?: string
  session_id?: string
  session_id_source?: string
  provider?: string
  endpoint?: string
  model?: string
  method?: string
  configuration?: string
  status_code: number
  duration_ms: number
  streaming?: boolean
  upstream_error?: string
  tokens_in?: number
  tokens_out?: number
  tokens_cached?: number
  tokens_cache_creation?: number
  tags?: string[]
  rules_matched?: RuleHit[]
  policy_ref?: string
  attempts?: AttemptHit[]
}

export type MessagesRecentResponse = {
  capacity: number
  entries: MessageEntry[]
}

/** Fetches the current ring contents (oldest-first). */
export async function fetchRecentMessages(limit?: number): Promise<MessagesRecentResponse> {
  const qs = limit && limit > 0 ? `?limit=${limit}` : ""
  return apiFetch<MessagesRecentResponse>(`/api/v1/messages/recent${qs}`)
}

// MessageFilters is the message browser's filter set. Empty fields are omitted
// from the query string (no predicate); tags is AND containment.
export type MessageFilters = {
  correlationId?: string
  sessionId?: string
  provider?: string
  model?: string
  configuration?: string
  endpoint?: string
  statusClass?: string
  tags?: string[]
  from?: string
  to?: string
}

// MessagesPage is one keyset page of the filtered browser. nextCursor is empty
// on the last page.
export type MessagesPage = {
  entries: MessageEntry[]
  nextCursor: string
}

type MessagesPageWire = {
  entries: MessageEntry[]
  next_cursor: string
}

/**
 * Fetches one filtered, keyset-paged page of messages (newest-first). Pass the
 * previous page's nextCursor to advance; omit it for page one. The endpoint
 * filter maps to the `protocol` column on the wire.
 */
export async function fetchMessagesPage(
  filters: MessageFilters,
  opts: { cursor?: string; limit?: number } = {},
): Promise<MessagesPage> {
  const p = new URLSearchParams()
  const put = (k: string, v?: string) => {
    if (v) p.set(k, v)
  }
  put("correlation_id", filters.correlationId)
  put("session_id", filters.sessionId)
  put("provider", filters.provider)
  put("model", filters.model)
  put("configuration", filters.configuration)
  put("protocol", filters.endpoint)
  put("status_class", filters.statusClass)
  put("from", filters.from)
  put("to", filters.to)
  for (const t of filters.tags ?? []) p.append("tags", t)
  put("cursor", opts.cursor)
  if (opts.limit && opts.limit > 0) p.set("limit", String(opts.limit))
  const r = await apiFetch<MessagesPageWire>(`/api/v1/messages?${p.toString()}`)
  return { entries: r.entries ?? [], nextCursor: r.next_cursor ?? "" }
}

// Facets is the distinct dropdown values for the browser. Each list is sorted
// and de-duplicated server-side.
export type Facets = {
  providers: string[]
  models: string[]
  configurations: string[]
  endpoints: string[]
  tags: string[]
}

/** Fetches the distinct filter-dropdown values (cached server-side). */
export async function fetchFacets(): Promise<Facets> {
  return apiFetch<Facets>(`/api/v1/facets`)
}

// GenAIMessagePart is a single part of a GenAI message — text, a model-issued
// tool call, a tool-call result, or a reasoning trace. Mirrors the gateway's
// emitPart JSON (cmd/gateway/reporter.go::jsonMap): tool calls carry name +
// arguments, tool-call responses carry a result, everything else carries
// content.
export type GenAIMessagePart = {
  type: string
  content?: string
  id?: string
  name?: string
  arguments?: unknown
  result?: unknown
}

// GenAIMessage is one role-tagged turn ([{role, parts}]). The gateway emits
// input as the latest user turn and output as the assistant turn (incl.
// tool_call parts).
export type GenAIMessage = {
  role: string
  parts?: GenAIMessagePart[]
}

// GenAIToolDefinition is one tool the request advertised to the model. Mirrors
// the gateway's jsonToolDefsString output.
export type GenAIToolDefinition = {
  type?: string
  name?: string
  description?: string
  parameters?: unknown
}

// GenAIContent is the bounded gen_ai.* content (system instructions, input
// turn, output, tool definitions) the gateway captured on the request span when
// content capture is enabled. Each field is the gateway's [{role, parts}] /
// tool-defs JSON. A {"truncated":...} marker replaces the object when the
// captured content exceeded the service's content cap.
export type GenAIContent = {
  input_messages?: GenAIMessage[]
  output_messages?: GenAIMessage[]
  tool_definitions?: GenAIToolDefinition[]
  system_instructions?: GenAIMessagePart[]
  truncated?: boolean
  original_bytes?: number
}

export type MessageBodyDetail = {
  event_id: string
  request?: string
  request_total_bytes: number
  request_truncated?: boolean
  response?: string
  response_total_bytes: number
  response_truncated?: boolean
  response_assembled?: string
  assembly_partial?: boolean
  request_headers?: Record<string, string[]>
  response_headers?: Record<string, string[]>
  // gen_ai_content is the bounded gen_ai.* content captured when content
  // capture is enabled, delivered as a JSON string (the contract field is a
  // string; parse with parseGenAIContent). Empty otherwise. Telemetry inspector
  // only.
  gen_ai_content?: string
}

// parseGenAIContent parses the gen_ai_content JSON string the body endpoint
// returns into a structured GenAIContent. Returns null when absent or
// malformed, so the inspector simply omits the GenAI tab rather than erroring.
export function parseGenAIContent(raw?: string): GenAIContent | null {
  if (!raw) return null
  try {
    const parsed = JSON.parse(raw) as GenAIContent
    return parsed && typeof parsed === "object" ? parsed : null
  } catch {
    return null
  }
}

/**
 * Fetches the captured request + response bodies for a single event
 * id. Returns null when the body store is disabled (503) or the
 * event_id has rolled out of the LRU (404). Anything else bubbles up
 * as a normal APIError / UnauthorizedError.
 */
export async function fetchMessageBody(eventId: string): Promise<MessageBodyDetail | null> {
  try {
    return await apiFetch<MessageBodyDetail>(`/api/v1/messages/${encodeURIComponent(eventId)}/body`)
  } catch (err) {
    const status = (err as { status?: number }).status
    if (status === 404 || status === 503) return null
    throw err
  }
}

export type StreamHandlers = {
  onMessage: (entry: MessageEntry) => void
  onDrop?: (count: number) => void
  onOpen?: () => void
  onError?: (err: unknown) => void
}

/**
 * Open the live SSE stream and invoke handlers as events arrive.
 * Returns an abort callback the caller invokes on unmount. Internally
 * uses fetch + ReadableStream because EventSource cannot carry the
 * Authorization header BasicAuth requires.
 *
 * Reconnect is the caller's responsibility — pages decide whether to
 * back off, surface a "disconnected" banner, etc.
 */
export function openMessageStream(handlers: StreamHandlers): () => void {
  const ctrl = new AbortController()
  void runStream(ctrl, handlers)
  return () => ctrl.abort()
}

async function runStream(ctrl: AbortController, h: StreamHandlers): Promise<void> {
  try {
    const headers = new Headers()
    const a = auth.header()
    if (a) headers.set("Authorization", a)
    headers.set("Accept", "text/event-stream")
    const res = await fetch("/admin/api/v1/messages/stream", {
      headers,
      signal: ctrl.signal,
    })
    if (!res.ok || !res.body) {
      h.onError?.(new Error(`stream open failed: ${res.status}`))
      return
    }
    h.onOpen?.()
    const reader = res.body.getReader()
    const decoder = new TextDecoder()
    let buffer = ""
    while (!ctrl.signal.aborted) {
      const { value, done } = await reader.read()
      if (done) return
      buffer += decoder.decode(value, { stream: true })
      let idx: number
      // SSE event frames are terminated by a blank line ("\n\n").
      while ((idx = buffer.indexOf("\n\n")) !== -1) {
        const frame = buffer.slice(0, idx)
        buffer = buffer.slice(idx + 2)
        parseFrame(frame, h)
      }
    }
  } catch (err) {
    if (!ctrl.signal.aborted) h.onError?.(err)
  }
}

function parseFrame(frame: string, h: StreamHandlers): void {
  let event = "message"
  const dataLines: string[] = []
  for (const line of frame.split("\n")) {
    if (line.startsWith(":")) continue // SSE comment / heartbeat
    if (line.startsWith("event:")) {
      event = line.slice(6).trim()
    } else if (line.startsWith("data:")) {
      dataLines.push(line.slice(5).trim())
    }
    // retry: lines are advisory — we ignore them because the page
    // controls reconnect cadence itself.
  }
  if (dataLines.length === 0) return
  const data = dataLines.join("\n")
  if (event === "message") {
    try {
      h.onMessage(JSON.parse(data) as MessageEntry)
    } catch (err) {
      h.onError?.(err)
    }
  } else if (event === "drop") {
    try {
      const parsed = JSON.parse(data) as { count?: number }
      if (typeof parsed.count === "number") h.onDrop?.(parsed.count)
    } catch {
      // ignore; drop frames are advisory
    }
  }
}
