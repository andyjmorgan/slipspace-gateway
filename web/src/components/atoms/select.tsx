import { useRef, useState } from "react"
import { Check, ChevronDown, X } from "lucide-react"
import { cn } from "@/lib/utils"
import { useDismiss } from "@/lib/use-dismiss"

// Select is a compact single-select dropdown for the filter bar. Options come
// from the facets endpoint; an empty value means "no filter on this dimension"
// and renders the placeholder. The control is clearable when a value is set.
export function Select({
  label,
  value,
  options,
  onChange,
  placeholder = "Any",
  className,
}: {
  label: string
  value: string
  options: string[]
  onChange: (v: string) => void
  placeholder?: string
  className?: string
}) {
  const [open, setOpen] = useState(false)
  const ref = useRef<HTMLDivElement>(null)
  useDismiss(ref, () => setOpen(false))

  return (
    <div ref={ref} className={cn("relative", className)}>
      <button
        type="button"
        onClick={() => setOpen((o) => !o)}
        aria-haspopup="listbox"
        aria-expanded={open}
        className={cn(
          "flex h-9 w-full items-center gap-2 rounded-md border border-[color:var(--border)] bg-transparent px-2.5 text-[12px] transition-colors hover:border-[color:var(--text-4)]",
          value ? "text-[color:var(--text)]" : "text-[color:var(--text-4)]",
        )}
      >
        <span className="text-[10px] uppercase tracking-[0.06em] text-[color:var(--text-4)]">{label}</span>
        <span className="mono truncate flex-1 text-left">{value || placeholder}</span>
        {value ? (
          <X
            className="size-3.5 shrink-0 text-[color:var(--text-4)] hover:text-[color:var(--text)]"
            onClick={(e) => {
              e.stopPropagation()
              onChange("")
              setOpen(false)
            }}
          />
        ) : (
          <ChevronDown className="size-3.5 shrink-0 text-[color:var(--text-4)]" />
        )}
      </button>
      {open && (
        <ul
          role="listbox"
          className="absolute z-30 mt-1 max-h-64 w-full min-w-[10rem] overflow-auto rounded-md border border-[color:var(--border)] bg-[color:var(--bg-1)] p-1 shadow-lg"
        >
          {options.length === 0 && (
            <li className="px-2 py-1.5 text-[11.5px] text-[color:var(--text-4)]">No values</li>
          )}
          {options.map((o) => (
            <li key={o}>
              <button
                type="button"
                onClick={() => {
                  onChange(o)
                  setOpen(false)
                }}
                className="flex w-full items-center gap-2 rounded-[4px] px-2 py-1.5 text-left text-[12px] mono hover:bg-[color:var(--hover)]"
              >
                <Check className={cn("size-3.5 shrink-0", o === value ? "opacity-100" : "opacity-0")} />
                <span className="truncate">{o}</span>
              </button>
            </li>
          ))}
        </ul>
      )}
    </div>
  )
}
