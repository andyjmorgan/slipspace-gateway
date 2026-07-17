import { useEffect, useRef, useState } from "react"
import { Funnel } from "lucide-react"
import { cn } from "@/lib/utils"
import { useDismiss } from "@/lib/use-dismiss"
import { MultiSelectPanel } from "@/components/atoms/multi-select"

// FILTER_MENU_WIDTH_PX is the popover's fixed width, needed up front so the
// open handler can clamp the fixed-position menu inside the viewport before
// the element exists to measure.
const FILTER_MENU_WIDTH_PX = 224

// HeaderFilter is the column-header filter affordance: a funnel trigger that
// pops a MultiSelectPanel for the column's dimension. The popover is
// position:fixed and anchored to the trigger's rect at open time — the tables
// live inside TableScroll (overflow-x-auto), where an absolutely-positioned
// child would be clipped to the scroll box. Fixed elements don't track the
// anchor, so any scroll (captured on window) closes the menu instead of
// leaving it detached.
export function HeaderFilter({
  label,
  values,
  options,
  onChange,
  emptyText,
  allowSelectAll = true,
}: {
  // label names the dimension in the trigger's accessible name/tooltip.
  label: string
  values: string[]
  options: string[]
  onChange: (v: string[]) => void
  emptyText?: string
  // allowSelectAll surfaces the panel's "All" action; on by default for the
  // OR dimensions, switched off for tags (AND containment).
  allowSelectAll?: boolean
}) {
  const [anchor, setAnchor] = useState<{ top: number; left: number } | null>(null)
  const ref = useRef<HTMLSpanElement>(null)
  const close = () => setAnchor(null)
  useDismiss(ref, close)
  // Close on any scroll while open — the fixed popover doesn't track its
  // anchor, so it would otherwise stay pinned to the viewport while the
  // header scrolls away. Capture-phase so scrolls inside TableScroll (which
  // don't bubble as window scroll events) also dismiss.
  const open = anchor !== null
  useEffect(() => {
    if (!open) return
    const onScroll = () => setAnchor(null)
    window.addEventListener("scroll", onScroll, { capture: true })
    return () => window.removeEventListener("scroll", onScroll, { capture: true })
  }, [open])

  const active = values.length > 0
  const toggleOpen = () => {
    if (anchor) {
      close()
      return
    }
    const rect = ref.current?.getBoundingClientRect()
    if (!rect) return
    setAnchor({
      top: rect.bottom + 4,
      left: Math.max(8, Math.min(rect.left, window.innerWidth - FILTER_MENU_WIDTH_PX - 8)),
    })
  }

  return (
    <span ref={ref} className="relative inline-flex">
      <button
        type="button"
        onClick={toggleOpen}
        aria-haspopup="listbox"
        aria-expanded={open}
        aria-label={`Filter by ${label}`}
        title={`Filter by ${label}`}
        className={cn(
          "inline-flex items-center gap-0.5 rounded-[3px] px-0.5 hover:text-[color:var(--text)] focus-visible:outline-2 focus-visible:outline-[color:var(--accent)]",
          active ? "text-[color:var(--accent)]" : "text-[color:var(--text-4)]",
        )}
      >
        <Funnel size={11} className={cn(active && "fill-current")} />
        {active && <span className="mono text-[10px] tnum">{values.length}</span>}
      </button>
      {anchor && (
        <div
          role="listbox"
          aria-label={`${label} filter options`}
          style={{ position: "fixed", top: anchor.top, left: anchor.left, width: FILTER_MENU_WIDTH_PX }}
          className="z-30 max-h-64 overflow-auto rounded-md border border-[color:var(--border)] bg-[color:var(--bg-1)] p-1 shadow-lg normal-case tracking-normal"
        >
          <MultiSelectPanel
            values={values}
            options={options}
            onChange={onChange}
            emptyText={emptyText}
            allowSelectAll={allowSelectAll}
          />
        </div>
      )}
    </span>
  )
}
