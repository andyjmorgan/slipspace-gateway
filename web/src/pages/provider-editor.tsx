// ProviderEditorPage handles creating a new provider (/providers/new) and
// replacing an existing one (/providers/:name/edit). The read detail
// endpoint returns ProviderDetail (protocols/passthrough as arrays); the
// write body is the contract Provider (protocols/passthrough as maps). The
// form models the array shape and serialises to the map shape on submit.
//
// Providers are a high-blast-radius topology resource: the editor runs a
// dry-run preview before the real save, and delete is type-to-confirm.

import { useEffect, useState } from "react"
import { Link, useNavigate, useParams } from "react-router"
import { Button } from "@/components/ui/button"
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
} from "@/components/forms/field-atoms"
import { PROTOCOL_OPTIONS } from "@/lib/protocols"
import {
  recordFromPairs,
  pairsFromRecord,
  stringsFromList,
  type KVPair,
} from "@/components/forms/field-helpers"
import { ErrorBanner, PreviewBanner } from "@/components/forms/write-atoms"
import { classifyWriteError, type EditorError } from "@/lib/write-error"
import {
  createProvider,
  replaceProvider,
  previewProvider,
  useProvider,
  type ProviderDetail,
  type ProviderWriteBody,
  type PreviewResult,
} from "@/lib/config-api"
import { APIError, UnauthorizedError } from "@/lib/api"

type ProtocolDraft = {
  name: string
  path: string
  authHeader: string
  authFormat: string
}

type PassthroughPathDraft = {
  match: string
  methods: string[]
}

type PassthroughFamilyDraft = {
  name: string
  authHeader: string
  paths: PassthroughPathDraft[]
}

type ProviderFormState = {
  name: string
  baseUrl: string
  requiredHeaders: KVPair[]
  query: KVPair[]
  protocols: ProtocolDraft[]
  passthrough: PassthroughFamilyDraft[]
}

function emptyForm(): ProviderFormState {
  return {
    name: "",
    baseUrl: "",
    requiredHeaders: [],
    query: [],
    protocols: [],
    passthrough: [],
  }
}

function formFromDetail(d: ProviderDetail): ProviderFormState {
  return {
    name: d.name,
    baseUrl: d.base_url,
    requiredHeaders: pairsFromRecord(d.required_headers),
    query: pairsFromRecord(d.query),
    protocols: d.protocols.map((p) => ({
      name: p.name,
      path: p.path,
      authHeader: p.auth_header ?? "",
      authFormat: p.auth_format ?? "",
    })),
    passthrough: (d.passthrough ?? []).map((f) => ({
      name: f.name,
      authHeader: f.auth_header ?? "",
      paths: f.paths.map((pp) => ({ match: pp.match, methods: pp.methods.slice() })),
    })),
  }
}

function toWriteBody(form: ProviderFormState): ProviderWriteBody {
  const body: ProviderWriteBody = {
    name: form.name.trim(),
    base_url: form.baseUrl.trim(),
  }
  const headers = recordFromPairs(form.requiredHeaders)
  if (Object.keys(headers).length > 0) body.required_headers = headers
  const query = recordFromPairs(form.query)
  if (Object.keys(query).length > 0) body.query = query

  if (form.protocols.length > 0) {
    body.protocols = {}
    for (const p of form.protocols) {
      const name = p.name.trim()
      if (name === "") continue
      body.protocols[name] = {
        path: p.path.trim() || undefined,
        auth: p.authHeader.trim()
          ? { header: p.authHeader.trim(), format: p.authFormat.trim() || undefined }
          : undefined,
      }
    }
  }

  const families = form.passthrough.filter((f) => f.name.trim() !== "")
  if (families.length > 0) {
    body.passthrough = {}
    for (const f of families) {
      body.passthrough[f.name.trim()] = {
        auth: f.authHeader.trim() ? { header: f.authHeader.trim() } : undefined,
        paths: f.paths
          .filter((pp) => pp.match.trim() !== "")
          .map((pp) => ({ match: pp.match.trim(), methods: stringsFromList(pp.methods) })),
      }
    }
  }
  return body
}

export function ProviderEditorPage({ mode }: { mode: "create" | "edit" }) {
  if (mode === "create") return <CreateProviderPage />
  return <EditProviderPage />
}

function CreateProviderPage() {
  const nav = useNavigate()
  const [form, setForm] = useState<ProviderFormState>(emptyForm)
  return (
    <EditorBody
      title="New provider"
      sub="Define an upstream connection shared across configurations — base URL, per-protocol paths + auth, and any passthrough families."
      form={form}
      setForm={setForm}
      urlName={null}
      onSaved={(d) => nav(`/providers/${encodeURIComponent(d.name)}`)}
      cancelTo="/providers"
      nameEditable
      submitLabel="Create provider"
    />
  )
}

function EditProviderPage() {
  const { name } = useParams<{ name: string }>()
  const nav = useNavigate()
  const { state } = useProvider(name)
  useUnauthorizedRedirect(state)
  const [form, setForm] = useState<ProviderFormState | null>(null)

  useEffect(() => {
    if (state.status === "ok" && form === null) {
      // eslint-disable-next-line react-hooks/set-state-in-effect
      setForm(formFromDetail(state.data))
    }
  }, [state, form])

  if (state.status === "loading") return <Wrap title={name ?? "Provider"}><LoadingPanel /></Wrap>
  if (state.status === "error") return <Wrap title={name ?? "Provider"}><ErrorPanel message={state.message} /></Wrap>
  if (state.status === "not_found") return <Wrap title={name ?? "Provider"}><NotFoundPanel kind="provider" name={name} /></Wrap>
  if (state.status !== "ok" || form === null) return null

  const providerName = state.data.name
  return (
    <EditorBody
      title={`Edit provider · ${providerName}`}
      sub="Replace this provider's connection settings. The name is fixed — bindings and credentials reference it by name."
      form={form}
      setForm={setForm}
      urlName={providerName}
      onSaved={(d) => nav(`/providers/${encodeURIComponent(d.name)}`)}
      cancelTo={`/providers/${encodeURIComponent(providerName)}`}
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
  form: ProviderFormState
  setForm: (next: ProviderFormState) => void
  urlName: string | null
  onSaved: (d: ProviderDetail) => void
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
      setPreview(await previewProvider(urlName, toWriteBody(form)))
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
      const d = urlName ? await replaceProvider(urlName, body) : await createProvider(body)
      onSaved(d)
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

      <PanelCard>
        <PanelHead title="Connection" sub="base URL + transport defaults applied to every request" />
        <div className="px-4 py-4 flex flex-col gap-3">
          <TextField
            label="Name"
            value={form.name}
            onChange={(v) => setForm({ ...form, name: v })}
            placeholder="openai"
            mono
            hint={nameEditable ? "Unique across providers. Referenced by bindings, groups, and credentials." : "Names are immutable post-create."}
          />
          <TextField
            label="Base URL"
            value={form.baseUrl}
            onChange={(v) => setForm({ ...form, baseUrl: v })}
            placeholder="https://api.openai.com"
            mono
          />
          <KeyValueEditor
            label="Required headers"
            pairs={form.requiredHeaders}
            onChange={(p) => setForm({ ...form, requiredHeaders: p })}
            keyPlaceholder="anthropic-version"
            valuePlaceholder="2023-06-01"
            addLabel="+ Add header"
            hint="Injected on every forwarded request to this provider."
          />
          <KeyValueEditor
            label="Default query parameters"
            pairs={form.query}
            onChange={(p) => setForm({ ...form, query: p })}
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
          sub="generative wire shapes this provider serves; each with an upstream path + optional per-protocol auth override"
          action={
            <Button
              type="button"
              variant="outline"
              size="sm"
              onClick={() => setForm({ ...form, protocols: [...form.protocols, { name: "", path: "", authHeader: "", authFormat: "" }] })}
            >
              + Add protocol
            </Button>
          }
        />
        <div className="px-4 py-4 flex flex-col gap-3">
          {form.protocols.length === 0 && (
            <div className="text-[12.5px] text-[color:var(--text-4)]">No protocols — this provider serves no generative traffic.</div>
          )}
          {form.protocols.map((p, i) => (
            <ProtocolCard
              key={i}
              draft={p}
              onChange={(next) => {
                const copy = form.protocols.slice()
                copy[i] = next
                setForm({ ...form, protocols: copy })
              }}
              onRemove={() => {
                const copy = form.protocols.slice()
                copy.splice(i, 1)
                setForm({ ...form, protocols: copy })
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
              onClick={() => setForm({ ...form, passthrough: [...form.passthrough, { name: "", authHeader: "", paths: [] }] })}
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
                setForm({ ...form, passthrough: copy })
              }}
              onRemove={() => {
                const copy = form.passthrough.slice()
                copy.splice(i, 1)
                setForm({ ...form, passthrough: copy })
              }}
            />
          ))}
        </div>
      </PanelCard>

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

function ProtocolCard({
  draft,
  onChange,
  onRemove,
}: {
  draft: ProtocolDraft
  onChange: (next: ProtocolDraft) => void
  onRemove: () => void
}) {
  return (
    <div className="rounded-[var(--radius)] border border-[color:var(--border)] overflow-hidden">
      <div className="px-3 py-2 border-b border-[color:var(--border)] bg-[color:var(--bg-2)] flex items-center gap-2">
        <Tag variant="default"><span className="mono">{draft.name || "protocol"}</span></Tag>
        <button
          type="button"
          onClick={onRemove}
          className="ml-auto text-[11.5px] text-[color:var(--text-3)] hover:text-[color:var(--err)]"
        >
          Remove
        </button>
      </div>
      <div className="px-3 py-3 grid grid-cols-1 sm:grid-cols-2 gap-3">
        <SelectField label="Protocol name" value={draft.name} options={[{ value: "", label: "Select a protocol…" }, ...PROTOCOL_OPTIONS]} onChange={(v) => onChange({ ...draft, name: v })} />
        <TextField label="Upstream path" value={draft.path} onChange={(v) => onChange({ ...draft, path: v })} placeholder="/v1/chat/completions" mono />
        <TextField label="Auth header (override)" value={draft.authHeader} onChange={(v) => onChange({ ...draft, authHeader: v })} placeholder="Authorization" mono hint="Leave blank to use the provider-native default." />
        <TextField label="Auth format (override)" value={draft.authFormat} onChange={(v) => onChange({ ...draft, authFormat: v })} placeholder="Bearer {key}" mono />
      </div>
    </div>
  )
}

function PassthroughCard({
  draft,
  onChange,
  onRemove,
}: {
  draft: PassthroughFamilyDraft
  onChange: (next: PassthroughFamilyDraft) => void
  onRemove: () => void
}) {
  return (
    <div className="rounded-[var(--radius)] border border-[color:var(--border)] overflow-hidden">
      <div className="px-3 py-2 border-b border-[color:var(--border)] bg-[color:var(--bg-2)] flex items-center gap-2">
        <Tag variant="violet"><span className="mono">{draft.name || "family"}</span></Tag>
        <button
          type="button"
          onClick={onRemove}
          className="ml-auto text-[11.5px] text-[color:var(--text-3)] hover:text-[color:var(--err)]"
        >
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
              <TextField label="Match" value={pp.match} onChange={(v) => {
                const copy = draft.paths.slice()
                copy[i] = { ...copy[i], match: v }
                onChange({ ...draft, paths: copy })
              }} placeholder="/v1/messages/batches" mono />
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

function Wrap({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <div>
      <PageHeader title={title} />
      {children}
    </div>
  )
}
