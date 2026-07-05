// Shared MessageEntry table presentation: the request table and the pager bar
// the messages browser and the session lifecycle page both render. Extracted
// from pages/messages.tsx so the lifecycle page's slice-bound embed reuses
// the exact same row/paging conventions instead of duplicating them. The
// fetch/paging hook lives in lib/messages-pager.ts.

import { ArrowDown, ArrowUp, ArrowUpDown, ChevronLeft, ChevronRight } from "lucide-react"
import { Button } from "@/components/ui/button"
import { StatusPill } from "@/components/atoms/status-pill"
import { ProviderChip } from "@/components/atoms/provider-chip"
import { TableScroll } from "@/components/atoms/card"
import { SkeletonRows } from "@/components/atoms/skeleton"
import { Segmented } from "@/components/atoms/segmented"
import { cn } from "@/lib/utils"
import { fmt } from "@/lib/fmt"
import { MESSAGE_PAGE_SIZES } from "@/lib/messages-pager"
import type { MessageEntry } from "@/lib/messages"

export function Dash() {
  return <span className="text-[color:var(--text-4)]">—</span>
}

// SortState is the active column sort a table view renders + reports through
// onSort: the column key and whether it's descending.
export type SortState = { key: string; desc: boolean }

// toggleSort is the shared header-click reducer: clicking the active column
// flips direction; clicking a new column selects it descending-first (the
// useful default for time/counts). Callers pass it the previous state.
export function toggleSort(prev: SortState | undefined, key: string): SortState {
  return prev?.key === key ? { key, desc: !prev.desc } : { key, desc: true }
}

// SortHeader is a table <th>: a sortable column button (with an asc/desc/idle
// indicator + aria-sort) when onSort is supplied, else a plain header. Shared
// by the messages and sessions tables so both sort the same way.
export function SortHeader({
  label,
  col,
  sort,
  onSort,
  align = "left",
  title,
}: {
  label: React.ReactNode
  col: string
  sort?: SortState
  onSort?: (key: string) => void
  align?: "left" | "right"
  title?: string
}) {
  const base = cn("font-medium px-4 py-2", align === "right" ? "text-right" : "text-left")
  if (!onSort) {
    return (
      <th scope="col" className={base} title={title}>
        {label}
      </th>
    )
  }
  const active = sort?.key === col
  return (
    <th scope="col" className={base} aria-sort={active ? (sort!.desc ? "descending" : "ascending") : "none"}>
      <button
        type="button"
        onClick={() => onSort(col)}
        title={title ?? "Sort by this column"}
        className={cn(
          "inline-flex items-center gap-1 -mx-1 px-1 rounded-[3px] hover:text-[color:var(--text)] focus-visible:outline-2 focus-visible:outline-[color:var(--accent)]",
          align === "right" && "flex-row-reverse",
          active && "text-[color:var(--text)]",
        )}
      >
        {label}
        {active ? (
          sort!.desc ? <ArrowDown size={12} /> : <ArrowUp size={12} />
        ) : (
          <ArrowUpDown size={12} className="opacity-40" />
        )}
      </button>
    </th>
  )
}

// TAG_CELL_MAX caps how many tag chips a row renders inline; the rest collapse
// into a "+N" chip so a request with many tags can't blow out the row height.
const TAG_CELL_MAX = 3

// TagCell renders a request's tags as compact chips, capped at TAG_CELL_MAX
// with a "+N" overflow. The full list is on the row's hover title, so the cap
// loses nothing — it just keeps rows uniform. Shared with the sessions table.
export function TagCell({ tags }: { tags?: string[] }) {
  if (!tags || tags.length === 0) return <Dash />
  const shown = tags.slice(0, TAG_CELL_MAX)
  const overflow = tags.length - shown.length
  return (
    <div className="flex flex-wrap items-center gap-1" title={tags.join(", ")}>
      {shown.map((t) => (
        <span
          key={t}
          className="inline-flex max-w-[10rem] items-center truncate rounded-[4px] border border-[color:var(--border)] bg-[color:var(--bg-2)] px-1.5 py-0.5 text-[10.5px] mono uppercase tracking-[0.04em] text-[color:var(--text-2)]"
        >
          {t}
        </span>
      ))}
      {overflow > 0 && (
        <span className="mono text-[10.5px] text-[color:var(--text-4)]">+{overflow}</span>
      )}
    </div>
  )
}

// AgentCell renders the per-row agent identity as a color-dotted label — the
// lane color from the session view's alias map, so a subagent's requests read
// the same hue here as in the timeline.
function AgentCell({ label, color }: { label: string; color?: string }) {
  return (
    <td className="px-4 py-2 whitespace-nowrap">
      <span className="inline-flex items-center gap-1.5 text-[12px]" style={{ color: color ?? "var(--text-2)" }} title={label}>
        <span aria-hidden="true" className="inline-block w-2 h-2 rounded-[2px] shrink-0" style={{ background: color ?? "var(--text-4)" }} />
        <span className="truncate max-w-[9rem]">{label}</span>
      </span>
    </td>
  )
}

// rowTime renders a row's completion time with millisecond precision.
function rowTime(iso: string): string {
  const d = new Date(iso)
  return d.toLocaleTimeString(undefined, { hour12: false }) + "." + String(d.getMilliseconds()).padStart(3, "0")
}

/**
 * MessagesTableView renders one page of MessageEntry rows with the browser's
 * standard columns, the loading skeleton, and the empty state. Pure
 * presentation — pair it with useMessagesPager (or any client-side pager) and
 * MessagesPagerBar inside a PanelCard.
 */
export function MessagesTableView({
  entries,
  status,
  limit,
  emptyText,
  onRowClick,
  agent,
  sort,
  onSort,
}: {
  entries: MessageEntry[]
  status: "loading" | "ok" | "error"
  limit: number
  emptyText: string
  onRowClick: (entry: MessageEntry, index: number) => void
  // agent, when provided, renders an "Agent" column resolving each row to a
  // display label + lane color (the session lifecycle page passes its alias
  // map). Omitted by the global messages browser, which has no session scope
  // and so no alias pool — the column simply doesn't render there.
  agent?: (entry: MessageEntry) => { label: string; color?: string }
  // sort / onSort make the Time, Status, and Tokens columns sortable. Supplied
  // by the global browser (server-side sort); omitted by the session embed,
  // whose rows are a client-side slice — its headers stay plain.
  sort?: SortState
  onSort?: (key: string) => void
}) {
  const cols = agent ? 11 : 10
  return (
    <TableScroll>
      <thead>
        <tr className="text-[11px] uppercase tracking-[0.07em] text-[color:var(--text-3)]">
          <SortHeader label="Time" col="time" sort={sort} onSort={onSort} />
          {agent && <th scope="col" className="text-left font-medium px-4 py-2">Agent</th>}
          <SortHeader label="Status" col="status" sort={sort} onSort={onSort} />
          <SortHeader label="Provider" col="provider" sort={sort} onSort={onSort} />
          <SortHeader label="Protocol" col="protocol" sort={sort} onSort={onSort} />
          <SortHeader label="Model" col="model" sort={sort} onSort={onSort} />
          <SortHeader label="Configuration" col="configuration" sort={sort} onSort={onSort} />
          <th scope="col" className="text-left font-medium px-4 py-2">Tags</th>
          <th scope="col" className="text-right font-medium px-4 py-2">Duration</th>
          <SortHeader
            col="tokens"
            sort={sort}
            onSort={onSort}
            align="right"
            title="tokens in / tokens out"
            label={<>Tokens <span className="normal-case tracking-normal text-[color:var(--text-4)]">(in/out)</span></>}
          />
          <th scope="col" className="text-right font-medium px-4 py-2">Cost</th>
        </tr>
      </thead>
      <tbody aria-busy={status === "loading"}>
        {status === "loading" && (
          <SkeletonRows
            rows={Math.min(limit, 12)}
            cols={[{ w: "5rem" }, ...(agent ? [{ w: "5rem" }] : []), { w: "2.5rem" }, { w: "4rem" }, { w: "3.5rem" }, { w: "6rem" }, { w: "5rem" }, { w: "5rem" }, { w: "3rem", align: "right" as const }, { w: "3.5rem", align: "right" as const }, { w: "3rem", align: "right" as const }]}
          />
        )}
        {status !== "loading" && entries.map((e, i) => (
          <tr
            key={e.event_id}
            role="button"
            tabIndex={0}
            aria-label={`Open request ${e.model || e.provider || ""} at ${rowTime(e.at)}, status ${e.status_code}`}
            onClick={() => onRowClick(e, i)}
            onKeyDown={(ev) => {
              if (ev.key === "Enter" || ev.key === " ") {
                ev.preventDefault()
                onRowClick(e, i)
              }
            }}
            className="border-t border-[color:var(--border)] cursor-pointer hover:bg-[color:var(--hover)] focus-visible:outline-2 focus-visible:-outline-offset-2 focus-visible:outline-[color:var(--accent)]"
          >
            <td className="mono text-[11.5px] px-4 py-2 text-[color:var(--text-3)] whitespace-nowrap">{rowTime(e.at)}</td>
            {agent && <AgentCell {...agent(e)} />}
            <td className="px-4 py-2"><StatusPill code={e.status_code} /></td>
            <td className="px-4 py-2">{e.provider ? <ProviderChip name={e.provider} /> : <Dash />}</td>
            <td className="mono text-[12px] px-4 py-2">{e.protocol || <Dash />}</td>
            <td className="mono text-[12px] px-4 py-2">{e.model || <Dash />}</td>
            <td className="mono text-[12px] px-4 py-2">{e.configuration || <Dash />}</td>
            <td className="px-4 py-2"><TagCell tags={e.tags} /></td>
            <td className="mono tnum text-[12px] text-right px-4 py-2">{fmt.ms(e.duration_ms)}</td>
            <td className="mono tnum text-[11.5px] text-right px-4 py-2 text-[color:var(--text-3)]">
              {(e.tokens_in ?? 0) + (e.tokens_out ?? 0) > 0 ? `${fmt.compact(e.tokens_in ?? 0)}/${fmt.compact(e.tokens_out ?? 0)}` : <Dash />}
            </td>
            <td className="mono tnum text-[11.5px] text-right px-4 py-2 text-[color:var(--text-3)]">
              {(e.cost_usd ?? 0) > 0 ? fmt.usd(e.cost_usd) : <Dash />}
            </td>
          </tr>
        ))}
        {status === "ok" && entries.length === 0 && (
          <tr><td colSpan={cols} className="px-4 py-10 text-center text-[12px] text-[color:var(--text-4)]">{emptyText}</td></tr>
        )}
      </tbody>
    </TableScroll>
  )
}

/**
 * MessagesPagerBar is the table footer: the rows-per-page segment, a
 * "X–Y of N" range readout, and Next/Prev keyset navigation.
 */
export function MessagesPagerBar({
  limit,
  onLimit,
  pageIndex,
  shown,
  total,
  hasPrev,
  hasNext,
  onPrev,
  onNext,
}: {
  limit: number
  onLimit: (n: number) => void
  pageIndex: number
  // shown is the row count on the current page; total is the full match count.
  shown: number
  total: number
  hasPrev: boolean
  hasNext: boolean
  onPrev: () => void
  onNext: () => void
}) {
  return (
    <div className="flex items-center gap-3 px-4 py-2.5 border-t border-[color:var(--border)] mt-auto">
      <div className="flex items-center gap-1.5 text-[11px] text-[color:var(--text-4)]">
        <span>Rows</span>
        <Segmented
          value={String(limit)}
          onChange={(v) => onLimit(Number(v))}
          options={MESSAGE_PAGE_SIZES.map((n) => ({ value: String(n), label: String(n) }))}
        />
      </div>
      <div className="ml-auto flex items-center gap-2">
        <span className="text-[11px] text-[color:var(--text-4)] mono">{rangeLabel(pageIndex, limit, shown, total)}</span>
        <Button variant="ghost" size="icon-xs" onClick={onPrev} disabled={!hasPrev} aria-label="Previous page"><ChevronLeft /></Button>
        <Button variant="ghost" size="icon-xs" onClick={onNext} disabled={!hasNext} aria-label="Next page"><ChevronRight /></Button>
      </div>
    </div>
  )
}

// rangeLabel renders the "X–Y of N" pager readout from the keyset page position.
// With no rows it reads "0 of N"; the start index is derived from the page
// index and limit (keyset pages are uniform until the last).
export function rangeLabel(pageIndex: number, limit: number, shown: number, total: number): string {
  if (shown === 0) return `0 of ${fmt.num(total)}`
  const start = pageIndex * limit + 1
  return `${fmt.num(start)}–${fmt.num(start + shown - 1)} of ${fmt.num(total)}`
}
