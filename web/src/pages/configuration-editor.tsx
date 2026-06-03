// ConfigurationEditorPage handles creating (/configurations/new) and editing
// (/configurations/:name/edit) a configuration. The presentational form is the
// shared <ConfigurationFormFields> (also used by the control-plane console);
// this page owns the gateway's typed read/write API, dry-run preview, and the
// redacted-credential mapping (mask on read; submit null to keep a stored
// secret) — the gateway-specific half the CP doesn't share.

import { useEffect, useState } from "react"
import { Link, useNavigate, useParams } from "react-router"
import { Button } from "@/components/ui/button"
import {
  PageHeader,
  LoadingPanel,
  ErrorPanel,
  NotFoundPanel,
  useUnauthorizedRedirect,
} from "@/components/atoms/page-states"
import { pairsFromRecord, stringsFromList } from "@/components/forms/field-helpers"
import { ErrorBanner, PreviewBanner } from "@/components/forms/write-atoms"
import { ConfigurationFormFields } from "@/components/config-editors/configuration-form"
import { emptyConfigForm, type ConfigFormState } from "@/components/config-editors/configuration-form-model"
import { classifyWriteError, type EditorError } from "@/lib/write-error"
import {
  createConfiguration,
  replaceConfiguration,
  previewConfiguration,
  useConfiguration,
  type ConfigurationDetail,
  type ConfigurationWriteBody,
  type BindingRow,
  type PassthroughBindingRow,
  type PreviewResult,
} from "@/lib/config-api"
import { APIError, UnauthorizedError } from "@/lib/api"

function formFromDetail(d: ConfigurationDetail): ConfigFormState {
  return {
    name: d.name,
    credentials: Object.entries(d.credentials ?? {}).map(([backend, redacted]) => ({
      backend,
      existing: redacted,
      value: "",
      dirty: false,
    })),
    bindings: d.bindings.map((b) => ({
      protocol: b.protocol,
      models: (b.models ?? []).slice(),
      destinationKind: b.group ? "group" : "backend",
      destination: b.group ?? b.backend ?? "",
      alias: b.alias ?? "",
    })),
    passthroughBindings: d.passthrough_bindings.map((p) => ({ family: p.family, backend: p.backend })),
    ruleNames: d.rules.map((r) => r.name),
    tags: pairsFromRecord(d.tags),
    connectorBindings: (d.connector_bindings ?? []).slice(),
  }
}

// toWriteBody applies the gateway's null-keep credential semantics: an
// untouched stored secret submits null (keep), never the masked placeholder.
function toWriteBody(form: ConfigFormState): ConfigurationWriteBody {
  const credentials: Record<string, string | null> = {}
  for (const c of form.credentials) {
    const backend = c.backend.trim()
    if (backend === "") continue
    credentials[backend] = c.existing && !c.dirty ? null : c.value
  }
  const bindings: BindingRow[] = form.bindings
    .filter((b) => b.destination.trim() !== "")
    .map((b) => {
      const row: BindingRow = { protocol: b.protocol, models: stringsFromList(b.models) }
      if (b.destinationKind === "group") row.group = b.destination.trim()
      else {
        row.backend = b.destination.trim()
        if (b.alias.trim()) row.alias = b.alias.trim()
      }
      return row
    })
  const passthrough: PassthroughBindingRow[] = form.passthroughBindings
    .filter((p) => p.family.trim() !== "" && p.backend.trim() !== "")
    .map((p) => ({ family: p.family.trim(), backend: p.backend.trim() }))

  const body: ConfigurationWriteBody = { name: form.name.trim() }
  if (Object.keys(credentials).length > 0) body.credentials = credentials
  if (bindings.length > 0) body.bindings = bindings
  if (passthrough.length > 0) body.passthrough_bindings = passthrough
  const rules = stringsFromList(form.ruleNames)
  if (rules.length > 0) body.rule_names = rules
  const tags: Record<string, string> = {}
  for (const t of form.tags) {
    const k = t.key.trim()
    if (k) tags[k] = t.value
  }
  if (Object.keys(tags).length > 0) body.tags = tags
  if (form.connectorBindings.length > 0) {
    body.connector_bindings = form.connectorBindings as ConfigurationWriteBody["connector_bindings"]
  }
  return body
}

export function ConfigurationEditorPage({ mode }: { mode: "create" | "edit" }) {
  if (mode === "create") return <CreateConfigPage />
  return <EditConfigPage />
}

function CreateConfigPage() {
  const nav = useNavigate()
  const [form, setForm] = useState<ConfigFormState>(emptyConfigForm)
  return (
    <EditorBody
      title="New configuration"
      sub="A reusable policy bundle — per-backend credentials, bindings that route models to backends, attached transform rules, and tags."
      form={form}
      setForm={setForm}
      urlName={null}
      onSaved={(c) => nav(`/configurations/${encodeURIComponent(c.name)}`)}
      cancelTo="/configurations"
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
  submitLabel,
}: {
  title: string
  sub: string
  form: ConfigFormState
  setForm: (next: ConfigFormState) => void
  urlName: string | null
  onSaved: (c: ConfigurationDetail) => void
  cancelTo: string
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

      <ConfigurationFormFields value={form} onChange={setForm} nameEditable={urlName === null} />

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

function Wrap({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <div>
      <PageHeader title={title} />
      {children}
    </div>
  )
}
