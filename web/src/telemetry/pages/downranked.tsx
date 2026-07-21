import { useEffect, useState } from "react"
import { useNavigate } from "react-router"
import { RefreshCw } from "lucide-react"
import { Button } from "@/components/ui/button"
import { PanelCard, PanelHead } from "@/components/atoms/card"
import { KPI } from "@/components/atoms/kpi"
import { PageHeader } from "@/components/atoms/page-header"
import { Segmented } from "@/components/atoms/segmented"
import { SkeletonBlock } from "@/components/atoms/skeleton"
import { fmt } from "@/lib/fmt"
import { type DashboardWindow } from "@/lib/dashboard"
import { useAdviseSavings, type AdviseSavingsResponse } from "@/lib/advise"
import { AgentLink } from "../components/agent-link"

const WINDOWS: { value: DashboardWindow; label: string }[] = [
  { value: "1h", label: "1h" },
  { value: "24h", label: "24h" },
  { value: "7d", label: "7d" },
  { value: "30d", label: "30d" },
]

// DownrankedPage lists the conversations the routing judge pinned to a cheaper
// model over the window, with the spend the pin avoided (priced from the
// operator's advise.model_rates). The totals are the headline; each row links
// to the owning session. Read-only over the arbiter's advise_audit +
// request_events tables.
export function DownrankedPage() {
  const nav = useNavigate()
  const [window, setWindow] = useState<DashboardWindow>("24h")
  const savings = useAdviseSavings(window)

  useEffect(() => {
    if (savings.state.status === "unauthorized") nav("/login", { replace: true })
  }, [savings.state, nav])

  const refreshing = savings.state.status === "loading"

  return (
    <div className="flex flex-col gap-3.5">
      <PageHeader
        title="Downranked Conversations"
        sub="Agent conversations the routing judge pinned to a cheaper model, and the spend that avoided"
      >
        <Segmented value={window} onChange={setWindow} options={WINDOWS.map((w) => ({ value: w.value, label: w.label }))} />
        <Button variant="ghost" size="sm" onClick={() => savings.refetch()} disabled={refreshing} aria-label="Refresh">
          <RefreshCw className={refreshing ? "animate-spin" : undefined} /> <span className="hidden sm:inline">Refresh</span>
        </Button>
      </PageHeader>

      {savings.state.status === "error" && (
        <div
          className="rounded-[var(--radius-lg)] border border-[color:var(--border)] p-4 text-[13px]"
          style={{ color: "var(--err)", background: "var(--err-bg)" }}
        >
          Failed to load savings: <span className="mono">{savings.state.message}</span>
        </div>
      )}
      {savings.state.status === "loading" && <SkeletonBlock height={92} />}
      {savings.state.status === "ok" && <SavingsView window={window} data={savings.state.data} />}
    </div>
  )
}

// SavingsView renders the attribution totals as KPI tiles plus the
// per-conversation breakdown. Null counterfactuals (models without a configured
// rate) render as an em-dash — the API never guesses, neither does the UI.
function SavingsView({ window, data }: { window: DashboardWindow; data: AdviseSavingsResponse }) {
  const { totals } = data
  const rows = data.items ?? []
  return (
    <>
      <div className="grid grid-cols-2 lg:grid-cols-4 gap-3">
        <KPI
          label="Net saved"
          value={fmt.usd(totals.net_saved_usd)}
          sub={`over ${window}, after judge cost`}
          accent={totals.net_saved_usd >= 0 ? "ok" : "warn"}
        />
        <KPI label="Saved" value={fmt.usd(totals.saved_usd)} sub="vs counterfactual" />
        <KPI
          label="Pinned spend"
          value={fmt.usd(totals.actual_usd)}
          sub={`would have been ${fmt.usd(totals.counterfactual_usd)}`}
        />
        <KPI label="Judge cost" value={fmt.usd(totals.judge_cost_usd)} sub="charged against saving" />
      </div>

      <PanelCard>
        <PanelHead title="Down-ranked conversations" sub={rows.length > 0 ? `${rows.length} · ${window}` : undefined} />
        {rows.length === 0 ? (
          <div className="px-4 py-10 text-center text-[12.5px] text-[color:var(--text-4)]">
            No conversations were down-ranked in this window.
          </div>
        ) : (
          <div className="flex flex-col">
            {rows.map((r) => (
              <div
                key={`${r.conversation_id}:${r.pinned_model}`}
                className="grid grid-cols-[1fr_auto_auto_auto] items-center gap-2.5 px-4 py-2.5 border-t border-[color:var(--border)] first:border-t-0 text-[12px]"
              >
                <span className="min-w-0 flex items-baseline gap-1 truncate">
                  <AgentLink conversationId={r.conversation_id} sessionId={r.session_id} />
                  <span className="text-[color:var(--text-4)] shrink-0">
                    · {r.requested_model} → {r.pinned_model} · {r.pinned_requests} req
                  </span>
                </span>
                <span className="mono tnum text-[color:var(--text-3)]" title="pinned spend">{fmt.usd(r.actual_usd)}</span>
                <span className="mono tnum text-[color:var(--text-4)]" title="counterfactual">
                  {r.counterfactual_usd == null ? "—" : fmt.usd(r.counterfactual_usd)}
                </span>
                <span className="mono tnum" style={{ color: r.saved_usd == null ? "var(--text-4)" : "var(--ok)" }} title="saved">
                  {r.saved_usd == null ? "no rate" : `+${fmt.usd(r.saved_usd)}`}
                </span>
              </div>
            ))}
          </div>
        )}
      </PanelCard>
    </>
  )
}
