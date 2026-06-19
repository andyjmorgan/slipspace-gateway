// ConnectorEditorPage handles creating (/connectors/new) and editing
// (/connectors/:name/edit) a spool connector. Connectors carry no plaintext
// secrets — cloud + webhook credentials are secret_ref indirections
// (env:NAME / file:/path) resolved at runtime — so the form round-trips the
// contract type directly with no mask/write-back. Type-specific fields show
// per the selected connector type. Delete goes through the warning dialog.

import { useEffect, useState } from "react"
import { Link, useNavigate, useParams } from "react-router"
import { Button } from "@/components/ui/button"
import { PanelCard, PanelHead } from "@/components/atoms/card"
import {
  PageHeader,
  LoadingPanel,
  ErrorPanel,
  NotFoundPanel,
  useUnauthorizedRedirect,
} from "@/components/atoms/page-states"
import {
  TextField,
  NumberField,
  SelectField,
  CheckboxField,
  type SelectOption,
} from "@/components/forms/field-atoms"
import { ErrorBanner, DeleteDialog } from "@/components/forms/write-atoms"
import { classifyWriteError, type EditorError } from "@/lib/write-error"
import {
  createConnector,
  replaceConnector,
  deleteConnector,
  useConnector,
  type Connector,
  type ConnectorAuth,
} from "@/lib/config-api"
import { APIError, UnauthorizedError } from "@/lib/api"

const TYPE_OPTIONS: SelectOption[] = [
  { value: "s3", label: "s3" },
  { value: "azure_blob", label: "azure_blob" },
  { value: "webhook", label: "webhook" },
]

const S3_AUTH_MODES: SelectOption[] = [
  { value: "workload_identity", label: "workload_identity" },
  { value: "static", label: "static" },
  { value: "assume_role", label: "assume_role" },
]

const AZURE_AUTH_MODES: SelectOption[] = [
  { value: "workload_identity", label: "workload_identity" },
  { value: "sas_token", label: "sas_token" },
  { value: "account_key", label: "account_key" },
]

type ConnectorFormState = Connector & { auth: ConnectorAuth }

function emptyForm(): ConnectorFormState {
  return {
    name: "",
    type: "s3",
    auth: { mode: "workload_identity" },
  }
}

function formFromConnector(c: Connector): ConnectorFormState {
  return { ...c, auth: c.auth ?? { mode: "workload_identity" } }
}

function toWriteBody(form: ConnectorFormState): Connector {
  const out: Connector = {
    name: form.name.trim(),
    type: form.type,
  }
  if (form.type === "s3") {
    out.bucket = form.bucket?.trim() || undefined
    out.prefix = form.prefix?.trim() || undefined
    out.region = form.region?.trim() || undefined
    out.endpoint_url = form.endpoint_url?.trim() || undefined
    if (form.use_path_style) out.use_path_style = true
    out.auth = cleanAuth(form.auth, form.type)
  } else if (form.type === "azure_blob") {
    out.account = form.account?.trim() || undefined
    out.container = form.container?.trim() || undefined
    out.auth = cleanAuth(form.auth, form.type)
  } else if (form.type === "webhook") {
    out.url = form.url?.trim() || undefined
    out.secret_ref = form.secret_ref?.trim() || undefined
    if (form.timeout_ms != null && form.timeout_ms > 0) out.timeout_ms = form.timeout_ms
  }
  return out
}

// cleanAuth drops auth fields irrelevant to the selected mode so the write
// body matches what the per-mode validator expects.
function cleanAuth(auth: ConnectorAuth, type: string): ConnectorAuth {
  const out: ConnectorAuth = { mode: auth.mode }
  if (type === "s3") {
    if (auth.mode === "static") {
      out.access_key_id_ref = auth.access_key_id_ref?.trim() || undefined
      out.secret_access_key_ref = auth.secret_access_key_ref?.trim() || undefined
    } else if (auth.mode === "assume_role") {
      out.role_arn = auth.role_arn?.trim() || undefined
      out.external_id_ref = auth.external_id_ref?.trim() || undefined
    }
  } else if (type === "azure_blob") {
    if (auth.mode === "sas_token") out.sas_token_ref = auth.sas_token_ref?.trim() || undefined
    else if (auth.mode === "account_key") out.account_key_ref = auth.account_key_ref?.trim() || undefined
  }
  return out
}

export function ConnectorEditorPage({ mode }: { mode: "create" | "edit" }) {
  if (mode === "create") return <CreateConnectorPage />
  return <EditConnectorPage />
}

function CreateConnectorPage() {
  const nav = useNavigate()
  const [form, setForm] = useState<ConnectorFormState>(emptyForm)
  return (
    <EditorBody
      title="New connector"
      sub="A reusable spool destination. Cloud + webhook credentials are secret_ref indirections (env:NAME or file:/path), never plaintext."
      form={form}
      setForm={setForm}
      urlName={null}
      onSaved={(c) => nav(`/connectors/${encodeURIComponent(c.name)}/edit`)}
      onDeleted={() => nav("/connectors")}
      nameEditable
      submitLabel="Create connector"
    />
  )
}

function EditConnectorPage() {
  const { name } = useParams<{ name: string }>()
  const nav = useNavigate()
  const { state } = useConnector(name)
  useUnauthorizedRedirect(state)
  const [form, setForm] = useState<ConnectorFormState | null>(null)

  useEffect(() => {
    if (state.status === "ok" && form === null) {
      // eslint-disable-next-line react-hooks/set-state-in-effect
      setForm(formFromConnector(state.data))
    }
  }, [state, form])

  if (state.status === "loading") return <Wrap title={name ?? "Connector"}><LoadingPanel /></Wrap>
  if (state.status === "error") return <Wrap title={name ?? "Connector"}><ErrorPanel message={state.message} /></Wrap>
  if (state.status === "not_found") return <Wrap title={name ?? "Connector"}><NotFoundPanel kind="connector" name={name} /></Wrap>
  if (state.status !== "ok" || form === null) return null

  const connectorName = state.data.name
  return (
    <EditorBody
      title={`Edit connector · ${connectorName}`}
      sub="Replace this connector's destination + auth. The name is fixed — connector bindings reference it by name."
      form={form}
      setForm={setForm}
      urlName={connectorName}
      onSaved={(c) => nav(`/connectors/${encodeURIComponent(c.name)}/edit`)}
      onDeleted={() => nav("/connectors")}
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
  onDeleted,
  nameEditable,
  submitLabel,
}: {
  title: string
  sub: string
  form: ConnectorFormState
  setForm: (next: ConnectorFormState) => void
  urlName: string | null
  onSaved: (c: Connector) => void
  onDeleted: () => void
  nameEditable: boolean
  submitLabel: string
}) {
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<EditorError | null>(null)
  const [confirmDelete, setConfirmDelete] = useState(false)

  const handleError = (e: unknown) => {
    if (e instanceof UnauthorizedError) {
      setError({ kind: "generic", message: "Session expired — log in again." })
    } else if (e instanceof APIError) {
      setError(classifyWriteError(e))
    } else {
      setError({ kind: "generic", message: e instanceof Error ? e.message : String(e) })
    }
  }

  const handleSubmit = async () => {
    setBusy(true)
    setError(null)
    try {
      const body = toWriteBody(form)
      const c = urlName ? await replaceConnector(urlName, body) : await createConnector(body)
      onSaved(c)
    } catch (e) {
      handleError(e)
    } finally {
      setBusy(false)
    }
  }

  const setAuth = (next: Partial<ConnectorAuth>) => setForm({ ...form, auth: { ...form.auth, ...next } })

  return (
    <div className="flex flex-col gap-4">
      <PageHeader
        title={title}
        sub={sub}
        action={
          <div className="flex items-center gap-2">
            {urlName && (
              <Button type="button" size="sm" variant="destructive" onClick={() => setConfirmDelete(true)}>
                Delete
              </Button>
            )}
            <Link to="/connectors" className="text-[12.5px] text-[color:var(--text-3)] hover:underline ml-1">
              ← cancel
            </Link>
          </div>
        }
      />

      {error && <ErrorBanner error={error} />}

      <PanelCard>
        <PanelHead title="Connector" sub="identity and type" />
        <div className="px-4 py-4 flex flex-col gap-3">
          <TextField
            label="Name"
            value={form.name}
            onChange={(v) => setForm({ ...form, name: v })}
            placeholder="audit-s3"
            mono
            hint={nameEditable ? "Unique across connectors. Referenced by connector bindings." : "Names are immutable post-create."}
          />
          <SelectField
            label="Type"
            value={form.type}
            options={TYPE_OPTIONS}
            onChange={(t) => setForm({ ...form, type: t })}
          />
        </div>
      </PanelCard>

      {form.type === "s3" && (
        <PanelCard>
          <PanelHead title="S3 destination" sub="bucket + region; endpoint_url for S3-compatible providers" />
          <div className="px-4 py-4 flex flex-col gap-3">
            <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
              <TextField label="Bucket" value={form.bucket ?? ""} onChange={(v) => setForm({ ...form, bucket: v })} placeholder="slipspace-artifacts" mono />
              <TextField label="Region" value={form.region ?? ""} onChange={(v) => setForm({ ...form, region: v })} placeholder="us-east-1" mono />
              <TextField label="Prefix" value={form.prefix ?? ""} onChange={(v) => setForm({ ...form, prefix: v })} placeholder="records/" mono />
              <TextField label="Endpoint URL" value={form.endpoint_url ?? ""} onChange={(v) => setForm({ ...form, endpoint_url: v })} placeholder="https://s3.donkeywork.dev" mono hint="Blank = real AWS S3." />
            </div>
            <CheckboxField
              label="Use path-style addressing"
              checked={form.use_path_style ?? false}
              onChange={(c) => setForm({ ...form, use_path_style: c })}
              hint="MinIO / SeaweedFS default to path-style; AWS prefers virtual-hosted."
            />
            <SelectField label="Auth mode" value={form.auth.mode} options={S3_AUTH_MODES} onChange={(m) => setAuth({ mode: m })} />
            {form.auth.mode === "static" && (
              <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
                <TextField label="Access key ID ref" value={form.auth.access_key_id_ref ?? ""} onChange={(v) => setAuth({ access_key_id_ref: v })} placeholder="env:AWS_ACCESS_KEY_ID" mono />
                <TextField label="Secret access key ref" value={form.auth.secret_access_key_ref ?? ""} onChange={(v) => setAuth({ secret_access_key_ref: v })} placeholder="env:AWS_SECRET_ACCESS_KEY" mono />
              </div>
            )}
            {form.auth.mode === "assume_role" && (
              <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
                <TextField label="Role ARN" value={form.auth.role_arn ?? ""} onChange={(v) => setAuth({ role_arn: v })} placeholder="arn:aws:iam::…:role/…" mono />
                <TextField label="External ID ref" value={form.auth.external_id_ref ?? ""} onChange={(v) => setAuth({ external_id_ref: v })} placeholder="env:EXTERNAL_ID" mono hint="Optional but recommended for cross-account." />
              </div>
            )}
          </div>
        </PanelCard>
      )}

      {form.type === "azure_blob" && (
        <PanelCard>
          <PanelHead title="Azure Blob destination" sub="storage account + container" />
          <div className="px-4 py-4 flex flex-col gap-3">
            <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
              <TextField label="Account" value={form.account ?? ""} onChange={(v) => setForm({ ...form, account: v })} placeholder="slipspacestore" mono />
              <TextField label="Container" value={form.container ?? ""} onChange={(v) => setForm({ ...form, container: v })} placeholder="records" mono />
            </div>
            <SelectField label="Auth mode" value={form.auth.mode} options={AZURE_AUTH_MODES} onChange={(m) => setAuth({ mode: m })} />
            {form.auth.mode === "sas_token" && (
              <TextField label="SAS token ref" value={form.auth.sas_token_ref ?? ""} onChange={(v) => setAuth({ sas_token_ref: v })} placeholder="env:AZURE_SAS_TOKEN" mono />
            )}
            {form.auth.mode === "account_key" && (
              <TextField label="Account key ref" value={form.auth.account_key_ref ?? ""} onChange={(v) => setAuth({ account_key_ref: v })} placeholder="env:AZURE_ACCOUNT_KEY" mono />
            )}
          </div>
        </PanelCard>
      )}

      {form.type === "webhook" && (
        <PanelCard>
          <PanelHead title="Webhook destination" sub="HTTPS endpoint + HMAC signing key ref" />
          <div className="px-4 py-4 flex flex-col gap-3">
            <TextField label="URL" value={form.url ?? ""} onChange={(v) => setForm({ ...form, url: v })} placeholder="https://receiver.example/slipspace" mono />
            <TextField label="Secret ref (HMAC key)" value={form.secret_ref ?? ""} onChange={(v) => setForm({ ...form, secret_ref: v })} placeholder="env:WEBHOOK_HMAC_KEY" mono />
            <NumberField label="Timeout (ms)" value={form.timeout_ms ?? null} onChange={(n) => setForm({ ...form, timeout_ms: n ?? undefined })} placeholder="5000" hint="Per-call HTTP timeout. 0 < timeout <= 60000." />
          </div>
        </PanelCard>
      )}

      <div className="flex items-center gap-2 justify-end">
        <Link to="/connectors">
          <Button type="button" variant="ghost">Cancel</Button>
        </Link>
        <Button type="button" onClick={handleSubmit} disabled={busy || form.name.trim() === ""}>
          {busy ? "Saving…" : submitLabel}
        </Button>
      </div>

      {urlName && (
        <DeleteDialog
          open={confirmDelete}
          resourceKind="connector"
          resourceName={urlName}
          onConfirm={async () => {
            await deleteConnector(urlName)
            onDeleted()
          }}
          onClose={() => setConfirmDelete(false)}
        />
      )}
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
