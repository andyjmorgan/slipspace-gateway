// BackendEditorPage handles creating a new backend (/backends/new) and
// replacing an existing one (/backends/:name/edit). The presentational form is
// the shared <BackendFormFields> (also used by the control-plane console); this
// page owns the gateway's data layer — the typed read/write API plus the
// dry-run preview.

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
import { pairsFromRecord } from "@/components/forms/field-helpers"
import { ErrorBanner, PreviewBanner } from "@/components/forms/write-atoms"
import { BackendFormFields } from "@/components/config-editors/backend-form"
import {
  backendFormToContract,
  emptyBackendForm,
  type BackendFormState,
} from "@/components/config-editors/backend-form-model"
import { classifyWriteError, type EditorError } from "@/lib/write-error"
import {
  createBackend,
  replaceBackend,
  previewBackend,
  useBackend,
  type BackendDetail,
  type PreviewResult,
} from "@/lib/config-api"
import { APIError, UnauthorizedError } from "@/lib/api"

// formFromDetail maps the gateway's read shape (BackendDetail — protocols and
// passthrough as arrays) into the shared form state. The write shape
// (contract Backend, maps) is produced by backendFormToContract.
function formFromDetail(d: BackendDetail): BackendFormState {
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

export function BackendEditorPage({ mode }: { mode: "create" | "edit" }) {
  if (mode === "create") return <CreateBackendPage />
  return <EditBackendPage />
}

function CreateBackendPage() {
  const nav = useNavigate()
  const [form, setForm] = useState<BackendFormState>(emptyBackendForm)
  return (
    <EditorBody
      title="New backend"
      sub="Define an upstream connection shared across configurations — base URL, per-protocol paths + auth, and any passthrough families."
      form={form}
      setForm={setForm}
      urlName={null}
      onSaved={(d) => nav(`/backends/${encodeURIComponent(d.name)}`)}
      cancelTo="/backends"
      nameEditable
      submitLabel="Create backend"
    />
  )
}

function EditBackendPage() {
  const { name } = useParams<{ name: string }>()
  const nav = useNavigate()
  const { state } = useBackend(name)
  useUnauthorizedRedirect(state)
  const [form, setForm] = useState<BackendFormState | null>(null)

  useEffect(() => {
    if (state.status === "ok" && form === null) {
      // eslint-disable-next-line react-hooks/set-state-in-effect
      setForm(formFromDetail(state.data))
    }
  }, [state, form])

  if (state.status === "loading") return <Wrap title={name ?? "Backend"}><LoadingPanel /></Wrap>
  if (state.status === "error") return <Wrap title={name ?? "Backend"}><ErrorPanel message={state.message} /></Wrap>
  if (state.status === "not_found") return <Wrap title={name ?? "Backend"}><NotFoundPanel kind="backend" name={name} /></Wrap>
  if (state.status !== "ok" || form === null) return null

  const backendName = state.data.name
  return (
    <EditorBody
      title={`Edit backend · ${backendName}`}
      sub="Replace this backend's connection settings. The name is fixed — bindings and credentials reference it by name."
      form={form}
      setForm={setForm}
      urlName={backendName}
      onSaved={(d) => nav(`/backends/${encodeURIComponent(d.name)}`)}
      cancelTo={`/backends/${encodeURIComponent(backendName)}`}
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
  form: BackendFormState
  setForm: (next: BackendFormState) => void
  urlName: string | null
  onSaved: (d: BackendDetail) => void
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
      setPreview(await previewBackend(urlName, backendFormToContract(form)))
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
      const body = backendFormToContract(form)
      const d = urlName ? await replaceBackend(urlName, body) : await createBackend(body)
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

      <BackendFormFields value={form} onChange={setForm} nameEditable={nameEditable} />

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
