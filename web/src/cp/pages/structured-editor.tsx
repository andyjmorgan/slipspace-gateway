import { useEffect, useState, type ComponentType } from "react"
import { useNavigate, useParams } from "react-router"
import { ArrowLeft, Save } from "lucide-react"
import { Button } from "@/components/ui/button"
import { apiErrorText, UnauthorizedError } from "../lib/api"
import { getEntity, putEntity } from "../lib/config-api"
import { KIND_META, routeFor } from "../lib/kinds"
import type { EntityKind } from "../lib/types"

// Props every shared *FormFields component accepts.
export interface FormFieldsProps<F> {
  value: F
  onChange: (next: F) => void
  nameEditable: boolean
}

// CpStructuredEditor is the control-plane host for a shared config form: it owns
// the load (edit) + stage-then-publish save against the entity API, and renders
// the supplied FormFields. One wrapper drives every per-type structured editor.
export function CpStructuredEditor<F>({
  mode,
  kind,
  FormFields,
  emptyForm,
  fromContract,
  toContract,
  nameOf,
  sub,
}: {
  mode: "create" | "edit"
  kind: EntityKind
  FormFields: ComponentType<FormFieldsProps<F>>
  emptyForm: () => F
  fromContract: (name: string, body: Record<string, unknown>) => F
  // Returns the full contract object; typed as { name } since that's all the
  // wrapper needs (putEntity takes the body as unknown).
  toContract: (form: F) => { name: string }
  nameOf: (form: F) => string
  sub: string
}) {
  const nav = useNavigate()
  const params = useParams()
  const editing = mode === "edit"
  const backTo = `/${routeFor(kind)}`

  const [form, setForm] = useState<F | null>(editing ? null : emptyForm())
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)

  useEffect(() => {
    if (!editing) return
    getEntity(kind, params.name!)
      .then((e) => setForm(fromContract(e.name, e.body as Record<string, unknown>)))
      .catch((e) => {
        if (e instanceof UnauthorizedError) return nav("/login", { replace: true })
        setError(apiErrorText(e))
      })
    // fromContract is a stable module fn; exclude to avoid needless reloads.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [editing, kind, params.name, nav])

  const save = async () => {
    if (!form) return
    setError(null)
    const body = toContract(form)
    if (!body.name.trim()) {
      setError("Name is required.")
      return
    }
    setBusy(true)
    try {
      await putEntity(kind, body.name.trim(), body)
      nav(backTo)
    } catch (e) {
      if (e instanceof UnauthorizedError) return nav("/login", { replace: true })
      setError(apiErrorText(e))
      setBusy(false)
    }
  }

  if (form === null) {
    return <div className="text-[13px] text-[color:var(--text-3)]">Loading…</div>
  }

  const label = KIND_META[kind].title.replace(/s$/, "").toLowerCase()
  return (
    <div className="flex flex-col gap-4 max-w-[860px]">
      <div>
        <button
          onClick={() => nav(backTo)}
          className="flex items-center gap-1.5 text-[13px] text-[color:var(--text-3)] hover:text-[color:var(--text)] mb-3"
        >
          <ArrowLeft size={14} /> {KIND_META[kind].title}
        </button>
        <h1 className="text-[22px] font-semibold tracking-[-0.02em]">
          {editing ? `Edit ${label} · ${nameOf(form)}` : `New ${label}`}
        </h1>
        <div className="text-[13px] text-[color:var(--text-3)] mt-1.5">{sub} Staged into the working set; validated on Publish.</div>
      </div>

      {error && (
        <div
          className="rounded-[var(--radius)] px-3 py-2 text-[13px] border whitespace-pre-wrap"
          style={{ color: "var(--err)", background: "var(--err-bg)", borderColor: "color-mix(in oklab, var(--err) 30%, var(--border))" }}
        >
          {error}
        </div>
      )}

      <FormFields value={form} onChange={setForm} nameEditable={!editing} />

      <div className="flex items-center gap-2 justify-end">
        <Button type="button" variant="ghost" onClick={() => nav(backTo)}>
          Cancel
        </Button>
        <Button type="button" onClick={save} disabled={busy || nameOf(form).trim() === ""}>
          <Save />
          {busy ? "Saving…" : editing ? "Save changes" : "Create"}
        </Button>
      </div>
    </div>
  )
}
