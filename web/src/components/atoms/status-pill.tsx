import { cn } from "@/lib/utils"

export function StatusPill({
  code,
  blocked,
  className,
}: {
  code: number
  blocked?: boolean
  className?: string
}) {
  let bg = "var(--ok-bg)"
  let fg = "var(--ok)"
  if (blocked) {
    bg = "var(--violet-bg)"
    fg = "var(--violet)"
  } else if (code >= 500) {
    bg = "var(--err-bg)"
    fg = "var(--err)"
  } else if (code >= 400) {
    bg = "var(--warn-bg)"
    fg = "var(--warn)"
  }
  return (
    <span
      className={cn(
        "mono inline-flex items-center rounded-[5px] px-1.5 py-0.5 text-[11px] font-medium tnum",
        className,
      )}
      style={{ background: bg, color: fg }}
    >
      {code}
    </span>
  )
}
