import { useCallback, useEffect, useMemo, useRef, useState } from "react"
import { useNavigate } from "react-router"
import { Layers } from "lucide-react"
import { Button } from "@/components/ui/button"
import { PageHeader, LoadingPanel, ErrorPanel, EmptyPanel } from "@/components/atoms/page-states"
import { MessagesTable, MessageInspectorModal } from "@/components/observability/messages-view"
import type { MessageEntry } from "@/lib/observability-types"
import { apiErrorText, UnauthorizedError } from "../lib/api"
import { EventFilterBar } from "../components/event-filter-bar"
import { DEFAULT_FILTERS, PAGE_SIZE, type EventFilters } from "../lib/event-filters"
import { fetchCpRecentMessages, fetchCpMessageBody, type CpRecentMessagesQuery } from "../lib/observability"

const RANGE_MS: Record<"1h" | "24h" | "7d", number> = {
  "1h": 60 * 60 * 1000,
  "24h": 24 * 60 * 60 * 1000,
  "7d": 7 * 24 * 60 * 60 * 1000,
}

// filtersToQuery maps the observability filter bar's state onto the CP
// recent-messages query. Preset ranges resolve to a rolling from=now-window;
// custom ranges pass the datetime-local inputs through as ISO instants.
// limit grows as the operator pages so the table accumulates rather than
// scrolling a fixed window.
function filtersToQuery(f: EventFilters, limit: number): CpRecentMessagesQuery {
  const q: CpRecentMessagesQuery = { limit }
  if (f.range === "custom") {
    if (f.from) q.from = new Date(f.from).toISOString()
    if (f.to) q.to = new Date(f.to).toISOString()
  } else {
    q.from = new Date(Date.now() - RANGE_MS[f.range]).toISOString()
  }
  if (f.configuration.trim()) q.configuration = f.configuration.trim()
  if (f.gateway.trim()) q.gateway = f.gateway.trim()
  if (f.model.trim()) q.model = f.model.trim()
  if (f.backend.trim()) q.backend = f.backend.trim()
  if (f.protocol.trim()) q.protocol = f.protocol.trim()
  if (f.statusClass !== "all") q.status_class = f.statusClass
  return q
}

interface PageState {
  entries: MessageEntry[]
  limit: number
  // hitLimit is true when the last page came back full — there may be more
  // history past the current limit, so the "Load more" affordance shows.
  hitLimit: boolean
  error: string | null
  loading: boolean
}

const INITIAL: PageState = { entries: [], limit: PAGE_SIZE, hitLimit: false, error: null, loading: true }

// ObservabilityPage is the fleet's historical request inspector: the
// filter bar (time range + dimension filters) drives a query over the slim
// per-request telemetry the gateways push, and the rich shared inspector
// (table + per-request detail modal with captured bodies) renders the
// result. The CP has more history than the gateway's in-memory ring, so the
// filter bar + a limit-grow "Load more" replace the gateway's live-tail
// controls. `attempts` is unset on CP entries today; the inspector degrades
// gracefully.
export function ObservabilityPage() {
  const nav = useNavigate()
  const [filters, setFilters] = useState<EventFilters>(DEFAULT_FILTERS)
  const [applied, setApplied] = useState<EventFilters>(DEFAULT_FILTERS)
  const [state, setState] = useState<PageState>(INITIAL)
  const [selectedId, setSelectedId] = useState<string | null>(null)
  const [grouped, setGrouped] = useState(false)
  const [collapsed, setCollapsed] = useState<Set<string>>(() => new Set())

  // apply swaps in a new filter set, resets the limit, and clears to the
  // loading state — all in the handler so the load effect never has to
  // setState synchronously.
  const apply = useCallback((next: EventFilters) => {
    setState({ entries: [], limit: PAGE_SIZE, hitLimit: false, error: null, loading: true })
    setSelectedId(null)
    setApplied(next)
  }, [])

  // reqSeq guards against out-of-order responses: a stale page (older Apply
  // or a load-more that lost a race with a re-Apply) is discarded.
  const reqSeq = useRef(0)

  useEffect(() => {
    const seq = ++reqSeq.current
    let cancelled = false
    fetchCpRecentMessages(filtersToQuery(applied, state.limit))
      .then((resp) => {
        if (cancelled || seq !== reqSeq.current) return
        const entries = resp.entries ?? []
        setState((s) => ({
          ...s,
          entries,
          hitLimit: entries.length >= s.limit,
          error: null,
          loading: false,
        }))
      })
      .catch((e) => {
        if (cancelled || seq !== reqSeq.current) return
        if (e instanceof UnauthorizedError) return nav("/login", { replace: true })
        setState((s) => ({ ...s, entries: [], hitLimit: false, error: apiErrorText(e), loading: false }))
      })
    return () => {
      cancelled = true
    }
  }, [applied, state.limit, nav])

  const loadMore = useCallback(() => {
    setState((s) => (s.loading ? s : { ...s, limit: s.limit + PAGE_SIZE, loading: true, error: null }))
  }, [])

  // The recent endpoint returns oldest-first (like the gateway ring); the
  // inspector wants newest-first, and the modal navigation is keyed off
  // this order.
  const ordered = useMemo(() => state.entries.slice().reverse(), [state.entries])
  const selected = useMemo(
    () => state.entries.find((e) => e.event_id === selectedId) ?? null,
    [state.entries, selectedId],
  )
  const toggleBundle = useCallback((key: string) => {
    setCollapsed((prev) => {
      const next = new Set(prev)
      if (next.has(key)) next.delete(key)
      else next.add(key)
      return next
    })
  }, [])
  const onSelect = useCallback((eid: string) => setSelectedId((cur) => (cur === eid ? null : eid)), [])

  const isInitialLoad = state.loading && state.entries.length === 0
  const isEmpty = !state.loading && !state.error && state.entries.length === 0

  return (
    <div className="flex flex-col gap-3.5">
      <PageHeader
        title="Observability"
        sub="Historical request events across the fleet — filter by time range, configuration, gateway, model, backend, protocol, and status. Click a row for the captured request/response bodies."
        action={
          <Button variant="ghost" size="sm" onClick={() => setGrouped((g) => !g)} aria-label="Group by session" aria-pressed={grouped}>
            <Layers />
            <span className="hidden sm:inline">{grouped ? "Ungroup" : "Group by session"}</span>
          </Button>
        }
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

      {!state.error && state.entries.length > 0 && (
        <>
          <div className="flex items-center gap-3 text-[11.5px] text-[color:var(--text-3)]">
            <span className="mono">{state.entries.length} shown</span>
            {state.hitLimit && (
              <>
                <span className="text-[color:var(--text-4)]">·</span>
                <span>more history available</span>
              </>
            )}
          </div>
          <MessagesTable
            ordered={ordered}
            grouped={grouped}
            collapsed={collapsed}
            onToggleBundle={toggleBundle}
            selectedId={selectedId}
            onSelect={onSelect}
            emptyLabel="No events match these filters."
          />
          <div className="flex items-center justify-center">
            {state.hitLimit ? (
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
        </>
      )}

      {selected && (
        <MessageInspectorModal
          entries={ordered}
          selectedId={selected.event_id}
          onSelect={setSelectedId}
          onClose={() => setSelectedId(null)}
          fetchBody={fetchCpMessageBody}
        />
      )}
    </div>
  )
}
