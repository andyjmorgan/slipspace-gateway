import { useEffect, useMemo, useRef, useState } from "react"
import { useNavigate } from "react-router"
import { ChevronDown, ChevronLeft, ChevronRight, ChevronUp, RefreshCw, X } from "lucide-react"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { StatusPill } from "@/components/atoms/status-pill"
import { ProviderChip } from "@/components/atoms/provider-chip"
import { JsonViewer } from "@/components/atoms/json-viewer"
import { PanelCard, PanelHead, TableScroll } from "@/components/atoms/card"
import { Segmented } from "@/components/atoms/segmented"
import { Select } from "@/components/atoms/select"
import { MultiSelect } from "@/components/atoms/multi-select"
import { fmt } from "@/lib/fmt"
import { UnauthorizedError } from "@/lib/api"
import {
  fetchFacets,
  fetchMessageBody,
  fetchMessagesPage,
  parseGenAIContent,
  type Facets,
  type GenAIContent,
  type GenAIMessage,
  type GenAIMessagePart,
  type GenAIToolDefinition,
  type MessageBodyDetail,
  type MessageEntry,
  type MessageFilters,
} from "@/lib/messages"

// PAGE_SIZES the operator can page in. Default keeps the table snappy while
// covering most browsing in one screen.
const PAGE_SIZES = [50, 100, 200] as const
const DEFAULT_PAGE_SIZE = 50

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

const EMPTY_FACETS: Facets = { providers: [], models: [], configurations: [], endpoints: [], tags: [] }

// useDebounced delays propagating a fast-changing value (the id search boxes)
// so each keystroke doesn't fire a query.
function useDebounced<T>(value: T, ms: number): T {
  const [debounced, setDebounced] = useState(value)
  useEffect(() => {
    const id = setTimeout(() => setDebounced(value), ms)
    return () => clearTimeout(id)
  }, [value, ms])
  return debounced
}

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
  const correlationId = useDebounced(corrInput, 300)
  const sessionId = useDebounced(sessInput, 300)
  const [provider, setProvider] = useState("")
  const [model, setModel] = useState("")
  const [configuration, setConfiguration] = useState("")
  const [endpoint, setEndpoint] = useState("")
  const [statusClass, setStatusClass] = useState("")
  const [tags, setTags] = useState<string[]>([])
  // Default the browse window to the last hour at 50 rows — the unbounded
  // all-time view scans far too much to be a useful landing state. Operators
  // widen via the time-range presets / row-size control as needed.
  const [timeRange, setTimeRange] = useState<TimeRange>("1h")
  const [limit, setLimit] = useState<number>(DEFAULT_PAGE_SIZE)

  // filters holds the pure (input-derived) predicates. The relative time bound
  // is resolved from timeRange at fetch time, not here — Date.now() is impure
  // and must not run during render.
  const filters = useMemo<MessageFilters>(
    () => ({ correlationId, sessionId, provider, model, configuration, endpoint, statusClass, tags }),
    [correlationId, sessionId, provider, model, configuration, endpoint, statusClass, tags],
  )

  const activeCount = useMemo(() => {
    const f = filters
    return (
      [f.correlationId, f.sessionId, f.provider, f.model, f.configuration, f.endpoint, f.statusClass].filter(Boolean)
        .length +
      (f.tags?.length ?? 0) +
      (timeRange !== "all" ? 1 : 0)
    )
  }, [filters, timeRange])

  // Data + paging state. cursorsRef holds the keyset cursor that fetches each
  // page (index 0 = "" = page one); it's navigational, not render state.
  const [facets, setFacets] = useState<Facets>(EMPTY_FACETS)
  const [entries, setEntries] = useState<MessageEntry[]>([])
  const [nextCursor, setNextCursor] = useState("")
  const [pageIndex, setPageIndex] = useState(0)
  const cursorsRef = useRef<string[]>([""])
  const [status, setStatus] = useState<"loading" | "ok" | "error">("loading")
  const [err, setErr] = useState("")
  const [selected, setSelected] = useState<number | null>(null)
  const [reloadNonce, setReloadNonce] = useState(0)

  // Any filter, time-range, or page-size change invalidates the cursor stack —
  // restart at page one. Runs before the fetch effect so it reads the reset
  // cursor.
  useEffect(() => {
    cursorsRef.current = [""]
    setPageIndex(0)
  }, [filters, timeRange, limit])

  // Fetch the current page whenever the filters, time range, page, size, or a
  // manual reload changes. pageIndex moves via Next/Prev within the same set.
  useEffect(() => {
    let cancelled = false
    setStatus("loading")
    const range = TIME_RANGES.find((r) => r.value === timeRange)
    const from = range && range.ms > 0 ? new Date(Date.now() - range.ms).toISOString() : undefined
    fetchMessagesPage({ ...filters, from }, { cursor: cursorsRef.current[pageIndex] ?? "", limit })
      .then((p) => {
        if (cancelled) return
        setEntries(p.entries)
        setNextCursor(p.nextCursor)
        setStatus("ok")
        setSelected(null)
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
  }, [filters, timeRange, pageIndex, limit, reloadNonce, nav])

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
    setCorrInput("")
    setSessInput("")
    setProvider("")
    setModel("")
    setConfiguration("")
    setEndpoint("")
    setStatusClass("")
    setTags([])
    setTimeRange("all")
  }

  return (
    <div className="flex flex-col gap-3.5">
      <div className="flex items-center gap-3">
        <div className="flex-1">
          <h1 className="text-[24px] font-semibold tracking-[-0.02em]">Messages</h1>
          <div className="text-[13px] text-[color:var(--text-3)] mt-1">Search, filter, and page through captured requests</div>
        </div>
        <Button variant="ghost" size="sm" onClick={() => setReloadNonce((n) => n + 1)} aria-label="Refresh">
          <RefreshCw /> <span className="hidden sm:inline">Refresh</span>
        </Button>
      </div>

      <PanelCard>
        <div className="flex flex-col gap-2.5 p-3 border-b border-[color:var(--border)]">
          <div className="grid grid-cols-2 sm:grid-cols-3 lg:grid-cols-6 gap-2">
            <Input placeholder="Correlation ID" value={corrInput} onChange={(e) => setCorrInput(e.target.value)} className="h-9 text-[12px] mono" />
            <Input placeholder="Session ID" value={sessInput} onChange={(e) => setSessInput(e.target.value)} className="h-9 text-[12px] mono" />
            <Select label="Provider" value={provider} options={facets.providers} onChange={setProvider} />
            <Select label="Model" value={model} options={facets.models} onChange={setModel} />
            <Select label="Config" value={configuration} options={facets.configurations} onChange={setConfiguration} />
            <Select label="Endpoint" value={endpoint} options={facets.endpoints} onChange={setEndpoint} />
          </div>
          <div className="flex flex-wrap items-center gap-2">
            <MultiSelect label="Tags" values={tags} options={facets.tags} onChange={setTags} className="w-44" />
            <Segmented
              value={statusClass}
              onChange={setStatusClass}
              options={STATUS_CLASSES.map((s) => ({ value: s.value, label: s.label }))}
            />
            <Segmented value={timeRange} onChange={setTimeRange} options={TIME_RANGES.map((r) => ({ value: r.value, label: r.label }))} />
            {activeCount > 0 && (
              <Button variant="ghost" size="sm" onClick={clearFilters}>
                <X /> Clear
              </Button>
            )}
          </div>
        </div>

        <PanelHead
          title="Requests"
          sub={`page ${pageIndex + 1} · ${entries.length} shown${activeCount > 0 ? ` · ${activeCount} filter${activeCount > 1 ? "s" : ""}` : ""}`}
        />
        {status === "error" && (
          <div className="m-3 rounded-[var(--radius-lg)] border p-4 text-[13px]" style={{ color: "var(--err)", background: "var(--err-bg)" }}>
            Failed to load messages: <span className="mono">{err}</span>
          </div>
        )}
        <TableScroll>
          <thead>
            <tr className="text-[11px] uppercase tracking-[0.07em] text-[color:var(--text-3)]">
              <th className="text-left font-medium px-4 py-2">Time</th>
              <th className="text-left font-medium px-4 py-2">Status</th>
              <th className="text-left font-medium px-4 py-2">Provider</th>
              <th className="text-left font-medium px-4 py-2">Endpoint</th>
              <th className="text-left font-medium px-4 py-2">Model</th>
              <th className="text-left font-medium px-4 py-2">Configuration</th>
              <th className="text-right font-medium px-4 py-2">Duration</th>
              <th className="text-right font-medium px-4 py-2">Tokens</th>
            </tr>
          </thead>
          <tbody>
            {entries.map((e, i) => (
              <tr
                key={e.event_id}
                onClick={() => setSelected(i)}
                className="border-t border-[color:var(--border)] cursor-pointer hover:bg-[color:var(--hover)]"
              >
                <td className="mono text-[11.5px] px-4 py-2 text-[color:var(--text-3)] whitespace-nowrap">{shortTime(e.at)}</td>
                <td className="px-4 py-2"><StatusPill code={e.status_code} /></td>
                <td className="px-4 py-2">{e.provider ? <ProviderChip name={e.provider} /> : <Dash />}</td>
                <td className="mono text-[12px] px-4 py-2">{e.endpoint || <Dash />}</td>
                <td className="mono text-[12px] px-4 py-2">{e.model || <Dash />}</td>
                <td className="mono text-[12px] px-4 py-2">{e.configuration || <Dash />}</td>
                <td className="mono tnum text-[12px] text-right px-4 py-2">{fmt.ms(e.duration_ms)}</td>
                <td className="mono tnum text-[11.5px] text-right px-4 py-2 text-[color:var(--text-3)]">
                  {(e.tokens_in ?? 0) + (e.tokens_out ?? 0) > 0 ? `${fmt.compact(e.tokens_in ?? 0)}/${fmt.compact(e.tokens_out ?? 0)}` : <Dash />}
                </td>
              </tr>
            ))}
            {status === "ok" && entries.length === 0 && (
              <tr><td colSpan={8} className="px-4 py-10 text-center text-[12px] text-[color:var(--text-4)]">{activeCount > 0 ? "No requests match these filters." : "No requests recorded yet."}</td></tr>
            )}
          </tbody>
        </TableScroll>

        <div className="flex items-center gap-3 px-4 py-2.5 border-t border-[color:var(--border)]">
          <div className="flex items-center gap-1.5 text-[11px] text-[color:var(--text-4)]">
            <span>Rows</span>
            <Segmented
              value={String(limit)}
              onChange={(v) => setLimit(Number(v))}
              options={PAGE_SIZES.map((n) => ({ value: String(n), label: String(n) }))}
            />
          </div>
          <div className="ml-auto flex items-center gap-2">
            <span className="text-[11px] text-[color:var(--text-4)] mono">page {pageIndex + 1}</span>
            <Button variant="ghost" size="icon-xs" onClick={onPrev} disabled={pageIndex === 0} aria-label="Previous page"><ChevronLeft /></Button>
            <Button variant="ghost" size="icon-xs" onClick={onNext} disabled={!nextCursor} aria-label="Next page"><ChevronRight /></Button>
          </div>
        </div>
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
    </div>
  )
}

function Dash() {
  return <span className="text-[color:var(--text-4)]">—</span>
}

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
  const [body, setBody] = useState<MessageBodyDetail | null | undefined>(undefined)

  useEffect(() => {
    let cancelled = false
    setBody(undefined)
    fetchMessageBody(entry.event_id)
      .then((b) => { if (!cancelled) setBody(b) })
      .catch(() => { if (!cancelled) setBody(null) })
    return () => { cancelled = true }
  }, [entry.event_id])

  // Keyboard nav: Esc close, arrows prev/next.
  useEffect(() => {
    const onKey = (ev: KeyboardEvent) => {
      if (ev.key === "Escape") onClose()
      else if (ev.key === "ArrowUp" && onPrev) { ev.preventDefault(); onPrev() }
      else if (ev.key === "ArrowDown" && onNext) { ev.preventDefault(); onNext() }
    }
    window.addEventListener("keydown", onKey)
    return () => window.removeEventListener("keydown", onKey)
  }, [onClose, onPrev, onNext])

  return (
    <div className="fixed inset-0 z-50 flex justify-end bg-black/40" onClick={onClose}>
      <div
        className="h-full w-full max-w-2xl overflow-auto border-l border-[color:var(--border)] bg-[color:var(--bg-1)] p-5 flex flex-col gap-4"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="flex items-center gap-2">
          <StatusPill code={entry.status_code} />
          <span className="mono text-[12px] text-[color:var(--text-3)] truncate">{entry.correlation_id || entry.event_id}</span>
          <span className="ml-auto mono text-[11px] text-[color:var(--text-4)]">{position}</span>
          <Button variant="ghost" size="icon-xs" onClick={onPrev} disabled={!onPrev} aria-label="Previous"><ChevronUp /></Button>
          <Button variant="ghost" size="icon-xs" onClick={onNext} disabled={!onNext} aria-label="Next"><ChevronDown /></Button>
          <Button variant="ghost" size="icon-xs" onClick={onClose} aria-label="Close"><X /></Button>
        </div>

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

        <BodyTabs body={body} streaming={entry.streaming} />
      </div>
    </div>
  )
}

function MetaGrid({ entry }: { entry: MessageEntry }) {
  const items: [string, React.ReactNode][] = [
    ["Provider", entry.provider || "—"],
    ["Endpoint", entry.endpoint || "—"],
    ["Model", entry.model || "—"],
    ["Method", entry.method || "—"],
    ["Configuration", entry.configuration || "—"],
    ["Duration", fmt.ms(entry.duration_ms)],
    ["Streaming", entry.streaming ? "yes" : "no"],
    ["Session", entry.session_id || "—"],
    ["At", new Date(entry.at).toLocaleString()],
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

function BodyTabs({ body, streaming }: { body: MessageBodyDetail | null | undefined; streaming?: boolean }) {
  // Parse the captured gen_ai content (a JSON string on the wire) once per
  // body so the rich GenAI view and the tab gating share one result.
  const genAI = useMemo(() => parseGenAIContent(body?.gen_ai_content), [body])

  if (body === undefined) return <div className="text-[12px] text-[color:var(--text-4)]">Loading bodies…</div>
  if (body === null) return <div className="text-[12px] text-[color:var(--text-4)]">No captured bodies (rolled off or capture disabled).</div>

  const hasGenAI = genAI !== null && genAIContentHasParts(genAI)
  // For streaming responses the Response tab shows the accumulator's
  // assembled JSON (the rollup) and the raw SSE event bytes live on a
  // separate Raw stream tab. For non-streaming there is no raw/assembled
  // split — Response is the full body. This mirrors the gateway admin
  // inspector so the two surfaces never disagree.
  return (
    <Tabs defaultValue={hasGenAI ? "genai" : "request"} className="flex flex-col gap-2">
      <TabsList>
        {hasGenAI && <TabsTrigger value="genai">Telemetry</TabsTrigger>}
        <TabsTrigger value="request">Request</TabsTrigger>
        <TabsTrigger value="response">Response</TabsTrigger>
        {streaming && <TabsTrigger value="raw">Raw stream</TabsTrigger>}
        <TabsTrigger value="headers">Headers</TabsTrigger>
      </TabsList>
      {hasGenAI && (
        <TabsContent value="genai">
          <GenAIContentView content={genAI} />
        </TabsContent>
      )}
      <TabsContent value="request">
        <BodyView label="Request body" text={body.request} bytes={body.request_total_bytes} truncated={body.request_truncated} />
      </TabsContent>
      <TabsContent value="response">
        {streaming ? (
          body.response_assembled ? (
            <BodyView
              label="Response (assembled)"
              text={body.response_assembled}
              bytes={body.response_total_bytes}
              truncated={body.response_truncated}
              partial={body.assembly_partial}
            />
          ) : (
            <NoAssemblyNote />
          )
        ) : (
          <BodyView label="Response body" text={body.response} bytes={body.response_total_bytes} truncated={body.response_truncated} />
        )}
      </TabsContent>
      {streaming && (
        <TabsContent value="raw">
          <BodyView label="Raw SSE bytes" text={body.response} bytes={body.response_total_bytes} truncated={body.response_truncated} />
        </TabsContent>
      )}
      <TabsContent value="headers">
        <Headers label="Request headers" headers={body.request_headers} />
        <div className="h-2" />
        <Headers label="Response headers" headers={body.response_headers} />
      </TabsContent>
    </Tabs>
  )
}

// NoAssemblyNote fills the Response tab when a streamed response carried no
// assembled rollup — the endpoint is outside the accumulator registry (e.g.
// /responses), the stream errored before any chunk parsed, or the gateway
// predates the rollup-on-Record change. The raw bytes are still on the Raw
// stream tab; say so rather than render a blank "No response" that reads as
// data loss.
function NoAssemblyNote() {
  return (
    <div className="flex flex-col gap-1">
      <div className="text-[10.5px] text-[color:var(--text-4)]">Response (assembled)</div>
      <div className="rounded-[var(--radius)] border border-[color:var(--border)] bg-[color:var(--bg-2)] p-3 text-[12px] text-[color:var(--text-3)]">
        No assembled response — the gateway accumulator did not reconstruct this stream. The raw
        SSE bytes are on the <span className="font-mono">Raw stream</span> tab.
      </div>
    </div>
  )
}

function BodyView({ label, text, bytes, truncated, partial }: { label: string; text?: string; bytes?: number; truncated?: boolean; partial?: boolean }) {
  if (!text) return <div className="text-[12px] text-[color:var(--text-4)]">No {label.toLowerCase()}.</div>
  return (
    <div className="flex flex-col gap-1">
      <div className="text-[10.5px] text-[color:var(--text-4)]">
        {label}{bytes ? ` · ${fmt.compact(bytes)} bytes` : ""}{truncated ? " · truncated" : ""}{partial ? " · partial" : ""}
      </div>
      <JsonViewer text={text} maxHeightClassName="max-h-[60vh]" />
    </div>
  )
}

// genAIContentHasParts reports whether the captured content carries any
// renderable section, so an empty {} or a content-less envelope does not light
// up an empty GenAI tab.
function genAIContentHasParts(c: GenAIContent): boolean {
  return (
    !!c.truncated ||
    (c.input_messages?.length ?? 0) > 0 ||
    (c.output_messages?.length ?? 0) > 0 ||
    (c.tool_definitions?.length ?? 0) > 0 ||
    (c.system_instructions?.length ?? 0) > 0
  )
}

// GenAIContentView renders the telemetry-native GenAI content the gateway put
// on the request span: the system instructions, the input turn, the model's
// response (incl. tool calls), and the tool definitions advertised to the
// model. This is the content channel — the request/response body tabs carry the
// spool-captured bytes — and either may be present without the other.
function GenAIContentView({ content }: { content: GenAIContent }) {
  if (content.truncated) {
    return (
      <div className="rounded-[var(--radius)] border border-[color:var(--warn)] bg-[color:var(--bg-2)] p-3 text-[12px] text-[color:var(--text-3)]">
        Captured content exceeded the telemetry service's content cap and was dropped
        {content.original_bytes ? ` (${fmt.compact(content.original_bytes)} bytes)` : ""}. The Request / Response tabs
        still carry the spool-captured bytes when a connector binding is configured.
      </div>
    )
  }
  return (
    <div className="flex flex-col gap-4">
      {(content.system_instructions?.length ?? 0) > 0 && (
        <GenAIPartsSection label="System instructions" parts={content.system_instructions ?? []} />
      )}
      {(content.input_messages?.length ?? 0) > 0 && (
        <GenAIMessagesSection label="Input messages" messages={content.input_messages ?? []} />
      )}
      {(content.output_messages?.length ?? 0) > 0 && (
        <GenAIMessagesSection label="Response" messages={content.output_messages ?? []} />
      )}
      {(content.tool_definitions?.length ?? 0) > 0 && (
        <GenAIToolDefsSection defs={content.tool_definitions ?? []} />
      )}
    </div>
  )
}

// GenAIMessagesSection renders a list of role-tagged turns, each turn a
// collapsible panel so individual messages can be folded away.
function GenAIMessagesSection({ label, messages }: { label: string; messages: GenAIMessage[] }) {
  return (
    <CollapsibleSection label={label} count={messages.length}>
      <div className="flex flex-col gap-2">
        {messages.map((m, i) => (
          <CollapsiblePanel
            key={i}
            header={<span className="mono text-[10px] uppercase tracking-[0.06em] text-[color:var(--text-4)]">{m.role}</span>}
            preview={messagePreview(m)}
          >
            <GenAIParts parts={m.parts ?? []} />
          </CollapsiblePanel>
        ))}
      </div>
    </CollapsibleSection>
  )
}

// GenAIPartsSection renders a bare parts array (system instructions have no
// role wrapper). Each part is its own collapsible panel so multi-part system
// prompts are visibly split rather than running together as one block.
function GenAIPartsSection({ label, parts }: { label: string; parts: GenAIMessagePart[] }) {
  return (
    <CollapsibleSection label={label} count={parts.length}>
      {parts.length === 0 ? (
        <div className="text-[11.5px] text-[color:var(--text-4)] italic">empty</div>
      ) : (
        <div className="flex flex-col gap-2">
          {parts.map((p, i) => (
            <CollapsiblePanel
              key={i}
              header={
                <>
                  <span className="mono text-[10px] uppercase tracking-[0.06em] text-[color:var(--text-4)]">{p.type || "text"}</span>
                  <span className="mono text-[10px] text-[color:var(--text-4)]">#{i + 1}</span>
                </>
              }
              preview={partPreview(p)}
            >
              <GenAIPart part={p} />
            </CollapsiblePanel>
          ))}
        </div>
      )}
    </CollapsibleSection>
  )
}

// GenAIParts renders each part by kind: assembled text, a model-issued tool
// call (name + arguments), a tool-call result, or a reasoning trace.
function GenAIParts({ parts }: { parts: GenAIMessagePart[] }) {
  if (parts.length === 0) {
    return <div className="text-[11.5px] text-[color:var(--text-4)] italic">empty</div>
  }
  return (
    <div className="flex flex-col gap-2">
      {parts.map((p, i) => (
        <GenAIPart key={i} part={p} />
      ))}
    </div>
  )
}

function GenAIPart({ part }: { part: GenAIMessagePart }) {
  if (part.type === "tool_call") {
    return (
      <div className="rounded-[var(--radius)] border border-[color:var(--border)] bg-[color:var(--bg-1)] p-2">
        <div className="mb-1 flex items-center gap-2">
          <span className="mono text-[10px] uppercase tracking-[0.06em]" style={{ color: "var(--violet)" }}>
            tool call
          </span>
          {part.name && <span className="mono text-[12px] text-[color:var(--text-2)]">{part.name}</span>}
          {part.id && <span className="mono text-[10.5px] text-[color:var(--text-4)]">{part.id}</span>}
        </div>
        {part.arguments !== undefined && <GenAIJson value={part.arguments} />}
      </div>
    )
  }
  if (part.type === "tool_call_response") {
    return (
      <div className="rounded-[var(--radius)] border border-[color:var(--border)] bg-[color:var(--bg-1)] p-2">
        <div className="mb-1 flex items-center gap-2">
          <span className="mono text-[10px] uppercase tracking-[0.06em]" style={{ color: "var(--ok)" }}>
            tool result
          </span>
          {part.id && <span className="mono text-[10.5px] text-[color:var(--text-4)]">{part.id}</span>}
        </div>
        <GenAIJson value={part.result} />
      </div>
    )
  }
  if (part.type === "reasoning") {
    return (
      <div className="flex flex-col">
        <span className="mono text-[10px] uppercase tracking-[0.06em] text-[color:var(--text-4)]">reasoning</span>
        <div className="whitespace-pre-wrap break-words text-[12.5px] text-[color:var(--text-3)]">
          {part.content || <span className="italic text-[color:var(--text-4)]">redacted</span>}
        </div>
      </div>
    )
  }
  if (part.type === "text" || part.content) {
    return (
      <div className="whitespace-pre-wrap break-words text-[12.5px] text-[color:var(--text-2)]">
        {part.content || <span className="italic text-[color:var(--text-4)]">empty</span>}
      </div>
    )
  }
  // Pass-through media block (image, audio, …): the gateway records only the
  // type, never the inline bytes, so surface the type alone.
  return (
    <div className="mono text-[11.5px] text-[color:var(--text-4)] italic">{part.type}</div>
  )
}

// GenAIToolDefsSection lists the tools the request advertised to the model.
// Each definition is a collapsible panel, collapsed by default — tool schemas
// are the bulkiest GenAI content, so the section opens as a scannable list of
// tool names and you expand only the one you want.
function GenAIToolDefsSection({ defs }: { defs: GenAIToolDefinition[] }) {
  return (
    <CollapsibleSection label="Tool definitions" count={defs.length}>
      <div className="flex flex-col gap-2">
        {defs.map((d, i) => (
          <CollapsiblePanel
            key={i}
            defaultOpen={false}
            header={
              <>
                {d.name && <span className="mono text-[12px] text-[color:var(--text-2)]">{d.name}</span>}
                {d.type && <span className="mono text-[10.5px] text-[color:var(--text-4)]">{d.type}</span>}
              </>
            }
            preview={previewText(d.description)}
          >
            {d.description && (
              <div className="mb-1 whitespace-pre-wrap break-words text-[12px] text-[color:var(--text-3)]">
                {d.description}
              </div>
            )}
            {d.parameters !== undefined && <GenAIJson value={d.parameters} />}
          </CollapsiblePanel>
        ))}
      </div>
    </CollapsibleSection>
  )
}

// GenAIJson pretty-prints a nested JSON value (tool arguments, results, tool
// parameter schemas) through the shared JsonViewer.
function GenAIJson({ value }: { value: unknown }) {
  const text = useMemo(() => {
    if (typeof value === "string") return value
    try {
      return JSON.stringify(value, null, 2)
    } catch {
      return String(value)
    }
  }, [value])
  return <JsonViewer text={text} maxHeightClassName="max-h-56" />
}

// CollapsibleSection wraps a GenAI content section in a click-to-toggle panel so
// the inspector's long turns and tool schemas can be folded away while
// navigating. Sections start expanded; the header chevron mirrors open state.
function CollapsibleSection({
  label,
  count,
  defaultOpen = true,
  children,
}: {
  label: string
  count: number
  defaultOpen?: boolean
  children: React.ReactNode
}) {
  const [open, setOpen] = useState(defaultOpen)
  return (
    <div className="flex flex-col">
      <button
        type="button"
        onClick={() => setOpen((o) => !o)}
        aria-expanded={open}
        className="mb-1 flex w-full items-center gap-1.5 text-left hover:opacity-80"
      >
        {open ? (
          <ChevronDown className="size-3 text-[color:var(--text-4)]" />
        ) : (
          <ChevronRight className="size-3 text-[color:var(--text-4)]" />
        )}
        <span className="text-[10.5px] uppercase tracking-[0.06em] text-[color:var(--text-4)]">{label}</span>
        <span className="mono text-[10.5px] text-[color:var(--text-4)]">{count}</span>
      </button>
      {open && children}
    </div>
  )
}

// CollapsiblePanel is a single bordered, click-to-toggle card for one item
// inside a section — a message turn, a system-instruction part, or a tool
// definition — so each can be folded away on its own. The header row stays
// visible; `preview` is a one-line summary shown only while collapsed so a
// folded card is still identifiable.
function CollapsiblePanel({
  header,
  preview,
  defaultOpen = true,
  children,
}: {
  header: React.ReactNode
  preview?: string
  defaultOpen?: boolean
  children: React.ReactNode
}) {
  const [open, setOpen] = useState(defaultOpen)
  return (
    <div className="rounded-[var(--radius)] border border-[color:var(--border)] bg-[color:var(--bg-2)]">
      <button
        type="button"
        onClick={() => setOpen((o) => !o)}
        aria-expanded={open}
        className="flex w-full items-center gap-2 p-2 text-left hover:bg-[color:var(--hover)]"
      >
        {open ? (
          <ChevronDown className="size-3 shrink-0 text-[color:var(--text-4)]" />
        ) : (
          <ChevronRight className="size-3 shrink-0 text-[color:var(--text-4)]" />
        )}
        <span className="flex min-w-0 items-center gap-2">{header}</span>
        {!open && preview && (
          <span className="min-w-0 flex-1 truncate text-[11.5px] text-[color:var(--text-4)]">{preview}</span>
        )}
      </button>
      {open && <div className="border-t border-[color:var(--border)] p-2">{children}</div>}
    </div>
  )
}

// previewText collapses whitespace and clips a string to a single short line,
// used for the collapsed-panel summaries.
function previewText(s: string | undefined, max = 90): string {
  if (!s) return ""
  const line = s.replace(/\s+/g, " ").trim()
  return line.length > max ? line.slice(0, max) + "…" : line
}

// partPreview summarises a single message part for a collapsed panel header.
function partPreview(part: GenAIMessagePart): string {
  if (part.type === "tool_call") return part.name ? `tool call · ${part.name}` : "tool call"
  if (part.type === "tool_call_response") return "tool result"
  if (part.content) return previewText(part.content)
  return part.type ?? ""
}

// messagePreview summarises a message turn by its first content-bearing part.
function messagePreview(m: GenAIMessage): string {
  for (const p of m.parts ?? []) {
    const t = partPreview(p)
    if (t) return t
  }
  return ""
}

function Headers({ label, headers }: { label: string; headers?: Record<string, string[]> }) {
  const keys = headers ? Object.keys(headers).sort() : []
  if (!keys.length) return <div className="text-[12px] text-[color:var(--text-4)]">No {label.toLowerCase()}.</div>
  return (
    <div className="flex flex-col gap-1">
      <div className="text-[10.5px] text-[color:var(--text-4)]">{label}</div>
      <div className="rounded-[var(--radius)] border border-[color:var(--border)] bg-[color:var(--bg-2)] p-2 text-[11.5px] mono flex flex-col gap-0.5">
        {keys.map((k) => (
          <div key={k} className="flex gap-2 break-all">
            <span className="text-[color:var(--text-3)] shrink-0">{k}:</span>
            <span className="text-[color:var(--text-2)]">{headers![k].join(", ")}</span>
          </div>
        ))}
      </div>
    </div>
  )
}

function shortTime(iso: string): string {
  const d = new Date(iso)
  return d.toLocaleTimeString(undefined, { hour12: false }) + "." + String(d.getMilliseconds()).padStart(3, "0")
}
