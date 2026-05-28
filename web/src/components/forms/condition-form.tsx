// Editable mirror of components/atoms/condition-view.tsx. Each
// concrete condition type has a sub-form; the top-level ConditionForm
// dispatches on the JSON `type` discriminator and renders the
// appropriate sub-form below a type-selector dropdown.
//
// Values flow as Record<string, unknown> — the same shape the read
// API returns and the write API accepts. The form never builds a
// typed model in the middle: read directly off the record, write a
// new record on every keystroke. This keeps round-trips byte-stable
// modulo whitespace.

import type { ReactNode } from "react"
import { Button } from "@/components/ui/button"
import {
  CheckboxField,
  FieldGrid,
  FieldRow,
  SelectField,
  TextField,
  type SelectOption,
} from "@/components/forms/field-atoms"

export type ConditionValue = Record<string, unknown>

const STRING_OPERATORS: SelectOption[] = [
  { value: "Equals" },
  { value: "StartsWith" },
  { value: "EndsWith" },
  { value: "Contains" },
  { value: "Regex" },
]

const ENUM_OPERATORS: SelectOption[] = [{ value: "Equals" }]

const LOGICAL_OPERATORS: SelectOption[] = [
  { value: "And" },
  { value: "Or" },
]

const BODY_FIELD_OPERATORS: SelectOption[] = [
  { value: "Exists" },
  { value: "Equals" },
  { value: "Contains" },
  { value: "Matches", label: "Matches (regex)" },
  { value: "Is", label: "Is (JSON type)" },
]

// CONDITION_TYPES is the canonical list surfaced in the type-selector
// dropdown. The order matches the most-common-first convention used
// in config-dev's hand-authored rules.
const CONDITION_TYPES: SelectOption[] = [
  { value: "provider", label: "provider" },
  { value: "endpoint", label: "endpoint" },
  { value: "modelName", label: "model name" },
  { value: "header", label: "header" },
  { value: "tag", label: "tag" },
  { value: "bodyField", label: "body field" },
  { value: "group", label: "group (and/or)" },
]

// defaultsFor returns a fresh empty Record for a chosen condition
// type. Switching types in the editor calls this so stale fields
// from the prior type don't leak into the wire payload.
function defaultsFor(type: string): ConditionValue {
  switch (type) {
    case "provider":
      return { type, operator: "Equals", expected_provider: "" }
    case "endpoint":
      return { type, operator: "Equals", expected_endpoint: "" }
    case "modelName":
      return { type, operator: "Equals", expected_model_name: "" }
    case "header":
      return { type, key_operator: "Equals", key_pattern: "" }
    case "tag":
      return { type, operator: "Equals", expected_tag: "" }
    case "bodyField":
      return { type, target: "", operator: "Exists" }
    case "group":
      return { type, logical_operator: "And", children: [] }
    default:
      return { type }
  }
}

// emptyCondition is the default seed when a new rule is created. A
// "match nothing" provider condition is a safe stand-in — the
// operator picks a real predicate in the same session.
export function emptyCondition(): ConditionValue {
  return defaultsFor("provider")
}

export function ConditionForm({
  value,
  onChange,
}: {
  value: ConditionValue
  onChange: (next: ConditionValue) => void
}) {
  const type = String(value?.type ?? "provider")

  return (
    <div className="flex flex-col gap-3 rounded-[var(--radius)] border border-[color:var(--border)] p-3">
      <FieldGrid>
        <SelectField
          label="Type"
          value={type}
          options={CONDITION_TYPES}
          onChange={(t) => onChange(defaultsFor(t))}
        />
        <NegatedField value={value} onChange={onChange} />
      </FieldGrid>
      <Subform value={value} onChange={onChange} />
    </div>
  )
}

function Subform({ value, onChange }: { value: ConditionValue; onChange: (v: ConditionValue) => void }) {
  const type = String(value?.type ?? "")
  switch (type) {
    case "provider":
      return <ProviderForm value={value} onChange={onChange} />
    case "endpoint":
      return <EndpointForm value={value} onChange={onChange} />
    case "modelName":
      return <ModelNameForm value={value} onChange={onChange} />
    case "header":
      return <HeaderForm value={value} onChange={onChange} />
    case "tag":
      return <TagForm value={value} onChange={onChange} />
    case "bodyField":
      return <BodyFieldForm value={value} onChange={onChange} />
    case "group":
      return <GroupForm value={value} onChange={onChange} />
    default:
      return (
        <FieldRow label="Unknown type">
          <span className="text-[12px] text-[color:var(--text-3)]">
            No form available for this condition type. Switch to JSON to edit.
          </span>
        </FieldRow>
      )
  }
}

function NegatedField({ value, onChange }: { value: ConditionValue; onChange: (v: ConditionValue) => void }) {
  return (
    <FieldRow label="Negate">
      <CheckboxField
        label="Invert the match result"
        checked={Boolean(value?.not)}
        onChange={(b) => onChange({ ...value, not: b })}
      />
    </FieldRow>
  )
}

function ProviderForm({ value, onChange }: { value: ConditionValue; onChange: (v: ConditionValue) => void }) {
  return (
    <FieldGrid>
      <SelectField
        label="Operator"
        value={String(value.operator ?? "Equals")}
        options={ENUM_OPERATORS}
        onChange={(op) => onChange({ ...value, operator: op })}
      />
      <TextField
        label="Expected provider"
        value={String(value.expected_provider ?? "")}
        onChange={(v) => onChange({ ...value, expected_provider: v })}
        placeholder="openai"
        mono
      />
    </FieldGrid>
  )
}

function EndpointForm({ value, onChange }: { value: ConditionValue; onChange: (v: ConditionValue) => void }) {
  return (
    <FieldGrid>
      <SelectField
        label="Operator"
        value={String(value.operator ?? "Equals")}
        options={ENUM_OPERATORS}
        onChange={(op) => onChange({ ...value, operator: op })}
      />
      <TextField
        label="Expected endpoint"
        value={String(value.expected_endpoint ?? "")}
        onChange={(v) => onChange({ ...value, expected_endpoint: v })}
        placeholder="chat_completions"
        mono
      />
    </FieldGrid>
  )
}

function ModelNameForm({ value, onChange }: { value: ConditionValue; onChange: (v: ConditionValue) => void }) {
  return (
    <>
      <FieldGrid>
        <SelectField
          label="Operator"
          value={String(value.operator ?? "Equals")}
          options={STRING_OPERATORS}
          onChange={(op) => onChange({ ...value, operator: op })}
        />
        <TextField
          label="Expected model name"
          value={String(value.expected_model_name ?? "")}
          onChange={(v) => onChange({ ...value, expected_model_name: v })}
          placeholder="claude-"
          mono
        />
      </FieldGrid>
      <CheckboxField
        label="Case-insensitive comparison"
        checked={Boolean(value.case_insensitive)}
        onChange={(b) => onChange({ ...value, case_insensitive: b })}
      />
    </>
  )
}

function HeaderForm({ value, onChange }: { value: ConditionValue; onChange: (v: ConditionValue) => void }) {
  // value_operator is optional in the wire shape (HeaderCondition
  // with no value_operator matches purely on the key). We expose it
  // as a "match value too?" toggle to keep the form simple.
  const hasValueOp = value.value_operator != null && value.value_operator !== ""
  return (
    <>
      <FieldGrid>
        <SelectField
          label="Key operator"
          value={String(value.key_operator ?? "Equals")}
          options={STRING_OPERATORS}
          onChange={(op) => onChange({ ...value, key_operator: op })}
        />
        <TextField
          label="Key pattern"
          value={String(value.key_pattern ?? "")}
          onChange={(v) => onChange({ ...value, key_pattern: v })}
          placeholder="x-api-key"
          mono
        />
      </FieldGrid>
      <CheckboxField
        label="Also match against the header value"
        checked={hasValueOp}
        onChange={(b) => {
          if (b) {
            onChange({ ...value, value_operator: "Equals", value_pattern: String(value.value_pattern ?? "") })
          } else {
            const next = { ...value }
            delete next.value_operator
            delete next.value_pattern
            onChange(next)
          }
        }}
      />
      {hasValueOp && (
        <FieldGrid>
          <SelectField
            label="Value operator"
            value={String(value.value_operator ?? "Equals")}
            options={STRING_OPERATORS}
            onChange={(op) => onChange({ ...value, value_operator: op })}
          />
          <TextField
            label="Value pattern"
            value={String(value.value_pattern ?? "")}
            onChange={(v) => onChange({ ...value, value_pattern: v })}
            mono
          />
        </FieldGrid>
      )}
      <CheckboxField
        label="Case-insensitive comparison"
        checked={Boolean(value.case_insensitive)}
        onChange={(b) => onChange({ ...value, case_insensitive: b })}
      />
    </>
  )
}

function TagForm({ value, onChange }: { value: ConditionValue; onChange: (v: ConditionValue) => void }) {
  return (
    <FieldGrid>
      <SelectField
        label="Operator"
        value={String(value.operator ?? "Equals")}
        options={ENUM_OPERATORS}
        onChange={(op) => onChange({ ...value, operator: op })}
      />
      <TextField
        label="Expected tag"
        value={String(value.expected_tag ?? "")}
        onChange={(v) => onChange({ ...value, expected_tag: v })}
        placeholder="claude-code"
        mono
      />
    </FieldGrid>
  )
}

function BodyFieldForm({ value, onChange }: { value: ConditionValue; onChange: (v: ConditionValue) => void }) {
  const op = String(value.operator ?? "Exists")
  const showValue = op !== "Exists"
  return (
    <>
      <FieldGrid>
        <TextField
          label="Target (sjson path)"
          value={String(value.target ?? "")}
          onChange={(v) => onChange({ ...value, target: v })}
          placeholder="request.body.stream"
          mono
        />
        <SelectField
          label="Operator"
          value={op}
          options={BODY_FIELD_OPERATORS}
          onChange={(o) => onChange({ ...value, operator: o })}
        />
      </FieldGrid>
      {showValue && (
        <TextField
          label="Expected value"
          value={String(value.value ?? "")}
          onChange={(v) => onChange({ ...value, value: v })}
          placeholder="true"
          mono
        />
      )}
    </>
  )
}

function GroupForm({ value, onChange }: { value: ConditionValue; onChange: (v: ConditionValue) => void }) {
  const children = Array.isArray(value.children) ? (value.children as ConditionValue[]) : []
  const update = (i: number, next: ConditionValue) => {
    const copy = children.slice()
    copy[i] = next
    onChange({ ...value, children: copy })
  }
  const remove = (i: number) => {
    const copy = children.slice()
    copy.splice(i, 1)
    onChange({ ...value, children: copy })
  }
  const add = () => {
    onChange({ ...value, children: [...children, emptyCondition()] })
  }
  return (
    <>
      <SelectField
        label="Logical operator"
        value={String(value.logical_operator ?? "And")}
        options={LOGICAL_OPERATORS}
        onChange={(op) => onChange({ ...value, logical_operator: op })}
      />
      <FieldRow label={`Children (${children.length})`}>
        <div className="flex flex-col gap-2 pl-3 border-l border-[color:var(--border)]">
          {children.length === 0 && (
            <span className="text-[11.5px] text-[color:var(--text-4)]">
              No children yet — empty group never matches.
            </span>
          )}
          {children.map((c, i) => (
            <ChildRow key={i} onRemove={() => remove(i)}>
              <ConditionForm value={c} onChange={(next) => update(i, next)} />
            </ChildRow>
          ))}
          <div>
            <Button type="button" variant="outline" size="sm" onClick={add}>
              + Add child
            </Button>
          </div>
        </div>
      </FieldRow>
    </>
  )
}

function ChildRow({ children, onRemove }: { children: ReactNode; onRemove: () => void }) {
  return (
    <div className="flex items-start gap-2">
      <div className="flex-1 min-w-0">{children}</div>
      <Button
        type="button"
        variant="ghost"
        size="sm"
        onClick={onRemove}
        className="text-[color:var(--text-3)] hover:text-[color:var(--err)]"
      >
        Remove
      </Button>
    </div>
  )
}
