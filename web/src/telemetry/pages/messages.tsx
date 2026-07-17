import { useEffect, useMemo, useState } from "react"
import { useNavigate } from "react-router"
import { RefreshCw } from "lucide-react"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { StatusPill } from "@/components/atoms/status-pill"
import { InspectorModal } from "@/components/atoms/inspector-modal"
import { PanelCard } from "@/components/atoms/card"
import { Sheet } from "@/components/atoms/sheet"
import { ActiveFilterChips, FilterField, FiltersButton, type Chip } from "@/components/atoms/filters"
import { Segmented } from "@/components/atoms/segmented"
import { MultiSelect } from "@/components/atoms/multi-select"
import { PageHeader } from "@/components/atoms/page-header"
import {
  fetchFacets,
  type Facets,
  type MessageEntry,
  type MessageFilters,
} from "@/lib/messages"
import { fetchEventSpan, type SessionSpan } from "@/lib/session-spans"
import { MESSAGE_DEFAULT_PAGE_SIZE, useDebounced, useMessagesPager } from "@/lib/messages-pager"
import { MessagesPagerBar, MessagesTableView, toggleSort, type SortState } from "../components/messages-table"
import { InspectorBody, type InspectorTab } from "../components/inspector-body"
import { loadVerdict } from "@/lib/verdict"

// TIME_RANGES are the relative-window presets. "all" omits the bound so the
// keyset scan can walk the full history.
const TIME_RANGES = [
  { value: "1h", label: "1h", ms: 3_600_000 },
  { value: "24h", label: "24h", ms: 86_400_000 },
  { value: "7d", label: "7d", ms: 604_800_000 },
  { value: "all", label: "All", ms: 0 },
] as const
type TimeRange = (typeof TIME_RANGES)[number]["value"]

const STATUS_CLASSES = [
  { value: "", label: "Any" },
  { value: "2xx", label: "2xx" },
  { value: "4xx", label: "4xx" },
  { value: "5xx", label: "5xx" },
] as const

const EMPTY_FACETS: Facets = { providers: [], models: [], configurations: [], protocols: [], status_codes: [], tags: [] }

// MessagesPage is the request browser: a filter bar (two exact id boxes, four
// facet dropdowns, a tags AND multi-select, status-class + time-range presets)
// over a keyset-paged table with Next/Prev. Unlike the dashboard's live feed it
// does not poll — it's a deliberate browse surface; Refresh re-fetches the
// current page and the dropdown facets. Rows open the per-request inspector.
export function MessagesPage() {
  const nav = useNavigate()

  // Filter inputs. Dropdowns/segments apply immediately; the id boxes debounce.
  const [corrInput, setCorrInput] = useState("")
  const [sessInput, setSessInput] = useState("")
  const [convInput, setConvInput] = useState("")
  const [agentInput, setAgentInput] = useState("")
  const [userInput, setUserInput] = useState("")
  const correlationId = useDebounced(corrInput, 300)
  const sessionId = useDebounced(sessInput, 300)
  const conversationId = useDebounced(convInput, 300)
  const agentId = useDebounced(agentInput, 300)
  const userId = useDebounced(userInput, 300)
  // Categorical dimensions are multi-select: many values OR within the
  // dimension. The header funnel menus and the filter sheet edit these same
  // arrays — one state, two surfaces, always in sync.
  const [providers, setProviders] = useState<string[]>([])
  const [models, setModels] = useState<string[]>([])
  const [configurations, setConfigurations] = useState<string[]>([])
  const [protocols, setProtocols] = useState<string[]>([])
  const [statusClass, setStatusClass] = useState("")
  const [statusCodes, setStatusCodes] = useState<string[]>([])
  const [tags, setTags] = useState<string[]>([])
  // Default the browse window to the last hour at 50 rows — the unbounded
  // all-time view scans far too much to be a useful landing state. Operators
  // widen via the time-range presets / row-size control as needed.
  const [timeRange, setTimeRange] = useState<TimeRange>("1h")
  const [limit, setLimit] = useState<number>(MESSAGE_DEFAULT_PAGE_SIZE)
  // sort defaults to time-descending — the server's natural order, set
  // explicitly so the Time header reads as the active sort (otherwise clicking
  // it produces no visible change and looks broken). Clicking a header sets it;
  // a sort change resets paging (the pager folds sort into its keyset identity).
  const [sort, setSort] = useState<SortState>({ key: "time", desc: true })
  // The filter controls live in a right-side slide-over so the toolbar stays
  // clean; applied filters surface as removable chips. filtersOpen toggles it.
  const [filtersOpen, setFiltersOpen] = useState(false)

  // filters holds the pure (input-derived) predicates. The relative time bound
  // is resolved from timeRange at fetch time, not here — Date.now() is impure
  // and must not run during render.
  const filters = useMemo<MessageFilters>(
    () => ({ correlationId, sessionId, conversationId, agentId, userId, providers, models, configurations, protocols, statusClass, statusCodes, tags }),
    [correlationId, sessionId, conversationId, agentId, userId, providers, models, configurations, protocols, statusClass, statusCodes, tags],
  )

  // Data + paging state. The shared pager hook owns the keyset cursor stack;
  // the time bound resolves from the relative preset at fetch time (Date.now()
  // is impure and must not run during render), so it rides resolveWindow with
  // timeRange as the invalidation key.
  const [facets, setFacets] = useState<Facets>(EMPTY_FACETS)
  const [reloadNonce, setReloadNonce] = useState(0)
  const { entries, status, err, pageIndex, hasNext, total, onNext, onPrev } = useMessagesPager({
    filters,
    limit,
    sort,
    reloadNonce,
    resolveWindow: () => {
      const range = TIME_RANGES.find((r) => r.value === timeRange)
      return { from: range && range.ms > 0 ? new Date(Date.now() - range.ms).toISOString() : undefined }
    },
    windowKey: timeRange,
    onUnauthorized: () => nav("/login", { replace: true }),
  })

  // The inspector selection is tagged with the page it points into, so a new
  // page derives it closed during render (no reset effect).
  const [selection, setSelection] = useState<{ list: MessageEntry[]; index: number } | null>(null)
  const selected = selection && selection.list === entries ? selection.index : null
  const setSelected = (index: number | null) =>
    setSelection(index === null ? null : { list: entries, index })

  // Facets load on mount, refresh, and every time-range change; they back the
  // header funnel menus + sheet dropdowns (cached server side per window, so
  // re-fetching on Refresh is cheap). Window-scoped so the menus offer only
  // values present in the range the table is showing; the relative bound is
  // resolved here at fetch time (Date.now() must not run during render).
  useEffect(() => {
    let cancelled = false
    const range = TIME_RANGES.find((r) => r.value === timeRange)
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

  const clearFilters = () => {
    setCorrInput("")
    setSessInput("")
    setConvInput("")
    setAgentInput("")
    setUserInput("")
    setProviders([])
    setModels([])
    setConfigurations([])
    setProtocols([])
    setStatusClass("")
    setStatusCodes([])
    setTags([])
    // Clear restores the default landing window (1h), not the unbounded all-time
    // scan — see the timeRange default above. Clearing to "all" would silently
    // widen to the heaviest query the page deliberately avoids on load.
    setTimeRange("1h")
  }

  // sheetFilterChips are the applied predicates shown as removable toolbar pills
  // (time range stays inline, so it isn't a chip). Their count badges the
  // Filters button — the bare time window doesn't read as a "filter".
  const chips: Chip[] = [
    corrInput && { key: "corr", label: "Correlation", value: corrInput, onClear: () => setCorrInput("") },
    sessInput && { key: "sess", label: "Session", value: sessInput, onClear: () => setSessInput("") },
    convInput && { key: "conv", label: "Thread", value: convInput, onClear: () => setConvInput("") },
    agentInput && { key: "agent", label: "Agent", value: agentInput, onClear: () => setAgentInput("") },
    userInput && { key: "user", label: "User", value: userInput, onClear: () => setUserInput("") },
    ...providers.map((v) => ({ key: `provider:${v}`, label: "Provider", value: v, onClear: () => setProviders(providers.filter((x) => x !== v)) })),
    ...models.map((v) => ({ key: `model:${v}`, label: "Model", value: v, onClear: () => setModels(models.filter((x) => x !== v)) })),
    ...configurations.map((v) => ({ key: `config:${v}`, label: "Config", value: v, onClear: () => setConfigurations(configurations.filter((x) => x !== v)) })),
    ...protocols.map((v) => ({ key: `protocol:${v}`, label: "Protocol", value: v, onClear: () => setProtocols(protocols.filter((x) => x !== v)) })),
    statusClass && { key: "status", label: "Status", value: statusClass, onClear: () => setStatusClass("") },
    ...statusCodes.map((v) => ({ key: `code:${v}`, label: "Code", value: v, onClear: () => setStatusCodes(statusCodes.filter((x) => x !== v)) })),
    ...tags.map((t) => ({ key: `tag:${t}`, label: "Tag", value: t, onClear: () => setTags(tags.filter((x) => x !== t)) })),
  ].filter(Boolean) as Chip[]

  // tableFilters binds the categorical columns' header funnel menus to the
  // same state the sheet edits. "All" is offered on the OR dimensions only —
  // tags is AND containment, where selecting every tag matches ~nothing.
  const tableFilters = {
    status: { values: statusCodes, options: facets.status_codes.map(String), onChange: setStatusCodes, allowSelectAll: true, emptyText: "No statuses in window" },
    provider: { values: providers, options: facets.providers, onChange: setProviders, allowSelectAll: true, emptyText: "No providers in window" },
    protocol: { values: protocols, options: facets.protocols, onChange: setProtocols, allowSelectAll: true, emptyText: "No protocols in window" },
    model: { values: models, options: facets.models, onChange: setModels, allowSelectAll: true, emptyText: "No models in window" },
    configuration: { values: configurations, options: facets.configurations, onChange: setConfigurations, allowSelectAll: true, emptyText: "No configurations in window" },
    tags: { values: tags, options: facets.tags, onChange: setTags, allowSelectAll: false, emptyText: "No tags in window" },
  }

  return (
    <div className="flex flex-col gap-3.5">
      <PageHeader title="Messages" sub="Search, filter, and page through captured requests">
        <Segmented value={timeRange} onChange={setTimeRange} options={TIME_RANGES.map((r) => ({ value: r.value, label: r.label }))} />
        <FiltersButton count={chips.length} onClick={() => setFiltersOpen(true)} />
        <Button variant="ghost" size="sm" onClick={() => setReloadNonce((n) => n + 1)} aria-label="Refresh">
          <RefreshCw /> <span className="hidden sm:inline">Refresh</span>
        </Button>
      </PageHeader>

      <ActiveFilterChips chips={chips} onClearAll={clearFilters} />

      <PanelCard>
        {status === "error" && (
          <div className="m-3 rounded-[var(--radius-lg)] border p-4 text-[13px]" style={{ color: "var(--err)", background: "var(--err-bg)" }}>
            Failed to load messages: <span className="mono">{err}</span>
          </div>
        )}
        <MessagesTableView
          entries={entries}
          status={status}
          limit={limit}
          emptyText={chips.length > 0 ? "No requests match these filters." : "No requests in this window."}
          onRowClick={(_e, i) => setSelected(i)}
          sort={sort}
          onSort={(key) => setSort((s) => toggleSort(s, key))}
          filters={tableFilters}
        />

        <MessagesPagerBar
          limit={limit}
          onLimit={setLimit}
          pageIndex={pageIndex}
          shown={entries.length}
          total={total}
          hasPrev={pageIndex > 0}
          hasNext={hasNext}
          onPrev={onPrev}
          onNext={onNext}
        />
      </PanelCard>

      {selected !== null && entries[selected] && (
        <Inspector
          entry={entries[selected]}
          position={`${selected + 1} / ${entries.length}`}
          onClose={() => setSelected(null)}
          onPrev={selected > 0 ? () => setSelected(selected - 1) : undefined}
          onNext={selected < entries.length - 1 ? () => setSelected(selected + 1) : undefined}
        />
      )}

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
          <FilterField label="Correlation ID">
            <Input aria-label="Filter by correlation ID" placeholder="exact id" value={corrInput} onChange={(e) => setCorrInput(e.target.value)} className="h-9 w-full text-[12px] mono" />
          </FilterField>
          <FilterField label="Session ID">
            <Input aria-label="Filter by session ID" placeholder="exact id" value={sessInput} onChange={(e) => setSessInput(e.target.value)} className="h-9 w-full text-[12px] mono" />
          </FilterField>
          <FilterField label="Thread / Conversation ID">
            <Input aria-label="Filter by thread or conversation ID" placeholder="exact id" value={convInput} onChange={(e) => setConvInput(e.target.value)} className="h-9 w-full text-[12px] mono" />
          </FilterField>
          <div className="grid grid-cols-2 gap-3">
            <FilterField label="Agent ID">
              <Input aria-label="Filter by agent ID" placeholder="exact id" value={agentInput} onChange={(e) => setAgentInput(e.target.value)} className="h-9 w-full text-[12px] mono" />
            </FilterField>
            <FilterField label="User ID">
              <Input aria-label="Filter by user ID" placeholder="exact id" value={userInput} onChange={(e) => setUserInput(e.target.value)} className="h-9 w-full text-[12px] mono" />
            </FilterField>
          </div>
          <div className="grid grid-cols-2 gap-3">
            <FilterField label="Provider (match any)">
              <MultiSelect label="Provider" values={providers} options={facets.providers} onChange={setProviders} allowSelectAll className="w-full" />
            </FilterField>
            <FilterField label="Model (match any)">
              <MultiSelect label="Model" values={models} options={facets.models} onChange={setModels} allowSelectAll className="w-full" />
            </FilterField>
            <FilterField label="Configuration (match any)">
              <MultiSelect label="Config" values={configurations} options={facets.configurations} onChange={setConfigurations} allowSelectAll className="w-full" />
            </FilterField>
            <FilterField label="Protocol (match any)">
              <MultiSelect label="Protocol" values={protocols} options={facets.protocols} onChange={setProtocols} allowSelectAll className="w-full" />
            </FilterField>
          </div>
          <FilterField label="Tags (match all)">
            <MultiSelect label="Tags" values={tags} options={facets.tags} onChange={setTags} emptyText="No tags" className="w-full" />
          </FilterField>
          <FilterField label="Status">
            <Segmented value={statusClass} onChange={setStatusClass} options={STATUS_CLASSES.map((s) => ({ value: s.value, label: s.label }))} />
          </FilterField>
          <FilterField label="Status code (match any)">
            <MultiSelect label="Code" values={statusCodes} options={facets.status_codes.map(String)} onChange={setStatusCodes} allowSelectAll className="w-full" />
          </FilterField>
        </div>
      </Sheet>
    </div>
  )
}

// Inspector is the per-request detail modal, rendered inside the shared
// InspectorModal shell. The message content is the SAME SessionSpan DTO
// element the session lifecycle modal renders (fetched per-event through
// /events/{id}/span), so one request looks identical on both surfaces; the
// heavier Telemetry/Report payloads load only when their tab is clicked.
//
// Exported so the Security page can reuse it verbatim (it opens this same modal
// on the Security tab via initialTab) — one inspector, never a near-duplicate.
export function Inspector({
  entry,
  position,
  onClose,
  onPrev,
  onNext,
  initialTab = "conversation",
}: {
  entry: MessageEntry
  position: string
  onClose: () => void
  onPrev?: () => void
  onNext?: () => void
  // initialTab seeds which detail tab opens first. The Security page passes
  // "security" so a flagged row lands straight on its verdict + findings.
  initialTab?: InspectorTab
}) {
  const cid = entry.correlation_id || entry.event_id
  // Keyed by cid so stepping prev/next derives "loading" during render (no
  // reset-state effect). undefined = loading; null = no gen_ai span captured
  // (record-only event).
  const [spanSt, setSpanSt] = useState<{ cid: string; v: SessionSpan | null } | null>(null)
  const span = spanSt && spanSt.cid === cid ? spanSt.v : undefined
  useEffect(() => {
    let cancelled = false
    fetchEventSpan(cid)
      .then((s) => { if (!cancelled) setSpanSt({ cid, v: s }) })
      .catch(() => { if (!cancelled) setSpanSt({ cid, v: null }) })
    return () => { cancelled = true }
  }, [cid])

  // Eagerly load the verdict (cached + shared with SecurityPane) so the Security
  // tab can carry a finding-count badge before it's opened. Same keyed-state
  // pattern as the span fetch — state is only set in the async callback.
  const [findingSt, setFindingSt] = useState<{ cid: string; count: number } | null>(null)
  useEffect(() => {
    let cancelled = false
    loadVerdict(cid)
      .then((v) => { if (!cancelled) setFindingSt({ cid, count: v?.findings.length ?? 0 }) })
      .catch(() => { if (!cancelled) setFindingSt({ cid, count: 0 }) })
    return () => { cancelled = true }
  }, [cid])
  const findingCount = findingSt && findingSt.cid === cid ? findingSt.count : 0

  // Tab choice persists across prev/next (the component survives the step).
  const [tab, setTab] = useState<InspectorTab>(initialTab)

  return (
    <InspectorModal
      onClose={onClose}
      onPrev={onPrev}
      onNext={onNext}
      header={
        <div className="flex items-center gap-2">
          <StatusPill code={entry.status_code} />
          <span className="mono text-[12px] text-[color:var(--text-3)] truncate">{cid}</span>
          <span className="ml-auto mono text-[11px] text-[color:var(--text-4)]">{position}</span>
        </div>
      }
    >
      <InspectorBody entry={entry} span={span} cid={cid} findingCount={findingCount} tab={tab} onTab={setTab} />
    </InspectorModal>
  )
}

