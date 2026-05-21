// Dashboard data hooks — fetches /api/v1/dashboard/{summary,timeseries},
// mirrors the Go contracts in contracts/admin/dashboard.go.

import { useCallback, useEffect, useRef, useState } from "react"
import { apiFetch, UnauthorizedError } from "@/lib/api"

export type DashboardProviderRow = {
  provider: string
  requests: number
  p95_latency_ms: number
  error_rate: number
}

export type DashboardModelRow = {
  model: string
  provider: string
  requests: number
  tokens_in: number
  tokens_out: number
}

export type DashboardEndpointRow = {
  provider: string
  endpoint: string
  requests: number
  p95_latency_ms: number
  error_rate: number
}

export type DashboardConfigurationRow = {
  configuration: string
  requests: number
  p95_latency_ms: number
  error_rate: number
}

export type DashboardRuleFiredRow = {
  rule_name: string
  fire_count: number
  used_by_configurations: string[]
}

export type DashboardTagFiredRow = {
  tag: string
  apply_count: number
  used_by_configurations: string[]
}

export type DashboardProviderHealth = {
  provider: string
  healthy: boolean
  error_rate_5m: number
  requests_5m: number
}

export type DashboardSummary = {
  window: string
  generated_at: string
  gateway_started_at: string
  totals: {
    requests: number
    requests_success: number
    requests_errored: number
    tokens_in: number
    tokens_out: number
    tokens_cached: number
    tokens_cache_creation: number
  }
  rates: {
    requests_per_second: number
    error_rate: number
  }
  latency_ms: { p50: number; p95: number; p99: number }
  by_provider: DashboardProviderRow[]
  by_endpoint: DashboardEndpointRow[]
  by_configuration: DashboardConfigurationRow[]
  by_model: DashboardModelRow[]
  rules_fired: DashboardRuleFiredRow[]
  tags_fired: DashboardTagFiredRow[]
  provider_health: DashboardProviderHealth[]
}

export type DashboardPoint = {
  timestamp: string
  value: number
}

export type DashboardSeries = {
  name: string
  unit?: string
  labels?: Record<string, string>
  points: DashboardPoint[]
}

export type DashboardTimeseries = {
  series: DashboardSeries[]
}

export type FetchState<T> =
  | { status: "loading" }
  | { status: "ok"; data: T }
  | { status: "unauthorized" }
  | { status: "error"; message: string }

/**
 * Default background poll interval for dashboard data, in ms. The
 * gateway's snapshotter samples at 5min in production / 2s in
 * compose dev — 30s on the client side is a sensible refresh rate
 * either way.
 */
export const DEFAULT_POLL_MS = 30_000

export type FetchHandle<T> = {
  state: FetchState<T>
  /** Trigger an immediate refetch. Errors during refetch don't blank
   * existing data — the previous "ok" state is preserved. */
  refetch: () => void
}

function useFetch<T>(path: string, pollMs = DEFAULT_POLL_MS): FetchHandle<T> {
  const [state, setState] = useState<FetchState<T>>({ status: "loading" })
  // Track the latest state in a ref so the poll closure can see the
  // current state without re-firing the effect on every state change.
  const stateRef = useRef<FetchState<T>>(state)
  stateRef.current = state

  // A nonce we bump to force a refetch outside the polling cadence.
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
          // On background refresh, don't blank existing data on
          // transient errors — flicker is worse than slightly-stale.
          // Only surface the error if we have nothing else to show.
          if (isFirst || stateRef.current.status !== "ok") {
            const msg = e instanceof Error ? e.message : String(e)
            setState({ status: "error", message: msg })
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

/** The set of windows the dashboard server accepts on ?window=. */
export type DashboardWindow = "1h" | "24h"

export function useDashboardSummary(window: DashboardWindow, pollMs?: number): FetchHandle<DashboardSummary> {
  return useFetch<DashboardSummary>(`/api/v1/dashboard/summary?window=${window}`, pollMs)
}

export function useDashboardTimeseries(name: string, window: DashboardWindow, pollMs?: number): FetchHandle<DashboardTimeseries> {
  return useFetch<DashboardTimeseries>(
    `/api/v1/dashboard/timeseries?series=${encodeURIComponent(name)}&window=${window}`,
    pollMs,
  )
}
