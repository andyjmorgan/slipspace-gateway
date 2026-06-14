import { useEffect, useState } from "react"
import { useNavigate } from "react-router"
import { RefreshCw } from "lucide-react"
import { Button } from "@/components/ui/button"
import { PanelCard } from "@/components/atoms/card"
import { Segmented } from "@/components/atoms/segmented"
import { UnauthorizedError } from "@/lib/api"
import { fetchFindings, type FindingRow } from "@/lib/findings"
import { fetchMessageByCorrelation, type MessageEntry } from "@/lib/messages"
import { FindingsTable } from "../components/findings-table"
import { Inspector } from "./messages"

// FetchState is the result of one findings fetch, tagged with the request key it
// answers (limit:nonce). Keying it lets the view derive "loading" during render
// — status is "loading" until a result for the CURRENT key lands — so the effect
// only ever setStates inside its async callbacks, never synchronously.
type FetchState = { key: string; rows: FindingRow[] | null; err: string | null }

// PAGE_SIZES caps how many recent findings the page pulls — the security view is
// a "what's flagged right now" triage surface, not an exhaustive browse, so it
// loads a bounded recent window rather than paging.
const PAGE_SIZES = [50, 100, 200] as const
const DEFAULT_LIMIT = 100

// SecurityPage is the top-level operator view of flagged traffic: a list of
// recent detector findings (injection / toxicity / pii) across all sessions,
// newest first — the "find flagged traffic without opening each request" surface
// that complements the per-request inspector's Security tab. A row opens its
// source request in that same shared inspector, landing on the Security tab.
export function SecurityPage() {
  const nav = useNavigate()
  const [limit, setLimit] = useState<number>(DEFAULT_LIMIT)
  const [reloadNonce, setReloadNonce] = useState(0)

  const key = `${limit}:${reloadNonce}`
  const [fetched, setFetched] = useState<FetchState | null>(null)
  // Derived during render — no setState-in-effect. Until a result for the
  // current key arrives, the view is loading.
  const current = fetched && fetched.key === key ? fetched : null
  const status: "loading" | "ok" | "error" = !current ? "loading" : current.err ? "error" : "ok"
  const findings = current?.rows ?? []
  const err = current?.err ?? null

  useEffect(() => {
    let cancelled = false
    fetchFindings({ limit })
      .then((rows) => {
        if (!cancelled) setFetched({ key, rows, err: null })
      })
      .catch((e) => {
        if (cancelled) return
        if (e instanceof UnauthorizedError) {
          nav("/login", { replace: true })
          return
        }
        setFetched({ key, rows: null, err: e instanceof Error ? e.message : String(e) })
      })
    return () => {
      cancelled = true
    }
  }, [limit, key, nav])

  // The inspector opens on a fetched MessageEntry (the shared modal needs the
  // full request facts, which a finding row doesn't carry). entry undefined =
  // fetching; null = the request could not be loaded. Keyed by correlation id so
  // it derives closed when findings reload (no reset effect).
  const [selected, setSelected] = useState<{ cid: string; entry: MessageEntry | null } | null>(null)

  const openFinding = (f: FindingRow) => {
    const cid = f.correlation_id
    setSelected({ cid, entry: null })
    fetchMessageByCorrelation(cid)
      .then((entry) => {
        setSelected((cur) => (cur && cur.cid === cid ? { cid, entry } : cur))
      })
      .catch((e) => {
        if (e instanceof UnauthorizedError) nav("/login", { replace: true })
        // leave entry null -> the inspector shows nothing to open; close it
        setSelected((cur) => (cur && cur.cid === cid ? null : cur))
      })
  }

  return (
    <div className="flex flex-col gap-3.5">
      <div className="flex flex-wrap items-center gap-2 sm:gap-3">
        <div className="flex-1 min-w-0">
          <h1 className="text-[24px] font-semibold tracking-[-0.02em]">Security</h1>
          <div className="text-[13px] text-[color:var(--text-3)] mt-1">
            Recent flagged traffic — detector findings across all sessions, newest first
          </div>
        </div>
        <Segmented
          value={String(limit)}
          onChange={(v) => setLimit(Number(v))}
          options={PAGE_SIZES.map((n) => ({ value: String(n), label: String(n) }))}
        />
        <Button variant="ghost" size="sm" onClick={() => setReloadNonce((n) => n + 1)} aria-label="Refresh">
          <RefreshCw /> <span className="hidden sm:inline">Refresh</span>
        </Button>
      </div>

      <PanelCard>
        {status === "error" && (
          <div
            className="m-3 rounded-[var(--radius-lg)] border p-4 text-[13px]"
            style={{ color: "var(--err)", background: "var(--err-bg)" }}
          >
            Failed to load findings: <span className="mono">{err}</span>
          </div>
        )}
        <FindingsTable
          findings={findings}
          status={status}
          limit={limit}
          emptyText="No findings — nothing flagged in this window, or the scanner is disabled."
          onOpenMessage={openFinding}
          onOpenSession={(sessionID) => nav(`/sessions/${encodeURIComponent(sessionID)}`)}
        />
      </PanelCard>

      {selected && selected.entry && (
        <Inspector
          entry={selected.entry}
          position={selected.entry.correlation_id || selected.entry.event_id}
          initialTab="security"
          onClose={() => setSelected(null)}
        />
      )}
    </div>
  )
}
