import { useEffect, useMemo, useRef, useState } from "react"
import { useNavigate, useParams } from "react-router"
import { ChevronDown, ChevronLeft, ChevronRight, RefreshCw, Search, Workflow, X } from "lucide-react"
import {
  Area,
  AreaChart,
  Bar,
  BarChart,
  Cell,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from "recharts"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { KPI } from "@/components/atoms/kpi"
import { StatusPill } from "@/components/atoms/status-pill"
import { ProviderChip } from "@/components/atoms/provider-chip"
import { PanelCard, PanelHead, TableScroll } from "@/components/atoms/card"
import { Skeleton, SkeletonRows, SkeletonTiles, SkeletonBlock } from "@/components/atoms/skeleton"
import { Segmented } from "@/components/atoms/segmented"
import { Select } from "@/components/atoms/select"
import { MultiSelect } from "@/components/atoms/multi-select"
import { fmt } from "@/lib/fmt"
import { providerColor } from "@/lib/provider-color"
import { UnauthorizedError } from "@/lib/api"
import { fetchFacets, type Facets, type MessageEntry } from "@/lib/messages"
import { fetchSession, fetchSessions, type SessionSummary, type SessionView } from "@/lib/sessions"
import { Dash, Inspector } from "./messages"

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

// SessionsPage has two modes keyed off the URL: a browsable list of sessions
// active in a time range (/sessions) and the per-session detail view
// (/sessions/:id). The detail view carries a breadcrumb back to the list; the
// list rows open the detail view — the same pivot a message's "View session"
// link makes.
export function SessionsPage() {
  const { id: routeId } = useParams<{ id?: string }>()

  if (routeId) {
    return (
      <div className="flex flex-col gap-3.5 h-full min-h-0">
        <SessionBreadcrumb sessionId={routeId} />
        <SessionBody key={routeId} sessionId={routeId} />
      </div>
    )
  }
  return <SessionsList />
}

// SessionBreadcrumb sits atop the detail view: "Sessions / <id>", the first
// segment routing back to the list, plus the pivot into the lifecycle
// dashboard (the spans-derived lanes/ledger view) for this session.
function SessionBreadcrumb({ sessionId }: { sessionId: string }) {
  const nav = useNavigate()
  return (
    <div className="flex items-center gap-1.5 text-[13px] min-w-0 shrink-0">
      <button
        type="button"
        onClick={() => nav("/sessions")}
        className="text-[color:var(--accent)] hover:underline shrink-0"
      >
        Sessions
      </button>
      <ChevronRight size={14} className="text-[color:var(--text-4)] shrink-0" />
      <span className="mono text-[12px] text-[color:var(--text-2)] truncate" title={sessionId}>
        {sessionId}
      </span>
      <Button
        variant="secondary"
        size="sm"
        className="ml-auto shrink-0"
        onClick={() => nav(`/sessions/${encodeURIComponent(sessionId)}/lifecycle`)}
        title="Open the span-derived lifecycle dashboard"
      >
        <Workflow /> <span className="hidden sm:inline">Lifecycle</span>
      </Button>
    </div>
  )
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

function Placeholder({ text, tone }: { text: string; tone?: "err" }) {
  return (
    <div
      className="rounded-[var(--radius-lg)] border border-[color:var(--border)] bg-[color:var(--bg-1)] p-8 text-center text-[13px]"
      style={tone === "err" ? { color: "var(--err)", background: "var(--err-bg)" } : { color: "var(--text-3)" }}
    >
      {text}
    </div>
  )
}

// SessionDetailSkeleton traces the loaded layout — KPI tiles, the graphs panel,
// and the messages table — so the page holds its shape while the session loads
// rather than flashing a single centered spinner.
function SessionDetailSkeleton() {
  return (
    <>
      <SkeletonTiles count={6} className="grid grid-cols-2 md:grid-cols-3 lg:grid-cols-6 gap-3 shrink-0" />
      <div className="flex flex-col gap-3.5 flex-1 min-h-0">
        <PanelCard className="shrink-0">
          <div className="flex items-center px-4 h-[42px] border-b border-[color:var(--border)]">
            <Skeleton className="h-3.5 w-24" />
          </div>
          <div className="p-4 flex flex-col gap-4">
            <SkeletonBlock height={200} />
            <SkeletonBlock height={150} />
          </div>
        </PanelCard>
        <PanelCard className="flex-1 min-h-0 flex flex-col">
          <PanelHead title="Messages" sub="loading…" />
          <TableScroll>
            <tbody aria-busy="true">
              <SkeletonRows
                rows={8}
                cols={[{ w: "5rem" }, { w: "2.5rem" }, { w: "4rem" }, { w: "3.5rem" }, { w: "6rem" }, { w: "4rem", align: "right" }, { w: "4rem", align: "right" }]}
              />
            </tbody>
          </TableScroll>
        </PanelCard>
      </div>
    </>
  )
}

const GRAPHS_COLLAPSED_KEY = "telemetry.sessions.graphsCollapsed"

function SessionBody({ sessionId }: { sessionId: string }) {
  const nav = useNavigate()
  const [view, setView] = useState<SessionView | null>(null)
  const [status, setStatus] = useState<"loading" | "ok" | "missing" | "error">("loading")
  const [err, setErr] = useState("")
  const [collapsed, setCollapsed] = useState(() => localStorage.getItem(GRAPHS_COLLAPSED_KEY) === "1")
  const [selected, setSelected] = useState<number | null>(null)
  // The whole session loads at once (the graphs plot the full series), so the
  // messages table paginates client-side over the already-fetched requests —
  // a long conversation otherwise renders hundreds of rows in one scroll.
  const [pageSize, setPageSize] = useState<number>(SESSION_DEFAULT_PAGE_SIZE)
  const [pageIndex, setPageIndex] = useState(0)

  const toggleCollapsed = () => {
    setCollapsed((c) => {
      const next = !c
      localStorage.setItem(GRAPHS_COLLAPSED_KEY, next ? "1" : "0")
      return next
    })
  }

  useEffect(() => {
    let cancelled = false
    setStatus("loading")
    setSelected(null)
    setPageIndex(0)
    fetchSession(sessionId)
      .then((v) => {
        if (cancelled) return
        if (!v) {
          setStatus("missing")
          return
        }
        setView(v)
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
  }, [sessionId, nav])

  // requests are oldest-first (graphs plot them as-is); the table wants
  // newest-first, so it iterates a reversed view.
  const requests = useMemo(() => view?.requests ?? [], [view])
  const newestFirst = useMemo(() => requests.slice().reverse(), [requests])

  // selectAt opens the inspector at an absolute index into newestFirst and pulls
  // that row's page into view, so inspector prev/next can walk across page
  // boundaries without the table lagging behind.
  const selectAt = (idx: number) => {
    setSelected(idx)
    setPageIndex(Math.floor(idx / pageSize))
  }

  if (status === "loading") return <SessionDetailSkeleton />
  if (status === "missing") return <Placeholder text={`No telemetry for session ${sessionId}.`} />
  if (status === "error") return <Placeholder text={`Failed to load session: ${err}`} tone="err" />
  if (!view) return null

  // Client-side paging over the newest-first rows. pageIndex is clamped so a
  // page-size bump (which shrinks the page count) can't strand the view past the
  // last page.
  const pageCount = Math.max(1, Math.ceil(newestFirst.length / pageSize))
  const page = Math.min(pageIndex, pageCount - 1)
  const pageStart = page * pageSize
  const pageRows = newestFirst.slice(pageStart, pageStart + pageSize)

  return (
    <>
      <SessionTiles view={view} />

      {/* Graphs are fixed-height content (shrink-0), not a forced half of the
          viewport: fixed charts in a 50/50 split either trail whitespace on a
          tall session or cram/overlap on a short one. The messages table takes
          the remaining height; collapsing graphs hands it the whole pane. */}
      <div className="flex flex-col gap-3.5 flex-1 min-h-0">
        <PanelCard className="shrink-0">
          <button
            type="button"
            onClick={toggleCollapsed}
            className="flex items-center gap-2 w-full px-4 h-[42px] border-b border-[color:var(--border)] text-left hover:bg-[color:var(--hover)]"
            aria-expanded={!collapsed}
          >
            {collapsed ? <ChevronRight size={15} /> : <ChevronDown size={15} />}
            <span className="text-[13px] font-medium">Graphs</span>
            <span className="text-[11px] text-[color:var(--text-4)]">
              {collapsed ? "show" : "hide to expand the messages table"}
            </span>
          </button>
          {!collapsed && (
            <div className="p-4 flex flex-col gap-4">
              <SessionGraphs requests={requests} />
            </div>
          )}
        </PanelCard>

        <PanelCard className="flex-1 min-h-0 flex flex-col">
          <PanelHead title="Messages" sub={`${requests.length} request${requests.length === 1 ? "" : "s"} · newest first`} />
          <TableScroll>
            <thead>
              <tr className="text-[11px] uppercase tracking-[0.07em] text-[color:var(--text-3)]">
                <th className="text-left font-medium px-4 py-2">Time</th>
                <th className="text-left font-medium px-4 py-2">Status</th>
                <th className="text-left font-medium px-4 py-2">Provider</th>
                <th className="text-left font-medium px-4 py-2">Protocol</th>
                <th className="text-left font-medium px-4 py-2">Model</th>
                <th className="text-right font-medium px-4 py-2">Duration</th>
                <th className="text-right font-medium px-4 py-2">Tokens</th>
              </tr>
            </thead>
            <tbody>
              {pageRows.map((e, i) => {
                const idx = pageStart + i
                return (
                  <tr
                    key={e.event_id}
                    onClick={() => selectAt(idx)}
                    className="border-t border-[color:var(--border)] cursor-pointer hover:bg-[color:var(--hover)]"
                  >
                    <td className="mono text-[11.5px] px-4 py-2 text-[color:var(--text-3)] whitespace-nowrap">{fmt.shortTime(e.at)}</td>
                    <td className="px-4 py-2"><StatusPill code={e.status_code} /></td>
                    <td className="px-4 py-2">{e.provider ? <ProviderChip name={e.provider} /> : <Dash />}</td>
                    <td className="mono text-[12px] px-4 py-2">{e.protocol || <Dash />}</td>
                    <td className="mono text-[12px] px-4 py-2">{e.model || <Dash />}</td>
                    <td className="mono tnum text-[12px] text-right px-4 py-2">{fmt.ms(e.duration_ms)}</td>
                    <td className="mono tnum text-[11.5px] text-right px-4 py-2 text-[color:var(--text-3)]">
                      {(e.tokens_in ?? 0) + (e.tokens_out ?? 0) > 0 ? `${fmt.compact(e.tokens_in ?? 0)}/${fmt.compact(e.tokens_out ?? 0)}` : <Dash />}
                    </td>
                  </tr>
                )
              })}
            </tbody>
          </TableScroll>

          {newestFirst.length > pageSize && (
            <div className="flex items-center gap-3 px-4 py-2.5 border-t border-[color:var(--border)] mt-auto">
              <div className="flex items-center gap-1.5 text-[11px] text-[color:var(--text-4)]">
                <span>Rows</span>
                <Segmented
                  value={String(pageSize)}
                  onChange={(v) => {
                    setPageSize(Number(v))
                    setPageIndex(0)
                  }}
                  options={SESSION_PAGE_SIZES.map((n) => ({ value: String(n), label: String(n) }))}
                />
              </div>
              <div className="ml-auto flex items-center gap-2">
                <span className="text-[11px] text-[color:var(--text-4)] mono">
                  {pageStart + 1}–{Math.min(pageStart + pageSize, newestFirst.length)} of {newestFirst.length}
                </span>
                <Button variant="ghost" size="icon-xs" onClick={() => setPageIndex(page - 1)} disabled={page === 0} aria-label="Previous page"><ChevronLeft /></Button>
                <Button variant="ghost" size="icon-xs" onClick={() => setPageIndex(page + 1)} disabled={page >= pageCount - 1} aria-label="Next page"><ChevronRight /></Button>
              </div>
            </div>
          )}
        </PanelCard>
      </div>

      {selected !== null && newestFirst[selected] && (
        <Inspector
          entry={newestFirst[selected]}
          position={`${selected + 1} / ${newestFirst.length}`}
          onClose={() => setSelected(null)}
          onPrev={selected > 0 ? () => selectAt(selected - 1) : undefined}
          onNext={selected < newestFirst.length - 1 ? () => selectAt(selected + 1) : undefined}
        />
      )}
    </>
  )
}

function SessionTiles({ view }: { view: SessionView }) {
  const { totals, requests } = view
  const errAccent: "ok" | "warn" | "err" = totals.errors > 0 ? "warn" : "ok"
  // p95 latency and the model mix are derived client-side — the totals rollup
  // deliberately omits them (no extra aggregation query).
  const p95 = useMemo(() => percentile(requests.map((r) => r.duration_ms), 0.95), [requests])
  const models = useMemo(() => distinctModels(requests), [requests])
  const cached = useMemo(() => requests.reduce((s, r) => s + (r.tokens_cached ?? 0), 0), [requests])

  return (
    <div className="grid grid-cols-2 md:grid-cols-3 lg:grid-cols-6 gap-3 shrink-0">
      <KPI label="Requests" value={fmt.compact(totals.requests)} sub={`${totals.errors} error${totals.errors === 1 ? "" : "s"}`} accent={errAccent} />
      <KPI label="p95 latency" value={fmt.ms(p95)} sub="derived" />
      <KPI label="Tokens in" value={fmt.compact(totals.tokens_in)} sub={`${fmt.compact(cached)} cached`} />
      <KPI label="Tokens out" value={fmt.compact(totals.tokens_out)} sub="completion" />
      <KPI label="Models" value={String(models.length)} sub={models.length === 1 ? models[0]?.model : `${models.length} used`} />
      <KPI label="Span" value={spanLabel(requests)} sub="first → last" />
    </div>
  )
}

// TurnPoint is one request projected onto the graph axes. Built oldest-first so
// the cumulative series and the timeline read left-to-right in arrival order.
type TurnPoint = {
  i: number
  at: string
  model: string
  status: number
  durationMs: number
  cached: number
  fresh: number
  tokensOut: number
  cumIn: number
  isError: boolean
}

function SessionGraphs({ requests }: { requests: MessageEntry[] }) {
  const points = useMemo<TurnPoint[]>(() => {
    const out: TurnPoint[] = []
    let cum = 0
    for (let idx = 0; idx < requests.length; idx++) {
      const r = requests[idx]
      const tin = r.tokens_in ?? 0
      const cachedTok = Math.min(r.tokens_cached ?? 0, tin)
      cum += tin
      out.push({
        i: idx + 1,
        at: r.at,
        model: r.model || "unknown",
        status: r.status_code,
        durationMs: r.duration_ms,
        cached: cachedTok,
        fresh: Math.max(0, tin - cachedTok),
        tokensOut: r.tokens_out ?? 0,
        cumIn: cum,
        isError: r.status_code >= 400,
      })
    }
    return out
  }, [requests])

  const models = useMemo(() => distinctModels(requests).map((m) => m.model), [requests])
  const axis = "var(--text-4)"

  if (!points.length) return <div className="text-[12px] text-[color:var(--text-4)]">No requests in this session.</div>

  return (
    <>
      <ChartBlock
        title="Request timeline"
        sub="latency per request · colored by model · errors in red"
        legend={
          <div className="flex items-center gap-3 flex-wrap text-[11px] text-[color:var(--text-2)]">
            {models.map((m) => (
              <span key={m} className="inline-flex items-center gap-1.5">
                <span className="inline-block w-2.5 h-2.5 rounded-[2px]" style={{ backgroundColor: providerColor(m).fg }} />
                <span className="mono">{m}</span>
              </span>
            ))}
          </div>
        }
      >
        <ResponsiveContainer width="100%" height={200}>
          <BarChart data={points} margin={{ top: 6, right: 8, bottom: 0, left: 0 }}>
            <XAxis dataKey="at" tickFormatter={(v) => fmt.shortTime(v as string)} tick={{ fontSize: 10, fill: axis }} stroke={axis} interval="preserveStartEnd" minTickGap={28} />
            <YAxis tickFormatter={(v) => fmt.ms(v as number)} tick={{ fontSize: 10, fill: axis }} stroke={axis} width={44} />
            <Tooltip cursor={{ fill: "var(--hover)" }} content={<TurnTooltip />} />
            <Bar dataKey="durationMs" radius={[2, 2, 0, 0]} isAnimationActive={false}>
              {points.map((p) => (
                <Cell key={p.i} fill={p.isError ? "var(--err)" : providerColor(p.model).fg} />
              ))}
            </Bar>
          </BarChart>
        </ResponsiveContainer>
      </ChartBlock>

      <ChartBlock title="Cumulative input tokens" sub="context growth across the session">
        <ResponsiveContainer width="100%" height={150}>
          <AreaChart data={points} margin={{ top: 6, right: 8, bottom: 0, left: 0 }}>
            <XAxis dataKey="at" tickFormatter={(v) => fmt.shortTime(v as string)} tick={{ fontSize: 10, fill: axis }} stroke={axis} interval="preserveStartEnd" minTickGap={28} />
            <YAxis tickFormatter={(v) => fmt.compact(v as number)} tick={{ fontSize: 10, fill: axis }} stroke={axis} width={44} />
            <Tooltip cursor={{ stroke: "var(--border)" }} content={<TurnTooltip />} />
            <Area dataKey="cumIn" stroke="var(--accent)" fill="color-mix(in oklch, var(--accent) 15%, transparent)" strokeWidth={1.5} isAnimationActive={false} />
          </AreaChart>
        </ResponsiveContainer>
      </ChartBlock>

      <ChartBlock
        title="Token burn per request"
        sub="cached + fresh input, plus output · cost proxy"
        legend={
          <div className="flex items-center gap-3 flex-wrap text-[11px] text-[color:var(--text-2)]">
            <LegendSwatch color="var(--accent)" label="cached input" />
            <LegendSwatch color="color-mix(in oklch, var(--accent) 45%, var(--bg-2))" label="fresh input" />
            <LegendSwatch color="var(--text-3)" label="output" />
          </div>
        }
      >
        <ResponsiveContainer width="100%" height={150}>
          <BarChart data={points} margin={{ top: 6, right: 8, bottom: 0, left: 0 }}>
            <XAxis dataKey="at" tickFormatter={(v) => fmt.shortTime(v as string)} tick={{ fontSize: 10, fill: axis }} stroke={axis} interval="preserveStartEnd" minTickGap={28} />
            <YAxis tickFormatter={(v) => fmt.compact(v as number)} tick={{ fontSize: 10, fill: axis }} stroke={axis} width={44} />
            <Tooltip cursor={{ fill: "var(--hover)" }} content={<TurnTooltip />} />
            <Bar dataKey="cached" stackId="t" fill="var(--accent)" isAnimationActive={false} />
            <Bar dataKey="fresh" stackId="t" fill="color-mix(in oklch, var(--accent) 45%, var(--bg-2))" isAnimationActive={false} />
            <Bar dataKey="tokensOut" stackId="t" fill="var(--text-3)" radius={[2, 2, 0, 0]} isAnimationActive={false} />
          </BarChart>
        </ResponsiveContainer>
      </ChartBlock>
    </>
  )
}

function ChartBlock({ title, sub, legend, children }: { title: string; sub: string; legend?: React.ReactNode; children: React.ReactNode }) {
  return (
    <div className="flex flex-col gap-1.5">
      <div className="flex items-baseline gap-2 flex-wrap">
        <span className="text-[13px] font-medium">{title}</span>
        <span className="text-[11px] text-[color:var(--text-4)]">{sub}</span>
        {legend && <span className="ml-auto">{legend}</span>}
      </div>
      {children}
    </div>
  )
}

function LegendSwatch({ color, label }: { color: string; label: string }) {
  return (
    <span className="inline-flex items-center gap-1.5">
      <span className="inline-block w-2.5 h-2.5 rounded-[2px]" style={{ backgroundColor: color }} />
      <span>{label}</span>
    </span>
  )
}

// TurnTooltip renders the hovered request's detail. Shared across all three
// charts — each datum is a TurnPoint, so the tooltip reads the same fields.
// Typed locally rather than via recharts' TooltipProps (its v3 content-prop
// shape doesn't surface payload directly); recharts injects active/payload at
// runtime when it clones the element passed to Tooltip's content.
type TurnTooltipProps = { active?: boolean; payload?: { payload?: TurnPoint }[] }
function TurnTooltip({ active, payload }: TurnTooltipProps) {
  if (!active || !payload?.length) return null
  const p = payload[0]?.payload
  if (!p) return null
  const totalIn = p.cached + p.fresh
  return (
    <div className="rounded-[var(--radius)] border border-[color:var(--border)] bg-[color:var(--bg-1)] px-3 py-2 text-[11.5px] shadow-[var(--shadow-md)] flex flex-col gap-0.5">
      <div className="flex items-center gap-2">
        <StatusPill code={p.status} />
        <span className="mono text-[color:var(--text-3)]">#{p.i}</span>
        <span className="mono text-[color:var(--text-4)]">{fmt.shortTime(p.at)}</span>
      </div>
      <div className="mono">{p.model}</div>
      <div className="text-[color:var(--text-3)]">{fmt.ms(p.durationMs)} · in {fmt.compact(totalIn)} ({fmt.compact(p.cached)} cached) · out {fmt.compact(p.tokensOut)}</div>
    </div>
  )
}

// percentile returns the p-th (0..1) percentile of values via nearest-rank on a
// sorted copy. Returns 0 for an empty input.
function percentile(values: number[], p: number): number {
  if (!values.length) return 0
  const sorted = values.slice().sort((a, b) => a - b)
  const rank = Math.ceil(p * sorted.length) - 1
  return sorted[Math.min(Math.max(rank, 0), sorted.length - 1)]
}

// distinctModels returns each model in first-seen order with its request count.
function distinctModels(requests: MessageEntry[]): { model: string; count: number }[] {
  const order: string[] = []
  const counts = new Map<string, number>()
  for (const r of requests) {
    const m = r.model || "unknown"
    if (!counts.has(m)) order.push(m)
    counts.set(m, (counts.get(m) ?? 0) + 1)
  }
  return order.map((model) => ({ model, count: counts.get(model) ?? 0 }))
}

// spanLabel is the wall-clock duration between the first and last request,
// using fmt.uptime's two-largest-units formatting.
function spanLabel(requests: MessageEntry[]): string {
  if (requests.length < 2) return "—"
  const first = new Date(requests[0].at).getTime()
  const last = new Date(requests[requests.length - 1].at).getTime()
  if (!Number.isFinite(first) || !Number.isFinite(last) || last < first) return "—"
  return fmt.uptime(last - first)
}
