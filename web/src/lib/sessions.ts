// Session view client. Mirrors contracts/admin.SessionView on the Go side and
// drives the telemetry console's per-session page: one conversation's requests
// (oldest-first, as the graphs plot them) plus aggregate totals for the header
// tiles. Each request is the same MessageEntry shape the messages browser
// renders, so the session page reuses the messages row + GenAI inspector.

import { apiFetch } from "@/lib/api"
import type { MessageEntry } from "@/lib/messages"

// SessionTotals is the header-tile rollup over a session's requests. An error
// is any request with status >= 400. Latency percentiles and cached-token sums
// are intentionally absent — the page derives those client-side from requests.
export type SessionTotals = {
  requests: number
  errors: number
  tokens_in: number
  tokens_out: number
}

// SessionView is the GET /api/v1/sessions/{id} response: every request in the
// session as a MessageEntry (oldest-first) plus the aggregate totals.
export type SessionView = {
  session_id: string
  totals: SessionTotals
  requests: MessageEntry[]
}

/**
 * Fetches one session's requests + totals. Returns null when the session id is
 * unknown (404) so the page can render an empty state rather than erroring;
 * anything else (incl. UnauthorizedError) bubbles up.
 */
export async function fetchSession(id: string): Promise<SessionView | null> {
  try {
    return await apiFetch<SessionView>(`/api/v1/sessions/${encodeURIComponent(id)}`)
  } catch (err) {
    if ((err as { status?: number }).status === 404) return null
    throw err
  }
}

// SessionSummary is one row of the session-discovery list (GET /api/v1/sessions).
// Mirrors contracts/admin.SessionSummary. When the request carried tag/config
// filters the counts + bounds reflect only the matching requests of the session.
export type SessionSummary = {
  session_id: string
  messages: number
  total_tokens: number
  models: string[]
  started_at: string
  last_activity: string
}

// SessionListPage is one keyset page of the session list. nextCursor is empty on
// the last page.
export type SessionListPage = {
  sessions: SessionSummary[]
  nextCursor: string
}

type SessionListWire = {
  sessions: SessionSummary[]
  next_cursor: string
}

// SessionListFilters is the session list's filter set. Empty fields are omitted
// from the query string; tags is AND containment. from is a resolved RFC3339
// lower bound (the page computes it from a relative preset at fetch time).
export type SessionListFilters = {
  from?: string
  configuration?: string
  tags?: string[]
}

/**
 * Fetches one keyset page of sessions active in the window, newest-activity
 * first. Pass the previous page's nextCursor to advance; omit it for page one.
 * Mirrors fetchMessagesPage's query-building.
 */
export async function fetchSessions(
  filters: SessionListFilters,
  opts: { cursor?: string; limit?: number } = {},
): Promise<SessionListPage> {
  const p = new URLSearchParams()
  if (filters.from) p.set("from", filters.from)
  if (filters.configuration) p.set("configuration", filters.configuration)
  for (const t of filters.tags ?? []) p.append("tags", t)
  if (opts.cursor) p.set("cursor", opts.cursor)
  if (opts.limit && opts.limit > 0) p.set("limit", String(opts.limit))
  const r = await apiFetch<SessionListWire>(`/api/v1/sessions?${p.toString()}`)
  return { sessions: r.sessions ?? [], nextCursor: r.next_cursor ?? "" }
}
