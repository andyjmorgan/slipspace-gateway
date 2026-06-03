import { usePolicies, type PolicySummary } from "@/lib/config-api"
import { GroupListTable } from "@/components/config-views/group-views"
import type { GroupListRow } from "@/components/config-views/group-views-model"
import {
  PageHeader,
  NewButton,
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
        title="Groups"
        sub="Failover and load-balance backend groups with live per-pod circuit-breaker state. Edit the policy + targets; live breaker state shown below."
        action={
          <NewButton to="/groups/new" label="New group" />
        }
      />
      {state.status === "loading" && <LoadingPanel />}
      {state.status === "error" && <ErrorPanel message={state.message} />}
      {state.status === "ok" && state.data.policies.length === 0 && (
        <EmptyPanel message="No groups configured." />
      )}
      {state.status === "ok" && state.data.policies.length > 0 && (
        <div className="space-y-3">
          <GroupListTable rows={state.data.policies.map(groupListRowFromPolicy)} />
          <div className="text-[11px] text-[color:var(--text-4)] mono pl-2">
            pod {state.data.pod}
          </div>
        </div>
      )}
    </div>
  )
}

// groupListRowFromPolicy adapts the gateway's live /policies summary into the
// shared list-row shape — the live per-target circuit state carries through.
function groupListRowFromPolicy(p: PolicySummary): GroupListRow {
  return {
    name: p.name,
    mode: p.mode,
    strict_weights: p.strict_weights,
    circuit_breaker_enabled: p.circuit_breaker_enabled,
    failure_status_codes: p.failure_status_codes,
    targets: p.targets.map((t) => ({
      name: t.name,
      backend: t.provider,
      order: t.order,
      weight: t.weight,
      circuit_state: t.circuit_state,
    })),
  }
}
