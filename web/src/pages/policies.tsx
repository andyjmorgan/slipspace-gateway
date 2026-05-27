import { usePolicies, type PolicySummary, type PolicyTarget } from "@/lib/config-api"
import { PanelCard } from "@/components/atoms/card"
import { Tag } from "@/components/atoms/tag"
import {
  PageHeader,
  LoadingPanel,
  ErrorPanel,
  EmptyPanel,
  useUnauthorizedRedirect,
} from "@/components/atoms/page-states"

export function PoliciesPage() {
  const { state } = usePolicies()
  useUnauthorizedRedirect(state)

  return (
    <div>
      <PageHeader
        title="Resilience policies"
        sub="Failover and load-balance targets with live per-pod circuit-breaker state. Read-only for now; editing lands in v1.3."
      />
      {state.status === "loading" && <LoadingPanel />}
      {state.status === "error" && <ErrorPanel message={state.message} />}
      {state.status === "ok" && state.data.policies.length === 0 && (
        <EmptyPanel message="No resilience policies configured." />
      )}
      {state.status === "ok" && state.data.policies.length > 0 && (
        <div className="space-y-3">
          {state.data.policies.map((p) => (
            <PolicyCard key={p.name} pol={p} />
          ))}
          <div className="text-[11px] text-[color:var(--text-4)] mono pl-2">
            pod {state.data.pod}
          </div>
        </div>
      )}
    </div>
  )
}

function PolicyCard({ pol }: { pol: PolicySummary }) {
  return (
    <PanelCard>
      <div className="px-4 py-3 border-b border-[color:var(--border)] flex items-center gap-2">
        <span className="font-semibold text-[13.5px]">{pol.name}</span>
        <Tag variant="violet">{pol.mode}</Tag>
        {pol.strict_weights && <Tag variant="warn">strict_weights</Tag>}
        {pol.circuit_breaker_enabled && <Tag variant="violet">cb enabled</Tag>}
        {pol.failure_status_codes && pol.failure_status_codes.length > 0 && (
          <span className="mono text-[11px] text-[color:var(--text-3)]">
            retry on {pol.failure_status_codes.join(", ")}
          </span>
        )}
      </div>
      <table className="w-full text-[12.5px]">
        <thead>
          <tr className="text-[11px] uppercase tracking-[0.07em] text-[color:var(--text-3)]">
            <th className="text-left font-medium px-4 py-2">Target</th>
            <th className="text-left font-medium px-4 py-2">Provider</th>
            <th className="text-right font-medium px-4 py-2">Order</th>
            <th className="text-right font-medium px-4 py-2">Weight</th>
            <th className="text-left font-medium px-4 py-2">Circuit state</th>
          </tr>
        </thead>
        <tbody>
          {pol.targets.map((t) => (
            <tr key={t.name} className="border-t border-[color:var(--border)]">
              <td className="px-4 py-2.5 font-medium">{t.name}</td>
              <td className="mono px-4 py-2.5 text-[color:var(--text-2)]">
                {t.provider ?? <span className="text-[color:var(--text-4)]">—</span>}
              </td>
              <td className="mono tnum text-right px-4 py-2.5">{t.order ?? "—"}</td>
              <td className="mono tnum text-right px-4 py-2.5">{t.weight ?? "—"}</td>
              <td className="px-4 py-2.5">
                <CircuitStateBadge state={t.circuit_state} />
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </PanelCard>
  )
}

function CircuitStateBadge({ state }: { state: PolicyTarget["circuit_state"] }) {
  const variant: "danger" | "warn" | "success" | "ghost" =
    state === "open" ? "danger" : state === "half_open" ? "warn" : state === "closed" ? "success" : "ghost"
  return <Tag variant={variant}>{state}</Tag>
}
