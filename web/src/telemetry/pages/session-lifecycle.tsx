import { useCallback, useEffect, useMemo, useRef, useState } from "react"
import { createPortal } from "react-dom"
import { useNavigate, useParams } from "react-router"
import { ChevronLeft, ChevronRight, RotateCcw, X } from "lucide-react"
import { Button } from "@/components/ui/button"
import { KPI } from "@/components/atoms/kpi"
import { StatusPill } from "@/components/atoms/status-pill"
import { PanelCard, PanelHead, TableScroll } from "@/components/atoms/card"
import { SkeletonTiles, SkeletonBlock } from "@/components/atoms/skeleton"
import { fmt } from "@/lib/fmt"
import { UnauthorizedError } from "@/lib/api"
import {
  fetchSessionSpans,
  type InputPart,
  type OutputPart,
  type SessionSpan,
} from "@/lib/session-spans"
import { cn } from "@/lib/utils"
import { Dash } from "./messages"

// SessionLifecyclePage renders the agent-session lifecycle dashboard from a
// SessionSpansDTO (GET /api/v1/sessions/{id}/spans): stat cards, the session
// activity strip, a per-conversation lanes timeline with a minimap slicer,
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
        if ((s.output_parts ?? []).some((q) => q.type === "tool_call_response" && q.id === p.id)) {
          row.server = true
          row.resultT = s.t
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

// id8 abbreviates a tool_call id to its last 8 chars, matching the ledger.
function id8(id?: string): string {
  return "…" + String(id ?? "").slice(-8)
}

// ─────────────────────────────────────────────────────────────────────────
// Page shell: route param → fetch → view
// ─────────────────────────────────────────────────────────────────────────

export function SessionLifecyclePage() {
  const { id: routeId } = useParams<{ id?: string }>()
  if (!routeId) return null
  return (
    <div className="flex flex-col gap-3.5">
      <LifecycleBreadcrumb sessionId={routeId} />
      <LifecycleBody key={routeId} sessionId={routeId} />
    </div>
  )
}

// LifecycleBreadcrumb: Sessions / <id> / Lifecycle — the first two segments
// route back to the list and the session detail respectively.
function LifecycleBreadcrumb({ sessionId }: { sessionId: string }) {
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
        onClick={() => nav(`/sessions/${encodeURIComponent(sessionId)}`)}
        className="mono text-[12px] text-[color:var(--accent)] hover:underline truncate"
        title={sessionId}
      >
        {sessionId}
      </button>
      <ChevronRight size={14} className="text-[color:var(--text-4)] shrink-0" />
      <span className="text-[color:var(--text-2)] shrink-0">Lifecycle</span>
    </div>
  )
}

function LifecycleBody({ sessionId }: { sessionId: string }) {
  const nav = useNavigate()
  const [spans, setSpans] = useState<SessionSpan[]>([])
  const [source, setSource] = useState<"api" | "fixture">("api")
  const [status, setStatus] = useState<"loading" | "ok" | "error">("loading")
  const [err, setErr] = useState("")

  // No "loading" reset inside the effect: LifecycleBody is keyed by session
  // id, so a new session remounts it with the initial loading state.
  useEffect(() => {
    let cancelled = false
    fetchSessionSpans(sessionId)
      .then((r) => {
        if (cancelled) return
        setSpans(r.spans)
        setSource(r.source)
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

  if (status === "loading") {
    return (
      <>
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
      <div
        className="rounded-[var(--radius-lg)] border border-[color:var(--border)] p-8 text-center text-[13px]"
        style={{ color: "var(--err)", background: "var(--err-bg)" }}
      >
        Failed to load spans: <span className="mono">{err}</span>
      </div>
    )
  }
  if (!vm) {
    return (
      <div className="rounded-[var(--radius-lg)] border border-[color:var(--border)] bg-[color:var(--bg-1)] p-8 text-center text-[13px] text-[color:var(--text-3)]">
        No gen_ai spans captured for session <span className="mono">{sessionId}</span>.
      </div>
    )
  }
  return <LifecycleView vm={vm} source={source} />
}

// ─────────────────────────────────────────────────────────────────────────
// The dashboard proper
// ─────────────────────────────────────────────────────────────────────────

function LifecycleView({ vm, source }: { vm: ViewModel; source: "api" | "fixture" }) {
  // useNames toggles rule-6 aliases vs raw agent ids everywhere a
  // conversation is named (lanes, ledger, modal).
  const [useNames, setUseNames] = useState(true)
  // Deep link: opening the page with #span=<cid> opens that span's inspector
  // on load (ported from the reference implementation's #modal= hash).
  const [selectedCid, setSelectedCid] = useState<string | null>(() => {
    const m = window.location.hash.match(/^#span=(.+)$/)
    return m && vm.byCid.has(decodeURIComponent(m[1])) ? decodeURIComponent(m[1]) : null
  })
  // ioTab is lifted here so the inspector's Input|Output choice persists
  // across prev/next while the page lives.
  const [ioTab, setIoTab] = useState<"output" | "input">("output")

  const dispName = useCallback(
    (conv: string) =>
      conv === vm.sid ? "main" : useNames ? (vm.alias.get(conv) ?? conv.slice(0, 10)) : "agent " + conv.slice(0, 10),
    [vm, useNames],
  )

  return (
    <>
      <LifecycleHeader vm={vm} source={source} />
      <StatCards vm={vm} />
      <ActivityStrip vm={vm} />
      <TimelinePanel
        vm={vm}
        dispName={dispName}
        useNames={useNames}
        onToggleNames={() => setUseNames((v) => !v)}
        onOpen={setSelectedCid}
      />
      <div className="grid md:grid-cols-2 gap-3.5 items-start">
        <ToolTable vm={vm} kind="client" />
        <ToolTable vm={vm} kind="server" />
      </div>
      <LedgerTable vm={vm} dispName={dispName} onOpen={setSelectedCid} />
      {selectedCid && (
        <SpanInspector
          vm={vm}
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

function LifecycleHeader({ vm, source }: { vm: ViewModel; source: "api" | "fixture" }) {
  const models = useMemo(() => {
    const ct = new Map<string, number>()
    for (const s of vm.spans) ct.set(s.model ?? "unknown", (ct.get(s.model ?? "unknown") ?? 0) + 1)
    return [...ct.entries()].sort((a, b) => b[1] - a[1])
  }, [vm])
  const errs = vm.spans.filter((s) => s.status != null && s.status !== 200).length
  const day = new Date(vm.t0ms).toLocaleDateString("en-CA")
  return (
    <div className="flex items-start gap-3 flex-wrap">
      <div className="flex-1 min-w-0">
        <h1 className="text-[24px] font-semibold tracking-[-0.02em]">Session lifecycle</h1>
        <div className="text-[13px] text-[color:var(--text-3)] mt-1">
          Reconstructed from gen_ai spans — every value derived at render time
        </div>
      </div>
      <div className="flex items-center gap-2 flex-wrap">
        {source === "fixture" && (
          <HeaderChip>
            <span style={{ color: "var(--warn)" }}>fixture data</span> — no /spans endpoint on this backend
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
      </div>
    </div>
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
// span list at render time.
function StatCards({ vm }: { vm: ViewModel }) {
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
    </div>
  )
}

// ActivityStrip: stacked active-conversation count (main + sub-agents) per
// time bucket across the whole session, as a normalized-viewBox SVG.
const STRIP_BUCKETS = 160

function ActivityStrip({ vm }: { vm: ViewModel }) {
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

// Drag mode for the minimap brush: create a new slice, resize an edge, or pan
// the window.
type DragState =
  | { mode: "new"; anchorT: number }
  | { mode: "l" }
  | { mode: "r" }
  | { mode: "move"; offT: number; w: number }

function TimelinePanel({
  vm,
  dispName,
  useNames,
  onToggleNames,
  onOpen,
}: {
  vm: ViewModel
  dispName: (conv: string) => string
  useNames: boolean
  onToggleNames: () => void
  onOpen: (cid: string) => void
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

  // The slice window [d0, d1] in session seconds. rangeRef mirrors it for the
  // window-level drag handlers, which must read the latest value without
  // re-subscribing.
  const [range, setRange] = useState({ d0: 0, d1: vm.dur })
  const rangeRef = useRef(range)
  useEffect(() => {
    rangeRef.current = range
  }, [range])
  const dragRef = useRef<DragState | null>(null)
  const miniRef = useRef<SVGSVGElement>(null)
  const chartRef = useRef<SVGSVGElement>(null)

  const { d0, d1 } = range
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

  const setBrush = useCallback(
    (t1: number, t2: number) => {
      let n0 = Math.max(0, Math.min(t1, t2))
      let n1 = Math.min(vm.dur, Math.max(t1, t2))
      if (n1 - n0 < 2) n1 = Math.min(vm.dur, n0 + 2)
      if (n1 - n0 < 2) n0 = Math.max(0, n1 - 2)
      setRange({ d0: n0, d1: n1 })
    },
    [vm.dur],
  )
  const reset = useCallback(() => setRange({ d0: 0, d1: vm.dur }), [vm.dur])

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

  // Wheel zoom on the lanes chart, centered on the cursor. Native listener
  // because React's onWheel is passive and we must preventDefault the scroll.
  useEffect(() => {
    const svg = chartRef.current
    if (!svg) return
    const onWheel = (ev: WheelEvent) => {
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
  // so mousemove only re-renders the overlay.
  const [crossPx, setCrossPx] = useState<number | null>(null)
  const [tip, setTip] = useState<{ x: number; y: number; cid: string } | null>(null)

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
  const lanes = useMemo(() => {
    const nodes: React.ReactNode[] = []
    vm.convIds.forEach((conv, i) => {
      const yTop = GEO.t + i * (LANE_H + LANE_GAP)
      const col = vm.colors.get(conv) ?? "var(--text-3)"
      const flow = vm.byConv.get(conv) ?? []
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
          style={{ fill: col, font: "11px var(--font-mono)" }}
        >
          {conv === vm.sid ? "◆ session" : dispName(conv)}
        </text>,
      )
      flow.forEach((s, j) => {
        const nx = flow[j + 1]
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
        if (s.tEnd < d0 || s.t > d1) return
        const x1 = clampX(xz(s.t))
        const x2 = Math.max(x1 + 2, clampX(xz(s.tEnd)))
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
            onMouseMove={(ev) => setTip({ x: ev.clientX, y: ev.clientY, cid: s.cid })}
            onMouseLeave={() => setTip(null)}
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
              onMouseMove={(ev) => setTip({ x: ev.clientX, y: ev.clientY, cid: s.cid })}
              onMouseLeave={() => setTip(null)}
            />,
          )
        }
      })
    })
    return nodes
  }, [vm, dispName, d0, d1, plotW, xz, clampX, onOpen])

  // Minimap content: minute ticks + every span as a slim bar (main lane on
  // top, sub-agents below), under the brush overlay.
  const miniBars = useMemo(() => {
    const nodes: React.ReactNode[] = []
    for (let ms = Math.ceil(vm.t0ms / 60000) * 60000; ms <= vm.t0ms + vm.dur * 1000; ms += 60000) {
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
  }, [vm, mx])

  const tipSpan = tip ? vm.byCid.get(tip.cid) : undefined
  const crossT = crossPx != null ? d0 + ((crossPx - GEO.l) / plotW) * span : null

  return (
    <div className="flex flex-col gap-2">
      <div className="flex items-baseline gap-2 px-0.5">
        <span className="text-[13px] font-medium">Timeline</span>
        <span className="text-[11px] text-[color:var(--text-4)]">
          requests per conversation — drag the minimap to slice; scroll to zoom
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
              setCrossPx(px >= GEO.l && px <= w - GEO.r ? px : null)
            }}
            onMouseLeave={() => {
              setCrossPx(null)
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
// Tool tables (rule 5) + ledger
// ─────────────────────────────────────────────────────────────────────────

// ToolTable aggregates the ledger per tool name: client-executed tools report
// the median of joined exec times (no gap heuristics); server-executed tools
// report the median hosting-span latency instead, since the tool ran inside
// the request.
function ToolTable({ vm, kind }: { vm: ViewModel; kind: "client" | "server" }) {
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
        sub={kind === "client" ? "result returns in a later request" : "call + response in the same span"}
      />
      <TableScroll className="min-w-0">
        <thead>
          <tr className="text-[11px] uppercase tracking-[0.07em] text-[color:var(--text-3)]">
            <th className="text-left font-medium px-4 py-2">Tool</th>
            <th className="text-right font-medium px-4 py-2">Calls</th>
            <th className="text-right font-medium px-4 py-2">Convs</th>
            <th className="text-right font-medium px-4 py-2">{kind === "client" ? "Median exec" : "Median host span"}</th>
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
                {kind === "client" ? "no client-executed tool calls" : "none detected (no same-span tool_call_response)"}
              </td>
            </tr>
          )}
        </tbody>
      </TableScroll>
    </PanelCard>
  )
}

// LedgerTable is the flat joined ledger: one row per tool_call, raw arguments
// (rule 7 default path — opaque vendor JSON, truncated, never field-picked).
function LedgerTable({
  vm,
  dispName,
  onOpen,
}: {
  vm: ViewModel
  dispName: (conv: string) => string
  onOpen: (cid: string) => void
}) {
  return (
    <PanelCard>
      <PanelHead title="Tool call ledger" sub="joined by tool_call id · parameters shown raw" />
      <div className="max-h-[430px] overflow-y-auto">
        <TableScroll>
          <thead className="sticky top-0 bg-[color:var(--bg-1)] z-[1]">
            <tr className="text-[11px] uppercase tracking-[0.07em] text-[color:var(--text-3)]">
              <th className="text-left font-medium px-4 py-2">tool_call id</th>
              <th className="text-left font-medium px-4 py-2">Tool</th>
              <th className="text-left font-medium px-4 py-2">Conversation</th>
              <th className="text-left font-medium px-4 py-2">Called</th>
              <th className="text-left font-medium px-4 py-2">Response</th>
              <th className="text-right font-medium px-4 py-2">Exec</th>
              <th className="text-left font-medium px-4 py-2">Arguments (raw)</th>
            </tr>
          </thead>
          <tbody>
            {vm.ledger.map((r, i) => (
              <tr
                key={`${r.id}-${i}`}
                onClick={() => onOpen(r.callCid)}
                className="border-t border-[color:var(--border)] cursor-pointer hover:bg-[color:var(--hover)]"
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
                    <span className="text-[color:var(--text-4)]">unjoined</span>
                  )}
                  {r.isError && (
                    <b className="ml-1.5" style={{ color: "var(--err)" }}>
                      ERR
                    </b>
                  )}
                </td>
                <td className="mono text-[10.5px] px-4 py-2 text-[color:var(--text-4)] max-w-[340px] overflow-hidden text-ellipsis whitespace-nowrap">
                  {r.args.slice(0, 110)}
                </td>
              </tr>
            ))}
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
}

// ─────────────────────────────────────────────────────────────────────────
// Span inspector modal
// ─────────────────────────────────────────────────────────────────────────

function SpanInspector({
  vm,
  cid,
  ioTab,
  onTab,
  dispName,
  onSelect,
  onClose,
}: {
  vm: ViewModel
  cid: string
  ioTab: "output" | "input"
  onTab: (t: "output" | "input") => void
  dispName: (conv: string) => string
  onSelect: (cid: string) => void
  onClose: () => void
}) {
  const s = vm.byCid.get(cid)
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

  // Body scrolls back to the top when stepping prev/next to another span.
  const bodyRef = useRef<HTMLDivElement>(null)
  useEffect(() => {
    bodyRef.current?.scrollTo({ top: 0 })
  }, [cid])

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose()
      else if (e.key === "ArrowLeft") {
        e.preventDefault()
        goPrev()
      } else if (e.key === "ArrowRight") {
        e.preventDefault()
        goNext()
      }
    }
    document.addEventListener("keydown", onKey)
    return () => document.removeEventListener("keydown", onKey)
  }, [goPrev, goNext, onClose])

  if (!s) return null
  const col = vm.colors.get(s.conversation_id)

  const toolCalls = (s.output_parts ?? []).filter((p) => p.type === "tool_call")
  // Same-span responses key server-tool detection (rule 8) and the
  // "chars back" meta on the call card.
  const srvResp = new Map<string, OutputPart>()
  for (const p of s.output_parts ?? []) {
    if (p.type === "tool_call_response" && p.id) srvResp.set(p.id, p)
  }
  const inParts = s.input_parts ?? []
  const resps = inParts.filter((p) => p.type === "tool_call_response")
  const texts = inParts.filter((p) => p.type === "text")
  const nReasoning = (s.output_parts ?? []).filter((p) => p.type === "reasoning").length

  const outChars = s.output_text_chars ?? (s.output_text ?? "").length
  const outMeta = `${fmt.compact(outChars)} chars${nReasoning ? ` · ${nReasoning} reasoning` : ""}${
    toolCalls.length ? ` · ${toolCalls.length} tool call${toolCalls.length === 1 ? "" : "s"}` : ""
  }`
  const inMeta = inParts.length
    ? `${resps.length} tool response${resps.length === 1 ? "" : "s"}${texts.length ? ` · ${texts.length} text` : ""}`
    : "empty"

  return createPortal(
    <div
      role="dialog"
      aria-modal="true"
      aria-label="Span detail"
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/55 p-4"
      onClick={onClose}
    >
      <div
        className="flex h-[94vh] w-[min(1100px,95vw)] flex-col rounded-[var(--radius-lg)] border border-[color:var(--border)] bg-[color:var(--bg-1)] shadow-[var(--shadow-pop)]"
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
          <div className="mt-1.5 mono text-[11px] text-[color:var(--text-4)]">
            conv <b className="text-[color:var(--text-3)]">{s.conversation_id}</b> · span{" "}
            <b className="text-[color:var(--text-3)]">{s.cid}</b>
          </div>
        </div>

        {/* body */}
        <div ref={bodyRef} className="flex-1 min-h-0 overflow-y-auto px-6 py-4">
          <TimingTokensTiles vm={vm} s={s} />

          {/* Input | Output tabs — Output default, choice persists across prev/next */}
          <div className="flex border-b border-[color:var(--border)] mt-5">
            <IOTab on={ioTab === "output"} onClick={() => onTab("output")} label="Output" meta={outMeta} />
            <IOTab on={ioTab === "input"} onClick={() => onTab("input")} label="Input" meta={inMeta} />
          </div>

          {ioTab === "output" ? (
            <div className="pt-3">
              {s.output_text ? (
                <MCard
                  head={
                    <>
                      <span style={{ color: "var(--accent)" }}>●</span>
                      <span>Text</span>
                      <span className="ml-auto mono tnum text-[11.5px] text-[color:var(--text-3)] pl-2.5 whitespace-nowrap">
                        <b className="text-[color:var(--text-2)]">{fmt.compact(outChars)}</b> chars
                        {outChars > s.output_text.length ? ` · showing first ${fmt.compact(s.output_text.length)} (server cap)` : ""}
                      </span>
                    </>
                  }
                >
                  <PreBlock text={s.output_text} />
                </MCard>
              ) : (
                <div className="text-[12.5px] text-[color:var(--text-4)]">(no text parts)</div>
              )}
              {toolCalls.map((p, i) => (
                <ToolCallCard key={p.id ?? i} vm={vm} part={p} sameSpanResp={p.id ? srvResp.get(p.id) : undefined} />
              ))}
            </div>
          ) : (
            <div className="pt-3">
              {inParts.length === 0 ? (
                s.input_text ? (
                  <PreBlock text={s.input_text} />
                ) : (
                  <div className="text-[12.5px] text-[color:var(--text-4)]">
                    (empty — no new input parts in this request)
                  </div>
                )
              ) : (
                <>
                  {resps.map((p, i) => (
                    <RespCard key={p.id ?? i} vm={vm} part={p} defaultOpen={resps.length === 1} />
                  ))}
                  {texts.map((p, i) => (
                    <MCard
                      key={`txt-${i}`}
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
                      <PreBlock text={s.input_text ?? "(empty)"} />
                    </MCard>
                  ))}
                </>
              )}
              {(s.input_text_chars ?? 0) > (s.input_text ?? "").length && (
                <div className="text-[11px] text-[color:var(--text-4)] mt-1.5">
                  showing first {fmt.compact((s.input_text ?? "").length)} of {fmt.compact(s.input_text_chars ?? 0)} chars (server cap)
                </div>
              )}
            </div>
          )}
        </div>

        {/* footer pinned to the modal bottom */}
        <div className="flex items-center gap-2.5 px-6 py-3 border-t border-[color:var(--border)] shrink-0">
          <Button variant="secondary" size="sm" onClick={goPrev} disabled={!hasPrev} aria-label="Previous request (←)" title="Previous request (←)">
            <ChevronLeft /> prev
          </Button>
          <span className="text-[12px] text-[color:var(--text-4)] mono">
            request {idx + 1} / {flow.length} in this conversation
          </span>
          <Button variant="secondary" size="sm" onClick={goNext} disabled={!hasNext} aria-label="Next request (→)" title="Next request (→)">
            next <ChevronRight />
          </Button>
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

// TimingTokensTiles: the side-by-side Timing / Tokens key-value tiles.
function TimingTokensTiles({ vm, s }: { vm: ViewModel; s: SpanVM }) {
  const fresh = (s.usage.input ?? 0) - (s.usage.cache_read ?? 0) - (s.usage.cache_creation ?? 0)
  const cacheShare = s.usage.input ? `${Math.round(((s.usage.cache_read ?? 0) / s.usage.input) * 100)}%` : "—"
  return (
    <div className="grid sm:grid-cols-2 gap-3 mt-1">
      <Tile label="Timing" accent="var(--warn)">
        <TKV k="start" v={clockMsAt(vm.t0ms, s.t)} />
        <TKV k="first chunk" v={s.ttfc_ms != null ? clockMsAt(vm.t0ms, s.t + s.ttfc_ms / 1000) : "—"} />
        <TKV k="end" v={s.latency_ms != null ? clockMsAt(vm.t0ms, s.tEnd) : "—"} />
        <TKV k="latency" v={fmt.ms(s.latency_ms)} />
        <TKV k="ttfc" v={fmt.ms(s.ttfc_ms)} />
        <TKV k="stream tail" v={s.ttfc_ms != null && s.latency_ms != null ? fmt.ms(s.latency_ms - s.ttfc_ms) : "—"} />
      </Tile>
      <Tile label="Tokens" accent="var(--ok)">
        <TKV k="in" v={fmt.compact(s.usage.input)} />
        <TKV k="out" v={fmt.compact(s.usage.output)} />
        <TKV k="fresh in" v={fmt.compact(fresh)} />
        <TKV k="cache read" v={fmt.compact(s.usage.cache_read)} />
        <TKV k="cache write" v={fmt.compact(s.usage.cache_creation)} />
        <TKV k="cache share" v={cacheShare} />
      </Tile>
    </div>
  )
}

function Tile({ label, accent, children }: { label: string; accent: string; children: React.ReactNode }) {
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

function TKV({ k, v }: { k: string; v: string }) {
  return (
    <div className="min-w-0">
      <div className="text-[10px] font-medium uppercase tracking-[0.08em] text-[color:var(--text-4)] whitespace-nowrap">{k}</div>
      <div className="mono tnum text-[14px] font-semibold mt-0.5 whitespace-nowrap">{v}</div>
    </div>
  )
}

function IOTab({ on, onClick, label, meta }: { on: boolean; onClick: () => void; label: string; meta: string }) {
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

// MCard is the modal's content card: header strip + body, matching the
// reference's mcard look in console tokens.
function MCard({ head, children }: { head: React.ReactNode; children: React.ReactNode }) {
  return (
    <div className="rounded-[var(--radius-lg)] border border-[color:var(--border)] bg-[color:var(--bg)] my-2 overflow-hidden">
      <div className="flex items-center gap-2 px-3.5 py-2 bg-[color:var(--bg-2)] border-b border-[color:var(--border)] text-[12.5px] text-[color:var(--text-3)] flex-wrap min-h-[35px]">
        {head}
      </div>
      <div className="px-3.5 py-3">{children}</div>
    </div>
  )
}

// ToolChip / ServerChip mirror the reference's tool-name and server-executed
// pills in token colors.
function ToolChip({ name }: { name?: string }) {
  return (
    <span
      className="inline-flex items-center rounded-full px-2.5 py-px text-[11.5px] font-semibold"
      style={{ background: "var(--accent-bg)", color: "var(--accent)", border: "1px solid color-mix(in oklab, var(--accent) 35%, transparent)" }}
    >
      {name ?? "(unnamed)"}
    </span>
  )
}

// ToolCallCard renders one tool_call the span emitted: server-executed when a
// same-span response exists (rule 8), otherwise annotated from the ledger
// join, with the raw-args display below (rule 7 default path).
function ToolCallCard({
  vm,
  part,
  sameSpanResp,
}: {
  vm: ViewModel
  part: OutputPart
  sameSpanResp?: OutputPart
}) {
  const ledgerRow = part.id ? vm.ledgerById.get(part.id) : undefined
  let meta: React.ReactNode
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
  } else if (ledgerRow && ledgerRow.resultT != null) {
    meta = (
      <span className="ml-auto mono tnum text-[11.5px] whitespace-nowrap pl-2.5">
        result at {clockAt(vm.t0ms, ledgerRow.resultT)} · exec{" "}
        <b className="text-[color:var(--text-2)]">{ledgerRow.execS != null ? `${ledgerRow.execS.toFixed(1)}s` : "–"}</b> ·{" "}
        <b className="text-[color:var(--text-2)]">{fmt.compact(ledgerRow.resultChars ?? null)}</b> chars
        {ledgerRow.isError && (
          <b className="ml-1" style={{ color: "var(--err)" }}>
            · ERROR
          </b>
        )}
      </span>
    )
  } else {
    meta = <span className="ml-auto text-[11.5px] text-[color:var(--text-4)] pl-2.5">no joined result in this session</span>
  }
  const argsLen = (part.args ?? "").length
  const truncated = (part.args_chars ?? 0) > argsLen
  return (
    <MCard
      head={
        <>
          <ToolChip name={part.name} />
          <span className="mono text-[11px] text-[color:var(--text-4)]">{id8(part.id)}</span>
          {meta}
        </>
      }
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
// collapsible card, annotated by joining its id back to the ledger row of the
// call that produced it. When no call matches anywhere in the captured spans,
// it says so honestly instead of guessing.
function RespCard({ vm, part, defaultOpen }: { vm: ViewModel; part: InputPart; defaultOpen: boolean }) {
  const row = part.id ? vm.ledgerById.get(part.id) : undefined
  const textLen = (part.text ?? "").length
  const truncated = (part.chars ?? 0) > textLen && !!part.text
  return (
    <details
      className="rounded-[var(--radius-lg)] border border-[color:var(--border)] bg-[color:var(--bg)] my-2 overflow-hidden group"
      open={defaultOpen}
    >
      <summary className="flex items-center gap-2 px-3.5 py-2 bg-[color:var(--bg-2)] text-[12.5px] text-[color:var(--text-3)] flex-wrap min-h-[35px] cursor-pointer list-none [&::-webkit-details-marker]:hidden">
        <span className="transition-transform group-open:-rotate-90" style={{ color: "var(--warn)" }}>
          ↩
        </span>
        {row ? (
          <>
            <span>response to</span>
            <ToolChip name={row.name} />
            <span className="mono text-[11px] text-[color:var(--text-4)]">{id8(part.id)}</span>
            <span className="text-[color:var(--text-4)]">
              called at {clockAt(vm.t0ms, row.callT)}
              {row.execS != null && (
                <b className="text-[color:var(--text-2)]"> (exec {row.execS.toFixed(1)}s)</b>
              )}
            </span>
          </>
        ) : (
          <>
            <span>tool response</span>
            <span className="mono text-[11px] text-[color:var(--text-4)]">{id8(part.id)}</span>
            <span className="text-[color:var(--text-4)]">no matching tool_call in captured spans — possible capture gap</span>
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
      </summary>
      <div className="px-3.5 py-3 border-t border-[color:var(--border)]">
        {truncated && (
          <div className="text-[11px] mb-1.5" style={{ color: "var(--warn)" }}>
            showing first {fmt.compact(textLen)} of {fmt.compact(part.chars ?? 0)} chars (server cap)
          </div>
        )}
        <PreBlock text={part.text || "(no text captured)"} />
      </div>
    </details>
  )
}

// PreBlock is the modal's raw-text well.
function PreBlock({ text }: { text: string }) {
  return (
    <pre className="whitespace-pre-wrap break-words rounded-[var(--radius)] border border-[color:var(--border)] bg-[color:var(--bg)] px-3 py-2.5 mono text-[12px] leading-[1.55] text-[color:var(--text-2)] max-h-[46vh] overflow-y-auto m-0">
      {text}
    </pre>
  )
}

// ArgsView is the rule-7 default path for tool arguments: opaque vendor JSON,
// possibly server-capped. Parse if possible and render a generic key/value
// grid (no field-picking, every shape handled); fall back to the raw string
// visibly when the payload isn't valid JSON.
function ArgsView({ raw }: { raw?: string }) {
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
