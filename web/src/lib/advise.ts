// Agent-routing advisor data hooks — fetches /api/v1/advise/audit (the
// judgement log) and /api/v1/advise/audit/savings (spend attribution),
// mirroring the Go contracts in contracts/admin/advise.go.

import { apiFetch } from "@/lib/api"
import { useFetch, type DashboardWindow, type FetchHandle } from "@/lib/dashboard"

// Wire DTOs are generated from the Go contracts (contracts/admin/advise.go).
// Do NOT hand-edit — run `make generate`. Source of truth: web/src/lib/generated/.
import type {
  AdviseAuditItem,
  AdviseAuditPage,
  AdviseSavingsItem,
  AdviseSavingsResponse,
  AdviseSavingsTotals,
} from "./generated/admin"

export type { AdviseAuditItem, AdviseAuditPage, AdviseSavingsItem, AdviseSavingsResponse, AdviseSavingsTotals }

// useAdviseAudit fetches the newest page of routing judgements. Older pages
// are fetched imperatively with fetchAdviseAuditBefore and appended by the
// page — polling only ever refreshes the head of the log.
export function useAdviseAudit(pollMs?: number): FetchHandle<AdviseAuditPage> {
  return useFetch<AdviseAuditPage>("/api/v1/advise/audit", pollMs)
}

// fetchAdviseAuditBefore fetches the page older than the given received_at
// cursor (the last row of the previous page).
export function fetchAdviseAuditBefore(before: string): Promise<AdviseAuditPage> {
  return apiFetch<AdviseAuditPage>(`/api/v1/advise/audit?before=${encodeURIComponent(before)}`)
}

// useAdviseSavings fetches the savings attribution over the console-standard
// window token — the server resolves the window to a time bound, keeping
// clock reads out of render.
export function useAdviseSavings(window: DashboardWindow, pollMs?: number): FetchHandle<AdviseSavingsResponse> {
  return useFetch<AdviseSavingsResponse>(`/api/v1/advise/audit/savings?window=${window}`, pollMs)
}
