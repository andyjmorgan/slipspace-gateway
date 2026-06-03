import { useEffect, useMemo, useState } from "react"
import { useNavigate } from "react-router"
import { RefreshCw } from "lucide-react"
import { Button } from "@/components/ui/button"
import { KPI } from "@/components/atoms/kpi"
import { Segmented } from "@/components/atoms/segmented"
import { LineChart } from "@/components/atoms/line-chart"
import { PanelCard, PanelHead } from "@/components/atoms/card"
import { PageIcon } from "@/components/atoms/page-states"
import type { DashboardSeries } from "@/lib/dashboard"
import { fmt } from "@/lib/fmt"
import { apiFetch, apiErrorText, UnauthorizedError } from "../lib/api"

// StatsTotals mirrors the `totals` object the CP observability stats API
// returns — fleet-wide aggregates over the selected range.
interface StatsTotals {
  requests: number
  tokens_in: number
  tokens_out: number
  errors: number
  error_rate: number
  avg_latency_ms: number
  p95_latency_ms: number
}

// StatsBucket is one time bucket in the `series` array. status_2xx/4xx/5xx
// partition the request count by response class for the status breakdown.
interface StatsBucket {
  ts: string
  requests: number
  tokens_in: number
  tokens_out: number
  avg_latency_ms: number
  p95_latency_ms: number
  status_2xx: number
  status_4xx: number
  status_5xx: number
}

// StatsTopRow is one entry in the `top` array — the per-group rollup for the
// selected group_by dimension over the whole range.
interface StatsTopRow {
  key: string
  requests: number
  tokens_in: number
  tokens_out: number
  errors: number
  error_rate: number
  avg_latency_ms: number
}

// StatsResponse is the full payload of
// GET /api/v1/observability/stats?from&to&bucket&group_by.
interface StatsResponse {
  from: string
  to: string
  bucket_seconds: number
  totals: StatsTotals
  series: StatsBucket[]
  top: StatsTopRow[]
}

type RangeKey = "1h" | "24h" | "7d" | "custom"

type GroupBy = "model" | "configuration" | "backend" | "gateway" | "protocol"

const RANGE_OPTIONS: { value: RangeKey; label: string }[] = [
  { value: "1h", label: "1h" },
  { value: "24h", label: "24h" },
  { value: "7d", label: "7d" },
  { value: "custom", label: "Custom" },
]

const GROUP_OPTIONS: { value: GroupBy; label: string }[] = [
  { value: "model", label: "Model" },
  { value: "configuration", label: "Configuration" },
  { value: "backend", label: "Backend" },
  { value: "gateway", label: "Gateway" },
  { value: "protocol", label: "Protocol" },
]

// rangeWindow maps a preset to a (durationSeconds, bucketSeconds) pair. Bucket
// sizes are chosen so each range renders a readable ~12-48 points: 1h→5m,
// 24h→1h, 7d→6h.
const RANGE_WINDOW: Record<Exclude<RangeKey, "custom">, { durationSec: number; bucketSec: number }> = {
  "1h": { durationSec: 3600, bucketSec: 300 },
  "24h": { durationSec: 86400, bucketSec: 3600 },
  "7d": { durationSec: 604800, bucketSec: 21600 },
}

type FetchState =
  | { status: "loading" }
  | { status: "ok"; data: StatsResponse }
  | { status: "error"; message: string }

// toLocalInput renders an ISO instant as a value the datetime-local input
// understands (no timezone suffix, minute precision, local clock).
function toLocalInput(d: Date): string {
  const pad = (n: number) => String(n).padStart(2, "0")
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`
}

// bucketForSpan picks a bucket size for an arbitrary custom span so the chart
// keeps roughly 24-72 points regardless of how wide the operator drags it.
function bucketForSpan(spanSec: number): number {
  const target = spanSec / 48
  const steps = [60, 300, 900, 1800, 3600, 10800, 21600, 43200, 86400]
  for (const s of steps) {
    if (target <= s) return s
  }
  return steps[steps.length - 1]
}

export function CpDashboardPage() {
  const nav = useNavigate()
  const [range, setRange] = useState<RangeKey>("24h")
  const [groupBy, setGroupBy] = useState<GroupBy>("model")
  const [state, setState] = useState<FetchState>({ status: "loading" })
  const [nonce, setNonce] = useState(0)

  // Custom-range endpoints, only consulted when range === "custom".
  const [customFrom, setCustomFrom] = useState(() => toLocalInput(new Date(Date.now() - 86400_000)))
  const [customTo, setCustomTo] = useState(() => toLocalInput(new Date()))

  const window = useMemo(() => {
    if (range !== "custom") {
      const { durationSec, bucketSec } = RANGE_WINDOW[range]
      const to = new Date()
      const from = new Date(to.getTime() - durationSec * 1000)
      return { from: from.toISOString(), to: to.toISOString(), bucketSec }
    }
    const from = new Date(customFrom)
    const to = new Date(customTo)
    const spanSec = Math.max(60, (to.getTime() - from.getTime()) / 1000)
    return { from: from.toISOString(), to: to.toISOString(), bucketSec: bucketForSpan(spanSec) }
    // range/nonce drive a refetch; for non-custom ranges the timestamps are
    // recomputed each run so the window tracks "now".
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [range, customFrom, customTo, nonce])

  const valid = !Number.isNaN(new Date(window.from).getTime()) && !Number.isNaN(new Date(window.to).getTime())

  useEffect(() => {
    if (!valid) return
    let cancelled = false
    const qs = new URLSearchParams({
      from: window.from,
      to: window.to,
      bucket: String(window.bucketSec),
      group_by: groupBy,
    })
    apiFetch<StatsResponse>(`/api/v1/observability/stats?${qs.toString()}`)
      .then((data) => {
        if (!cancelled) setState({ status: "ok", data })
      })
      .catch((e) => {
        if (cancelled) return
        if (e instanceof UnauthorizedError) return nav("/login", { replace: true })
        // Keep prior data on a background-refresh failure (range/group
        // toggles refetch); only surface the error if there's nothing to show.
        setState((prev) => (prev.status === "ok" ? prev : { status: "error", message: apiErrorText(e) }))
      })
    return () => {
      cancelled = true
    }
  }, [window.from, window.to, window.bucketSec, groupBy, valid, nav])

  const refreshing = state.status === "loading"

  return (
    <div className="flex flex-col gap-3.5">
      <div className="flex items-start gap-3">
        <PageIcon className="mt-1" />
        <div className="flex-1 min-w-0">
          <h1 className="text-[24px] font-semibold tracking-[-0.02em]">Dashboard</h1>
          <div className="text-[13px] text-[color:var(--text-3)] mt-1">
            Fleet-wide telemetry aggregated from the gateways' request events.
          </div>
        </div>
        <div className="flex items-center gap-2">
          <Segmented value={range} onChange={setRange} options={RANGE_OPTIONS} />
          <Button
            variant="ghost"
            size="sm"
            onClick={() => setNonce((n) => n + 1)}
            disabled={refreshing}
            aria-label="Refresh dashboard"
          >
            <RefreshCw className={refreshing ? "animate-spin" : undefined} />{" "}
            <span className="hidden sm:inline">Refresh</span>
          </Button>
        </div>
      </div>

      {range === "custom" && (
        <CustomRange from={customFrom} to={customTo} onFrom={setCustomFrom} onTo={setCustomTo} />
      )}

      {!valid && <ErrorBody message="invalid custom range — check the from / to values" />}
      {valid && state.status === "loading" && <LoadingBody />}
      {valid && state.status === "error" && <ErrorBody message={state.message} />}
      {valid && state.status === "ok" && (
        <DashboardBody data={state.data} groupBy={groupBy} onGroupBy={setGroupBy} />
      )}
    </div>
  )
}

function CustomRange({
  from,
  to,
  onFrom,
  onTo,
}: {
  from: string
  to: string
  onFrom: (v: string) => void
  onTo: (v: string) => void
}) {
  const inputCls =
    "mono text-[12px] rounded-[var(--radius)] border border-[color:var(--border)] bg-[color:var(--bg-1)] px-2.5 py-1.5 text-[color:var(--text)] [color-scheme:dark]"
  return (
    <div className="flex flex-wrap items-center gap-2 text-[12px] text-[color:var(--text-3)]">
      <label className="flex items-center gap-1.5">
        <span>From</span>
        <input type="datetime-local" value={from} max={to} onChange={(e) => onFrom(e.target.value)} className={inputCls} />
      </label>
      <label className="flex items-center gap-1.5">
        <span>To</span>
        <input type="datetime-local" value={to} min={from} onChange={(e) => onTo(e.target.value)} className={inputCls} />
      </label>
    </div>
  )
}

function LoadingBody() {
  return (
    <div className="rounded-[var(--radius-lg)] border border-[color:var(--border)] bg-[color:var(--bg-1)] p-8 text-center text-[13px] text-[color:var(--text-3)]">
      Loading dashboard…
    </div>
  )
}

function ErrorBody({ message }: { message: string }) {
  return (
    <div
      className="rounded-[var(--radius-lg)] border p-5 text-[13px]"
      style={{
        color: "var(--err)",
        background: "var(--err-bg)",
        borderColor: "color-mix(in oklab, var(--err) 30%, var(--border))",
      }}
    >
      Failed to load dashboard: <span className="mono">{message}</span>
    </div>
  )
}

function DashboardBody({
  data,
  groupBy,
  onGroupBy,
}: {
  data: StatsResponse
  groupBy: GroupBy
  onGroupBy: (g: GroupBy) => void
}) {
  const t = data.totals
  const hasTraffic = t.requests > 0 || data.series.some((b) => b.requests > 0)

  if (!hasTraffic) {
    return (
      <>
        <SummaryTiles totals={t} />
        <div className="rounded-[var(--radius-lg)] border border-[color:var(--border)] bg-[color:var(--bg-1)] p-12 text-center text-[13px] text-[color:var(--text-3)]">
          No request events in this range. Gateways push these over OTLP as they serve traffic — widen
          the range or wait for the fleet to handle requests.
        </div>
      </>
    )
  }

  const errAccent: "ok" | "warn" | "err" =
    t.error_rate > 0.05 ? "err" : t.error_rate > 0.02 ? "warn" : "ok"

  return (
    <>
      <div className="grid grid-cols-2 md:grid-cols-3 lg:grid-cols-5 gap-3">
        <KPI label="Requests" value={fmt.compact(t.requests)} sub={`${fmt.compact(t.errors)} errored`} />
        <KPI label="Tokens in" value={fmt.compact(t.tokens_in)} sub="prompt + cache" />
        <KPI label="Tokens out" value={fmt.compact(t.tokens_out)} sub="completion" />
        <KPI
          label="Error rate"
          value={fmt.pct(t.error_rate)}
          sub={`${fmt.compact(t.errors)} / ${fmt.compact(t.requests)}`}
          accent={errAccent}
        />
        <KPI label="p95 latency" value={fmt.ms(t.p95_latency_ms)} sub={`avg ${fmt.ms(t.avg_latency_ms)}`} />
      </div>

      <div className="grid grid-cols-1 lg:grid-cols-2 gap-3.5">
        <ChartPanel title="Request volume" sub="requests per bucket">
          <LineChart
            series={[
              { name: "requests", points: data.series.map((b) => ({ timestamp: b.ts, value: b.requests })) },
            ]}
            height={180}
            color="var(--accent)"
            formatY={(v) => fmt.compact(Math.round(v))}
          />
        </ChartPanel>

        <ChartPanel title="Tokens" sub="in vs out per bucket">
          <LineChart
            series={[
              tokenSeries("tokens in", data.series, (b) => b.tokens_in),
              tokenSeries("tokens out", data.series, (b) => b.tokens_out),
            ]}
            height={180}
            formatY={(v) => fmt.compact(Math.round(v))}
          />
        </ChartPanel>

        <ChartPanel title="Latency" sub="avg vs p95 (ms) per bucket">
          <LineChart
            series={[
              { name: "avg", points: data.series.map((b) => ({ timestamp: b.ts, value: b.avg_latency_ms })) },
              { name: "p95", points: data.series.map((b) => ({ timestamp: b.ts, value: b.p95_latency_ms })) },
            ]}
            height={180}
            formatY={(v) => fmt.ms(Math.round(v))}
          />
        </ChartPanel>

        <ChartPanel title="Status breakdown" sub="2xx / 4xx / 5xx per bucket">
          <LineChart
            series={[
              statusSeries("2xx", "var(--ok)", data.series, (b) => b.status_2xx),
              statusSeries("4xx", "var(--warn)", data.series, (b) => b.status_4xx),
              statusSeries("5xx", "var(--err)", data.series, (b) => b.status_5xx),
            ]}
            height={180}
            formatY={(v) => fmt.compact(Math.round(v))}
          />
        </ChartPanel>
      </div>

      <TopPanel rows={data.top} groupBy={groupBy} onGroupBy={onGroupBy} />
    </>
  )
}

function SummaryTiles({ totals: t }: { totals: StatsTotals }) {
  return (
    <div className="grid grid-cols-2 md:grid-cols-3 lg:grid-cols-5 gap-3">
      <KPI label="Requests" value={fmt.compact(t.requests)} sub={`${fmt.compact(t.errors)} errored`} />
      <KPI label="Tokens in" value={fmt.compact(t.tokens_in)} sub="prompt + cache" />
      <KPI label="Tokens out" value={fmt.compact(t.tokens_out)} sub="completion" />
      <KPI label="Error rate" value={fmt.pct(t.error_rate)} sub="—" />
      <KPI label="p95 latency" value={fmt.ms(t.p95_latency_ms)} sub={`avg ${fmt.ms(t.avg_latency_ms)}`} />
    </div>
  )
}

function ChartPanel({ title, sub, children }: { title: string; sub: string; children: React.ReactNode }) {
  return (
    <PanelCard>
      <PanelHead title={title} sub={sub} />
      <div className="px-4 pt-3 pb-2 min-h-[180px]">{children}</div>
    </PanelCard>
  )
}

function tokenSeries(
  name: string,
  buckets: StatsBucket[],
  pick: (b: StatsBucket) => number,
): DashboardSeries {
  return { name, points: buckets.map((b) => ({ timestamp: b.ts, value: pick(b) })) }
}

function statusSeries(
  name: string,
  color: string,
  buckets: StatsBucket[],
  pick: (b: StatsBucket) => number,
): DashboardSeries & { color: string } {
  return { name, color, points: buckets.map((b) => ({ timestamp: b.ts, value: pick(b) })) }
}

function TopPanel({
  rows,
  groupBy,
  onGroupBy,
}: {
  rows: StatsTopRow[]
  groupBy: GroupBy
  onGroupBy: (g: GroupBy) => void
}) {
  const selector = <Segmented value={groupBy} onChange={onGroupBy} options={GROUP_OPTIONS} />
  if (!rows.length) {
    return (
      <PanelCard>
        <PanelHead title="Top by" action={selector} />
        <div className="px-4 py-8 text-center text-[12px] text-[color:var(--text-4)]">
          No traffic to rank for this dimension in the selected range.
        </div>
      </PanelCard>
    )
  }
  const max = Math.max(...rows.map((r) => r.requests))
  return (
    <PanelCard>
      <PanelHead title="Top by" action={selector} />
      <div className="px-4 py-3 flex flex-col gap-2.5 overflow-x-auto">
        {rows.map((r) => (
          <div
            key={r.key || "(none)"}
            className="grid grid-cols-[180px_1fr_auto_auto_auto] items-center gap-3 min-w-[34rem]"
          >
            <div className="mono text-[12px] truncate" title={r.key || "(none)"}>
              {r.key || "(none)"}
            </div>
            <div className="h-2 rounded-full bg-[color:var(--bg-2)] overflow-hidden">
              <div
                className="h-full rounded-full"
                style={{ width: `${max > 0 ? (r.requests / max) * 100 : 0}%`, background: "var(--accent)" }}
              />
            </div>
            <div className="mono tnum text-[12px] text-right w-14">{fmt.compact(r.requests)}</div>
            <div
              className="mono tnum text-[11.5px] text-right w-12"
              style={{ color: r.error_rate > 0.04 ? "var(--err)" : "var(--text-3)" }}
              title="error rate"
            >
              {fmt.pct(r.error_rate, 1)}
            </div>
            <div
              className="mono tnum text-[11.5px] text-right w-14 text-[color:var(--text-3)]"
              title="avg latency"
            >
              {fmt.ms(Math.round(r.avg_latency_ms))}
            </div>
          </div>
        ))}
      </div>
    </PanelCard>
  )
}
