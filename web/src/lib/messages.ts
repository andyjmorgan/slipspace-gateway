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

// Wire DTOs are generated from contracts/admin/messages.go. Do NOT hand-edit —
// run `make generate`. Source of truth: web/src/lib/generated/. The GenAI*,
// MessagesPage, and Facets shapes further down stay hand-written: they have no
// named Go contract struct yet (the gateway emits them as inline JSON).
import type {
  RuleHit,
  AttemptHit,
  MessageEntry,
  MessagesRecentResponse,
  MessageBodyDetail,
} from "./generated/admin"

export type {
  RuleHit,
  AttemptHit,
  MessageEntry,
  MessagesRecentResponse,
  MessageBodyDetail,
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
  conversationId?: string
  agentId?: string
  userId?: string
  provider?: string
  model?: string
  configuration?: string
  protocol?: string
  statusClass?: string
  tags?: string[]
  from?: string
  to?: string
}

// MessagesPage is one keyset page of the filtered browser. nextCursor is empty
// on the last page; total is the full match count (page-independent).
export type MessagesPage = {
  entries: MessageEntry[]
  nextCursor: string
  total: number
}

type MessagesPageWire = {
  entries: MessageEntry[]
  next_cursor: string
  total: number
}

/**
 * Fetches one filtered, keyset-paged page of messages (newest-first). Pass the
 * previous page's nextCursor to advance; omit it for page one. The endpoint
 * filter maps to the `protocol` column on the wire.
 */
export async function fetchMessagesPage(
  filters: MessageFilters,
  opts: { cursor?: string; limit?: number; sort?: string; order?: "asc" | "desc" } = {},
): Promise<MessagesPage> {
  const p = new URLSearchParams()
  const put = (k: string, v?: string) => {
    if (v) p.set(k, v)
  }
  put("correlation_id", filters.correlationId)
  put("session_id", filters.sessionId)
  put("conversation_id", filters.conversationId)
  put("agent_id", filters.agentId)
  put("user_id", filters.userId)
  put("provider", filters.provider)
  put("model", filters.model)
  put("configuration", filters.configuration)
  put("protocol", filters.protocol)
  put("status_class", filters.statusClass)
  put("from", filters.from)
  put("to", filters.to)
  for (const t of filters.tags ?? []) p.append("tags", t)
  put("cursor", opts.cursor)
  if (opts.limit && opts.limit > 0) p.set("limit", String(opts.limit))
  // sort defaults (newest-first by time) are the server's default — only send
  // a column when set, and only send order=asc (desc is the implicit default).
  put("sort", opts.sort)
  if (opts.order === "asc") p.set("order", "asc")
  const r = await apiFetch<MessagesPageWire>(`/api/v1/messages?${p.toString()}`)
  return { entries: r.entries ?? [], nextCursor: r.next_cursor ?? "", total: r.total ?? 0 }
}

// Facets is the distinct dropdown values for the browser. Each list is sorted
// and de-duplicated server-side.
export type Facets = {
  providers: string[]
  models: string[]
  configurations: string[]
  protocols: string[]
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

// MessageBodyDetail is generated (imported at the top of this file). The
// gen_ai_content field is a JSON string the body endpoint returns; parse it
// with parseGenAIContent into the hand-written GenAIContent shape below.

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

// BodySide selects one side of the body detail: "telemetry" (gen_ai content
// + raw span) or "report" (record wire bodies + headers). The console's
// bridge tabs fetch per side so pressing one never pulls the other.
export type BodySide = "telemetry" | "report"

/**
 * Fetches the captured request + response bodies for a single event
 * id. `include` narrows the response to one side (telemetry/report); a
 * backend predating the param ignores it and serves both — a superset, so
 * callers are unaffected. Returns null when the body store is disabled (503)
 * or the side doesn't exist for the id (404). Anything else bubbles up as a
 * normal APIError / UnauthorizedError.
 */
export async function fetchMessageBody(eventId: string, include?: BodySide): Promise<MessageBodyDetail | null> {
  try {
    const qs = include ? `?include=${include}` : ""
    return await apiFetch<MessageBodyDetail>(`/api/v1/messages/${encodeURIComponent(eventId)}/body${qs}`)
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
