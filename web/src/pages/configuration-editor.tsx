// ConfigurationEditorPage handles creating (/configurations/new) and editing
// (/configurations/:name/edit) a configuration. Configurations are
// high-blast-radius (api_keys resolve to them), so the editor runs a dry-run
// preview before save and delete is type-to-confirm.
//
// Credentials are starred + write-back-if-delivered. On GET each credential
// arrives redacted ({length, last4}); the editor shows ••••last4 as a
// placeholder and submits the field as `null` (keep existing) UNLESS the
// operator types a new value. A real string sets it; "" is a no-credential
// provider. Submitting the masked placeholder back would wipe the secret —
// so an untouched credential always submits null.

import { useEffect, useState } from "react"
import { Link, useNavigate, useParams } from "react-router"
import { Button } from "@/components/ui/button"
import { Tabs, TabsList, TabsTrigger, TabsContent } from "@/components/ui/tabs"
import { PanelCard, PanelHead } from "@/components/atoms/card"
import { Tag } from "@/components/atoms/tag"
import {
  PageHeader,
  LoadingPanel,
  ErrorPanel,
  NotFoundPanel,
  useUnauthorizedRedirect,
} from "@/components/atoms/page-states"
import {
  TextField,
  SelectField,
  KeyValueEditor,
  StringListEditor,
  type SelectOption,
} from "@/components/forms/field-atoms"
import {
  recordFromPairs,
  pairsFromRecord,
  stringsFromList,
  type KVPair,
} from "@/components/forms/field-helpers"
import { ErrorBanner, PreviewBanner } from "@/components/forms/write-atoms"
import { classifyWriteError, type EditorError } from "@/lib/write-error"
import {
  createConfiguration,
  replaceConfiguration,
  previewConfiguration,
  useConfiguration,
  type ConfigurationDetail,
  type ConfigurationWriteBody,
  type RedactedSecret,
  type BindingRow,
  type PassthroughBindingRow,
  type ConnectorBinding,
  type PreviewResult,
} from "@/lib/config-api"
import { APIError, UnauthorizedError } from "@/lib/api"

const PROTOCOL_OPTIONS: SelectOption[] = [
  { value: "chat", label: "chat" },
  { value: "responses", label: "responses" },
  { value: "messages", label: "messages" },
  { value: "generate_content", label: "generate_content" },
  { value: "embeddings", label: "embeddings" },
]

// CredentialDraft models one per-provider credential row. `existing` carries the
// redacted GET projection (when editing a pre-existing credential); `value` is
// the operator's typed input; `dirty` flips true the moment they touch the
// field. An undirty existing credential submits null (keep stored secret).
type CredentialDraft = {
  provider: string
  existing: RedactedSecret | null
  value: string
  dirty: boolean
}

type BindingDraft = {
  protocol: string
  models: string[]
  destinationKind: "provider" | "group"
  destination: string
  alias: string
}

type PassthroughBindingDraft = {
  family: string
  provider: string
}

type ConfigFormState = {
  name: string
  credentials: CredentialDraft[]
  bindings: BindingDraft[]
  passthroughBindings: PassthroughBindingDraft[]
  ruleNames: string[]
  tags: KVPair[]
  // connectorBindings are not editable here yet; carried verbatim from GET to
  // PUT so a full-replace save preserves them (no data loss).
  connectorBindings: ConnectorBinding[]
}

function emptyForm(): ConfigFormState {
  return {
    name: "",
    credentials: [],
    bindings: [],
    passthroughBindings: [],
    ruleNames: [],
    tags: [],
    connectorBindings: [],
  }
}

function formFromDetail(d: ConfigurationDetail): ConfigFormState {
  return {
    name: d.name,
    credentials: Object.entries(d.credentials ?? {}).map(([provider, redacted]) => ({
      provider,
      existing: redacted,
      value: "",
      dirty: false,
    })),
    bindings: d.bindings.map((b) => ({
      protocol: b.protocol,
      models: (b.models ?? []).slice(),
      destinationKind: b.group ? "group" : "provider",
      destination: b.group ?? b.provider ?? "",
      alias: b.alias ?? "",
    })),
    passthroughBindings: d.passthrough_bindings.map((p) => ({ family: p.family, provider: p.provider })),
    ruleNames: d.rules.map((r) => r.name),
    tags: pairsFromRecord(d.tags),
    connectorBindings: (d.connector_bindings ?? []).slice(),
  }
}

function toWriteBody(form: ConfigFormState): ConfigurationWriteBody {
  const credentials: Record<string, string | null> = {}
  for (const c of form.credentials) {
    const provider = c.provider.trim()
    if (provider === "") continue
    if (c.existing && !c.dirty) {
      // Untouched stored secret — null keeps it (never round-trip the mask).
      credentials[provider] = null
    } else {
      // New row, or operator typed a new value (including "" for no-credential).
      credentials[provider] = c.value
    }
  }

  const bindings: BindingRow[] = form.bindings
    .filter((b) => b.destination.trim() !== "")
    .map((b) => {
      const row: BindingRow = {
        protocol: b.protocol,
        models: stringsFromList(b.models),
      }
      if (b.destinationKind === "group") {
        row.group = b.destination.trim()
      } else {
        row.provider = b.destination.trim()
        if (b.alias.trim()) row.alias = b.alias.trim()
      }
      return row
    })

  const passthrough: PassthroughBindingRow[] = form.passthroughBindings
    .filter((p) => p.family.trim() !== "" && p.provider.trim() !== "")
    .map((p) => ({ family: p.family.trim(), provider: p.provider.trim() }))

  const body: ConfigurationWriteBody = {
    name: form.name.trim(),
  }
  if (Object.keys(credentials).length > 0) body.credentials = credentials
  if (bindings.length > 0) body.bindings = bindings
  if (passthrough.length > 0) body.passthrough_bindings = passthrough
  const rules = stringsFromList(form.ruleNames)
  if (rules.length > 0) body.rule_names = rules
  const tags = recordFromPairs(form.tags)
  if (Object.keys(tags).length > 0) body.tags = tags
  // Round-trip connector bindings unchanged so a full-replace PUT preserves them.
  if (form.connectorBindings.length > 0) body.connector_bindings = form.connectorBindings
  return body
}

export function ConfigurationEditorPage({ mode }: { mode: "create" | "edit" }) {
  if (mode === "create") return <CreateConfigPage />
  return <EditConfigPage />
}

function CreateConfigPage() {
  const nav = useNavigate()
  const [form, setForm] = useState<ConfigFormState>(emptyForm)
  return (
    <EditorBody
      title="New configuration"
      sub="A reusable policy bundle — per-provider credentials, bindings that route models to providers, attached transform rules, and tags."
      form={form}
      setForm={setForm}
      urlName={null}
      onSaved={(c) => nav(`/configurations/${encodeURIComponent(c.name)}`)}
      cancelTo="/configurations"
      nameEditable
      submitLabel="Create configuration"
    />
  )
}

function EditConfigPage() {
  const { name } = useParams<{ name: string }>()
  const nav = useNavigate()
  const { state } = useConfiguration(name)
  useUnauthorizedRedirect(state)
  const [form, setForm] = useState<ConfigFormState | null>(null)

  useEffect(() => {
    if (state.status === "ok" && form === null) {
      // eslint-disable-next-line react-hooks/set-state-in-effect
      setForm(formFromDetail(state.data))
    }
  }, [state, form])

  if (state.status === "loading") return <Wrap title={name ?? "Configuration"}><LoadingPanel /></Wrap>
  if (state.status === "error") return <Wrap title={name ?? "Configuration"}><ErrorPanel message={state.message} /></Wrap>
  if (state.status === "not_found") return <Wrap title={name ?? "Configuration"}><NotFoundPanel kind="configuration" name={name} /></Wrap>
  if (state.status !== "ok" || form === null) return null

  const configName = state.data.name
  return (
    <EditorBody
      title={`Edit configuration · ${configName}`}
      sub="Replace this configuration's credentials, bindings, rules, and tags. The name is fixed — api keys reference it by name."
      form={form}
      setForm={setForm}
      urlName={configName}
      onSaved={(c) => nav(`/configurations/${encodeURIComponent(c.name)}`)}
      cancelTo={`/configurations/${encodeURIComponent(configName)}`}
      nameEditable={false}
      submitLabel="Save changes"
    />
  )
}

function EditorBody({
  title,
  sub,
  form,
  setForm,
  urlName,
  onSaved,
  cancelTo,
  nameEditable,
  submitLabel,
}: {
  title: string
  sub: string
  form: ConfigFormState
  setForm: (next: ConfigFormState) => void
  urlName: string | null
  onSaved: (c: ConfigurationDetail) => void
  cancelTo: string
  nameEditable: boolean
  submitLabel: string
}) {
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<EditorError | null>(null)
  const [preview, setPreview] = useState<PreviewResult | null>(null)

  const handleError = (e: unknown) => {
    if (e instanceof UnauthorizedError) {
      setError({ kind: "generic", message: "Session expired — log in again." })
    } else if (e instanceof APIError) {
      setError(classifyWriteError(e))
    } else {
      setError({ kind: "generic", message: e instanceof Error ? e.message : String(e) })
    }
  }

  const runPreview = async () => {
    setBusy(true)
    setError(null)
    setPreview(null)
    try {
      setPreview(await previewConfiguration(urlName, toWriteBody(form)))
    } catch (e) {
      handleError(e)
    } finally {
      setBusy(false)
    }
  }

  const handleSubmit = async () => {
    setBusy(true)
    setError(null)
    try {
      const body = toWriteBody(form)
      const c = urlName ? await replaceConfiguration(urlName, body) : await createConfiguration(body)
      onSaved(c)
    } catch (e) {
      handleError(e)
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="flex flex-col gap-4">
      <PageHeader
        title={title}
        sub={sub}
        action={
          <Link to={cancelTo} className="text-[12.5px] text-[color:var(--text-3)] hover:underline">
            ← cancel
          </Link>
        }
      />

      {error && <ErrorBanner error={error} />}
      {preview && <PreviewBanner result={preview} onDismiss={() => setPreview(null)} />}

      {urlName && form.connectorBindings.length > 0 && (
        <div
          className="rounded-[var(--radius-lg)] border p-3 text-[12.5px]"
          style={{
            color: "var(--warn)",
            background: "var(--warn-bg)",
            borderColor: "color-mix(in oklab, var(--warn) 30%, var(--border))",
          }}
        >
          <span className="font-semibold">
            This configuration has {form.connectorBindings.length} connector binding
            {form.connectorBindings.length === 1 ? "" : "s"}.
          </span>{" "}
          They are preserved on save but not yet editable here — edit them in YAML
          (<span className="mono">policy.yaml</span>) until this editor grows a connector-bindings panel.
        </div>
      )}

      <Tabs defaultValue="identity" className="gap-4">
        <TabsList variant="line" className="flex-wrap">
          <TabsTrigger value="identity">Identity</TabsTrigger>
          <TabsTrigger value="credentials">
            Credentials{form.credentials.length > 0 ? ` · ${form.credentials.length}` : ""}
          </TabsTrigger>
          <TabsTrigger value="bindings">
            Bindings{form.bindings.length + form.passthroughBindings.length > 0 ? ` · ${form.bindings.length + form.passthroughBindings.length}` : ""}
          </TabsTrigger>
          <TabsTrigger value="rules">
            Rules{form.ruleNames.length > 0 ? ` · ${form.ruleNames.length}` : ""}
          </TabsTrigger>
        </TabsList>

        <TabsContent value="identity">
          <PanelCard>
            <PanelHead title="Identity" sub="configuration name and telemetry tags" />
            <div className="px-4 py-4 flex flex-col gap-3">
              <TextField
                label="Name"
                value={form.name}
                onChange={(v) => setForm({ ...form, name: v })}
                placeholder="production"
                mono
                hint={nameEditable ? "Unique. Named by use-case (production, internal-dev). API keys resolve to this." : "Names are immutable post-create."}
              />
              <KeyValueEditor
                label="Tags"
                pairs={form.tags}
                onChange={(p) => setForm({ ...form, tags: p })}
                keyPlaceholder="client"
                valuePlaceholder="k3s-agentling"
                addLabel="+ Add tag"
                hint="Propagated to telemetry for every request under this configuration."
              />
            </div>
          </PanelCard>
        </TabsContent>

        <TabsContent value="credentials">
          <PanelCard>
            <PanelHead
              title="Credentials"
              sub="per-provider upstream credential (managed mode). Existing secrets are masked — leave a row untouched to keep it."
              action={
                <Button
                  type="button"
                  variant="outline"
                  size="sm"
                  onClick={() => setForm({ ...form, credentials: [...form.credentials, { provider: "", existing: null, value: "", dirty: true }] })}
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
                    setForm({ ...form, credentials: copy })
                  }}
                  onRemove={() => {
                    const copy = form.credentials.slice()
                    copy.splice(i, 1)
                    setForm({ ...form, credentials: copy })
                  }}
                />
              ))}
            </div>
          </PanelCard>
        </TabsContent>

        <TabsContent value="bindings" className="flex flex-col gap-4">
          <PanelCard>
            <PanelHead
              title="Bindings"
              sub="map (protocol, model) to a provider or group · evaluated in order, first match wins"
              action={
                <Button
                  type="button"
                  variant="outline"
                  size="sm"
                  onClick={() => setForm({ ...form, bindings: [...form.bindings, { protocol: "chat", models: [], destinationKind: "provider", destination: "", alias: "" }] })}
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
                    setForm({ ...form, bindings: copy })
                  }}
                  onRemove={() => {
                    const copy = form.bindings.slice()
                    copy.splice(i, 1)
                    setForm({ ...form, bindings: copy })
                  }}
                  onMoveUp={() => {
                    if (i === 0) return
                    const copy = form.bindings.slice()
                    ;[copy[i - 1], copy[i]] = [copy[i], copy[i - 1]]
                    setForm({ ...form, bindings: copy })
                  }}
                  onMoveDown={() => {
                    if (i === form.bindings.length - 1) return
                    const copy = form.bindings.slice()
                    ;[copy[i], copy[i + 1]] = [copy[i + 1], copy[i]]
                    setForm({ ...form, bindings: copy })
                  }}
                />
              ))}
            </div>
          </PanelCard>

          <PanelCard>
            <PanelHead
              title="Passthrough bindings"
              sub="expose a provider's passthrough family on this configuration"
              action={
                <Button
                  type="button"
                  variant="outline"
                  size="sm"
                  onClick={() => setForm({ ...form, passthroughBindings: [...form.passthroughBindings, { family: "", provider: "" }] })}
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
                    setForm({ ...form, passthroughBindings: copy })
                  }} placeholder="message_batches" mono />
                  <TextField label="Provider" value={p.provider} onChange={(v) => {
                    const copy = form.passthroughBindings.slice()
                    copy[i] = { ...copy[i], provider: v }
                    setForm({ ...form, passthroughBindings: copy })
                  }} placeholder="anthropic" mono />
                  <Button type="button" variant="ghost" size="sm" className="text-[color:var(--text-3)] hover:text-[color:var(--err)]" onClick={() => {
                    const copy = form.passthroughBindings.slice()
                    copy.splice(i, 1)
                    setForm({ ...form, passthroughBindings: copy })
                  }}>Remove</Button>
                </div>
              ))}
            </div>
          </PanelCard>
        </TabsContent>

        <TabsContent value="rules">
          <PanelCard>
            <PanelHead title="Attached rules" sub="transform rules from the shared library, applied in order" />
            <div className="px-4 py-4">
              <StringListEditor
                label="Rule names"
                values={form.ruleNames}
                onChange={(v) => setForm({ ...form, ruleNames: v })}
                placeholder="force-openai-streaming-usage"
                addLabel="+ Attach rule"
                hint="Must match a rule in the shared rules library."
              />
            </div>
          </PanelCard>
        </TabsContent>
      </Tabs>

      <div className="flex items-center gap-2 justify-end">
        <Link to={cancelTo}>
          <Button type="button" variant="ghost">Cancel</Button>
        </Link>
        <Button type="button" variant="outline" onClick={runPreview} disabled={busy || form.name.trim() === ""}>
          {busy ? "…" : "Preview"}
        </Button>
        <Button type="button" onClick={handleSubmit} disabled={busy || form.name.trim() === ""}>
          {busy ? "Saving…" : submitLabel}
        </Button>
      </div>
    </div>
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
    <div className="rounded-[var(--radius)] border border-[color:var(--border)] px-3 py-3 grid grid-cols-1 sm:grid-cols-[1fr_1.4fr_auto] gap-3 items-start">
      <TextField
        label="Provider"
        value={draft.provider}
        onChange={(v) => onChange({ ...draft, provider: v })}
        placeholder="openai"
        mono
      />
      <div className="flex flex-col gap-1.5">
        <label className="text-[10px] uppercase tracking-[0.07em] text-[color:var(--text-3)]">Credential</label>
        {masked ? (
          <div className="flex items-center gap-2">
            <span className="mono text-[12.5px] flex-1 px-2 py-1 rounded-[5px] border border-[color:var(--border)] bg-[color:var(--bg-2)] text-[color:var(--text-3)]">
              <span aria-hidden="true">••••</span>{draft.existing?.last4}
            </span>
            <button
              type="button"
              onClick={() => onChange({ ...draft, dirty: true, value: "" })}
              className="text-[11.5px] text-[color:var(--text-3)] hover:text-[color:var(--text)]"
            >
              Change
            </button>
          </div>
        ) : (
          <div className="flex items-center gap-2">
            <input
              type="text"
              value={draft.value}
              onChange={(e) => onChange({ ...draft, value: e.target.value, dirty: true })}
              placeholder={draft.existing ? "new secret (blank = no-credential provider)" : "sk-… (blank = no-credential provider)"}
              className="mono flex-1 min-w-0 rounded-[5px] border border-[color:var(--border)] bg-[color:var(--bg-2)] px-2 py-1 text-[12.5px] text-[color:var(--text)] outline-none focus:border-[color:var(--text-3)]"
            />
            {draft.existing && (
              <button
                type="button"
                onClick={() => onChange({ ...draft, dirty: false, value: "" })}
                className="text-[11.5px] text-[color:var(--text-3)] hover:text-[color:var(--text)]"
              >
                Keep existing
              </button>
            )}
          </div>
        )}
        <span className="text-[11px] text-[color:var(--text-4)]">
          {masked ? "Stored secret kept on save unless you change it." : "Typed value will be set. Leave blank for a no-credential provider."}
        </span>
      </div>
      <div className="flex flex-col gap-1.5">
        {/* Spacer mirrors FieldRow's label height so the ✕ aligns with the
            inputs, not the labels, now that the row is top-aligned. */}
        <span aria-hidden="true" className="text-[10px] tracking-[0.07em] select-none invisible">.</span>
        <button
          type="button"
          aria-label="Remove credential"
          onClick={onRemove}
          className="text-[color:var(--text-3)] hover:text-[color:var(--err)] text-[12px] px-1 py-1"
        >
          ✕
        </button>
      </div>
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
          <SelectField label="Protocol" value={draft.protocol} options={PROTOCOL_OPTIONS} onChange={(v) => onChange({ ...draft, protocol: v })} />
          <SelectField
            label="Destination"
            value={draft.destinationKind}
            options={[{ value: "provider", label: "provider" }, { value: "group", label: "group" }]}
            onChange={(v) => onChange({ ...draft, destinationKind: v as "provider" | "group" })}
          />
          <TextField label={draft.destinationKind === "group" ? "Group name" : "Provider name"} value={draft.destination} onChange={(v) => onChange({ ...draft, destination: v })} placeholder={draft.destinationKind === "group" ? "qwen-pool" : "openai"} mono />
        </div>
        <StringListEditor
          label="Models"
          values={draft.models}
          onChange={(m) => onChange({ ...draft, models: m })}
          placeholder="claude-* (empty = catch-all for the protocol)"
          addLabel="+ Add model pattern"
          hint="Trailing-* wildcard. Empty matches any model for the protocol."
        />
        {draft.destinationKind === "provider" && (
          <TextField label="Alias (model rewrite)" value={draft.alias} onChange={(v) => onChange({ ...draft, alias: v })} placeholder="" mono hint="Rewrites the request model on the way to the provider." />
        )}
      </div>
    </div>
  )
}

function Wrap({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <div>
      <PageHeader title={title} />
      {children}
    </div>
  )
}
