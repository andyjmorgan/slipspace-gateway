import { Button } from "@/components/ui/button"
import { PanelCard, PanelHead } from "@/components/atoms/card"
import { Tag } from "@/components/atoms/tag"
import { TextField, SelectField } from "@/components/forms/field-atoms"
import { ConditionForm } from "@/components/forms/condition-form"
import { ActionForm, emptyAction, type ActionValue } from "@/components/forms/action-form"
import { RULE_BEHAVIOR_OPTIONS, type RuleFormState } from "./rule-form-model"

// RuleFormFields is the shared rule form body — identity + behavior, the
// condition predicate, and the ordered action list. Built on the shared
// ConditionForm / ActionForm.
export function RuleFormFields({
  value: form,
  onChange,
  nameEditable,
}: {
  value: RuleFormState
  onChange: (next: RuleFormState) => void
  nameEditable: boolean
}) {
  return (
    <>
      <PanelCard>
        <PanelHead title="Identity" sub="rule name and post-match behavior" />
        <div className="px-4 py-4 flex flex-col gap-3 md:flex-row md:gap-4">
          <TextField
            label="Name"
            value={form.name}
            onChange={(v) => onChange({ ...form, name: v })}
            placeholder="tag-claude-code"
            mono
            hint={nameEditable ? "Lowercase + hyphens recommended. Must be unique across the rules library." : "Names are immutable post-create."}
            className="flex-1"
          />
          <SelectField
            label="Behavior"
            value={form.behavior}
            options={RULE_BEHAVIOR_OPTIONS}
            onChange={(b) => onChange({ ...form, behavior: b })}
            className="md:w-[28ch]"
          />
        </div>
      </PanelCard>

      <PanelCard>
        <PanelHead title="Condition" sub="the predicate that must hold for the actions to run" />
        <div className="px-4 py-4">
          <ConditionForm value={form.condition} onChange={(c) => onChange({ ...form, condition: c })} />
        </div>
      </PanelCard>

      <PanelCard>
        <PanelHead
          title="Actions"
          sub={`${form.actions.length} action${form.actions.length === 1 ? "" : "s"} · run in order when the condition matches`}
          action={
            <Button
              type="button"
              variant="outline"
              size="sm"
              onClick={() => onChange({ ...form, actions: [...form.actions, emptyAction()] })}
            >
              + Add action
            </Button>
          }
        />
        <div className="px-4 py-4 flex flex-col gap-4">
          {form.actions.length === 0 && (
            <div className="text-[12.5px] text-[color:var(--text-4)]">No actions yet — a rule with no actions is a no-op.</div>
          )}
          {form.actions.map((a, i) => (
            <ActionRow
              key={i}
              index={i}
              total={form.actions.length}
              action={a}
              onChange={(next) => {
                const copy = form.actions.slice()
                copy[i] = next
                onChange({ ...form, actions: copy })
              }}
              onRemove={() => {
                const copy = form.actions.slice()
                copy.splice(i, 1)
                onChange({ ...form, actions: copy })
              }}
              onMoveUp={() => {
                if (i === 0) return
                const copy = form.actions.slice()
                ;[copy[i - 1], copy[i]] = [copy[i], copy[i - 1]]
                onChange({ ...form, actions: copy })
              }}
              onMoveDown={() => {
                if (i === form.actions.length - 1) return
                const copy = form.actions.slice()
                ;[copy[i], copy[i + 1]] = [copy[i + 1], copy[i]]
                onChange({ ...form, actions: copy })
              }}
            />
          ))}
        </div>
      </PanelCard>
    </>
  )
}

function ActionRow({
  index,
  total,
  action,
  onChange,
  onRemove,
  onMoveUp,
  onMoveDown,
}: {
  index: number
  total: number
  action: ActionValue
  onChange: (next: ActionValue) => void
  onRemove: () => void
  onMoveUp: () => void
  onMoveDown: () => void
}) {
  return (
    <div className="rounded-[var(--radius)] border border-[color:var(--border)] overflow-hidden">
      <div className="px-3 py-2 border-b border-[color:var(--border)] bg-[color:var(--bg-2)] flex items-center gap-2">
        <span className="text-[11px] text-[color:var(--text-4)] mono w-5 text-right">{index + 1}</span>
        <Tag variant="ghost"><span className="mono">{String(action.type ?? "?")}</span></Tag>
        <div className="ml-auto flex items-center gap-1">
          <Button type="button" size="xs" variant="ghost" onClick={onMoveUp} disabled={index === 0}>↑</Button>
          <Button type="button" size="xs" variant="ghost" onClick={onMoveDown} disabled={index === total - 1}>↓</Button>
          <Button type="button" size="xs" variant="ghost" onClick={onRemove} className="text-[color:var(--text-3)] hover:text-[color:var(--err)]">Remove</Button>
        </div>
      </div>
      <div className="px-3 py-3">
        <ActionForm value={action} onChange={onChange} />
      </div>
    </div>
  )
}
