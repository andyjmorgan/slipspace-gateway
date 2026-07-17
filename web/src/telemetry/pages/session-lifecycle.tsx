import { memo, useCallback, useDeferredValue, useEffect, useMemo, useRef, useState } from "react"
import type { ReactNode } from "react"
import { createPortal } from "react-dom"
import { useNavigate, useParams } from "react-router"
import { ChevronLeft, ChevronRight, RotateCcw, X } from "lucide-react"
import { Line, LineChart, ResponsiveContainer, Tooltip, XAxis, YAxis } from "recharts"
import { Button } from "@/components/ui/button"
import { KPI } from "@/components/atoms/kpi"
import { StatusPill } from "@/components/atoms/status-pill"
import { PanelCard, PanelHead, TableScroll } from "@/components/atoms/card"
import { PageHeader } from "@/components/atoms/page-header"
import { SkeletonTiles, SkeletonBlock } from "@/components/atoms/skeleton"
import { fmt } from "@/lib/fmt"
import { UnauthorizedError } from "@/lib/api"
import { fetchMessageByCorrelation, type MessageEntry, type MessageFilters } from "@/lib/messages"
import {
  fetchFullSpan,
  fetchSessionSpans,
  peekFullSpan,
  type InputPart,
  type OutputPart,
  type SessionSpan,
} from "@/lib/session-spans"
import { SESSION_MESSAGES_PAGE_SIZE, useDebounced, useMessagesPager } from "@/lib/messages-pager"
import { loadSessionFindings, type FindingRow } from "@/lib/findings"
import { Dash, MessagesPagerBar, MessagesTableView } from "../components/messages-table"
import { FindingsTable } from "../components/findings-table"
import { id8 } from "@/lib/span-view"
import {
  CardCopy,
  IOTab,
  type CallJoin,
  type RespJoin,
} from "../components/span-inspector-panes"
import { InspectorBody, type InspectorTab, type SpanClock } from "../components/inspector-body"
import { loadVerdict } from "@/lib/verdict"

// SessionLifecyclePage renders THE session view — the lifecycle dashboard
// derived from a SessionSpansDTO (GET /api/v1/sessions/{id}/spans): stat
// cards, the session activity strip, the token burn chart, a per-conversation
// lanes timeline with a minimap slicer, the slice-bound messages table,
// client/server tool tables, the tool-call ledger, and a span inspector modal.
//
// Renderer-contract purity (renderer-contract.md): every value on this page is
// a pure function of the span list, computed at render time — flow grouping by
// conversation, the TTFC/tail bar split, inter-request gaps, the exact
// tool_call ↔ tool_call_response id join, server-tool detection (call +
// same-span response), and FNV-1a name aliasing. Tool `args` are opaque vendor
// JSON: rendered raw or as a generic parsed key/value grid, never field-picked.

// ─────────────────────────────────────────────────────────────────────────
// Renderer-contract transform (pure functions of the DTO)
// ─────────────────────────────────────────────────────────────────────────

// NAME_POOL is the static 300-name alias pool (renderer-contract rule 6),
// ported verbatim from the reference implementation. AgentIDs hash into it via
// FNV-1a with per-session linear probing for uniqueness; the toggle in the
// timeline legend switches lanes back to raw ids.
const NAME_POOL = [
  "James", "John", "Robert", "Michael", "William", "David", "Richard", "Joseph", "Thomas", "Charles",
  "Christopher", "Daniel", "Matthew", "Anthony", "Mark", "Donald", "Steven", "Paul", "Andrew", "Joshua",
  "Kenneth", "Kevin", "Brian", "George", "Edward", "Ronald", "Timothy", "Jason", "Jeffrey", "Ryan",
  "Jacob", "Gary", "Nicholas", "Eric", "Jonathan", "Stephen", "Larry", "Justin", "Scott", "Brandon",
  "Benjamin", "Samuel", "Gregory", "Frank", "Alexander", "Raymond", "Patrick", "Jack", "Dennis", "Jerry",
  "Tyler", "Aaron", "Adam", "Henry", "Nathan", "Douglas", "Zachary", "Peter", "Kyle", "Walter",
  "Ethan", "Jeremy", "Harold", "Keith", "Christian", "Roger", "Noah", "Gerald", "Carl", "Terry",
  "Sean", "Austin", "Arthur", "Lawrence", "Jesse", "Dylan", "Bryan", "Jordan", "Billy", "Bruce",
  "Albert", "Willie", "Gabriel", "Logan", "Alan", "Wayne", "Roy", "Ralph", "Randy", "Eugene",
  "Vincent", "Russell", "Elijah", "Louis", "Bobby", "Philip", "Johnny", "Fred", "Wesley", "Oscar",
  "Derek", "Stanley", "Leon", "Marcus", "Calvin", "Edwin", "Dale", "Leo", "Gordon", "Dean",
  "Hank", "Glenn", "Ross", "Caleb", "Owen", "Lucas", "Mason", "Liam", "Hunter", "Cole",
  "Max", "Eli", "Miles", "Felix", "Hugo", "Victor", "Simon", "Oliver", "Isaac", "Levi",
  "Wyatt", "Carson", "Chase", "Blake", "Brett", "Troy", "Drew", "Grant", "Clark", "Reid",
  "Todd", "Neil", "Kurt", "Ray", "Gene", "Dustin", "Curtis", "Marvin", "Vernon", "Clyde",
  "Floyd", "Mary", "Patricia", "Jennifer", "Linda", "Elizabeth", "Barbara", "Susan", "Jessica", "Sarah",
  "Karen", "Lisa", "Nancy", "Betty", "Margaret", "Sandra", "Ashley", "Kimberly", "Emily", "Donna",
  "Michelle", "Carol", "Amanda", "Dorothy", "Melissa", "Deborah", "Stephanie", "Rebecca", "Sharon", "Laura",
  "Cynthia", "Kathleen", "Amy", "Angela", "Shirley", "Anna", "Brenda", "Pamela", "Emma", "Nicole",
  "Helen", "Samantha", "Katherine", "Christine", "Debra", "Rachel", "Carolyn", "Janet", "Catherine", "Maria",
  "Heather", "Diane", "Ruth", "Julie", "Olivia", "Joyce", "Virginia", "Victoria", "Kelly", "Lauren",
  "Christina", "Joan", "Evelyn", "Judith", "Megan", "Andrea", "Cheryl", "Hannah", "Jacqueline", "Martha",
  "Gloria", "Teresa", "Ann", "Sara", "Madison", "Frances", "Kathryn", "Janice", "Jean", "Abigail",
  "Alice", "Julia", "Judy", "Sophia", "Grace", "Denise", "Amber", "Doris", "Marilyn", "Danielle",
  "Beverly", "Isabella", "Theresa", "Diana", "Natalie", "Brittany", "Charlotte", "Marie", "Kayla", "Alexis",
  "Lori", "Tiffany", "Paula", "Erin", "Claire", "Audrey", "Ella", "Lucy", "Stella", "Ivy",
  "Ruby", "Naomi", "Vera", "Mae", "Pearl", "Hazel", "June", "Iris", "Faith", "Hope",
  "Autumn", "Summer", "Savannah", "Sydney", "Paige", "Brooke", "Chloe", "Zoe", "Leah", "Nora",
  "Lily", "Violet", "Rose", "Daisy", "Jade", "Eve", "Tessa", "Gina", "Rita", "Dawn",
  "Robin", "Holly", "Wendy", "Bonnie", "Peggy", "Lois", "Elaine", "Connie", "Vicki", "Tracy",
]

// fnv1a is the 32-bit FNV-1a hash the alias pool is keyed by (rule 6).
function fnv1a(s: string): number {
  let h = 0x811c9dc5
  for (let i = 0; i < s.length; i++) {
    h ^= s.charCodeAt(i)
    h = Math.imul(h, 0x01000193) >>> 0
  }
  return h >>> 0
}

// SpanVM is a DTO span annotated with session-relative seconds: t = receipt,
// tEnd = receipt + latency. All timeline math runs on these.
type SpanVM = SessionSpan & { t: number; tEnd: number }

// LedgerRow is one tool_call joined to its response per renderer-contract
// rule 4 (exact id join, envelope-only) and rule 8 (same-span response =
// server-executed). execS = result span receipt − call span end.
type LedgerRow = {
  id: string
  name: string
  args: string
  argsChars?: number
  conv: string
  callCid: string
  // callT is when the calling span finished emitting (tEnd) — the base the
  // client-tool exec time measures from.
  callT: number
  server: boolean
  // hostMs is the hosting span's latency for server-executed tools (the tool
  // ran inside the request, so there is no cross-span exec time).
  hostMs?: number | null
  resultT?: number
  execS?: number
  resultChars?: number
  isError?: boolean
}

// ViewModel is everything the page renders, derived once per span list.
type ViewModel = {
  sid: string
  t0ms: number
  dur: number
  spans: SpanVM[]
  byConv: Map<string, SpanVM[]>
  byCid: Map<string, SpanVM>
  convIds: string[]
  alias: Map<string, string>
  colors: Map<string, string>
  ledger: LedgerRow[]
  ledgerById: Map<string, LedgerRow>
}

// SliceRange is the timeline slicer's window in session-relative seconds.
// Owned by LifecycleView (null = full session) and shared by the minimap
// brush, the token burn chart, and the messages table.
type SliceRange = { d0: number; d1: number }

// MAIN_COLOR is the main conversation's lane color (the gold the whole design
// keys on); sub-agent lanes walk the hue wheel by the golden angle, dodging
// the band around the main hue so no agent reads as "main". Fixed
// lightness/chroma keeps the palette coherent in both themes — the same
// approach providerColor takes.
const MAIN_COLOR = "var(--warn)"

function convColor(i: number): string {
  let hue = Math.round(i * 137.508) % 360
  if (hue > 55 && hue < 95) hue += 45
  return `oklch(${i % 2 ? 0.78 : 0.66} 0.14 ${hue})`
}

// buildViewModel derives the full view model from the DTO (rules 1, 4, 6, 8).
// Returns null for an empty span list.
function buildViewModel(spansIn: SessionSpan[]): ViewModel | null {
  if (!spansIn.length) return null
  const sid = spansIn[0].session_id

  // Rule 0: session-relative time base.
  const t0ms = Math.min(...spansIn.map((s) => Date.parse(s.at)))
  const spans: SpanVM[] = spansIn
    .map((s) => {
      const t = (Date.parse(s.at) - t0ms) / 1000
      return { ...s, t, tEnd: t + (s.latency_ms ?? 0) / 1000 }
    })
    .sort((a, b) => a.t - b.t)
  const dur = Math.max(1, ...spans.map((s) => s.tEnd))

  // Rule 1: flow grouping — one lane per distinct conversation, main first,
  // sub-agents by first activity.
  const byConv = new Map<string, SpanVM[]>()
  for (const s of spans) {
    const flow = byConv.get(s.conversation_id)
    if (flow) flow.push(s)
    else byConv.set(s.conversation_id, [s])
  }
  const convIds = [...byConv.keys()].sort((a, b) =>
    a === sid ? -1 : b === sid ? 1 : (byConv.get(a)![0]?.t ?? 0) - (byConv.get(b)![0]?.t ?? 0),
  )

  // Rule 6: deterministic aliases — FNV-1a into the static pool, linear probe
  // per session for uniqueness. Falls back to the raw id if the pool is
  // exhausted (>300 sub-agents).
  const alias = new Map<string, string>()
  const used = new Set<number>()
  for (const c of convIds) {
    if (c === sid) continue
    if (used.size >= NAME_POOL.length) {
      alias.set(c, c.slice(0, 10))
      continue
    }
    let i = fnv1a(c) % NAME_POOL.length
    while (used.has(i)) i = (i + 1) % NAME_POOL.length
    used.add(i)
    alias.set(c, NAME_POOL[i])
  }

  const colors = new Map<string, string>()
  colors.set(sid, MAIN_COLOR)
  convIds.filter((c) => c !== sid).forEach((c, i) => colors.set(c, convColor(i)))

  // Rule 4 + 8: the ledger join. For every tool_call in any span's output:
  // a same-span tool_call_response makes it server-executed; otherwise the
  // first LATER span in the same conversation whose input carries a
  // tool_call_response with the same id joins it, and exec time is that
  // span's receipt minus the calling span's end. No gap heuristics.
  const ledger: LedgerRow[] = []
  for (const cid of convIds) {
    const flow = byConv.get(cid)!
    flow.forEach((s, si) => {
      for (const p of s.output_parts ?? []) {
        if (p.type !== "tool_call") continue
        const row: LedgerRow = {
          id: p.id ?? "",
          name: p.name ?? "(unnamed)",
          args: p.args ?? "",
          argsChars: p.args_chars,
          conv: cid,
          callCid: s.cid,
          callT: s.tEnd,
          server: false,
        }
        // Executor is authoritative when the gateway tagged it (provider-
        // hosted tools like OpenAI web_search fold their result into the model
        // output, so there is no same-span tool_call_response to pair — the
        // flag is the only server signal). The same-span pairing below stays
        // as the fallback for legacy spans and for providers whose result
        // does ride a discrete part (Anthropic).
        const sameSpanResp = (s.output_parts ?? []).some(
          (q) => q.type === "tool_call_response" && q.id === p.id,
        )
        if (p.executor === "server" || (p.executor !== "client" && sameSpanResp)) {
          row.server = true
          if (sameSpanResp) row.resultT = s.t
          row.hostMs = s.latency_ms
        } else {
          for (let j = si + 1; j < flow.length; j++) {
            const m = (flow[j].input_parts ?? []).find(
              (r) => r.type === "tool_call_response" && r.id === p.id,
            )
            if (m) {
              row.resultT = flow[j].t
              row.execS = +(flow[j].t - s.tEnd).toFixed(2)
              row.resultChars = m.chars
              row.isError = m.is_error
              break
            }
          }
        }
        ledger.push(row)
      }
    })
  }
  ledger.sort((a, b) => a.callT - b.callT)

  return {
    sid,
    t0ms,
    dur,
    spans,
    byConv,
    byCid: new Map(spans.map((s) => [s.cid, s])),
    convIds,
    alias,
    colors,
    ledger,
    ledgerById: new Map(ledger.map((r) => [r.id, r])),
  }
}

// median / percentileOf are nearest-rank stats over plain number arrays.
function median(values: number[]): number | null {
  if (!values.length) return null
  const s = [...values].sort((a, b) => a - b)
  return s[Math.floor(s.length / 2)]
}
function percentileOf(values: number[], p: number): number | null {
  if (!values.length) return null
  const s = [...values].sort((a, b) => a - b)
  return s[Math.min(s.length - 1, Math.floor(s.length * p))]
}

// clockAt renders a session-relative second as a local wall-clock HH:MM:SS;
// clockMsAt appends milliseconds for the crosshair / modal timing tile.
function clockAt(t0ms: number, t: number): string {
  return new Date(t0ms + t * 1000).toLocaleTimeString("en-GB", { hour12: false })
}
function clockMsAt(t0ms: number, t: number): string {
  const ms = Math.round(t0ms + t * 1000)
  return `${clockAt(ms, 0)}.${String(ms % 1000).padStart(3, "0")}`
}

// useRafThrottle coalesces high-frequency updates (brush drag, wheel zoom,
// crosshair mousemove) to at most one state commit per animation frame —
// mousemove can fire several times per frame, and each uncoalesced commit
// re-rendered the whole lanes SVG. Returns [push, cancel]; cancel drops any
// pending value (used when an immediate update like reset must not be
// overwritten by a stale queued frame).
function useRafThrottle<T>(apply: (v: T) => void): [(v: T) => void, () => void] {
  const raf = useRef(0)
  // Boxed so a pushed `null`/`undefined` value still counts as pending.
  const pending = useRef<{ v: T } | null>(null)
  const applyRef = useRef(apply)
  applyRef.current = apply
  const cancel = useCallback(() => {
    pending.current = null
    if (raf.current) {
      cancelAnimationFrame(raf.current)
      raf.current = 0
    }
  }, [])
  useEffect(() => cancel, [cancel])
  const push = useCallback((v: T) => {
    pending.current = { v }
    if (raf.current) return
    raf.current = requestAnimationFrame(() => {
      raf.current = 0
      if (pending.current) applyRef.current(pending.current.v)
      pending.current = null
    })
  }, [])
  return [push, cancel]
}

// ─────────────────────────────────────────────────────────────────────────
// Page shell: route param → fetch → view
// ─────────────────────────────────────────────────────────────────────────

export function SessionLifecyclePage() {
  const { id: routeId } = useParams<{ id?: string }>()
  if (!routeId) return null
  return (
    <div className="flex flex-col gap-3.5">
      <LifecycleBody key={routeId} sessionId={routeId} />
    </div>
  )
}

// LifecycleBreadcrumb: Sessions / <id> [ / <agent> ] — the first segment
// routes back to the list; the id IS this page (the lifecycle dashboard is the
// session view). The trailing agent segment appears only while a sub-agent is
// focused (drilldown), and clicking it clears the focus.
function LifecycleBreadcrumb({
  sessionId,
  focusedConv,
  agentLabel,
  onClear,
}: {
  sessionId: string
  focusedConv: string | null
  agentLabel: string | null
  onClear: () => void
}) {
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
      <button
        type="button"
        onClick={onClear}
        className={
          "mono text-[12px] truncate shrink min-w-0 " +
          (focusedConv ? "text-[color:var(--accent)] hover:underline" : "text-[color:var(--text-2)] cursor-default")
        }
        title={sessionId}
        disabled={!focusedConv}
      >
        {sessionId}
      </button>
      {focusedConv && agentLabel && (
        <>
          <ChevronRight size={14} className="text-[color:var(--text-4)] shrink-0" />
          <span className="text-[12px] text-[color:var(--text-2)] truncate shrink-0" title={focusedConv}>
            {agentLabel}
          </span>
        </>
      )}
    </div>
  )
}

function LifecycleBody({ sessionId }: { sessionId: string }) {
  const nav = useNavigate()
  const [spans, setSpans] = useState<SessionSpan[]>([])
  const [source, setSource] = useState<"api" | "fixture">("api")
  const [status, setStatus] = useState<"loading" | "ok" | "error">("loading")
  // partial is true while later pages of a multi-page session are still in
  // flight — the dashboard paints from page 1 and appends as pages land
  // instead of blanking on a serial cursor walk.
  const [partial, setPartial] = useState(false)
  const [err, setErr] = useState("")

  // No "loading" reset inside the effect: LifecycleBody is keyed by session
  // id, so a new session remounts it with the initial loading state.
  useEffect(() => {
    let cancelled = false
    fetchSessionSpans(sessionId, (cum, done) => {
      // Progressive paint: each page hands back the cumulative list (a fresh
      // array), so the view model rebuilds and the dashboard grows in place.
      if (cancelled) return
      setSpans(cum)
      setPartial(!done)
      setStatus("ok")
    })
      .then((r) => {
        if (cancelled) return
        setSpans(r.spans)
        setSource(r.source)
        setPartial(false)
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

  const vm = useMemo(() => (status === "ok" ? buildViewModel(spans) : null), [status, spans])

  // focusedConv is the sub-agent drilldown, seeded from #agent=<conv> so a
  // focused view is shareable; null = whole session. onFocus mirrors it back
  // to the hash (a handler, never during render).
  const [focusedConv, setFocusedConvState] = useState<string | null>(() => {
    const m = window.location.hash.match(/^#agent=(.+)$/)
    return m ? decodeURIComponent(m[1]) : null
  })
  const onFocus = useCallback((conv: string | null) => {
    setFocusedConvState(conv)
    const next = conv ? `#agent=${encodeURIComponent(conv)}` : window.location.pathname + window.location.search
    window.history.replaceState(null, "", next)
  }, [])
  // Only honor a focus that maps to a real conversation in this session.
  const effFocus = vm && focusedConv && vm.byConv.has(focusedConv) ? focusedConv : null
  const agentLabel = effFocus ? (effFocus === vm!.sid ? "main" : (vm!.alias.get(effFocus) ?? effFocus.slice(0, 10))) : null
  const crumb = (
    <LifecycleBreadcrumb sessionId={sessionId} focusedConv={effFocus} agentLabel={agentLabel} onClear={() => onFocus(null)} />
  )

  if (status === "loading") {
    return (
      <>
        {crumb}
        <SkeletonTiles count={8} className="grid grid-cols-2 lg:grid-cols-4 gap-3" />
        <PanelCard className="p-4">
          <SkeletonBlock height={60} />
        </PanelCard>
        <PanelCard className="p-4">
          <SkeletonBlock height={260} />
        </PanelCard>
      </>
    )
  }
  if (status === "error") {
    return (
      <>
        {crumb}
        <div
          className="rounded-[var(--radius-lg)] border border-[color:var(--border)] p-8 text-center text-[13px]"
          style={{ color: "var(--err)", background: "var(--err-bg)" }}
        >
          Failed to load spans: <span className="mono">{err}</span>
        </div>
      </>
    )
  }
  if (!vm) {
    return (
      <>
        {crumb}
        <div className="rounded-[var(--radius-lg)] border border-[color:var(--border)] bg-[color:var(--bg-1)] p-8 text-center text-[13px] text-[color:var(--text-3)]">
          No AI requests captured for session <span className="mono">{sessionId}</span>.
        </div>
      </>
    )
  }
  return (
    <>
      {crumb}
      <LifecycleView vm={vm} source={source} partial={partial} focusedConv={effFocus} onFocus={onFocus} />
    </>
  )
}

// ─────────────────────────────────────────────────────────────────────────
// The dashboard proper
// ─────────────────────────────────────────────────────────────────────────

function LifecycleView({
  vm,
  source,
  partial,
  focusedConv,
  onFocus,
}: {
  vm: ViewModel
  source: "api" | "fixture"
  partial: boolean
  focusedConv: string | null
  onFocus: (conv: string | null) => void
}) {
  // useNames toggles rule-6 aliases vs raw agent ids everywhere a
  // conversation is named (lanes, ledger, modal).
  const [useNames, setUseNames] = useState(true)
  // Deep link: opening the page with #span=<cid> opens that span's inspector
  // on load (ported from the reference implementation's #modal= hash). With
  // progressive paint the view mounts on page 1, so a hash pointing at a
  // later page's span resolves via the effect below as pages land —
  // hashConsumed makes it one-shot so closing the modal stays closed.
  const [selectedCid, setSelectedCid] = useState<string | null>(() => {
    const m = window.location.hash.match(/^#span=(.+)$/)
    return m && vm.byCid.has(decodeURIComponent(m[1])) ? decodeURIComponent(m[1]) : null
  })
  const hashConsumed = useRef(selectedCid != null)
  useEffect(() => {
    if (hashConsumed.current) return
    const m = window.location.hash.match(/^#span=(.+)$/)
    if (!m) {
      hashConsumed.current = true
      return
    }
    const cid = decodeURIComponent(m[1])
    if (vm.byCid.has(cid)) {
      hashConsumed.current = true
      setSelectedCid(cid)
    }
  }, [vm])
  // ioTab is lifted here so the inspector's tab choice (Output | Input |
  // Telemetry | Report) persists across prev/next while the page lives.
  const [ioTab, setIoTab] = useState<InspectorTab>("conversation")
  // slice is the timeline brush window [d0, d1] in session seconds, lifted
  // here so the token burn chart and the messages table bind to it. null =
  // the full session — and tracks vm.dur as progressive pages land, so a
  // growing session never strands the view on page one's extent.
  const [slice, setSlice] = useState<SliceRange | null>(null)

  const dispName = useCallback(
    (conv: string) =>
      conv === vm.sid ? "main" : useNames ? (vm.alias.get(conv) ?? conv.slice(0, 10)) : "agent " + conv.slice(0, 10),
    [vm, useNames],
  )

  // openSpan guards a messages-table row click: the table's rows come from
  // the events store, so a correlation id without a captured gen_ai span
  // (capture gap) must not open an empty inspector.
  const openSpan = useCallback(
    (cid: string) => {
      if (vm.byCid.has(cid)) setSelectedCid(cid)
    },
    [vm],
  )

  // openSpanSecurity opens a span's inspector straight on the Security tab —
  // used by the session's Security findings list so clicking a finding lands
  // on its verdict + offending text, not the default Output pane.
  const openSpanSecurity = useCallback((cid: string) => {
    setIoTab("security")
    setSelectedCid(cid)
  }, [])

  return (
    <>
      <LifecycleHeader vm={vm} source={source} partial={partial} />
      <StatCards vm={vm} />
      <ActivityStrip vm={vm} />
      <TokenBurnPanel vm={vm} slice={slice} />
      <TimelinePanel
        vm={vm}
        slice={slice}
        onSlice={setSlice}
        dispName={dispName}
        useNames={useNames}
        onToggleNames={() => setUseNames((v) => !v)}
        onOpen={setSelectedCid}
        focusedConv={focusedConv}
        onFocus={onFocus}
      />
      <SessionDataTabs
        vm={vm}
        slice={slice}
        source={source}
        dispName={dispName}
        onOpenMessage={openSpan}
        onOpenSpan={setSelectedCid}
        onOpenSecurity={openSpanSecurity}
        focusedConv={focusedConv}
      />
      {selectedCid && (
        <SpanInspector
          vm={vm}
          source={source}
          cid={selectedCid}
          ioTab={ioTab}
          onTab={setIoTab}
          dispName={dispName}
          onSelect={setSelectedCid}
          onClose={() => setSelectedCid(null)}
        />
      )}
    </>
  )
}

function LifecycleHeader({
  vm,
  source,
  partial,
}: {
  vm: ViewModel
  source: "api" | "fixture"
  partial: boolean
}) {
  const models = useMemo(() => {
    const ct = new Map<string, number>()
    for (const s of vm.spans) ct.set(s.model ?? "unknown", (ct.get(s.model ?? "unknown") ?? 0) + 1)
    return [...ct.entries()].sort((a, b) => b[1] - a[1])
  }, [vm])
  const errs = vm.spans.filter((s) => s.status != null && s.status !== 200).length
  const day = new Date(vm.t0ms).toLocaleDateString("en-CA")
  return (
    <PageHeader title="Session lifecycle" sub="Reconstructed from this session's request telemetry">
      {source === "fixture" && (
        <HeaderChip>
          <span style={{ color: "var(--warn)" }}>sample data</span> — live telemetry unavailable
        </HeaderChip>
      )}
      {partial && (
        <HeaderChip>
          <span style={{ color: "var(--accent)" }}>loading…</span> {vm.spans.length} spans so far
        </HeaderChip>
      )}
      <HeaderChip>
        <b className="mono">{models[0]?.[0]}</b>
        {models.length > 1 ? <span className="text-[color:var(--text-4)]"> +{models.length - 1}</span> : null}
      </HeaderChip>
      <HeaderChip>
        {day} · <b className="mono">{clockAt(vm.t0ms, 0)} → {clockAt(vm.t0ms, vm.dur)}</b>
      </HeaderChip>
      <HeaderChip>
        {errs > 0 ? (
          <>
            <b style={{ color: "var(--err)" }}>{errs}</b> non-200
          </>
        ) : (
          <>
            all responses <b>HTTP 200</b>
          </>
        )}
      </HeaderChip>
    </PageHeader>
  )
}

function HeaderChip({ children }: { children: React.ReactNode }) {
  return (
    <span className="inline-flex items-center gap-1 rounded-full border border-[color:var(--border)] bg-[color:var(--bg-1)] px-2.5 py-0.5 text-[11.5px] text-[color:var(--text-3)]">
      {children}
    </span>
  )
}

// StatCards: the 8 lifecycle summary cards (2×4), all aggregated from the
// span list at render time. Memoized (as are ActivityStrip, ToolTable, and
// LedgerTable): the slice state lives on LifecycleView, so every brush frame
// re-renders it — the panels that don't read the slice must not re-render
// with it.
const StatCards = memo(function StatCards({ vm }: { vm: ViewModel }) {
  const c = useMemo(() => {
    const lats = vm.spans.map((s) => s.latency_ms).filter((v): v is number => v != null)
    const outs = vm.spans.map((s) => s.usage.output).filter((v): v is number => v != null)
    const ttfcs = vm.spans.map((s) => s.ttfc_ms).filter((v): v is number => v != null)
    const tot = (k: "input" | "output" | "cache_read" | "cache_creation") =>
      vm.spans.reduce((a, s) => a + (s.usage[k] ?? 0), 0)
    const totIn = tot("input")
    const totCr = tot("cache_read")
    // Peak concurrency, computed per-second over the whole window.
    let peakN = 0
    let peakT = 0
    for (let sec = 0; sec < vm.dur; sec++) {
      const n = vm.spans.reduce((a, s) => a + (s.t < sec + 1 && s.tEnd > sec ? 1 : 0), 0)
      if (n > peakN) {
        peakN = n
        peakT = sec
      }
    }
    const nSrv = vm.ledger.filter((r) => r.server).length
    // Spend aggregates. When any span carries cost the grid grows a whole
    // 4-card cost row (total / rate / costliest / sub-agent split) so the
    // layout stays 4-per-row symmetric; costing-off sessions keep the
    // original 2×4.
    const costs = vm.spans.map((s) => s.usage.cost_usd ?? 0)
    const totCost = costs.reduce((a, v) => a + v, 0)
    const subCost = vm.spans.reduce(
      (a, s) => a + (s.conversation_id !== vm.sid ? (s.usage.cost_usd ?? 0) : 0),
      0,
    )
    const nSubThreads = new Set(
      vm.spans.filter((s) => s.conversation_id !== vm.sid && (s.usage.cost_usd ?? 0) > 0).map((s) => s.conversation_id),
    ).size
    let maxCost = 0
    let maxCostSpan: (typeof vm.spans)[number] | null = null
    for (const s of vm.spans) {
      const v = s.usage.cost_usd ?? 0
      if (v > maxCost) {
        maxCost = v
        maxCostSpan = s
      }
    }
    return {
      lats,
      outs,
      ttfcs,
      totIn,
      totOut: tot("output"),
      totCw: tot("cache_creation"),
      crShare: totIn ? Math.round((totCr / totIn) * 100) : 0,
      peakN,
      peakT,
      nSrv,
      nCli: vm.ledger.length - nSrv,
      totCost,
      subCost,
      nSubThreads,
      maxCost,
      maxCostSpan,
      medCost: median(costs.filter((v) => v > 0)),
    }
  }, [vm])

  const subAgents = vm.convIds.length - 1
  return (
    <div className="grid grid-cols-2 lg:grid-cols-4 gap-3">
      <KPI
        label="Wall time"
        value={fmt.uptime(vm.dur * 1000)}
        sub={`${clockAt(vm.t0ms, 0)} → ${clockAt(vm.t0ms, vm.dur)}`}
        accent="warn"
      />
      <KPI
        label="Spans"
        value={String(vm.spans.length)}
        sub={`${(vm.spans.length / (vm.dur / 60)).toFixed(1)}/min · med latency ${fmt.ms(median(c.lats))}`}
        accent="info"
      />
      <KPI
        label="Conversations"
        value={String(vm.convIds.length)}
        sub={`1 main · ${subAgents} sub-agent${subAgents === 1 ? "" : "s"}`}
      />
      <KPI
        label="Peak concurrency"
        value={
          <>
            {c.peakN}
            <span className="text-[12px] font-medium text-[color:var(--text-3)]"> in flight</span>
          </>
        }
        sub={`per-second · at ${clockAt(vm.t0ms, c.peakT)}`}
      />
      <KPI
        label="Tokens in"
        value={fmt.compact(c.totIn)}
        sub={`${c.crShare}% cache reads · ${fmt.compact(c.totCw)} written`}
        accent="ok"
      />
      <KPI label="Tokens out" value={fmt.compact(c.totOut)} sub={`median ${fmt.compact(median(c.outs))} / span`} accent="ok" />
      <KPI label="Tool calls" value={String(vm.ledger.length)} sub={`${c.nCli} client · ${c.nSrv} server`} accent="warn" />
      <KPI
        label="Median TTFC"
        value={fmt.ms(median(c.ttfcs))}
        sub={`p90 ${fmt.ms(percentileOf(c.ttfcs, 0.9))} · n=${c.ttfcs.length}`}
        accent="info"
      />
      {c.totCost > 0 && (
        <>
          <KPI
            label="Est. spend"
            value={fmt.usd(c.totCost)}
            sub={`median ${fmt.usd(c.medCost)} / span`}
            accent="warn"
          />
          <KPI
            label="Spend rate"
            value={vm.dur > 0 ? `${fmt.usd((c.totCost / vm.dur) * 3600)}/hr` : "—"}
            sub={`over ${fmt.uptime(vm.dur * 1000)}`}
            accent="warn"
          />
          <KPI
            label="Costliest request"
            value={fmt.usd(c.maxCost)}
            sub={
              c.maxCostSpan
                ? `${c.maxCostSpan.model ?? "unknown model"} · ${Math.round((c.maxCost / c.totCost) * 100)}% of spend`
                : "—"
            }
          />
          <KPI
            label="Sub-agent spend"
            value={c.subCost > 0 ? fmt.usd(c.subCost) : "$0.00"}
            sub={
              c.subCost > 0
                ? `${c.nSubThreads} thread${c.nSubThreads === 1 ? "" : "s"} · ${Math.round((c.subCost / c.totCost) * 100)}% of spend`
                : "all on the main thread"
            }
          />
        </>
      )}
    </div>
  )
})

// ActivityStrip: stacked active-conversation count (main + sub-agents) per
// time bucket across the whole session, as a normalized-viewBox SVG.
const STRIP_BUCKETS = 160

const ActivityStrip = memo(function ActivityStrip({ vm }: { vm: ViewModel }) {
  const { rows, maxC, bucketLabel } = useMemo(() => {
    const bw = vm.dur / STRIP_BUCKETS
    const out: [number, number][] = []
    let max = 1
    const main = vm.byConv.get(vm.sid) ?? []
    for (let i = 0; i < STRIP_BUCKETS; i++) {
      const t1 = i * bw
      const t2 = t1 + bw
      const m = main.some((s) => s.t < t2 && s.tEnd > t1) ? 1 : 0
      const sub = vm.convIds.reduce(
        (a, cId) =>
          a + (cId !== vm.sid && (vm.byConv.get(cId) ?? []).some((s) => s.t < t2 && s.tEnd > t1) ? 1 : 0),
        0,
      )
      out.push([m, sub])
      max = Math.max(max, m + sub)
    }
    return { rows: out, maxC: max, bucketLabel: bw >= 1 ? `${Math.round(bw)}s` : `${bw.toFixed(1)}s` }
  }, [vm])

  return (
    <PanelCard className="px-4 pt-3 pb-2.5">
      <div className="flex justify-between items-baseline mb-1.5 gap-2 flex-wrap">
        <span className="text-[10.5px] font-semibold uppercase tracking-[0.1em] text-[color:var(--text-3)]">
          Session activity — active conversations per <b className="text-[color:var(--text-2)]">{bucketLabel}</b> bucket
          <span className="ml-2.5 normal-case tracking-normal font-normal">
            <span style={{ color: "var(--warn)" }}>▪</span> main{" "}
            <span style={{ color: "var(--accent)" }}>▪</span> sub-agents
          </span>
        </span>
        <span className="text-[10.5px] font-semibold uppercase tracking-[0.1em] text-[color:var(--text-3)]">
          peak <b className="text-[color:var(--text-2)]">{maxC}</b> active
        </span>
      </div>
      <svg
        viewBox={`0 0 ${STRIP_BUCKETS} ${maxC}`}
        preserveAspectRatio="none"
        className="block w-full h-[42px]"
        aria-label="Session activity"
      >
        {[0.25, 0.5, 0.75].map((f) => (
          <line
            key={f}
            x1={0}
            y1={maxC * f}
            x2={STRIP_BUCKETS}
            y2={maxC * f}
            style={{ stroke: "var(--border)" }}
            strokeWidth={0.5}
            vectorEffect="non-scaling-stroke"
          />
        ))}
        {rows.map(([m, s], i) => (
          <g key={i}>
            {m > 0 && (
              <rect x={i + 0.09} y={maxC - m} width={0.82} height={m} style={{ fill: "var(--warn)" }} opacity={0.9} />
            )}
            {s > 0 && (
              <rect
                x={i + 0.09}
                y={maxC - m - s}
                width={0.82}
                height={s}
                style={{ fill: "var(--accent)" }}
                opacity={0.78}
              />
            )}
          </g>
        ))}
      </svg>
      <div className="flex justify-between text-[10px] mono text-[color:var(--text-4)] mt-1">
        <span>{clockAt(vm.t0ms, 0)}</span>
        <span>{clockAt(vm.t0ms, vm.dur)}</span>
      </div>
    </PanelCard>
  )
})

// ─────────────────────────────────────────────────────────────────────────
// Token burn chart
// ─────────────────────────────────────────────────────────────────────────

// BurnPoint is one span projected onto the burn chart: per-span tokens in/out
// plus the running session-wide cumulative burn as of that span.
type BurnPoint = {
  t: number
  cid: string
  model: string
  tin: number
  tout: number
  cum: number
}

// TokenBurnPanel charts token consumption across the session, split into two
// side-by-side line charts that share one time domain: per-request tokens
// in/out (--p-input / --p-output, the designated token-curve colors) and the
// running cumulative burn (--warn). They were one dual-axis ComposedChart, but
// on long sessions the per-span bars collapsed to sub-pixel widths against the
// cumulative line's scale and effectively vanished; two single-axis line charts
// render cleanly at any span count and keep each question legible on its own
// scale. Both respect the slice — when the brush moves, the charts re-domain to
// [d0, d1], computed client-side from the already-loaded structure spans. The
// slice is deferred so a fast brush drag doesn't force a recharts re-render
// every frame.
function TokenBurnPanel({ vm, slice }: { vm: ViewModel; slice: SliceRange | null }) {
  const deferred = useDeferredValue(slice)
  const d0 = deferred?.d0 ?? 0
  const d1 = deferred?.d1 ?? vm.dur
  const full = d0 <= 0.5 && d1 >= vm.dur - 0.5

  const points = useMemo<BurnPoint[]>(() => {
    // Cumulative sums over the WHOLE session (true burn-to-date, so the line
    // doesn't restart at zero inside a slice); points then filter to the
    // slice for display.
    const out: BurnPoint[] = []
    let cum = 0
    for (const s of vm.spans) {
      const tin = s.usage.input ?? 0
      const tout = s.usage.output ?? 0
      cum += tin + tout
      if (s.t < d0 || s.t > d1) continue
      out.push({ t: s.t, cid: s.cid, model: s.model ?? "unknown", tin, tout, cum })
    }
    return out
  }, [vm, d0, d1])

  const sub = full ? null : (
    <span className="mono tnum font-semibold" style={{ color: "var(--accent)" }}>
      slice {clockAt(vm.t0ms, d0)} → {clockAt(vm.t0ms, d1)} · {fmt.uptime((d1 - d0) * 1000)}
    </span>
  )

  return (
    <div className="grid lg:grid-cols-2 gap-3 items-start">
      <BurnChartCard
        vm={vm}
        points={points}
        d0={d0}
        d1={d1}
        title="Tokens per request"
        sub={sub ?? "tokens in / out per request — follows the timeline slice"}
        action={
          <div className="flex items-center gap-3 flex-wrap text-[11px] text-[color:var(--text-2)]">
            <BurnSwatch color="var(--p-input)" label="tokens in" line />
            <BurnSwatch color="var(--p-output)" label="tokens out" line dash />
          </div>
        }
        lines={[
          { key: "tin", color: "var(--p-input)" },
          { key: "tout", color: "var(--p-output)", dash: "5 3" },
        ]}
      />
      <BurnChartCard
        vm={vm}
        points={points}
        d0={d0}
        d1={d1}
        title="Cumulative burn"
        sub={sub ?? "running total tokens across the session — follows the timeline slice"}
        action={
          <div className="flex items-center gap-3 flex-wrap text-[11px] text-[color:var(--text-2)]">
            <BurnSwatch color="var(--warn)" label="cumulative" line />
          </div>
        }
        lines={[{ key: "cum", color: "var(--warn)" }]}
      />
    </div>
  )
}

// BurnChartCard is one half of the token-burn split: a single-axis line chart
// over the shared [d0, d1] time domain. `lines` selects which BurnPoint series
// to draw (tin/tout for the per-request card, cum for the cumulative card).
function BurnChartCard({
  vm,
  points,
  d0,
  d1,
  title,
  sub,
  action,
  lines,
}: {
  vm: ViewModel
  points: BurnPoint[]
  d0: number
  d1: number
  title: string
  sub: ReactNode
  action: ReactNode
  lines: { key: keyof BurnPoint; color: string; dash?: string }[]
}) {
  const axis = "var(--text-4)"
  return (
    <PanelCard>
      <PanelHead title={title} sub={sub} action={action} />
      <div className="px-4 pt-3 pb-2">
        {points.length === 0 ? (
          <div className="text-[12px] grid place-items-center min-h-[180px] text-[color:var(--text-4)]">
            No requests in this window.
          </div>
        ) : (
          <ResponsiveContainer width="100%" height={180}>
            <LineChart data={points} margin={{ top: 6, right: 8, bottom: 0, left: 0 }}>
              <XAxis
                dataKey="t"
                type="number"
                domain={[d0, d1]}
                tickFormatter={(v) => clockAt(vm.t0ms, v as number)}
                tick={{ fontSize: 10, fill: axis }}
                stroke={axis}
                minTickGap={42}
              />
              <YAxis
                tickFormatter={(v) => fmt.compact(v as number)}
                tick={{ fontSize: 10, fill: axis }}
                stroke={axis}
                width={44}
              />
              <Tooltip cursor={{ stroke: "var(--border)" }} content={<BurnTooltip t0ms={vm.t0ms} />} />
              {lines.map((l) => (
                <Line
                  key={l.key}
                  dataKey={l.key}
                  type="monotone"
                  stroke={l.color}
                  strokeWidth={1.5}
                  strokeDasharray={l.dash}
                  dot={false}
                  isAnimationActive={false}
                />
              ))}
            </LineChart>
          </ResponsiveContainer>
        )}
      </div>
    </PanelCard>
  )
}

// BurnSwatch is the token chart's legend chip: a square for a bar series, a
// line for the cumulative curve. `dash` renders the line swatch dashed to
// mirror a dashed series, so the two token curves are told apart by line
// style as well as color (color-blind safe).
function BurnSwatch({ color, label, line, dash }: { color: string; label: string; line?: boolean; dash?: boolean }) {
  return (
    <span className="inline-flex items-center gap-1.5">
      <span
        className={line ? "inline-block w-3 h-[2px] rounded-full" : "inline-block w-2.5 h-2.5 rounded-[2px]"}
        style={
          line && dash
            ? { backgroundImage: `repeating-linear-gradient(to right, ${color} 0 3px, transparent 3px 5px)` }
            : { backgroundColor: color }
        }
      />
      <span>{label}</span>
    </span>
  )
}

// BurnTooltip renders the hovered span's burn detail. Typed locally rather
// than via recharts' TooltipProps (its v3 content-prop shape doesn't surface
// payload directly); recharts injects active/payload at runtime when it
// clones the element passed to Tooltip's content.
type BurnTooltipProps = { active?: boolean; payload?: { payload?: BurnPoint }[]; t0ms?: number }
function BurnTooltip({ active, payload, t0ms }: BurnTooltipProps) {
  if (!active || !payload?.length || t0ms == null) return null
  const p = payload[0]?.payload
  if (!p) return null
  return (
    <div className="rounded-[var(--radius)] border border-[color:var(--border)] bg-[color:var(--bg-1)] px-3 py-2 text-[11.5px] shadow-[var(--shadow-md)] flex flex-col gap-0.5">
      <div className="mono text-[color:var(--text-4)]">{clockAt(t0ms, p.t)}</div>
      <div className="mono">{p.model}</div>
      <div className="text-[color:var(--text-3)]">
        in {fmt.compact(p.tin)} · out {fmt.compact(p.tout)} · cumulative {fmt.compact(p.cum)}
      </div>
    </div>
  )
}

// ─────────────────────────────────────────────────────────────────────────
// Timeline: minimap slicer + per-conversation lanes (rules 2-3)
// ─────────────────────────────────────────────────────────────────────────

// Shared geometry between the minimap and the lanes chart — same left margin,
// same plot width, stacked in the same scroll container so a time on one
// lines up vertically with the other.
const GEO = { l: 122, r: 24, t: 36, b: 14 }
const LANE_H = 22
const LANE_GAP = 4
const MINI_H = 58
const MINI_AXIS = 18
const MIN_PLOT_W = 1000

// MODAL_FOCUSABLE selects the tabbable descendants the span-modal focus trap
// cycles through.
const MODAL_FOCUSABLE =
  'a[href],button:not([disabled]),input:not([disabled]),select:not([disabled]),textarea:not([disabled]),[tabindex]:not([tabindex="-1"])'

// Drag mode for the minimap brush: create a new slice, resize an edge, or pan
// the window.
type DragState =
  | { mode: "new"; anchorT: number }
  | { mode: "l" }
  | { mode: "r" }
  | { mode: "move"; offT: number; w: number }

function TimelinePanel({
  vm,
  slice,
  onSlice,
  dispName,
  useNames,
  onToggleNames,
  onOpen,
  focusedConv,
  onFocus,
}: {
  vm: ViewModel
  slice: SliceRange | null
  onSlice: (s: SliceRange | null) => void
  dispName: (conv: string) => string
  useNames: boolean
  onToggleNames: () => void
  onOpen: (cid: string) => void
  // focusedConv is the sub-agent conversation the operator drilled into (null
  // = whole session); onFocus toggles it. Drives lane dimming + the agents
  // strip; the messages table re-queries to the focused conversation.
  focusedConv: string | null
  onFocus: (conv: string | null) => void
}) {
  // Measured width: the SVGs fill the panel, never narrower than MIN_PLOT_W
  // (horizontal scroll takes over below that).
  const boxRef = useRef<HTMLDivElement>(null)
  const [w, setW] = useState(MIN_PLOT_W)
  useEffect(() => {
    const el = boxRef.current
    if (!el) return
    const ro = new ResizeObserver(() => setW(Math.max(MIN_PLOT_W, el.clientWidth)))
    ro.observe(el)
    setW(Math.max(MIN_PLOT_W, el.clientWidth))
    return () => ro.disconnect()
  }, [])
  const plotW = w - GEO.l - GEO.r

  // The slice window [d0, d1] in session seconds, owned by LifecycleView
  // (null = full session, which tracks vm.dur as progressive pages land).
  // rangeRef mirrors the resolved window for the window-level drag handlers,
  // which must read the latest value without re-subscribing.
  const d0 = slice?.d0 ?? 0
  const d1 = slice?.d1 ?? vm.dur
  const rangeRef = useRef({ d0, d1 })
  useEffect(() => {
    rangeRef.current = { d0, d1 }
  }, [d0, d1])
  const dragRef = useRef<DragState | null>(null)
  const miniRef = useRef<SVGSVGElement>(null)
  const chartRef = useRef<SVGSVGElement>(null)

  const full = d0 <= 0.5 && d1 >= vm.dur - 0.5
  const span = Math.max(1, d1 - d0)

  // mx maps full-session time → minimap x; xz maps time → chart x within the
  // current slice.
  const mx = useCallback((t: number) => GEO.l + (t / vm.dur) * plotW, [vm.dur, plotW])
  const mt = useCallback(
    (px: number) => Math.max(0, Math.min(vm.dur, ((px - GEO.l) / plotW) * vm.dur)),
    [vm.dur, plotW],
  )
  const xz = useCallback((t: number) => GEO.l + ((t - d0) / span) * plotW, [d0, span, plotW])
  const clampX = useCallback((px: number) => Math.max(GEO.l, Math.min(w - GEO.r, px)), [w])

  // Brush updates are rAF-throttled: drag/wheel mousemoves coalesce to one
  // range commit (and so one lanes re-render) per frame.
  const [pushRange, cancelPendingRange] = useRafThrottle<SliceRange>(onSlice)
  const setBrush = useCallback(
    (t1: number, t2: number) => {
      let n0 = Math.max(0, Math.min(t1, t2))
      let n1 = Math.min(vm.dur, Math.max(t1, t2))
      if (n1 - n0 < 2) n1 = Math.min(vm.dur, n0 + 2)
      if (n1 - n0 < 2) n0 = Math.max(0, n1 - 2)
      pushRange({ d0: n0, d1: n1 })
    },
    [vm.dur, pushRange],
  )
  const reset = useCallback(() => {
    // Immediate, and drop any queued drag frame so it can't undo the reset.
    cancelPendingRange()
    onSlice(null)
  }, [onSlice, cancelPendingRange])

  // Minimap drag: mousedown picks the mode (edge resize / pan / new slice);
  // window-level move/up finish the gesture so it survives leaving the svg.
  const onMiniDown = (ev: React.MouseEvent<SVGSVGElement>) => {
    const rect = ev.currentTarget.getBoundingClientRect()
    const px = ev.clientX - rect.left
    const t = mt(px)
    const r = rangeRef.current
    const sliced = !(r.d0 <= 0.5 && r.d1 >= vm.dur - 0.5)
    if (sliced && Math.abs(px - mx(r.d0)) < 6) dragRef.current = { mode: "l" }
    else if (sliced && Math.abs(px - mx(r.d1)) < 6) dragRef.current = { mode: "r" }
    else if (sliced && t > r.d0 && t < r.d1) dragRef.current = { mode: "move", offT: t - r.d0, w: r.d1 - r.d0 }
    else dragRef.current = { mode: "new", anchorT: t }
    ev.preventDefault()
  }
  useEffect(() => {
    const onMove = (ev: MouseEvent) => {
      const drag = dragRef.current
      const mini = miniRef.current
      if (!drag || !mini) return
      const t = mt(ev.clientX - mini.getBoundingClientRect().left)
      const r = rangeRef.current
      if (drag.mode === "new") setBrush(drag.anchorT, t)
      else if (drag.mode === "l") setBrush(t, r.d1)
      else if (drag.mode === "r") setBrush(r.d0, t)
      else {
        const s = Math.max(0, Math.min(vm.dur - drag.w, t - drag.offT))
        setBrush(s, s + drag.w)
      }
    }
    const onUp = () => {
      dragRef.current = null
    }
    window.addEventListener("mousemove", onMove)
    window.addEventListener("mouseup", onUp)
    return () => {
      window.removeEventListener("mousemove", onMove)
      window.removeEventListener("mouseup", onUp)
    }
  }, [mt, setBrush, vm.dur])

  // Wheel zoom on the lanes chart, centered on the cursor. Gated behind
  // Ctrl/⌘ (trackpad pinch also sets ctrlKey) so a plain wheel scrolls the
  // page through the chart instead of being trapped by zoom — otherwise the
  // tall waterfall swallows vertical scroll on long sessions. Native listener
  // because React's onWheel is passive and we must preventDefault to zoom.
  useEffect(() => {
    const svg = chartRef.current
    if (!svg) return
    const onWheel = (ev: WheelEvent) => {
      if (!ev.ctrlKey && !ev.metaKey) return // let the page scroll
      ev.preventDefault()
      const px = ev.clientX - svg.getBoundingClientRect().left
      if (px < GEO.l || px > w - GEO.r) return
      const r = rangeRef.current
      const t = r.d0 + ((px - GEO.l) / plotW) * (r.d1 - r.d0)
      const f = ev.deltaY > 0 ? 1.25 : 0.8
      setBrush(t - (t - r.d0) * f, t + (r.d1 - t) * f)
    }
    svg.addEventListener("wheel", onWheel, { passive: false })
    return () => svg.removeEventListener("wheel", onWheel)
  }, [w, plotW, setBrush])

  // Crosshair + hover tooltip state. The bars themselves are memoized below,
  // so mousemove only re-renders the overlay — and the overlay updates are
  // rAF-throttled too, so a fast mouse costs at most one render per frame.
  const [crossPx, setCrossPx] = useState<number | null>(null)
  const [tip, setTip] = useState<{ x: number; y: number; cid: string } | null>(null)
  const [pushCross, cancelPendingCross] = useRafThrottle<number | null>(setCrossPx)
  const [pushTip, cancelPendingTip] = useRafThrottle<{ x: number; y: number; cid: string } | null>(setTip)

  const chartH = GEO.t + vm.convIds.length * (LANE_H + LANE_GAP) + GEO.b

  // Time gridlines for the current slice.
  const ticks = useMemo(() => {
    const steps = [1, 5, 10, 30, 60, 120, 300, 600, 1800, 3600]
    const step = steps.find((s) => span / s <= 14) ?? 3600
    const out: number[] = []
    const startMs = Math.ceil((vm.t0ms + d0 * 1000) / (step * 1000)) * step * 1000
    for (let ms = startMs; ms <= vm.t0ms + d1 * 1000; ms += step * 1000) out.push((ms - vm.t0ms) / 1000)
    return out
  }, [vm.t0ms, d0, d1, span])

  // Lanes + bars (rules 2-3): per conversation, a bar per span split at TTFC
  // (solid head, gradient streaming tail) and a hatched gap between
  // consecutive spans — harness/tool time, never drawn for server tools.
  //
  // Level-of-detail: when zoomed out far enough that individual bars project
  // to sub-pixel widths, runs of adjacent sub-pixel bars merge into ONE rect
  // per run (and the sub-pixel gaps between them are skipped). Pure visual —
  // the merged blob is what thousands of overlapping 2px rects rendered as
  // anyway, minus the DOM weight; zooming in past the threshold restores the
  // individual bars with their full click/hover behavior. A merged rect
  // carries an SVG <title> with the run size so it still explains itself.
  const lanes = useMemo(() => {
    const MIN_BAR_PX = 1.5
    const groups: React.ReactNode[] = []
    vm.convIds.forEach((conv, i) => {
      const yTop = GEO.t + i * (LANE_H + LANE_GAP)
      const col = vm.colors.get(conv) ?? "var(--text-3)"
      const flow = vm.byConv.get(conv) ?? []
      // Each lane's nodes collect into their own array so the whole lane can
      // be wrapped in a <g> and dimmed when another sub-agent is focused.
      const nodes: React.ReactNode[] = []
      if (i % 2 === 0) {
        nodes.push(
          <rect
            key={`bg-${conv}`}
            x={GEO.l}
            y={yTop - LANE_GAP / 2}
            width={plotW}
            height={LANE_H + LANE_GAP}
            style={{ fill: "var(--bg-2)" }}
            opacity={0.5}
          />,
        )
      }
      nodes.push(
        <text
          key={`lb-${conv}`}
          x={GEO.l - 12}
          y={yTop + LANE_H / 2 + 4}
          textAnchor="end"
          className="cursor-pointer hover:underline"
          style={{ fill: col, font: `${focusedConv === conv ? "600 " : ""}11px var(--font-mono)` }}
          onClick={() => onFocus(focusedConv === conv ? null : conv)}
        >
          <title>{conv === vm.sid ? "Focus the main session" : "Focus this sub-agent"}</title>
          {conv === vm.sid ? "◆ session" : dispName(conv)}
        </text>,
      )
      // Pending merged run of sub-pixel bars: [x1, x2) plus the run size.
      let run: { x1: number; x2: number; n: number; key: string } | null = null
      const flushRun = () => {
        if (!run) return
        nodes.push(
          <rect
            key={`md-${run.key}`}
            x={run.x1}
            y={yTop + 2}
            width={Math.max(1, run.x2 - run.x1)}
            height={LANE_H - 4}
            rx={2.5}
            style={{ fill: col }}
            opacity={0.85}
          >
            <title>{`${run.n} requests — zoom in for detail`}</title>
          </rect>,
        )
        run = null
      }
      flow.forEach((s, j) => {
        const nx = flow[j + 1]
        if (s.tEnd < d0 || s.t > d1) {
          // Bar outside the slice — but a gap trailing a bar that ended left
          // of the slice can still reach into view.
          if (nx && nx.t > s.tEnd && nx.t > d0 && s.tEnd < d1) {
            const g1 = clampX(xz(s.tEnd))
            const g2 = clampX(xz(nx.t))
            if (g2 - g1 >= MIN_BAR_PX) {
              nodes.push(
                <rect
                  key={`gap-${s.cid}`}
                  x={g1}
                  y={yTop + 7}
                  width={Math.max(1, g2 - g1)}
                  height={LANE_H - 14}
                  fill="url(#lifecycle-hatch)"
                  opacity={0.55}
                />,
              )
            }
          }
          return
        }
        const x1 = clampX(xz(s.t))
        const xEnd = clampX(xz(s.tEnd))
        const gapPx = nx && nx.t > s.tEnd ? clampX(xz(nx.t)) - xEnd : 0

        if (xEnd - x1 < MIN_BAR_PX) {
          // Sub-pixel bar: extend or start a merged run.
          if (run && x1 - run.x2 <= MIN_BAR_PX) {
            run.x2 = Math.max(run.x2, xEnd, x1 + 0.5)
            run.n++
          } else {
            flushRun()
            run = { x1, x2: Math.max(xEnd, x1 + 0.5), n: 1, key: s.cid }
          }
          // The gap after a merged member only renders when it is visibly
          // wide; sub-pixel gaps stay inside the blob.
          if (nx && nx.t > s.tEnd && nx.t > d0 && gapPx >= MIN_BAR_PX) {
            flushRun()
            nodes.push(
              <rect
                key={`gap-${s.cid}`}
                x={xEnd}
                y={yTop + 7}
                width={Math.max(1, clampX(xz(nx.t)) - xEnd)}
                height={LANE_H - 14}
                fill="url(#lifecycle-hatch)"
                opacity={0.55}
              />,
            )
          }
          return
        }

        flushRun()
        if (nx && nx.t > s.tEnd && nx.t > d0 && s.tEnd < d1) {
          const g1 = clampX(xz(s.tEnd))
          const g2 = clampX(xz(nx.t))
          if (g2 > g1) {
            nodes.push(
              <rect
                key={`gap-${s.cid}`}
                x={g1}
                y={yTop + 7}
                width={Math.max(1, g2 - g1)}
                height={LANE_H - 14}
                fill="url(#lifecycle-hatch)"
                opacity={0.55}
              />,
            )
          }
        }
        const x2 = Math.max(x1 + 2, xEnd)
        const tt = s.ttfc_ms != null ? s.ttfc_ms / 1000 : null
        const xs = tt != null ? Math.max(x1, Math.min(x2, clampX(xz(s.t + tt)))) : x2
        nodes.push(
          <rect
            key={`hd-${s.cid}`}
            x={x1}
            y={yTop + 2}
            width={xs - x1}
            height={LANE_H - 4}
            rx={2.5}
            style={{ fill: col, stroke: "var(--bg)" }}
            strokeWidth={0.5}
            className="cursor-pointer hover:brightness-125"
            onClick={() => onOpen(s.cid)}
            onMouseMove={(ev) => pushTip({ x: ev.clientX, y: ev.clientY, cid: s.cid })}
            onMouseLeave={() => {
              cancelPendingTip()
              setTip(null)
            }}
          />,
        )
        if (xs < x2) {
          nodes.push(
            <rect
              key={`tl-${s.cid}`}
              x={xs}
              y={yTop + 2}
              width={x2 - xs}
              height={LANE_H - 4}
              rx={2.5}
              fill={`url(#lifecycle-tail-${i})`}
              className="cursor-pointer hover:brightness-125"
              onClick={() => onOpen(s.cid)}
              onMouseMove={(ev) => pushTip({ x: ev.clientX, y: ev.clientY, cid: s.cid })}
              onMouseLeave={() => {
                cancelPendingTip()
                setTip(null)
              }}
            />,
          )
        }
      })
      flushRun()
      // Wrap the lane and dim it when a different sub-agent is focused, so the
      // focused flow reads clearly against the rest.
      const dim = focusedConv != null && conv !== focusedConv
      groups.push(
        <g key={`lane-${conv}`} opacity={dim ? 0.18 : 1} style={{ transition: "opacity 120ms ease" }}>
          {nodes}
        </g>,
      )
    })
    return groups
  }, [vm, dispName, d0, d1, plotW, xz, clampX, onOpen, pushTip, cancelPendingTip, focusedConv, onFocus])

  // Minimap content: time ticks + every span as a slim bar (main lane on
  // top, sub-agents below), under the brush overlay. The tick step adapts to
  // pixel density — an HH:MM label is ~36px, so steps walk a 1/2/5/10/15/30/60
  // minute ladder until labels get >= ~64px each and stop bleeding together.
  const miniBars = useMemo(() => {
    const nodes: React.ReactNode[] = []
    const pxPerSec = plotW / Math.max(1, vm.dur)
    const stepS = [60, 120, 300, 600, 900, 1800, 3600].find((s) => s * pxPerSec >= 64) ?? 7200
    const stepMs = stepS * 1000
    for (let ms = Math.ceil(vm.t0ms / stepMs) * stepMs; ms <= vm.t0ms + vm.dur * 1000; ms += stepMs) {
      const t = (ms - vm.t0ms) / 1000
      nodes.push(
        <g key={`mt-${ms}`}>
          <line
            x1={mx(t)}
            y1={MINI_AXIS - 3}
            x2={mx(t)}
            y2={MINI_AXIS + MINI_H}
            style={{ stroke: "var(--border)" }}
            strokeWidth={0.75}
          />
          <text
            x={mx(t)}
            y={MINI_AXIS - 6}
            textAnchor="middle"
            style={{ fill: "var(--text-4)", font: "10px var(--font-mono)" }}
          >
            {clockAt(vm.t0ms, t).slice(0, 5)}
          </text>
        </g>,
      )
    }
    for (const s of vm.spans) {
      const isMain = s.conversation_id === vm.sid
      nodes.push(
        <rect
          key={`mb-${s.cid}`}
          x={mx(s.t)}
          y={MINI_AXIS + (isMain ? 4 : 16)}
          width={Math.max(1, mx(s.tEnd) - mx(s.t))}
          height={isMain ? 10 : MINI_H - 22}
          style={{ fill: vm.colors.get(s.conversation_id) ?? "var(--text-3)" }}
          opacity={0.7}
        />,
      )
    }
    return nodes
  }, [vm, mx, plotW])

  const tipSpan = tip ? vm.byCid.get(tip.cid) : undefined
  const crossT = crossPx != null ? d0 + ((crossPx - GEO.l) / plotW) * span : null

  return (
    <div className="flex flex-col gap-2">
      <div className="flex items-baseline gap-2 px-0.5">
        <span className="text-[13px] font-medium">Timeline</span>
        <span className="text-[11px] text-[color:var(--text-4)]">
          requests per conversation — drag the minimap to slice; ⌘/ctrl-scroll to zoom
        </span>
      </div>
      <PanelCard>
        <div ref={boxRef} className="overflow-x-auto py-2">
          {/* minimap (full session, fixed axis) */}
          <svg
            ref={miniRef}
            width={w}
            height={MINI_H + MINI_AXIS}
            className="block cursor-crosshair select-none"
            onMouseDown={onMiniDown}
            onDoubleClick={reset}
            aria-label="Timeline minimap — drag to slice"
          >
            <rect
              x={GEO.l}
              y={MINI_AXIS}
              width={plotW}
              height={MINI_H}
              style={{ fill: "var(--bg-2)", stroke: "var(--border)" }}
            />
            {miniBars}
            {!full && (
              <g>
                <rect x={GEO.l} y={MINI_AXIS} width={Math.max(0, mx(d0) - GEO.l)} height={MINI_H} style={{ fill: "var(--bg)" }} opacity={0.62} />
                <rect x={mx(d1)} y={MINI_AXIS} width={Math.max(0, GEO.l + plotW - mx(d1))} height={MINI_H} style={{ fill: "var(--bg)" }} opacity={0.62} />
                <rect
                  x={mx(d0)}
                  y={MINI_AXIS}
                  width={mx(d1) - mx(d0)}
                  height={MINI_H}
                  rx={3}
                  style={{ fill: "var(--accent)", stroke: "var(--accent)", cursor: "grab" }}
                  fillOpacity={0.1}
                  strokeWidth={1.25}
                />
                <rect x={mx(d0) - 4.5} y={MINI_AXIS} width={9} height={MINI_H} rx={3} style={{ fill: "var(--accent)", cursor: "ew-resize" }} opacity={0.85} />
                <rect x={mx(d1) - 4.5} y={MINI_AXIS} width={9} height={MINI_H} rx={3} style={{ fill: "var(--accent)", cursor: "ew-resize" }} opacity={0.85} />
              </g>
            )}
          </svg>

          {/* slice readout — interchangeable full-window / slice duration, centered */}
          <div className="flex justify-center items-center gap-2.5 text-[12px] text-[color:var(--text-3)]">
            <span className="mono tnum font-semibold" style={full ? undefined : { color: "var(--accent)" }}>
              {full
                ? `${clockAt(vm.t0ms, 0)} → ${clockAt(vm.t0ms, vm.dur)} · ${fmt.uptime(vm.dur * 1000)}`
                : `slice ${clockAt(vm.t0ms, d0)} → ${clockAt(vm.t0ms, d1)} · ${fmt.uptime((d1 - d0) * 1000)}`}
            </span>
            {!full && (
              <button
                type="button"
                onClick={reset}
                className="inline-flex items-center gap-1 rounded-full border border-[color:var(--border)] px-2 py-px text-[11px] text-[color:var(--text-3)] hover:text-[color:var(--text)] hover:bg-[color:var(--hover)]"
              >
                <RotateCcw size={10} /> reset
              </button>
            )}
          </div>

          {/* lanes chart */}
          <svg
            ref={chartRef}
            width={w}
            height={chartH}
            className="block select-none"
            onMouseMove={(ev) => {
              const px = ev.clientX - ev.currentTarget.getBoundingClientRect().left
              pushCross(px >= GEO.l && px <= w - GEO.r ? px : null)
            }}
            onMouseLeave={() => {
              // Direct + cancel: a queued move frame must not resurrect the
              // crosshair after the pointer left the chart.
              cancelPendingCross()
              setCrossPx(null)
              cancelPendingTip()
              setTip(null)
            }}
            aria-label="Per-conversation request timeline"
          >
            <defs>
              <pattern id="lifecycle-hatch" width={6} height={6} patternUnits="userSpaceOnUse" patternTransform="rotate(45)">
                <line x1={0} y1={0} x2={0} y2={6} style={{ stroke: "var(--text-4)" }} strokeWidth={0.8} opacity={0.7} />
              </pattern>
              {vm.convIds.map((conv, i) => (
                <linearGradient key={conv} id={`lifecycle-tail-${i}`} x1="0" y1="0" x2="1" y2="0">
                  <stop offset="0" style={{ stopColor: vm.colors.get(conv), stopOpacity: 0.75 }} />
                  <stop offset="1" style={{ stopColor: vm.colors.get(conv), stopOpacity: 0.16 }} />
                </linearGradient>
              ))}
            </defs>
            {ticks.map((t) => (
              <g key={t}>
                <line x1={xz(t)} y1={GEO.t - 14} x2={xz(t)} y2={chartH - GEO.b} style={{ stroke: "var(--border)" }} strokeDasharray="2 4" />
                <text x={xz(t)} y={GEO.t - 18} textAnchor="middle" style={{ fill: "var(--text-4)", font: "10.5px var(--font-mono)" }}>
                  {span / 14 < 60 ? clockAt(vm.t0ms, t) : clockAt(vm.t0ms, t).slice(0, 5)}
                </text>
              </g>
            ))}
            {lanes}
            {crossPx != null && crossT != null && (
              <g pointerEvents="none">
                <line x1={crossPx} x2={crossPx} y1={GEO.t - 6} y2={chartH - GEO.b} style={{ stroke: "var(--accent)" }} strokeWidth={1} opacity={0.65} strokeDasharray="3 3" />
                <rect x={Math.max(GEO.l, Math.min(w - GEO.r - 96, crossPx - 48))} y={GEO.t - 16} width={96} height={17} rx={8.5} style={{ fill: "var(--bg-2)", stroke: "var(--accent)" }} strokeWidth={0.75} />
                <text x={Math.max(GEO.l, Math.min(w - GEO.r - 96, crossPx - 48)) + 48} y={GEO.t - 4} textAnchor="middle" style={{ fill: "var(--accent)", font: "600 10.5px var(--font-mono)" }}>
                  {clockMsAt(vm.t0ms, crossT)}
                </text>
              </g>
            )}
          </svg>
        </div>

        {/* legend + names toggle */}
        <div className="flex justify-center items-center gap-2.5 flex-wrap px-4 py-2 border-t border-[color:var(--border)] text-[11.5px] text-[color:var(--text-3)]">
          <LegendChip>
            <span className="inline-block w-2.5 h-2.5 rounded-[2px] align-[-1px] mr-1.5" style={{ background: "var(--warn)" }} />
            solid = TTFC head
          </LegendChip>
          <LegendChip>
            <span
              className="inline-block w-2.5 h-2.5 rounded-[2px] align-[-1px] mr-1.5"
              style={{ background: "linear-gradient(90deg, color-mix(in oklab, var(--warn) 75%, transparent), color-mix(in oklab, var(--warn) 16%, transparent))" }}
            />
            fade = streaming tail
          </LegendChip>
          <LegendChip>
            <span
              className="inline-block w-2.5 h-2.5 rounded-[2px] align-[-1px] mr-1.5"
              style={{ background: "repeating-linear-gradient(45deg, var(--text-4), var(--text-4) 2px, transparent 2px, transparent 5px)" }}
            />
            hatched = inter-request gap
          </LegendChip>
          <button
            type="button"
            onClick={onToggleNames}
            className="rounded-full border border-[color:var(--border)] bg-[color:var(--bg-1)] px-2.5 py-0.5 hover:text-[color:var(--text)] hover:bg-[color:var(--hover)]"
            title="Toggle alias names vs raw agent ids"
          >
            names: {useNames ? "on" : "off"}
          </button>
        </div>

        {/* agents strip — click to drill into one conversation's requests */}
        {vm.convIds.length > 1 && (
          <div className="flex items-center gap-2 flex-wrap px-4 py-2 border-t border-[color:var(--border)]">
            <span className="text-[11px] uppercase tracking-[0.06em] text-[color:var(--text-4)] mr-0.5">Agents</span>
            {vm.convIds.map((conv) => {
              const on = focusedConv === conv
              const col = vm.colors.get(conv) ?? "var(--text-3)"
              const convCost = vm.spans.reduce(
                (a, sp) => a + (sp.conversation_id === conv ? (sp.usage.cost_usd ?? 0) : 0),
                0,
              )
              return (
                <button
                  key={conv}
                  type="button"
                  onClick={() => onFocus(on ? null : conv)}
                  aria-pressed={on}
                  title={conv === vm.sid ? conv : `Focus ${dispName(conv)} · ${conv}`}
                  className="inline-flex items-center gap-1.5 rounded-full border px-2.5 py-0.5 text-[11.5px] hover:bg-[color:var(--hover)]"
                  style={{
                    borderColor: on ? col : "var(--border)",
                    background: on ? "color-mix(in oklab, " + col + " 14%, transparent)" : "var(--bg-1)",
                    color: on ? "var(--text)" : "var(--text-2)",
                  }}
                >
                  <span className="inline-block w-2 h-2 rounded-[2px] shrink-0" style={{ background: col }} />
                  {conv === vm.sid ? "session" : dispName(conv)}
                  {convCost > 0 && (
                    <span className="mono tnum text-[10.5px] text-[color:var(--text-4)]">{fmt.usd(convCost)}</span>
                  )}
                </button>
              )
            })}
            {focusedConv != null && (
              <button
                type="button"
                onClick={() => onFocus(null)}
                className="ml-1 rounded-full border border-[color:var(--border)] bg-[color:var(--bg-1)] px-2.5 py-0.5 text-[11.5px] text-[color:var(--text-3)] hover:bg-[color:var(--hover)] hover:text-[color:var(--text)]"
              >
                clear focus
              </button>
            )}
          </div>
        )}
      </PanelCard>

      {/* hover tooltip */}
      {tip && tipSpan && (
        <div
          className="fixed z-50 max-w-[420px] rounded-[var(--radius)] border border-[color:var(--border)] bg-[color:var(--bg-1)] px-3 py-2 text-[12px] pointer-events-none shadow-[var(--shadow-md)]"
          style={{
            left: Math.min(tip.x + 14, window.innerWidth - 320),
            top: Math.min(tip.y + 14, window.innerHeight - 110),
          }}
        >
          <b style={{ color: vm.colors.get(tipSpan.conversation_id) }}>{dispName(tipSpan.conversation_id)}</b>
          <div className="text-[color:var(--text-2)]">
            {clockAt(vm.t0ms, tipSpan.t)} · {fmt.ms(tipSpan.latency_ms)} (ttfc {fmt.ms(tipSpan.ttfc_ms)}) · in{" "}
            {fmt.compact(tipSpan.usage.input)} → out {fmt.compact(tipSpan.usage.output)}
            {(tipSpan.usage.cost_usd ?? 0) > 0 && <> · {fmt.usd(tipSpan.usage.cost_usd)}</>}
          </div>
          <div className="text-[color:var(--text-3)]">
            {(() => {
              const ntc = (tipSpan.output_parts ?? []).filter((p) => p.type === "tool_call").length
              return ntc ? `${ntc} tool call${ntc > 1 ? "s" : ""}` : (tipSpan.finish_reason ?? "")
            })()}{" "}
            <span className="text-[color:var(--text-4)]">— click for detail</span>
          </div>
        </div>
      )}
    </div>
  )
}

function LegendChip({ children }: { children: React.ReactNode }) {
  return (
    <span className="inline-flex items-center rounded-full border border-[color:var(--border)] bg-[color:var(--bg-1)] px-2.5 py-0.5">
      {children}
    </span>
  )
}

// ─────────────────────────────────────────────────────────────────────────
// Slice-bound messages table
// ─────────────────────────────────────────────────────────────────────────

// SessionMessagesPanel embeds the shared paginated MessageEntry table at the
// bottom of the timeline section, pre-filtered to this session. The time
// window binds to the slicer: full session by default; once the operator
// slices, the table re-queries with from/to = the slice window (absolute
// timestamps derived from t0 + offset), debounced 300ms so the brush settles
// before a query fires. Keyset pagination runs within the current window; a
// row click opens the span inspector for that correlation id. On the fixture
// fallback (no backend) rows are synthesized from the loaded spans so the
// table still demonstrates the slice binding honestly.
function SessionMessagesPanel({
  vm,
  slice,
  source,
  onOpen,
  focusedConv,
  dispName,
}: {
  vm: ViewModel
  slice: SliceRange | null
  source: "api" | "fixture"
  onOpen: (cid: string) => void
  // focusedConv (sub-agent drilldown) narrows the table to one conversation;
  // null = the whole session. dispName + vm.colors back the Agent column.
  focusedConv: string | null
  dispName: (conv: string) => string
}) {
  const nav = useNavigate()
  const debounced = useDebounced(slice, 300)
  const d0 = debounced?.d0 ?? 0
  const d1 = debounced?.d1 ?? vm.dur
  const full = d0 <= 0.5 && d1 >= vm.dur - 0.5
  const [limit, setLimit] = useState<number>(SESSION_MESSAGES_PAGE_SIZE)
  const isFixture = source === "fixture"

  // Model header filter (multi-select, OR). Options come from the session's
  // own loaded spans — complete session coverage, no extra fetch — which is
  // also why Model is the only filterable column here: SessionSpan carries no
  // provider/protocol/configuration/tags to enumerate. API mode pushes the
  // selection into the server-side pager filters; fixture mode narrows the
  // synthesized rows client-side.
  const [modelSel, setModelSel] = useState<string[]>([])
  const modelOptions = useMemo(
    () => [...new Set(vm.spans.map((s) => s.model).filter((m): m is string => !!m))].sort(),
    [vm.spans],
  )

  // Query filters: session-scoped always; conversation-scoped when a sub-agent
  // is focused; from/to only when sliced — the full session needs no time
  // bounds, session_id already scopes the scan.
  const filters = useMemo<MessageFilters>(() => {
    const f: MessageFilters = { sessionId: vm.sid }
    if (focusedConv) f.conversationId = focusedConv
    if (modelSel.length > 0) f.models = modelSel
    if (debounced) {
      f.from = new Date(vm.t0ms + debounced.d0 * 1000).toISOString()
      f.to = new Date(vm.t0ms + debounced.d1 * 1000).toISOString()
    }
    return f
  }, [vm.sid, vm.t0ms, debounced, focusedConv, modelSel])

  const pager = useMessagesPager({
    filters,
    limit,
    enabled: !isFixture,
    onUnauthorized: () => nav("/login", { replace: true }),
  })

  // Fixture path: rows derived client-side from the loaded spans (newest
  // first, windowed + paged locally) — there is no backend to query.
  const fixtureRows = useMemo<MessageEntry[]>(() => {
    if (!isFixture) return []
    return vm.spans
      .filter((s) => s.t >= d0 && s.t <= d1)
      .filter((s) => !focusedConv || s.conversation_id === focusedConv)
      .filter((s) => modelSel.length === 0 || (s.model != null && modelSel.includes(s.model)))
      .slice()
      .reverse()
      .map((s) => ({
        event_id: s.cid,
        correlation_id: s.cid,
        conversation_id: s.conversation_id,
        at: s.at,
        status_code: s.status ?? 0,
        model: s.model ?? undefined,
        duration_ms: s.latency_ms ?? 0,
        tokens_in: s.usage.input ?? 0,
        tokens_out: s.usage.output ?? 0,
      }))
  }, [isFixture, vm, d0, d1, focusedConv, modelSel])
  // Fixture paging is tagged with the row set + page size it belongs to, so a
  // window or size change derives page one during render (no reset effect).
  const [fixNav, setFixNav] = useState<{ rows: MessageEntry[]; limit: number; page: number } | null>(null)
  const fixPage = fixNav && fixNav.rows === fixtureRows && fixNav.limit === limit ? fixNav.page : 0
  const setFixPage = (page: number) => setFixNav({ rows: fixtureRows, limit, page })

  const entries = isFixture ? fixtureRows.slice(fixPage * limit, (fixPage + 1) * limit) : pager.entries
  const status = isFixture ? ("ok" as const) : pager.status
  const pageIndex = isFixture ? fixPage : pager.pageIndex
  const hasNext = isFixture ? (fixPage + 1) * limit < fixtureRows.length : pager.hasNext
  const onNext = isFixture ? () => setFixPage(fixPage + 1) : pager.onNext
  const onPrev = isFixture ? () => setFixPage(Math.max(0, fixPage - 1)) : pager.onPrev
  const total = isFixture ? fixtureRows.length : pager.total

  return (
    <PanelCard>
      <PanelHead
        title="Messages"
        sub={
          <>
            <span className="mono tnum font-semibold" style={full ? undefined : { color: "var(--accent)" }}>
              {full
                ? `${clockAt(vm.t0ms, 0)} → ${clockAt(vm.t0ms, vm.dur)} · full session`
                : `slice ${clockAt(vm.t0ms, d0)} → ${clockAt(vm.t0ms, d1)} · ${fmt.uptime((d1 - d0) * 1000)}`}
            </span>
            {" "}· slice the timeline to narrow · click a row to open the span
          </>
        }
      />
      {status === "error" && (
        <div className="m-3 rounded-[var(--radius-lg)] border p-4 text-[13px]" style={{ color: "var(--err)", background: "var(--err-bg)" }}>
          Failed to load messages: <span className="mono">{pager.err}</span>
        </div>
      )}
      <MessagesTableView
        entries={entries}
        status={status}
        limit={limit}
        emptyText={
          focusedConv
            ? "No requests for this agent in view."
            : full
              ? "No requests recorded for this session."
              : "No requests in the sliced window."
        }
        onRowClick={(e) => onOpen(e.correlation_id || e.event_id)}
        agent={(e) => {
          const conv = e.conversation_id ?? vm.sid
          return { label: dispName(conv), color: vm.colors.get(conv) }
        }}
        filters={{
          model: { values: modelSel, options: modelOptions, onChange: setModelSel, allowSelectAll: true, emptyText: "No models in session" },
        }}
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
  )
}

// SessionDataTabs puts the session's data tables behind one tab strip:
// Messages — the slice-bound table, the priority view and the default — and
// Tools — the per-tool aggregates plus the joined call ledger. Only the
// active tab mounts, so the tools aggregation costs nothing while browsing
// messages.
function SessionDataTabs({
  vm,
  slice,
  source,
  dispName,
  onOpenMessage,
  onOpenSpan,
  onOpenSecurity,
  focusedConv,
}: {
  vm: ViewModel
  slice: SliceRange | null
  source: "api" | "fixture"
  dispName: (conv: string) => string
  onOpenMessage: (cid: string) => void
  onOpenSpan: (cid: string) => void
  // onOpenSecurity opens a span on the Security tab (the findings list); kept
  // distinct from onOpenSpan, which the tool ledger uses to land on Output.
  onOpenSecurity: (cid: string) => void
  focusedConv: string | null
}) {
  const [tab, setTab] = useState<"messages" | "tools" | "security">("messages")
  // Request count reflects the focus: the messages tab is conversation-scoped
  // when a sub-agent is drilled into.
  const msgCount = focusedConv ? (vm.byConv.get(focusedConv)?.length ?? 0) : vm.spans.length

  // Eagerly load the session's findings (cached + shared with the Security
  // panel) so the Security tab carries a count badge before it's opened. Fixture
  // sessions have no findings endpoint, so they stay unbadged. Keyed-state, set
  // only in the async callback (no setState-in-effect during render).
  const isFixture = source !== "api"
  const [findCount, setFindCount] = useState<{ sid: string; n: number } | null>(null)
  useEffect(() => {
    if (isFixture) return
    let cancelled = false
    loadSessionFindings(vm.sid)
      .then((rows) => { if (!cancelled) setFindCount({ sid: vm.sid, n: rows.length }) })
      .catch(() => { if (!cancelled) setFindCount({ sid: vm.sid, n: 0 }) })
    return () => { cancelled = true }
  }, [vm.sid, isFixture])
  const securityCount = findCount && findCount.sid === vm.sid ? findCount.n : 0

  return (
    <div className="flex flex-col gap-3.5">
      <div role="tablist" aria-label="Session detail" className="flex border-b border-[color:var(--border)]">
        <IOTab
          on={tab === "messages"}
          onClick={() => setTab("messages")}
          label="Messages"
          meta={`${msgCount} request${msgCount === 1 ? "" : "s"}${focusedConv ? ` · ${dispName(focusedConv)}` : ""}`}
        />
        <IOTab
          on={tab === "tools"}
          onClick={() => setTab("tools")}
          label="Tools"
          meta={`${vm.ledger.length} call${vm.ledger.length === 1 ? "" : "s"}`}
        />
        <IOTab
          on={tab === "security"}
          onClick={() => setTab("security")}
          label="Security"
          meta="detector findings"
          badge={securityCount}
        />
      </div>
      {tab === "messages" && (
        <SessionMessagesPanel vm={vm} slice={slice} source={source} onOpen={onOpenMessage} focusedConv={focusedConv} dispName={dispName} />
      )}
      {tab === "tools" && (
        <>
          <div className="grid md:grid-cols-2 gap-3.5 items-start">
            <ToolTable vm={vm} kind="client" />
            <ToolTable vm={vm} kind="server" />
          </div>
          <LedgerTable vm={vm} dispName={dispName} onOpen={onOpenSpan} />
        </>
      )}
      {tab === "security" && <SessionSecurityPanel sid={vm.sid} source={source} onOpenSpan={onOpenSecurity} />}
    </div>
  )
}

// SessionSecurityPanel is the session view's Security tab: every detector
// finding in this session, scoped by session id, in the SAME FindingsTable the
// top-level Security page mounts (showSession hidden — every row is this
// session). A finding's row opens its source request in the session's in-place
// span inspector via onOpenSpan; that inspector only resolves spans this session
// carries, so a finding whose span wasn't captured simply doesn't open. Findings
// load lazily when the tab is first opened, and only against a live backend (the
// fixture session has no findings endpoint).
function SessionSecurityPanel({
  sid,
  source,
  onOpenSpan,
}: {
  sid: string
  source: "api" | "fixture"
  onOpenSpan: (cid: string) => void
}) {
  // Keyed by sid so the panel derives "loading" during render rather than
  // resetting via setState-in-effect; the effect only setStates in callbacks.
  const [res, setRes] = useState<{ sid: string; rows: FindingRow[] | null; err: string | null } | null>(null)
  const nav = useNavigate()
  // A fixture session has no findings endpoint — short-circuit to an empty list
  // during render (not via setState-in-effect) so the table shows its empty state.
  const isFixture = source !== "api"

  useEffect(() => {
    if (isFixture) return
    let cancelled = false
    loadSessionFindings(sid)
      .then((rows) => {
        if (!cancelled) setRes({ sid, rows, err: null })
      })
      .catch((e) => {
        if (cancelled) return
        if (e instanceof UnauthorizedError) {
          nav("/login", { replace: true })
          return
        }
        setRes({ sid, rows: null, err: e instanceof Error ? e.message : String(e) })
      })
    return () => {
      cancelled = true
    }
  }, [sid, isFixture, nav])

  const ready = isFixture
    ? { sid, rows: [] as FindingRow[], err: null }
    : res && res.sid === sid
      ? res
      : null
  const status: "loading" | "ok" | "error" = !ready ? "loading" : ready.err ? "error" : "ok"

  return (
    <PanelCard>
      {status === "error" && (
        <div className="m-3 rounded-[var(--radius-lg)] border p-4 text-[13px]" style={{ color: "var(--err)", background: "var(--err-bg)" }}>
          Failed to load findings: <span className="mono">{ready?.err}</span>
        </div>
      )}
      <FindingsTable
        findings={ready?.rows ?? []}
        status={status}
        limit={SESSION_MESSAGES_PAGE_SIZE}
        emptyText="No findings in this session — nothing flagged, or the scanner is disabled."
        onOpenMessage={(f) => onOpenSpan(f.correlation_id)}
        // Every row is this session; the Session link column is hidden, so this
        // pivot never fires — wired to a self-navigation only for completeness.
        onOpenSession={(s) => nav(`/sessions/${encodeURIComponent(s)}`)}
        showSession={false}
      />
    </PanelCard>
  )
}

// ─────────────────────────────────────────────────────────────────────────
// Tool tables (rule 5) + ledger
// ─────────────────────────────────────────────────────────────────────────

// ToolTable aggregates the ledger per tool name: client-executed tools report
// the median of joined exec times (no gap heuristics); server-executed tools
// report the median hosting-span latency instead, since the tool ran inside
// the request.
const ToolTable = memo(function ToolTable({ vm, kind }: { vm: ViewModel; kind: "client" | "server" }) {
  const rows = useMemo(() => {
    const agg = new Map<string, { calls: number; convs: Set<string>; execs: number[]; hosts: number[] }>()
    for (const r of vm.ledger) {
      if (r.server !== (kind === "server")) continue
      let a = agg.get(r.name)
      if (!a) {
        a = { calls: 0, convs: new Set(), execs: [], hosts: [] }
        agg.set(r.name, a)
      }
      a.calls++
      a.convs.add(r.conv)
      if (r.execS != null) a.execs.push(r.execS)
      if (r.hostMs != null) a.hosts.push(r.hostMs)
    }
    return [...agg.entries()].sort((a, b) => b[1].calls - a[1].calls)
  }, [vm, kind])

  return (
    <PanelCard>
      <PanelHead
        title={kind === "client" ? "Tools — client-executed" : "Tools — server-executed"}
        sub={kind === "client" ? "result returns in a later request" : "provider runs it inline within the request"}
      />
      <TableScroll className="min-w-0">
        <thead>
          <tr className="text-[11px] uppercase tracking-[0.07em] text-[color:var(--text-3)]">
            <th scope="col" className="text-left font-medium px-4 py-2">Tool</th>
            <th scope="col" className="text-right font-medium px-4 py-2">Calls</th>
            <th scope="col" className="text-right font-medium px-4 py-2">Convs</th>
            <th scope="col" className="text-right font-medium px-4 py-2">{kind === "client" ? "Median exec" : "Median host span"}</th>
          </tr>
        </thead>
        <tbody>
          {rows.map(([name, a]) => (
            <tr key={name} className="border-t border-[color:var(--border)]">
              <td className="px-4 py-2 font-medium mono text-[12px]">{name}</td>
              <td className="mono tnum text-[12px] text-right px-4 py-2">{a.calls}</td>
              <td className="mono tnum text-[12px] text-right px-4 py-2">{a.convs.size}</td>
              <td className="mono tnum text-[12px] text-right px-4 py-2">
                {kind === "client" ? (
                  a.execs.length ? (
                    <>
                      {median(a.execs)!.toFixed(1)}s <span className="text-[color:var(--text-4)]">(n={a.execs.length})</span>
                    </>
                  ) : (
                    <span className="text-[color:var(--text-4)]">no joined result</span>
                  )
                ) : (
                  fmt.ms(median(a.hosts))
                )}
              </td>
            </tr>
          ))}
          {rows.length === 0 && (
            <tr className="border-t border-[color:var(--border)]">
              <td colSpan={4} className="px-4 py-6 text-center text-[12px] text-[color:var(--text-4)]">
                {kind === "client" ? "no client-executed tool calls" : "no server-executed tool calls"}
              </td>
            </tr>
          )}
        </tbody>
      </TableScroll>
    </PanelCard>
  )
})

// LEDGER_PREVIEW caps the rows the ledger renders by default. A 688-message
// session can carry thousands of tool calls; mounting them all as table rows
// dominated the initial paint. The cap is presentation-only — stats and the
// ledger joins always run over the full list — and "show all" lifts it.
const LEDGER_PREVIEW = 250

// LedgerTable is the flat joined ledger: one row per tool_call, raw arguments
// (rule 7 default path — opaque vendor JSON, truncated, never field-picked).
// List spans are envelope-only, so the args column may have only the size to
// show; the calling span's inspector (click the row) fetches the full args.
const LedgerTable = memo(function LedgerTable({
  vm,
  dispName,
  onOpen,
}: {
  vm: ViewModel
  dispName: (conv: string) => string
  onOpen: (cid: string) => void
}) {
  const [showAll, setShowAll] = useState(false)
  const rows = showAll ? vm.ledger : vm.ledger.slice(0, LEDGER_PREVIEW)
  return (
    <PanelCard>
      <PanelHead title="Tool call ledger" sub="joined by call ID · arguments shown raw" />
      <div className="max-h-[430px] overflow-y-auto">
        <TableScroll>
          <thead className="sticky top-0 bg-[color:var(--bg-1)] z-[1]">
            <tr className="text-[11px] uppercase tracking-[0.07em] text-[color:var(--text-3)]">
              <th scope="col" className="text-left font-medium px-4 py-2">Call ID</th>
              <th scope="col" className="text-left font-medium px-4 py-2">Tool</th>
              <th scope="col" className="text-left font-medium px-4 py-2">Conversation</th>
              <th scope="col" className="text-left font-medium px-4 py-2">Called</th>
              <th scope="col" className="text-left font-medium px-4 py-2">Response</th>
              <th scope="col" className="text-right font-medium px-4 py-2">Exec</th>
              <th scope="col" className="text-left font-medium px-4 py-2">Arguments (raw)</th>
            </tr>
          </thead>
          <tbody>
            {rows.map((r, i) => (
              <tr
                key={`${r.id}-${i}`}
                role="button"
                tabIndex={0}
                aria-label={`Open calling span for ${r.name}`}
                onClick={() => onOpen(r.callCid)}
                onKeyDown={(ev) => {
                  if (ev.key === "Enter" || ev.key === " ") {
                    ev.preventDefault()
                    onOpen(r.callCid)
                  }
                }}
                className="border-t border-[color:var(--border)] cursor-pointer hover:bg-[color:var(--hover)] focus-visible:outline-2 focus-visible:-outline-offset-2 focus-visible:outline-[color:var(--accent)]"
                title="Open the calling span"
              >
                <td className="mono text-[11px] px-4 py-2 text-[color:var(--text-4)]">{id8(r.id)}</td>
                <td className="mono text-[12px] px-4 py-2 font-medium">{r.name}</td>
                <td className="text-[12px] px-4 py-2" style={{ color: vm.colors.get(r.conv) }}>
                  {dispName(r.conv)}
                </td>
                <td className="mono text-[11.5px] px-4 py-2 whitespace-nowrap">{clockAt(vm.t0ms, r.callT)}</td>
                <td className="mono text-[11.5px] px-4 py-2 whitespace-nowrap">
                  {r.resultT != null ? clockAt(vm.t0ms, r.resultT) : <Dash />}
                </td>
                <td className="mono tnum text-[12px] text-right px-4 py-2 whitespace-nowrap">
                  {r.server ? (
                    <span className="text-[color:var(--text-4)]">in-span</span>
                  ) : r.execS != null ? (
                    `${r.execS.toFixed(1)}s`
                  ) : (
                    <span className="text-[color:var(--text-4)]">no result</span>
                  )}
                  {r.isError && (
                    <b className="ml-1.5" style={{ color: "var(--err)" }}>
                      ERR
                    </b>
                  )}
                </td>
                <td className="mono text-[10.5px] px-4 py-2 text-[color:var(--text-4)] max-w-[340px] overflow-hidden text-ellipsis whitespace-nowrap">
                  {r.args ? (
                    r.args.slice(0, 110)
                  ) : r.argsChars ? (
                    <span className="italic">{`{…} ${fmt.compact(r.argsChars)} chars — open span`}</span>
                  ) : null}
                </td>
              </tr>
            ))}
            {!showAll && vm.ledger.length > LEDGER_PREVIEW && (
              <tr className="border-t border-[color:var(--border)]">
                <td colSpan={7} className="px-4 py-2.5 text-center">
                  <button
                    type="button"
                    onClick={() => setShowAll(true)}
                    className="rounded-full border border-[color:var(--border)] bg-[color:var(--bg-1)] px-3 py-0.5 text-[11.5px] text-[color:var(--text-3)] hover:text-[color:var(--text)] hover:bg-[color:var(--hover)]"
                  >
                    show all {vm.ledger.length} tool calls (first {LEDGER_PREVIEW} shown)
                  </button>
                </td>
              </tr>
            )}
            {vm.ledger.length === 0 && (
              <tr className="border-t border-[color:var(--border)]">
                <td colSpan={7} className="px-4 py-6 text-center text-[12px] text-[color:var(--text-4)]">
                  no tool calls in the captured spans
                </td>
              </tr>
            )}
          </tbody>
        </TableScroll>
      </div>
    </PanelCard>
  )
})

// ─────────────────────────────────────────────────────────────────────────
// Span inspector modal
// ─────────────────────────────────────────────────────────────────────────

function SpanInspector({
  vm,
  source,
  cid,
  ioTab,
  onTab,
  dispName,
  onSelect,
  onClose,
}: {
  vm: ViewModel
  source: "api" | "fixture"
  cid: string
  ioTab: InspectorTab
  onTab: (t: InspectorTab) => void
  dispName: (conv: string) => string
  onSelect: (cid: string) => void
  onClose: () => void
}) {
  // The list spans are envelope-only (include=structure), so the inspector
  // lazy-fetches THIS span's full element (content bodies) on open, through
  // the module-level LRU cache. The structural span renders immediately —
  // timing/tokens/part counts need no bodies — and the text wells fill in
  // when the fetch lands. Fixture spans already carry bodies (no fetch); a
  // backend predating the per-span endpoint 404s and we honestly degrade to
  // structure-only.
  const [full, setFull] = useState<SessionSpan | null>(null)
  const [bodyState, setBodyState] = useState<"ready" | "loading" | "unavailable">("ready")
  useEffect(() => {
    if (source !== "api") {
      setFull(null)
      setBodyState("ready")
      return
    }
    const cached = peekFullSpan(vm.sid, cid)
    if (cached) {
      setFull(cached)
      setBodyState("ready")
      return
    }
    let cancelled = false
    setFull(null)
    setBodyState("loading")
    fetchFullSpan(vm.sid, cid)
      .then((sp) => {
        if (cancelled) return
        setFull(sp)
        setBodyState("ready")
      })
      .catch(() => {
        if (!cancelled) setBodyState("unavailable")
      })
    return () => {
      cancelled = true
    }
  }, [vm.sid, cid, source])

  // Eagerly load the verdict (cached + shared with SecurityPane) so the
  // Security tab carries a finding-count badge before it's opened. Fixture
  // sessions have no verdict endpoint, so they stay unbadged. Keyed-state, set
  // only in the async callback (no setState-in-effect during render).
  const [findingSt, setFindingSt] = useState<{ cid: string; count: number } | null>(null)
  useEffect(() => {
    if (source !== "api") return
    let cancelled = false
    loadVerdict(cid)
      .then((v) => { if (!cancelled) setFindingSt({ cid, count: v?.findings.length ?? 0 }) })
      .catch(() => { if (!cancelled) setFindingSt({ cid, count: 0 }) })
    return () => { cancelled = true }
  }, [cid, source])
  const findingCount = findingSt && findingSt.cid === cid ? findingSt.count : 0

  // The shared InspectorBody renders from a MessageEntry, but session spans
  // don't carry the gateway facts (provider/configuration/tags/rules). Resolve
  // the request's MessageEntry by correlation id (api mode); fixtures and
  // lookup misses fall back to a minimal entry synthesized from the span so the
  // modal still renders. Keyed-state, set only in the async callback.
  const [entrySt, setEntrySt] = useState<{ cid: string; v: MessageEntry | null } | null>(null)
  useEffect(() => {
    // Fixtures have no /messages endpoint — leave entrySt unset so resolvedEntry
    // stays null and the synthesized fallback (below) is used.
    if (source !== "api") return
    let cancelled = false
    fetchMessageByCorrelation(cid)
      .then((e) => { if (!cancelled) setEntrySt({ cid, v: e }) })
      .catch(() => { if (!cancelled) setEntrySt({ cid, v: null }) })
    return () => { cancelled = true }
  }, [cid, source])
  const resolvedEntry = entrySt && entrySt.cid === cid ? entrySt.v : null

  // The rendered span: the full element when fetched, overlaid on the view
  // model's session-relative timing (t/tEnd never come over the wire).
  const s = useMemo<SpanVM | undefined>(() => {
    const base = vm.byCid.get(cid)
    if (!base) return undefined
    return full ? { ...full, t: base.t, tEnd: base.tEnd } : base
  }, [vm, cid, full])
  const flow = useMemo(
    () => (s ? (vm.byConv.get(s.conversation_id) ?? []) : []),
    [vm, s],
  )
  const idx = flow.findIndex((f) => f.cid === cid)
  const hasPrev = idx > 0
  const hasNext = idx >= 0 && idx < flow.length - 1
  // goPrev/goNext no-op at the conversation's boundary, so the keyboard
  // handler and footer buttons can share them unconditionally.
  const goPrev = useCallback(() => {
    if (idx > 0) onSelect(flow[idx - 1].cid)
  }, [idx, flow, onSelect])
  const goNext = useCallback(() => {
    if (idx >= 0 && idx < flow.length - 1) onSelect(flow[idx + 1].cid)
  }, [idx, flow, onSelect])

  // panelRef is the dialog surface, for focus-in / Tab-trap / focus-restore.
  // (Stepping prev/next resets stage scroll inside InspectorBody, keyed by cid.)
  const panelRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    // Remember the trigger so focus returns to it when the modal closes, and
    // move focus into the dialog on open (it opens via a row/bar click or the
    // #span= deep-link). Tab is trapped within the panel.
    const restoreTo = document.activeElement as HTMLElement | null
    panelRef.current?.focus()
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose()
      else if (e.key === "ArrowLeft") {
        e.preventDefault()
        goPrev()
      } else if (e.key === "ArrowRight") {
        e.preventDefault()
        goNext()
      } else if (e.key === "Tab") {
        const panel = panelRef.current
        if (!panel) return
        const items = panel.querySelectorAll<HTMLElement>(MODAL_FOCUSABLE)
        if (items.length === 0) {
          e.preventDefault()
          panel.focus()
          return
        }
        const first = items[0]
        const last = items[items.length - 1]
        const active = document.activeElement
        if (e.shiftKey && (active === first || active === panel)) {
          e.preventDefault()
          last.focus()
        } else if (!e.shiftKey && active === last) {
          e.preventDefault()
          first.focus()
        }
      }
    }
    document.addEventListener("keydown", onKey)
    return () => {
      document.removeEventListener("keydown", onKey)
      if (restoreTo && document.contains(restoreTo)) restoreTo.focus()
    }
  }, [goPrev, goNext, onClose])

  // Ledger joins for the shared panes, pre-formatted here because the clocks
  // are session-relative (only this page owns t0ms). null = looked, no join
  // (the panes say so honestly).
  const joinCall = useCallback(
    (p: OutputPart): CallJoin => {
      const r = p.id ? vm.ledgerById.get(p.id) : undefined
      return r && r.resultT != null
        ? { resultClock: clockAt(vm.t0ms, r.resultT), execS: r.execS, resultChars: r.resultChars, isError: r.isError }
        : null
    },
    [vm],
  )
  const joinResp = useCallback(
    (p: InputPart): RespJoin => {
      const r = p.id ? vm.ledgerById.get(p.id) : undefined
      return r ? { name: r.name, calledClock: clockAt(vm.t0ms, r.callT), execS: r.execS } : null
    },
    [vm],
  )

  if (!s) return null
  const col = vm.colors.get(s.conversation_id)

  // entry feeds the shared InspectorBody: the resolved MessageEntry when found,
  // else a minimal one from the span (model/status/duration/ids; provider and
  // policy facts are simply absent on fixtures).
  const entry: MessageEntry = resolvedEntry ?? {
    event_id: s.cid,
    correlation_id: s.cid,
    at: s.at ?? "",
    status_code: s.status ?? 0,
    duration_ms: s.latency_ms ?? 0,
    model: s.model ?? undefined,
    conversation_id: s.conversation_id,
  }
  // Session clocks are relative to the session t0 (only this page owns it), so
  // the timing tiles in InspectorBody read in session time, not wall time.
  const clock: SpanClock = (ms = 0) => clockMsAt(vm.t0ms, s.t + (ms ?? 0) / 1000)

  return createPortal(
    <div
      role="dialog"
      aria-modal="true"
      aria-label="Span detail"
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/55 p-4"
      onClick={onClose}
    >
      <div
        ref={panelRef}
        tabIndex={-1}
        className="flex h-[94vh] w-[min(1100px,95vw)] flex-col rounded-[var(--radius-lg)] border border-[color:var(--border)] bg-[color:var(--bg-1)] shadow-[var(--shadow-pop)] outline-none"
        onClick={(e) => e.stopPropagation()}
      >
        {/* header: name + chips + ids */}
        <div className="px-6 pt-4 pb-3 border-b border-[color:var(--border)] shrink-0">
          <div className="flex items-center gap-2 flex-wrap">
            <span className="text-[18px] font-bold tracking-[-0.01em] mr-1" style={{ color: col }}>
              {dispName(s.conversation_id)}
            </span>
            <HeaderChip>
              request <b>{idx + 1}</b> of {flow.length}
            </HeaderChip>
            <HeaderChip>
              <b className="mono">{s.model ?? "—"}</b>
            </HeaderChip>
            {s.status != null ? <StatusPill code={s.status} /> : <HeaderChip>HTTP —</HeaderChip>}
            <HeaderChip>
              stop <b>{s.finish_reason ?? "–"}</b>
            </HeaderChip>
            <button
              type="button"
              onClick={onClose}
              aria-label="Close"
              title="Close (Esc)"
              className="ml-auto text-[color:var(--text-3)] hover:text-[color:var(--text)]"
            >
              <X size={16} />
            </button>
          </div>
          <div className="mt-1.5 mono text-[11px] text-[color:var(--text-4)] flex items-center flex-wrap gap-x-1">
            conv <b className="text-[color:var(--text-3)]">{s.conversation_id}</b>
            <CardCopy text={s.conversation_id} label="conversation id" />
            <span className="mx-1">·</span> span <b className="text-[color:var(--text-3)]">{s.cid}</b>
            <CardCopy text={s.cid} label="span id" />
          </div>
        </div>

        {/* body: the shared trace-detail layout. The session supplies its own
            enrichments — the ledger joins and a session-relative clock. */}
        <div className="flex-1 min-h-0 flex flex-col px-6 pt-4 pb-4">
          {bodyState === "unavailable" && (
            <div className="flex-none mb-2 text-[11.5px]" style={{ color: "var(--warn)" }}>
              full content unavailable on this backend — showing structure only
            </div>
          )}
          <InspectorBody
            entry={entry}
            span={s}
            cid={s.cid}
            findingCount={findingCount}
            tab={ioTab}
            onTab={onTab}
            joinCall={joinCall}
            joinResp={joinResp}
            clock={clock}
          />
        </div>

        {/* footer pinned to the modal bottom */}
        <div className="flex items-center gap-2.5 px-6 py-3 border-t border-[color:var(--border)] shrink-0">
          <Button variant="secondary" size="sm" onClick={goPrev} disabled={!hasPrev} aria-label="Previous request (←)" title="Previous request (←)">
            <ChevronLeft /> prev
          </Button>
          <Button variant="secondary" size="sm" onClick={goNext} disabled={!hasNext} aria-label="Next request (→)" title="Next request (→)">
            next <ChevronRight />
          </Button>
          <span className="text-[12px] text-[color:var(--text-4)] mono">
            {idx + 1}/{flow.length}
          </span>
          <div className="flex-1" />
          <Button variant="ghost" size="sm" onClick={onClose}>
            <X /> close
          </Button>
        </div>
      </div>
    </div>,
    document.body,
  )
}

