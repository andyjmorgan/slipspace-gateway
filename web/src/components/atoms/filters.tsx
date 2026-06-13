import { type ReactNode } from "react"
import { SlidersHorizontal, X } from "lucide-react"
import { Button } from "@/components/ui/button"

// Chip is one applied filter shown in the toolbar's active-filter row: a
// human label, the current value, and a clearer for just that dimension.
export type Chip = { key: string; label: string; value: string; onClear: () => void }

// FiltersButton is the toolbar trigger that opens the filter sheet, badged with
// the active-filter count so applied filters are visible without opening it.
export function FiltersButton({ count, onClick }: { count: number; onClick: () => void }) {
  return (
    <Button variant="secondary" size="sm" onClick={onClick} aria-label={`Filters${count > 0 ? `, ${count} active` : ""}`}>
      <SlidersHorizontal />
      <span className="hidden sm:inline">Filters</span>
      {count > 0 && (
        <span className="ml-0.5 inline-flex h-[18px] min-w-[18px] items-center justify-center rounded-full bg-[color:var(--accent)] px-1 text-[10px] font-semibold text-white">
          {count}
        </span>
      )}
    </Button>
  )
}

// ActiveFilterChips renders the applied filters as removable pills with a
// "Clear all". Clicking a pill clears just that dimension; renders nothing when
// no filters are active.
export function ActiveFilterChips({ chips, onClearAll }: { chips: Chip[]; onClearAll: () => void }) {
  if (chips.length === 0) return null
  return (
    <div className="flex flex-wrap items-center gap-1.5">
      {chips.map((c) => (
        <button
          key={c.key}
          type="button"
          onClick={c.onClear}
          title={`Remove ${c.label} filter`}
          aria-label={`Remove ${c.label} filter ${c.value}`}
          className="group inline-flex items-center gap-1 rounded-full border border-[color:var(--border)] bg-[color:var(--bg-2)] py-0.5 pl-2 pr-1.5 text-[11px] transition-colors hover:border-[color:var(--accent)]"
        >
          <span className="text-[color:var(--text-3)]">{c.label}:</span>
          <span className="mono max-w-[12rem] truncate text-[color:var(--text)]">{c.value}</span>
          <X size={11} className="text-[color:var(--text-4)] group-hover:text-[color:var(--text)]" />
        </button>
      ))}
      <button
        type="button"
        onClick={onClearAll}
        className="ml-0.5 text-[11px] text-[color:var(--text-3)] underline underline-offset-2 hover:text-[color:var(--text)]"
      >
        Clear all
      </button>
    </div>
  )
}

// FilterField labels one control inside the filter sheet. The label is a plain
// div (not a <label>) because the controls carry their own aria-labels and the
// custom Select/MultiSelect aren't native form elements a label can target.
export function FilterField({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="flex flex-col gap-1.5">
      <div className="text-[11px] font-medium uppercase tracking-[0.06em] text-[color:var(--text-3)]">{label}</div>
      {children}
    </div>
  )
}
