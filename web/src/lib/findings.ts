// findings.ts is the one HTTP path for the operator Security view: the
// finding-centric list at GET /api/v1/findings. The same endpoint backs the
// top-level Security page (recent findings across all sessions) and a
// session-scoped view (?session=<id>); the wire shape (FindingsListResponse /
// FindingRow) is generated from contracts/admin — never hand-edited.
import { apiFetch } from "@/lib/api"
import type { FindingsListResponse, FindingRow } from "@/lib/generated/admin"

export type { FindingRow }

// fetchFindings returns recent findings, newest first. With a session id it
// scopes to that one session; without, it returns the most recent across all
// sessions bounded by limit (server default ~100).
export async function fetchFindings(opts: { session?: string; limit?: number } = {}): Promise<FindingRow[]> {
  const p = new URLSearchParams()
  if (opts.session) p.set("session", opts.session)
  if (opts.limit && opts.limit > 0) p.set("limit", String(opts.limit))
  const qs = p.toString()
  const r = await apiFetch<FindingsListResponse>(`/api/v1/findings${qs ? `?${qs}` : ""}`)
  return r.items ?? []
}

// sessionFindingsCache dedupes the per-session findings fetch so the session
// view can badge its Security tab with the finding count (loaded eagerly) and
// the Security panel reuses the same result when opened — one request per
// session. Mirrors the verdict / span module caches.
const sessionFindingsCache = new Map<string, Promise<FindingRow[]>>()

export function loadSessionFindings(sid: string): Promise<FindingRow[]> {
  let p = sessionFindingsCache.get(sid)
  if (!p) {
    p = fetchFindings({ session: sid })
    sessionFindingsCache.set(sid, p)
  }
  return p
}
