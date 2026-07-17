// Session list client. Drives the telemetry console's session discovery list;
// a row pivots into the lifecycle dashboard (lib/session-spans.ts), which owns
// the per-session feeds.

import { apiFetch } from "@/lib/api"
// Wire DTOs are generated from the Go contracts (contracts/admin). Do NOT
// hand-edit — run `make generate`. Source of truth: web/src/lib/generated/.
// SessionSummary = one session-list row; SessionList = the wire list page.
import type {
  SessionSummary,
  SessionList as SessionListWire,
} from "./generated/admin"

export type { SessionSummary }

// SessionListPage is one keyset page of the session list, client-shaped:
// nextCursor (camelCase) is remapped from the wire's next_cursor by fetchSessions
// and is empty on the last page. The wire shape itself is the generated
// SessionListWire (= contracts/admin.SessionList) imported above.
export type SessionListPage = {
  sessions: SessionSummary[]
  nextCursor: string
  total: number
}

// SessionListFilters is the session list's filter set. Empty fields are
// omitted from the query string. The categorical dimensions send one repeated
// param per value and OR within the dimension; tags is AND containment. from
// is a resolved RFC3339 lower bound (the page computes it from a relative
// preset at fetch time).
export type SessionListFilters = {
  from?: string
  configurations?: string[]
  providers?: string[]
  models?: string[]
  protocols?: string[]
  tags?: string[]
}

/**
 * Fetches one keyset page of sessions active in the window, newest-activity
 * first. Pass the previous page's nextCursor to advance; omit it for page one.
 * sort selects the ordering column (last/started/messages/subagents/tokens)
 * and order its direction (default desc). Mirrors fetchMessagesPage's
 * query-building.
 */
export async function fetchSessions(
  filters: SessionListFilters,
  opts: { cursor?: string; limit?: number; sort?: string; order?: "asc" | "desc" } = {},
): Promise<SessionListPage> {
  const p = new URLSearchParams()
  const putAll = (k: string, vs?: string[]) => {
    for (const v of vs ?? []) if (v) p.append(k, v)
  }
  if (filters.from) p.set("from", filters.from)
  putAll("configuration", filters.configurations)
  putAll("provider", filters.providers)
  putAll("model", filters.models)
  putAll("protocol", filters.protocols)
  putAll("tags", filters.tags)
  if (opts.cursor) p.set("cursor", opts.cursor)
  if (opts.limit && opts.limit > 0) p.set("limit", String(opts.limit))
  if (opts.sort) p.set("sort", opts.sort)
  if (opts.order === "asc") p.set("order", "asc")
  const r = await apiFetch<SessionListWire>(`/api/v1/sessions?${p.toString()}`)
  return { sessions: r.sessions ?? [], nextCursor: r.next_cursor ?? "", total: r.total ?? 0 }
}
