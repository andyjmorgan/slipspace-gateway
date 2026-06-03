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

// PAGE_SIZE is the message-list page increment: the observability inspector
// fetches this many recent messages and grows the limit by it on "Load
// more". Range resolution + the filter→query mapping live in the page
// (observability.tsx) since the CP recent-messages endpoint is limit-based,
// not cursor-based.
export const PAGE_SIZE = 100
