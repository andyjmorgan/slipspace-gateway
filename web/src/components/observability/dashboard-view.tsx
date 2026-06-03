// Shared presentational dashboard — renders a DashboardSummary (the wire
// shape both the gateway admin API and the control-plane observability API
// return) into the KPI strip + traffic/health/rules/tags/models panels.
//
// Timeseries panels are fed per-console: each console hits a different API
// path for the line-chart series, so DashboardView takes a Timeseries
// component prop (wired to that console's fetch hook) rather than fetching
// itself. Everything below the charts is driven purely off the summary
// prop, so the two consoles render identical surfaces from identical data.

import type { ComponentType } from "react"
import { KPI } from "@/components/atoms/kpi"
import { ProviderChip } from "@/components/atoms/provider-chip"
import { PanelCard, PanelHead, TableScroll } from "@/components/atoms/card"
import { fmt } from "@/lib/fmt"
import type {
  DashboardSummary,
  DashboardProviderHealth,
  DashboardProviderRow,
  DashboardEndpointRow,
  DashboardConfigurationRow,
  DashboardModelRow,
  DashboardRuleFiredRow,
  DashboardTagFiredRow,
  DashboardWindow,
} from "@/lib/observability-types"

// TimeseriesPanelProps is the contract each console's timeseries panel
// component fulfils. DashboardView places these panels and names the
// series; the console-supplied component owns the fetch + render.
export type TimeseriesPanelProps = {
  title: string
  sub: string
  series: string
  window: DashboardWindow
  colorByLabel?: string
  singleColor?: string
  formatY?: (v: number) => string
}

export type DashboardViewProps = {
  data: DashboardSummary
  window: DashboardWindow
  // TimeseriesPanel renders one line-chart panel for a named series,
  // fetched via the owning console's hook.
  TimeseriesPanel: ComponentType<TimeseriesPanelProps>
}

export function DashboardView({ data: d, window, TimeseriesPanel }: DashboardViewProps) {
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
        <KPI label="Tokens in" value={fmt.compact(d.totals.tokens_in)} sub={tokensInSub(d.totals)} />
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
          formatY={(v) => (v >= 1000 ? (v / 1000).toFixed(1) + "k" : v.toFixed(0))}
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
        <ByProviderStrip rows={d.by_provider ?? []} window={d.window} />
        <ProviderHealth rows={d.provider_health ?? []} />
      </div>

      <div className="grid grid-cols-1 lg:grid-cols-2 gap-3.5">
        <ByEndpointStrip rows={d.by_endpoint ?? []} window={d.window} />
        <ByConfigurationStrip rows={d.by_configuration ?? []} window={d.window} />
      </div>

      <div className="grid grid-cols-1 lg:grid-cols-2 gap-3.5">
        <RulesFired rows={d.rules_fired ?? []} window={d.window} />
        <TagsFired rows={d.tags_fired ?? []} window={d.window} />
      </div>

      <ModelsCard rows={d.by_model ?? []} window={d.window} />
    </>
  )
}

function ByProviderStrip({ rows, window }: { rows: DashboardProviderRow[]; window: string }) {
  const sub = `requests · ${window}`
  if (!rows.length) return <EmptyCard title="Traffic by provider" sub={sub} message="No traffic recorded yet." />
  const max = Math.max(...rows.map((r) => r.requests))
  return (
    <PanelCard>
      <PanelHead title="Traffic by provider" sub={sub} />
      <div className="px-4 py-3 flex flex-col gap-2.5 overflow-x-auto">
        {rows.map((r) => (
          <div key={r.provider} className="grid grid-cols-[110px_1fr_auto_auto_auto] items-center gap-3">
            <div>
              <ProviderChip name={r.provider} />
            </div>
            <div className="h-2 rounded-full bg-[color:var(--bg-2)] overflow-hidden">
              <div
                className="h-full rounded-full"
                style={{ width: `${(r.requests / max) * 100}%`, background: "var(--accent)" }}
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
      <div className="px-4 py-3 flex flex-col gap-2.5 overflow-x-auto">
        {rows.map((r) => (
          <div
            key={`${r.provider}.${r.endpoint}`}
            className="grid grid-cols-[170px_1fr_auto_auto_auto] items-center gap-3 min-w-[30rem]"
          >
            <div className="flex items-center gap-2 min-w-0">
              <ProviderChip name={r.provider} />
              <span className="mono text-[11.5px] text-[color:var(--text-3)] truncate">{r.endpoint}</span>
            </div>
            <div className="h-2 rounded-full bg-[color:var(--bg-2)] overflow-hidden">
              <div
                className="h-full rounded-full"
                style={{ width: `${(r.requests / max) * 100}%`, background: "var(--accent)" }}
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
      <div className="px-4 py-3 flex flex-col gap-2.5 overflow-x-auto">
        {rows.map((r) => (
          <div key={r.configuration} className="grid grid-cols-[170px_1fr_auto_auto_auto] items-center gap-3 min-w-[30rem]">
            <div className="mono text-[12px] truncate" title={r.configuration}>
              {r.configuration}
            </div>
            <div className="h-2 rounded-full bg-[color:var(--bg-2)] overflow-hidden">
              <div
                className="h-full rounded-full"
                style={{ width: `${(r.requests / max) * 100}%`, background: "var(--accent)" }}
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
          // Dot: green when healthy AND saw traffic, grey when idle (no
          // signal either way), red when unhealthy. Avoids the misleading
          // "everything's green" when nothing's running.
          const dotColor = !hasTraffic ? "var(--text-4)" : p.healthy ? "var(--ok)" : "var(--err)"
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

function RulesFired({ rows, window }: { rows: DashboardRuleFiredRow[]; window: string }) {
  const sub = `match counts · ${window}`
  if (!rows?.length) {
    return <EmptyCard title="Rules fired" sub={sub} message="No rules have matched yet." />
  }
  const max = Math.max(...rows.map((r) => r.fire_count))
  return (
    <PanelCard>
      <PanelHead title="Rules fired" sub={sub} />
      <div className="px-4 py-3 flex flex-col gap-2.5 overflow-x-auto">
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

// TagsFired mirrors the RulesFired panel — same layout, same accent colour,
// same "N configs" attribution. Operators get a parallel surface for
// tag-applied counts so the two concerns read at-a-glance without forcing
// them to mentally translate between rule names and tag names.
function TagsFired({ rows, window }: { rows: DashboardTagFiredRow[]; window: string }) {
  const sub = `apply counts · ${window}`
  if (!rows?.length) {
    return <EmptyCard title="Tags fired" sub={sub} message="No tagging rules have fired yet." />
  }
  const max = Math.max(...rows.map((r) => r.apply_count))
  return (
    <PanelCard>
      <PanelHead title="Tags fired" sub={sub} />
      <div className="px-4 py-3 flex flex-col gap-2.5 overflow-x-auto">
        {rows.map((r) => (
          <div key={r.tag} className="grid grid-cols-[1fr_auto_auto] items-center gap-3">
            <TagChip name={r.tag} />
            <div className="flex items-center gap-2">
              <div className="w-28 h-2 rounded-full bg-[color:var(--bg-2)] overflow-hidden">
                <div
                  className="h-full rounded-full"
                  style={{ width: `${(r.apply_count / max) * 100}%`, background: "var(--accent)" }}
                />
              </div>
              <div className="mono tnum text-[12px] w-12 text-right">{fmt.compact(r.apply_count)}</div>
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

// TagChip renders a tag as a small monospace chip. Same visual language as
// ProviderChip but dimmer so a long list of tags reads as a chip cluster
// rather than a column of provider chips.
function TagChip({ name }: { name: string }) {
  return (
    <span className="inline-flex items-center px-1.5 py-0.5 rounded-[4px] text-[10.5px] mono uppercase tracking-[0.04em] border border-[color:var(--border)] text-[color:var(--text-2)] bg-[color:var(--bg-2)]">
      {name}
    </span>
  )
}

function ModelsCard({ rows, window }: { rows: DashboardModelRow[]; window: string }) {
  const sub = `aggregated · ${window}`
  if (!rows.length) {
    return <EmptyCard title="Models" sub={sub} message="No model traffic yet." />
  }
  const totalReq = rows.reduce((s, m) => s + m.requests, 0)
  return (
    <PanelCard>
      <PanelHead title="Models" sub={sub} />
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
              <td className="px-4 py-2">
                <ProviderChip name={m.provider} />
              </td>
              <td className="mono tnum text-right px-4 py-2">{fmt.compact(m.requests)}</td>
              <td className="mono tnum text-right px-4 py-2 text-[color:var(--text-4)]">
                {totalReq > 0 ? ((m.requests / totalReq) * 100).toFixed(1) : "0.0"}%
              </td>
              <td className="mono tnum text-right px-4 py-2">{fmt.compact(m.tokens_in)}</td>
              <td className="mono tnum text-right px-4 py-2">{fmt.compact(m.tokens_out)}</td>
            </tr>
          ))}
        </tbody>
      </TableScroll>
    </PanelCard>
  )
}

// tokensInSub renders the secondary line under the Tokens-in KPI tile,
// surfacing the cache breakdown when present (cache reads are a discount,
// cache writes a premium — operators care about both).
function tokensInSub(totals: DashboardSummary["totals"]): string {
  if (totals.tokens_cached === 0 && totals.tokens_cache_creation === 0) {
    return "no cache activity"
  }
  const parts: string[] = []
  if (totals.tokens_cached > 0) parts.push(`${fmt.compact(totals.tokens_cached)} cached`)
  if (totals.tokens_cache_creation > 0) parts.push(`${fmt.compact(totals.tokens_cache_creation)} write`)
  return parts.join(" · ")
}

// tokensRatioLabel formats Output:Input as "N:1" or "1:N" so the secondary
// line on the Tokens-out tile carries useful context without a second
// numeric KPI.
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
