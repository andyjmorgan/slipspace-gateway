// Shared backend editor form — the presentational fields + the form↔contract
// mappers, used by BOTH the gateway admin console and the control-plane
// console so the editing surface is identical. Each console supplies its own
// load + save (the gateway's typed write API + dry-run preview; the CP's
// stage-then-publish entity API); this module owns only the look and feel and
// the mapping to/from the on-the-wire contract shape (contracts/config.Backend).

import { Button } from "@/components/ui/button"
import { PanelCard, PanelHead } from "@/components/atoms/card"
import { Tag } from "@/components/atoms/tag"
import { TextField, KeyValueEditor, StringListEditor } from "@/components/forms/field-atoms"
import type {
  BackendFormState,
  ProtocolDraft,
  PassthroughFamilyDraft,
} from "./backend-form-model"

// BackendFormFields is the presentational form body — the connection,
// protocols, and passthrough panels. The hosting page owns the page header,
// error/preview banners, and the action buttons.
export function BackendFormFields({
  value,
  onChange,
  nameEditable,
}: {
  value: BackendFormState
  onChange: (next: BackendFormState) => void
  nameEditable: boolean
}) {
  const form = value
  return (
    <>
      <PanelCard>
        <PanelHead title="Connection" sub="base URL + transport defaults applied to every request" />
        <div className="px-4 py-4 flex flex-col gap-3">
          <TextField
            label="Name"
            value={form.name}
            onChange={(v) => onChange({ ...form, name: v })}
            placeholder="openai"
            mono
            hint={nameEditable ? "Unique across backends. Referenced by bindings, groups, and credentials." : "Names are immutable post-create."}
          />
          <TextField
            label="Base URL"
            value={form.baseUrl}
            onChange={(v) => onChange({ ...form, baseUrl: v })}
            placeholder="https://api.openai.com"
            mono
          />
          <KeyValueEditor
            label="Required headers"
            pairs={form.requiredHeaders}
            onChange={(p) => onChange({ ...form, requiredHeaders: p })}
            keyPlaceholder="anthropic-version"
            valuePlaceholder="2023-06-01"
            addLabel="+ Add header"
            hint="Injected on every forwarded request to this backend."
          />
          <KeyValueEditor
            label="Default query parameters"
            pairs={form.query}
            onChange={(p) => onChange({ ...form, query: p })}
            keyPlaceholder="api-version"
            valuePlaceholder="2024-02-01"
            addLabel="+ Add parameter"
            hint="Appended to every request (e.g. Azure's api-version)."
          />
        </div>
      </PanelCard>

      <PanelCard>
        <PanelHead
          title="Protocols"
          sub="generative wire shapes this backend serves; each with an upstream path + optional per-protocol auth override"
          action={
            <Button
              type="button"
              variant="outline"
              size="sm"
              onClick={() => onChange({ ...form, protocols: [...form.protocols, { name: "", path: "", authHeader: "", authFormat: "" }] })}
            >
              + Add protocol
            </Button>
          }
        />
        <div className="px-4 py-4 flex flex-col gap-3">
          {form.protocols.length === 0 && (
            <div className="text-[12.5px] text-[color:var(--text-4)]">No protocols — this backend serves no generative traffic.</div>
          )}
          {form.protocols.map((p, i) => (
            <ProtocolCard
              key={i}
              draft={p}
              onChange={(next) => {
                const copy = form.protocols.slice()
                copy[i] = next
                onChange({ ...form, protocols: copy })
              }}
              onRemove={() => {
                const copy = form.protocols.slice()
                copy.splice(i, 1)
                onChange({ ...form, protocols: copy })
              }}
            />
          ))}
        </div>
      </PanelCard>

      <PanelCard>
        <PanelHead
          title="Passthrough families"
          sub="opaque endpoint families proxied verbatim (e.g. message batches)"
          action={
            <Button
              type="button"
              variant="outline"
              size="sm"
              onClick={() => onChange({ ...form, passthrough: [...form.passthrough, { name: "", authHeader: "", paths: [] }] })}
            >
              + Add family
            </Button>
          }
        />
        <div className="px-4 py-4 flex flex-col gap-3">
          {form.passthrough.length === 0 && (
            <div className="text-[12.5px] text-[color:var(--text-4)]">No passthrough families.</div>
          )}
          {form.passthrough.map((f, i) => (
            <PassthroughCard
              key={i}
              draft={f}
              onChange={(next) => {
                const copy = form.passthrough.slice()
                copy[i] = next
                onChange({ ...form, passthrough: copy })
              }}
              onRemove={() => {
                const copy = form.passthrough.slice()
                copy.splice(i, 1)
                onChange({ ...form, passthrough: copy })
              }}
            />
          ))}
        </div>
      </PanelCard>
    </>
  )
}

function ProtocolCard({ draft, onChange, onRemove }: { draft: ProtocolDraft; onChange: (next: ProtocolDraft) => void; onRemove: () => void }) {
  return (
    <div className="rounded-[var(--radius)] border border-[color:var(--border)] overflow-hidden">
      <div className="px-3 py-2 border-b border-[color:var(--border)] bg-[color:var(--bg-2)] flex items-center gap-2">
        <Tag variant="default"><span className="mono">{draft.name || "protocol"}</span></Tag>
        <button type="button" onClick={onRemove} className="ml-auto text-[11.5px] text-[color:var(--text-3)] hover:text-[color:var(--err)]">
          Remove
        </button>
      </div>
      <div className="px-3 py-3 grid grid-cols-1 sm:grid-cols-2 gap-3">
        <TextField label="Protocol name" value={draft.name} onChange={(v) => onChange({ ...draft, name: v })} placeholder="chat" mono />
        <TextField label="Upstream path" value={draft.path} onChange={(v) => onChange({ ...draft, path: v })} placeholder="/v1/chat/completions" mono />
        <TextField label="Auth header (override)" value={draft.authHeader} onChange={(v) => onChange({ ...draft, authHeader: v })} placeholder="Authorization" mono hint="Leave blank to use the provider-native default." />
        <TextField label="Auth format (override)" value={draft.authFormat} onChange={(v) => onChange({ ...draft, authFormat: v })} placeholder="Bearer {key}" mono />
      </div>
    </div>
  )
}

function PassthroughCard({ draft, onChange, onRemove }: { draft: PassthroughFamilyDraft; onChange: (next: PassthroughFamilyDraft) => void; onRemove: () => void }) {
  return (
    <div className="rounded-[var(--radius)] border border-[color:var(--border)] overflow-hidden">
      <div className="px-3 py-2 border-b border-[color:var(--border)] bg-[color:var(--bg-2)] flex items-center gap-2">
        <Tag variant="violet"><span className="mono">{draft.name || "family"}</span></Tag>
        <button type="button" onClick={onRemove} className="ml-auto text-[11.5px] text-[color:var(--text-3)] hover:text-[color:var(--err)]">
          Remove
        </button>
      </div>
      <div className="px-3 py-3 flex flex-col gap-3">
        <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
          <TextField label="Family name" value={draft.name} onChange={(v) => onChange({ ...draft, name: v })} placeholder="message_batches" mono />
          <TextField label="Auth header (override)" value={draft.authHeader} onChange={(v) => onChange({ ...draft, authHeader: v })} placeholder="x-api-key" mono />
        </div>
        <div className="flex flex-col gap-2">
          {draft.paths.map((pp, i) => (
            <div key={i} className="rounded-[var(--radius)] border border-[color:var(--border)] px-3 py-2 flex flex-col gap-2">
              <div className="flex items-center gap-2">
                <span className="text-[10px] uppercase tracking-[0.07em] text-[color:var(--text-4)]">path</span>
                <button
                  type="button"
                  onClick={() => {
                    const copy = draft.paths.slice()
                    copy.splice(i, 1)
                    onChange({ ...draft, paths: copy })
                  }}
                  className="ml-auto text-[11px] text-[color:var(--text-3)] hover:text-[color:var(--err)]"
                >
                  Remove path
                </button>
              </div>
              <TextField
                label="Match"
                value={pp.match}
                onChange={(v) => {
                  const copy = draft.paths.slice()
                  copy[i] = { ...copy[i], match: v }
                  onChange({ ...draft, paths: copy })
                }}
                placeholder="/v1/messages/batches"
                mono
              />
              <StringListEditor
                label="Methods"
                values={pp.methods}
                onChange={(m) => {
                  const copy = draft.paths.slice()
                  copy[i] = { ...copy[i], methods: m }
                  onChange({ ...draft, paths: copy })
                }}
                placeholder="POST"
                addLabel="+ Add method"
              />
            </div>
          ))}
          <button
            type="button"
            onClick={() => onChange({ ...draft, paths: [...draft.paths, { match: "", methods: [] }] })}
            className="self-start text-[11.5px] text-[color:var(--text-3)] hover:text-[color:var(--text)]"
          >
            + Add path
          </button>
        </div>
      </div>
    </div>
  )
}
