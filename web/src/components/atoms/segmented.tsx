import { cn } from "@/lib/utils"

type Option<T extends string> = { value: T; label: string }

export function Segmented<T extends string>({
  value,
  onChange,
  options,
  className,
}: {
  value: T
  onChange: (v: T) => void
  options: Option<T>[]
  className?: string
}) {
  return (
    <div
      className={cn(
        "inline-flex rounded-[var(--radius)] border border-[color:var(--border)] bg-[color:var(--bg-1)] p-0.5 text-[12px]",
        className,
      )}
    >
      {options.map((o) => {
        const selected = value === o.value
        return (
          <button
            key={o.value}
            type="button"
            onClick={() => onChange(o.value)}
            aria-selected={selected}
            className={cn(
              "mono rounded-[4px] px-2.5 py-1 transition-colors",
              selected
                ? "bg-[color:var(--bg-3)] text-[color:var(--text)]"
                : "text-[color:var(--text-3)] hover:text-[color:var(--text)]",
            )}
          >
            {o.label}
          </button>
        )
      })}
    </div>
  )
}
