import { cn } from "@/lib/utils"

type Variant = "default" | "ghost" | "success" | "warn" | "danger" | "violet"

const STYLES: Record<Variant, { bg: string; fg: string }> = {
  default: { bg: "var(--bg-2)", fg: "var(--text-2)" },
  ghost: { bg: "transparent", fg: "var(--text-3)" },
  success: { bg: "var(--ok-bg)", fg: "var(--ok)" },
  warn: { bg: "var(--warn-bg)", fg: "var(--warn)" },
  danger: { bg: "var(--err-bg)", fg: "var(--err)" },
  violet: { bg: "var(--violet-bg)", fg: "var(--violet)" },
}

export function Tag({
  children,
  variant = "default",
  className,
}: {
  children: React.ReactNode
  variant?: Variant
  className?: string
}) {
  const s = STYLES[variant]
  return (
    <span
      className={cn(
        "inline-flex items-center rounded-[5px] px-1.5 py-0.5 text-[11px] font-medium",
        variant === "ghost" && "border border-[color:var(--border)]",
        className,
      )}
      style={{ background: s.bg, color: s.fg }}
    >
      {children}
    </span>
  )
}
