// RuleEditorPage handles creating (/rules/new) and editing (/rules/:name/edit)
// a rule. The presentational form is the shared <RuleFormFields> (also used by
// the control-plane console); this page owns the gateway's typed read/write API
// and the 409/422 conflict/validation banner.

import { useEffect, useMemo, useState } from "react"
import { useNavigate, useParams, Link } from "react-router"
import { Button } from "@/components/ui/button"
import {
  PageHeader,
  LoadingPanel,
  ErrorPanel,
  NotFoundPanel,
  useUnauthorizedRedirect,
} from "@/components/atoms/page-states"
import { RuleFormFields } from "@/components/config-editors/rule-form"
import {
  ruleFormFromContract,
  ruleFormToContract,
  emptyRuleForm,
  type RuleFormState,
} from "@/components/config-editors/rule-form-model"
import {
  createRule,
  replaceRule,
  useRule,
  type RuleConflict,
  type RuleValidationFailure,
  type RuleWriteBody,
} from "@/lib/config-api"
import { APIError, UnauthorizedError } from "@/lib/api"

export function RuleEditorPage({ mode }: { mode: "create" | "edit" }) {
  if (mode === "create") return <CreateRulePage />
  return <EditRulePage />
}

function CreateRulePage() {
  const nav = useNavigate()
  const [form, setForm] = useState<RuleFormState>(emptyRuleForm)
  const onSave = async () => {
    const created = await createRule(ruleFormToContract(form) as RuleWriteBody)
    nav(`/rules/${encodeURIComponent(created.name)}`)
  }
  return (
    <EditorBody
      title="New rule"
      sub="Create a rule in the shared library. Attach it to a configuration to make it fire."
      form={form}
      setForm={setForm}
      onSave={onSave}
      cancelTo="/rules"
      nameEditable
      submitLabel="Create rule"
    />
  )
}

function EditRulePage() {
  const { name } = useParams<{ name: string }>()
  const nav = useNavigate()
  const { state } = useRule(name)
  useUnauthorizedRedirect(state)
  const [form, setForm] = useState<RuleFormState | null>(null)

  useEffect(() => {
    if (state.status === "ok" && form === null) {
      // eslint-disable-next-line react-hooks/set-state-in-effect
      setForm(ruleFormFromContract(state.data.name, state.data as unknown as Record<string, unknown>))
    }
  }, [state, form])

  if (state.status === "loading") return <Wrap title={name ?? "Rule"}><LoadingPanel /></Wrap>
  if (state.status === "error") return <Wrap title={name ?? "Rule"}><ErrorPanel message={state.message} /></Wrap>
  if (state.status === "not_found") return <Wrap title={name ?? "Rule"}><NotFoundPanel kind="rule" name={name} /></Wrap>
  if (state.status !== "ok" || form === null) return null

  const ruleName = state.data.name
  const onSave = async () => {
    await replaceRule(ruleName, ruleFormToContract(form) as RuleWriteBody)
    nav(`/rules/${encodeURIComponent(ruleName)}`)
  }

  return (
    <EditorBody
      title={`Edit rule · ${ruleName}`}
      sub="Replace this rule's condition, actions, and behavior. The name is fixed — to rename, delete the rule and re-create it."
      form={form}
      setForm={setForm}
      onSave={onSave}
      cancelTo={`/rules/${encodeURIComponent(ruleName)}`}
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
  onSave,
  cancelTo,
  nameEditable,
  submitLabel,
}: {
  title: string
  sub: string
  form: RuleFormState
  setForm: (next: RuleFormState) => void
  onSave: () => Promise<void>
  cancelTo: string
  nameEditable: boolean
  submitLabel: string
}) {
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<EditorError | null>(null)

  const handleSubmit = async () => {
    setBusy(true)
    setError(null)
    try {
      await onSave()
    } catch (e) {
      if (e instanceof UnauthorizedError) {
        setError({ kind: "generic", message: "Session expired — log in again." })
      } else if (e instanceof APIError) {
        setError(classifyError(e))
      } else {
        setError({ kind: "generic", message: e instanceof Error ? e.message : String(e) })
      }
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

      <RuleFormFields value={form} onChange={setForm} nameEditable={nameEditable} />

      <div className="flex items-center gap-2 justify-end">
        <Link to={cancelTo}>
          <Button type="button" variant="ghost">Cancel</Button>
        </Link>
        <Button type="button" onClick={handleSubmit} disabled={busy || form.name.trim() === ""}>
          {busy ? "Saving…" : submitLabel}
        </Button>
      </div>
    </div>
  )
}

type EditorError =
  | { kind: "conflict"; message: string; usedBy?: string[]; name?: string }
  | { kind: "validation"; detail: string }
  | { kind: "generic"; message: string }

function classifyError(err: APIError): EditorError {
  if (err.status === 409) {
    const body = err.body as RuleConflict | undefined
    return { kind: "conflict", message: body?.error ?? err.message, usedBy: body?.used_by, name: body?.name }
  }
  if (err.status === 422) {
    const body = err.body as RuleValidationFailure | undefined
    return { kind: "validation", detail: body?.detail ?? err.message }
  }
  return { kind: "generic", message: err.message }
}

function ErrorBanner({ error }: { error: EditorError }) {
  const heading = useMemo(() => {
    if (error.kind === "conflict") return "Conflict — write rejected"
    if (error.kind === "validation") return "Validation failed"
    return "Save failed"
  }, [error])
  return (
    <div
      className="rounded-[var(--radius-lg)] border p-4 text-[13px]"
      style={{ color: "var(--err)", background: "var(--err-bg)", borderColor: "color-mix(in oklab, var(--err) 30%, var(--border))" }}
    >
      <div className="font-semibold mb-1">{heading}</div>
      {error.kind === "conflict" && (
        <>
          <div>{error.message}</div>
          {error.usedBy && error.usedBy.length > 0 && (
            <div className="mt-2 text-[12px]">
              Bound to configurations: {error.usedBy.map((n) => <span key={n} className="mono mr-2">{n}</span>)}
            </div>
          )}
        </>
      )}
      {error.kind === "validation" && <div className="mono text-[12px] whitespace-pre-wrap">{error.detail}</div>}
      {error.kind === "generic" && <div className="mono text-[12px]">{error.message}</div>}
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
