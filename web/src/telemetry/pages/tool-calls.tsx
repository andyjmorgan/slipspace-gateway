import { useEffect, useMemo, useState } from "react"
import { useNavigate } from "react-router"
import { RefreshCw, ChevronLeft, ChevronRight } from "lucide-react"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Tag } from "@/components/atoms/tag"
import { InspectorModal } from "@/components/atoms/inspector-modal"
import { PanelCard, TableScroll } from "@/components/atoms/card"
import { JsonViewer } from "@/components/atoms/json-viewer"
import { Sheet } from "@/components/atoms/sheet"
import { ActiveFilterChips, FilterField, FiltersButton, type Chip } from "@/components/atoms/filters"
import { Segmented } from "@/components/atoms/segmented"
import { Select } from "@/components/atoms/select"
import { PageHeader } from "@/components/atoms/page-header"
import { cn } from "@/lib/utils"
import { fmt } from "@/lib/fmt"
import { useDebounced } from "@/lib/messages-pager"
import { fetchFacets, type Facets } from "@/lib/messages"
import {
  fetchToolNames,
  useToolCallsPager,
  TOOL_CALL_PAGE_SIZES,
  TOOL_CALL_DEFAULT_PAGE_SIZE,
  type ToolCallEntry,
  type ToolCallFilters,
  type ToolCallStatus,
} from "@/lib/tool-calls"

// TIME_RANGES are the relative-window presets (resolved at fetch time, never in
// render). "all" omits the bound so the keyset scan walks full history.
const TIME_RANGES = [
  { value: "1h", label: "1h", ms: 3_600_000 },
  { value: "24h", label: "24h", ms: 86_400_000 },
  { value: "7d", label: "7d", ms: 604_800_000 },
  { value: "all", label: "All", ms: 0 },
] as const
type TimeRange = (typeof TIME_RANGES)[number]["value"]

const STATUSES = [
  { value: "", label: "Any" },
  { value: "pending", label: "Pending" },
  { value: "resolved", label: "Resolved" },
] as const

const EMPTY_FACETS: Facets = { providers: [], models: [], configurations: [], protocols: [], tags: [] }

// statusTag renders the call's lifecycle: resolved (the result is in) reads
// green, pending (awaiting the result turn) reads amber — not an error, a state.
function statusTag(status: string) {
  return status === "resolved" ? (
    <Tag variant="success">resolved</Tag>
  ) : (
    <Tag variant="warn">pending</Tag>
  )
}

// ToolCallsPage is the tool-call audit surface: find every invocation of a named
// tool (e.g. all "Skill" calls), filter by session/status/dimension + time, and
// open a row to inspect its arguments and eventual result. Keyset-paged with
// Next/Prev (the endpoint reports no total). It does not poll — a deliberate
// browse surface; Refresh re-fetches the page and the dropdown facets.
export function ToolCallsPage() {
  const nav = useNavigate()

  const [name, setName] = useState("")
  const [sessInput, setSessInput] = useState("")
  const sessionId = useDebounced(sessInput, 300)
  const [status, setStatus] = useState<ToolCallStatus>("")
  const [provider, setProvider] = useState("")
  const [configuration, setConfiguration] = useState("")
  const [protocol, setProtocol] = useState("")
  const [model, setModel] = useState("")
  const [timeRange, setTimeRange] = useState<TimeRange>("24h")
  const [limit, setLimit] = useState<number>(TOOL_CALL_DEFAULT_PAGE_SIZE)
  const [filtersOpen, setFiltersOpen] = useState(false)
  const [reloadNonce, setReloadNonce] = useState(0)

  const [toolNames, setToolNames] = useState<string[]>([])
  const [facets, setFacets] = useState<Facets>(EMPTY_FACETS)

  // The dropdowns load on mount + refresh. Tool names come from the tool-call
  // facet; provider/model/config/protocol reuse the shared message facets (the
  // same dimensions, cached server-side). Both degrade to empty on failure.
  useEffect(() => {
    let cancelled = false
    fetchToolNames()
      .then((n) => !cancelled && setToolNames(n))
      .catch(() => {})
    fetchFacets()
      .then((f) => !cancelled && setFacets(f))
      .catch(() => {})
    return () => {
      cancelled = true
    }
  }, [reloadNonce])

  // Pure predicates; the relative time bound resolves at fetch time (Date.now()
  // is impure and must not run during render).
  const filters = useMemo<ToolCallFilters>(
    () => ({ name, sessionId, status, provider, configuration, protocol, model }),
    [name, sessionId, status, provider, configuration, protocol, model],
  )

  const { entries, status: pageStatus, err, pageIndex, hasNext, onNext, onPrev } = useToolCallsPager({
    filters,
    limit,
    reloadNonce,
    resolveWindow: () => {
      const range = TIME_RANGES.find((r) => r.value === timeRange)
      return { from: range && range.ms > 0 ? new Date(Date.now() - range.ms).toISOString() : undefined }
    },
    windowKey: timeRange,
    onUnauthorized: () => nav("/login", { replace: true }),
  })

  // Selection is tagged with the page it indexes into, so a new page derives it
  // closed during render (no reset effect).
  const [selection, setSelection] = useState<{ list: ToolCallEntry[]; index: number } | null>(null)
  const selected = selection && selection.list === entries ? selection.index : null
  const setSelected = (index: number | null) =>
    setSelection(index === null ? null : { list: entries, index })

  const clearFilters = () => {
    setName("")
    setSessInput("")
    setStatus("")
    setProvider("")
    setConfiguration("")
    setProtocol("")
    setModel("")
    setTimeRange("24h")
  }

  const chips: Chip[] = [
    name && { key: "name", label: "Tool", value: name, onClear: () => setName("") },
    sessInput && { key: "sess", label: "Session", value: sessInput, onClear: () => setSessInput("") },
    status && { key: "status", label: "Status", value: status, onClear: () => setStatus("") },
    provider && { key: "provider", label: "Provider", value: provider, onClear: () => setProvider("") },
    configuration && { key: "config", label: "Config", value: configuration, onClear: () => setConfiguration("") },
    protocol && { key: "protocol", label: "Protocol", value: protocol, onClear: () => setProtocol("") },
    model && { key: "model", label: "Model", value: model, onClear: () => setModel("") },
  ].filter(Boolean) as Chip[]

  const empty = chips.length > 0 ? "No tool calls match these filters." : "No tool calls in this window."

  return (
    <div className="flex flex-col gap-3.5">
      <PageHeader title="Tool Calls" sub="Audit individual tool invocations — inputs and results, searchable by tool">
        <Select label="Tool" value={name} options={toolNames} onChange={setName} className="w-44" />
        <Segmented value={status} onChange={setStatus} options={STATUSES.map((s) => ({ value: s.value, label: s.label }))} />
        <Segmented value={timeRange} onChange={setTimeRange} options={TIME_RANGES.map((r) => ({ value: r.value, label: r.label }))} />
        <FiltersButton count={chips.length} onClick={() => setFiltersOpen(true)} />
        <Button variant="ghost" size="sm" onClick={() => setReloadNonce((n) => n + 1)} aria-label="Refresh">
          <RefreshCw /> <span className="hidden sm:inline">Refresh</span>
        </Button>
      </PageHeader>

      <ActiveFilterChips chips={chips} onClearAll={clearFilters} />

      <PanelCard>
        {pageStatus === "error" && (
          <div className="m-3 rounded-[var(--radius-lg)] border p-4 text-[13px]" style={{ color: "var(--err)", background: "var(--err-bg)" }}>
            Failed to load tool calls: <span className="mono">{err}</span>
          </div>
        )}
        <ToolCallsTable
          entries={entries}
          status={pageStatus}
          limit={limit}
          empty={empty}
          onRowClick={(i) => setSelected(i)}
          onSession={(s) => setSessInput(s)}
        />
        <PagerBar
          limit={limit}
          onLimit={setLimit}
          pageIndex={pageIndex}
          shown={entries.length}
          hasPrev={pageIndex > 0}
          hasNext={hasNext}
          onPrev={onPrev}
          onNext={onNext}
        />
      </PanelCard>

      {selected !== null && entries[selected] && (
        <ToolCallInspector
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
          <FilterField label="Tool name">
            <Select label="Tool" value={name} options={toolNames} onChange={setName} className="w-full" />
          </FilterField>
          <FilterField label="Session ID">
            <Input
              aria-label="Filter by session ID"
              placeholder="exact id"
              value={sessInput}
              onChange={(e) => setSessInput(e.target.value)}
              className="h-9 w-full text-[12px] mono"
            />
          </FilterField>
          <FilterField label="Status">
            <Segmented value={status} onChange={setStatus} options={STATUSES.map((s) => ({ value: s.value, label: s.label }))} />
          </FilterField>
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
        </div>
      </Sheet>
    </div>
  )
}

// ToolCallsTable is the keyset page table. Clicking a row opens the inspector;
// clicking the session cell sets the session filter instead (stopPropagation).
function ToolCallsTable({
  entries,
  status,
  limit,
  empty,
  onRowClick,
  onSession,
}: {
  entries: ToolCallEntry[]
  status: "loading" | "ok" | "error"
  limit: number
  empty: string
  onRowClick: (index: number) => void
  onSession: (sessionId: string) => void
}) {
  const th = "px-3 py-2 text-left text-[10px] font-medium uppercase tracking-[0.06em] text-[color:var(--text-4)]"
  const td = "px-3 py-2 align-middle border-t border-[color:var(--border)]"

  if (status === "loading" && entries.length === 0) {
    return (
      <div className="p-3 flex flex-col gap-2">
        {Array.from({ length: Math.min(limit, 8) }).map((_, i) => (
          <div key={i} className="h-8 rounded-[var(--radius)] bg-[color:var(--bg-2)] animate-pulse" />
        ))}
      </div>
    )
  }
  if (status === "ok" && entries.length === 0) {
    return <div className="p-8 text-center text-[13px] text-[color:var(--text-3)]">{empty}</div>
  }
  return (
    <TableScroll>
      <thead>
        <tr>
          <th className={th}>Tool</th>
          <th className={th}>Status</th>
          <th className={th}>Session</th>
          <th className={th}>Provider</th>
          <th className={th}>Called</th>
          <th className={cn(th, "text-right")}>Args</th>
          <th className={cn(th, "text-right")}>Result</th>
        </tr>
      </thead>
      <tbody>
        {entries.map((e, i) => (
          <tr
            key={e.tool_call_id}
            onClick={() => onRowClick(i)}
            className="cursor-pointer hover:bg-[color:var(--hover)] transition-colors"
          >
            <td className={cn(td, "font-medium")}>{e.tool_name || <span className="text-[color:var(--text-4)]">—</span>}</td>
            <td className={td}>{statusTag(e.status)}</td>
            <td className={td}>
              {e.session_id ? (
                <button
                  type="button"
                  onClick={(ev) => {
                    ev.stopPropagation()
                    onSession(e.session_id)
                  }}
                  title={`Filter by session ${e.session_id}`}
                  className="mono text-[11px] text-[color:var(--text-3)] hover:text-[color:var(--accent)] truncate max-w-[10rem]"
                >
                  {e.session_id}
                </button>
              ) : (
                <span className="text-[color:var(--text-4)]">—</span>
              )}
            </td>
            <td className={cn(td, "text-[color:var(--text-3)]")}>{e.provider || "—"}</td>
            <td className={cn(td, "text-[color:var(--text-3)] whitespace-nowrap")} title={fmt.fullTime(e.called_at ?? e.observed_at)}>
              {fmt.ago(e.called_at ?? e.observed_at)}
            </td>
            <td className={cn(td, "text-right mono text-[color:var(--text-3)] tnum")}>{fmt.compact(e.arguments_chars)}</td>
            <td className={cn(td, "text-right mono text-[color:var(--text-3)] tnum")}>
              {e.status === "resolved" ? fmt.compact(e.result_chars) : <span className="text-[color:var(--text-4)]">—</span>}
            </td>
          </tr>
        ))}
      </tbody>
    </TableScroll>
  )
}

// PagerBar is the keyset Next/Prev bar + page-size control. There is no total
// (keyset-only endpoint), so it shows the page number + shown count, gates Prev
// on the cursor stack and Next on the server's next_cursor.
function PagerBar({
  limit,
  onLimit,
  pageIndex,
  shown,
  hasPrev,
  hasNext,
  onPrev,
  onNext,
}: {
  limit: number
  onLimit: (n: number) => void
  pageIndex: number
  shown: number
  hasPrev: boolean
  hasNext: boolean
  onPrev: () => void
  onNext: () => void
}) {
  return (
    <div className="flex items-center gap-3 px-3 py-2.5 border-t border-[color:var(--border)] text-[12px] text-[color:var(--text-3)]">
      <label className="flex items-center gap-1.5">
        <span className="text-[10px] uppercase tracking-[0.06em] text-[color:var(--text-4)]">Rows</span>
        <select
          value={limit}
          onChange={(e) => onLimit(Number(e.target.value))}
          className="h-8 rounded-md border border-[color:var(--border)] bg-transparent px-1.5 text-[12px]"
          aria-label="Rows per page"
        >
          {TOOL_CALL_PAGE_SIZES.map((n) => (
            <option key={n} value={n}>
              {n}
            </option>
          ))}
        </select>
      </label>
      <div className="flex-1" />
      <span className="mono tnum">
        Page {pageIndex + 1}
        {shown > 0 ? ` · ${shown} shown` : ""}
      </span>
      <div className="flex items-center gap-1">
        <Button variant="ghost" size="icon-sm" onClick={onPrev} disabled={!hasPrev} aria-label="Previous page">
          <ChevronLeft />
        </Button>
        <Button variant="ghost" size="icon-sm" onClick={onNext} disabled={!hasNext} aria-label="Next page">
          <ChevronRight />
        </Button>
      </div>
    </div>
  )
}

// MetaRow is one label/value line in the inspector metadata grid.
function MetaRow({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="flex items-baseline gap-3 py-1">
      <span className="w-32 shrink-0 text-[11px] uppercase tracking-[0.06em] text-[color:var(--text-4)]">{label}</span>
      <span className="min-w-0 flex-1 text-[12.5px] text-[color:var(--text-2)] break-all">{children}</span>
    </div>
  )
}

// ToolCallInspector is the per-call detail modal: the lifecycle badge + metadata,
// the arguments JSON (shared JsonViewer), and the result text. Reuses the shared
// InspectorModal shell so it steps prev/next and dismisses like every other
// inspector. Exported so a fixture harness can render it in isolation.
export function ToolCallInspector({
  entry,
  position,
  onClose,
  onPrev,
  onNext,
}: {
  entry: ToolCallEntry
  position: string
  onClose: () => void
  onPrev?: () => void
  onNext?: () => void
}) {
  const argsText = entry.arguments == null ? "" : JSON.stringify(entry.arguments, null, 2)
  // result is capped at ingest; result_chars is the true size, so a longer
  // true size than the served text means the server truncated.
  const resultTruncated = entry.result != null && entry.result_chars > [...(entry.result ?? "")].length

  return (
    <InspectorModal
      onClose={onClose}
      onPrev={onPrev}
      onNext={onNext}
      prevTitle="Previous (↑)"
      nextTitle="Next (↓)"
      header={
        <div className="flex items-center gap-2 min-w-0">
          {statusTag(entry.status)}
          <span className="font-semibold truncate">{entry.tool_name || "tool call"}</span>
          <span className="mono text-[11px] text-[color:var(--text-4)] truncate">{entry.tool_call_id}</span>
          <span className="ml-auto mono text-[11px] text-[color:var(--text-4)] shrink-0">{position}</span>
        </div>
      }
    >
      <div className="flex flex-col gap-5 p-4">
        <section>
          <MetaRow label="Status">
            {entry.status}
            {entry.executor ? <span className="text-[color:var(--text-4)]"> · {entry.executor}</span> : null}
          </MetaRow>
          {entry.session_id && (
            <MetaRow label="Session">
              <span className="mono text-[11.5px]">{entry.session_id}</span>
            </MetaRow>
          )}
          {(entry.provider || entry.model || entry.configuration || entry.protocol) && (
            <MetaRow label="Route">
              {[entry.provider, entry.model, entry.configuration, entry.protocol].filter(Boolean).join(" · ")}
            </MetaRow>
          )}
          {entry.call_correlation_id && (
            <MetaRow label="Call span">
              <span className="mono text-[11.5px]">{entry.call_correlation_id}</span>
            </MetaRow>
          )}
          {entry.response_correlation_id && (
            <MetaRow label="Result span">
              <span className="mono text-[11.5px]">{entry.response_correlation_id}</span>
            </MetaRow>
          )}
          <MetaRow label="Called">{fmt.fullTime(entry.called_at)}</MetaRow>
          <MetaRow label="Responded">{entry.responded_at ? fmt.fullTime(entry.responded_at) : "—"}</MetaRow>
        </section>

        <section className="flex flex-col gap-1.5">
          <div className="flex items-center gap-2">
            <h3 className="text-[12px] font-medium uppercase tracking-[0.06em] text-[color:var(--text-3)]">Arguments</h3>
            <span className="mono text-[11px] text-[color:var(--text-4)] tnum">{fmt.num(entry.arguments_chars)} chars</span>
          </div>
          {argsText ? (
            <JsonViewer text={argsText} />
          ) : (
            <div className="text-[12.5px] text-[color:var(--text-4)]">No arguments.</div>
          )}
        </section>

        <section className="flex flex-col gap-1.5">
          <div className="flex items-center gap-2">
            <h3 className="text-[12px] font-medium uppercase tracking-[0.06em] text-[color:var(--text-3)]">Result</h3>
            {entry.result_chars > 0 && (
              <span className="mono text-[11px] text-[color:var(--text-4)] tnum">{fmt.num(entry.result_chars)} chars</span>
            )}
          </div>
          {entry.status !== "resolved" ? (
            <div className="text-[12.5px] text-[color:var(--text-4)]">Pending — the tool result has not been returned yet.</div>
          ) : entry.result ? (
            <>
              <pre className="max-h-72 overflow-auto rounded-[var(--radius)] border border-[color:var(--border)] bg-[color:var(--bg-2)] p-3 text-[12px] mono whitespace-pre-wrap break-words">
                {entry.result}
              </pre>
              {resultTruncated && (
                <div className="text-[11px] text-[color:var(--text-4)]">
                  Showing the first {fmt.num([...entry.result].length)} of {fmt.num(entry.result_chars)} characters (server cap).
                </div>
              )}
            </>
          ) : (
            <div className="text-[12.5px] text-[color:var(--text-4)]">Empty result.</div>
          )}
        </section>
      </div>
    </InspectorModal>
  )
}
