import { cn } from "@/lib/utils"

// Skeleton is the base shimmer block: a pulsing rounded rectangle in the
// surface-3 token, sized by className/style. Every telemetry view's loading
// placeholder composes this so they all fade in identically. Decorative, so
// it's hidden from the accessibility tree — callers pair it with an
// aria-busy/sr-only label on the surrounding region.
export function Skeleton({ className, style }: { className?: string; style?: React.CSSProperties }) {
  return (
    <span
      aria-hidden="true"
      className={cn("block animate-pulse rounded-[4px] bg-[color:var(--bg-3)]", className)}
      style={style}
    />
  )
}

// SkelCol describes one skeleton table cell: an approximate bar width and
// whether it right-aligns (numeric columns), so the shimmer roughly traces the
// real column shapes rather than reading as a uniform grid.
export type SkelCol = { w?: string; align?: "right" }

// SkeletonRows fills a <tbody> with `rows` placeholder rows shaped by `cols`.
// Drops straight into an existing TableScroll in place of the real rows while a
// page loads, so the header + column rhythm stay put and only the body shimmers.
export function SkeletonRows({ rows, cols }: { rows: number; cols: SkelCol[] }) {
  return (
    <>
      {Array.from({ length: rows }).map((_, r) => (
        <tr key={r} className="border-t border-[color:var(--border)]">
          {cols.map((c, i) => (
            <td key={i} className={cn("px-4 py-2", c.align === "right" && "text-right")}>
              <Skeleton className={cn("h-3.5", c.align === "right" && "ml-auto")} style={{ width: c.w ?? "60%" }} />
            </td>
          ))}
        </tr>
      ))}
    </>
  )
}

// SkeletonTiles renders `count` KPI-shaped placeholder cards into the grid
// described by `className` (the caller supplies the same grid classes its real
// tile row uses), so the header tiles don't jump when data arrives.
export function SkeletonTiles({ count, className }: { count: number; className?: string }) {
  return (
    <div className={className}>
      {Array.from({ length: count }).map((_, i) => (
        <div
          key={i}
          className="rounded-[var(--radius-lg)] border border-[color:var(--border)] bg-[color:var(--bg-1)] p-[16px_18px] flex flex-col gap-2"
        >
          <Skeleton className="h-3 w-14" />
          <Skeleton className="h-6 w-20" />
          <Skeleton className="h-2.5 w-12" />
        </div>
      ))}
    </div>
  )
}

// SkeletonBlock is a single shimmer panel sized by height — used where a chart
// or graph would render so the panel reserves its space while data loads.
export function SkeletonBlock({ height, className }: { height: number | string; className?: string }) {
  return <Skeleton className={cn("w-full", className)} style={{ height }} />
}
