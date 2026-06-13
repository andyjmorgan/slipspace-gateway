// Keyset-pager machinery for the MessageEntry browser surfaces: the shared
// fetch/paging hook the messages page and the session lifecycle page's
// slice-bound table both drive, plus the debounce helper their filter inputs
// use. Presentation lives in telemetry/components/messages-table.tsx.

import { useEffect, useMemo, useRef, useState } from "react"
import { UnauthorizedError } from "@/lib/api"
import { fetchMessagesPage, type MessageEntry, type MessageFilters } from "@/lib/messages"

// MESSAGE_PAGE_SIZES the operator can page in. The default keeps the table
// snappy while covering most browsing in one screen; the session view
// defaults smaller (SESSION_MESSAGES_PAGE_SIZE) since it shares the page
// with the timeline.
export const MESSAGE_PAGE_SIZES = [20, 50, 100, 200] as const
export const MESSAGE_DEFAULT_PAGE_SIZE = 50
export const SESSION_MESSAGES_PAGE_SIZE = 20

// useDebounced delays propagating a fast-changing value (id search boxes, the
// timeline brush) so each keystroke / drag frame doesn't fire a query.
export function useDebounced<T>(value: T, ms: number): T {
  const [debounced, setDebounced] = useState(value)
  useEffect(() => {
    const id = setTimeout(() => setDebounced(value), ms)
    return () => clearTimeout(id)
  }, [value, ms])
  return debounced
}

// MessagesPager is the keyset-paged fetch state useMessagesPager returns: the
// current page plus the navigation handles the pager bar renders.
export type MessagesPager = {
  entries: MessageEntry[]
  status: "loading" | "ok" | "error"
  err: string
  pageIndex: number
  hasNext: boolean
  // total is the full match count across all pages (page-independent), for the
  // pager's "X–Y of N" readout; 0 until the first page settles.
  total: number
  onNext: () => void
  onPrev: () => void
}

// pagerNav is the keyset navigation state: the cursor that fetches each page
// (index 0 = "" = page one) plus the page the view is on, tagged with the
// filter identity it belongs to so a filter change derives a fresh page-one
// nav during render — no reset effect, no cascading re-render.
type pagerNav = { key: string; pageIndex: number; cursors: string[] }

// pagerResult is one settled fetch, tagged with the request identity it
// answered; a stale tag (filters/page moved on) reads as still-loading.
type pagerResult = { key: string; entries: MessageEntry[]; nextCursor: string; total: number; err: string }

/**
 * useMessagesPager drives a filtered, keyset-paged walk of /api/v1/messages.
 * `filters` are the pure predicates (their identity drives refetch + a page
 * reset); `resolveWindow` (optional) computes the impure relative time bounds
 * at fetch time — Date.now() must not run during render — and `windowKey`
 * invalidates paging when its inputs change. `enabled: false` parks the hook
 * without fetching (the caller is serving synthetic data). A live 401 routes
 * through `onUnauthorized`.
 */
export function useMessagesPager(opts: {
  filters: MessageFilters
  limit: number
  reloadNonce?: number
  enabled?: boolean
  resolveWindow?: () => { from?: string; to?: string }
  windowKey?: string
  // sort selects the server-side ordering column + direction. Changing it
  // invalidates the keyset cursor stack (a cursor is only valid for the sort
  // that minted it), so it's part of the paging identity below.
  sort?: { key: string; desc: boolean }
  onUnauthorized?: () => void
}): MessagesPager {
  const { filters, limit, reloadNonce = 0, enabled = true, windowKey = "", sort } = opts
  // filtersKey is the paging identity. Both callers build MessageFilters with
  // a fixed field order, so JSON.stringify is a stable serial.
  const filtersKey = useMemo(
    () => JSON.stringify([filters, windowKey, limit, sort ?? null]),
    [filters, windowKey, limit, sort],
  )
  const [nav, setNav] = useState<pagerNav>({ key: "", pageIndex: 0, cursors: [""] })
  const cur: pagerNav = nav.key === filtersKey ? nav : { key: filtersKey, pageIndex: 0, cursors: [""] }
  const cursor = cur.cursors[cur.pageIndex] ?? ""
  const reqKey = `${filtersKey}|${cur.pageIndex}|${reloadNonce}`
  const [result, setResult] = useState<pagerResult | null>(null)

  // Refs so the fetch effect reads the latest callbacks without re-running;
  // synced in an effect (declared first, so it lands before the fetch effect
  // in the same commit).
  const resolveWindowRef = useRef(opts.resolveWindow)
  const onUnauthorizedRef = useRef(opts.onUnauthorized)
  useEffect(() => {
    resolveWindowRef.current = opts.resolveWindow
    onUnauthorizedRef.current = opts.onUnauthorized
  })

  useEffect(() => {
    if (!enabled) return
    let cancelled = false
    const win = resolveWindowRef.current?.() ?? {}
    fetchMessagesPage(
      { ...filters, ...win },
      { cursor, limit, sort: sort?.key, order: sort && !sort.desc ? "asc" : "desc" },
    )
      .then((p) => {
        if (cancelled) return
        setResult({ key: reqKey, entries: p.entries, nextCursor: p.nextCursor, total: p.total, err: "" })
      })
      .catch((e) => {
        if (cancelled) return
        if (e instanceof UnauthorizedError) {
          onUnauthorizedRef.current?.()
          return
        }
        setResult({ key: reqKey, entries: [], nextCursor: "", total: 0, err: e instanceof Error ? e.message : String(e) })
      })
    return () => {
      cancelled = true
    }
  }, [filters, cursor, limit, sort, reqKey, enabled])

  // A result tagged with a superseded request key reads as loading — the
  // matching fetch is already in flight.
  const fresh = result?.key === reqKey ? result : null
  const status: MessagesPager["status"] = !fresh ? "loading" : fresh.err ? "error" : "ok"

  const onNext = () => {
    if (!fresh?.nextCursor) return
    const ci = cur.pageIndex + 1
    const cursors = cur.cursors.slice(0, ci)
    cursors[ci] = fresh.nextCursor
    setNav({ key: filtersKey, pageIndex: ci, cursors })
  }
  const onPrev = () => {
    if (cur.pageIndex > 0) setNav({ ...cur, pageIndex: cur.pageIndex - 1 })
  }
  return {
    entries: fresh?.entries ?? [],
    status,
    err: fresh?.err ?? "",
    pageIndex: cur.pageIndex,
    hasNext: !!fresh?.nextCursor,
    total: fresh?.total ?? 0,
    onNext,
    onPrev,
  }
}
