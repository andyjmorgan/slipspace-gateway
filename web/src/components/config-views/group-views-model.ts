// View shapes + body→view mappers for the shared group (resilience) list view.
// Kept out of group-views.tsx so that module only exports components
// (react-refresh/only-export-components) — same split as backend-views-model.ts
// and the *-form-model.ts files. The gateway feeds these rows from its live
// /policies read API (per-target circuit state + pod), so it maps a
// PolicySummary; the control-plane console maps a staged entity's GroupContract
// body, which carries no live state — the circuit-state column renders empty.

import type { GroupContract } from "@/components/config-editors/group-form-model"

// GroupTargetRow is one row of a group's targets table. order/circuit_state are
// gateway-live-only — the CP leaves them undefined and the view shows a dash.
export interface GroupTargetRow {
  name: string
  backend?: string
  order?: number
  weight?: number
  circuit_state?: string
}

// GroupListRow is one resilience group as the shared card renders it: the
// header chips (mode, strict_weights, circuit breaker, retry codes) plus the
// per-target table.
export interface GroupListRow {
  name: string
  mode: string
  strict_weights?: boolean
  circuit_breaker_enabled?: boolean
  failure_status_codes?: number[]
  targets: GroupTargetRow[]
}

// groupListRowFromContract derives a card row from a stored group body. The
// contract carries no live circuit state, so each target's circuit_state is
// left undefined; the target's position in the array is its failover order.
export function groupListRowFromContract(name: string, body: GroupContract | Record<string, unknown>): GroupListRow {
  const g = body as GroupContract
  return {
    name,
    mode: g.mode || "",
    strict_weights: g.strict_weights ?? false,
    circuit_breaker_enabled: g.circuit_breaker?.enabled ?? false,
    failure_status_codes: g.failure_status_codes,
    targets: (g.targets ?? []).map((t, i) => ({
      name: t.alias?.trim() ? t.alias : t.backend,
      backend: t.backend,
      order: i + 1,
      weight: t.weight,
    })),
  }
}
