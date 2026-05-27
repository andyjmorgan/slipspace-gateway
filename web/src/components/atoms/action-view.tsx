// Structured renderer for a rule's action list. One numbered card per
// action, dispatched on the JSON `type` discriminator. Mirrors the
// "THEN" half of the prototype: an ordered list of actions, each with
// a labelled set of parameters laid out as field pills.

import { ProviderChip } from "@/components/atoms/provider-chip"
import { Tag } from "@/components/atoms/tag"
import { FieldPill } from "@/components/atoms/condition-view"

export type RawAction = Record<string, unknown>

export function ActionView({ action, index }: { action: RawAction; index: number }) {
  const t = String(action.type ?? "")
  return (
    <div className="rounded-[var(--radius)] border border-[color:var(--border)] overflow-hidden">
      <div className="px-3 py-2 border-b border-[color:var(--border)] bg-[color:var(--bg-2)] flex items-center gap-2">
        <span className="text-[11px] text-[color:var(--text-4)] mono w-5 text-right">{index + 1}</span>
        <span className="mono text-[12px] font-semibold">{t || "?"}</span>
        <ActionKindBadge type={t} />
      </div>
      <div className="px-3 py-2.5">
        <ActionFields action={action} />
      </div>
    </div>
  )
}

function ActionFields({ action }: { action: RawAction }) {
  const t = String(action.type ?? "")
  switch (t) {
    case "changeProvider":
      return (
        <div className="flex items-center gap-2 flex-wrap">
          <FieldPill label="new provider">
            <ProviderChip name={String(action.new_provider ?? "")} />
          </FieldPill>
        </div>
      )
    case "changeModelName":
      return (
        <div className="flex items-center gap-2 flex-wrap">
          <FieldPill label="new model"><span className="mono">{String(action.new_model_name ?? "")}</span></FieldPill>
        </div>
      )
    case "changeUrl":
      return (
        <div className="flex items-center gap-2 flex-wrap">
          <FieldPill label="new URL"><span className="mono break-all">{String(action.new_url ?? "")}</span></FieldPill>
        </div>
      )
    case "changeApiKey":
      // The wire shape carries the key as plaintext today (operator-
      // authored YAML), so we mask it visually here rather than echoing
      // it as a normal pill — same convention as the API-key reveal flow.
      return (
        <div className="flex items-center gap-2 flex-wrap">
          <FieldPill label="new key"><span className="mono">••••</span></FieldPill>
          <span className="text-[11px] text-[color:var(--text-4)]">masked — see policy.yaml for the literal value</span>
        </div>
      )
    case "setHeader":
      return (
        <div className="flex items-center gap-2 flex-wrap">
          <FieldPill label="op">{String(action.header_action ?? "Set")}</FieldPill>
          <FieldPill label="name"><span className="mono">{String(action.header_name ?? "")}</span></FieldPill>
          {action.header_value != null ? (
            <FieldPill label="value"><span className="mono">{String(action.header_value)}</span></FieldPill>
          ) : null}
        </div>
      )
    case "appendQueryString":
      return (
        <div className="flex items-center gap-2 flex-wrap">
          <FieldPill label="name"><span className="mono">{String(action.parameter_name ?? "")}</span></FieldPill>
          <FieldPill label="value"><span className="mono">{String(action.parameter_value ?? "")}</span></FieldPill>
        </div>
      )
    case "returnStatusCode":
      return (
        <div className="flex items-center gap-2 flex-wrap">
          <FieldPill label="status"><span className="mono">{String(action.status_code ?? "")}</span></FieldPill>
          {action.body_type != null ? (
            <FieldPill label="body type"><span className="mono">{String(action.body_type)}</span></FieldPill>
          ) : null}
          {action.body != null ? (
            <details className="text-[11px] text-[color:var(--text-3)] mt-1 w-full">
              <summary className="cursor-pointer">body</summary>
              <pre className="mono text-[11px] mt-1 overflow-x-auto bg-[color:var(--bg-0)] p-2 rounded">{String(action.body)}</pre>
            </details>
          ) : null}
        </div>
      )
    case "llmImpersonation":
      return (
        <div className="flex items-center gap-2 flex-wrap">
          <FieldPill label="model"><span className="mono">{String(action.model ?? "")}</span></FieldPill>
          {action.message != null ? (
            <FieldPill label="message"><span className="mono">{String(action.message)}</span></FieldPill>
          ) : null}
        </div>
      )
    case "useResiliencePolicy":
      return (
        <div className="flex items-center gap-2 flex-wrap">
          <FieldPill label="policy"><span className="mono">{String(action.policyName ?? "")}</span></FieldPill>
        </div>
      )
    case "addTag":
      return (
        <div className="flex items-center gap-2 flex-wrap">
          <FieldPill label="tag"><span className="mono">{String(action.tag ?? "")}</span></FieldPill>
        </div>
      )
    case "rewriteField":
    case "appendField":
      return (
        <div className="flex items-center gap-2 flex-wrap">
          <FieldPill label="target"><span className="mono">{String(action.target ?? "")}</span></FieldPill>
          <FieldPill label="value"><span className="mono break-all">{formatRewriteValue(action.value)}</span></FieldPill>
        </div>
      )
    case "removeField":
      return (
        <div className="flex items-center gap-2 flex-wrap">
          <FieldPill label="target"><span className="mono">{String(action.target ?? "")}</span></FieldPill>
        </div>
      )
    default:
      return (
        <div className="flex items-center gap-2 flex-wrap">
          <Tag variant="warn">unrecognised action type</Tag>
          <details className="text-[11px] text-[color:var(--text-3)] w-full">
            <summary className="cursor-pointer">raw</summary>
            <pre className="mono text-[11px] mt-1 overflow-x-auto bg-[color:var(--bg-0)] p-2 rounded">{JSON.stringify(action, null, 2)}</pre>
          </details>
        </div>
      )
  }
}

// ActionKindBadge tints the action card header by category: the
// destination-rewriting actions (change*) are violet to match the
// rules-list chip style, the synthesising actions are warn-coloured
// because they short-circuit the pipeline, the rest are default.
function ActionKindBadge({ type }: { type: string }) {
  if (type.startsWith("change")) return <Tag variant="violet">rewrite</Tag>
  if (type === "returnStatusCode" || type === "llmImpersonation") return <Tag variant="warn">terminating</Tag>
  if (type === "setHeader" || type === "appendQueryString") return <Tag variant="default">augment</Tag>
  if (type === "useResiliencePolicy") return <Tag variant="violet">resilience</Tag>
  if (type === "addTag") return <Tag variant="default">tag</Tag>
  if (type === "rewriteField" || type === "removeField" || type === "appendField") return <Tag variant="violet">body</Tag>
  return null
}

// formatRewriteValue renders a rewriteField/appendField value in its
// wire form: template strings show as-is, every other JSON type
// (number, bool, null, array, object) is stringified so the operator
// sees exactly what lands on the body.
function formatRewriteValue(v: unknown): string {
  if (v === undefined) return ""
  if (typeof v === "string") return v
  try {
    return JSON.stringify(v)
  } catch {
    return String(v)
  }
}
