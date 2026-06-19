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
// answers (range:nonce). Keying it lets the view derive "loading" during render
// — status is "loading" until a result for the CURRENT key lands — so the effect
// only ever setStates inside its async callbacks, never synchronously.
type FetchState = { key: string; rows: FindingRow[] | null; err: string | null }

// TIME_RANGES are the relative-window presets for the Security findings list,
// mirroring the messages/sessions browsers: the page selects flagged traffic by
// window, not row count. "all" omits the lower bound (the server's safety cap
// still bounds the scan). Default 24h matches the sessions list.
const TIME_RANGES = [
  { value: "1h", label: "1h", ms: 3_600_000 },
  { value: "24h", label: "24h", ms: 86_400_000 },
  { value: "7d", label: "7d", ms: 604_800_000 },
  { value: "all", label: "All", ms: 0 },
] as const
type TimeRange = (typeof TIME_RANGES)[number]["value"]
const DEFAULT_RANGE: TimeRange = "24h"
// SKELETON_ROWS sizes the loading placeholder — a fixed guess now that the page
// loads by window rather than a chosen row count.
const SKELETON_ROWS = 12

// SecurityPage is the top-level operator view of flagged traffic: a list of
// recent detector findings (injection / toxicity / pii) across all sessions,
// newest first — the "find flagged traffic without opening each request" surface
// that complements the per-request inspector's Security tab. A row opens its
// source request in that same shared inspector, landing on the Security tab.
export function SecurityPage() {
  const nav = useNavigate()
  const [timeRange, setTimeRange] = useState<TimeRange>(DEFAULT_RANGE)
  const [reloadNonce, setReloadNonce] = useState(0)

  const key = `${timeRange}:${reloadNonce}`
  const [fetched, setFetched] = useState<FetchState | null>(null)
  // Derived during render — no setState-in-effect. Until a result for the
  // current key arrives, the view is loading.
  const current = fetched && fetched.key === key ? fetched : null
  const status: "loading" | "ok" | "error" = !current ? "loading" : current.err ? "error" : "ok"
  const findings = current?.rows ?? []
  const err = current?.err ?? null

  useEffect(() => {
    let cancelled = false
    // Resolve the window at fetch time (mirrors the sessions browser): "all"
    // omits the lower bound, every other preset is now-minus-span.
    const range = TIME_RANGES.find((r) => r.value === timeRange)
    const from = range && range.ms > 0 ? new Date(Date.now() - range.ms).toISOString() : undefined
    fetchFindings({ from })
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
  }, [timeRange, key, nav])

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
            Flagged traffic — detector findings across all sessions in the selected window, newest first
          </div>
        </div>
        <Segmented
          value={timeRange}
          onChange={(v) => setTimeRange(v as TimeRange)}
          options={TIME_RANGES.map((r) => ({ value: r.value, label: r.label }))}
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
          limit={SKELETON_ROWS}
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
