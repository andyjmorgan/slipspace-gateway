import { useEffect, useState } from "react"
import { useNavigate } from "react-router"
import { RefreshCw } from "lucide-react"
import { Button } from "@/components/ui/button"
import { PanelCard, PanelHead } from "@/components/atoms/card"
import { PageHeader } from "@/components/atoms/page-header"
import { Segmented } from "@/components/atoms/segmented"
import { SkeletonBlock } from "@/components/atoms/skeleton"
import { fmt } from "@/lib/fmt"
import { useDashboardSecurityAudit, type DashboardWindow, type ScanAuditEntry } from "@/lib/dashboard"

// reasonTone maps a scan-failure reason to a design-system tone. A timeout is a
// soft (warn) failure — the detector is reachable but slow; the rest are harder
// failures (transport down, detector erroring, misconfiguration) shown in error.
function reasonTone(reason: string): "warn" | "err" {
  return reason === "timeout" ? "warn" : "err"
}

// SecurityAuditPage is the operator view of the Arbiter scan-failure audit log:
// the append-only record of scans that did NOT complete (timeout / unreachable /
// detector_error / no_detector / unit_missing), newest first, over a selectable
// window. It is the WHEN behind the dashboard's aggregate Partial/inconclusive
// count — a dedicated triage surface, not a home-page panel. Sibling of the
// Findings page under the Security section. Empty window = detectors healthy.
export function SecurityAuditPage() {
  const nav = useNavigate()
  const [window, setWindow] = useState<DashboardWindow>("24h")
  const { state, refetch } = useDashboardSecurityAudit(window)

  useEffect(() => {
    if (state.status === "unauthorized") nav("/login", { replace: true })
  }, [state, nav])

  const items: ScanAuditEntry[] = state.status === "ok" ? (state.data.items ?? []) : []
  const refreshing = state.status === "loading"

  return (
    <div className="flex flex-col gap-3.5">
      <PageHeader title="Scan audit" sub="Operational scan failures — when a detector timed out, was unreachable, or errored">
        <Segmented
          value={window}
          onChange={setWindow}
          options={[
            { value: "1h", label: "1h" },
            { value: "24h", label: "24h" },
            { value: "7d", label: "7d" },
            { value: "30d", label: "30d" },
          ]}
        />
        <Button variant="ghost" size="sm" onClick={refetch} disabled={refreshing} aria-label="Refresh">
          <RefreshCw className={refreshing ? "animate-spin" : undefined} /> <span className="hidden sm:inline">Refresh</span>
        </Button>
      </PageHeader>

      {state.status === "error" && (
        <div
          className="rounded-[var(--radius-lg)] border border-[color:var(--border)] p-4 text-[13px]"
          style={{ color: "var(--err)", background: "var(--err-bg)" }}
        >
          Failed to load scan audit: <span className="mono">{state.message}</span>
        </div>
      )}

      <PanelCard>
        <PanelHead
          title="Scan failures"
          sub={state.status === "ok" ? `${items.length} · ${window}` : window}
        />
        {state.status === "loading" && (
          <div className="px-4 py-3">
            <SkeletonBlock height={180} />
          </div>
        )}
        {state.status === "ok" && items.length === 0 && (
          <div className="px-4 py-10 text-center text-[12.5px] text-[color:var(--text-4)]">
            No scan failures in this window — detectors healthy.
          </div>
        )}
        {state.status === "ok" && items.length > 0 && (
          <div className="flex flex-col">
            {items.map((e, i) => {
              const color = reasonTone(e.reason) === "warn" ? "var(--warn)" : "var(--err)"
              return (
                <div
                  key={`${e.correlation_id}:${e.unit_id}:${e.occurred_at}:${i}`}
                  className="grid grid-cols-[auto_auto_1fr_auto] items-center gap-2.5 px-4 py-2.5 border-t border-[color:var(--border)] first:border-t-0"
                >
                  <span className="mono tnum text-[11.5px] text-[color:var(--text-4)] shrink-0 whitespace-nowrap">
                    {new Date(e.occurred_at).toLocaleString("en-GB", { hour12: false })}
                  </span>
                  <span
                    className="text-[10.5px] uppercase tracking-[0.06em] font-medium rounded px-1.5 py-0.5 shrink-0"
                    style={{ color, background: "var(--bg-2)" }}
                  >
                    {e.reason.replace(/_/g, " ")}
                  </span>
                  <span className="text-[12px] truncate min-w-0">
                    <span className="mono">{e.check_type || "—"}</span>
                    {e.detector_id ? <span className="text-[color:var(--text-4)]"> · {e.detector_id}</span> : null}
                    {e.attempts > 0 ? <span className="text-[color:var(--text-4)]"> · {e.attempts} {e.attempts === 1 ? "try" : "tries"}</span> : null}
                    {e.detail ? <span className="text-[color:var(--text-4)]" title={e.detail}> · {e.detail}</span> : null}
                  </span>
                  <span
                    className="mono text-[11px] text-[color:var(--text-4)] text-right shrink-0"
                    title={`${e.correlation_id} · ${fmt.ago(e.occurred_at)}`}
                  >
                    {e.correlation_id.slice(0, 8)}
                  </span>
                </div>
              )
            })}
          </div>
        )}
      </PanelCard>
    </div>
  )
}
