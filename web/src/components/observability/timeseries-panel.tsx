// Shared presentational timeseries panel — renders a single line-chart
// panel from an already-fetched DashboardTimeseries (or its loading /
// error state). Each console wraps this with its own fetch hook (the
// gateway and CP hit different timeseries endpoints) and passes the
// resulting state down; the colourisation + empty/loading/error chrome
// lives here so both consoles draw identical charts.

import { LineChart } from "@/components/atoms/line-chart"
import { PanelCard, PanelHead } from "@/components/atoms/card"
import { providerColor } from "@/lib/provider-color"
import type { DashboardSeries, DashboardTimeseries } from "@/lib/observability-types"

export type TimeseriesState =
  | { status: "loading" }
  | { status: "error"; message: string }
  | { status: "ok"; data: DashboardTimeseries }

export type TimeseriesPanelViewProps = {
  title: string
  sub: string
  state: TimeseriesState
  colorByLabel?: string
  singleColor?: string
  formatY?: (v: number) => string
}

export function TimeseriesPanelView({
  title,
  sub,
  state,
  colorByLabel,
  singleColor,
  formatY,
}: TimeseriesPanelViewProps) {
  return (
    <PanelCard>
      <PanelHead title={title} sub={sub} />
      <div className="px-4 pt-3 pb-2 min-h-[180px]">
        {state.status === "loading" && (
          <div className="text-[12px] text-[color:var(--text-4)] grid place-items-center min-h-[180px]">
            Loading…
          </div>
        )}
        {state.status === "error" && (
          <div className="text-[12px] text-[color:var(--err)] grid place-items-center min-h-[180px]">
            {state.message}
          </div>
        )}
        {state.status === "ok" && state.data.series.length === 0 && (
          <div className="text-[12px] text-[color:var(--text-4)] grid place-items-center min-h-[180px]">
            No samples in this range yet.
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
