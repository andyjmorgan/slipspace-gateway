// Rule editor model — shared by both consoles. The form is the rule's
// identity + a single condition + an ordered action list, built on the shared
// ConditionForm / ActionForm. The CP entity body and the gateway write body are
// both the contract Rule.

import { emptyCondition, type ConditionValue } from "@/components/forms/condition-form"
import { type ActionValue } from "@/components/forms/action-form"
import type { SelectOption } from "@/components/forms/field-atoms"

export const RULE_BEHAVIOR_OPTIONS: SelectOption[] = [
  { value: "continue", label: "continue (default — evaluate next rule)" },
  { value: "exit", label: "exit (stop after this rule's actions)" },
]

export interface RuleFormState {
  name: string
  behavior: string
  condition: ConditionValue
  actions: ActionValue[]
}

export interface RuleContract {
  name: string
  behavior: string
  condition: ConditionValue
  actions: ActionValue[]
}

export function emptyRuleForm(): RuleFormState {
  return { name: "", behavior: "continue", condition: emptyCondition(), actions: [] }
}

export function ruleFormFromContract(name: string, body: Record<string, unknown>): RuleFormState {
  return {
    name,
    behavior: typeof body.behavior === "string" ? body.behavior : "continue",
    condition: (body.condition as ConditionValue) ?? emptyCondition(),
    actions: Array.isArray(body.actions) ? (body.actions as ActionValue[]) : [],
  }
}

export function ruleFormToContract(form: RuleFormState): RuleContract {
  return {
    name: form.name.trim(),
    behavior: form.behavior,
    condition: form.condition,
    actions: form.actions,
  }
}
