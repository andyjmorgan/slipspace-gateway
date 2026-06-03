// GroupEditorPage handles creating (/groups/new) and editing
// (/groups/:name/edit) a resilience group. The presentational form is the
// shared <GroupFormFields> (also used by the control-plane console); this page
// owns the gateway's typed read/write/delete API + dry-run preview.

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
import { ErrorBanner, PreviewBanner, DeleteDialog } from "@/components/forms/write-atoms"
import { GroupFormFields } from "@/components/config-editors/group-form"
import {
  groupFormFromContract,
  groupFormToContract,
  emptyGroupForm,
  type GroupFormState,
} from "@/components/config-editors/group-form-model"
import { classifyWriteError, type EditorError } from "@/lib/write-error"
import {
  createGroup,
  replaceGroup,
  previewGroup,
  deleteGroup,
  useGroup,
  type GroupView,
  type GroupWriteBody,
  type PreviewResult,
} from "@/lib/config-api"
import { APIError, UnauthorizedError } from "@/lib/api"

export function GroupEditorPage({ mode }: { mode: "create" | "edit" }) {
  if (mode === "create") return <CreateGroupPage />
  return <EditGroupPage />
}

function CreateGroupPage() {
  const nav = useNavigate()
  const [form, setForm] = useState<GroupFormState>(emptyGroupForm)
  return (
    <EditorBody
      title="New group"
      sub="A named resilience unit — an ordered or weighted set of backend targets the orchestrator routes across."
      form={form}
      setForm={setForm}
      urlName={null}
      onSaved={(g) => nav(`/groups/${encodeURIComponent(g.name)}`)}
      cancelTo="/policies"
      nameEditable
      submitLabel="Create group"
    />
  )
}

function EditGroupPage() {
  const { name } = useParams<{ name: string }>()
  const nav = useNavigate()
  const { state } = useGroup(name)
  useUnauthorizedRedirect(state)
  const [form, setForm] = useState<GroupFormState | null>(null)

  useEffect(() => {
    if (state.status === "ok" && form === null) {
      // eslint-disable-next-line react-hooks/set-state-in-effect
      setForm(groupFormFromContract(state.data.name, state.data))
    }
  }, [state, form])

  if (state.status === "loading") return <Wrap title={name ?? "Group"}><LoadingPanel /></Wrap>
  if (state.status === "error") return <Wrap title={name ?? "Group"}><ErrorPanel message={state.message} /></Wrap>
  if (state.status === "not_found") return <Wrap title={name ?? "Group"}><NotFoundPanel kind="group" name={name} /></Wrap>
  if (state.status !== "ok" || form === null) return null

  const groupName = state.data.name
  return (
    <EditorBody
      title={`Edit group · ${groupName}`}
      sub="Replace this group's mode, failure policy, and targets. The name is fixed — bindings reference it by name."
      form={form}
      setForm={setForm}
      urlName={groupName}
      onSaved={(g) => nav(`/groups/${encodeURIComponent(g.name)}`)}
      cancelTo="/policies"
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
  form: GroupFormState
  setForm: (next: GroupFormState) => void
  urlName: string | null
  onSaved: (g: GroupView) => void
  cancelTo: string
  nameEditable: boolean
  submitLabel: string
}) {
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<EditorError | null>(null)
  const [preview, setPreview] = useState<PreviewResult | null>(null)
  const [confirmDelete, setConfirmDelete] = useState(false)
  const nav = useNavigate()

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
      setPreview(await previewGroup(urlName, groupFormToContract(form) as GroupWriteBody))
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
      const body = groupFormToContract(form) as GroupWriteBody
      const g = urlName ? await replaceGroup(urlName, body) : await createGroup(body)
      onSaved(g)
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
            <Link to={cancelTo} className="text-[12.5px] text-[color:var(--text-3)] hover:underline ml-1">
              ← cancel
            </Link>
          </div>
        }
      />

      {urlName && (
        <DeleteDialog
          open={confirmDelete}
          resourceKind="group"
          resourceName={urlName}
          onConfirm={async () => {
            await deleteGroup(urlName)
            nav("/policies", { replace: true })
          }}
          onClose={() => setConfirmDelete(false)}
        />
      )}

      {error && <ErrorBanner error={error} />}
      {preview && <PreviewBanner result={preview} onDismiss={() => setPreview(null)} />}

      <GroupFormFields value={form} onChange={setForm} nameEditable={nameEditable} />

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
