import { useEffect, useState } from "react"
import { useNavigate } from "react-router"
import { RefreshCw } from "lucide-react"
import { Button } from "@/components/ui/button"
import { KPI } from "@/components/atoms/kpi"
import { ProviderChip } from "@/components/atoms/provider-chip"
import { Segmented } from "@/components/atoms/segmented"
import { LineChart } from "@/components/atoms/line-chart"
import { PanelCard, PanelHead, TableScroll } from "@/components/atoms/card"
import { Skeleton, SkeletonTiles, SkeletonBlock } from "@/components/atoms/skeleton"
import { fmt } from "@/lib/fmt"
import {
  useDashboardSummary,
  useDashboardTimeseries,
  useDashboardSecurity,
  useDashboardSecurityAudit,
  type DashboardSummary,
  type DashboardSecurityCount,
  type ScanAuditEntry,
  type DashboardWindow,
} from "@/lib/dashboard"

// DashboardPage is the telemetry console's aggregate view. It reuses the shared
// dashboard data hooks (fed by the telemetry API client) and design-system
// atoms. Unlike the gateway dashboard it only requests the timeseries series
// the telemetry service produces (rps, error_rate) and reads its aggregates
// from the DB-backed summary; the rules/tags panels are meter rollups
// (invariant #4).
export function DashboardPage() {
  const [range, setRange] = useState<DashboardWindow>("1h")
  const { state, refetch } = useDashboardSummary(range)
  const nav = useNavigate()

  useEffect(() => {
    if (state.status === "unauthorized") nav("/login", { replace: true })
  }, [state, nav])

  const refreshing = state.status === "loading"

  return (
    <div className="flex flex-col gap-3.5">
      <div className="flex items-start gap-3">
        <div className="flex-1 min-w-0">
          <h1 className="text-[24px] font-semibold tracking-[-0.02em]">
            Last {state.status === "ok" ? state.data.window : range}
          </h1>
          <div className="text-[13px] text-[color:var(--text-3)] mt-1">
            Across all gateways · refreshed every 30s
          </div>
        </div>
        <div className="flex items-center gap-2">
          <Segmented
            value={range}
            onChange={setRange}
            options={[
              { value: "1h", label: "1h" },
              { value: "24h", label: "24h" },
              { value: "7d", label: "7d" },
              { value: "30d", label: "30d" },
            ]}
          />
          <Button variant="ghost" size="sm" onClick={refetch} disabled={refreshing} aria-label="Refresh dashboard">
            <RefreshCw className={refreshing ? "animate-spin" : undefined} /> <span className="hidden sm:inline">Refresh</span>
          </Button>
        </div>
      </div>

      {state.status === "loading" && <DashboardSkeleton />}
      {state.status === "error" && <Notice text={`Failed to load dashboard: ${state.message}`} tone="err" />}
      {state.status === "ok" && <Body data={state.data} window={range} />}
    </div>
  )
}

// DashboardSkeleton mirrors the above-the-fold layout — the five KPI tiles and
// the two series panels — so the first load holds the page shape instead of
// flashing a centered "Loading…". Background polls keep the prior data (the
// fetch hook never reverts to "loading"), so this only ever shows on first load.
function DashboardSkeleton() {
  return (
    <>
      <SkeletonTiles count={5} className="grid grid-cols-2 md:grid-cols-3 lg:grid-cols-5 gap-3" />
      <div className="grid grid-cols-1 lg:grid-cols-2 gap-3.5">
        {[0, 1].map((i) => (
          <PanelCard key={i}>
            <div className="flex items-center gap-2 px-4 py-3 border-b border-[color:var(--border)]">
              <Skeleton className="h-3.5 w-36" />
            </div>
            <div className="px-4 pt-3 pb-2">
              <SkeletonBlock height={180} />
            </div>
          </PanelCard>
        ))}
      </div>
    </>
  )
}

function Notice({ text, tone }: { text: string; tone?: "err" }) {
  return (
    <div
      className="rounded-[var(--radius-lg)] border border-[color:var(--border)] bg-[color:var(--bg-1)] p-6 text-center text-[13px]"
      style={tone === "err" ? { color: "var(--err)", background: "var(--err-bg)" } : { color: "var(--text-3)" }}
    >
      {text}
    </div>
  )
}

function Body({ data: d, window }: { data: DashboardSummary; window: DashboardWindow }) {
  const successRate = d.totals.requests > 0 ? d.totals.requests_success / d.totals.requests : 0
  const errAccent: "ok" | "warn" | "err" =
    d.rates.error_rate > 0.05 ? "err" : d.rates.error_rate > 0.02 ? "warn" : "ok"

  return (
    <>
      <div className="grid grid-cols-2 md:grid-cols-3 lg:grid-cols-5 gap-3">
        <KPI label="Requests" value={fmt.compact(d.totals.requests)} sub={`${fmt.compact(d.totals.requests_success)} ok · ${fmt.compact(d.totals.requests_errored)} err`} />
        <KPI label="Error rate" value={fmt.pct(d.rates.error_rate)} sub={`${(successRate * 100).toFixed(2)}% success`} accent={errAccent} />
        <KPI label="Requests / sec" value={d.rates.requests_per_second.toFixed(2)} sub={`avg · ${d.window}`} accent="ok" />
        <KPI label="Tokens in" value={fmt.compact(d.totals.tokens_in)} sub={fmt.compact(d.totals.tokens_cached) + " cached"} />
        <KPI label="Tokens out" value={fmt.compact(d.totals.tokens_out)} sub="completion" />
      </div>

      <div className="grid grid-cols-1 lg:grid-cols-2 gap-3.5">
        <SeriesPanel title="Requests per second" sub={`${d.window} · per bucket`} series="rps" window={window} formatY={(v) => v.toFixed(2)} />
        <SeriesPanel title="Error rate" sub={`${d.window} · per bucket`} series="error_rate" window={window} formatY={(v) => fmt.pct(v, 1)} />
      </div>

      <div className="grid grid-cols-1 lg:grid-cols-2 gap-3.5">
        <Strip title="Traffic by provider" sub={`requests · ${d.window}`} rows={d.by_provider ?? []} label={(r) => <ProviderChip name={r.provider} />} rowKey={(r) => r.provider} />
        <ProviderHealth rows={d.provider_health ?? []} />
      </div>

      <div className="grid grid-cols-1 lg:grid-cols-2 gap-3.5">
        <Strip
          title="Traffic by protocol"
          sub={`requests · ${d.window}`}
          rows={d.by_protocol ?? []}
          rowKey={(r) => `${r.provider}.${r.protocol}`}
          label={(r) => (
            <span className="flex items-center gap-2 min-w-0">
              <ProviderChip name={r.provider} />
              <span className="mono text-[11.5px] text-[color:var(--text-3)] truncate">{r.protocol}</span>
            </span>
          )}
        />
        <Strip title="Traffic by configuration" sub={`requests · ${d.window}`} rows={d.by_configuration ?? []} rowKey={(r) => r.configuration} label={(r) => <span className="mono text-[12px] truncate" title={r.configuration}>{r.configuration}</span>} />
      </div>

      <div className="grid grid-cols-1 lg:grid-cols-2 gap-3.5">
        <Fired title="Rules fired" sub={`match counts · ${d.window}`} rows={(d.rules_fired ?? []).map((r) => ({ key: r.rule_name, count: r.fire_count, configs: r.used_by_configurations }))} />
        <Fired title="Tags fired" sub={`apply counts · ${d.window}`} rows={(d.tags_fired ?? []).map((r) => ({ key: r.tag, count: r.apply_count, configs: r.used_by_configurations }))} />
      </div>

      <SecuritySection window={window} />

      <Models rows={d.by_model ?? []} window={d.window} />
    </>
  )
}

function SeriesPanel({ title, sub, series, window, formatY }: { title: string; sub: string; series: string; window: DashboardWindow; formatY?: (v: number) => string }) {
  const { state } = useDashboardTimeseries(series, window)
  return (
    <PanelCard>
      <PanelHead title={title} sub={sub} />
      <div className="px-4 pt-3 pb-2 min-h-[180px]">
        {state.status === "loading" && <SkeletonBlock height={180} />}
        {state.status === "error" && <Center tone="err">{state.message}</Center>}
        {state.status === "ok" && state.data.series.length === 0 && <Center>No samples in window.</Center>}
        {state.status === "ok" && state.data.series.length > 0 && <LineChart series={state.data.series} height={180} formatY={formatY} />}
      </div>
    </PanelCard>
  )
}

function Center({ children, tone }: { children: React.ReactNode; tone?: "err" }) {
  return (
    <div className="text-[12px] grid place-items-center min-h-[180px]" style={{ color: tone === "err" ? "var(--err)" : "var(--text-4)" }}>
      {children}
    </div>
  )
}

// Strip is the shared bar-row breakdown (provider / protocol / configuration):
// a label, a request-count bar, the count, and the error rate.
type StripRow = { requests: number; error_rate: number }
function Strip<T extends StripRow>({ title, sub, rows, label, rowKey }: { title: string; sub: string; rows: T[]; label: (r: T) => React.ReactNode; rowKey: (r: T) => string }) {
  if (!rows.length) return <Empty title={title} sub={sub} message="No traffic recorded yet." />
  const max = Math.max(1, ...rows.map((r) => r.requests))
  return (
    <PanelCard>
      <PanelHead title={title} sub={sub} />
      <div className="px-4 py-3 flex flex-col gap-2.5">
        {rows.map((r) => (
          <div key={rowKey(r)} className="grid grid-cols-[6.5rem_1fr_auto_auto] sm:grid-cols-[170px_1fr_auto_auto] items-center gap-2 sm:gap-3">
            <div className="min-w-0">{label(r)}</div>
            <div className="h-2 rounded-full bg-[color:var(--bg-2)] overflow-hidden">
              <div className="h-full rounded-full" style={{ width: `${(r.requests / max) * 100}%`, background: "var(--accent)" }} />
            </div>
            <div className="mono tnum text-[12px] text-right w-14">{fmt.compact(r.requests)}</div>
            <div className="mono tnum text-[11.5px] text-right w-12" style={{ color: r.error_rate > 0.04 ? "var(--err)" : "var(--text-3)" }}>{fmt.pct(r.error_rate, 1)}</div>
          </div>
        ))}
      </div>
    </PanelCard>
  )
}

function Fired({ title, sub, rows }: { title: string; sub: string; rows: { key: string; count: number; configs: string[] }[] }) {
  if (!rows.length) return <Empty title={title} sub={sub} message="Nothing recorded yet." />
  const max = Math.max(1, ...rows.map((r) => r.count))
  return (
    <PanelCard>
      <PanelHead title={title} sub={sub} />
      <div className="px-4 py-3 flex flex-col gap-2.5 overflow-x-auto">
        {rows.map((r) => (
          <div key={r.key} className="grid grid-cols-[1fr_auto_auto] items-center gap-3">
            <div className="mono text-[12px] truncate">{r.key}</div>
            <div className="flex items-center gap-2">
              <div className="w-28 h-2 rounded-full bg-[color:var(--bg-2)] overflow-hidden">
                <div className="h-full rounded-full" style={{ width: `${(r.count / max) * 100}%`, background: "var(--accent)" }} />
              </div>
              <div className="mono tnum text-[12px] w-12 text-right">{fmt.compact(r.count)}</div>
            </div>
            <div className="text-[11px] text-[color:var(--text-3)] truncate">
              {(r.configs?.length ?? 0) === 1 ? r.configs[0] : `${r.configs?.length ?? 0} configs`}
            </div>
          </div>
        ))}
      </div>
    </PanelCard>
  )
}

// SecuritySection renders the SlipSpace Arbiter dashboard rows — a posture
// strip (scanned / flagged / partial / clean) and the finding breakdowns (by
// check type, top categories). It self-fetches and renders nothing unless the
// scanner is enabled and the data has loaded: a scanner-off deployment shows
// no security rows at all, and a transient load/error simply omits the section
// rather than disturbing the rest of the dashboard. The top-categories list is
// already capped server-side, so the strip never grows unbounded.
function SecuritySection({ window }: { window: DashboardWindow }) {
  const { state } = useDashboardSecurity(window)
  if (state.status !== "ok" || !state.data.enabled) return null
  const d = state.data
  const flaggedPct = d.scanned > 0 ? d.flagged / d.scanned : 0
  return (
    <>
      <div className="flex items-baseline gap-2 pt-1 px-0.5">
        <span className="text-[11px] font-semibold uppercase tracking-[0.1em] text-[color:var(--text-3)]">Security</span>
        <span className="text-[11px] text-[color:var(--text-4)]">SlipSpace Arbiter · {d.window}</span>
      </div>
      <div className="grid grid-cols-2 md:grid-cols-4 gap-3">
        <KPI label="Scanned" value={fmt.compact(d.scanned)} sub={`requests · ${d.window}`} />
        <KPI label="Flagged" value={fmt.compact(d.flagged)} sub={`${fmt.pct(flaggedPct, 1)} of scanned`} accent={d.flagged > 0 ? "err" : "ok"} />
        <KPI label="Partial" value={fmt.compact(d.partial)} sub="inconclusive scans" accent={d.partial > 0 ? "warn" : undefined} />
        <KPI label="Clean" value={fmt.compact(d.clean)} sub="no findings" accent="ok" />
      </div>
      <div className="grid grid-cols-1 lg:grid-cols-2 gap-3.5">
        <CountStrip title="Findings by check type" sub={`finding counts · ${d.window}`} rows={d.by_check_type ?? []} empty="No findings recorded yet." />
        <CountStrip title="Top finding categories" sub={`most common · ${d.window}`} rows={d.top_categories ?? []} empty="No findings recorded yet." />
      </div>
      <ScanFailures window={window} />
    </>
  )
}

// reasonTone maps a scan-failure reason to a design-system tone for its chip.
// A timeout is a soft (warn) failure — the detector is reachable but slow; the
// rest are harder failures (transport down, detector erroring, misconfiguration)
// shown in the error tone.
function reasonTone(reason: string): "warn" | "err" {
  return reason === "timeout" ? "warn" : "err"
}

// ScanFailures renders the append-only log of operational scan failures — the
// WHEN behind the aggregate Partial count. It self-fetches and shows the Empty
// state when no failures fall in the window (detectors healthy). Full-width row;
// each entry is a local timestamp, a reason chip, the check type + detector,
// and a short correlation id.
function ScanFailures({ window }: { window: DashboardWindow }) {
  const { state } = useDashboardSecurityAudit(window)
  const title = "Scan failures"
  const sub = `operational · ${window}`
  if (state.status !== "ok") return <Empty title={title} sub={sub} message="Loading scan failures…" />
  const items = state.data.items ?? []
  if (!items.length) return <Empty title={title} sub={sub} message="No scan failures in this window — detectors healthy." />
  return (
    <PanelCard>
      <PanelHead title={title} sub={`${items.length} · ${window}`} />
      <div className="flex flex-col">
        {items.map((e: ScanAuditEntry, i: number) => {
          const tone = reasonTone(e.reason)
          const color = tone === "warn" ? "var(--warn)" : "var(--err)"
          return (
            <div
              key={`${e.correlation_id}:${e.unit_id}:${e.occurred_at}:${i}`}
              className="grid grid-cols-[auto_auto_1fr_auto] items-center gap-2.5 px-4 py-2 border-t border-[color:var(--border)] first:border-t-0"
            >
              <span className="mono tnum text-[11.5px] text-[color:var(--text-4)] shrink-0">{fmt.shortTime(e.occurred_at)}</span>
              <span
                className="text-[10.5px] uppercase tracking-[0.06em] font-medium rounded px-1.5 py-0.5 shrink-0"
                style={{ color, background: "var(--bg-2)" }}
              >
                {e.reason.replace(/_/g, " ")}
              </span>
              <span className="text-[12px] truncate min-w-0">
                <span className="mono">{e.check_type || "—"}</span>
                {e.detector_id ? <span className="text-[color:var(--text-4)]"> · {e.detector_id}</span> : null}
                {e.detail ? <span className="text-[color:var(--text-4)]" title={e.detail}> · {e.detail}</span> : null}
              </span>
              <span className="mono text-[11px] text-[color:var(--text-4)] text-right shrink-0" title={e.correlation_id}>
                {e.correlation_id.slice(0, 8)}
              </span>
            </div>
          )
        })}
      </div>
    </PanelCard>
  )
}

// CountStrip is the security breakdown bar-row: a key label, a count bar, the
// count. Simpler than Fired (no configurations / error rate) — findings carry
// only (key, count). Rows arrive pre-sorted + (for categories) pre-capped from
// the server.
function CountStrip({ title, sub, rows, empty }: { title: string; sub: string; rows: DashboardSecurityCount[]; empty: string }) {
  if (!rows.length) return <Empty title={title} sub={sub} message={empty} />
  const max = Math.max(1, ...rows.map((r) => r.count))
  return (
    <PanelCard>
      <PanelHead title={title} sub={sub} />
      <div className="px-4 py-3 flex flex-col gap-2.5">
        {rows.map((r) => (
          <div key={r.key} className="grid grid-cols-[1fr_auto] items-center gap-3">
            <div className="mono text-[12px] truncate" title={r.key}>{r.key}</div>
            <div className="flex items-center gap-2">
              <div className="w-28 h-2 rounded-full bg-[color:var(--bg-2)] overflow-hidden">
                <div className="h-full rounded-full" style={{ width: `${(r.count / max) * 100}%`, background: "var(--accent)" }} />
              </div>
              <div className="mono tnum text-[12px] w-12 text-right">{fmt.compact(r.count)}</div>
            </div>
          </div>
        ))}
      </div>
    </PanelCard>
  )
}

function ProviderHealth({ rows }: { rows: DashboardSummary["provider_health"] }) {
  if (!rows.length) return <Empty title="Provider health" sub="5m error rate" message="No providers seen yet." />
  const anyUnhealthy = rows.some((p) => !p.healthy && p.requests_5m > 0)
  return (
    <PanelCard accent={anyUnhealthy ? "warn" : "ok"}>
      <PanelHead title="Provider health" sub="5m error rate" />
      <div className="grid grid-cols-1 sm:grid-cols-2">
        {rows.map((p) => {
          const hasTraffic = p.requests_5m > 0
          const status = !hasTraffic ? "No traffic" : p.healthy ? "Healthy" : "Degraded"
          const dotColor = !hasTraffic ? "var(--text-4)" : p.healthy ? "var(--ok)" : "var(--err)"
          return (
            <div key={p.provider} className="flex items-center gap-2 px-3.5 py-2.5 border-[color:var(--border)] border-b [&:last-child]:border-b-0 sm:[&:nth-last-child(-n+2)]:border-b-0 sm:[&:nth-child(odd)]:border-r">
              <span className="inline-block size-1.5 rounded-full shrink-0" style={{ background: dotColor }} title={status} />
              <span className="sr-only">{status}:</span>
              <ProviderChip name={p.provider} />
              <span className="mono tnum ml-auto text-[11px] shrink-0" style={{ color: hasTraffic ? (p.healthy ? "var(--ok)" : "var(--err)") : "var(--text-4)" }}>
                {hasTraffic ? fmt.pct(p.error_rate_5m, 1) : "—"}
              </span>
            </div>
          )
        })}
      </div>
    </PanelCard>
  )
}

function Models({ rows, window }: { rows: DashboardSummary["by_model"]; window: string }) {
  if (!rows.length) return <Empty title="Models" sub={`aggregated · ${window}`} message="No model traffic yet." />
  const totalReq = rows.reduce((s, m) => s + m.requests, 0)
  return (
    <PanelCard>
      <PanelHead title="Models" sub={`aggregated · ${window}`} />
      <TableScroll>
        <thead>
          <tr className="text-[11px] uppercase tracking-[0.07em] text-[color:var(--text-3)]">
            <th className="text-left font-medium px-4 py-2">Model</th>
            <th className="text-left font-medium px-4 py-2">Provider</th>
            <th className="text-right font-medium px-4 py-2">Requests</th>
            <th className="text-right font-medium px-4 py-2">Share</th>
            <th className="text-right font-medium px-4 py-2">Tokens in</th>
            <th className="text-right font-medium px-4 py-2">Tokens out</th>
          </tr>
        </thead>
        <tbody>
          {rows.map((m) => (
            <tr key={m.model} className="border-t border-[color:var(--border)]">
              <td className="mono px-4 py-2">{m.model}</td>
              <td className="px-4 py-2"><ProviderChip name={m.provider} /></td>
              <td className="mono tnum text-right px-4 py-2">{fmt.compact(m.requests)}</td>
              <td className="mono tnum text-right px-4 py-2 text-[color:var(--text-4)]">{((m.requests / totalReq) * 100).toFixed(1)}%</td>
              <td className="mono tnum text-right px-4 py-2">{fmt.compact(m.tokens_in)}</td>
              <td className="mono tnum text-right px-4 py-2">{fmt.compact(m.tokens_out)}</td>
            </tr>
          ))}
        </tbody>
      </TableScroll>
    </PanelCard>
  )
}

function Empty({ title, sub, message }: { title: string; sub: string; message: string }) {
  return (
    <PanelCard>
      <PanelHead title={title} sub={sub} />
      <div className="px-4 py-8 text-center text-[12px] text-[color:var(--text-4)]">{message}</div>
    </PanelCard>
  )
}
