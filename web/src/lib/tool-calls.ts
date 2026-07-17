// Tool-Call audit client. Mirrors contracts/admin/toolcalls.go on the Go side
// and drives the Tool Calls page in the telemetry SPA: a searchable index of
// individual tool invocations (name + arguments + eventual result), joined
// across the call span and the result span by tool_call_id.

import { useEffect, useMemo, useRef, useState } from "react"
import { apiFetch, UnauthorizedError } from "@/lib/api"

// ToolCallEntry is generated from contracts/admin/toolcalls.go. Do NOT hand-edit
// — run `make generate`. Source of truth: web/src/lib/generated/admin.
import type { ToolCallEntry } from "./generated/admin"

export type { ToolCallEntry }

// ToolCallStatus narrows the wire status string to the two states the index
// produces: "resolved" once the result half is in, "pending" otherwise.
export type ToolCallStatus = "" | "pending" | "resolved"

// ToolCallFilters is the audit filter set. Empty fields are omitted from the
// query string (no predicate). The categorical dimensions (names/providers/
// configurations/protocols/models) send one repeated param per value and OR
// within the dimension; `sessionId` is an exact match; `status` narrows to
// pending/resolved.
export type ToolCallFilters = {
  names?: string[]
  sessionId?: string
  status?: ToolCallStatus
  providers?: string[]
  configurations?: string[]
  protocols?: string[]
  models?: string[]
  from?: string
  to?: string
}

// ToolCallsPage is one keyset page of the index. nextCursor is empty on the last
// page. The endpoint reports no total (keyset-only), so the pager gates Next on
// nextCursor and Prev on the cursor stack.
export type ToolCallsPage = {
  entries: ToolCallEntry[]
  nextCursor: string
}

type ToolCallsPageWire = {
  tool_calls: ToolCallEntry[]
  next_cursor: string
}

/**
 * Fetches one filtered, keyset-paged page of tool calls (newest-first by
 * observed_at). Pass the previous page's nextCursor to advance; omit it for
 * page one. Empty filter values are dropped from the query string.
 */
export async function fetchToolCallsPage(
  filters: ToolCallFilters,
  opts: { cursor?: string; limit?: number } = {},
): Promise<ToolCallsPage> {
  const p = new URLSearchParams()
  const put = (k: string, v?: string) => {
    if (v) p.set(k, v)
  }
  const putAll = (k: string, vs?: string[]) => {
    for (const v of vs ?? []) if (v) p.append(k, v)
  }
  putAll("name", filters.names)
  put("session_id", filters.sessionId)
  put("status", filters.status)
  putAll("provider", filters.providers)
  putAll("configuration", filters.configurations)
  putAll("protocol", filters.protocols)
  putAll("model", filters.models)
  put("from", filters.from)
  put("to", filters.to)
  put("cursor", opts.cursor)
  if (opts.limit && opts.limit > 0) p.set("limit", String(opts.limit))
  const r = await apiFetch<ToolCallsPageWire>(`/api/v1/tool-calls?${p.toString()}`)
  return { entries: r.tool_calls ?? [], nextCursor: r.next_cursor ?? "" }
}

/** Fetches one tool call by id, or null when the id is unknown (404). */
export async function fetchToolCall(id: string): Promise<ToolCallEntry | null> {
  try {
    return await apiFetch<ToolCallEntry>(`/api/v1/tool-calls/${encodeURIComponent(id)}`)
  } catch (err) {
    if ((err as { status?: number }).status === 404) return null
    throw err
  }
}

// ToolNamesResponse is the distinct tool-name facet for the page's dropdown.
type ToolNamesResponse = { tool_names: string[] }

/** Fetches the distinct tool names seen (for the tool dropdown). */
export async function fetchToolNames(): Promise<string[]> {
  const r = await apiFetch<ToolNamesResponse>(`/api/v1/tool-calls/facets`)
  return r.tool_names ?? []
}

// --- keyset pager (mirrors useMessagesPager; no total since the endpoint is
// keyset-only) ---

export const TOOL_CALL_PAGE_SIZES = [20, 50, 100, 200] as const
export const TOOL_CALL_DEFAULT_PAGE_SIZE = 50

export type ToolCallsPager = {
  entries: ToolCallEntry[]
  status: "loading" | "ok" | "error"
  err: string
  pageIndex: number
  hasNext: boolean
  onNext: () => void
  onPrev: () => void
}

// pagerNav is the keyset navigation state tagged with the filter identity it
// belongs to, so a filter change derives a fresh page-one nav during render —
// no reset effect.
type pagerNav = { key: string; pageIndex: number; cursors: string[] }
type pagerResult = { key: string; entries: ToolCallEntry[]; nextCursor: string; err: string }

/**
 * useToolCallsPager drives a filtered, keyset-paged walk of /api/v1/tool-calls.
 * `filters` are the pure predicates (their identity drives refetch + a page
 * reset); `resolveWindow` (optional) computes the impure relative time bound at
 * fetch time — Date.now() must not run during render — invalidated by
 * `windowKey`. A live 401 routes through `onUnauthorized`.
 */
export function useToolCallsPager(opts: {
  filters: ToolCallFilters
  limit: number
  reloadNonce?: number
  resolveWindow?: () => { from?: string; to?: string }
  windowKey?: string
  onUnauthorized?: () => void
}): ToolCallsPager {
  const { filters, limit, reloadNonce = 0, windowKey = "" } = opts
  const filtersKey = useMemo(
    () => JSON.stringify([filters, windowKey, limit]),
    [filters, windowKey, limit],
  )
  const [nav, setNav] = useState<pagerNav>({ key: "", pageIndex: 0, cursors: [""] })
  const cur: pagerNav = nav.key === filtersKey ? nav : { key: filtersKey, pageIndex: 0, cursors: [""] }
  const cursor = cur.cursors[cur.pageIndex] ?? ""
  const reqKey = `${filtersKey}|${cur.pageIndex}|${reloadNonce}`
  const [result, setResult] = useState<pagerResult | null>(null)

  const resolveWindowRef = useRef(opts.resolveWindow)
  const onUnauthorizedRef = useRef(opts.onUnauthorized)
  useEffect(() => {
    resolveWindowRef.current = opts.resolveWindow
    onUnauthorizedRef.current = opts.onUnauthorized
  })

  useEffect(() => {
    let cancelled = false
    const win = resolveWindowRef.current?.() ?? {}
    fetchToolCallsPage({ ...filters, ...win }, { cursor, limit })
      .then((p) => {
        if (cancelled) return
        setResult({ key: reqKey, entries: p.entries, nextCursor: p.nextCursor, err: "" })
      })
      .catch((e) => {
        if (cancelled) return
        if (e instanceof UnauthorizedError) {
          onUnauthorizedRef.current?.()
          return
        }
        setResult({ key: reqKey, entries: [], nextCursor: "", err: e instanceof Error ? e.message : String(e) })
      })
    return () => {
      cancelled = true
    }
  }, [filters, cursor, limit, reqKey])

  const fresh = result?.key === reqKey ? result : null
  const status: ToolCallsPager["status"] = !fresh ? "loading" : fresh.err ? "error" : "ok"

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
    onNext,
    onPrev,
  }
}
