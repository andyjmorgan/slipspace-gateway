import { useEffect, useRef, useState } from "react"
import { useNavigate } from "react-router"
import { ChevronLeft, ChevronRight, RefreshCw, Search } from "lucide-react"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { PanelCard, TableScroll } from "@/components/atoms/card"
import { Sheet } from "@/components/atoms/sheet"
import { ActiveFilterChips, FilterField, FiltersButton, type Chip } from "@/components/atoms/filters"
import { SkeletonRows } from "@/components/atoms/skeleton"
import { Segmented } from "@/components/atoms/segmented"
import { MultiSelect } from "@/components/atoms/multi-select"
import { HeaderFilter } from "@/components/atoms/header-filter"
import { PageHeader } from "@/components/atoms/page-header"
import { fmt } from "@/lib/fmt"
import { UnauthorizedError } from "@/lib/api"
import { fetchFacets, type Facets } from "@/lib/messages"
import { fetchSessions, type SessionSummary } from "@/lib/sessions"
import { Dash, rangeLabel, SortHeader, TagCell, toggleSort, type SortState } from "../components/messages-table"

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

  // Filters: tags (AND containment) + configuration/provider/model/protocol
  // multi-selects (many values OR within the dimension) + relative window
  // preset. The Models/Tags header funnel menus edit the same arrays as the
  // sheet — one state, two surfaces.
  const [configurations, setConfigurations] = useState<string[]>([])
  const [providers, setProviders] = useState<string[]>([])
  const [models, setModels] = useState<string[]>([])
  const [protocols, setProtocols] = useState<string[]>([])
  const [tags, setTags] = useState<string[]>([])
  const [timeRange, setTimeRange] = useState<SessionTimeRange>("24h")
  const [limit, setLimit] = useState<number>(SESSION_DEFAULT_PAGE_SIZE)
  // sort defaults to last-activity descending — the natural order, set
  // explicitly so the "Last activity" header reads as the active sort. A header
  // click changes it; a sort change restarts paging like a filter change.
  const [sort, setSort] = useState<SortState>({ key: "last", desc: true })
  // Filter controls live in a right-side slide-over; applied filters show as
  // removable toolbar chips. filtersOpen toggles the panel.
  const [filtersOpen, setFiltersOpen] = useState(false)

  // Data + keyset paging. cursorsRef holds the cursor that fetches each page
  // (index 0 = "" = page one); navigational, not render state.
  const [facets, setFacets] = useState<Facets>(EMPTY_FACETS)
  const [sessions, setSessions] = useState<SessionSummary[]>([])
  const [nextCursor, setNextCursor] = useState("")
  const [total, setTotal] = useState(0)
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
  }, [configurations, providers, models, protocols, tags, timeRange, limit, sort])

  useEffect(() => {
    let cancelled = false
    setStatus("loading")
    const range = SESSION_TIME_RANGES.find((r) => r.value === timeRange)
    const from = range && range.ms > 0 ? new Date(Date.now() - range.ms).toISOString() : undefined
    fetchSessions(
      { from, configurations, providers, models, protocols, tags },
      { cursor: cursorsRef.current[pageIndex] ?? "", limit, sort: sort?.key, order: sort && !sort.desc ? "asc" : "desc" },
    )
      .then((p) => {
        if (cancelled) return
        setSessions(p.sessions)
        setNextCursor(p.nextCursor)
        setTotal(p.total)
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
  }, [configurations, providers, models, protocols, tags, timeRange, sort, pageIndex, limit, reloadNonce, nav])

  // Facets back the dropdowns (cached server-side per window, cheap to
  // re-fetch on Refresh). Window-scoped to the same range as the list, so the
  // menus offer only values present in it; the relative bound is resolved
  // here at fetch time (Date.now() must not run during render).
  useEffect(() => {
    let cancelled = false
    const range = SESSION_TIME_RANGES.find((r) => r.value === timeRange)
    const from = range && range.ms > 0 ? new Date(Date.now() - range.ms).toISOString() : undefined
    fetchFacets(from ? { from } : undefined)
      .then((f) => {
        if (!cancelled) setFacets(f)
      })
      .catch(() => {
        /* dropdowns degrade to empty; the table still loads */
      })
    return () => {
      cancelled = true
    }
  }, [reloadNonce, timeRange])

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
  const onSort = (key: string) => setSort((s) => toggleSort(s, key))
  const clearFilters = () => {
    setConfigurations([])
    setProviders([])
    setModels([])
    setProtocols([])
    setTags([])
    // Clear restores the default window (24h), not the unbounded all-time scan
    // ("all" is the heaviest query — see the timeRange default above).
    setTimeRange("24h")
  }

  const chips: Chip[] = [
    ...configurations.map((v) => ({ key: `config:${v}`, label: "Config", value: v, onClear: () => setConfigurations(configurations.filter((x) => x !== v)) })),
    ...providers.map((v) => ({ key: `provider:${v}`, label: "Provider", value: v, onClear: () => setProviders(providers.filter((x) => x !== v)) })),
    ...models.map((v) => ({ key: `model:${v}`, label: "Model", value: v, onClear: () => setModels(models.filter((x) => x !== v)) })),
    ...protocols.map((v) => ({ key: `protocol:${v}`, label: "Protocol", value: v, onClear: () => setProtocols(protocols.filter((x) => x !== v)) })),
    ...tags.map((t) => ({ key: `tag:${t}`, label: "Tag", value: t, onClear: () => setTags(tags.filter((x) => x !== t)) })),
  ] as Chip[]

  return (
    <div className="flex flex-col gap-3.5">
      <PageHeader title="Sessions" sub="Conversations active in the selected window, newest activity first">
        <form onSubmit={jump} className="flex items-center gap-2">
          <Input
            aria-label="Open session by ID"
            placeholder="Session ID"
            value={jumpInput}
            onChange={(e) => setJumpInput(e.target.value)}
            className="h-9 w-40 text-[12px] mono"
          />
          <Button type="submit" size="sm" variant="secondary" disabled={!jumpInput.trim()} aria-label="Open session">
            <Search /> <span className="hidden sm:inline">Open</span>
          </Button>
        </form>
        <Segmented
          value={timeRange}
          onChange={setTimeRange}
          options={SESSION_TIME_RANGES.map((r) => ({ value: r.value, label: r.label }))}
        />
        <FiltersButton count={chips.length} onClick={() => setFiltersOpen(true)} />
        <Button variant="ghost" size="sm" onClick={() => setReloadNonce((n) => n + 1)} aria-label="Refresh">
          <RefreshCw /> <span className="hidden sm:inline">Refresh</span>
        </Button>
      </PageHeader>

      <ActiveFilterChips chips={chips} onClearAll={clearFilters} />

      <PanelCard>
        {status === "error" && (
          <div className="m-3 rounded-[var(--radius-lg)] border p-4 text-[13px]" style={{ color: "var(--err)", background: "var(--err-bg)" }}>
            Failed to load sessions: <span className="mono">{err}</span>
          </div>
        )}
        <TableScroll>
          <thead>
            <tr className="text-[11px] uppercase tracking-[0.07em] text-[color:var(--text-3)]">
              <th scope="col" className="text-left font-medium px-4 py-2">Session</th>
              <SortHeader label="Messages" col="messages" sort={sort} onSort={onSort} align="right" />
              <SortHeader label="Subagents" col="subagents" sort={sort} onSort={onSort} align="right" />
              <SortHeader label="Tokens" col="tokens" sort={sort} onSort={onSort} align="right" />
              <SortHeader label="Cost" col="cost" sort={sort} onSort={onSort} align="right" />
              <th scope="col" className="text-left font-medium px-4 py-2">
                <span className="inline-flex items-center gap-1">
                  Models
                  <HeaderFilter label="model" values={models} options={facets.models} onChange={setModels} emptyText="No models in window" />
                </span>
              </th>
              <th scope="col" className="text-left font-medium px-4 py-2">
                <span className="inline-flex items-center gap-1">
                  Tags
                  <HeaderFilter label="tags" values={tags} options={facets.tags} onChange={setTags} allowSelectAll={false} emptyText="No tags in window" />
                </span>
              </th>
              <SortHeader label="Started" col="started" sort={sort} onSort={onSort} />
              <SortHeader label="Last activity" col="last" sort={sort} onSort={onSort} />
            </tr>
          </thead>
          <tbody aria-busy={status === "loading"}>
            {status === "loading" && (
              <SkeletonRows
                rows={Math.min(limit, 12)}
                cols={[{ w: "12rem" }, { w: "2.5rem", align: "right" }, { w: "2.5rem", align: "right" }, { w: "3rem", align: "right" }, { w: "3rem", align: "right" }, { w: "8rem" }, { w: "6rem" }, { w: "9rem" }, { w: "4rem" }]}
              />
            )}
            {status !== "loading" && sessions.map((s) => (
              <tr
                key={s.session_id}
                role="button"
                tabIndex={0}
                aria-label={`Open session ${s.session_id}`}
                onClick={() => nav(`/sessions/${encodeURIComponent(s.session_id)}`)}
                onKeyDown={(ev) => {
                  if (ev.key === "Enter" || ev.key === " ") {
                    ev.preventDefault()
                    nav(`/sessions/${encodeURIComponent(s.session_id)}`)
                  }
                }}
                className="border-t border-[color:var(--border)] cursor-pointer hover:bg-[color:var(--hover)] focus-visible:outline-2 focus-visible:-outline-offset-2 focus-visible:outline-[color:var(--accent)]"
              >
                <td className="mono text-[12px] px-4 py-2 max-w-[14rem] truncate" title={s.session_id}>{s.session_id}</td>
                <td className="mono tnum text-[12px] text-right px-4 py-2">{fmt.compact(s.messages)}</td>
                <td className="mono tnum text-[12px] text-right px-4 py-2">
                  {s.subagents > 0
                    ? <span className="inline-flex items-center rounded-full bg-[color:var(--accent-soft,var(--hover))] px-1.5 text-[color:var(--accent)]" title={`${s.subagents} subagent${s.subagents === 1 ? "" : "s"} spawned`}>{s.subagents}</span>
                    : <span className="text-[color:var(--text-4)]"><Dash /></span>}
                </td>
                <td className="mono tnum text-[12px] text-right px-4 py-2 text-[color:var(--text-3)]">
                  {s.total_tokens > 0 ? fmt.compact(s.total_tokens) : <Dash />}
                </td>
                <td className="mono tnum text-[12px] text-right px-4 py-2 text-[color:var(--text-3)]">
                  {s.total_cost > 0 ? fmt.usd(s.total_cost) : <Dash />}
                </td>
                <td className="px-4 py-2"><ModelsCell models={s.models} /></td>
                <td className="px-4 py-2"><TagCell tags={s.tags} /></td>
                <td className="mono text-[11.5px] px-4 py-2 text-[color:var(--text-3)] whitespace-nowrap" title={fmt.fullTime(s.started_at)}>{fmt.fullTime(s.started_at)}</td>
                <td className="mono text-[11.5px] px-4 py-2 text-[color:var(--text-3)] whitespace-nowrap" title={fmt.fullTime(s.last_activity)}>{fmt.ago(s.last_activity)}</td>
              </tr>
            ))}
            {status === "ok" && sessions.length === 0 && (
              <tr><td colSpan={9} className="px-4 py-10 text-center text-[12px] text-[color:var(--text-4)]">{chips.length > 0 ? "No sessions match these filters." : "No sessions in this window."}</td></tr>
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
            <span className="text-[11px] text-[color:var(--text-4)] mono">{rangeLabel(pageIndex, limit, sessions.length, total)}</span>
            <Button variant="ghost" size="icon-xs" onClick={onPrev} disabled={pageIndex === 0} aria-label="Previous page"><ChevronLeft /></Button>
            <Button variant="ghost" size="icon-xs" onClick={onNext} disabled={!nextCursor} aria-label="Next page"><ChevronRight /></Button>
          </div>
        </div>
      </PanelCard>

      <Sheet
        open={filtersOpen}
        onClose={() => setFiltersOpen(false)}
        title={
          <>
            Filters
            {chips.length > 0 && <span className="ml-1.5 font-normal text-[color:var(--text-4)]">{chips.length} active</span>}
          </>
        }
        footer={
          <>
            <Button variant="ghost" size="sm" onClick={clearFilters} disabled={chips.length === 0}>
              Clear all
            </Button>
            <div className="flex-1" />
            <Button size="sm" onClick={() => setFiltersOpen(false)}>
              Done
            </Button>
          </>
        }
      >
        <div className="flex flex-col gap-3.5">
          <div className="grid grid-cols-2 gap-3">
            <FilterField label="Configuration (match any)">
              <MultiSelect label="Config" values={configurations} options={facets.configurations} onChange={setConfigurations} allowSelectAll className="w-full" />
            </FilterField>
            <FilterField label="Provider (match any)">
              <MultiSelect label="Provider" values={providers} options={facets.providers} onChange={setProviders} allowSelectAll className="w-full" />
            </FilterField>
            <FilterField label="Model (match any)">
              <MultiSelect label="Model" values={models} options={facets.models} onChange={setModels} allowSelectAll className="w-full" />
            </FilterField>
            <FilterField label="Protocol (match any)">
              <MultiSelect label="Protocol" values={protocols} options={facets.protocols} onChange={setProtocols} allowSelectAll className="w-full" />
            </FilterField>
          </div>
          <FilterField label="Tags (match all)">
            <MultiSelect label="Tags" values={tags} options={facets.tags} onChange={setTags} emptyText="No tags" className="w-full" />
          </FilterField>
        </div>
      </Sheet>
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
