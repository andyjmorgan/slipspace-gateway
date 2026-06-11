import { useEffect, useMemo, useRef, useState } from "react"
import { useNavigate } from "react-router"
import { ChevronLeft, ChevronRight, RefreshCw, Search, X } from "lucide-react"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { PanelCard, PanelHead, TableScroll } from "@/components/atoms/card"
import { SkeletonRows } from "@/components/atoms/skeleton"
import { Segmented } from "@/components/atoms/segmented"
import { Select } from "@/components/atoms/select"
import { MultiSelect } from "@/components/atoms/multi-select"
import { fmt } from "@/lib/fmt"
import { UnauthorizedError } from "@/lib/api"
import { fetchFacets, type Facets } from "@/lib/messages"
import { fetchSessions, type SessionSummary } from "@/lib/sessions"
import { Dash } from "../components/messages-table"

// TIME_RANGES are the relative-window presets for the session list. "all" omits
// the lower bound so the grouped scan walks the full history (heaviest query, so
// not the default). Mirrors the messages browser's presets.
const SESSION_TIME_RANGES = [
  { value: "1h", label: "1h", ms: 3_600_000 },
  { value: "24h", label: "24h", ms: 86_400_000 },
  { value: "7d", label: "7d", ms: 604_800_000 },
  { value: "all", label: "All", ms: 0 },
] as const
type SessionTimeRange = (typeof SESSION_TIME_RANGES)[number]["value"]

const SESSION_PAGE_SIZES = [50, 100, 200] as const
const SESSION_DEFAULT_PAGE_SIZE = 50

const EMPTY_FACETS: Facets = { providers: [], models: [], configurations: [], protocols: [], tags: [] }

// SessionsPage is the browsable list of sessions active in a time range
// (/sessions). A row opens the session's lifecycle dashboard (sessions/:id —
// the spans-derived lanes/ledger/messages view), the same pivot a message's
// "View session" link makes.
export function SessionsPage() {
  return <SessionsList />
}

// SessionsList is the discovery surface: a filter bar (tags + configuration +
// time-range preset, plus a jump-to-id box) over a keyset-paged table of
// sessions active in the window. Each row opens the per-session detail view. Not
// a live feed — Refresh re-fetches the current page + facets.
function SessionsList() {
  const nav = useNavigate()

  // Jump-to-id box (direct lookup, bypasses the list).
  const [jumpInput, setJumpInput] = useState("")
  const jump = (e: React.FormEvent) => {
    e.preventDefault()
    const id = jumpInput.trim()
    if (id) nav(`/sessions/${encodeURIComponent(id)}`)
  }

  // Filters: tags (AND containment) + configuration + relative window preset.
  const [configuration, setConfiguration] = useState("")
  const [tags, setTags] = useState<string[]>([])
  const [timeRange, setTimeRange] = useState<SessionTimeRange>("24h")
  const [limit, setLimit] = useState<number>(SESSION_DEFAULT_PAGE_SIZE)

  const activeCount = useMemo(
    () => (configuration ? 1 : 0) + tags.length + (timeRange !== "all" ? 1 : 0),
    [configuration, tags, timeRange],
  )

  // Data + keyset paging. cursorsRef holds the cursor that fetches each page
  // (index 0 = "" = page one); navigational, not render state.
  const [facets, setFacets] = useState<Facets>(EMPTY_FACETS)
  const [sessions, setSessions] = useState<SessionSummary[]>([])
  const [nextCursor, setNextCursor] = useState("")
  const [pageIndex, setPageIndex] = useState(0)
  const cursorsRef = useRef<string[]>([""])
  const [status, setStatus] = useState<"loading" | "ok" | "error">("loading")
  const [err, setErr] = useState("")
  const [reloadNonce, setReloadNonce] = useState(0)

  // Any filter / range / page-size change invalidates the cursor stack — restart
  // at page one. Runs before the fetch effect so it reads the reset cursor.
  useEffect(() => {
    cursorsRef.current = [""]
    setPageIndex(0)
  }, [configuration, tags, timeRange, limit])

  useEffect(() => {
    let cancelled = false
    setStatus("loading")
    const range = SESSION_TIME_RANGES.find((r) => r.value === timeRange)
    const from = range && range.ms > 0 ? new Date(Date.now() - range.ms).toISOString() : undefined
    fetchSessions({ from, configuration, tags }, { cursor: cursorsRef.current[pageIndex] ?? "", limit })
      .then((p) => {
        if (cancelled) return
        setSessions(p.sessions)
        setNextCursor(p.nextCursor)
        setStatus("ok")
      })
      .catch((e) => {
        if (cancelled) return
        if (e instanceof UnauthorizedError) {
          nav("/login", { replace: true })
          return
        }
        setErr(e instanceof Error ? e.message : String(e))
        setStatus("error")
      })
    return () => {
      cancelled = true
    }
  }, [configuration, tags, timeRange, pageIndex, limit, reloadNonce, nav])

  // Facets back the dropdowns (cached server-side, cheap to re-fetch on Refresh).
  useEffect(() => {
    let cancelled = false
    fetchFacets()
      .then((f) => {
        if (!cancelled) setFacets(f)
      })
      .catch(() => {
        /* dropdowns degrade to empty; the table still loads */
      })
    return () => {
      cancelled = true
    }
  }, [reloadNonce])

  const onNext = () => {
    if (!nextCursor) return
    const ci = pageIndex + 1
    if (ci >= cursorsRef.current.length) cursorsRef.current = [...cursorsRef.current, nextCursor]
    else cursorsRef.current[ci] = nextCursor
    setPageIndex(ci)
  }
  const onPrev = () => {
    if (pageIndex > 0) setPageIndex(pageIndex - 1)
  }
  const clearFilters = () => {
    setConfiguration("")
    setTags([])
    setTimeRange("all")
  }

  return (
    <div className="flex flex-col gap-3.5">
      <div className="flex items-start gap-3">
        <div className="flex-1 min-w-0">
          <h1 className="text-[24px] font-semibold tracking-[-0.02em]">Sessions</h1>
          <div className="text-[13px] text-[color:var(--text-3)] mt-1">
            Conversations active in the selected window, newest activity first
          </div>
        </div>
        <Button variant="ghost" size="sm" onClick={() => setReloadNonce((n) => n + 1)} aria-label="Refresh">
          <RefreshCw /> <span className="hidden sm:inline">Refresh</span>
        </Button>
        <form onSubmit={jump} className="flex items-center gap-2">
          <Input
            placeholder="Session ID"
            value={jumpInput}
            onChange={(e) => setJumpInput(e.target.value)}
            className="h-9 w-48 text-[12px] mono"
          />
          <Button type="submit" size="sm" variant="secondary" disabled={!jumpInput.trim()}>
            <Search /> <span className="hidden sm:inline">Open</span>
          </Button>
        </form>
      </div>

      <PanelCard>
        <div className="flex flex-wrap items-center gap-2 p-3 border-b border-[color:var(--border)]">
          <MultiSelect label="Tags" values={tags} options={facets.tags} onChange={setTags} className="w-44" />
          <Select label="Config" value={configuration} options={facets.configurations} onChange={setConfiguration} className="w-44" />
          <Segmented
            value={timeRange}
            onChange={setTimeRange}
            options={SESSION_TIME_RANGES.map((r) => ({ value: r.value, label: r.label }))}
          />
          {activeCount > 0 && (
            <Button variant="ghost" size="sm" onClick={clearFilters}>
              <X /> Clear
            </Button>
          )}
        </div>

        <PanelHead
          title="Sessions"
          sub={`page ${pageIndex + 1} · ${sessions.length} shown${activeCount > 0 ? ` · ${activeCount} filter${activeCount > 1 ? "s" : ""}` : ""}`}
        />
        {status === "error" && (
          <div className="m-3 rounded-[var(--radius-lg)] border p-4 text-[13px]" style={{ color: "var(--err)", background: "var(--err-bg)" }}>
            Failed to load sessions: <span className="mono">{err}</span>
          </div>
        )}
        <TableScroll>
          <thead>
            <tr className="text-[11px] uppercase tracking-[0.07em] text-[color:var(--text-3)]">
              <th className="text-left font-medium px-4 py-2">Session</th>
              <th className="text-right font-medium px-4 py-2">Messages</th>
              <th className="text-right font-medium px-4 py-2">Subagents</th>
              <th className="text-right font-medium px-4 py-2">Tokens</th>
              <th className="text-left font-medium px-4 py-2">Models</th>
              <th className="text-left font-medium px-4 py-2">Started</th>
              <th className="text-left font-medium px-4 py-2">Last activity</th>
            </tr>
          </thead>
          <tbody aria-busy={status === "loading"}>
            {status === "loading" && (
              <SkeletonRows
                rows={Math.min(limit, 12)}
                cols={[{ w: "12rem" }, { w: "2.5rem", align: "right" }, { w: "2.5rem", align: "right" }, { w: "3rem", align: "right" }, { w: "8rem" }, { w: "9rem" }, { w: "4rem" }]}
              />
            )}
            {status !== "loading" && sessions.map((s) => (
              <tr
                key={s.session_id}
                onClick={() => nav(`/sessions/${encodeURIComponent(s.session_id)}`)}
                className="border-t border-[color:var(--border)] cursor-pointer hover:bg-[color:var(--hover)]"
              >
                <td className="mono text-[12px] px-4 py-2 max-w-[22rem] truncate" title={s.session_id}>{s.session_id}</td>
                <td className="mono tnum text-[12px] text-right px-4 py-2">{fmt.compact(s.messages)}</td>
                <td className="mono tnum text-[12px] text-right px-4 py-2">
                  {s.subagents > 0
                    ? <span className="inline-flex items-center rounded-full bg-[color:var(--accent-soft,var(--hover))] px-1.5 text-[color:var(--accent)]" title={`${s.subagents} subagent${s.subagents === 1 ? "" : "s"} spawned`}>{s.subagents}</span>
                    : <span className="text-[color:var(--text-4)]"><Dash /></span>}
                </td>
                <td className="mono tnum text-[12px] text-right px-4 py-2 text-[color:var(--text-3)]">
                  {s.total_tokens > 0 ? fmt.compact(s.total_tokens) : <Dash />}
                </td>
                <td className="px-4 py-2"><ModelsCell models={s.models} /></td>
                <td className="mono text-[11.5px] px-4 py-2 text-[color:var(--text-3)] whitespace-nowrap" title={fmt.fullTime(s.started_at)}>{fmt.fullTime(s.started_at)}</td>
                <td className="mono text-[11.5px] px-4 py-2 text-[color:var(--text-3)] whitespace-nowrap" title={fmt.fullTime(s.last_activity)}>{fmt.ago(s.last_activity)}</td>
              </tr>
            ))}
            {status === "ok" && sessions.length === 0 && (
              <tr><td colSpan={7} className="px-4 py-10 text-center text-[12px] text-[color:var(--text-4)]">{activeCount > 0 ? "No sessions match these filters." : "No sessions in this window."}</td></tr>
            )}
          </tbody>
        </TableScroll>

        <div className="flex items-center gap-3 px-4 py-2.5 border-t border-[color:var(--border)]">
          <div className="flex items-center gap-1.5 text-[11px] text-[color:var(--text-4)]">
            <span>Rows</span>
            <Segmented
              value={String(limit)}
              onChange={(v) => setLimit(Number(v))}
              options={SESSION_PAGE_SIZES.map((n) => ({ value: String(n), label: String(n) }))}
            />
          </div>
          <div className="ml-auto flex items-center gap-2">
            <span className="text-[11px] text-[color:var(--text-4)] mono">page {pageIndex + 1}</span>
            <Button variant="ghost" size="icon-xs" onClick={onPrev} disabled={pageIndex === 0} aria-label="Previous page"><ChevronLeft /></Button>
            <Button variant="ghost" size="icon-xs" onClick={onNext} disabled={!nextCursor} aria-label="Next page"><ChevronRight /></Button>
          </div>
        </div>
      </PanelCard>
    </div>
  )
}

// MODELS_CELL_MAX caps how many model chips a session row renders inline; the
// rest collapse into a "+N" so a session that touched many models can't blow out
// the row height. Mirrors the messages browser's TagCell.
const MODELS_CELL_MAX = 3

// ModelsCell renders a session's distinct models as compact chips, capped with a
// "+N" overflow. The full list is on the cell's hover title. Empty renders as the
// same em-dash the other columns use.
function ModelsCell({ models }: { models: string[] }) {
  if (!models || models.length === 0) return <Dash />
  const shown = models.slice(0, MODELS_CELL_MAX)
  const overflow = models.length - shown.length
  return (
    <div className="flex flex-wrap items-center gap-1" title={models.join(", ")}>
      {shown.map((m) => (
        <span
          key={m}
          className="inline-flex max-w-[12rem] items-center truncate rounded-[4px] border border-[color:var(--border)] bg-[color:var(--bg-2)] px-1.5 py-0.5 text-[10.5px] mono text-[color:var(--text-2)]"
        >
          {m}
        </span>
      ))}
      {overflow > 0 && <span className="mono text-[10.5px] text-[color:var(--text-4)]">+{overflow}</span>}
    </div>
  )
}
