import { Segmented } from "@/components/atoms/segmented"
import { cn } from "@/lib/utils"
import type { EventFilters, RangePreset, StatusClass } from "../lib/event-filters"

const RANGE_OPTIONS: { value: RangePreset; label: string }[] = [
  { value: "1h", label: "1h" },
  { value: "24h", label: "24h" },
  { value: "7d", label: "7d" },
  { value: "custom", label: "Custom" },
]

const STATUS_OPTIONS: { value: StatusClass; label: string }[] = [
  { value: "all", label: "All" },
  { value: "2xx", label: "2xx" },
  { value: "4xx", label: "4xx" },
  { value: "5xx", label: "5xx" },
]

const inputClass =
  "rounded-[5px] border border-[color:var(--border)] bg-[color:var(--bg-2)] px-2 py-1 text-[12.5px] text-[color:var(--text)] outline-none focus:border-[color:var(--text-3)]"

function TextFilter({
  label,
  value,
  onChange,
  placeholder,
}: {
  label: string
  value: string
  onChange: (v: string) => void
  placeholder?: string
}) {
  return (
    <label className="flex flex-col gap-1 min-w-0">
      <span className="text-[10px] uppercase tracking-[0.07em] text-[color:var(--text-3)]">{label}</span>
      <input
        type="text"
        value={value}
        placeholder={placeholder}
        onChange={(e) => onChange(e.target.value)}
        className={cn(inputClass, "mono w-full")}
      />
    </label>
  )
}

// EventFilterBar is the compact filter row above the observability table:
// time range (preset + custom from/to), free-text dimension filters, and a
// status-class band selector. It is fully controlled — it owns no state;
// the page holds `filters` and re-queries on Apply. `onApply` fires on the
// Apply button and on Enter in any text field so the keyboard path works.
export function EventFilterBar({
  filters,
  onChange,
  onApply,
  onReset,
  busy,
}: {
  filters: EventFilters
  onChange: (next: EventFilters) => void
  onApply: () => void
  onReset: () => void
  busy?: boolean
}) {
  const set = <K extends keyof EventFilters>(key: K, value: EventFilters[K]) =>
    onChange({ ...filters, [key]: value })

  return (
    <form
      onSubmit={(e) => {
        e.preventDefault()
        onApply()
      }}
      className="mb-4 rounded-[var(--radius-lg)] border border-[color:var(--border)] bg-[color:var(--bg-1)] p-3 flex flex-col gap-3"
    >
      <div className="flex flex-wrap items-center gap-3">
        <div className="flex flex-col gap-1">
          <span className="text-[10px] uppercase tracking-[0.07em] text-[color:var(--text-3)]">Range</span>
          <Segmented value={filters.range} onChange={(v) => set("range", v)} options={RANGE_OPTIONS} />
        </div>
        {filters.range === "custom" && (
          <>
            <label className="flex flex-col gap-1">
              <span className="text-[10px] uppercase tracking-[0.07em] text-[color:var(--text-3)]">From</span>
              <input
                type="datetime-local"
                value={filters.from}
                onChange={(e) => set("from", e.target.value)}
                className={cn(inputClass, "mono")}
              />
            </label>
            <label className="flex flex-col gap-1">
              <span className="text-[10px] uppercase tracking-[0.07em] text-[color:var(--text-3)]">To</span>
              <input
                type="datetime-local"
                value={filters.to}
                onChange={(e) => set("to", e.target.value)}
                className={cn(inputClass, "mono")}
              />
            </label>
          </>
        )}
        <div className="flex flex-col gap-1">
          <span className="text-[10px] uppercase tracking-[0.07em] text-[color:var(--text-3)]">Status</span>
          <Segmented value={filters.statusClass} onChange={(v) => set("statusClass", v)} options={STATUS_OPTIONS} />
        </div>
      </div>

      <div className="grid grid-cols-2 sm:grid-cols-3 lg:grid-cols-5 gap-3">
        <TextFilter label="Configuration" value={filters.configuration} onChange={(v) => set("configuration", v)} placeholder="any" />
        <TextFilter label="Gateway" value={filters.gateway} onChange={(v) => set("gateway", v)} placeholder="any" />
        <TextFilter label="Model" value={filters.model} onChange={(v) => set("model", v)} placeholder="any" />
        <TextFilter label="Backend" value={filters.backend} onChange={(v) => set("backend", v)} placeholder="any" />
        <TextFilter label="Protocol" value={filters.protocol} onChange={(v) => set("protocol", v)} placeholder="any" />
      </div>

      <div className="flex items-center gap-2">
        <button
          type="submit"
          disabled={busy}
          className="rounded-[5px] border border-[color:var(--border)] bg-[color:var(--bg-3)] px-3 py-1.5 text-[12px] font-medium text-[color:var(--text)] hover:bg-[color:var(--hover)] disabled:opacity-50"
        >
          {busy ? "Loading…" : "Apply"}
        </button>
        <button
          type="button"
          onClick={onReset}
          disabled={busy}
          className="rounded-[5px] px-3 py-1.5 text-[12px] text-[color:var(--text-3)] hover:text-[color:var(--text)] disabled:opacity-50"
        >
          Reset
        </button>
      </div>
    </form>
  )
}
