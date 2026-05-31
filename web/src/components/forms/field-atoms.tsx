// Shared form input atoms styled to match the read-only FieldPill /
// PanelCard look used elsewhere in the admin SPA. Every atom is a
// label + control pair laid out as a column so the editor's polymorphic
// dispatch can stack any combination of fields without bespoke CSS.

import type { ReactNode } from "react"
import { cn } from "@/lib/utils"
import type { KVPair } from "./field-helpers"

// FieldRow lays out a label above its control, with optional hint text
// below and an error slot that reuses the page-level --err variable.
// The label is rendered uppercase / small to mirror FieldPill's caption
// style, so a form sitting next to a read-only ConditionView reads
// as a coherent surface.
export function FieldRow({
  label,
  hint,
  error,
  children,
  className,
}: {
  label: string
  hint?: string
  error?: string
  children: ReactNode
  className?: string
}) {
  return (
    <div className={cn("flex flex-col gap-1.5", className)}>
      <label className="text-[10px] uppercase tracking-[0.07em] text-[color:var(--text-3)]">
        {label}
      </label>
      {children}
      {hint && !error && (
        <span className="text-[11px] text-[color:var(--text-4)]">{hint}</span>
      )}
      {error && (
        <span className="text-[11px]" style={{ color: "var(--err)" }}>
          {error}
        </span>
      )}
    </div>
  )
}

// inputClassName centralises the visual treatment so TextField,
// SelectField, and TextareaField all share the same border, padding,
// and focus ring.
const inputClassName =
  "rounded-[5px] border border-[color:var(--border)] bg-[color:var(--bg-2)] px-2 py-1 text-[12.5px] text-[color:var(--text)] outline-none focus:border-[color:var(--text-3)]"

export function TextField({
  label,
  value,
  onChange,
  placeholder,
  hint,
  error,
  mono,
  className,
}: {
  label: string
  value: string
  onChange: (next: string) => void
  placeholder?: string
  hint?: string
  error?: string
  mono?: boolean
  className?: string
}) {
  return (
    <FieldRow label={label} hint={hint} error={error} className={className}>
      <input
        type="text"
        value={value}
        placeholder={placeholder}
        onChange={(e) => onChange(e.target.value)}
        className={cn(inputClassName, mono && "mono")}
      />
    </FieldRow>
  )
}

export function NumberField({
  label,
  value,
  onChange,
  placeholder,
  hint,
  error,
  className,
}: {
  label: string
  value: number | null
  onChange: (next: number | null) => void
  placeholder?: string
  hint?: string
  error?: string
  className?: string
}) {
  return (
    <FieldRow label={label} hint={hint} error={error} className={className}>
      <input
        type="number"
        value={value ?? ""}
        placeholder={placeholder}
        onChange={(e) => {
          const v = e.target.value
          if (v === "") return onChange(null)
          const n = Number(v)
          onChange(Number.isFinite(n) ? n : null)
        }}
        className={cn(inputClassName, "mono")}
      />
    </FieldRow>
  )
}

export function TextareaField({
  label,
  value,
  onChange,
  placeholder,
  hint,
  error,
  rows = 4,
  mono = true,
  className,
}: {
  label: string
  value: string
  onChange: (next: string) => void
  placeholder?: string
  hint?: string
  error?: string
  rows?: number
  mono?: boolean
  className?: string
}) {
  return (
    <FieldRow label={label} hint={hint} error={error} className={className}>
      <textarea
        value={value}
        placeholder={placeholder}
        rows={rows}
        onChange={(e) => onChange(e.target.value)}
        className={cn(inputClassName, "resize-y", mono && "mono")}
      />
    </FieldRow>
  )
}

export type SelectOption = {
  value: string
  label?: string
}

export function SelectField({
  label,
  value,
  onChange,
  options,
  hint,
  error,
  className,
}: {
  label: string
  value: string
  onChange: (next: string) => void
  options: SelectOption[]
  hint?: string
  error?: string
  className?: string
}) {
  return (
    <FieldRow label={label} hint={hint} error={error} className={className}>
      <select
        value={value}
        onChange={(e) => onChange(e.target.value)}
        className={cn(inputClassName, "pr-6")}
      >
        {options.map((opt) => (
          <option key={opt.value} value={opt.value}>
            {opt.label ?? opt.value}
          </option>
        ))}
      </select>
    </FieldRow>
  )
}

export function CheckboxField({
  label,
  checked,
  onChange,
  hint,
  className,
}: {
  label: string
  checked: boolean
  onChange: (next: boolean) => void
  hint?: string
  className?: string
}) {
  return (
    <label className={cn("inline-flex items-center gap-2 cursor-pointer select-none", className)}>
      <input
        type="checkbox"
        checked={checked}
        onChange={(e) => onChange(e.target.checked)}
        className="cursor-pointer"
      />
      <span className="text-[12.5px] text-[color:var(--text)]">{label}</span>
      {hint && (
        <span className="text-[11px] text-[color:var(--text-4)]">{hint}</span>
      )}
    </label>
  )
}

// FieldGrid lays its children out in a responsive 1-or-2 column grid
// so a sub-form like `setHeader` (op + name + value) renders compactly
// on a wide editor and stacks on a narrow one.
export function FieldGrid({ children, className }: { children: ReactNode; className?: string }) {
  return (
    <div className={cn("grid grid-cols-1 sm:grid-cols-2 gap-3", className)}>
      {children}
    </div>
  )
}

// KeyValueEditor edits an ordered list of key/value pairs that serialises
// to a Record<string,string> (required_headers, query, tags). The internal
// shape is a pair array so an empty key while typing does not collapse a
// row; the caller maps it back to a record on submit.
export function KeyValueEditor({
  label,
  pairs,
  onChange,
  keyPlaceholder = "key",
  valuePlaceholder = "value",
  addLabel = "+ Add row",
  hint,
}: {
  label: string
  pairs: KVPair[]
  onChange: (next: KVPair[]) => void
  keyPlaceholder?: string
  valuePlaceholder?: string
  addLabel?: string
  hint?: string
}) {
  return (
    <FieldRow label={label} hint={pairs.length === 0 ? hint : undefined}>
      <div className="flex flex-col gap-2">
        {pairs.map((p, i) => (
          <div key={i} className="flex items-center gap-2">
            <input
              type="text"
              value={p.key}
              placeholder={keyPlaceholder}
              onChange={(e) => {
                const copy = pairs.slice()
                copy[i] = { ...copy[i], key: e.target.value }
                onChange(copy)
              }}
              className={cn(inputClassName, "mono flex-1 min-w-0")}
            />
            <input
              type="text"
              value={p.value}
              placeholder={valuePlaceholder}
              onChange={(e) => {
                const copy = pairs.slice()
                copy[i] = { ...copy[i], value: e.target.value }
                onChange(copy)
              }}
              className={cn(inputClassName, "mono flex-1 min-w-0")}
            />
            <button
              type="button"
              aria-label="Remove row"
              onClick={() => {
                const copy = pairs.slice()
                copy.splice(i, 1)
                onChange(copy)
              }}
              className="text-[color:var(--text-3)] hover:text-[color:var(--err)] text-[12px] px-1"
            >
              ✕
            </button>
          </div>
        ))}
        <button
          type="button"
          onClick={() => onChange([...pairs, { key: "", value: "" }])}
          className="self-start text-[11.5px] text-[color:var(--text-3)] hover:text-[color:var(--text)]"
        >
          {addLabel}
        </button>
      </div>
    </FieldRow>
  )
}

// StringListEditor edits an ordered list of single strings (model patterns,
// tags, methods, failure status codes as text). Empty trailing entries are
// dropped on read via stringsFromList (see field-helpers.ts).
export function StringListEditor({
  label,
  values,
  onChange,
  placeholder,
  addLabel = "+ Add",
  hint,
  mono = true,
}: {
  label: string
  values: string[]
  onChange: (next: string[]) => void
  placeholder?: string
  addLabel?: string
  hint?: string
  mono?: boolean
}) {
  return (
    <FieldRow label={label} hint={values.length === 0 ? hint : undefined}>
      <div className="flex flex-col gap-2">
        {values.map((v, i) => (
          <div key={i} className="flex items-center gap-2">
            <input
              type="text"
              value={v}
              placeholder={placeholder}
              onChange={(e) => {
                const copy = values.slice()
                copy[i] = e.target.value
                onChange(copy)
              }}
              className={cn(inputClassName, "flex-1 min-w-0", mono && "mono")}
            />
            <button
              type="button"
              aria-label="Remove entry"
              onClick={() => {
                const copy = values.slice()
                copy.splice(i, 1)
                onChange(copy)
              }}
              className="text-[color:var(--text-3)] hover:text-[color:var(--err)] text-[12px] px-1"
            >
              ✕
            </button>
          </div>
        ))}
        <button
          type="button"
          onClick={() => onChange([...values, ""])}
          className="self-start text-[11.5px] text-[color:var(--text-3)] hover:text-[color:var(--text)]"
        >
          {addLabel}
        </button>
      </div>
    </FieldRow>
  )
}
