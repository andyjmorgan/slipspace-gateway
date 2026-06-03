// Filter state + query construction for the observability event browser.
// Kept separate from the filter-bar component so the constants and the
// pure buildQuery helper can be shared without tripping react-refresh's
// component-only-export rule.

// RangePreset is the time-range selector value. "custom" reveals the
// from/to datetime inputs; the others resolve to a rolling window
// anchored on "now" when the query is built.
export type RangePreset = "1h" | "24h" | "7d" | "custom"

// StatusClass narrows results to a single HTTP status band. "all" omits
// the status_class query param entirely.
export type StatusClass = "all" | "2xx" | "4xx" | "5xx"

// EventFilters is the full filter state the observability browser drives
// its query from. The text fields are free-form (sent verbatim as query
// params when non-empty); from/to are only consulted when range is
// "custom", and carry datetime-local strings ("YYYY-MM-DDTHH:mm").
export interface EventFilters {
  range: RangePreset
  from: string
  to: string
  configuration: string
  gateway: string
  model: string
  backend: string
  protocol: string
  statusClass: StatusClass
}

export const DEFAULT_FILTERS: EventFilters = {
  range: "24h",
  from: "",
  to: "",
  configuration: "",
  gateway: "",
  model: "",
  backend: "",
  protocol: "",
  statusClass: "all",
}

export const PAGE_SIZE = 100

const RANGE_MS: Record<Exclude<RangePreset, "custom">, number> = {
  "1h": 60 * 60 * 1000,
  "24h": 24 * 60 * 60 * 1000,
  "7d": 7 * 24 * 60 * 60 * 1000,
}

// buildQuery turns the filter state + an optional cursor into the events
// query string. Preset ranges resolve to a rolling from=now-window; custom
// ranges pass the datetime-local inputs through as ISO instants. Empty text
// filters and "all" status are omitted so the backend sees no param.
export function buildQuery(filters: EventFilters, cursor: string): string {
  const params = new URLSearchParams()
  if (filters.range === "custom") {
    if (filters.from) params.set("from", new Date(filters.from).toISOString())
    if (filters.to) params.set("to", new Date(filters.to).toISOString())
  } else {
    params.set("from", new Date(Date.now() - RANGE_MS[filters.range]).toISOString())
  }
  if (filters.configuration.trim()) params.set("configuration", filters.configuration.trim())
  if (filters.gateway.trim()) params.set("gateway", filters.gateway.trim())
  if (filters.model.trim()) params.set("model", filters.model.trim())
  if (filters.backend.trim()) params.set("backend", filters.backend.trim())
  if (filters.protocol.trim()) params.set("protocol", filters.protocol.trim())
  if (filters.statusClass !== "all") params.set("status_class", filters.statusClass)
  if (cursor) params.set("cursor", cursor)
  params.set("limit", String(PAGE_SIZE))
  return params.toString()
}
