import { useEffect, useState } from "react"
import { useNavigate } from "react-router"
import { RefreshCw } from "lucide-react"
import { Button } from "@/components/ui/button"
import { Segmented } from "@/components/atoms/segmented"
import { PageIcon } from "@/components/atoms/page-states"
import { DashboardView, type TimeseriesPanelProps } from "@/components/observability/dashboard-view"
import { TimeseriesPanelView } from "@/components/observability/timeseries-panel"
import type { DashboardWindow } from "@/lib/observability-types"
import { useCpDashboardSummary, useCpDashboardTimeseries } from "../lib/observability"

// CpTimeseriesPanel wires the shared timeseries chrome to the CP
// observability /dashboard/timeseries endpoint.
function CpTimeseriesPanel({ title, sub, series, window, colorByLabel, singleColor, formatY }: TimeseriesPanelProps) {
  const { state } = useCpDashboardTimeseries(series, window)
  const view =
    state.status === "ok"
      ? ({ status: "ok", data: state.data } as const)
      : state.status === "error"
        ? ({ status: "error", message: state.message } as const)
        : ({ status: "loading" } as const)
  return (
    <TimeseriesPanelView
      title={title}
      sub={sub}
      state={view}
      colorByLabel={colorByLabel}
      singleColor={singleColor}
      formatY={formatY}
    />
  )
}

// CpDashboardPage is the fleet-wide dashboard. It renders the same rich
// surface as the gateway console (DashboardView), fed by the DB-backed CP
// observability summary + timeseries API rather than a single gateway's
// in-process Prometheus registry.
export function CpDashboardPage() {
  const nav = useNavigate()
  const [range, setRange] = useState<DashboardWindow>("24h")
  const { state, refetch } = useCpDashboardSummary(range)

  useEffect(() => {
    if (state.status === "unauthorized") {
      nav("/login", { replace: true })
    }
  }, [state, nav])

  const refreshing = state.status === "loading"

  return (
    <div className="flex flex-col gap-3.5">
      <div className="flex items-start gap-3">
        <PageIcon className="mt-1" />
        <div className="flex-1 min-w-0">
          <h1 className="text-[24px] font-semibold tracking-[-0.02em]">Dashboard</h1>
          <div className="text-[13px] text-[color:var(--text-3)] mt-1">
            Fleet-wide telemetry aggregated from the gateways' request events. Refreshed every 30s.
          </div>
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
          <Button variant="ghost" size="sm" onClick={refetch} disabled={refreshing} aria-label="Refresh dashboard">
            <RefreshCw className={refreshing ? "animate-spin" : undefined} /> <span className="hidden sm:inline">Refresh</span>
          </Button>
        </div>
      </div>

      {state.status === "loading" && <LoadingBody />}
      {state.status === "error" && <ErrorBody message={state.message} />}
      {state.status === "ok" && (
        <DashboardView data={state.data} window={range} TimeseriesPanel={CpTimeseriesPanel} />
      )}
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
