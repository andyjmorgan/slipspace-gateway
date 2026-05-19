import { cn } from "@/lib/utils"

type Accent = "ok" | "warn" | "err" | "info"

const ACCENT_COLOR: Record<Accent, string> = {
  ok: "var(--ok)",
  warn: "var(--warn)",
  err: "var(--err)",
  info: "var(--accent)",
}

export function KPI({
  label,
  value,
  sub,
  delta,
  deltaDir,
  accent,
  className,
}: {
  label: string
  value: React.ReactNode
  sub?: React.ReactNode
  delta?: string
  deltaDir?: "up" | "dn"
  accent?: Accent
  className?: string
}) {
  return (
    <div
      className={cn(
        "rounded-[var(--radius-lg)] border border-[color:var(--border)] bg-[color:var(--bg-1)] p-[16px_18px] flex flex-col gap-1",
        className,
      )}
    >
      <div className="flex items-center gap-1.5 text-[11.5px] font-medium uppercase tracking-[0.07em] text-[color:var(--text-3)]">
        {accent && (
          <span
            className="inline-block size-1.5 rounded-full"
            style={{ background: ACCENT_COLOR[accent] }}
          />
        )}
        {label}
      </div>
      <div className="mono tnum text-[22px] font-semibold leading-tight tracking-[-0.01em] text-[color:var(--text)]">
        {value}
      </div>
      {(sub || delta) && (
        <div className="flex items-center gap-2 text-[11.5px] mt-0.5">
          {delta && (
            <span
              className={cn(
                "mono",
                deltaDir === "up" && "text-[color:var(--err)]",
                deltaDir === "dn" && "text-[color:var(--ok)]",
              )}
            >
              {deltaDir === "up" ? "▲" : deltaDir === "dn" ? "▼" : ""} {delta}
            </span>
          )}
          {sub && <span className="text-[color:var(--text-3)]">{sub}</span>}
        </div>
      )}
    </div>
  )
}
