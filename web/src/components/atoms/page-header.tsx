import type React from "react"

// PageHeader is the shared page header for the telemetry console's top-level
// views (dashboard, messages, sessions, tool calls): a title with an optional
// description on the left, and page controls (filters, search, refresh) passed
// as children on the right.
//
// It stacks vertically on narrow viewports — the title block takes the full
// width so the description wraps as prose, and the controls drop onto their own
// wrapping row beneath it — then switches to a single row at md+. This is the
// fix for the mobile bug where a `flex-wrap` row let the `min-w-0` title shrink
// to a sliver beside the controls, wrapping the heading character-by-character.
export function PageHeader({
  title,
  sub,
  children,
}: {
  title: React.ReactNode
  sub?: React.ReactNode
  children?: React.ReactNode
}) {
  return (
    <div className="flex flex-col gap-3 md:flex-row md:flex-wrap md:items-center">
      <div className="min-w-0 md:flex-1">
        <h1 className="text-[24px] font-semibold tracking-[-0.02em]">{title}</h1>
        {sub != null && <div className="text-[13px] text-[color:var(--text-3)] mt-1">{sub}</div>}
      </div>
      {children != null && <div className="flex flex-wrap items-center gap-2 sm:gap-3">{children}</div>}
    </div>
  )
}
