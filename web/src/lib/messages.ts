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

export type MessageEntry = {
  event_id: string
  at: string
  correlation_id?: string
  provider?: string
  endpoint?: string
  model?: string
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
