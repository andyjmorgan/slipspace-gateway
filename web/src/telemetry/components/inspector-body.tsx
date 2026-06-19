// InspectorBody is the shared per-request detail layout used by every surface
// that opens a request: the Messages browser, the Security findings list, and
// the Sessions lifecycle view. It owns the "trace detail" layout — three flex-
// none tiers (context line + KPI strip + collapsible Details) over a lens stage
// that fills the rest of the height with its own scroll, so the payload, not the
// metadata, gets the modal's real estate.
//
// What differs per surface is supplied as props, not forked: the Sessions view
// passes `joinCall`/`joinResp` (its cross-span tool-call ledger joins) and a
// session-relative `clock`; Messages/Security omit them and get wall-clock
// timing. The owning modal shell (header/footer/nav) stays with each caller.
import { useState, type ReactNode } from "react"
import { useNavigate } from "react-router"
import { Check, ChevronDown, ChevronRight, Copy } from "lucide-react"
import { fmt } from "@/lib/fmt"
import { cn } from "@/lib/utils"
import type { MessageEntry } from "@/lib/messages"
import type { InputPart, OutputPart, SessionSpan } from "@/lib/session-spans"
import {
  type CallJoin,
  type RespJoin,
  IOTab,
  InputPane,
  OutputPane,
  ReportPane,
  SecurityPane,
  TelemetryPane,
  Tile,
  TKV,
} from "./span-inspector-panes"

// InspectorTab is the lens strip: the merged Conversation (input → output) plus
// the on-demand bridge lenses (Telemetry, Security, Raw wire) that fetch their
// heavier payloads only when first opened.
export type InspectorTab = "conversation" | "telemetry" | "security" | "raw"

// SpanClock formats a span clock value given an offset in ms from the span
// start. Messages uses wall-clock; the Sessions view passes a session-relative
// formatter (timed from the session t0).
export type SpanClock = (offsetMs?: number) => string

export function InspectorBody({
  entry,
  span,
  cid,
  findingCount,
  tab,
  onTab,
  joinCall,
  joinResp,
  clock,
}: {
  entry: MessageEntry
  // span: undefined = loading, null = record-only (no gen_ai span captured).
  span: SessionSpan | null | undefined
  cid: string
  findingCount: number
  tab: InspectorTab
  onTab: (t: InspectorTab) => void
  // joinCall/joinResp annotate the Conversation panes with cross-span tool-call
  // timing — supplied by the Sessions view, omitted elsewhere.
  joinCall?: (part: OutputPart) => CallJoin
  joinResp?: (part: InputPart) => RespJoin
  clock?: SpanClock
}) {
  // A record-only event has no normalized conversation — fall to the raw wire
  // lens, which still carries the bodies from the Report feed.
  const effTab: InspectorTab = span === null && tab === "conversation" ? "raw" : tab
  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <ContextLine entry={entry} />
      <KpiStrip entry={entry} span={span} findingCount={findingCount} />

      {entry.upstream_error && (
        <div className="flex-none mt-3 rounded-[var(--radius)] border p-3 text-[12px] mono" style={{ color: "var(--err)", background: "var(--err-bg)" }}>
          {entry.upstream_error}
        </div>
      )}

      <InspectorDetails entry={entry} span={span} clock={clock} />

      {span === undefined ? (
        <div className="pt-4 text-[12px] text-[color:var(--text-4)]">loading span…</div>
      ) : (
        <>
          {span === null && (
            <div className="flex-none pt-3 text-[12px] text-[color:var(--text-4)]">
              no gen_ai span captured for this request — the raw wire bodies may still be in the report
            </div>
          )}
          <div role="tablist" aria-label="Request lenses" className="flex-none mt-3 flex border-b border-[color:var(--border)] flex-wrap overflow-x-auto">
            {span && <IOTab on={effTab === "conversation"} onClick={() => onTab("conversation")} label="Conversation" />}
            <IOTab on={effTab === "telemetry"} onClick={() => onTab("telemetry")} label="Telemetry" />
            <IOTab on={effTab === "security"} onClick={() => onTab("security")} label="Security" badge={findingCount} />
            <IOTab on={effTab === "raw"} onClick={() => onTab("raw")} label="Raw wire" />
          </div>
          {/* Keyed by cid so stepping prev/next to another request resets the
              stage scroll (and re-reads the per-cid pane caches) cleanly. */}
          <div key={cid} className="min-h-0 flex-1 overflow-auto pt-3">
            {effTab === "conversation" && span && <Conversation span={span} joinCall={joinCall} joinResp={joinResp} />}
            {effTab === "telemetry" && <TelemetryPane cid={cid} wanted />}
            {effTab === "security" && <SecurityPane cid={cid} wanted />}
            {effTab === "raw" && <ReportPane cid={cid} wanted />}
          </div>
        </>
      )}
    </div>
  )
}

// ContextLine is tier 1: the routing facts in one wrapping line, below the
// modal header's status + correlation id. The provider/model pair is the
// emphasized middle.
function ContextLine({ entry }: { entry: MessageEntry }) {
  const bits = [entry.method, `${entry.provider}/${entry.model}`, entry.protocol, entry.configuration].filter(Boolean) as string[]
  return (
    <div className="flex flex-none flex-wrap items-center gap-x-2.5 gap-y-1 text-[12.5px]">
      {bits.map((b, i) => (
        <span key={i} className="flex items-center gap-2.5">
          {i > 0 && <span className="text-[color:var(--text-4)]">·</span>}
          <span className={cn("mono", i === 1 ? "text-[color:var(--text)] font-medium" : "text-[color:var(--text-2)]")}>{b}</span>
        </span>
      ))}
      <span className="ml-auto mono text-[11px] text-[color:var(--text-4)]">{fmt.fullTime(entry.at)}</span>
    </div>
  )
}

// KpiStrip is tier 2: the numbers an operator scans first, as label/value stat
// cells. Reflows 6→3→2 columns down to mobile. The Policy + Security cells
// summarize the counts the Details disclosure and Security lens expand.
function KpiStrip({ entry, span, findingCount }: { entry: MessageEntry; span: SessionSpan | null | undefined; findingCount: number }) {
  const u = span?.usage
  const cacheShare = u?.input ? `${Math.round(((u.cache_read ?? 0) / u.input) * 100)}%` : "—"
  const rules = entry.rules_matched?.length ?? 0
  const tries = entry.attempts?.length ?? 0
  const policy = rules || tries ? `${rules}r · ${tries}t` : "none"
  const security = findingCount > 0 ? `${findingCount} finding${findingCount === 1 ? "" : "s"}` : "clean"
  const cells: { label: string; value: string; tone?: "ok" | "warn" | "err" }[] = [
    { label: "Duration", value: fmt.ms(entry.duration_ms) },
    { label: "Tokens", value: u ? `${fmt.compact(u.input)} → ${fmt.compact(u.output)}` : "—" },
    { label: "Cache", value: cacheShare },
    { label: "TTFC", value: span?.ttfc_ms != null ? fmt.ms(span.ttfc_ms) : "—" },
    { label: "Policy", value: policy, tone: tries > rules ? "warn" : undefined },
    { label: "Security", value: security, tone: findingCount > 0 ? "err" : "ok" },
  ]
  return (
    <div className="mt-3 grid grid-cols-2 gap-px overflow-hidden rounded-[var(--radius)] border border-[color:var(--border)] bg-[color:var(--border)] sm:grid-cols-3 lg:grid-cols-6">
      {cells.map((c) => (
        <div key={c.label} className="flex flex-col gap-1 bg-[color:var(--bg-2)] px-3 py-2.5">
          <div className="text-[10px] font-medium uppercase tracking-[0.08em] text-[color:var(--text-4)]">{c.label}</div>
          <div
            className="mono tnum text-[15px] font-semibold leading-none truncate"
            style={c.tone ? { color: `var(--${c.tone})` } : undefined}
            title={c.value}
          >
            {c.value}
          </div>
        </div>
      ))}
    </div>
  )
}

// InspectorDetails is the long-tail metadata as a collapsed-by-default
// disclosure between the KPI strip and the lens tabs — collapsed it's a single
// row, so the payload keeps full height; expanded it reveals the full identity
// grid, timing/tokens breakdown, tags, rules, and resilience attempts.
function InspectorDetails({ entry, span, clock }: { entry: MessageEntry; span: SessionSpan | null | undefined; clock?: SpanClock }) {
  const [open, setOpen] = useState(false)
  const rules = entry.rules_matched?.length ?? 0
  const tries = entry.attempts?.length ?? 0
  const hint = [rules ? `${rules} rule${rules === 1 ? "" : "s"}` : null, tries ? `${tries} attempt${tries === 1 ? "" : "s"}` : null]
    .filter(Boolean)
    .join(" · ")
  return (
    <div className="flex-none mt-3 border-t border-[color:var(--border)] pt-2.5">
      <button
        type="button"
        onClick={() => setOpen((o) => !o)}
        aria-expanded={open}
        className="flex w-full items-center gap-1.5 text-[11px] font-medium uppercase tracking-[0.06em] text-[color:var(--text-3)] hover:text-[color:var(--text)] transition-colors"
      >
        {open ? <ChevronDown size={14} /> : <ChevronRight size={14} />}
        Details
        <span className="normal-case tracking-normal text-[color:var(--text-4)]">identity{hint ? ` · ${hint}` : ""}</span>
      </button>
      {open && (
        <div className="flex flex-col gap-4 pt-3">
          <MetaGrid entry={entry} />
          {span && <SpanTiles span={span} clock={clock} />}
          {(entry.tags?.length ?? 0) > 0 && (
            <div className="flex flex-wrap gap-1.5">
              {entry.tags!.map((t) => (
                <span key={t} className="inline-flex items-center px-1.5 py-0.5 rounded-[4px] text-[10.5px] mono uppercase tracking-[0.04em] border border-[color:var(--border)] text-[color:var(--text-2)] bg-[color:var(--bg-2)]">{t}</span>
              ))}
            </div>
          )}
          {rules > 0 && (
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
          {tries > 0 && (
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
        </div>
      )}
    </div>
  )
}

// Conversation merges the two halves of the I/O — the trailing request input on
// top, the assistant response below — into one readable stage, each rendered by
// the existing part renderers. This collapses the old Output/Input tab split
// into the thing operators actually reason about.
function Conversation({
  span,
  joinCall,
  joinResp,
}: {
  span: SessionSpan
  joinCall?: (part: OutputPart) => CallJoin
  joinResp?: (part: InputPart) => RespJoin
}) {
  return (
    <div className="flex flex-col gap-5">
      <section className="flex flex-col gap-2">
        <h3 className="text-[11px] font-semibold uppercase tracking-[0.08em] text-[color:var(--text-3)]">Request</h3>
        <InputPane span={span} joinResp={joinResp} />
      </section>
      <div className="border-t border-dashed border-[color:var(--border)]" />
      <section className="flex flex-col gap-2">
        <h3 className="text-[11px] font-semibold uppercase tracking-[0.08em] text-[color:var(--text-3)]">Response</h3>
        <OutputPane span={span} joinCall={joinCall} />
      </section>
    </div>
  )
}

// SpanTiles is the Timing / Tokens key-value tile pair. The clock defaults to
// wall time; the Sessions view passes a session-relative formatter.
function SpanTiles({ span, clock }: { span: SessionSpan; clock?: SpanClock }) {
  const u = span.usage
  const fresh = (u.input ?? 0) - (u.cache_read ?? 0) - (u.cache_creation ?? 0)
  const cacheShare = u.input ? `${Math.round(((u.cache_read ?? 0) / u.input) * 100)}%` : "—"
  const at: SpanClock = clock ?? ((ms = 0) => wallClock(span.at, ms))
  return (
    <div className="grid sm:grid-cols-2 gap-3">
      <Tile label="Timing" accent="var(--warn)">
        <TKV k="start" v={at(0)} />
        <TKV k="first chunk" v={span.ttfc_ms != null ? at(span.ttfc_ms) : "—"} />
        <TKV k="end" v={span.latency_ms != null ? at(span.latency_ms) : "—"} />
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
  const items: [string, ReactNode][] = [
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

function Section({ title, children }: { title: string; children: ReactNode }) {
  return (
    <div className="flex flex-col gap-1.5">
      <div className="text-[11px] uppercase tracking-[0.06em] text-[color:var(--text-3)]">{title}</div>
      {children}
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
