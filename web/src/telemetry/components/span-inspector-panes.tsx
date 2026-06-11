// Shared span-inspector building blocks: the per-message card design the
// session lifecycle modal established, factored out so the message browser's
// inspector renders requests identically (one design per message everywhere).
//
// Two layers live here:
//
//  1. The card primitives + the Output/Input panes — pure functions of a
//     SessionSpan DTO element (renderer-contract purity: tool args stay
//     opaque vendor JSON, *_chars drive the truncation notices). Session
//     context (the tool-call ledger join, session-relative clocks) is the
//     lifecycle page's privilege: it arrives through optional join callbacks
//     with the strings pre-formatted, so this module never needs the view
//     model. Without callbacks the cards simply omit the session
//     annotations — the message browser has no session to join against.
//
//  2. The on-demand bridge panes — Telemetry (system prompt, tool
//     definitions, the raw OTel span) and Report (the spool-captured wire
//     bodies + headers) — fed by GET /api/v1/messages/{cid}/body through
//     useMessageBody. The fetch fires ONLY when the operator first opens the
//     tab (`wanted`), never on modal open: these payloads are the heavy
//     ones, and most inspections never leave Output/Input.

import { useMemo, useState } from "react"
import { Check, Copy } from "lucide-react"
import { cn } from "@/lib/utils"
import { fmt } from "@/lib/fmt"
import {
  parseGenAIContent,
  type GenAIMessagePart,
  type GenAIToolDefinition,
} from "@/lib/messages"
import { id8, useMessageBody } from "@/lib/span-view"
import type { InputPart, OutputPart, SessionSpan } from "@/lib/session-spans"

// ─────────────────────────────────────────────────────────────────────────
// Primitives
// ─────────────────────────────────────────────────────────────────────────

// CardCopy is the top-right copy affordance every content card carries: one
// click puts the card's FULL text (not the visible, possibly-capped well) on
// the clipboard. stopPropagation keeps it from toggling <details> summaries.
// Exported for the inspector headers, which copy ids with the same control.
export function CardCopy({ text, label = "card text" }: { text: string; label?: string }) {
  const [copied, setCopied] = useState(false)
  const copy = (e: React.MouseEvent) => {
    e.preventDefault()
    e.stopPropagation()
    navigator.clipboard
      .writeText(text)
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
      className="shrink-0 ml-1.5 text-[color:var(--text-4)] hover:text-[color:var(--text)]"
      aria-label={copied ? `Copied ${label}` : `Copy ${label}`}
      title={copied ? "Copied" : `Copy ${label}`}
    >
      {copied ? <Check size={13} style={{ color: "var(--ok)" }} /> : <Copy size={13} />}
    </button>
  )
}

// MCard is the modal's content card: header strip + body. Every card
// collapses — the header is a <details> summary with a rotating chevron —
// and content cards default open. copyText renders the standard top-right
// copy button for the card's FULL content; it works while collapsed.
export function MCard({
  head,
  copyText,
  defaultOpen = true,
  children,
}: {
  head: React.ReactNode
  copyText?: string
  defaultOpen?: boolean
  children: React.ReactNode
}) {
  return (
    <details
      className="rounded-[var(--radius-lg)] border border-[color:var(--border)] bg-[color:var(--bg)] my-2 overflow-hidden group"
      open={defaultOpen}
    >
      <summary className="flex items-center gap-2 px-3.5 py-2 bg-[color:var(--bg-2)] text-[12.5px] text-[color:var(--text-3)] flex-wrap min-h-[35px] cursor-pointer list-none [&::-webkit-details-marker]:hidden">
        <span className="text-[9px] text-[color:var(--text-4)] transition-transform group-open:rotate-90">▶</span>
        {head}
        {copyText ? <CardCopy text={copyText} /> : null}
      </summary>
      <div className="px-3.5 py-3 border-t border-[color:var(--border)]">{children}</div>
    </details>
  )
}

// DetailsCard is the collapsible sibling of MCard (native <details>), used
// for cards whose bodies are bulky (tool responses, system prompts, raw
// span, wire bodies). The marker arrow rotates on open; copyText renders the
// same top-right copy button, which works while collapsed.
export function DetailsCard({
  head,
  copyText,
  defaultOpen = false,
  markerColor = "var(--warn)",
  children,
}: {
  head: React.ReactNode
  copyText?: string
  defaultOpen?: boolean
  markerColor?: string
  children: React.ReactNode
}) {
  return (
    <details
      className="rounded-[var(--radius-lg)] border border-[color:var(--border)] bg-[color:var(--bg)] my-2 overflow-hidden group"
      open={defaultOpen}
    >
      <summary className="flex items-center gap-2 px-3.5 py-2 bg-[color:var(--bg-2)] text-[12.5px] text-[color:var(--text-3)] flex-wrap min-h-[35px] cursor-pointer list-none [&::-webkit-details-marker]:hidden">
        <span className="transition-transform group-open:-rotate-90" style={{ color: markerColor }}>
          ↩
        </span>
        {head}
        {copyText ? <CardCopy text={copyText} /> : null}
      </summary>
      <div className="px-3.5 py-3 border-t border-[color:var(--border)]">{children}</div>
    </details>
  )
}

// ToolChip mirrors the reference's tool-name pill in token colors.
export function ToolChip({ name }: { name?: string }) {
  return (
    <span
      className="inline-flex items-center rounded-full px-2.5 py-px text-[11.5px] font-semibold"
      style={{ background: "var(--accent-bg)", color: "var(--accent)", border: "1px solid color-mix(in oklab, var(--accent) 35%, transparent)" }}
    >
      {name ?? "(unnamed)"}
    </span>
  )
}

// PreBlock is the modal's raw-text well.
export function PreBlock({ text }: { text: string }) {
  return (
    <pre className="whitespace-pre-wrap break-words rounded-[var(--radius)] border border-[color:var(--border)] bg-[color:var(--bg)] px-3 py-2.5 mono text-[12px] leading-[1.55] text-[color:var(--text-2)] max-h-[46vh] overflow-y-auto m-0">
      {text}
    </pre>
  )
}

// IOTab is one tab of the inspector's tab strip.
export function IOTab({ on, onClick, label, meta }: { on: boolean; onClick: () => void; label: string; meta: string }) {
  return (
    <button
      type="button"
      onClick={onClick}
      className={cn(
        "px-4 py-2 -mb-px text-[12.5px] font-semibold border-b-2 transition-colors",
        on
          ? "text-[color:var(--text)] border-[color:var(--accent)]"
          : "text-[color:var(--text-3)] border-transparent hover:text-[color:var(--text-2)]",
      )}
    >
      {label} <span className="font-normal text-[11px] text-[color:var(--text-4)] ml-1">{meta}</span>
    </button>
  )
}

// Tile / TKV are the key-value stat tiles (Timing / Tokens).
export function Tile({ label, accent, children }: { label: string; accent: string; children: React.ReactNode }) {
  return (
    <div className="rounded-[var(--radius-lg)] border border-[color:var(--border)] bg-[color:var(--bg-2)] px-4 pt-3 pb-3.5">
      <div className="flex items-center gap-1.5 text-[10.5px] font-semibold uppercase tracking-[0.1em] text-[color:var(--text-3)]">
        <span className="inline-block size-1.5 rounded-full" style={{ background: accent }} />
        {label}
      </div>
      <div className="grid grid-cols-3 gap-x-4 gap-y-2.5 mt-2.5">{children}</div>
    </div>
  )
}

export function TKV({ k, v }: { k: string; v: string }) {
  return (
    <div className="min-w-0">
      <div className="text-[10px] font-medium uppercase tracking-[0.08em] text-[color:var(--text-4)] whitespace-nowrap">{k}</div>
      <div className="mono tnum text-[14px] font-semibold mt-0.5 whitespace-nowrap">{v}</div>
    </div>
  )
}

// ─────────────────────────────────────────────────────────────────────────
// Tool argument rendering (renderer-contract rule 7: opaque vendor JSON)
// ─────────────────────────────────────────────────────────────────────────

// ArgsView is the rule-7 default path for tool arguments: opaque vendor JSON,
// possibly server-capped. Parse if possible and render a generic key/value
// grid (no field-picking, every shape handled); fall back to the raw string
// visibly when the payload isn't valid JSON.
export function ArgsView({ raw }: { raw?: string }) {
  if (!raw) return <div className="text-[12px] text-[color:var(--text-4)]">(no arguments captured)</div>
  let parsed: unknown
  let ok = true
  try {
    parsed = JSON.parse(raw)
  } catch {
    ok = false
  }
  if (!ok) {
    return (
      <>
        <div className="text-[11px] mb-1.5" style={{ color: "var(--warn)" }}>
          ⚠ raw — not valid JSON
        </div>
        <PreBlock text={raw} />
      </>
    )
  }
  if (parsed && typeof parsed === "object" && !Array.isArray(parsed)) {
    const entries = Object.entries(parsed as Record<string, unknown>)
    if (!entries.length) {
      return <div className="text-[12px] text-[color:var(--text-4)]">{"{}"} — empty arguments</div>
    }
    return (
      <div className="grid grid-cols-[minmax(96px,max-content)_1fr] gap-x-4 gap-y-2 items-start">
        {entries.map(([k, v]) => (
          <ArgRow key={k} k={k} v={v} />
        ))}
      </div>
    )
  }
  return <ArgValue v={parsed} />
}

function ArgRow({ k, v }: { k: string; v: unknown }) {
  return (
    <>
      <div className="mono text-[11.5px] font-semibold text-[color:var(--text-3)] pt-0.5 break-all">{k}</div>
      <div className="min-w-0">
        <ArgValue v={v} />
      </div>
    </>
  )
}

// ArgValue renders any JSON value generically: short strings inline, long or
// multiline strings in a well, primitives in mono, nested structures
// pretty-printed — never interpreting the payload.
function ArgValue({ v }: { v: unknown }) {
  if (typeof v === "string") {
    if (v.includes("\n") || v.length > 160) return <PreBlock text={v} />
    return <span className="text-[12.5px] whitespace-pre-wrap break-words">{v}</span>
  }
  if (v === null || typeof v !== "object") {
    return (
      <span className="mono text-[12px]" style={{ color: "var(--accent)" }}>
        {JSON.stringify(v)}
      </span>
    )
  }
  return <PreBlock text={JSON.stringify(v, null, 2)} />
}

// ─────────────────────────────────────────────────────────────────────────
// Output / Input panes (pure functions of one SessionSpan element)
// ─────────────────────────────────────────────────────────────────────────

// CallJoin is the session-side annotation for a tool_call card, pre-formatted
// by the caller (clocks are session-relative, so only the lifecycle page can
// produce them). null = the caller looked and found no joined result (say so
// honestly); undefined = no session context at all (omit the annotation).
export type CallJoin = {
  resultClock: string
  execS?: number | null
  resultChars?: number | null
  isError?: boolean
} | null

// RespJoin annotates a tool_call_response card with the call that produced
// it. Same null/undefined convention as CallJoin.
export type RespJoin = {
  name?: string
  calledClock: string
  execS?: number | null
} | null

// ToolCallCard renders one tool_call the span emitted: server-executed when a
// same-span response exists (rule 8), otherwise annotated via the caller's
// ledger join, with the raw-args display below (rule 7 default path).
export function ToolCallCard({
  part,
  sameSpanResp,
  join,
}: {
  part: OutputPart
  sameSpanResp?: OutputPart
  join?: CallJoin
}) {
  let meta: React.ReactNode = null
  if (sameSpanResp) {
    meta = (
      <>
        <span
          className="inline-flex items-center rounded-full px-2.5 py-px text-[10.5px] font-semibold"
          style={{ background: "var(--violet-bg)", color: "var(--violet)", border: "1px solid color-mix(in oklab, var(--violet) 30%, transparent)" }}
        >
          server-executed · response in this span
        </span>
        <span className="ml-auto mono tnum text-[11.5px] whitespace-nowrap pl-2.5">
          <b className="text-[color:var(--text-2)]">{fmt.compact(sameSpanResp.chars ?? null)}</b> chars back
        </span>
      </>
    )
  } else if (join) {
    meta = (
      <span className="ml-auto mono tnum text-[11.5px] whitespace-nowrap pl-2.5">
        result at {join.resultClock} · exec{" "}
        <b className="text-[color:var(--text-2)]">{join.execS != null ? `${join.execS.toFixed(1)}s` : "–"}</b> ·{" "}
        <b className="text-[color:var(--text-2)]">{fmt.compact(join.resultChars ?? null)}</b> chars
        {join.isError && (
          <b className="ml-1" style={{ color: "var(--err)" }}>
            · ERROR
          </b>
        )}
      </span>
    )
  } else if (join === null) {
    meta = <span className="ml-auto text-[11.5px] text-[color:var(--text-4)] pl-2.5">no joined result in this session</span>
  }
  const argsLen = (part.args ?? "").length
  const truncated = (part.args_chars ?? 0) > argsLen
  return (
    <MCard
      head={
        <>
          <ToolChip name={part.name} />
          <span className="mono text-[11px] text-[color:var(--text-4)]" title={part.id}>{id8(part.id)}</span>
          {meta ?? <span className="ml-auto" />}
        </>
      }
      copyText={part.args || undefined}
    >
      {truncated && (
        <div className="text-[11px] mb-1.5" style={{ color: "var(--warn)" }}>
          arguments truncated — showing first {fmt.compact(argsLen)} of {fmt.compact(part.args_chars ?? 0)} chars (server cap)
        </div>
      )}
      <ArgsView raw={part.args} />
    </MCard>
  )
}

// RespCard renders one tool_call_response from the span's input delta as a
// collapsible card, annotated by the caller's ledger join when session
// context exists. join === null says honestly that no call matched anywhere
// in the captured spans; join === undefined (no session context) shows the
// plain header without guessing.
export function RespCard({
  part,
  defaultOpen,
  join,
}: {
  part: InputPart
  defaultOpen: boolean
  join?: RespJoin
}) {
  const textLen = (part.text ?? "").length
  const truncated = (part.chars ?? 0) > textLen && !!part.text
  return (
    <DetailsCard
      defaultOpen={defaultOpen}
      copyText={part.text || undefined}
      head={
        <>
          {join ? (
            <>
              <span>response to</span>
              <ToolChip name={join.name} />
              <span className="mono text-[11px] text-[color:var(--text-4)]" title={part.id}>{id8(part.id)}</span>
              <span className="text-[color:var(--text-4)]">
                called at {join.calledClock}
                {join.execS != null && (
                  <b className="text-[color:var(--text-2)]"> (exec {join.execS.toFixed(1)}s)</b>
                )}
              </span>
            </>
          ) : (
            <>
              <span>tool response</span>
              <span className="mono text-[11px] text-[color:var(--text-4)]" title={part.id}>{id8(part.id)}</span>
              {join === null && (
                <span className="text-[color:var(--text-4)]">no matching tool_call in captured spans — possible capture gap</span>
              )}
            </>
          )}
          <span className="ml-auto mono tnum text-[11.5px] whitespace-nowrap pl-2.5">
            <b className="text-[color:var(--text-2)]">{fmt.compact(part.chars ?? null)}</b> chars
            {part.is_error && (
              <b className="ml-1" style={{ color: "var(--err)" }}>
                · ERROR
              </b>
            )}
          </span>
        </>
      }
    >
      {truncated && (
        <div className="text-[11px] mb-1.5" style={{ color: "var(--warn)" }}>
          showing first {fmt.compact(textLen)} of {fmt.compact(part.chars ?? 0)} chars (server cap)
        </div>
      )}
      <PreBlock text={part.text || "(no text captured)"} />
    </DetailsCard>
  )
}

// OutputPane renders the span's assistant output: the text card plus one
// card per tool_call (server-executed calls pair with their same-span
// response). joinCall, when given, annotates client tool calls from the
// session's ledger.
export function OutputPane({
  span,
  joinCall,
}: {
  span: SessionSpan
  joinCall?: (part: OutputPart) => CallJoin
}) {
  const toolCalls = (span.output_parts ?? []).filter((p) => p.type === "tool_call")
  const srvResp = new Map<string, OutputPart>()
  for (const p of span.output_parts ?? []) {
    if (p.type === "tool_call_response" && p.id) srvResp.set(p.id, p)
  }
  const outChars = span.output_text_chars ?? (span.output_text ?? "").length
  return (
    <div className="pt-3">
      {span.output_text ? (
        <MCard
          copyText={span.output_text}
          head={
            <>
              <span style={{ color: "var(--accent)" }}>●</span>
              <span>Text</span>
              <span className="ml-auto mono tnum text-[11.5px] text-[color:var(--text-3)] pl-2.5 whitespace-nowrap">
                <b className="text-[color:var(--text-2)]">{fmt.compact(outChars)}</b> chars
                {outChars > span.output_text.length ? ` · showing first ${fmt.compact(span.output_text.length)} (server cap)` : ""}
              </span>
            </>
          }
        >
          <PreBlock text={span.output_text} />
        </MCard>
      ) : (
        <div className="text-[12.5px] text-[color:var(--text-4)]">(no text parts)</div>
      )}
      {toolCalls.map((p, i) => {
        const sameSpan = p.id ? srvResp.get(p.id) : undefined
        return (
          <ToolCallCard
            key={p.id ?? i}
            part={p}
            sameSpanResp={sameSpan}
            join={sameSpan ? undefined : joinCall?.(p)}
          />
        )
      })}
    </div>
  )
}

// InputPane renders the span's trailing input delta: tool responses first
// (collapsible), then the human text. joinResp, when given, annotates each
// response with the call that produced it.
export function InputPane({
  span,
  joinResp,
}: {
  span: SessionSpan
  joinResp?: (part: InputPart) => RespJoin
}) {
  const inParts = span.input_parts ?? []
  const resps = inParts.filter((p) => p.type === "tool_call_response")
  const texts = inParts.filter((p) => p.type === "text")
  return (
    <div className="pt-3">
      {inParts.length === 0 ? (
        span.input_text ? (
          <MCard
            copyText={span.input_text}
            head={
              <>
                <span style={{ color: "var(--accent)" }}>●</span>
                <span>Text</span>
                <span className="ml-auto mono tnum text-[11.5px] text-[color:var(--text-3)] pl-2.5 whitespace-nowrap">
                  <b className="text-[color:var(--text-2)]">{fmt.compact(span.input_text_chars ?? span.input_text.length)}</b> chars
                </span>
              </>
            }
          >
            <PreBlock text={span.input_text} />
          </MCard>
        ) : (
          <div className="text-[12.5px] text-[color:var(--text-4)]">
            (empty — no new input parts in this request)
          </div>
        )
      ) : (
        <>
          {resps.map((p, i) => (
            <RespCard key={p.id ?? i} part={p} defaultOpen={resps.length === 1} join={joinResp?.(p)} />
          ))}
          {texts.map((p, i) => (
            <MCard
              key={`txt-${i}`}
              copyText={span.input_text || undefined}
              head={
                <>
                  <span style={{ color: "var(--accent)" }}>●</span>
                  <span>Text</span>
                  <span className="ml-auto mono tnum text-[11.5px] text-[color:var(--text-3)] pl-2.5 whitespace-nowrap">
                    <b className="text-[color:var(--text-2)]">{fmt.compact(p.chars ?? null)}</b> chars
                  </span>
                </>
              }
            >
              <PreBlock text={span.input_text ?? "(empty)"} />
            </MCard>
          ))}
        </>
      )}
      {(span.input_text_chars ?? 0) > (span.input_text ?? "").length && (
        <div className="text-[11px] text-[color:var(--text-4)] mt-1.5">
          showing first {fmt.compact((span.input_text ?? "").length)} of {fmt.compact(span.input_text_chars ?? 0)} chars (server cap)
        </div>
      )}
    </div>
  )
}

// ─────────────────────────────────────────────────────────────────────────
// On-demand bridge: Telemetry + Report panes (fed by useMessageBody — see
// @/lib/span-view for the lazy fetch + LRU)
// ─────────────────────────────────────────────────────────────────────────

// prettyJSON pretty-prints a JSON string for display; returns "" for an
// empty input and the original string when it does not parse as JSON.
function prettyJSON(s: string | undefined): string {
  if (!s) return ""
  try {
    return JSON.stringify(JSON.parse(s), null, 2)
  } catch {
    return s
  }
}

function PaneNote({ children, warn }: { children: React.ReactNode; warn?: boolean }) {
  return (
    <div className="pt-3 text-[12.5px]" style={{ color: warn ? "var(--warn)" : "var(--text-4)" }}>
      {children}
    </div>
  )
}



// SubTab is the second-level strip inside a parent tab (Telemetry / Report):
// the same underline idiom as IOTab but smaller and neutral-colored so the
// two levels read distinctly.
function SubTab({ on, onClick, label, meta }: { on: boolean; onClick: () => void; label: string; meta?: string }) {
  return (
    <button
      type="button"
      onClick={onClick}
      className={cn(
        "px-3 py-1.5 -mb-px text-[11.5px] font-medium border-b-2 transition-colors",
        on
          ? "text-[color:var(--text)] border-[color:var(--text-3)]"
          : "text-[color:var(--text-4)] border-transparent hover:text-[color:var(--text-2)]",
      )}
    >
      {label}
      {meta ? <span className="font-normal text-[10.5px] text-[color:var(--text-4)] ml-1 mono tnum">{meta}</span> : null}
    </button>
  )
}

// WellHead is the slim meta row above a sub-tab's content well: left-aligned
// meta, right-aligned copy control for the well's full text.
function WellHead({ copyText, children }: { copyText?: string; children?: React.ReactNode }) {
  return (
    <div className="flex items-center gap-1.5 text-[11px] text-[color:var(--text-4)] mono tnum mt-2.5 mb-1.5">
      {children}
      <span className="flex-1" />
      {copyText ? <CardCopy text={copyText} /> : null}
    </div>
  )
}

// TelemetryPane is the bridge's first parent tab: everything the gen_ai span
// knows beyond the Output/Input turn, split across sub-tabs — System prompts,
// Tools (the definitions the request advertised), and Raw (the complete OTel
// span verbatim). The fetch (?include=telemetry — span side only, never the
// record) fires when the parent tab is first opened.
export function TelemetryPane({ cid, wanted }: { cid: string; wanted: boolean }) {
  const st = useMessageBody(cid, wanted, "telemetry")
  const [sub, setSub] = useState<"system" | "tools" | "raw">("system")
  const body = st.state === "ready" ? st.body : null
  const genAI = useMemo(() => (body ? parseGenAIContent(body.gen_ai_content) : null), [body])
  const rawSpan = useMemo(() => (body ? prettyJSON(body.span_event) : ""), [body])
  const sys = genAI?.system_instructions ?? []
  const defs = genAI?.tool_definitions ?? []
  return (
    <div className="pt-2.5">
      <div className="flex border-b border-[color:var(--border)] flex-wrap">
        <SubTab on={sub === "system"} onClick={() => setSub("system")} label="System prompts" meta={body ? String(sys.length) : undefined} />
        <SubTab on={sub === "tools"} onClick={() => setSub("tools")} label="Tools" meta={body ? String(defs.length) : undefined} />
        <SubTab on={sub === "raw"} onClick={() => setSub("raw")} label="Raw" meta="otel span" />
      </div>
      {st.state === "loading" && <PaneNote>loading telemetry…</PaneNote>}
      {st.state === "error" && <PaneNote warn>failed to load telemetry — try re-opening the tab</PaneNote>}
      {st.state === "missing" && (
        <PaneNote>no telemetry stored for this request (rolled off, or content capture disabled)</PaneNote>
      )}
      {body && genAI?.truncated && (
        <PaneNote warn>
          captured gen_ai content exceeded the service's content cap and was dropped
          {genAI.original_bytes ? ` (${fmt.compact(genAI.original_bytes)} bytes)` : ""}
        </PaneNote>
      )}
      {body && sub === "system" && (
        sys.length ? (
          <div className="pt-1">
            {sys.map((p, i) => (
              <SystemPromptCard key={i} part={p} index={i} count={sys.length} />
            ))}
          </div>
        ) : (
          <PaneNote>no system instructions captured on this span</PaneNote>
        )
      )}
      {body && sub === "tools" && (
        defs.length ? (
          <div className="pt-1">
            {defs.map((d, i) => (
              <ToolDefCard key={d.name ?? i} def={d} />
            ))}
          </div>
        ) : (
          <PaneNote>no tool definitions captured on this span</PaneNote>
        )
      )}
      {body && sub === "raw" && (
        rawSpan ? (
          <>
            <WellHead copyText={rawSpan}>
              the complete OTel gen_ai span, every attribute verbatim ·{" "}
              <b className="text-[color:var(--text-2)]">{fmt.compact(rawSpan.length)}</b> chars
            </WellHead>
            <PreBlock text={rawSpan} />
          </>
        ) : (
          <PaneNote>no raw span stored for this request (record-only event)</PaneNote>
        )
      )}
    </div>
  )
}

// SystemPromptCard renders one system-instruction part, open by default —
// the operator chose this sub-tab, so show the content (still foldable).
function SystemPromptCard({ part, index, count }: { part: GenAIMessagePart; index: number; count: number }) {
  const text = part.content ?? ""
  return (
    <DetailsCard
      defaultOpen
      copyText={text || undefined}
      head={
        <>
          <span style={{ color: "var(--warn)" }}>●</span>
          <span>System prompt{count > 1 ? ` · part ${index + 1}/${count}` : ""}</span>
          {part.type && part.type !== "text" && (
            <span className="mono text-[11px] text-[color:var(--text-4)]">{part.type}</span>
          )}
          <span className="ml-auto mono tnum text-[11.5px] text-[color:var(--text-3)] pl-2.5 whitespace-nowrap">
            <b className="text-[color:var(--text-2)]">{fmt.compact(text.length)}</b> chars
          </span>
        </>
      }
    >
      <PreBlock text={text || "(empty)"} />
    </DetailsCard>
  )
}

// ToolDefCard renders one advertised tool definition, collapsed by default —
// tool schemas are the bulkiest gen_ai content, so the tab opens as a
// scannable list of names.
function ToolDefCard({ def }: { def: GenAIToolDefinition }) {
  const params = useMemo(() => {
    if (def.parameters === undefined) return ""
    try {
      return JSON.stringify(def.parameters, null, 2)
    } catch {
      return String(def.parameters)
    }
  }, [def])
  const copyText = useMemo(() => {
    try {
      return JSON.stringify(def, null, 2)
    } catch {
      return undefined
    }
  }, [def])
  return (
    <DetailsCard
      copyText={copyText}
      markerColor="var(--accent)"
      head={
        <>
          <ToolChip name={def.name} />
          {def.type && <span className="mono text-[11px] text-[color:var(--text-4)]">{def.type}</span>}
          {def.description && (
            <span className="min-w-0 flex-1 truncate text-[11.5px] text-[color:var(--text-4)]">{def.description}</span>
          )}
        </>
      }
    >
      {def.description && (
        <div className="mb-2 whitespace-pre-wrap break-words text-[12px] text-[color:var(--text-3)]">{def.description}</div>
      )}
      {params ? <PreBlock text={params} /> : <div className="text-[12px] text-[color:var(--text-4)]">(no parameter schema)</div>}
    </DetailsCard>
  )
}

// ReportPane is the bridge's second parent tab: the spool-captured wire bytes
// the Record carried, split across sub-tabs — Request, Response (the
// accumulator's assembled rollup for streams), Stream (the raw SSE bytes),
// and Headers (redacted snapshots). The fetch (?include=report — record side
// only, never the span) fires ONLY when this tab is first opened.
export function ReportPane({ cid, wanted }: { cid: string; wanted: boolean }) {
  const st = useMessageBody(cid, wanted, "report")
  const [sub, setSub] = useState<"request" | "response" | "stream" | "headers">("request")
  const body = st.state === "ready" ? st.body : null
  const req = useMemo(() => (body ? prettyJSON(body.request) : ""), [body])
  const assembled = useMemo(() => (body ? prettyJSON(body.response_assembled) : ""), [body])
  // streaming = the accumulator produced a rollup; the raw SSE bytes then
  // live on the Stream sub-tab. Non-streaming responses pretty-print on the
  // Response sub-tab and Stream says so.
  const streaming = !!body?.response_assembled
  const resp = useMemo(() => {
    if (!body) return ""
    return streaming ? "" : prettyJSON(body.response)
  }, [body, streaming])
  const sse = (streaming ? body?.response : "") ?? ""
  const reqHdr = body?.request_headers ?? {}
  const respHdr = body?.response_headers ?? {}
  const nHdr = Object.keys(reqHdr).length + Object.keys(respHdr).length
  return (
    <div className="pt-2.5">
      <div className="flex border-b border-[color:var(--border)] flex-wrap">
        <SubTab on={sub === "request"} onClick={() => setSub("request")} label="Request" meta={body?.request_total_bytes ? `${fmt.compact(body.request_total_bytes)} B` : undefined} />
        <SubTab on={sub === "response"} onClick={() => setSub("response")} label="Response" meta={body?.response_total_bytes ? `${fmt.compact(body.response_total_bytes)} B` : undefined} />
        <SubTab on={sub === "stream"} onClick={() => setSub("stream")} label="Stream" meta={streaming ? "sse" : undefined} />
        <SubTab on={sub === "headers"} onClick={() => setSub("headers")} label="Headers" meta={body ? String(nHdr) : undefined} />
      </div>
      {st.state === "loading" && <PaneNote>loading report…</PaneNote>}
      {st.state === "error" && <PaneNote warn>failed to load the report — try re-opening the tab</PaneNote>}
      {st.state === "missing" && (
        <PaneNote>no report for this request (no connector binding, or the record rolled off)</PaneNote>
      )}
      {body && sub === "request" && (
        req ? (
          <>
            <WellHead copyText={req}>
              inbound request body as the client sent it ·{" "}
              <b className="text-[color:var(--text-2)]">{fmt.compact(body.request_total_bytes)}</b> bytes
              {body.request_truncated ? <b style={{ color: "var(--warn)" }}> · truncated</b> : null}
            </WellHead>
            <PreBlock text={req} />
          </>
        ) : (
          <PaneNote>no request body captured</PaneNote>
        )
      )}
      {body && sub === "response" && (
        streaming ? (
          <>
            <WellHead copyText={assembled}>
              assembled by the accumulator from the streamed chunks ·{" "}
              <b className="text-[color:var(--text-2)]">{fmt.compact(body.response_total_bytes)}</b> bytes on the wire
              {body.assembly_partial ? <b style={{ color: "var(--warn)" }}> · partial</b> : null}
              {body.response_truncated ? <b style={{ color: "var(--warn)" }}> · truncated</b> : null}
            </WellHead>
            <PreBlock text={assembled} />
          </>
        ) : resp ? (
          <>
            <WellHead copyText={resp}>
              outbound response body as it left the gateway ·{" "}
              <b className="text-[color:var(--text-2)]">{fmt.compact(body.response_total_bytes)}</b> bytes
              {body.response_truncated ? <b style={{ color: "var(--warn)" }}> · truncated</b> : null}
            </WellHead>
            <PreBlock text={resp} />
          </>
        ) : (
          <PaneNote>no response body captured</PaneNote>
        )
      )}
      {body && sub === "stream" && (
        streaming && sse ? (
          <>
            <WellHead copyText={sse}>
              raw SSE event bytes as they left the gateway ·{" "}
              <b className="text-[color:var(--text-2)]">{fmt.compact(body.response_total_bytes)}</b> bytes
              {body.response_truncated ? <b style={{ color: "var(--warn)" }}> · truncated</b> : null}
            </WellHead>
            <PreBlock text={sse} />
          </>
        ) : (
          <PaneNote>no SSE stream — this response was not streamed (the body is on the Response tab)</PaneNote>
        )
      )}
      {body && sub === "headers" && (
        nHdr ? (
          <>
            <WellHead copyText={headersCopyText(reqHdr, respHdr)}>credential values redacted at capture</WellHead>
            <HeaderList label="Request headers" headers={reqHdr} />
            <div className="h-2.5" />
            <HeaderList label="Response headers" headers={respHdr} />
          </>
        ) : (
          <PaneNote>no headers captured</PaneNote>
        )
      )}
    </div>
  )
}

function headersCopyText(req: Record<string, string[]>, resp: Record<string, string[]>): string {
  const block = (label: string, h: Record<string, string[]>) =>
    [label, ...Object.keys(h).sort().map((k) => `${k}: ${h[k].join(", ")}`)].join("\n")
  return `${block("# request", req)}\n\n${block("# response", resp)}`
}

function HeaderList({ label, headers }: { label: string; headers: Record<string, string[]> }) {
  const keys = Object.keys(headers).sort()
  if (!keys.length) return <div className="text-[12px] text-[color:var(--text-4)]">No {label.toLowerCase()}.</div>
  return (
    <div className="flex flex-col gap-1">
      <div className="text-[10.5px] font-semibold uppercase tracking-[0.08em] text-[color:var(--text-4)]">{label}</div>
      <div className="rounded-[var(--radius)] border border-[color:var(--border)] bg-[color:var(--bg)] p-2 text-[11.5px] mono flex flex-col gap-0.5">
        {keys.map((k) => (
          <div key={k} className="flex gap-2 break-all">
            <span className="text-[color:var(--text-3)] shrink-0">{k}:</span>
            <span className="text-[color:var(--text-2)]">{headers[k].join(", ")}</span>
          </div>
        ))}
      </div>
    </div>
  )
}
