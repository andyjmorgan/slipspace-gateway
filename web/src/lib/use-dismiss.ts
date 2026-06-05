import { useEffect } from "react"

// useDismiss closes a popover on outside-click or Escape. Shared by the filter
// dropdowns (Select, MultiSelect).
export function useDismiss(ref: React.RefObject<HTMLElement | null>, close: () => void) {
  useEffect(() => {
    const onDown = (e: MouseEvent) => {
      if (ref.current && !ref.current.contains(e.target as Node)) close()
    }
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") close()
    }
    document.addEventListener("mousedown", onDown)
    document.addEventListener("keydown", onKey)
    return () => {
      document.removeEventListener("mousedown", onDown)
      document.removeEventListener("keydown", onKey)
    }
  }, [ref, close])
}
