import { useEffect, useState } from "react"
import { useNavigate } from "react-router"
import { RefreshCw } from "lucide-react"
import { Button } from "@/components/ui/button"
import { KPI } from "@/components/atoms/kpi"
import { ProviderChip } from "@/components/atoms/provider-chip"
import { Segmented } from "@/components/atoms/segmented"
import { LineChart } from "@/components/atoms/line-chart"
import { PanelCard, PanelHead } from "@/components/atoms/card"
import { fmt } from "@/lib/fmt"
import { providerColor } from "@/lib/provider-color"
import {
  useDashboardSummary,
  useDashboardTimeseries,
  type DashboardSummary,
  type DashboardProviderHealth,
  type DashboardProviderRow,
  type DashboardEndpointRow,
  type DashboardConfigurationRow,
  type DashboardModelRow,
  type DashboardRuleFiredRow,
  type DashboardSeries,
  type DashboardWindow,
} from "@/lib/dashboard"

export function DashboardPage() {
  const [range, setRange] = useState<DashboardWindow>("24h")
  const { state, refetch } = useDashboardSummary(range)
  const nav = useNavigate()

  useEffect(() => {
    if (state.status === "unauthorized") {
      nav("/login", { replace: true })
    }
  }, [state, nav])

  const refreshing = state.status === "loading"

  const startedAt = state.status === "ok" ? state.data.gateway_started_at : null

  return (
    <div className="flex flex-col gap-3.5">
      <div className="flex items-start gap-3">
        <div className="flex-1 min-w-0">
          <h1 className="text-[24px] font-semibold tracking-[-0.02em]">Last {state.status === "ok" ? state.data.window : "24h"}</h1>
          <Uptime startedAt={startedAt} />
        </div>
        <div className="flex items-center gap-2">
          <Segmented
            value={range}
            onChange={setRange}
            options={[
              { value: "1h", label: "1h" },
              { value: "24h", label: "24h" },
            ]}
          />
          <Button variant="outline" size="sm" onClick={refetch} disabled={refreshing} aria-label="Refresh dashboard">
            <RefreshCw className={refreshing ? "animate-spin" : undefined} /> Refresh
          </Button>
        </div>
      </div>

      {state.status === "loading" && <LoadingBody />}
      {state.status === "error" && <ErrorBody message={state.message} />}
      {state.status === "ok" && <DashboardBody data={state.data} window={range} />}
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

function DashboardBody({ data: d, window }: { data: DashboardSummary; window: DashboardWindow }) {
  const successRate = d.totals.requests > 0 ? d.totals.requests_success / d.totals.requests : 0
  const errAccent: "ok" | "warn" | "err" =
    d.rates.error_rate > 0.05 ? "err" : d.rates.error_rate > 0.02 ? "warn" : "ok"

  return (
    <>
      <div className="grid grid-cols-2 md:grid-cols-3 lg:grid-cols-6 gap-3">
        <KPI
          label="Requests"
          value={fmt.compact(d.totals.requests)}
          sub={`${fmt.compact(d.totals.requests_success)} ok · ${fmt.compact(d.totals.requests_errored)} err`}
        />
        <KPI
          label="Error rate"
          value={fmt.pct(d.rates.error_rate)}
          sub={`${(successRate * 100).toFixed(2)}% success`}
          accent={errAccent}
        />
        <KPI
          label="p95 latency"
          value={fmt.ms(d.latency_ms.p95)}
          sub={`p50 ${fmt.ms(d.latency_ms.p50)} · p99 ${fmt.ms(d.latency_ms.p99)}`}
        />
        <KPI
          label="Requests / sec"
          value={d.rates.requests_per_second.toFixed(2)}
          sub={`avg · ${d.window}`}
          accent="ok"
        />
        <KPI
          label="Tokens in"
          value={fmt.compact(d.totals.tokens_in)}
          sub={tokensInSub(d.totals)}
        />
        <KPI
          label="Tokens out"
          value={fmt.compact(d.totals.tokens_out)}
          sub={`${tokensRatioLabel(d.totals)} I/O`}
        />
      </div>

      <div className="grid grid-cols-1 lg:grid-cols-2 gap-3.5">
        <TimeseriesPanel
          title="Requests per second"
          sub={`${d.window} · per snapshot interval`}
          series="rps"
          window={window}
          formatY={(v) => v.toFixed(2)}
        />
        <TimeseriesPanel
          title="Tokens per second"
          sub={`${d.window} · input + output`}
          series="tokens_per_second"
          window={window}
          colorByLabel="kind"
          formatY={(v) => v >= 1000 ? (v / 1000).toFixed(1) + "k" : v.toFixed(0)}
        />
      </div>

      <div className="grid grid-cols-1 lg:grid-cols-2 gap-3.5">
        <TimeseriesPanel
          title="Top providers · requests per second"
          sub={`${d.window} · per snapshot interval`}
          series="rps_by_provider_top5"
          window={window}
          colorByLabel="provider"
          formatY={(v) => v.toFixed(2)}
        />
        <TimeseriesPanel
          title="Top providers · error rate"
          sub={`${d.window} · per snapshot interval`}
          series="error_rate_by_provider_top5"
          window={window}
          colorByLabel="provider"
          formatY={(v) => v.toFixed(1) + "%"}
        />
      </div>

      <div className="grid grid-cols-1 lg:grid-cols-2 gap-3.5">
        <ByProviderStrip rows={d.by_provider ?? []} />
        <ProviderHealth rows={d.provider_health ?? []} />
      </div>

      <div className="grid grid-cols-1 lg:grid-cols-2 gap-3.5">
        <ByEndpointStrip rows={d.by_endpoint ?? []} window={d.window} />
        <ByConfigurationStrip rows={d.by_configuration ?? []} window={d.window} />
      </div>

      <RulesFired rows={d.rules_fired ?? []} />

      <ModelsCard rows={d.by_model ?? []} />
    </>
  )
}

function TimeseriesPanel({
  title,
  sub,
  series,
  window,
  colorByLabel,
  singleColor,
  formatY,
}: {
  title: string
  sub: string
  series: string
  window: DashboardWindow
  colorByLabel?: string
  singleColor?: string
  formatY?: (v: number) => string
}) {
  const { state } = useDashboardTimeseries(series, window)
  return (
    <PanelCard>
      <PanelHead title={title} sub={sub} />
      <div className="px-4 pt-3 pb-2 min-h-[180px]">
        {state.status === "loading" && (
          <div className="text-[12px] text-[color:var(--text-4)] grid place-items-center min-h-[180px]">Loading…</div>
        )}
        {state.status === "error" && (
          <div className="text-[12px] text-[color:var(--err)] grid place-items-center min-h-[180px]">{state.message}</div>
        )}
        {state.status === "ok" && state.data.series.length === 0 && (
          <div className="text-[12px] text-[color:var(--text-4)] grid place-items-center min-h-[180px]">
            No samples yet — gateway just started.
          </div>
        )}
        {state.status === "ok" && state.data.series.length > 0 && (
          <LineChart
            series={state.data.series.map((s) => colorize(s, colorByLabel, singleColor))}
            height={180}
            formatY={formatY}
          />
        )}
      </div>
    </PanelCard>
  )
}

function colorize(s: DashboardSeries, colorByLabel?: string, fallback?: string): DashboardSeries & { color?: string } {
  if (colorByLabel && s.labels?.[colorByLabel]) {
    return { ...s, color: providerColor(s.labels[colorByLabel]).fg }
  }
  if (fallback) {
    return { ...s, color: fallback }
  }
  return s
}

function ByProviderStrip({ rows }: { rows: DashboardProviderRow[] }) {
  if (!rows.length) return <EmptyCard title="Traffic by provider" sub="requests · 24h" message="No traffic recorded yet." />
  const max = Math.max(...rows.map((r) => r.requests))
  return (
    <PanelCard>
      <PanelHead title="Traffic by provider" sub="requests · 24h" />
      <div className="px-4 py-3 flex flex-col gap-2.5">
        {rows.map((r) => (
          <div key={r.provider} className="grid grid-cols-[110px_1fr_auto_auto_auto] items-center gap-3">
            <div>
              <ProviderChip name={r.provider} />
            </div>
            <div className="h-2 rounded-full bg-[color:var(--bg-2)] overflow-hidden">
              <div
                className="h-full rounded-full"
                style={{
                  width: `${(r.requests / max) * 100}%`,
                  background: "var(--accent)",
                }}
              />
            </div>
            <div className="mono tnum text-[12px] text-right w-14">{fmt.compact(r.requests)}</div>
            <div
              className="mono tnum text-[11.5px] text-right w-12"
              style={{ color: r.error_rate > 0.04 ? "var(--err)" : "var(--text-3)" }}
            >
              {fmt.pct(r.error_rate, 1)}
            </div>
            <div className="mono tnum text-[11.5px] text-right w-12 text-[color:var(--text-3)]">
              {fmt.ms(r.p95_latency_ms)}
            </div>
          </div>
        ))}
      </div>
    </PanelCard>
  )
}

function ByEndpointStrip({ rows, window }: { rows: DashboardEndpointRow[]; window: string }) {
  const sub = `requests · ${window}`
  if (!rows.length) return <EmptyCard title="Traffic by endpoint" sub={sub} message="No traffic recorded yet." />
  const max = Math.max(...rows.map((r) => r.requests))
  return (
    <PanelCard>
      <PanelHead title="Traffic by endpoint" sub={sub} />
      <div className="px-4 py-3 flex flex-col gap-2.5">
        {rows.map((r) => (
          <div
            key={`${r.provider}.${r.endpoint}`}
            className="grid grid-cols-[170px_1fr_auto_auto_auto] items-center gap-3"
          >
            <div className="flex items-center gap-2 min-w-0">
              <ProviderChip name={r.provider} />
              <span className="mono text-[11.5px] text-[color:var(--text-3)] truncate">{r.endpoint}</span>
            </div>
            <div className="h-2 rounded-full bg-[color:var(--bg-2)] overflow-hidden">
              <div
                className="h-full rounded-full"
                style={{
                  width: `${(r.requests / max) * 100}%`,
                  background: "var(--accent)",
                }}
              />
            </div>
            <div className="mono tnum text-[12px] text-right w-14">{fmt.compact(r.requests)}</div>
            <div
              className="mono tnum text-[11.5px] text-right w-12"
              style={{ color: r.error_rate > 0.04 ? "var(--err)" : "var(--text-3)" }}
            >
              {fmt.pct(r.error_rate, 1)}
            </div>
            <div className="mono tnum text-[11.5px] text-right w-12 text-[color:var(--text-3)]">
              {fmt.ms(r.p95_latency_ms)}
            </div>
          </div>
        ))}
      </div>
    </PanelCard>
  )
}

function ByConfigurationStrip({ rows, window }: { rows: DashboardConfigurationRow[]; window: string }) {
  const sub = `requests · ${window}`
  if (!rows.length) {
    return <EmptyCard title="Traffic by configuration" sub={sub} message="No traffic recorded yet." />
  }
  const max = Math.max(...rows.map((r) => r.requests))
  return (
    <PanelCard>
      <PanelHead title="Traffic by configuration" sub={sub} />
      <div className="px-4 py-3 flex flex-col gap-2.5">
        {rows.map((r) => (
          <div key={r.configuration} className="grid grid-cols-[170px_1fr_auto_auto_auto] items-center gap-3">
            <div className="mono text-[12px] truncate" title={r.configuration}>
              {r.configuration}
            </div>
            <div className="h-2 rounded-full bg-[color:var(--bg-2)] overflow-hidden">
              <div
                className="h-full rounded-full"
                style={{
                  width: `${(r.requests / max) * 100}%`,
                  background: "var(--accent)",
                }}
              />
            </div>
            <div className="mono tnum text-[12px] text-right w-14">{fmt.compact(r.requests)}</div>
            <div
              className="mono tnum text-[11.5px] text-right w-12"
              style={{ color: r.error_rate > 0.04 ? "var(--err)" : "var(--text-3)" }}
            >
              {fmt.pct(r.error_rate, 1)}
            </div>
            <div className="mono tnum text-[11.5px] text-right w-12 text-[color:var(--text-3)]">
              {fmt.ms(r.p95_latency_ms)}
            </div>
          </div>
        ))}
      </div>
    </PanelCard>
  )
}

function ProviderHealth({ rows }: { rows: DashboardProviderHealth[] }) {
  if (!rows.length) {
    return <EmptyCard title="Provider health" sub="5m error rate" message="No providers configured." />
  }
  const anyUnhealthy = rows.some((p) => !p.healthy && p.requests_5m > 0)
  return (
    <PanelCard accent={anyUnhealthy ? "warn" : "ok"}>
      <PanelHead title="Provider health" sub="5m error rate" />
      <div className="grid grid-cols-2">
        {rows.map((p, i) => {
          const isLastRow = i >= rows.length - 2
          const isRight = i % 2 === 1
          const hasTraffic = p.requests_5m > 0
          // Dot: green when healthy AND saw traffic, grey when idle
          // (no signal either way), red when unhealthy. Avoids the
          // misleading "everything's green" when nothing's running.
          const dotColor = !hasTraffic
            ? "var(--text-4)"
            : p.healthy
              ? "var(--ok)"
              : "var(--err)"
          return (
            <div
              key={p.provider}
              className="flex items-center gap-2 px-3.5 py-2.5"
              style={{
                borderRight: isRight ? "none" : "1px solid var(--border)",
                borderBottom: isLastRow ? "none" : "1px solid var(--border)",
              }}
            >
              <span className="inline-block size-1.5 rounded-full shrink-0" style={{ background: dotColor }} />
              <ProviderChip name={p.provider} />
              <span
                className="mono tnum ml-auto text-[11px] shrink-0"
                style={{ color: hasTraffic ? (p.healthy ? "var(--ok)" : "var(--err)") : "var(--text-4)" }}
                title={hasTraffic ? `${p.requests_5m} req in last 5m` : "no traffic in last 5m"}
              >
                {hasTraffic ? fmt.pctRaw(p.error_rate_5m * 100, 1) : "—"}
              </span>
            </div>
          )
        })}
      </div>
    </PanelCard>
  )
}

function RulesFired({ rows }: { rows: DashboardRuleFiredRow[] }) {
  if (!rows?.length) {
    return <EmptyCard title="Rules fired" sub="match counts · 24h" message="No rules have matched yet." />
  }
  const max = Math.max(...rows.map((r) => r.fire_count))
  return (
    <PanelCard>
      <PanelHead title="Rules fired" sub="match counts · 24h" />
      <div className="px-4 py-3 flex flex-col gap-2.5">
        {rows.map((r) => (
          <div key={r.rule_name} className="grid grid-cols-[1fr_auto_auto] items-center gap-3">
            <div className="mono text-[12px] truncate">{r.rule_name}</div>
            <div className="flex items-center gap-2">
              <div className="w-28 h-2 rounded-full bg-[color:var(--bg-2)] overflow-hidden">
                <div
                  className="h-full rounded-full"
                  style={{ width: `${(r.fire_count / max) * 100}%`, background: "var(--accent)" }}
                />
              </div>
              <div className="mono tnum text-[12px] w-12 text-right">{fmt.compact(r.fire_count)}</div>
            </div>
            <div className="text-[11px] text-[color:var(--text-3)] truncate">
              {(r.used_by_configurations?.length ?? 0) === 1
                ? r.used_by_configurations![0]
                : `${r.used_by_configurations?.length ?? 0} configs`}
            </div>
          </div>
        ))}
      </div>
    </PanelCard>
  )
}

function ModelsCard({ rows }: { rows: DashboardModelRow[] }) {
  if (!rows.length) {
    return <EmptyCard title="Models" sub="aggregated · 24h" message="No model traffic yet." />
  }
  const totalReq = rows.reduce((s, m) => s + m.requests, 0)
  return (
    <PanelCard>
      <PanelHead title="Models" sub="aggregated · 24h" />
      <table className="w-full text-[12.5px]">
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
              <td className="px-4 py-2">
                <ProviderChip name={m.provider} />
              </td>
              <td className="mono tnum text-right px-4 py-2">{fmt.compact(m.requests)}</td>
              <td className="mono tnum text-right px-4 py-2 text-[color:var(--text-4)]">
                {((m.requests / totalReq) * 100).toFixed(1)}%
              </td>
              <td className="mono tnum text-right px-4 py-2">{fmt.compact(m.tokens_in)}</td>
              <td className="mono tnum text-right px-4 py-2">{fmt.compact(m.tokens_out)}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </PanelCard>
  )
}

// tokensInSub renders the secondary line under the Tokens-in KPI tile,
// surfacing the cache breakdown when present (cache reads are a
// discount, cache writes a premium — operators care about both).
function tokensInSub(totals: DashboardSummary["totals"]): string {
  if (totals.tokens_cached === 0 && totals.tokens_cache_creation === 0) {
    return "no cache activity"
  }
  const parts: string[] = []
  if (totals.tokens_cached > 0) parts.push(`${fmt.compact(totals.tokens_cached)} cached`)
  if (totals.tokens_cache_creation > 0) parts.push(`${fmt.compact(totals.tokens_cache_creation)} write`)
  return parts.join(" · ")
}

// tokensRatioLabel formats Output:Input as "N:1" or "1:N" so the
// secondary line on the Tokens-out tile carries useful context
// without a second numeric KPI.
function tokensRatioLabel(totals: DashboardSummary["totals"]): string {
  if (totals.tokens_in === 0 && totals.tokens_out === 0) return "—"
  if (totals.tokens_in === 0) return "∞:1"
  const ratio = totals.tokens_out / totals.tokens_in
  if (ratio >= 1) return `${ratio.toFixed(2)}:1`
  return `1:${(1 / ratio).toFixed(2)}`
}

function EmptyCard({ title, sub, message }: { title: string; sub: string; message: string }) {
  return (
    <PanelCard>
      <PanelHead title={title} sub={sub} />
      <div className="px-4 py-8 text-center text-[12px] text-[color:var(--text-4)]">{message}</div>
    </PanelCard>
  )
}

function Uptime({ startedAt }: { startedAt: string | null }) {
  // Tick once per second so the suffix value ages without re-polling.
  // The startedAt timestamp is static, refetched only on summary poll.
  const [now, setNow] = useState(() => Date.now())
  useEffect(() => {
    const id = setInterval(() => setNow(Date.now()), 1000)
    return () => clearInterval(id)
  }, [])
  if (!startedAt) {
    return (
      <div className="text-[13px] text-[color:var(--text-3)] mt-1">
        Aggregated from in-process Prometheus registry. Refreshed every 30s.
      </div>
    )
  }
  const ms = now - new Date(startedAt).getTime()
  return (
    <div className="text-[13px] text-[color:var(--text-3)] mt-1">
      Gateway up <span className="mono tnum text-[color:var(--text-2)]">{fmt.uptime(ms)}</span> · refreshed every 30s
    </div>
  )
}
