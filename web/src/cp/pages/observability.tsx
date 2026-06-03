import { useCallback, useEffect, useRef, useState } from "react"
import { useNavigate } from "react-router"
import { PanelCard, PanelHead, TableScroll } from "@/components/atoms/card"
import { StatusPill } from "@/components/atoms/status-pill"
import { PageHeader, LoadingPanel, ErrorPanel, EmptyPanel } from "@/components/atoms/page-states"
import { fmt } from "@/lib/fmt"
import { apiFetch, apiErrorText, UnauthorizedError } from "../lib/api"
import { EventFilterBar } from "../components/event-filter-bar"
import { DEFAULT_FILTERS, buildQuery, type EventFilters } from "../lib/event-filters"

interface EventRow {
  correlation_id: string
  gateway_id: string
  configuration: string
  backend: string
  model: string
  protocol: string
  status_code: number
  latency_ms: number
  tokens_in: number
  tokens_out: number
  observed_at: string
}

// EventsEnvelope is the paginated events response: a newest-first page of
// rows plus an opaque cursor. An empty next_cursor means the last page.
interface EventsEnvelope {
  events: EventRow[]
  next_cursor: string
}

interface PageState {
  rows: EventRow[]
  cursor: string
  error: string | null
  loading: boolean
}

const INITIAL: PageState = { rows: [], cursor: "", error: null, loading: true }

// ObservabilityPage is the fleet's historical request browser: a time range
// + dimension filters drive a paginated query over the slim per-request
// telemetry the gateways push over OTLP. Rows drill into the per-request
// detail view (captured bodies + receipt verification). The cursor pages
// older newest-first; an empty next_cursor ends the list.
export function ObservabilityPage() {
  const nav = useNavigate()
  const [filters, setFilters] = useState<EventFilters>(DEFAULT_FILTERS)
  const [state, setState] = useState<PageState>(INITIAL)

  // applied is the filter set the current results were fetched with. The
  // filter bar mutates `filters` freely; the query only re-runs on Apply,
  // which copies `filters` here and triggers the initial-page effect.
  const [applied, setApplied] = useState<EventFilters>(DEFAULT_FILTERS)

  // apply swaps in a new filter set and clears the current page to its
  // loading state — both in the handler so the load-on-`applied` effect
  // never has to setState synchronously.
  const apply = useCallback((next: EventFilters) => {
    setState({ rows: [], cursor: "", error: null, loading: true })
    setApplied(next)
  }, [])

  // reqSeq guards against out-of-order responses: a stale page (older Apply,
  // or a Load-more that lost a race with a re-Apply) is discarded.
  const reqSeq = useRef(0)

  useEffect(() => {
    const seq = ++reqSeq.current
    let cancelled = false
    apiFetch<EventsEnvelope>(`/api/v1/observability/events?${buildQuery(applied, "")}`)
      .then((env) => {
        if (cancelled || seq !== reqSeq.current) return
        setState({ rows: env.events ?? [], cursor: env.next_cursor ?? "", error: null, loading: false })
      })
      .catch((e) => {
        if (cancelled || seq !== reqSeq.current) return
        if (e instanceof UnauthorizedError) return nav("/login", { replace: true })
        setState({ rows: [], cursor: "", error: apiErrorText(e), loading: false })
      })
    return () => {
      cancelled = true
    }
  }, [applied, nav])

  const loadMore = useCallback(() => {
    if (!state.cursor || state.loading) return
    const seq = reqSeq.current
    setState((s) => ({ ...s, loading: true, error: null }))
    apiFetch<EventsEnvelope>(`/api/v1/observability/events?${buildQuery(applied, state.cursor)}`)
      .then((env) => {
        if (seq !== reqSeq.current) return
        setState((s) => ({
          rows: [...s.rows, ...(env.events ?? [])],
          cursor: env.next_cursor ?? "",
          error: null,
          loading: false,
        }))
      })
      .catch((e) => {
        if (seq !== reqSeq.current) return
        if (e instanceof UnauthorizedError) return nav("/login", { replace: true })
        setState((s) => ({ ...s, loading: false, error: apiErrorText(e) }))
      })
  }, [applied, state.cursor, state.loading, nav])

  const isInitialLoad = state.loading && state.rows.length === 0
  const isEmpty = !state.loading && !state.error && state.rows.length === 0

  return (
    <div>
      <PageHeader
        title="Observability"
        sub="Historical request events across the fleet — filter by time range, configuration, gateway, model, backend, protocol, and status. Pushed by gateways over OTLP."
      />

      <EventFilterBar
        filters={filters}
        onChange={setFilters}
        onApply={() => apply(filters)}
        onReset={() => {
          setFilters(DEFAULT_FILTERS)
          apply(DEFAULT_FILTERS)
        }}
        busy={state.loading}
      />

      {state.error && <ErrorPanel message={state.error} />}
      {!state.error && isInitialLoad && <LoadingPanel />}
      {isEmpty && (
        <EmptyPanel message="No request events match these filters in the selected range. Widen the range or clear filters." />
      )}

      {!state.error && state.rows.length > 0 && (
        <PanelCard>
          <PanelHead
            title="Events"
            sub={`${state.rows.length} shown${state.cursor ? " · more available" : ""}`}
          />
          <TableScroll>
            <thead>
              <tr className="text-[11px] uppercase tracking-[0.07em] text-[color:var(--text-3)]">
                <th className="text-left font-medium px-4 py-2">When</th>
                <th className="text-left font-medium px-4 py-2">Gateway</th>
                <th className="text-left font-medium px-4 py-2">Configuration</th>
                <th className="text-left font-medium px-4 py-2">Model</th>
                <th className="text-left font-medium px-4 py-2">Backend</th>
                <th className="text-left font-medium px-4 py-2">Status</th>
                <th className="text-right font-medium px-4 py-2">Tokens</th>
                <th className="text-right font-medium px-4 py-2">Latency</th>
              </tr>
            </thead>
            <tbody>
              {state.rows.map((e) => (
                <tr
                  key={e.correlation_id}
                  onClick={() => nav(`/observability/${encodeURIComponent(e.correlation_id)}`)}
                  className="border-t border-[color:var(--border)] hover:bg-[color:var(--hover)] cursor-pointer"
                >
                  <td className="px-4 py-2.5 text-[12px] text-[color:var(--text-3)]" title={fmt.fullTime(e.observed_at)}>
                    {fmt.ago(e.observed_at)}
                  </td>
                  <td className="px-4 py-2.5 mono text-[12px]">{e.gateway_id}</td>
                  <td className="px-4 py-2.5 mono text-[12px] text-[color:var(--text-2)]">{e.configuration || "—"}</td>
                  <td className="px-4 py-2.5 mono text-[12px] text-[color:var(--text-2)]">{e.model || "—"}</td>
                  <td className="px-4 py-2.5 mono text-[12px] text-[color:var(--text-3)]">{e.backend || "—"}</td>
                  <td className="px-4 py-2.5"><StatusPill code={e.status_code} /></td>
                  <td className="px-4 py-2.5 text-right mono text-[12px] text-[color:var(--text-3)]">
                    {fmt.compact(e.tokens_in)} / {fmt.compact(e.tokens_out)}
                  </td>
                  <td className="px-4 py-2.5 text-right mono text-[12px] text-[color:var(--text-3)]">{fmt.ms(e.latency_ms)}</td>
                </tr>
              ))}
            </tbody>
          </TableScroll>
          <div className="border-t border-[color:var(--border)] px-4 py-3 flex items-center justify-center">
            {state.cursor ? (
              <button
                type="button"
                onClick={loadMore}
                disabled={state.loading}
                className="rounded-[5px] border border-[color:var(--border)] bg-[color:var(--bg-2)] px-4 py-1.5 text-[12px] font-medium text-[color:var(--text)] hover:bg-[color:var(--hover)] disabled:opacity-50"
              >
                {state.loading ? "Loading…" : "Load more"}
              </button>
            ) : (
              <span className="text-[11.5px] text-[color:var(--text-3)]">End of results</span>
            )}
          </div>
        </PanelCard>
      )}
    </div>
  )
}
