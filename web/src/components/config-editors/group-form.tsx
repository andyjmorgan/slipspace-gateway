import { Button } from "@/components/ui/button"
import { PanelCard, PanelHead } from "@/components/atoms/card"
import { Tag } from "@/components/atoms/tag"
import { TextField, NumberField, SelectField, CheckboxField, StringListEditor } from "@/components/forms/field-atoms"
import { GROUP_MODE_OPTIONS, type GroupFormState, type TargetDraft } from "./group-form-model"

// GroupFormFields is the shared resilience-group form body — policy (mode,
// failure accounting, breaker) and the ordered/weighted target list.
export function GroupFormFields({
  value: form,
  onChange,
  nameEditable,
}: {
  value: GroupFormState
  onChange: (next: GroupFormState) => void
  nameEditable: boolean
}) {
  return (
    <>
      <PanelCard>
        <PanelHead title="Policy" sub="orchestration mode and failure accounting" />
        <div className="px-4 py-4 flex flex-col gap-3">
          <TextField
            label="Name"
            value={form.name}
            onChange={(v) => onChange({ ...form, name: v })}
            placeholder="qwen-pool"
            mono
            hint={nameEditable ? "Unique across groups. Referenced by configuration bindings." : "Names are immutable post-create."}
          />
          <SelectField label="Mode" value={form.mode} options={GROUP_MODE_OPTIONS} onChange={(m) => onChange({ ...form, mode: m })} />
          <StringListEditor
            label="Failure status codes"
            values={form.failureStatusCodes}
            onChange={(v) => onChange({ ...form, failureStatusCodes: v })}
            placeholder="503"
            addLabel="+ Add code"
            hint="Upstream statuses treated as failures. Empty = 5xx is a failure."
          />
          <div className="flex flex-col gap-2">
            <CheckboxField
              label="Circuit breaker enabled"
              checked={form.cbEnabled}
              onChange={(c) => onChange({ ...form, cbEnabled: c })}
              hint="Skip a dead backend across every group that includes it."
            />
            <CheckboxField
              label="Strict weights"
              checked={form.strictWeights}
              onChange={(c) => onChange({ ...form, strictWeights: c })}
              hint="load_balance only — first weighted pick is final (no re-roll on failure)."
            />
          </div>
          <NumberField
            label="Response-header timeout (seconds)"
            value={form.responseHeaderTimeoutSeconds}
            onChange={(n) => onChange({ ...form, responseHeaderTimeoutSeconds: n })}
            placeholder="(gateway default)"
            hint="Per-attempt override so this group fails over off a slow target faster."
          />
        </div>
      </PanelCard>

      <PanelCard>
        <PanelHead
          title="Targets"
          sub={`backends this group routes across · ${form.targets.length}`}
          action={
            <Button
              type="button"
              variant="outline"
              size="sm"
              onClick={() => onChange({ ...form, targets: [...form.targets, { backend: "", alias: "", path: "", weight: null }] })}
            >
              + Add target
            </Button>
          }
        />
        <div className="px-4 py-4 flex flex-col gap-3">
          {form.targets.length === 0 && (
            <div className="text-[12.5px] text-[color:var(--text-4)]">No targets — a group needs at least one to validate.</div>
          )}
          {form.targets.map((t, i) => (
            <TargetCard
              key={i}
              index={i}
              total={form.targets.length}
              draft={t}
              mode={form.mode}
              onChange={(next) => {
                const copy = form.targets.slice()
                copy[i] = next
                onChange({ ...form, targets: copy })
              }}
              onRemove={() => {
                const copy = form.targets.slice()
                copy.splice(i, 1)
                onChange({ ...form, targets: copy })
              }}
              onMoveUp={() => {
                if (i === 0) return
                const copy = form.targets.slice()
                ;[copy[i - 1], copy[i]] = [copy[i], copy[i - 1]]
                onChange({ ...form, targets: copy })
              }}
              onMoveDown={() => {
                if (i === form.targets.length - 1) return
                const copy = form.targets.slice()
                ;[copy[i], copy[i + 1]] = [copy[i + 1], copy[i]]
                onChange({ ...form, targets: copy })
              }}
            />
          ))}
          {form.mode === "failover" && form.targets.length > 1 && (
            <div className="text-[11px] text-[color:var(--text-4)]">
              Failover order follows the list order — top target is tried first.
            </div>
          )}
        </div>
      </PanelCard>
    </>
  )
}

function TargetCard({
  index,
  total,
  draft,
  mode,
  onChange,
  onRemove,
  onMoveUp,
  onMoveDown,
}: {
  index: number
  total: number
  draft: TargetDraft
  mode: string
  onChange: (next: TargetDraft) => void
  onRemove: () => void
  onMoveUp: () => void
  onMoveDown: () => void
}) {
  const weighted = mode !== "failover"
  return (
    <div className="rounded-[var(--radius)] border border-[color:var(--border)] overflow-hidden">
      <div className="px-3 py-2 border-b border-[color:var(--border)] bg-[color:var(--bg-2)] flex items-center gap-2">
        <span className="text-[11px] text-[color:var(--text-4)] mono w-5 text-right">{index + 1}</span>
        <Tag variant="default"><span className="mono">{draft.backend || "backend"}</span></Tag>
        <div className="ml-auto flex items-center gap-1">
          <Button type="button" size="xs" variant="ghost" onClick={onMoveUp} disabled={index === 0}>↑</Button>
          <Button type="button" size="xs" variant="ghost" onClick={onMoveDown} disabled={index === total - 1}>↓</Button>
          <Button type="button" size="xs" variant="ghost" onClick={onRemove} className="text-[color:var(--text-3)] hover:text-[color:var(--err)]">Remove</Button>
        </div>
      </div>
      <div className="px-3 py-3 grid grid-cols-1 sm:grid-cols-2 gap-3">
        <TextField label="Backend" value={draft.backend} onChange={(v) => onChange({ ...draft, backend: v })} placeholder="openai" mono />
        <TextField label="Alias (model rewrite)" value={draft.alias} onChange={(v) => onChange({ ...draft, alias: v })} placeholder="gpt-4o" mono hint="Rewrites the request model when this target is picked." />
        <TextField label="Path override" value={draft.path} onChange={(v) => onChange({ ...draft, path: v })} placeholder="" mono />
        {weighted && (
          <NumberField label="Weight" value={draft.weight} onChange={(n) => onChange({ ...draft, weight: n })} placeholder="1" hint="Relative selection weight. Zero = even." />
        )}
      </div>
    </div>
  )
}
