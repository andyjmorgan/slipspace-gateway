import { cn } from "@/lib/utils"

// DotPill is a small coloured-dot + label status indicator, shared by both
// frontends (the gateway admin SPA and the control-plane console). Unlike
// StatusPill — which is keyed to an HTTP status code — DotPill is generic: the
// caller supplies the accent colour (a CSS var) and the label, so it renders
// liveness (online/stale/offline), config drift, or any other categorical
// state consistently.
export function DotPill({
  color,
  label,
  className,
}: {
  color: string
  label: string
  className?: string
}) {
  return (
    <span
      className={cn("inline-flex items-center gap-1.5 text-[12px] capitalize", className)}
      style={{ color }}
    >
      <span className="inline-block size-1.5 rounded-full" style={{ background: color }} />
      {label}
    </span>
  )
}
