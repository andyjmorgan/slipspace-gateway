import { useEffect, useState } from "react"
import { useNavigate, useParams } from "react-router"
import { ArrowLeft, Save } from "lucide-react"
import { Button } from "@/components/ui/button"
import { BackendFormFields } from "@/components/config-editors/backend-form"
import {
  backendFormFromContract,
  backendFormToContract,
  emptyBackendForm,
  type BackendFormState,
} from "@/components/config-editors/backend-form-model"
import { apiErrorText, UnauthorizedError } from "../lib/api"
import { getEntity, putEntity } from "../lib/config-api"

// CpBackendEditorPage edits a backend entity with the SAME structured form the
// gateway console uses (BackendFormFields). The data layer is the CP's
// stage-then-publish entity API — no dry-run preview; validation lands on
// Publish.
export function CpBackendEditorPage({ mode }: { mode: "create" | "edit" }) {
  const nav = useNavigate()
  const params = useParams()
  const editing = mode === "edit"

  const [form, setForm] = useState<BackendFormState | null>(editing ? null : emptyBackendForm())
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)

  useEffect(() => {
    if (!editing) return
    getEntity("backend", params.name!)
      .then((e) => setForm(backendFormFromContract(e.name, e.body as Record<string, unknown>)))
      .catch((e) => {
        if (e instanceof UnauthorizedError) return nav("/login", { replace: true })
        setError(apiErrorText(e))
      })
  }, [editing, params.name, nav])

  const save = async () => {
    if (!form) return
    setError(null)
    const name = form.name.trim()
    if (!name) {
      setError("Name is required.")
      return
    }
    setBusy(true)
    try {
      await putEntity("backend", name, backendFormToContract(form))
      nav("/config")
    } catch (e) {
      if (e instanceof UnauthorizedError) return nav("/login", { replace: true })
      setError(apiErrorText(e))
      setBusy(false)
    }
  }

  if (form === null) {
    return <div className="text-[13px] text-[color:var(--text-3)]">Loading…</div>
  }

  return (
    <div className="flex flex-col gap-4 max-w-[860px]">
      <div>
        <button
          onClick={() => nav("/config")}
          className="flex items-center gap-1.5 text-[13px] text-[color:var(--text-3)] hover:text-[color:var(--text)] mb-3"
        >
          <ArrowLeft size={14} /> Config
        </button>
        <h1 className="text-[22px] font-semibold tracking-[-0.02em]">
          {editing ? `Edit backend · ${form.name}` : "New backend"}
        </h1>
        <div className="text-[13px] text-[color:var(--text-3)] mt-1.5">
          An upstream connection — base URL, per-protocol paths + auth, and passthrough families. Staged into the
          working set; validated on Publish.
        </div>
      </div>

      {error && (
        <div
          className="rounded-[var(--radius)] px-3 py-2 text-[13px] border whitespace-pre-wrap"
          style={{ color: "var(--err)", background: "var(--err-bg)", borderColor: "color-mix(in oklab, var(--err) 30%, var(--border))" }}
        >
          {error}
        </div>
      )}

      <BackendFormFields value={form} onChange={setForm} nameEditable={!editing} />

      <div className="flex items-center gap-2 justify-end">
        <Button type="button" variant="ghost" onClick={() => nav("/config")}>
          Cancel
        </Button>
        <Button type="button" onClick={save} disabled={busy || form.name.trim() === ""}>
          <Save />
          {busy ? "Saving…" : editing ? "Save changes" : "Create backend"}
        </Button>
      </div>
    </div>
  )
}
