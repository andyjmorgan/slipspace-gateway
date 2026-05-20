// Structured renderer for a rule's condition tree. Mirrors the
// polymorphic Condition contract in contracts/rules/condition.go —
// dispatches on the JSON discriminator (`type`) and renders one row
// per simple condition or a nested block per RuleGroup.
//
// Receives the condition as a Record<string, unknown> because the
// /admin/api/v1/config/rules/{name} response marshals concrete
// condition types through their MarshalJSON. The shape is stable but
// untyped on the SPA side; this component is the only consumer that
// reaches into it.

import { Tag } from "@/components/atoms/tag"
import { ProviderChip } from "@/components/atoms/provider-chip"

export type RawCondition = Record<string, unknown> | undefined

export function ConditionView({ condition }: { condition: RawCondition }) {
  if (!condition) return <Empty />
  const t = String(condition.type ?? "")
  switch (t) {
    case "provider":
      return <ProviderConditionView c={condition} />
    case "endpoint":
      return <EndpointConditionView c={condition} />
    case "modelName":
      return <ModelNameConditionView c={condition} />
    case "header":
      return <HeaderConditionView c={condition} />
    case "group":
      return <GroupConditionView c={condition} />
    default:
      return <UnknownConditionView c={condition} />
  }
}

function ProviderConditionView({ c }: { c: Record<string, unknown> }) {
  return (
    <ConditionRow label="WHEN">
      <FieldPill label="property">provider</FieldPill>
      <FieldPill label="op">{String(c.operator ?? "Equals")}</FieldPill>
      <FieldPill label="value">
        <ProviderChip name={String(c.expected_provider ?? "")} />
      </FieldPill>
      <NegatedFlag not={c.not} />
    </ConditionRow>
  )
}

function EndpointConditionView({ c }: { c: Record<string, unknown> }) {
  return (
    <ConditionRow label="WHEN">
      <FieldPill label="property">endpoint</FieldPill>
      <FieldPill label="op">{String(c.operator ?? "Equals")}</FieldPill>
      <FieldPill label="value"><span className="mono">{String(c.expected_endpoint ?? "")}</span></FieldPill>
      <NegatedFlag not={c.not} />
    </ConditionRow>
  )
}

function ModelNameConditionView({ c }: { c: Record<string, unknown> }) {
  return (
    <ConditionRow label="WHEN">
      <FieldPill label="property">model</FieldPill>
      <FieldPill label="op">{String(c.operator ?? "Equals")}</FieldPill>
      <FieldPill label="value"><span className="mono">{String(c.expected_model_name ?? "")}</span></FieldPill>
      {c.case_insensitive ? <Tag variant="ghost">case-insensitive</Tag> : null}
      <NegatedFlag not={c.not} />
    </ConditionRow>
  )
}

function HeaderConditionView({ c }: { c: Record<string, unknown> }) {
  const valueOp = c.value_operator as string | undefined
  return (
    <ConditionRow label="WHEN">
      <FieldPill label="property">header</FieldPill>
      <FieldPill label="key op">{String(c.key_operator ?? "Equals")}</FieldPill>
      <FieldPill label="key"><span className="mono">{String(c.key_pattern ?? "")}</span></FieldPill>
      {valueOp ? (
        <>
          <FieldPill label="value op">{valueOp}</FieldPill>
          <FieldPill label="value"><span className="mono">{String(c.value_pattern ?? "")}</span></FieldPill>
        </>
      ) : null}
      {c.case_insensitive ? <Tag variant="ghost">case-insensitive</Tag> : null}
      <NegatedFlag not={c.not} />
    </ConditionRow>
  )
}

function GroupConditionView({ c }: { c: Record<string, unknown> }) {
  const op = String(c.logical_operator ?? "And")
  const children = (c.children as Record<string, unknown>[] | undefined) ?? []
  return (
    <div className="rounded-[var(--radius)] border border-[color:var(--border)] px-3 py-2.5">
      <div className="flex items-center gap-2 mb-2">
        <span className="text-[10px] uppercase tracking-[0.07em] text-[color:var(--text-3)]">group</span>
        <Tag variant={op === "And" ? "default" : "violet"}><span className="mono">{op}</span></Tag>
        <span className="text-[11px] text-[color:var(--text-4)]">
          {children.length} {children.length === 1 ? "child" : "children"}
        </span>
        <NegatedFlag not={c.not} />
      </div>
      <div className="flex flex-col gap-2 pl-3 border-l border-[color:var(--border)]">
        {children.map((child, i) => (
          <ConditionView key={i} condition={child} />
        ))}
      </div>
    </div>
  )
}

function UnknownConditionView({ c }: { c: Record<string, unknown> }) {
  // Round-trip safety net for forward-compat: render the raw JSON
  // when the SPA doesn't know this discriminator yet so the data
  // is still visible to the operator.
  return (
    <ConditionRow label="WHEN">
      <FieldPill label="type"><span className="mono">{String(c.type ?? "unknown")}</span></FieldPill>
      <Tag variant="warn">unrecognised condition type</Tag>
      <details className="text-[11px] text-[color:var(--text-3)]">
        <summary className="cursor-pointer">raw</summary>
        <pre className="mono text-[11px] mt-1 overflow-x-auto">{JSON.stringify(c, null, 2)}</pre>
      </details>
    </ConditionRow>
  )
}

function Empty() {
  return (
    <div className="px-4 py-6 text-[12.5px] text-[color:var(--text-4)]">No condition — rule never matches.</div>
  )
}

// ConditionRow lays out the WHEN-style row in the prototype: a label
// gutter on the left + a sequence of field pills.
function ConditionRow({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="flex items-start gap-3">
      <span className="text-[10px] uppercase tracking-[0.07em] text-[color:var(--text-3)] mt-1.5 w-12 shrink-0">
        {label}
      </span>
      <div className="flex items-center gap-2 flex-wrap min-h-[28px]">{children}</div>
    </div>
  )
}

// FieldPill renders one "label: value" cluster styled like a dropdown
// at rest — the same visual the editable prototype used, minus the
// open-on-click affordance.
export function FieldPill({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="inline-flex items-center gap-1.5 rounded-[5px] border border-[color:var(--border)] bg-[color:var(--bg-2)] px-2 py-1 text-[12px]">
      <span className="text-[10px] uppercase tracking-[0.07em] text-[color:var(--text-4)]">{label}</span>
      <span className="text-[color:var(--text)]">{children}</span>
    </div>
  )
}

function NegatedFlag({ not }: { not: unknown }) {
  if (!not) return null
  return <Tag variant="warn">negated</Tag>
}
