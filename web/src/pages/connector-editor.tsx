// ConnectorEditorPage handles creating (/connectors/new) and editing
// (/connectors/:name/edit) a spool connector. The presentational form is the
// shared <ConnectorFormFields> (also used by the control-plane console); this
// page owns the gateway's typed read/write/delete API.

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
import { ErrorBanner, DeleteDialog } from "@/components/forms/write-atoms"
import { ConnectorFormFields } from "@/components/config-editors/connector-form"
import {
  connectorFormFromContract,
  connectorFormToContract,
  emptyConnectorForm,
  type ConnectorFormState,
} from "@/components/config-editors/connector-form-model"
import { classifyWriteError, type EditorError } from "@/lib/write-error"
import { createConnector, replaceConnector, deleteConnector, useConnector, type Connector } from "@/lib/config-api"
import { APIError, UnauthorizedError } from "@/lib/api"

export function ConnectorEditorPage({ mode }: { mode: "create" | "edit" }) {
  if (mode === "create") return <CreateConnectorPage />
  return <EditConnectorPage />
}

function CreateConnectorPage() {
  const nav = useNavigate()
  const [form, setForm] = useState<ConnectorFormState>(emptyConnectorForm)
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
      setForm(connectorFormFromContract(state.data.name, state.data))
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
      const body = connectorFormToContract(form) as Connector
      const c = urlName ? await replaceConnector(urlName, body) : await createConnector(body)
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

      <ConnectorFormFields value={form} onChange={setForm} nameEditable={nameEditable} />

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
