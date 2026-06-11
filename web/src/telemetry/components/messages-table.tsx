// Shared MessageEntry table presentation: the request table and the pager bar
// the messages browser and the session lifecycle page both render. Extracted
// from pages/messages.tsx so the lifecycle page's slice-bound embed reuses
// the exact same row/paging conventions instead of duplicating them. The
// fetch/paging hook lives in lib/messages-pager.ts.

import { ChevronLeft, ChevronRight } from "lucide-react"
import { Button } from "@/components/ui/button"
import { StatusPill } from "@/components/atoms/status-pill"
import { ProviderChip } from "@/components/atoms/provider-chip"
import { TableScroll } from "@/components/atoms/card"
import { SkeletonRows } from "@/components/atoms/skeleton"
import { Segmented } from "@/components/atoms/segmented"
import { fmt } from "@/lib/fmt"
import { MESSAGE_PAGE_SIZES } from "@/lib/messages-pager"
import type { MessageEntry } from "@/lib/messages"

export function Dash() {
  return <span className="text-[color:var(--text-4)]">—</span>
}

// TAG_CELL_MAX caps how many tag chips a row renders inline; the rest collapse
// into a "+N" chip so a request with many tags can't blow out the row height.
const TAG_CELL_MAX = 3

// TagCell renders a request's tags as compact chips, capped at TAG_CELL_MAX
// with a "+N" overflow. The full list is on the row's hover title, so the cap
// loses nothing — it just keeps rows uniform.
function TagCell({ tags }: { tags?: string[] }) {
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
}: {
  entries: MessageEntry[]
  status: "loading" | "ok" | "error"
  limit: number
  emptyText: string
  onRowClick: (entry: MessageEntry, index: number) => void
}) {
  return (
    <TableScroll>
      <thead>
        <tr className="text-[11px] uppercase tracking-[0.07em] text-[color:var(--text-3)]">
          <th className="text-left font-medium px-4 py-2">Time</th>
          <th className="text-left font-medium px-4 py-2">Status</th>
          <th className="text-left font-medium px-4 py-2">Provider</th>
          <th className="text-left font-medium px-4 py-2">Protocol</th>
          <th className="text-left font-medium px-4 py-2">Model</th>
          <th className="text-left font-medium px-4 py-2">Configuration</th>
          <th className="text-left font-medium px-4 py-2">Tags</th>
          <th className="text-right font-medium px-4 py-2">Duration</th>
          <th className="text-right font-medium px-4 py-2">Tokens</th>
        </tr>
      </thead>
      <tbody aria-busy={status === "loading"}>
        {status === "loading" && (
          <SkeletonRows
            rows={Math.min(limit, 12)}
            cols={[{ w: "5rem" }, { w: "2.5rem" }, { w: "4rem" }, { w: "3.5rem" }, { w: "6rem" }, { w: "5rem" }, { w: "5rem" }, { w: "3rem", align: "right" }, { w: "3.5rem", align: "right" }]}
          />
        )}
        {status !== "loading" && entries.map((e, i) => (
          <tr
            key={e.event_id}
            onClick={() => onRowClick(e, i)}
            className="border-t border-[color:var(--border)] cursor-pointer hover:bg-[color:var(--hover)]"
          >
            <td className="mono text-[11.5px] px-4 py-2 text-[color:var(--text-3)] whitespace-nowrap">{rowTime(e.at)}</td>
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
          </tr>
        ))}
        {status === "ok" && entries.length === 0 && (
          <tr><td colSpan={9} className="px-4 py-10 text-center text-[12px] text-[color:var(--text-4)]">{emptyText}</td></tr>
        )}
      </tbody>
    </TableScroll>
  )
}

/**
 * MessagesPagerBar is the table footer: the rows-per-page segment plus the
 * page indicator and Next/Prev keyset navigation.
 */
export function MessagesPagerBar({
  limit,
  onLimit,
  pageIndex,
  hasPrev,
  hasNext,
  onPrev,
  onNext,
}: {
  limit: number
  onLimit: (n: number) => void
  pageIndex: number
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
        <span className="text-[11px] text-[color:var(--text-4)] mono">page {pageIndex + 1}</span>
        <Button variant="ghost" size="icon-xs" onClick={onPrev} disabled={!hasPrev} aria-label="Previous page"><ChevronLeft /></Button>
        <Button variant="ghost" size="icon-xs" onClick={onNext} disabled={!hasNext} aria-label="Next page"><ChevronRight /></Button>
      </div>
    </div>
  )
}
