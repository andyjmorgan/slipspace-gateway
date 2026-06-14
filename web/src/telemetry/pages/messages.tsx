import { useEffect, useMemo, useState } from "react"
import { useNavigate } from "react-router"
import { Check, Copy, RefreshCw } from "lucide-react"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { StatusPill } from "@/components/atoms/status-pill"
import { InspectorModal } from "@/components/atoms/inspector-modal"
import { PanelCard } from "@/components/atoms/card"
import { Sheet } from "@/components/atoms/sheet"
import { ActiveFilterChips, FilterField, FiltersButton, type Chip } from "@/components/atoms/filters"
import { Segmented } from "@/components/atoms/segmented"
import { Select } from "@/components/atoms/select"
import { MultiSelect } from "@/components/atoms/multi-select"
import { fmt } from "@/lib/fmt"
import {
  fetchFacets,
  type Facets,
  type MessageEntry,
  type MessageFilters,
} from "@/lib/messages"
import { fetchEventSpan, type SessionSpan } from "@/lib/session-spans"
import { MESSAGE_DEFAULT_PAGE_SIZE, useDebounced, useMessagesPager } from "@/lib/messages-pager"
import { MessagesPagerBar, MessagesTableView, toggleSort, type SortState } from "../components/messages-table"
import { inputMeta, outputMeta } from "@/lib/span-view"
import {
  IOTab,
  InputPane,
  OutputPane,
  ReportPane,
  SecurityPane,
  TelemetryPane,
  Tile,
  TKV,
} from "../components/span-inspector-panes"

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

const EMPTY_FACETS: Facets = { providers: [], models: [], configurations: [], protocols: [], tags: [] }

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
  const [provider, setProvider] = useState("")
  const [model, setModel] = useState("")
  const [configuration, setConfiguration] = useState("")
  const [protocol, setProtocol] = useState("")
  const [statusClass, setStatusClass] = useState("")
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
    () => ({ correlationId, sessionId, conversationId, agentId, userId, provider, model, configuration, protocol, statusClass, tags }),
    [correlationId, sessionId, conversationId, agentId, userId, provider, model, configuration, protocol, statusClass, tags],
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

  // Facets load on mount and refresh; they back the dropdowns (cached server
  // side, so re-fetching on Refresh is cheap).
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

  const clearFilters = () => {
    setCorrInput("")
    setSessInput("")
    setConvInput("")
    setAgentInput("")
    setUserInput("")
    setProvider("")
    setModel("")
    setConfiguration("")
    setProtocol("")
    setStatusClass("")
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
    provider && { key: "provider", label: "Provider", value: provider, onClear: () => setProvider("") },
    model && { key: "model", label: "Model", value: model, onClear: () => setModel("") },
    configuration && { key: "config", label: "Config", value: configuration, onClear: () => setConfiguration("") },
    protocol && { key: "protocol", label: "Protocol", value: protocol, onClear: () => setProtocol("") },
    statusClass && { key: "status", label: "Status", value: statusClass, onClear: () => setStatusClass("") },
    ...tags.map((t) => ({ key: `tag:${t}`, label: "Tag", value: t, onClear: () => setTags(tags.filter((x) => x !== t)) })),
  ].filter(Boolean) as Chip[]

  return (
    <div className="flex flex-col gap-3.5">
      <div className="flex flex-wrap items-center gap-2 sm:gap-3">
        <div className="flex-1 min-w-0">
          <h1 className="text-[24px] font-semibold tracking-[-0.02em]">Messages</h1>
          <div className="text-[13px] text-[color:var(--text-3)] mt-1">Search, filter, and page through captured requests</div>
        </div>
        <Segmented value={timeRange} onChange={setTimeRange} options={TIME_RANGES.map((r) => ({ value: r.value, label: r.label }))} />
        <FiltersButton count={chips.length} onClick={() => setFiltersOpen(true)} />
        <Button variant="ghost" size="sm" onClick={() => setReloadNonce((n) => n + 1)} aria-label="Refresh">
          <RefreshCw /> <span className="hidden sm:inline">Refresh</span>
        </Button>
      </div>

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
            <FilterField label="Provider">
              <Select label="Provider" value={provider} options={facets.providers} onChange={setProvider} className="w-full" />
            </FilterField>
            <FilterField label="Model">
              <Select label="Model" value={model} options={facets.models} onChange={setModel} className="w-full" />
            </FilterField>
            <FilterField label="Configuration">
              <Select label="Config" value={configuration} options={facets.configurations} onChange={setConfiguration} className="w-full" />
            </FilterField>
            <FilterField label="Protocol">
              <Select label="Protocol" value={protocol} options={facets.protocols} onChange={setProtocol} className="w-full" />
            </FilterField>
          </div>
          <FilterField label="Tags (match all)">
            <MultiSelect label="Tags" values={tags} options={facets.tags} onChange={setTags} className="w-full" />
          </FilterField>
          <FilterField label="Status">
            <Segmented value={statusClass} onChange={setStatusClass} options={STATUS_CLASSES.map((s) => ({ value: s.value, label: s.label }))} />
          </FilterField>
        </div>
      </Sheet>
    </div>
  )
}

// CopyButton copies `value` to the clipboard and flips to a check for a beat.
// Used beside ids (session id) where the operator's next move is often to paste
// the value elsewhere.
function CopyButton({ value, label }: { value: string; label: string }) {
  const [copied, setCopied] = useState(false)
  const copy = () => {
    navigator.clipboard
      .writeText(value)
      .then(() => {
        setCopied(true)
        setTimeout(() => setCopied(false), 1200)
      })
      .catch(() => {
        /* clipboard blocked (insecure context / permissions) — no-op */
      })
  }
  return (
    <button
      type="button"
      onClick={copy}
      className="shrink-0 text-[color:var(--text-4)] hover:text-[color:var(--text)]"
      aria-label={copied ? `Copied ${label}` : `Copy ${label}`}
      title={copied ? "Copied" : `Copy ${label}`}
    >
      {copied ? <Check size={12} /> : <Copy size={12} />}
    </button>
  )
}

// InspectorTab mirrors the session lifecycle modal's strip: the span's own
// Output/Input panes plus the on-demand bridge tabs (Telemetry, Report).
type InspectorTab = "output" | "input" | "telemetry" | "report" | "security"

// Inspector is the per-request detail modal, rendered inside the shared
// InspectorModal shell. The message content is the SAME SessionSpan DTO
// element the session lifecycle modal renders (fetched per-event through
// /events/{id}/span), so one request looks identical on both surfaces; the
// heavier Telemetry/Report payloads load only when their tab is clicked.
function Inspector({
  entry,
  position,
  onClose,
  onPrev,
  onNext,
}: {
  entry: MessageEntry
  position: string
  onClose: () => void
  onPrev?: () => void
  onNext?: () => void
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

  // Tab choice persists across prev/next (the component survives the step).
  const [tab, setTab] = useState<InspectorTab>("output")
  // A record-only event has no Output/Input panes — fall to the bridge tabs.
  const effTab: InspectorTab = span === null && (tab === "output" || tab === "input") ? "telemetry" : tab

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
      {/* Telemetry content stacks taller than the modal, so it scrolls within
          the fixed-height shell rather than filling it like the admin view. */}
      <div className="flex min-h-0 flex-1 flex-col gap-4 overflow-auto">
        <MetaGrid entry={entry} />

        {entry.upstream_error && (
          <div className="rounded-[var(--radius)] border p-3 text-[12px] mono" style={{ color: "var(--err)", background: "var(--err-bg)" }}>{entry.upstream_error}</div>
        )}

        {(entry.tags?.length ?? 0) > 0 && (
          <div className="flex flex-wrap gap-1.5">
            {entry.tags!.map((t) => (
              <span key={t} className="inline-flex items-center px-1.5 py-0.5 rounded-[4px] text-[10.5px] mono uppercase tracking-[0.04em] border border-[color:var(--border)] text-[color:var(--text-2)] bg-[color:var(--bg-2)]">{t}</span>
            ))}
          </div>
        )}

        {(entry.rules_matched?.length ?? 0) > 0 && (
          <Section title="Rules fired">
            <div className="flex flex-col gap-1">
              {entry.rules_matched!.map((r, i) => (
                <div key={i} className="flex items-center gap-2 text-[12px]">
                  <span className="mono">{r.rule_name}</span>
                  {r.actions_applied?.map((a) => <span key={a} className="text-[10.5px] mono text-[color:var(--text-4)]">{a}</span>)}
                  {r.terminated && <span className="text-[10.5px] mono text-[color:var(--warn)]">terminated</span>}
                </div>
              ))}
            </div>
          </Section>
        )}

        {(entry.attempts?.length ?? 0) > 0 && (
          <Section title="Resilience attempts">
            <div className="flex flex-col gap-1">
              {entry.attempts!.map((a, i) => (
                <div key={i} className="grid grid-cols-[auto_1fr_auto_auto] items-center gap-2 text-[12px]">
                  <span className="mono text-[color:var(--text-4)]">#{i + 1}</span>
                  <span className="mono truncate">{a.target}</span>
                  <span className="mono tnum text-[color:var(--text-3)]">{a.status_code || a.error || "—"}</span>
                  <span className="mono text-[10.5px]" style={{ color: a.outcome === "success" ? "var(--ok)" : "var(--err)" }}>{a.outcome}</span>
                </div>
              ))}
            </div>
          </Section>
        )}

        {span === undefined ? (
          <div className="text-[12px] text-[color:var(--text-4)]">loading span…</div>
        ) : (
          <div>
            {span && <SpanTiles span={span} />}
            {span === null && (
              <div className="text-[12px] text-[color:var(--text-4)] mb-1">
                no gen_ai span captured for this request — the report may still carry the wire bodies
              </div>
            )}
            <div role="tablist" aria-label="Request detail views" className="flex border-b border-[color:var(--border)] mt-2 flex-wrap overflow-x-auto">
              {span && <IOTab on={effTab === "output"} onClick={() => setTab("output")} label="Output" meta={outputMeta(span)} />}
              {span && <IOTab on={effTab === "input"} onClick={() => setTab("input")} label="Input" meta={inputMeta(span)} />}
              <IOTab on={effTab === "telemetry"} onClick={() => setTab("telemetry")} label="Telemetry" meta="system · tools · raw" />
              <IOTab on={effTab === "report"} onClick={() => setTab("report")} label="Report" meta="request · response · stream · headers" />
              <IOTab on={effTab === "security"} onClick={() => setTab("security")} label="Security" meta="verdict · findings" />
            </div>
            {effTab === "output" && span && <OutputPane span={span} />}
            {effTab === "input" && span && <InputPane span={span} />}
            {effTab === "telemetry" && <TelemetryPane cid={cid} wanted />}
            {effTab === "report" && <ReportPane cid={cid} wanted />}
            {effTab === "security" && <SecurityPane cid={cid} wanted />}
          </div>
        )}
      </div>
    </InspectorModal>
  )
}

// SpanTiles is the message browser's variant of the lifecycle modal's
// Timing/Tokens tiles — same layout, but clocks are wall time (there is no
// session t0 to be relative to).
function SpanTiles({ span }: { span: SessionSpan }) {
  const u = span.usage
  const fresh = (u.input ?? 0) - (u.cache_read ?? 0) - (u.cache_creation ?? 0)
  const cacheShare = u.input ? `${Math.round(((u.cache_read ?? 0) / u.input) * 100)}%` : "—"
  return (
    <div className="grid sm:grid-cols-2 gap-3 mb-3">
      <Tile label="Timing" accent="var(--warn)">
        <TKV k="start" v={wallClock(span.at)} />
        <TKV k="first chunk" v={span.ttfc_ms != null ? wallClock(span.at, span.ttfc_ms) : "—"} />
        <TKV k="end" v={span.latency_ms != null ? wallClock(span.at, span.latency_ms) : "—"} />
        <TKV k="latency" v={fmt.ms(span.latency_ms)} />
        <TKV k="ttfc" v={fmt.ms(span.ttfc_ms)} />
        <TKV k="stream tail" v={span.ttfc_ms != null && span.latency_ms != null ? fmt.ms(span.latency_ms - span.ttfc_ms) : "—"} />
      </Tile>
      <Tile label="Tokens" accent="var(--ok)">
        <TKV k="in" v={fmt.compact(u.input)} />
        <TKV k="out" v={fmt.compact(u.output)} />
        <TKV k="fresh in" v={fmt.compact(fresh)} />
        <TKV k="cache read" v={fmt.compact(u.cache_read)} />
        <TKV k="cache write" v={fmt.compact(u.cache_creation)} />
        <TKV k="cache share" v={cacheShare} />
      </Tile>
    </div>
  )
}

// wallClock formats an RFC3339 instant (plus an optional offset) as local
// HH:MM:SS.mmm — the tile clock format, absolute rather than session-relative.
function wallClock(iso: string, plusMs = 0): string {
  const d = new Date(new Date(iso).getTime() + plusMs)
  if (Number.isNaN(d.getTime())) return "—"
  const p = (n: number, w = 2) => String(n).padStart(w, "0")
  return `${p(d.getHours())}:${p(d.getMinutes())}:${p(d.getSeconds())}.${p(d.getMilliseconds(), 3)}`
}

function MetaGrid({ entry }: { entry: MessageEntry }) {
  const nav = useNavigate()
  // The session id links to that session's view — the common pivot from "this
  // one request" to "the whole conversation". The route switch unmounts this
  // modal on its own (or re-keys it when already on the sessions page).
  const sessionNode = entry.session_id ? (
    <span className="flex items-center gap-1 min-w-0">
      <button
        type="button"
        onClick={() => nav(`/sessions/${encodeURIComponent(entry.session_id!)}`)}
        className="truncate min-w-0 text-left text-[color:var(--accent)] hover:underline"
        title={`View session ${entry.session_id}`}
      >
        {entry.session_id}
      </button>
      <CopyButton value={entry.session_id} label="session id" />
    </span>
  ) : (
    "—"
  )
  // Conversation/thread id: the subagent thread when active, else the session.
  // Shown copyable, titled with its provenance header; a subagent thread also
  // shows its parent so the operator can walk up toward the session bundle.
  const threadNode = entry.conversation_id ? (
    <span className="flex items-center gap-1 min-w-0">
      <span className="truncate min-w-0" title={entry.conversation_id_source ? `from ${entry.conversation_id_source}` : entry.conversation_id}>
        {entry.conversation_id}
      </span>
      {entry.parent_conversation_id ? (
        <span className="text-[10.5px] text-[color:var(--text-4)] shrink-0" title={`parent ${entry.parent_conversation_id}`}>
          ↳ parent
        </span>
      ) : null}
      <CopyButton value={entry.conversation_id} label="conversation id" />
    </span>
  ) : (
    "—"
  )
  // Agent id is a leaf dimension (no per-agent page yet): show it copyable,
  // titled with its provenance header so the operator knows which header it
  // came from. Reserved for genuinely named agents — a subagent thread rides
  // the Thread cell, not here.
  const agentNode = entry.agent_id ? (
    <span className="flex items-center gap-1 min-w-0">
      <span className="truncate min-w-0" title={entry.agent_id_source ? `from ${entry.agent_id_source}` : entry.agent_id}>
        {entry.agent_id}
      </span>
      <CopyButton value={entry.agent_id} label="agent id" />
    </span>
  ) : (
    "—"
  )
  // User id is a leaf dimension (no per-user page yet): show it copyable, titled
  // with its provenance header so the operator knows which header it came from.
  const userNode = entry.user_id ? (
    <span className="flex items-center gap-1 min-w-0">
      <span className="truncate min-w-0" title={entry.user_id_source ? `from ${entry.user_id_source}` : entry.user_id}>
        {entry.user_id}
      </span>
      <CopyButton value={entry.user_id} label="user id" />
    </span>
  ) : (
    "—"
  )
  const items: [string, React.ReactNode][] = [
    ["Provider", entry.provider || "—"],
    ["Protocol", entry.protocol || "—"],
    ["Model", entry.model || "—"],
    ["Method", entry.method || "—"],
    ["Configuration", entry.configuration || "—"],
    ["Duration", fmt.ms(entry.duration_ms)],
    ["Streaming", entry.streaming ? "yes" : "no"],
    ["Session", sessionNode],
    ["Thread", threadNode],
    ["Agent", agentNode],
    ["User", userNode],
    ["At", fmt.fullTime(entry.at)],
  ]
  return (
    <div className="grid grid-cols-2 sm:grid-cols-3 gap-x-4 gap-y-2 rounded-[var(--radius)] border border-[color:var(--border)] bg-[color:var(--bg-2)] p-3">
      {items.map(([k, v]) => (
        <div key={k} className="min-w-0">
          <div className="text-[10.5px] uppercase tracking-[0.06em] text-[color:var(--text-4)]">{k}</div>
          <div className="mono text-[12px] truncate" title={typeof v === "string" ? v : undefined}>{v}</div>
        </div>
      ))}
    </div>
  )
}

function Section({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <div className="flex flex-col gap-1.5">
      <div className="text-[11px] uppercase tracking-[0.06em] text-[color:var(--text-3)]">{title}</div>
      {children}
    </div>
  )
}
