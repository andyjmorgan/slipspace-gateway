import { Button } from "@/components/ui/button"
import { PanelCard, PanelHead } from "@/components/atoms/card"
import { Tag } from "@/components/atoms/tag"
import { TextField, SelectField, KeyValueEditor, StringListEditor } from "@/components/forms/field-atoms"
import {
  CONFIG_PROTOCOL_OPTIONS,
  type BindingDraft,
  type ConfigFormState,
  type CredentialDraft,
} from "./configuration-form-model"

// ConfigurationFormFields is the shared configuration form body — identity +
// tags, per-backend credentials, bindings, passthrough bindings, and attached
// rules. The credential rows render a masked placeholder when a redacted
// existing secret is present (gateway), or an editable plaintext value
// otherwise (control plane).
export function ConfigurationFormFields({
  value: form,
  onChange,
  nameEditable,
}: {
  value: ConfigFormState
  onChange: (next: ConfigFormState) => void
  nameEditable: boolean
}) {
  return (
    <>
      {form.connectorBindings.length > 0 && (
        <div
          className="rounded-[var(--radius-lg)] border p-3 text-[12.5px]"
          style={{ color: "var(--warn)", background: "var(--warn-bg)", borderColor: "color-mix(in oklab, var(--warn) 30%, var(--border))" }}
        >
          <span className="font-semibold">
            This configuration has {form.connectorBindings.length} connector binding{form.connectorBindings.length === 1 ? "" : "s"}.
          </span>{" "}
          They are preserved on save but not yet editable here.
        </div>
      )}

      <PanelCard>
        <PanelHead title="Identity" sub="configuration name and telemetry tags" />
        <div className="px-4 py-4 flex flex-col gap-3">
          <TextField
            label="Name"
            value={form.name}
            onChange={(v) => onChange({ ...form, name: v })}
            placeholder="production"
            mono
            hint={nameEditable ? "Unique. Named by use-case (production, internal-dev). API keys resolve to this." : "Names are immutable post-create."}
          />
          <KeyValueEditor
            label="Tags"
            pairs={form.tags}
            onChange={(p) => onChange({ ...form, tags: p })}
            keyPlaceholder="client"
            valuePlaceholder="k3s-agentling"
            addLabel="+ Add tag"
            hint="Propagated to telemetry for every request under this configuration."
          />
        </div>
      </PanelCard>

      <PanelCard>
        <PanelHead
          title="Credentials"
          sub="per-backend upstream credential (managed mode). Leave a masked row untouched to keep it."
          action={
            <Button
              type="button"
              variant="outline"
              size="sm"
              onClick={() => onChange({ ...form, credentials: [...form.credentials, { backend: "", existing: null, value: "", dirty: true }] })}
            >
              + Add credential
            </Button>
          }
        />
        <div className="px-4 py-4 flex flex-col gap-3">
          {form.credentials.length === 0 && (
            <div className="text-[12.5px] text-[color:var(--text-4)]">No managed credentials — passthrough-only configuration.</div>
          )}
          {form.credentials.map((c, i) => (
            <CredentialRow
              key={i}
              draft={c}
              onChange={(next) => {
                const copy = form.credentials.slice()
                copy[i] = next
                onChange({ ...form, credentials: copy })
              }}
              onRemove={() => {
                const copy = form.credentials.slice()
                copy.splice(i, 1)
                onChange({ ...form, credentials: copy })
              }}
            />
          ))}
        </div>
      </PanelCard>

      <PanelCard>
        <PanelHead
          title="Bindings"
          sub="map (protocol, model) to a backend or group · evaluated in order, first match wins"
          action={
            <Button
              type="button"
              variant="outline"
              size="sm"
              onClick={() => onChange({ ...form, bindings: [...form.bindings, { protocol: "chat", models: [], destinationKind: "backend", destination: "", alias: "" }] })}
            >
              + Add binding
            </Button>
          }
        />
        <div className="px-4 py-4 flex flex-col gap-3">
          {form.bindings.length === 0 && (
            <div className="text-[12.5px] text-[color:var(--text-4)]">No bindings — this configuration routes no generative traffic.</div>
          )}
          {form.bindings.map((b, i) => (
            <BindingCard
              key={i}
              index={i}
              total={form.bindings.length}
              draft={b}
              onChange={(next) => {
                const copy = form.bindings.slice()
                copy[i] = next
                onChange({ ...form, bindings: copy })
              }}
              onRemove={() => {
                const copy = form.bindings.slice()
                copy.splice(i, 1)
                onChange({ ...form, bindings: copy })
              }}
              onMoveUp={() => {
                if (i === 0) return
                const copy = form.bindings.slice()
                ;[copy[i - 1], copy[i]] = [copy[i], copy[i - 1]]
                onChange({ ...form, bindings: copy })
              }}
              onMoveDown={() => {
                if (i === form.bindings.length - 1) return
                const copy = form.bindings.slice()
                ;[copy[i], copy[i + 1]] = [copy[i + 1], copy[i]]
                onChange({ ...form, bindings: copy })
              }}
            />
          ))}
        </div>
      </PanelCard>

      <PanelCard>
        <PanelHead
          title="Passthrough bindings"
          sub="expose a backend's passthrough family on this configuration"
          action={
            <Button
              type="button"
              variant="outline"
              size="sm"
              onClick={() => onChange({ ...form, passthroughBindings: [...form.passthroughBindings, { family: "", backend: "" }] })}
            >
              + Add passthrough
            </Button>
          }
        />
        <div className="px-4 py-4 flex flex-col gap-3">
          {form.passthroughBindings.length === 0 && (
            <div className="text-[12.5px] text-[color:var(--text-4)]">No passthrough bindings.</div>
          )}
          {form.passthroughBindings.map((p, i) => (
            <div key={i} className="rounded-[var(--radius)] border border-[color:var(--border)] px-3 py-3 grid grid-cols-1 sm:grid-cols-[1fr_1fr_auto] gap-3 items-end">
              <TextField label="Family" value={p.family} onChange={(v) => {
                const copy = form.passthroughBindings.slice()
                copy[i] = { ...copy[i], family: v }
                onChange({ ...form, passthroughBindings: copy })
              }} placeholder="message_batches" mono />
              <TextField label="Backend" value={p.backend} onChange={(v) => {
                const copy = form.passthroughBindings.slice()
                copy[i] = { ...copy[i], backend: v }
                onChange({ ...form, passthroughBindings: copy })
              }} placeholder="anthropic" mono />
              <Button type="button" variant="ghost" size="sm" className="text-[color:var(--text-3)] hover:text-[color:var(--err)]" onClick={() => {
                const copy = form.passthroughBindings.slice()
                copy.splice(i, 1)
                onChange({ ...form, passthroughBindings: copy })
              }}>Remove</Button>
            </div>
          ))}
        </div>
      </PanelCard>

      <PanelCard>
        <PanelHead title="Attached rules" sub="transform rules from the shared library, applied in order" />
        <div className="px-4 py-4">
          <StringListEditor
            label="Rule names"
            values={form.ruleNames}
            onChange={(v) => onChange({ ...form, ruleNames: v })}
            placeholder="force-openai-streaming-usage"
            addLabel="+ Attach rule"
            hint="Must match a rule in the shared rules library."
          />
        </div>
      </PanelCard>
    </>
  )
}

function CredentialRow({
  draft,
  onChange,
  onRemove,
}: {
  draft: CredentialDraft
  onChange: (next: CredentialDraft) => void
  onRemove: () => void
}) {
  const masked = draft.existing && !draft.dirty
  return (
    <div className="rounded-[var(--radius)] border border-[color:var(--border)] px-3 py-3 grid grid-cols-1 sm:grid-cols-[1fr_1.4fr_auto] gap-3 items-end">
      <TextField label="Backend" value={draft.backend} onChange={(v) => onChange({ ...draft, backend: v })} placeholder="openai" mono />
      <div className="flex flex-col gap-1.5">
        <label className="text-[10px] uppercase tracking-[0.07em] text-[color:var(--text-3)]">Credential</label>
        {masked ? (
          <div className="flex items-center gap-2">
            <span className="mono text-[12.5px] flex-1 px-2 py-1 rounded-[5px] border border-[color:var(--border)] bg-[color:var(--bg-2)] text-[color:var(--text-3)]">
              <span aria-hidden="true">••••</span>{draft.existing?.last4}
            </span>
            <button type="button" onClick={() => onChange({ ...draft, dirty: true, value: "" })} className="text-[11.5px] text-[color:var(--text-3)] hover:text-[color:var(--text)]">
              Change
            </button>
          </div>
        ) : (
          <div className="flex items-center gap-2">
            <input
              type="text"
              value={draft.value}
              onChange={(e) => onChange({ ...draft, value: e.target.value, dirty: true })}
              placeholder={draft.existing ? "new secret (blank = no-credential backend)" : "sk-… (blank = no-credential backend)"}
              className="mono flex-1 min-w-0 rounded-[5px] border border-[color:var(--border)] bg-[color:var(--bg-2)] px-2 py-1 text-[12.5px] text-[color:var(--text)] outline-none focus:border-[color:var(--text-3)]"
            />
            {draft.existing && (
              <button type="button" onClick={() => onChange({ ...draft, dirty: false, value: "" })} className="text-[11.5px] text-[color:var(--text-3)] hover:text-[color:var(--text)]">
                Keep existing
              </button>
            )}
          </div>
        )}
        <span className="text-[11px] text-[color:var(--text-4)]">
          {masked ? "Stored secret kept on save unless you change it." : "Typed value will be set. Leave blank for a no-credential backend."}
        </span>
      </div>
      <button type="button" aria-label="Remove credential" onClick={onRemove} className="text-[color:var(--text-3)] hover:text-[color:var(--err)] text-[12px] px-1 pb-2">
        ✕
      </button>
    </div>
  )
}

function BindingCard({
  index,
  total,
  draft,
  onChange,
  onRemove,
  onMoveUp,
  onMoveDown,
}: {
  index: number
  total: number
  draft: BindingDraft
  onChange: (next: BindingDraft) => void
  onRemove: () => void
  onMoveUp: () => void
  onMoveDown: () => void
}) {
  return (
    <div className="rounded-[var(--radius)] border border-[color:var(--border)] overflow-hidden">
      <div className="px-3 py-2 border-b border-[color:var(--border)] bg-[color:var(--bg-2)] flex items-center gap-2">
        <span className="text-[11px] text-[color:var(--text-4)] mono w-5 text-right">{index + 1}</span>
        <Tag variant="default"><span className="mono">{draft.protocol}</span></Tag>
        <Tag variant={draft.destinationKind === "group" ? "violet" : "default"}>
          <span className="mono">{draft.destinationKind} {draft.destination || "?"}</span>
        </Tag>
        <div className="ml-auto flex items-center gap-1">
          <Button type="button" size="xs" variant="ghost" onClick={onMoveUp} disabled={index === 0}>↑</Button>
          <Button type="button" size="xs" variant="ghost" onClick={onMoveDown} disabled={index === total - 1}>↓</Button>
          <Button type="button" size="xs" variant="ghost" onClick={onRemove} className="text-[color:var(--text-3)] hover:text-[color:var(--err)]">Remove</Button>
        </div>
      </div>
      <div className="px-3 py-3 flex flex-col gap-3">
        <div className="grid grid-cols-1 sm:grid-cols-3 gap-3">
          <SelectField label="Protocol" value={draft.protocol} options={CONFIG_PROTOCOL_OPTIONS} onChange={(v) => onChange({ ...draft, protocol: v })} />
          <SelectField
            label="Destination"
            value={draft.destinationKind}
            options={[{ value: "backend", label: "backend" }, { value: "group", label: "group" }]}
            onChange={(v) => onChange({ ...draft, destinationKind: v as "backend" | "group" })}
          />
          <TextField label={draft.destinationKind === "group" ? "Group name" : "Backend name"} value={draft.destination} onChange={(v) => onChange({ ...draft, destination: v })} placeholder={draft.destinationKind === "group" ? "qwen-pool" : "openai"} mono />
        </div>
        <StringListEditor
          label="Models"
          values={draft.models}
          onChange={(m) => onChange({ ...draft, models: m })}
          placeholder="claude-* (empty = catch-all for the protocol)"
          addLabel="+ Add model pattern"
          hint="Trailing-* wildcard. Empty matches any model for the protocol."
        />
        {draft.destinationKind === "backend" && (
          <TextField label="Alias (model rewrite)" value={draft.alias} onChange={(v) => onChange({ ...draft, alias: v })} placeholder="" mono hint="Rewrites the request model on the way to the backend." />
        )}
      </div>
    </div>
  )
}
