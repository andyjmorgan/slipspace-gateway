// View shapes + body→view mappers for the shared rule list/detail views.
// Kept out of rule-views.tsx so that module only exports components
// (react-refresh/only-export-components) — same split as the backend views.
// The gateway feeds these from its live read API (RuleSummary / RuleDetail,
// which carry server-derived condition_summary / action_types / used_by); the
// control-plane console maps a staged entity's RuleContract body into them via
// the functions here, deriving the same summaries client-side. A staged body
// has no usage graph, so used_by is empty for the CP.

import type { RuleContract } from "@/components/config-editors/rule-form-model"

// RuleListItem is one row of the rules table. Structurally identical to the
// gateway's RuleSummary so the live API can pass straight through.
export interface RuleListItem {
  name: string
  condition_summary: string
  action_types: string[]
  behavior?: string
  used_by: string[]
}

// RuleDetailData is the full rule as the detail view renders it. Mirrors the
// gateway's RuleDetail — the condition + actions are polymorphic and rendered
// as-is by ConditionView / ActionView.
export interface RuleDetailData {
  name: string
  behavior?: string
  condition?: Record<string, unknown>
  actions?: Record<string, unknown>[]
  used_by: string[]
}

// ruleListItemFromContract derives a list row from a stored rule body —
// the condition collapses to a one-line summary and the actions to a type
// list, mirroring the gateway's server-side summariseCondition /
// summariseActionTypes.
export function ruleListItemFromContract(name: string, body: RuleContract | Record<string, unknown>): RuleListItem {
  const b = body as Record<string, unknown>
  return {
    name,
    condition_summary: summariseCondition(b.condition as Record<string, unknown> | undefined),
    action_types: summariseActionTypes(b.actions),
    behavior: typeof b.behavior === "string" ? b.behavior : undefined,
    used_by: [],
  }
}

// ruleDetailFromContract expands a stored rule body into the detail view shape.
// The condition + actions pass through untouched for the polymorphic renderers.
export function ruleDetailFromContract(name: string, body: RuleContract | Record<string, unknown>): RuleDetailData {
  const b = body as Record<string, unknown>
  return {
    name,
    behavior: typeof b.behavior === "string" ? b.behavior : undefined,
    condition: (b.condition as Record<string, unknown> | undefined) ?? undefined,
    actions: Array.isArray(b.actions) ? (b.actions as Record<string, unknown>[]) : [],
    used_by: [],
  }
}

// summariseCondition mirrors internal/admin/config_dto.go::summariseCondition —
// a one-line description of the condition tree dispatched on the discriminator.
function summariseCondition(c: Record<string, unknown> | undefined): string {
  if (!c) return "(no condition)"
  const t = String(c.type ?? "")
  switch (t) {
    case "provider":
      return `provider ${op(c.operator)} ${String(c.expected_provider ?? "")}${notSuffix(c.not)}`
    case "endpoint":
      return `endpoint ${op(c.operator)} ${String(c.expected_endpoint ?? "")}${notSuffix(c.not)}`
    case "modelName":
      return `model ${op(c.operator)} ${String(c.expected_model_name ?? "")}${notSuffix(c.not)}`
    case "header": {
      let base = `header ${op(c.key_operator)} ${String(c.key_pattern ?? "")}`
      const valueOp = c.value_operator as string | undefined
      if (valueOp) {
        base += ` · value ${op(valueOp)} ${String(c.value_pattern ?? "")}`
      }
      return base + notSuffix(c.not)
    }
    case "group": {
      const children = (c.children as unknown[] | undefined) ?? []
      return `group (${String(c.logical_operator ?? "And")} · ${pluralChildren(children.length)})${notSuffix(c.not)}`
    }
    default:
      return t || "(no condition)"
  }
}

// summariseActionTypes mirrors the server's summariseActionTypes — the ordered
// list of action discriminators, skipping nil entries.
function summariseActionTypes(actions: unknown): string[] {
  if (!Array.isArray(actions)) return []
  const out: string[] = []
  for (const a of actions) {
    if (a && typeof a === "object") {
      const t = (a as Record<string, unknown>).type
      if (typeof t === "string") out.push(t)
    }
  }
  return out
}

function op(v: unknown): string {
  const s = typeof v === "string" ? v : ""
  return s === "" ? "Equals" : s
}

function notSuffix(not: unknown): string {
  return not ? " (negated)" : ""
}

function pluralChildren(n: number): string {
  return n === 1 ? "1 child" : `${n} children`
}
