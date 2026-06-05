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
