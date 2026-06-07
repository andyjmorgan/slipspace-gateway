// Session view client. Mirrors contracts/admin.SessionView on the Go side and
// drives the telemetry console's per-session page: one conversation's requests
// (oldest-first, as the graphs plot them) plus aggregate totals for the header
// tiles. Each request is the same MessageEntry shape the messages browser
// renders, so the session page reuses the messages row + GenAI inspector.

import { apiFetch } from "@/lib/api"
// Wire DTOs are generated from the Go contracts (contracts/admin). Do NOT
// hand-edit — run `make generate`. Source of truth: web/src/lib/generated/.
// SessionTotals = header-tile rollup; SessionView = GET /sessions/{id};
// SessionSummary = one session-list row; SessionList = the wire list page.
import type {
  SessionTotals,
  SessionView,
  SessionSummary,
  SessionList as SessionListWire,
} from "./generated/admin"

export type { SessionTotals, SessionView, SessionSummary }

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

// SessionListPage is one keyset page of the session list, client-shaped:
// nextCursor (camelCase) is remapped from the wire's next_cursor by fetchSessions
// and is empty on the last page. The wire shape itself is the generated
// SessionListWire (= contracts/admin.SessionList) imported above.
export type SessionListPage = {
  sessions: SessionSummary[]
  nextCursor: string
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
