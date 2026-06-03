import { PanelCard, PanelHead } from "@/components/atoms/card"
import { TextField, CheckboxField } from "@/components/forms/field-atoms"
import type { ApiKeyFormState } from "./api-key-form-model"

// ApiKeyFormFields is the shared API-key form body.
export function ApiKeyFormFields({
  value: form,
  onChange,
  nameEditable,
}: {
  value: ApiKeyFormState
  onChange: (next: ApiKeyFormState) => void
  nameEditable: boolean
}) {
  return (
    <PanelCard>
      <PanelHead title="API key" sub="a credential clients present, resolving to a configuration" />
      <div className="px-4 py-4 flex flex-col gap-3">
        <TextField
          label="Name"
          value={form.name}
          onChange={(v) => onChange({ ...form, name: v })}
          placeholder="k3s-agentling"
          mono
          hint={nameEditable ? "Operator-visible label, and the entity's key." : "Names are immutable post-create."}
        />
        <TextField
          label="Secret"
          value={form.secret}
          onChange={(v) => onChange({ ...form, secret: v })}
          placeholder="sk_live_…"
          mono
          hint="The bearer token clients present. Stored in the config; masked on read."
        />
        <TextField
          label="Configuration"
          value={form.configuration}
          onChange={(v) => onChange({ ...form, configuration: v })}
          placeholder="production"
          mono
          hint="The configuration this key resolves to."
        />
        <CheckboxField
          label="Enabled"
          checked={form.enabled}
          onChange={(c) => onChange({ ...form, enabled: c })}
          hint="A disabled key authenticates structurally but is rejected before forwarding."
        />
      </div>
    </PanelCard>
  )
}
