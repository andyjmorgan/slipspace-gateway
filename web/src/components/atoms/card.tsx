import { cn } from "@/lib/utils"

type Accent = "ok" | "warn" | "err"

const ACCENT_BORDER: Record<Accent, string> = {
  ok: "color-mix(in oklab, var(--ok) 28%, var(--border))",
  warn: "color-mix(in oklab, var(--warn) 28%, var(--border))",
  err: "color-mix(in oklab, var(--err) 32%, var(--border))",
}

export function PanelCard({
  children,
  className,
  accent,
  ...rest
}: React.ComponentProps<"div"> & { accent?: Accent }) {
  return (
    <div
      className={cn(
        "rounded-[var(--radius-lg)] border bg-[color:var(--bg-1)] flex flex-col",
        className,
      )}
      style={{
        borderColor: accent ? ACCENT_BORDER[accent] : "var(--border)",
      }}
      {...rest}
    >
      {children}
    </div>
  )
}

export function PanelHead({
  title,
  sub,
  action,
  className,
}: {
  title: React.ReactNode
  sub?: React.ReactNode
  action?: React.ReactNode
  className?: string
}) {
  return (
    <div
      className={cn(
        "flex items-center gap-2 px-4 py-3 border-b border-[color:var(--border)]",
        className,
      )}
    >
      <span className="text-[11.5px] font-semibold uppercase tracking-[0.07em] text-[color:var(--text-2)]">
        {title}
      </span>
      {sub && <span className="text-[11px] text-[color:var(--text-3)]">{sub}</span>}
      {action && <div className="ml-auto">{action}</div>}
    </div>
  )
}
