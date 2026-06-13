import { useEffect, useRef, type ReactNode } from "react"
import { createPortal } from "react-dom"
import { X } from "lucide-react"
import { Button } from "@/components/ui/button"

// FOCUSABLE selects the tabbable descendants the focus trap cycles through.
const FOCUSABLE =
  'a[href],button:not([disabled]),input:not([disabled]),select:not([disabled]),textarea:not([disabled]),[tabindex]:not([tabindex="-1"])'

// Sheet is a right-side slide-over panel: a portal'd dialog that slides in from
// the edge over a dimmed backdrop, leaving the page visible behind it. Used for
// the table filter panels — the controls tuck away so the toolbar stays clean,
// and the table updates live behind the open sheet. It owns the same a11y
// contract as InspectorModal (role=dialog + aria-modal, focus-in on open, Tab
// trap, focus restore on close, Esc + backdrop-click to dismiss); renders
// nothing when closed.
export function Sheet({
  open,
  onClose,
  title,
  children,
  footer,
}: {
  // open mounts the sheet; closed renders nothing (no exit animation).
  open: boolean
  // onClose dismisses — backdrop click, the X button, and Esc.
  onClose: () => void
  // title is the panel header content (heading + optional count).
  title: ReactNode
  // children is the scrollable panel body (the filter controls).
  children: ReactNode
  // footer pins to the panel bottom (Clear / Done).
  footer?: ReactNode
}) {
  const panelRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    if (!open) return
    const restoreTo = document.activeElement as HTMLElement | null
    panelRef.current?.focus()
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") {
        onClose()
        return
      }
      if (e.key !== "Tab") return
      const panel = panelRef.current
      if (!panel) return
      const items = panel.querySelectorAll<HTMLElement>(FOCUSABLE)
      if (items.length === 0) {
        e.preventDefault()
        panel.focus()
        return
      }
      const first = items[0]
      const last = items[items.length - 1]
      const active = document.activeElement
      if (e.shiftKey && (active === first || active === panel)) {
        e.preventDefault()
        last.focus()
      } else if (!e.shiftKey && active === last) {
        e.preventDefault()
        first.focus()
      }
    }
    document.addEventListener("keydown", onKey)
    return () => {
      document.removeEventListener("keydown", onKey)
      if (restoreTo && document.contains(restoreTo)) restoreTo.focus()
    }
  }, [open, onClose])

  if (!open) return null

  return createPortal(
    <div
      role="dialog"
      aria-modal="true"
      aria-label={typeof title === "string" ? title : "Panel"}
      className="fixed inset-0 z-50 flex justify-end bg-black/40 animate-in fade-in-0 duration-150"
      onClick={onClose}
    >
      <div
        ref={panelRef}
        tabIndex={-1}
        className="flex h-full w-[min(420px,92vw)] flex-col border-l border-[color:var(--border)] bg-[color:var(--bg-1)] shadow-xl outline-none animate-in slide-in-from-right duration-200"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="flex flex-none items-center gap-2 px-4 h-[var(--header-h)] border-b border-[color:var(--border)]">
          <span className="text-[13px] font-semibold tracking-[-0.01em] flex-1 min-w-0">{title}</span>
          <Button variant="ghost" size="icon-sm" onClick={onClose} aria-label="Close filters" title="Close (Esc)">
            <X />
          </Button>
        </div>
        <div className="flex-1 min-h-0 overflow-y-auto p-4">{children}</div>
        {footer && (
          <div className="flex flex-none items-center gap-2 px-4 py-3 border-t border-[color:var(--border)]">{footer}</div>
        )}
      </div>
    </div>,
    document.body,
  )
}
