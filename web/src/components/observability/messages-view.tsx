// Shared presentational message inspector — the recent-requests table
// (with optional session grouping) and the per-request detail modal
// (metadata, rules, resilience attempts, and lazy-loaded captured
// request/response bodies).
//
// The two consoles feed this differently: the gateway streams entries
// from its in-memory ring over SSE; the control plane pages historical
// entries out of its DB. So this module renders entries + a body fetcher
// passed in as props and owns no fetching itself. `attempts` is rendered
// only when present — the CP does not populate it yet, and the modal
// degrades gracefully to "no resilience attempts" rather than an empty
// table.

import { useCallback, useEffect, useMemo, useState } from "react"
import { createPortal } from "react-dom"
import { ChevronDown, ChevronRight, ChevronUp, X } from "lucide-react"
import { Button } from "@/components/ui/button"
import { Tabs, TabsList, TabsTrigger, TabsContent } from "@/components/ui/tabs"
import { PanelCard } from "@/components/atoms/card"
import { StatusPill } from "@/components/atoms/status-pill"
import { ProviderChip } from "@/components/atoms/provider-chip"
import { JsonViewer } from "@/components/atoms/json-viewer"
import { fmt } from "@/lib/fmt"
import { cn } from "@/lib/utils"
import type {
  AttemptHit,
  GenAIContent,
  GenAIMessage,
  GenAIMessagePart,
  GenAIToolDefinition,
  MessageBodyDetail,
  MessageEntry,
  RuleHit,
} from "@/lib/observability-types"

// MessageBodyFetcher loads the captured request/response bodies for a
// single entry. Returns null when the body store is disabled or the
// entry has rolled out of the cache; the modal renders a "not available"
// note in that case.
export type MessageBodyFetcher = (eventId: string) => Promise<MessageBodyDetail | null>

// MessagesTable renders the recent-requests table. When `grouped` is set
// the rows are bundled by (configuration, session_id); the caller owns
// the collapse set so the grouping state survives re-renders. The empty
// state is caller-supplied so each console can phrase it for its own
// data source (live tail vs. filtered history).
export function MessagesTable({
  ordered,
  grouped,
  collapsed,
  onToggleBundle,
  selectedId,
  onSelect,
  emptyLabel = "Waiting for traffic…",
}: {
  ordered: MessageEntry[]
  grouped?: boolean
  collapsed?: Set<string>
  onToggleBundle?: (key: string) => void
  selectedId: string | null
  onSelect: (eventId: string) => void
  emptyLabel?: string
}) {
  const bundles = useMemo(() => (grouped ? bundleBySession(ordered) : []), [grouped, ordered])
  return (
    <PanelCard className="overflow-hidden">
      <table className="w-full text-[12.5px]">
        <thead className="bg-[color:var(--bg-2)] text-[11px] uppercase tracking-[0.05em] text-[color:var(--text-4)]">
          <tr>
            <th className="px-3 py-2 text-left font-medium">Time</th>
            <th className="px-3 py-2 text-left font-medium">Status</th>
            <th className="px-3 py-2 text-left font-medium">Provider</th>
            <th className="px-3 py-2 text-left font-medium">Endpoint</th>
            <th className="px-3 py-2 text-left font-medium">Model</th>
            <th className="px-3 py-2 text-left font-medium">Configuration</th>
            <th className="px-3 py-2 text-right font-medium">Duration</th>
            <th className="px-3 py-2 text-right font-medium">Tokens in</th>
            <th className="px-3 py-2 text-right font-medium">Tokens out</th>
            <th className="px-3 py-2 text-left font-medium">Rules</th>
            <th className="px-3 py-2 text-left font-medium">Tags</th>
          </tr>
        </thead>
        <tbody>
          {ordered.length === 0 && (
            <tr>
              <td colSpan={11} className="px-3 py-8 text-center text-[color:var(--text-3)]">
                {emptyLabel}
              </td>
            </tr>
          )}
          {grouped
            ? bundles.map((b) => (
                <BundleGroup
                  key={b.key}
                  bundle={b}
                  collapsed={collapsed?.has(b.key) ?? false}
                  onToggle={() => onToggleBundle?.(b.key)}
                  selectedId={selectedId}
                  onSelectRow={onSelect}
                />
              ))
            : ordered.map((e) => (
                <Row key={e.event_id} entry={e} selected={e.event_id === selectedId} onClick={() => onSelect(e.event_id)} />
              ))}
        </tbody>
      </table>
    </PanelCard>
  )
}

function Row({
  entry,
  selected,
  onClick,
}: {
  entry: MessageEntry
  selected: boolean
  onClick: () => void
}) {
  return (
    <tr
      onClick={onClick}
      className={cn(
        "cursor-pointer border-t border-[color:var(--border)] hover:bg-[color:var(--hover)]",
        selected && "bg-[color:var(--hover)]",
      )}
    >
      <td className="mono px-3 py-1.5 whitespace-nowrap text-[color:var(--text-3)]">{formatClock(entry.at)}</td>
      <td className="px-3 py-1.5">
        <StatusPill code={entry.status_code} />
      </td>
      <td className="px-3 py-1.5">
        {entry.provider ? <ProviderChip name={entry.provider} /> : <span className="text-[color:var(--text-4)]">—</span>}
      </td>
      <td className="px-3 py-1.5">
        <div className="flex items-center gap-1.5">
          {entry.method && (
            <span className="mono rounded-[3px] border border-[color:var(--border)] bg-[color:var(--bg-2)] px-1 py-px text-[10px] font-medium uppercase text-[color:var(--text-3)]">
              {entry.method}
            </span>
          )}
          <span className="mono text-[color:var(--text-3)]">{entry.endpoint ?? "—"}</span>
          {entry.streaming && <span className="mono text-[10px] uppercase text-[color:var(--text-4)]">sse</span>}
        </div>
      </td>
      <td className="mono px-3 py-1.5 text-[color:var(--text-2)]">{entry.model ?? "—"}</td>
      <td className="mono px-3 py-1.5 text-[color:var(--text-2)]">{entry.configuration ?? "—"}</td>
      <td className="mono px-3 py-1.5 text-right tnum text-[color:var(--text-2)]">{entry.duration_ms} ms</td>
      <td className="mono px-3 py-1.5 text-right tnum text-[color:var(--text-2)]">
        {entry.tokens_in ? fmt.compact(entry.tokens_in) : "—"}
      </td>
      <td className="mono px-3 py-1.5 text-right tnum text-[color:var(--text-2)]">
        {entry.tokens_out ? fmt.compact(entry.tokens_out) : "—"}
      </td>
      <td className="px-3 py-1.5 text-[color:var(--text-3)]">
        {entry.rules_matched && entry.rules_matched.length > 0 ? `${entry.rules_matched.length}` : "—"}
      </td>
      <td className="px-3 py-1.5">
        {entry.tags && entry.tags.length > 0 ? (
          <div className="flex flex-wrap gap-1">
            {entry.tags.map((t) => (
              <span
                key={t}
                className="inline-flex items-center px-1.5 py-0.5 rounded-[4px] text-[10.5px] mono uppercase tracking-[0.04em] border border-[color:var(--border)] text-[color:var(--text-2)] bg-[color:var(--bg-2)]"
              >
                {t}
              </span>
            ))}
          </div>
        ) : (
          <span className="text-[color:var(--text-4)]">—</span>
        )}
      </td>
    </tr>
  )
}

type Bundle = {
  key: string
  sessionId: string
  source?: string
  configuration?: string
  entries: MessageEntry[]
  tokensIn: number
  tokensOut: number
  spanMs: number
}

// bundleBySession groups newest-first entries into (configuration,
// session_id) bundles — the same tuple the connector records bundle on —
// preserving newest-bundle-first order by first appearance. Entries with
// no session id collect under one "no session" bundle that always renders
// after every real group, so grouped sessions never sort below loose
// ungrouped traffic even when the newest entry has no session.
function bundleBySession(ordered: MessageEntry[]): Bundle[] {
  const byKey = new Map<string, Bundle>()
  const order: string[] = []
  for (const e of ordered) {
    const sid = e.session_id ?? ""
    const key = sid === "" ? " nosession" : `${e.configuration ?? ""} ${sid}`
    let b = byKey.get(key)
    if (!b) {
      b = {
        key,
        sessionId: sid,
        source: e.session_id_source,
        configuration: e.configuration,
        entries: [],
        tokensIn: 0,
        tokensOut: 0,
        spanMs: 0,
      }
      byKey.set(key, b)
      order.push(key)
    }
    b.entries.push(e)
    b.tokensIn += e.tokens_in ?? 0
    b.tokensOut += e.tokens_out ?? 0
  }
  for (const b of byKey.values()) {
    const ts = b.entries.map((e) => new Date(e.at).getTime()).filter((n) => !Number.isNaN(n))
    if (ts.length > 0) b.spanMs = Math.max(...ts) - Math.min(...ts)
  }
  // Force the no-session bundle (sessionId === "") to render after every
  // real session group. Array sort is stable, so the 0/1 partition key
  // preserves newest-first first-appearance order within each side.
  return order.map((k) => byKey.get(k)!).sort((a, b) => Number(a.sessionId === "") - Number(b.sessionId === ""))
}

// BundleGroup renders a collapsible session header row spanning the table
// plus its member request rows. The header carries the turn count, summed
// tokens, and time span — the conversation-level rollup the flat pane
// can't show.
function BundleGroup({
  bundle,
  collapsed,
  onToggle,
  selectedId,
  onSelectRow,
}: {
  bundle: Bundle
  collapsed: boolean
  onToggle: () => void
  selectedId: string | null
  onSelectRow: (eventId: string) => void
}) {
  const noSession = bundle.sessionId === ""
  return (
    <>
      <tr
        onClick={onToggle}
        className="cursor-pointer border-t border-[color:var(--border)] bg-[color:var(--bg-2)] hover:bg-[color:var(--hover)]"
      >
        <td colSpan={11} className="px-3 py-1.5">
          <div className="flex items-center gap-2 text-[12px]">
            <span className="inline-flex items-center text-[color:var(--text-4)]">
              {collapsed ? <ChevronRight size={13} /> : <ChevronDown size={13} />}
            </span>
            {noSession ? (
              <span className="italic text-[color:var(--text-4)]">no session</span>
            ) : (
              <>
                <span className="mono max-w-[22rem] truncate text-[color:var(--text-2)]" title={bundle.sessionId}>
                  {bundle.sessionId}
                </span>
                {bundle.source && (
                  <span className="mono rounded-[3px] border border-[color:var(--border)] bg-[color:var(--bg-1)] px-1 py-px text-[10px] text-[color:var(--text-3)]">
                    {bundle.source}
                  </span>
                )}
                {bundle.configuration && (
                  <span className="mono text-[10.5px] text-[color:var(--text-4)]">{bundle.configuration}</span>
                )}
              </>
            )}
            <span className="ml-auto flex items-center gap-3 text-[11px] text-[color:var(--text-3)]">
              <span className="mono">
                {bundle.entries.length} turn{bundle.entries.length === 1 ? "" : "s"}
              </span>
              {(bundle.tokensIn > 0 || bundle.tokensOut > 0) && (
                <span className="mono tnum">
                  {fmt.compact(bundle.tokensIn)}↑ / {fmt.compact(bundle.tokensOut)}↓ tok
                </span>
              )}
              {bundle.spanMs > 0 && <span className="mono tnum">{formatSpan(bundle.spanMs)}</span>}
            </span>
          </div>
        </td>
      </tr>
      {!collapsed &&
        bundle.entries.map((e) => (
          <Row key={e.event_id} entry={e} selected={e.event_id === selectedId} onClick={() => onSelectRow(e.event_id)} />
        ))}
    </>
  )
}

function formatSpan(ms: number): string {
  if (ms < 1000) return `${ms} ms`
  const s = ms / 1000
  if (s < 60) return `${s.toFixed(1)} s`
  const m = Math.floor(s / 60)
  return `${m}m ${Math.round(s % 60)}s`
}

// MessageInspectorModal renders the per-request detail in a centered
// overlay. Up / Down buttons (and ArrowUp / ArrowDown keys) scan through
// `entries` (which are newest-first); Up = newer (lower index), Down =
// older. Esc + backdrop click + X close. Bodies are lazy-loaded via the
// caller-supplied fetchBody. If the selected entry drops out of `entries`
// (e.g. rolled off the gateway ring while open), the modal closes itself.
export function MessageInspectorModal({
  entries,
  selectedId,
  onSelect,
  onClose,
  fetchBody,
}: {
  entries: MessageEntry[]
  selectedId: string
  onSelect: (id: string) => void
  onClose: () => void
  fetchBody: MessageBodyFetcher
}) {
  const index = useMemo(() => entries.findIndex((e) => e.event_id === selectedId), [entries, selectedId])
  const entry = index >= 0 ? entries[index] : null

  const goNewer = useCallback(() => {
    if (index > 0) onSelect(entries[index - 1].event_id)
  }, [entries, index, onSelect])
  const goOlder = useCallback(() => {
    if (index >= 0 && index < entries.length - 1) onSelect(entries[index + 1].event_id)
  }, [entries, index, onSelect])

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") {
        onClose()
      } else if (e.key === "ArrowUp") {
        e.preventDefault()
        goNewer()
      } else if (e.key === "ArrowDown") {
        e.preventDefault()
        goOlder()
      }
    }
    document.addEventListener("keydown", onKey)
    return () => document.removeEventListener("keydown", onKey)
  }, [goNewer, goOlder, onClose])

  // If the selected entry rolled off while the modal was open, close
  // rather than render nothing — keeps the SPA honest about why the
  // detail disappeared.
  useEffect(() => {
    if (index < 0) onClose()
  }, [index, onClose])

  if (!entry) return null

  const canNewer = index > 0
  const canOlder = index >= 0 && index < entries.length - 1

  return createPortal(
    <div
      role="dialog"
      aria-modal="true"
      aria-label="Request detail"
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/55 p-4"
      onClick={onClose}
    >
      <div
        className="relative flex w-[92vw] max-w-7xl h-[92vh] flex-col rounded-[var(--radius-lg)] border border-[color:var(--border)] bg-[color:var(--bg-1)] p-4 shadow-xl"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="mb-3 flex flex-none items-start gap-2">
          <div className="flex-1 min-w-0">
            <div className="text-[13px] font-medium">Request detail</div>
            <div className="mono text-[11.5px] text-[color:var(--text-3)] truncate">
              {entry.correlation_id ?? entry.event_id}
            </div>
            <div className="mono mt-0.5 text-[10.5px] text-[color:var(--text-4)]">
              {index + 1} of {entries.length}
            </div>
          </div>
          <div className="flex flex-none items-center gap-0.5">
            <Button variant="ghost" size="icon" onClick={goNewer} disabled={!canNewer} aria-label="Newer entry (Arrow Up)" title="Newer (↑)">
              <ChevronUp />
            </Button>
            <Button variant="ghost" size="icon" onClick={goOlder} disabled={!canOlder} aria-label="Older entry (Arrow Down)" title="Older (↓)">
              <ChevronDown />
            </Button>
            <Button variant="ghost" size="icon" onClick={onClose} aria-label="Close" title="Close (Esc)">
              <X />
            </Button>
          </div>
        </div>

        <dl className="grid flex-none grid-cols-2 gap-x-4 gap-y-1 text-[12.5px] md:grid-cols-3">
          <Field label="Status" value={String(entry.status_code)} />
          <Field label="Duration" value={`${entry.duration_ms} ms`} />
          <Field label="Streaming" value={entry.streaming ? "yes" : "no"} />
          <Field label="Provider" value={entry.provider ?? "—"} />
          <Field label="Method" value={entry.method ?? "—"} />
          <Field label="Endpoint" value={entry.endpoint ?? "—"} />
          <Field label="Model" value={entry.model ?? "—"} />
          <Field label="Configuration" value={entry.configuration ?? "—"} />
          <Field label="Session" value={entry.session_id ?? "—"} />
          <Field label="Session source" value={entry.session_id_source ?? "—"} />
          <Field label="At" value={entry.at} />
        </dl>
        {entry.upstream_error && (
          <div className="mt-3 flex-none rounded-[var(--radius)] border border-[color:var(--err)] bg-[color:var(--err-bg)] p-2 text-[12.5px]">
            <div className="mono text-[11px] uppercase text-[color:var(--err)]">upstream error</div>
            <div className="mono mt-1 text-[color:var(--text-2)]">{entry.upstream_error}</div>
          </div>
        )}
        {entry.rules_matched && entry.rules_matched.length > 0 && (
          <div className="flex-none">
            <RulesList rules={entry.rules_matched} />
          </div>
        )}
        {entry.attempts && entry.attempts.length > 0 && (
          <div className="flex-none">
            <AttemptsList policy={entry.policy_ref} attempts={entry.attempts} />
          </div>
        )}
        <BodyDetail eventId={entry.event_id} streaming={!!entry.streaming} fetchBody={fetchBody} />
      </div>
    </div>,
    document.body,
  )
}

// BodyDetail lazy-loads the captured bodies when the modal opens for a
// specific entry. Reloads when eventId changes (i.e. the operator hits
// up/down). Renders separately when the body store is disabled (fetchBody
// returns null), missing, or loading.
function BodyDetail({
  eventId,
  streaming,
  fetchBody,
}: {
  eventId: string
  streaming: boolean
  fetchBody: MessageBodyFetcher
}) {
  const [state, setState] = useState<
    | { status: "loading" }
    | { status: "missing" }
    | { status: "ok"; body: MessageBodyDetail }
    | { status: "error"; message: string }
  >({ status: "loading" })

  useEffect(() => {
    let cancelled = false
    setState({ status: "loading" })
    fetchBody(eventId)
      .then((body) => {
        if (cancelled) return
        if (!body) {
          setState({ status: "missing" })
          return
        }
        setState({ status: "ok", body })
      })
      .catch((err) => {
        if (cancelled) return
        setState({ status: "error", message: String((err as Error).message ?? err) })
      })
    return () => {
      cancelled = true
    }
  }, [eventId, fetchBody])

  if (state.status === "loading") {
    return <div className="mt-3 flex-1 text-[11.5px] text-[color:var(--text-3)]">Loading bodies…</div>
  }
  if (state.status === "missing") {
    return (
      <div className="mt-3 flex-1 rounded-[var(--radius)] border border-[color:var(--border)] bg-[color:var(--bg-2)] p-2 text-[11.5px] text-[color:var(--text-3)]">
        Bodies for this request are not available — either body capture is disabled or this event has no captured
        request/response (no connector binding, sampled out, or rolled out of the cache).
      </div>
    )
  }
  if (state.status === "error") {
    return (
      <div className="mt-3 flex-1 rounded-[var(--radius)] border border-[color:var(--err)] bg-[color:var(--err-bg)] p-2 text-[11.5px] text-[color:var(--err)]">
        {state.message}
      </div>
    )
  }
  const body = state.body
  const genAI = body.gen_ai_content
  const hasGenAI = genAI !== undefined && genAIContentHasParts(genAI)
  const hasBodies = (body.request?.length ?? 0) > 0 || (body.response?.length ?? 0) > 0
  // When only telemetry-native content is present (no spool bodies), open on
  // the GenAI tab so the operator lands on the populated view.
  const defaultTab = hasGenAI && !hasBodies ? "genai" : "request"
  // For streaming responses, the assembled JSON is the "Response" tab and
  // the unprocessed SSE bytes live on a third tab so operators can eyeball
  // both. For non-streaming, Response is the only response tab.
  return (
    <Tabs defaultValue={defaultTab} className="mt-3 flex min-h-0 flex-1 flex-col">
      <TabsList variant="line" className="flex-none">
        <TabsTrigger value="request">Request</TabsTrigger>
        <TabsTrigger value="response">Response</TabsTrigger>
        {streaming && <TabsTrigger value="raw">Raw stream</TabsTrigger>}
        {hasGenAI && <TabsTrigger value="genai">GenAI content</TabsTrigger>}
      </TabsList>
      <TabsContent value="request" className="mt-2 flex min-h-0 flex-1 flex-col gap-3">
        <BodySection
          label="Request body"
          text={body.request}
          totalBytes={body.request_total_bytes}
          truncated={body.request_truncated}
        />
        <HeadersSection label="Request headers" headers={body.request_headers} />
      </TabsContent>
      <TabsContent value="response" className="mt-2 flex min-h-0 flex-1 flex-col gap-3">
        {streaming ? (
          body.response_assembled !== undefined ? (
            <BodySection
              label="Response (assembled)"
              text={body.response_assembled}
              totalBytes={body.response_total_bytes}
              truncated={body.response_truncated}
              headerExtras={
                body.assembly_partial && (
                  <span className="mono text-[10px] uppercase text-[color:var(--warn)]">partial</span>
                )
              }
            />
          ) : (
            <NoAssemblyPlaceholder />
          )
        ) : (
          <BodySection
            label="Response body"
            text={body.response}
            totalBytes={body.response_total_bytes}
            truncated={body.response_truncated}
          />
        )}
        <HeadersSection label="Response headers" headers={body.response_headers} />
      </TabsContent>
      {streaming && (
        <TabsContent value="raw" className="mt-2 flex min-h-0 flex-1 flex-col gap-3">
          <BodySection
            label="Raw SSE bytes"
            text={body.response}
            totalBytes={body.response_total_bytes}
            truncated={body.response_truncated}
          />
        </TabsContent>
      )}
      {hasGenAI && (
        <TabsContent value="genai" className="mt-2 flex min-h-0 flex-1 flex-col gap-3 overflow-auto">
          <GenAIContentView content={genAI} />
        </TabsContent>
      )}
    </Tabs>
  )
}

// genAIContentHasParts reports whether the captured content carries any
// renderable section, so an empty {} or a content-less envelope does not light
// up an empty tab.
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
        Captured content exceeded the control plane's content cap and was dropped
        {content.original_bytes ? ` (${formatBytes(content.original_bytes)})` : ""}. The request/response body tabs
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

// GenAIMessagesSection renders a list of role-tagged turns and their parts.
function GenAIMessagesSection({ label, messages }: { label: string; messages: GenAIMessage[] }) {
  return (
    <div className="flex flex-col">
      <SectionLabel label={label} count={messages.length} />
      <div className="flex flex-col gap-2">
        {messages.map((m, i) => (
          <div
            key={i}
            className="rounded-[var(--radius)] border border-[color:var(--border)] bg-[color:var(--bg-2)] p-2"
          >
            <div className="mb-1 mono text-[10px] uppercase tracking-[0.06em] text-[color:var(--text-4)]">{m.role}</div>
            <GenAIParts parts={m.parts ?? []} />
          </div>
        ))}
      </div>
    </div>
  )
}

// GenAIPartsSection renders a bare parts array (system instructions have no
// role wrapper).
function GenAIPartsSection({ label, parts }: { label: string; parts: GenAIMessagePart[] }) {
  return (
    <div className="flex flex-col">
      <SectionLabel label={label} count={parts.length} />
      <div className="rounded-[var(--radius)] border border-[color:var(--border)] bg-[color:var(--bg-2)] p-2">
        <GenAIParts parts={parts} />
      </div>
    </div>
  )
}

// GenAIParts renders each part by kind: assembled text, a model-issued tool
// call (name + arguments), or a tool-call result.
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
  return (
    <div className="whitespace-pre-wrap break-words text-[12.5px] text-[color:var(--text-2)]">
      {part.content || <span className="italic text-[color:var(--text-4)]">empty</span>}
    </div>
  )
}

// GenAIToolDefsSection lists the tools the request advertised to the model.
function GenAIToolDefsSection({ defs }: { defs: GenAIToolDefinition[] }) {
  return (
    <div className="flex flex-col">
      <SectionLabel label="Tool definitions" count={defs.length} />
      <div className="flex flex-col gap-2">
        {defs.map((d, i) => (
          <div
            key={i}
            className="rounded-[var(--radius)] border border-[color:var(--border)] bg-[color:var(--bg-2)] p-2"
          >
            <div className="mb-1 flex items-center gap-2">
              {d.name && <span className="mono text-[12px] text-[color:var(--text-2)]">{d.name}</span>}
              {d.type && <span className="mono text-[10.5px] text-[color:var(--text-4)]">{d.type}</span>}
            </div>
            {d.description && (
              <div className="mb-1 whitespace-pre-wrap break-words text-[12px] text-[color:var(--text-3)]">
                {d.description}
              </div>
            )}
            {d.parameters !== undefined && <GenAIJson value={d.parameters} />}
          </div>
        ))}
      </div>
    </div>
  )
}

// GenAIJson pretty-prints a nested JSON value (tool arguments, results, tool
// parameter schemas) through the shared JsonViewer.
function GenAIJson({ value }: { value: unknown }) {
  const text = useMemo(() => {
    try {
      return JSON.stringify(value, null, 2)
    } catch {
      return String(value)
    }
  }, [value])
  return <JsonViewer text={text} className="min-h-0" maxHeightClassName="max-h-56" />
}

function SectionLabel({ label, count }: { label: string; count: number }) {
  return (
    <div className="mb-1 flex items-center gap-2">
      <div className="text-[10.5px] uppercase tracking-[0.06em] text-[color:var(--text-4)]">{label}</div>
      <div className="mono text-[10.5px] text-[color:var(--text-4)]">{count}</div>
    </div>
  )
}

// NoAssemblyPlaceholder fills the Response tab when streaming is on but the
// per-endpoint accumulator did not produce an assembled JSON.
function NoAssemblyPlaceholder() {
  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <div className="mb-1 flex flex-none items-center gap-2">
        <div className="text-[10.5px] uppercase tracking-[0.06em] text-[color:var(--text-4)]">Response (assembled)</div>
      </div>
      <div className="rounded-[var(--radius)] border border-[color:var(--border)] bg-[color:var(--bg-2)] p-3 text-[12.5px] text-[color:var(--text-3)]">
        <div>No accumulated response.</div>
        <div className="mt-1 text-[11.5px] text-[color:var(--text-4)]">
          The per-endpoint accumulator did not produce a reconstructed response — usually because the endpoint is not
          modelled (e.g. <span className="mono">/responses</span>). Raw SSE bytes are on the{" "}
          <span className="mono">Raw stream</span> tab.
        </div>
      </div>
    </div>
  )
}

function HeadersSection({ label, headers }: { label: string; headers?: Record<string, string[]> }) {
  const [open, setOpen] = useState(false)
  const entries = useMemo(() => {
    if (!headers) return []
    return Object.entries(headers)
      .map(([k, vs]) => [k, vs.join(", ")] as const)
      .sort(([a], [b]) => a.localeCompare(b))
  }, [headers])
  if (entries.length === 0) return null
  return (
    <div className="flex flex-none flex-col">
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        className="flex items-center gap-2 py-0.5 text-left hover:text-[color:var(--text-2)]"
      >
        <span className="inline-flex items-center text-[color:var(--text-4)]">
          {open ? <ChevronDown size={12} /> : <ChevronRight size={12} />}
        </span>
        <span className="text-[10.5px] uppercase tracking-[0.06em] text-[color:var(--text-4)]">{label}</span>
        <span className="mono text-[10.5px] text-[color:var(--text-4)]">{entries.length}</span>
      </button>
      {open && (
        <div className="mt-1 max-h-56 overflow-auto rounded-[var(--radius)] border border-[color:var(--border)] bg-[color:var(--bg-2)] p-2">
          <dl className="mono grid grid-cols-[minmax(140px,_max-content)_1fr] gap-x-3 gap-y-1 text-[11.5px] text-[color:var(--text-2)]">
            {entries.map(([k, v]) => (
              <div key={k} className="contents">
                <dt className="truncate text-[color:var(--text-3)]" title={k}>
                  {k}
                </dt>
                <dd className="break-all whitespace-pre-wrap">{v}</dd>
              </div>
            ))}
          </dl>
        </div>
      )}
    </div>
  )
}

function BodySection({
  label,
  text,
  totalBytes,
  truncated,
  headerExtras,
}: {
  label: string
  text?: string
  totalBytes: number
  truncated?: boolean
  headerExtras?: React.ReactNode
}) {
  const isEmpty = !text || text.length === 0
  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <div className="mb-1 flex flex-none items-center gap-2">
        <div className="text-[10.5px] uppercase tracking-[0.06em] text-[color:var(--text-4)]">{label}</div>
        <div className="mono text-[10.5px] text-[color:var(--text-4)]">{formatBytes(totalBytes)}</div>
        {truncated && <span className="mono text-[10px] uppercase text-[color:var(--warn)]">truncated</span>}
        <div className="ml-auto flex items-center gap-1">{headerExtras}</div>
      </div>
      {isEmpty ? (
        <div className="text-[11.5px] text-[color:var(--text-4)] italic">empty</div>
      ) : (
        <JsonViewer text={text ?? ""} className="min-h-0 flex-1" maxHeightClassName="" />
      )}
    </div>
  )
}

function formatBytes(n: number): string {
  if (n < 1024) return `${n} B`
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`
  return `${(n / (1024 * 1024)).toFixed(1)} MB`
}

function Field({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex flex-col">
      <dt className="text-[10.5px] uppercase tracking-[0.06em] text-[color:var(--text-4)]">{label}</dt>
      <dd className="mono text-[color:var(--text-2)]">{value}</dd>
    </div>
  )
}

function AttemptsList({ policy, attempts }: { policy?: string; attempts: AttemptHit[] }) {
  return (
    <div className="mt-3">
      <div className="text-[10.5px] uppercase tracking-[0.06em] text-[color:var(--text-4)] mb-1 flex items-center gap-1.5">
        <span>Resilience attempts</span>
        {policy && <span className="mono text-[color:var(--text-3)] normal-case tracking-normal">· {policy}</span>}
      </div>
      <table className="w-full text-[12px]">
        <thead>
          <tr className="text-[10px] uppercase tracking-[0.07em] text-[color:var(--text-4)]">
            <th className="text-left font-medium pr-3 pb-1">#</th>
            <th className="text-left font-medium pr-3 pb-1">Target</th>
            <th className="text-right font-medium pr-3 pb-1">Status</th>
            <th className="text-right font-medium pr-3 pb-1">Duration</th>
            <th className="text-left font-medium pb-1">Outcome</th>
          </tr>
        </thead>
        <tbody>
          {attempts.map((a, i) => (
            <tr key={i} className="border-t border-[color:var(--border)]">
              <td className="mono tnum pr-3 py-1.5 text-[color:var(--text-3)]">{i + 1}</td>
              <td className="mono py-1.5 pr-3">{a.target}</td>
              <td className="mono tnum text-right pr-3 py-1.5">
                {a.status_code ? a.status_code : <span className="text-[color:var(--text-4)]">—</span>}
              </td>
              <td className="mono tnum text-right pr-3 py-1.5">
                {a.duration_ms !== undefined ? `${a.duration_ms} ms` : <span className="text-[color:var(--text-4)]">—</span>}
              </td>
              <td className="py-1.5">
                <AttemptOutcomeChip outcome={a.outcome} />
                {a.error && (
                  <span className="ml-2 mono text-[10.5px]" style={{ color: "var(--err)" }}>
                    {a.error}
                  </span>
                )}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}

function AttemptOutcomeChip({ outcome }: { outcome: string }) {
  const colour = outcome === "success" ? "var(--ok)" : outcome === "cb_blocked" ? "var(--text-3)" : "var(--err)"
  return (
    <span className="mono text-[10.5px]" style={{ color: colour }}>
      {outcome}
    </span>
  )
}

function RulesList({ rules }: { rules: RuleHit[] }) {
  return (
    <div className="mt-3">
      <div className="text-[10.5px] uppercase tracking-[0.06em] text-[color:var(--text-4)] mb-1">Rules matched</div>
      <ul className="flex flex-col gap-1">
        {rules.map((r, i) => (
          <li
            key={`${r.rule_name}-${i}`}
            className="flex flex-wrap items-center gap-2 rounded-[var(--radius)] border border-[color:var(--border)] px-2 py-1.5 text-[12.5px]"
          >
            <span className="mono">{r.rule_name}</span>
            {r.actions_applied && r.actions_applied.length > 0 && (
              <span className="mono text-[11px] text-[color:var(--text-3)]">→ {r.actions_applied.join(", ")}</span>
            )}
            {r.terminated && (
              <span className="mono text-[10px] uppercase" style={{ color: "var(--violet)" }}>
                terminated
              </span>
            )}
            {r.error_message && (
              <span className="mono text-[11px]" style={{ color: "var(--err)" }}>
                {r.error_message}
              </span>
            )}
          </li>
        ))}
      </ul>
    </div>
  )
}

function formatClock(iso: string): string {
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return iso
  return d.toLocaleTimeString([], { hour12: false }) + "." + String(d.getMilliseconds()).padStart(3, "0")
}
