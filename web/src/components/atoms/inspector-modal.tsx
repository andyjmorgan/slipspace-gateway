import { useEffect, type ReactNode } from "react"
import { createPortal } from "react-dom"
import { ChevronDown, ChevronUp, X } from "lucide-react"
import { Button } from "@/components/ui/button"

// InspectorModal is the shared per-request detail shell used by both the admin
// live-messages view and the telemetry messages browser. It owns the parts that
// are purely presentation + navigation plumbing — the portal, the dimmed
// backdrop (click to close), the centered tall container, the prev/next/close
// button cluster, and the Esc / ArrowUp / ArrowDown keyboard handling — so the
// two surfaces share one look instead of each maintaining its own copy. The
// page supplies the `header` left-hand content and the body `children`; what
// differs between the two surfaces (the admin live feed vs the telemetry GenAI
// content channel) lives entirely in those, not here.
//
// ArrowUp maps to onPrev and ArrowDown to onNext, matching the ChevronUp /
// ChevronDown buttons; a handler left undefined disables both its button and
// its key. Children are placed as direct flex children of the column container,
// so a body element marked `flex-1 min-h-0` fills the remaining height (admin)
// while a body that should scroll wraps itself in an `overflow-auto` region
// (telemetry).
export function InspectorModal({
  header,
  onClose,
  onPrev,
  onNext,
  prevTitle = "Previous (↑)",
  nextTitle = "Next (↓)",
  children,
}: {
  // header is the left-hand header content (title block, status pill, ids…).
  header: ReactNode
  // onClose dismisses the modal — backdrop click, the X button, and Esc.
  onClose: () => void
  // onPrev / onNext move to the adjacent entry; omit to disable that direction.
  onPrev?: () => void
  onNext?: () => void
  // prevTitle / nextTitle label the up/down buttons (tooltip + aria-label) so a
  // surface with newer/older semantics can say so.
  prevTitle?: string
  nextTitle?: string
  children: ReactNode
}) {
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") {
        onClose()
      } else if (e.key === "ArrowUp" && onPrev) {
        e.preventDefault()
        onPrev()
      } else if (e.key === "ArrowDown" && onNext) {
        e.preventDefault()
        onNext()
      }
    }
    document.addEventListener("keydown", onKey)
    return () => document.removeEventListener("keydown", onKey)
  }, [onPrev, onNext, onClose])

  return createPortal(
    <div
      role="dialog"
      aria-modal="true"
      aria-label="Request detail"
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/55 p-4"
      onClick={onClose}
    >
      <div
        className="relative flex h-[92vh] w-[92vw] max-w-7xl flex-col rounded-[var(--radius-lg)] border border-[color:var(--border)] bg-[color:var(--bg-1)] p-4 shadow-xl"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="mb-3 flex flex-none items-start gap-2">
          <div className="min-w-0 flex-1">{header}</div>
          <div className="flex flex-none items-center gap-0.5">
            <Button
              variant="ghost"
              size="icon"
              onClick={onPrev}
              disabled={!onPrev}
              aria-label={prevTitle}
              title={prevTitle}
            >
              <ChevronUp />
            </Button>
            <Button
              variant="ghost"
              size="icon"
              onClick={onNext}
              disabled={!onNext}
              aria-label={nextTitle}
              title={nextTitle}
            >
              <ChevronDown />
            </Button>
            <Button variant="ghost" size="icon" onClick={onClose} aria-label="Close" title="Close (Esc)">
              <X />
            </Button>
          </div>
        </div>
        {children}
      </div>
    </div>,
    document.body,
  )
}
