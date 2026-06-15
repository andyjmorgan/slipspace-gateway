// verdict.ts is the one HTTP path for a single request's SlipSpace Arbiter
// security verdict + findings: GET /api/v1/verdict/{cid}. The wire shape
// (VerdictResponse) is generated from contracts/admin — never hand-edited.
import { apiFetch } from "@/lib/api"
import type { VerdictResponse } from "@/lib/generated/admin"

// verdictCache dedupes the fetch: the message inspector loads it once when a
// message opens (to badge the Security tab with the finding count) and the
// SecurityPane reuses the same promise, so opening the tab is instant and there
// is one request per request-id. Mirrors the span / session-findings caches. A
// failed fetch resolves to null (a missing verdict is a normal state).
const verdictCache = new Map<string, Promise<VerdictResponse | null>>()

export function loadVerdict(cid: string): Promise<VerdictResponse | null> {
  let p = verdictCache.get(cid)
  if (!p) {
    p = apiFetch<VerdictResponse>(`/api/v1/verdict/${encodeURIComponent(cid)}`).catch(() => null)
    verdictCache.set(cid, p)
  }
  return p
}
