import { useRef, useState } from "react"
import { Check, ChevronDown } from "lucide-react"
import { cn } from "@/lib/utils"
import { useDismiss } from "@/lib/use-dismiss"

// MultiSelectPanel is the checkbox option list shared by the filter-sheet
// MultiSelect below and the column-header FilterMenu — one panel, two anchors.
// An empty selection means "Any" (no predicate). allowSelectAll adds an "All"
// action that checks every listed option; it belongs on the OR dimensions
// (provider/model/…) and is withheld from tags, whose AND containment makes
// "every tag" a filter that matches almost nothing.
export function MultiSelectPanel({
  values,
  options,
  onChange,
  emptyText = "No options",
  allowSelectAll = false,
}: {
  values: string[]
  options: string[]
  onChange: (v: string[]) => void
  emptyText?: string
  allowSelectAll?: boolean
}) {
  const toggle = (o: string) => {
    onChange(values.includes(o) ? values.filter((v) => v !== o) : [...values, o])
  }
  return (
    <>
      {options.length > 0 && (
        <div className="flex items-center justify-between border-b border-[color:var(--border)] px-2 py-1">
          {allowSelectAll ? (
            <button
              type="button"
              onClick={() => onChange(options)}
              className="rounded-[3px] px-1 text-[10.5px] uppercase tracking-[0.05em] text-[color:var(--text-3)] hover:text-[color:var(--text)]"
            >
              All
            </button>
          ) : (
            <span />
          )}
          <button
            type="button"
            onClick={() => onChange([])}
            disabled={values.length === 0}
            className="rounded-[3px] px-1 text-[10.5px] uppercase tracking-[0.05em] text-[color:var(--text-3)] hover:text-[color:var(--text)] disabled:cursor-default disabled:opacity-40"
          >
            Clear
          </button>
        </div>
      )}
      {options.length === 0 && (
        <div className="px-2 py-1.5 text-[11.5px] text-[color:var(--text-4)]">{emptyText}</div>
      )}
      {options.map((o) => {
        const on = values.includes(o)
        return (
          <button
            key={o}
            type="button"
            onClick={() => toggle(o)}
            className="flex w-full items-center gap-2 rounded-[4px] px-2 py-1.5 text-left text-[12px] mono hover:bg-[color:var(--hover)]"
          >
            <span
              className={cn(
                "flex size-3.5 shrink-0 items-center justify-center rounded-[3px] border",
                on
                  ? "border-[color:var(--accent)] bg-[color:var(--accent)] text-[color:var(--accent-fg)]"
                  : "border-[color:var(--border)]",
              )}
            >
              {on && <Check className="size-3" />}
            </span>
            <span className="truncate">{o}</span>
          </button>
        )
      })}
    </>
  )
}

// MultiSelect is a checkbox popover form control for the filter sheets. The
// trigger shows the selected count; an empty selection reads "Any" (no
// predicate). Selection semantics belong to the dimension: the caller's query
// builder ORs scalar dimensions and ANDs tags.
export function MultiSelect({
  label,
  values,
  options,
  onChange,
  className,
  emptyText,
  allowSelectAll,
}: {
  label: string
  values: string[]
  options: string[]
  onChange: (v: string[]) => void
  className?: string
  emptyText?: string
  allowSelectAll?: boolean
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
          values.length ? "text-[color:var(--text)]" : "text-[color:var(--text-4)]",
        )}
      >
        <span className="text-[10px] uppercase tracking-[0.06em] text-[color:var(--text-4)]">{label}</span>
        <span className="mono truncate flex-1 text-left">
          {values.length ? `${values.length} selected` : "Any"}
        </span>
        <ChevronDown className="size-3.5 shrink-0 text-[color:var(--text-4)]" />
      </button>
      {open && (
        <div className="absolute z-30 mt-1 max-h-64 w-full min-w-[10rem] overflow-auto rounded-md border border-[color:var(--border)] bg-[color:var(--bg-1)] p-1 shadow-lg">
          <MultiSelectPanel
            values={values}
            options={options}
            onChange={onChange}
            emptyText={emptyText}
            allowSelectAll={allowSelectAll}
          />
        </div>
      )}
    </div>
  )
}
