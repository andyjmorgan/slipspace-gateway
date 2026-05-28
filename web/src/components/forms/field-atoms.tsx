// Shared form input atoms styled to match the read-only FieldPill /
// PanelCard look used elsewhere in the admin SPA. Every atom is a
// label + control pair laid out as a column so the editor's polymorphic
// dispatch can stack any combination of fields without bespoke CSS.

import type { ReactNode } from "react"
import { cn } from "@/lib/utils"

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
