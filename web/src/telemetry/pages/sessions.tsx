import { useEffect, useMemo, useState } from "react"
import { useNavigate, useParams } from "react-router"
import { ChevronDown, ChevronRight, Search } from "lucide-react"
import {
  Area,
  AreaChart,
  Bar,
  BarChart,
  Cell,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from "recharts"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { KPI } from "@/components/atoms/kpi"
import { StatusPill } from "@/components/atoms/status-pill"
import { ProviderChip } from "@/components/atoms/provider-chip"
import { PanelCard, PanelHead, TableScroll } from "@/components/atoms/card"
import { fmt } from "@/lib/fmt"
import { providerColor } from "@/lib/provider-color"
import { UnauthorizedError } from "@/lib/api"
import type { MessageEntry } from "@/lib/messages"
import { fetchSession, type SessionView } from "@/lib/sessions"
import { Dash, Inspector } from "./messages"

// SessionsPage is the per-session view: a session id is typed in or supplied by
// URL (/sessions/:id). The page splits 50/50 vertically — cross-request graphs
// on top (collapsible, to hand the viewport to the table), the session's
// messages newest-first below, reusing the messages row + GenAI inspector.
export function SessionsPage() {
  const nav = useNavigate()
  const { id: routeId } = useParams<{ id?: string }>()
  const [input, setInput] = useState(routeId ?? "")

  // Keep the search box in sync when the URL changes (back/forward, deep link).
  useEffect(() => setInput(routeId ?? ""), [routeId])

  const submit = (e: React.FormEvent) => {
    e.preventDefault()
    const id = input.trim()
    if (id) nav(`/sessions/${encodeURIComponent(id)}`)
  }

  return (
    <div className="flex flex-col gap-3.5 h-full min-h-0">
      <div className="flex items-start gap-3">
        <div className="flex-1 min-w-0">
          <h1 className="text-[24px] font-semibold tracking-[-0.02em]">Sessions</h1>
          <div className="text-[13px] text-[color:var(--text-3)] mt-1">
            One conversation's requests over time, newest-first
          </div>
        </div>
        <form onSubmit={submit} className="flex items-center gap-2">
          <Input
            placeholder="Session ID"
            value={input}
            onChange={(e) => setInput(e.target.value)}
            className="h-9 w-64 text-[12px] mono"
            autoFocus={!routeId}
          />
          <Button type="submit" size="sm" disabled={!input.trim()}>
            <Search /> <span className="hidden sm:inline">View</span>
          </Button>
        </form>
      </div>

      {routeId ? (
        <SessionBody key={routeId} sessionId={routeId} />
      ) : (
        <Placeholder text="Enter a session id to inspect its requests." />
      )}
    </div>
  )
}

function Placeholder({ text, tone }: { text: string; tone?: "err" }) {
  return (
    <div
      className="rounded-[var(--radius-lg)] border border-[color:var(--border)] bg-[color:var(--bg-1)] p-8 text-center text-[13px]"
      style={tone === "err" ? { color: "var(--err)", background: "var(--err-bg)" } : { color: "var(--text-3)" }}
    >
      {text}
    </div>
  )
}

const GRAPHS_COLLAPSED_KEY = "telemetry.sessions.graphsCollapsed"

function SessionBody({ sessionId }: { sessionId: string }) {
  const nav = useNavigate()
  const [view, setView] = useState<SessionView | null>(null)
  const [status, setStatus] = useState<"loading" | "ok" | "missing" | "error">("loading")
  const [err, setErr] = useState("")
  const [collapsed, setCollapsed] = useState(() => localStorage.getItem(GRAPHS_COLLAPSED_KEY) === "1")
  const [selected, setSelected] = useState<number | null>(null)

  const toggleCollapsed = () => {
    setCollapsed((c) => {
      const next = !c
      localStorage.setItem(GRAPHS_COLLAPSED_KEY, next ? "1" : "0")
      return next
    })
  }

  useEffect(() => {
    let cancelled = false
    setStatus("loading")
    setSelected(null)
    fetchSession(sessionId)
      .then((v) => {
        if (cancelled) return
        if (!v) {
          setStatus("missing")
          return
        }
        setView(v)
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

  // requests are oldest-first (graphs plot them as-is); the table wants
  // newest-first, so it iterates a reversed view.
  const requests = useMemo(() => view?.requests ?? [], [view])
  const newestFirst = useMemo(() => requests.slice().reverse(), [requests])

  if (status === "loading") return <Placeholder text="Loading session…" />
  if (status === "missing") return <Placeholder text={`No telemetry for session ${sessionId}.`} />
  if (status === "error") return <Placeholder text={`Failed to load session: ${err}`} tone="err" />
  if (!view) return null

  return (
    <>
      <SessionTiles view={view} />

      <div className="flex flex-col gap-3.5 flex-1 min-h-0">
        <PanelCard className={collapsed ? "shrink-0" : "flex-1 min-h-0 flex flex-col"}>
          <button
            type="button"
            onClick={toggleCollapsed}
            className="flex items-center gap-2 w-full px-4 h-[42px] border-b border-[color:var(--border)] text-left hover:bg-[color:var(--hover)]"
            aria-expanded={!collapsed}
          >
            {collapsed ? <ChevronRight size={15} /> : <ChevronDown size={15} />}
            <span className="text-[13px] font-medium">Graphs</span>
            <span className="text-[11px] text-[color:var(--text-4)]">
              {collapsed ? "show" : "hide to expand the messages table"}
            </span>
          </button>
          {!collapsed && (
            <div className="flex-1 min-h-0 overflow-auto p-4 flex flex-col gap-4">
              <SessionGraphs requests={requests} />
            </div>
          )}
        </PanelCard>

        <PanelCard className="flex-1 min-h-0 flex flex-col">
          <PanelHead title="Messages" sub={`${requests.length} request${requests.length === 1 ? "" : "s"} · newest first`} />
          <TableScroll>
            <thead>
              <tr className="text-[11px] uppercase tracking-[0.07em] text-[color:var(--text-3)]">
                <th className="text-left font-medium px-4 py-2">Time</th>
                <th className="text-left font-medium px-4 py-2">Status</th>
                <th className="text-left font-medium px-4 py-2">Provider</th>
                <th className="text-left font-medium px-4 py-2">Endpoint</th>
                <th className="text-left font-medium px-4 py-2">Model</th>
                <th className="text-right font-medium px-4 py-2">Duration</th>
                <th className="text-right font-medium px-4 py-2">Tokens</th>
              </tr>
            </thead>
            <tbody>
              {newestFirst.map((e, i) => (
                <tr
                  key={e.event_id}
                  onClick={() => setSelected(i)}
                  className="border-t border-[color:var(--border)] cursor-pointer hover:bg-[color:var(--hover)]"
                >
                  <td className="mono text-[11.5px] px-4 py-2 text-[color:var(--text-3)] whitespace-nowrap">{fmt.shortTime(e.at)}</td>
                  <td className="px-4 py-2"><StatusPill code={e.status_code} /></td>
                  <td className="px-4 py-2">{e.provider ? <ProviderChip name={e.provider} /> : <Dash />}</td>
                  <td className="mono text-[12px] px-4 py-2">{e.endpoint || <Dash />}</td>
                  <td className="mono text-[12px] px-4 py-2">{e.model || <Dash />}</td>
                  <td className="mono tnum text-[12px] text-right px-4 py-2">{fmt.ms(e.duration_ms)}</td>
                  <td className="mono tnum text-[11.5px] text-right px-4 py-2 text-[color:var(--text-3)]">
                    {(e.tokens_in ?? 0) + (e.tokens_out ?? 0) > 0 ? `${fmt.compact(e.tokens_in ?? 0)}/${fmt.compact(e.tokens_out ?? 0)}` : <Dash />}
                  </td>
                </tr>
              ))}
            </tbody>
          </TableScroll>
        </PanelCard>
      </div>

      {selected !== null && newestFirst[selected] && (
        <Inspector
          entry={newestFirst[selected]}
          position={`${selected + 1} / ${newestFirst.length}`}
          onClose={() => setSelected(null)}
          onPrev={selected > 0 ? () => setSelected(selected - 1) : undefined}
          onNext={selected < newestFirst.length - 1 ? () => setSelected(selected + 1) : undefined}
        />
      )}
    </>
  )
}

function SessionTiles({ view }: { view: SessionView }) {
  const { totals, requests } = view
  const errAccent: "ok" | "warn" | "err" = totals.errors > 0 ? "warn" : "ok"
  // p95 latency and the model mix are derived client-side — the totals rollup
  // deliberately omits them (no extra aggregation query).
  const p95 = useMemo(() => percentile(requests.map((r) => r.duration_ms), 0.95), [requests])
  const models = useMemo(() => distinctModels(requests), [requests])
  const cached = useMemo(() => requests.reduce((s, r) => s + (r.tokens_cached ?? 0), 0), [requests])

  return (
    <div className="grid grid-cols-2 md:grid-cols-3 lg:grid-cols-6 gap-3 shrink-0">
      <KPI label="Requests" value={fmt.compact(totals.requests)} sub={`${totals.errors} error${totals.errors === 1 ? "" : "s"}`} accent={errAccent} />
      <KPI label="p95 latency" value={fmt.ms(p95)} sub="derived" />
      <KPI label="Tokens in" value={fmt.compact(totals.tokens_in)} sub={`${fmt.compact(cached)} cached`} />
      <KPI label="Tokens out" value={fmt.compact(totals.tokens_out)} sub="completion" />
      <KPI label="Models" value={String(models.length)} sub={models.length === 1 ? models[0]?.model : `${models.length} used`} />
      <KPI label="Span" value={spanLabel(requests)} sub="first → last" />
    </div>
  )
}

// TurnPoint is one request projected onto the graph axes. Built oldest-first so
// the cumulative series and the timeline read left-to-right in arrival order.
type TurnPoint = {
  i: number
  at: string
  model: string
  status: number
  durationMs: number
  cached: number
  fresh: number
  tokensOut: number
  cumIn: number
  isError: boolean
}

function SessionGraphs({ requests }: { requests: MessageEntry[] }) {
  const points = useMemo<TurnPoint[]>(() => {
    const out: TurnPoint[] = []
    let cum = 0
    for (let idx = 0; idx < requests.length; idx++) {
      const r = requests[idx]
      const tin = r.tokens_in ?? 0
      const cachedTok = Math.min(r.tokens_cached ?? 0, tin)
      cum += tin
      out.push({
        i: idx + 1,
        at: r.at,
        model: r.model || "unknown",
        status: r.status_code,
        durationMs: r.duration_ms,
        cached: cachedTok,
        fresh: Math.max(0, tin - cachedTok),
        tokensOut: r.tokens_out ?? 0,
        cumIn: cum,
        isError: r.status_code >= 400,
      })
    }
    return out
  }, [requests])

  const models = useMemo(() => distinctModels(requests).map((m) => m.model), [requests])
  const axis = "var(--text-4)"

  if (!points.length) return <div className="text-[12px] text-[color:var(--text-4)]">No requests in this session.</div>

  return (
    <>
      <ChartBlock
        title="Request timeline"
        sub="latency per request · colored by model · errors in red"
        legend={
          <div className="flex items-center gap-3 flex-wrap text-[11px] text-[color:var(--text-2)]">
            {models.map((m) => (
              <span key={m} className="inline-flex items-center gap-1.5">
                <span className="inline-block w-2.5 h-2.5 rounded-[2px]" style={{ backgroundColor: providerColor(m).fg }} />
                <span className="mono">{m}</span>
              </span>
            ))}
          </div>
        }
      >
        <ResponsiveContainer width="100%" height={170}>
          <BarChart data={points} margin={{ top: 6, right: 8, bottom: 0, left: 0 }}>
            <XAxis dataKey="i" tick={{ fontSize: 10, fill: axis }} stroke={axis} interval="preserveStartEnd" />
            <YAxis tickFormatter={(v) => fmt.ms(v as number)} tick={{ fontSize: 10, fill: axis }} stroke={axis} width={44} />
            <Tooltip cursor={{ fill: "var(--hover)" }} content={<TurnTooltip />} />
            <Bar dataKey="durationMs" radius={[2, 2, 0, 0]} isAnimationActive={false}>
              {points.map((p) => (
                <Cell key={p.i} fill={p.isError ? "var(--err)" : providerColor(p.model).fg} />
              ))}
            </Bar>
          </BarChart>
        </ResponsiveContainer>
      </ChartBlock>

      <ChartBlock title="Cumulative input tokens" sub="context growth across the session">
        <ResponsiveContainer width="100%" height={150}>
          <AreaChart data={points} margin={{ top: 6, right: 8, bottom: 0, left: 0 }}>
            <XAxis dataKey="i" tick={{ fontSize: 10, fill: axis }} stroke={axis} interval="preserveStartEnd" />
            <YAxis tickFormatter={(v) => fmt.compact(v as number)} tick={{ fontSize: 10, fill: axis }} stroke={axis} width={44} />
            <Tooltip cursor={{ stroke: "var(--border)" }} content={<TurnTooltip />} />
            <Area dataKey="cumIn" stroke="var(--accent)" fill="color-mix(in oklch, var(--accent) 15%, transparent)" strokeWidth={1.5} isAnimationActive={false} />
          </AreaChart>
        </ResponsiveContainer>
      </ChartBlock>

      <ChartBlock
        title="Token burn per request"
        sub="cached + fresh input, plus output · cost proxy"
        legend={
          <div className="flex items-center gap-3 flex-wrap text-[11px] text-[color:var(--text-2)]">
            <LegendSwatch color="var(--accent)" label="cached input" />
            <LegendSwatch color="color-mix(in oklch, var(--accent) 45%, var(--bg-2))" label="fresh input" />
            <LegendSwatch color="var(--text-3)" label="output" />
          </div>
        }
      >
        <ResponsiveContainer width="100%" height={150}>
          <BarChart data={points} margin={{ top: 6, right: 8, bottom: 0, left: 0 }}>
            <XAxis dataKey="i" tick={{ fontSize: 10, fill: axis }} stroke={axis} interval="preserveStartEnd" />
            <YAxis tickFormatter={(v) => fmt.compact(v as number)} tick={{ fontSize: 10, fill: axis }} stroke={axis} width={44} />
            <Tooltip cursor={{ fill: "var(--hover)" }} content={<TurnTooltip />} />
            <Bar dataKey="cached" stackId="t" fill="var(--accent)" isAnimationActive={false} />
            <Bar dataKey="fresh" stackId="t" fill="color-mix(in oklch, var(--accent) 45%, var(--bg-2))" isAnimationActive={false} />
            <Bar dataKey="tokensOut" stackId="t" fill="var(--text-3)" radius={[2, 2, 0, 0]} isAnimationActive={false} />
          </BarChart>
        </ResponsiveContainer>
      </ChartBlock>
    </>
  )
}

function ChartBlock({ title, sub, legend, children }: { title: string; sub: string; legend?: React.ReactNode; children: React.ReactNode }) {
  return (
    <div className="flex flex-col gap-1.5">
      <div className="flex items-baseline gap-2 flex-wrap">
        <span className="text-[13px] font-medium">{title}</span>
        <span className="text-[11px] text-[color:var(--text-4)]">{sub}</span>
        {legend && <span className="ml-auto">{legend}</span>}
      </div>
      {children}
    </div>
  )
}

function LegendSwatch({ color, label }: { color: string; label: string }) {
  return (
    <span className="inline-flex items-center gap-1.5">
      <span className="inline-block w-2.5 h-2.5 rounded-[2px]" style={{ backgroundColor: color }} />
      <span>{label}</span>
    </span>
  )
}

// TurnTooltip renders the hovered request's detail. Shared across all three
// charts — each datum is a TurnPoint, so the tooltip reads the same fields.
// Typed locally rather than via recharts' TooltipProps (its v3 content-prop
// shape doesn't surface payload directly); recharts injects active/payload at
// runtime when it clones the element passed to Tooltip's content.
type TurnTooltipProps = { active?: boolean; payload?: { payload?: TurnPoint }[] }
function TurnTooltip({ active, payload }: TurnTooltipProps) {
  if (!active || !payload?.length) return null
  const p = payload[0]?.payload
  if (!p) return null
  const totalIn = p.cached + p.fresh
  return (
    <div className="rounded-[var(--radius)] border border-[color:var(--border)] bg-[color:var(--bg-1)] px-3 py-2 text-[11.5px] shadow-[var(--shadow-md)] flex flex-col gap-0.5">
      <div className="flex items-center gap-2">
        <StatusPill code={p.status} />
        <span className="mono text-[color:var(--text-3)]">#{p.i}</span>
        <span className="mono text-[color:var(--text-4)]">{fmt.shortTime(p.at)}</span>
      </div>
      <div className="mono">{p.model}</div>
      <div className="text-[color:var(--text-3)]">{fmt.ms(p.durationMs)} · in {fmt.compact(totalIn)} ({fmt.compact(p.cached)} cached) · out {fmt.compact(p.tokensOut)}</div>
    </div>
  )
}

// percentile returns the p-th (0..1) percentile of values via nearest-rank on a
// sorted copy. Returns 0 for an empty input.
function percentile(values: number[], p: number): number {
  if (!values.length) return 0
  const sorted = values.slice().sort((a, b) => a - b)
  const rank = Math.ceil(p * sorted.length) - 1
  return sorted[Math.min(Math.max(rank, 0), sorted.length - 1)]
}

// distinctModels returns each model in first-seen order with its request count.
function distinctModels(requests: MessageEntry[]): { model: string; count: number }[] {
  const order: string[] = []
  const counts = new Map<string, number>()
  for (const r of requests) {
    const m = r.model || "unknown"
    if (!counts.has(m)) order.push(m)
    counts.set(m, (counts.get(m) ?? 0) + 1)
  }
  return order.map((model) => ({ model, count: counts.get(model) ?? 0 }))
}

// spanLabel is the wall-clock duration between the first and last request,
// using fmt.uptime's two-largest-units formatting.
function spanLabel(requests: MessageEntry[]): string {
  if (requests.length < 2) return "—"
  const first = new Date(requests[0].at).getTime()
  const last = new Date(requests[requests.length - 1].at).getTime()
  if (!Number.isFinite(first) || !Number.isFinite(last) || last < first) return "—"
  return fmt.uptime(last - first)
}
