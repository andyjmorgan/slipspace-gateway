// Control-plane observability data hooks. The CP serves the dashboard +
// message-inspector surfaces from its DB-backed observability API, which
// returns the same JSON shapes (contracts/admin, byte-for-byte) the
// gateway admin API does — so the presentational components under
// components/observability/ are shared, and only these fetchers differ
// (CP paths under /api/v1/observability/* vs. the gateway's
// /admin/api/v1/* base).

import { useCallback, useEffect, useRef, useState } from "react"
import type {
  DashboardSummary,
  DashboardTimeseries,
  DashboardWindow,
  MessageBodyDetail,
  MessagesRecentResponse,
} from "@/lib/observability-types"
import { apiFetch, apiErrorText, UnauthorizedError } from "./api"

export type CpFetchState<T> =
  | { status: "loading" }
  | { status: "ok"; data: T }
  | { status: "unauthorized" }
  | { status: "error"; message: string }

export type CpFetchHandle<T> = {
  state: CpFetchState<T>
  // refetch triggers an immediate refetch. On a refetch error the prior
  // "ok" data is preserved — flicker is worse than slightly-stale.
  refetch: () => void
}

// DEFAULT_POLL_MS matches the gateway dashboard's client refresh cadence.
// The CP aggregates over a wider window so this is a soft refresh, not a
// live tail.
export const DEFAULT_POLL_MS = 30_000

function useCpFetch<T>(path: string, pollMs = DEFAULT_POLL_MS): CpFetchHandle<T> {
  const [state, setState] = useState<CpFetchState<T>>({ status: "loading" })
  const stateRef = useRef<CpFetchState<T>>(state)
  stateRef.current = state

  const [nonce, setNonce] = useState(0)
  const refetch = useCallback(() => setNonce((n) => n + 1), [])

  useEffect(() => {
    let cancelled = false

    const run = (isFirst: boolean) => {
      apiFetch<T>(path)
        .then((data) => {
          if (!cancelled) setState({ status: "ok", data })
        })
        .catch((e) => {
          if (cancelled) return
          if (e instanceof UnauthorizedError) {
            setState({ status: "unauthorized" })
            return
          }
          if (isFirst || stateRef.current.status !== "ok") {
            setState({ status: "error", message: apiErrorText(e) })
          }
        })
    }

    run(true)

    if (pollMs > 0) {
      const id = setInterval(() => run(false), pollMs)
      return () => {
        cancelled = true
        clearInterval(id)
      }
    }
    return () => {
      cancelled = true
    }
  }, [path, pollMs, nonce])

  return { state, refetch }
}

export function useCpDashboardSummary(window: DashboardWindow, pollMs?: number): CpFetchHandle<DashboardSummary> {
  return useCpFetch<DashboardSummary>(`/api/v1/observability/dashboard/summary?window=${window}`, pollMs)
}

export function useCpDashboardTimeseries(
  name: string,
  window: DashboardWindow,
  pollMs?: number,
): CpFetchHandle<DashboardTimeseries> {
  return useCpFetch<DashboardTimeseries>(
    `/api/v1/observability/dashboard/timeseries?series=${encodeURIComponent(name)}&window=${window}`,
    pollMs,
  )
}

// CpRecentMessagesQuery narrows the recent-messages list. limit caps the
// page; the dimension filters map to the same query params the events
// browser uses, so the CP message inspector can reuse the filter bar.
export type CpRecentMessagesQuery = {
  limit?: number
  from?: string
  to?: string
  configuration?: string
  gateway?: string
  model?: string
  backend?: string
  protocol?: string
  status_class?: string
}

function recentMessagesPath(q: CpRecentMessagesQuery): string {
  const params = new URLSearchParams()
  if (q.limit && q.limit > 0) params.set("limit", String(q.limit))
  if (q.from) params.set("from", q.from)
  if (q.to) params.set("to", q.to)
  if (q.configuration) params.set("configuration", q.configuration)
  if (q.gateway) params.set("gateway", q.gateway)
  if (q.model) params.set("model", q.model)
  if (q.backend) params.set("backend", q.backend)
  if (q.protocol) params.set("protocol", q.protocol)
  if (q.status_class) params.set("status_class", q.status_class)
  const qs = params.toString()
  return `/api/v1/observability/messages/recent${qs ? `?${qs}` : ""}`
}

/** Fetches a page of recent fleet messages (newest-first). */
export async function fetchCpRecentMessages(q: CpRecentMessagesQuery = {}): Promise<MessagesRecentResponse> {
  return apiFetch<MessagesRecentResponse>(recentMessagesPath(q))
}

/**
 * Fetches the captured request + response bodies for a single message.
 * Returns null when the body store is disabled (503) or the message has
 * no captured body (404) — the inspector renders a "not available" note
 * rather than an error in that case.
 */
export async function fetchCpMessageBody(eventId: string): Promise<MessageBodyDetail | null> {
  try {
    return await apiFetch<MessageBodyDetail>(`/api/v1/observability/messages/${encodeURIComponent(eventId)}/body`)
  } catch (err) {
    const status = (err as { status?: number }).status
    if (status === 404 || status === 503) return null
    throw err
  }
}
