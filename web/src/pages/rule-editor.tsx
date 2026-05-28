// RuleEditorPage handles both creating a new rule (/rules/new) and
// replacing an existing one (/rules/:name/edit). The same form
// component drives both paths — for create the form starts empty,
// for edit it loads the current shape via useRule and seeds state
// from the response.
//
// On save the page calls createRule (POST) or replaceRule (PUT) and
// navigates to /rules/:name on success. 409 and 422 responses are
// surfaced inline above the form so the operator can correct the
// authoring mistake without leaving the page.

import { useEffect, useMemo, useState } from "react"
import { useNavigate, useParams, Link } from "react-router"
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
  type SelectOption,
} from "@/components/forms/field-atoms"
import {
  ConditionForm,
  emptyCondition,
  type ConditionValue,
} from "@/components/forms/condition-form"
import {
  ActionForm,
  emptyAction,
  type ActionValue,
} from "@/components/forms/action-form"
import {
  createRule,
  replaceRule,
  useRule,
  type RuleConflict,
  type RuleValidationFailure,
  type RuleWriteBody,
} from "@/lib/config-api"
import { APIError, UnauthorizedError } from "@/lib/api"

const BEHAVIOR_OPTIONS: SelectOption[] = [
  { value: "continue", label: "continue (default — evaluate next rule)" },
  { value: "exit", label: "exit (stop after this rule's actions)" },
]

type RuleFormState = {
  name: string
  behavior: string
  condition: ConditionValue
  actions: ActionValue[]
}

function emptyForm(): RuleFormState {
  return {
    name: "",
    behavior: "continue",
    condition: emptyCondition(),
    actions: [],
  }
}

function formFromDetail(name: string, detail: {
  behavior?: string
  condition?: Record<string, unknown>
  actions?: Record<string, unknown>[]
}): RuleFormState {
  return {
    name,
    behavior: detail.behavior || "continue",
    condition: (detail.condition as ConditionValue) ?? emptyCondition(),
    actions: (detail.actions as ActionValue[]) ?? [],
  }
}

function toWriteBody(form: RuleFormState): RuleWriteBody {
  return {
    name: form.name,
    behavior: form.behavior,
    condition: form.condition,
    actions: form.actions,
  }
}

// EditorMode discriminates the two entry points without an extra
// route component: pass `mode="create"` for /rules/new, `mode="edit"`
// for /rules/:name/edit.
export function RuleEditorPage({ mode }: { mode: "create" | "edit" }) {
  if (mode === "create") return <CreateRulePage />
  return <EditRulePage />
}

function CreateRulePage() {
  const nav = useNavigate()
  const [form, setForm] = useState<RuleFormState>(emptyForm)
  const onSave = async () => {
    const body = toWriteBody(form)
    const created = await createRule(body)
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
      setForm(formFromDetail(state.data.name, state.data))
    }
  }, [state, form])

  if (state.status === "loading") return <Wrap title={name ?? "Rule"}><LoadingPanel /></Wrap>
  if (state.status === "error") return <Wrap title={name ?? "Rule"}><ErrorPanel message={state.message} /></Wrap>
  if (state.status === "not_found") return <Wrap title={name ?? "Rule"}><NotFoundPanel kind="rule" name={name} /></Wrap>
  if (state.status !== "ok" || form === null) return null

  const ruleName = state.data.name
  const onSave = async () => {
    const body = toWriteBody(form)
    await replaceRule(ruleName, body)
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
        // page-level unauthorized handler will catch on next render
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

      <PanelCard>
        <PanelHead title="Identity" sub="rule name and post-match behavior" />
        <div className="px-4 py-4 flex flex-col gap-3 md:flex-row md:gap-4">
          <TextField
            label="Name"
            value={form.name}
            onChange={(v) => setForm({ ...form, name: v })}
            placeholder="tag-claude-code"
            mono
            hint={nameEditable ? "Lowercase + hyphens recommended. Must be unique across the rules library." : "Names are immutable post-create."}
            className="flex-1"
          />
          <SelectField
            label="Behavior"
            value={form.behavior}
            options={BEHAVIOR_OPTIONS}
            onChange={(b) => setForm({ ...form, behavior: b })}
            className="md:w-[28ch]"
          />
        </div>
        {!nameEditable && (
          <div className="px-4 pb-3 -mt-2">
            <span className="text-[11px] text-[color:var(--text-4)]">
              To rename, delete this rule and re-create it with the new name.
            </span>
          </div>
        )}
      </PanelCard>

      <PanelCard>
        <PanelHead title="Condition" sub="the predicate that must hold for the actions to run" />
        <div className="px-4 py-4">
          <ConditionForm
            value={form.condition}
            onChange={(c) => setForm({ ...form, condition: c })}
          />
        </div>
      </PanelCard>

      <PanelCard>
        <PanelHead
          title="Actions"
          sub={`${form.actions.length} action${form.actions.length === 1 ? "" : "s"} · run in order when the condition matches`}
          action={
            <Button
              type="button"
              variant="outline"
              size="sm"
              onClick={() => setForm({ ...form, actions: [...form.actions, emptyAction()] })}
            >
              + Add action
            </Button>
          }
        />
        <div className="px-4 py-4 flex flex-col gap-4">
          {form.actions.length === 0 && (
            <div className="text-[12.5px] text-[color:var(--text-4)]">
              No actions yet — a rule with no actions is a no-op.
            </div>
          )}
          {form.actions.map((a, i) => (
            <ActionRow
              key={i}
              index={i}
              total={form.actions.length}
              action={a}
              onChange={(next) => {
                const copy = form.actions.slice()
                copy[i] = next
                setForm({ ...form, actions: copy })
              }}
              onRemove={() => {
                const copy = form.actions.slice()
                copy.splice(i, 1)
                setForm({ ...form, actions: copy })
              }}
              onMoveUp={() => {
                if (i === 0) return
                const copy = form.actions.slice()
                ;[copy[i - 1], copy[i]] = [copy[i], copy[i - 1]]
                setForm({ ...form, actions: copy })
              }}
              onMoveDown={() => {
                if (i === form.actions.length - 1) return
                const copy = form.actions.slice()
                ;[copy[i], copy[i + 1]] = [copy[i + 1], copy[i]]
                setForm({ ...form, actions: copy })
              }}
            />
          ))}
        </div>
      </PanelCard>

      <div className="flex items-center gap-2 justify-end">
        <Link to={cancelTo}>
          <Button type="button" variant="ghost">Cancel</Button>
        </Link>
        <Button
          type="button"
          onClick={handleSubmit}
          disabled={busy || form.name.trim() === ""}
        >
          {busy ? "Saving…" : submitLabel}
        </Button>
      </div>
    </div>
  )
}

function ActionRow({
  index,
  total,
  action,
  onChange,
  onRemove,
  onMoveUp,
  onMoveDown,
}: {
  index: number
  total: number
  action: ActionValue
  onChange: (next: ActionValue) => void
  onRemove: () => void
  onMoveUp: () => void
  onMoveDown: () => void
}) {
  return (
    <div className="rounded-[var(--radius)] border border-[color:var(--border)] overflow-hidden">
      <div className="px-3 py-2 border-b border-[color:var(--border)] bg-[color:var(--bg-2)] flex items-center gap-2">
        <span className="text-[11px] text-[color:var(--text-4)] mono w-5 text-right">{index + 1}</span>
        <Tag variant="ghost"><span className="mono">{String(action.type ?? "?")}</span></Tag>
        <div className="ml-auto flex items-center gap-1">
          <Button type="button" size="xs" variant="ghost" onClick={onMoveUp} disabled={index === 0}>↑</Button>
          <Button type="button" size="xs" variant="ghost" onClick={onMoveDown} disabled={index === total - 1}>↓</Button>
          <Button
            type="button"
            size="xs"
            variant="ghost"
            onClick={onRemove}
            className="text-[color:var(--text-3)] hover:text-[color:var(--err)]"
          >
            Remove
          </Button>
        </div>
      </div>
      <div className="px-3 py-3">
        <ActionForm value={action} onChange={onChange} />
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
    return {
      kind: "conflict",
      message: body?.error ?? err.message,
      usedBy: body?.used_by,
      name: body?.name,
    }
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
      style={{
        color: "var(--err)",
        background: "var(--err-bg)",
        borderColor: "color-mix(in oklab, var(--err) 30%, var(--border))",
      }}
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
      {error.kind === "validation" && (
        <div className="mono text-[12px] whitespace-pre-wrap">{error.detail}</div>
      )}
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
