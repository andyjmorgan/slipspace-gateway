import { useEffect, useState } from "react"
import { useNavigate } from "react-router"
import { RefreshCw } from "lucide-react"
import { Button } from "@/components/ui/button"
import { Segmented } from "@/components/atoms/segmented"
import { PageIcon } from "@/components/atoms/page-states"
import { DashboardView, type TimeseriesPanelProps } from "@/components/observability/dashboard-view"
import { TimeseriesPanelView } from "@/components/observability/timeseries-panel"
import { fmt } from "@/lib/fmt"
import { useDashboardSummary, useDashboardTimeseries, type DashboardWindow } from "@/lib/dashboard"

// GatewayTimeseriesPanel wires the shared timeseries chrome to the gateway
// admin /dashboard/timeseries endpoint via useDashboardTimeseries.
function GatewayTimeseriesPanel({ title, sub, series, window, colorByLabel, singleColor, formatY }: TimeseriesPanelProps) {
  const { state } = useDashboardTimeseries(series, window)
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
        <PageIcon className="mt-1" />
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
          <Button variant="ghost" size="sm" onClick={refetch} disabled={refreshing} aria-label="Refresh dashboard">
            <RefreshCw className={refreshing ? "animate-spin" : undefined} /> <span className="hidden sm:inline">Refresh</span>
          </Button>
        </div>
      </div>

      {state.status === "loading" && <LoadingBody />}
      {state.status === "error" && <ErrorBody message={state.message} />}
      {state.status === "ok" && (
        <DashboardView data={state.data} window={range} TimeseriesPanel={GatewayTimeseriesPanel} />
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

function Uptime({ startedAt }: { startedAt: string | null }) {
  // Tick once per second so the suffix value ages without re-polling. The
  // startedAt timestamp is static, refetched only on summary poll.
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
