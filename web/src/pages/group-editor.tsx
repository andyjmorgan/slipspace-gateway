// GroupEditorPage handles creating (/groups/new) and editing
// (/groups/:name/edit) a resilience group. The GET /groups/{name} endpoint
// returns the contract Group + name (GroupView) directly, so seeding is a
// straight copy. Groups are a topology resource: dry-run preview before save,
// type-to-confirm not required (medium blast radius — only bindings target it).

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
  NumberField,
  SelectField,
  CheckboxField,
  StringListEditor,
  type SelectOption,
} from "@/components/forms/field-atoms"
import { ProviderSelectField } from "@/components/forms/provider-select"
import { stringsFromList } from "@/components/forms/field-helpers"
import { ErrorBanner, PreviewBanner, DeleteDialog } from "@/components/forms/write-atoms"
import { classifyWriteError, type EditorError } from "@/lib/write-error"
import {
  createGroup,
  replaceGroup,
  previewGroup,
  deleteGroup,
  useGroup,
  type GroupView,
  type GroupWriteBody,
  type Target,
  type CircuitBreakerConfig,
  type PreviewResult,
} from "@/lib/config-api"
import { APIError, UnauthorizedError } from "@/lib/api"

const MODE_OPTIONS: SelectOption[] = [
  { value: "failover", label: "failover (ordered — try targets in sequence)" },
  { value: "load_balance", label: "load_balance (weighted random)" },
  { value: "load_balance_with_failover", label: "load_balance_with_failover" },
]

type TargetDraft = {
  provider: string
  alias: string
  path: string
  weight: number | null
}

type GroupFormState = {
  name: string
  mode: string
  failureStatusCodes: string[]
  strictWeights: boolean
  cbEnabled: boolean
  // cbConfig carries the existing circuit-breaker tuning (thresholds,
  // cooldown) so an edit that merely toggles enabled does not wipe it.
  cbConfig: CircuitBreakerConfig | null
  responseHeaderTimeoutSeconds: number | null
  targets: TargetDraft[]
}

function emptyForm(): GroupFormState {
  return {
    name: "",
    mode: "failover",
    failureStatusCodes: [],
    strictWeights: false,
    cbEnabled: false,
    cbConfig: null,
    responseHeaderTimeoutSeconds: null,
    targets: [],
  }
}

function formFromView(v: GroupView): GroupFormState {
  return {
    name: v.name,
    mode: v.mode || "failover",
    failureStatusCodes: (v.failure_status_codes ?? []).map((n) => String(n)),
    strictWeights: v.strict_weights ?? false,
    cbEnabled: v.circuit_breaker?.enabled ?? false,
    cbConfig: v.circuit_breaker ?? null,
    responseHeaderTimeoutSeconds: v.response_header_timeout_seconds ?? null,
    targets: v.targets.map((t) => ({
      provider: t.provider,
      alias: t.alias ?? "",
      path: t.path ?? "",
      weight: t.weight ?? null,
    })),
  }
}

function toWriteBody(form: GroupFormState): GroupWriteBody {
  const targets: Target[] = form.targets
    .filter((t) => t.provider.trim() !== "")
    .map((t) => {
      const out: Target = { provider: t.provider.trim() }
      if (t.alias.trim()) out.alias = t.alias.trim()
      if (t.path.trim()) out.path = t.path.trim()
      if (t.weight != null && t.weight > 0) out.weight = t.weight
      return out
    })

  const body: GroupWriteBody = {
    name: form.name.trim(),
    mode: form.mode,
    targets,
  }
  const codes = stringsFromList(form.failureStatusCodes)
    .map((s) => Number(s))
    .filter((n) => Number.isFinite(n) && n > 0)
  if (codes.length > 0) body.failure_status_codes = codes
  if (form.strictWeights) body.strict_weights = true
  if (form.cbEnabled && form.cbConfig) {
    // Preserve the existing tuning (thresholds, cooldown, sampling window);
    // only flip enabled. A breaker needs tuning to be valid (the server
    // rejects all-zero thresholds), so without an existing cbConfig there is
    // nothing valid to attach — tuning is authored in YAML, the editor only
    // toggles enable/disable and round-trips the rest.
    body.circuit_breaker = { ...form.cbConfig, enabled: true }
  }
  if (form.responseHeaderTimeoutSeconds != null && form.responseHeaderTimeoutSeconds > 0) {
    body.response_header_timeout_seconds = form.responseHeaderTimeoutSeconds
  }
  return body
}

export function GroupEditorPage({ mode }: { mode: "create" | "edit" }) {
  if (mode === "create") return <CreateGroupPage />
  return <EditGroupPage />
}

function CreateGroupPage() {
  const nav = useNavigate()
  const [form, setForm] = useState<GroupFormState>(emptyForm)
  return (
    <EditorBody
      title="New group"
      sub="A named resilience unit — an ordered or weighted set of provider targets the orchestrator routes across."
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
      setForm(formFromView(state.data))
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
      setPreview(await previewGroup(urlName, toWriteBody(form)))
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

      <PanelCard>
        <PanelHead title="Policy" sub="orchestration mode and failure accounting" />
        <div className="px-4 py-4 flex flex-col gap-3">
          <TextField
            label="Name"
            value={form.name}
            onChange={(v) => setForm({ ...form, name: v })}
            placeholder="qwen-pool"
            mono
            hint={nameEditable ? "Unique across groups. Referenced by configuration bindings." : "Names are immutable post-create."}
          />
          <SelectField
            label="Mode"
            value={form.mode}
            options={MODE_OPTIONS}
            onChange={(m) => setForm({ ...form, mode: m })}
          />
          <StringListEditor
            label="Failure status codes"
            values={form.failureStatusCodes}
            onChange={(v) => setForm({ ...form, failureStatusCodes: v })}
            placeholder="503"
            addLabel="+ Add code"
            hint="Upstream statuses treated as failures. Empty = 5xx is a failure."
          />
          <div className="flex flex-col gap-2">
            <CheckboxField
              label="Circuit breaker enabled"
              checked={form.cbEnabled}
              onChange={(c) => setForm({ ...form, cbEnabled: c })}
              hint="Skip a dead provider across every group that includes it."
            />
            <CheckboxField
              label="Strict weights"
              checked={form.strictWeights}
              onChange={(c) => setForm({ ...form, strictWeights: c })}
              hint="load_balance only — first weighted pick is final (no re-roll on failure)."
            />
          </div>
          <NumberField
            label="Response-header timeout (seconds)"
            value={form.responseHeaderTimeoutSeconds}
            onChange={(n) => setForm({ ...form, responseHeaderTimeoutSeconds: n })}
            placeholder="(gateway default)"
            hint="Per-attempt override so this group fails over off a slow target faster."
          />
        </div>
      </PanelCard>

      <PanelCard>
        <PanelHead
          title="Targets"
          sub={`providers this group routes across · ${form.targets.length}`}
          action={
            <Button
              type="button"
              variant="outline"
              size="sm"
              onClick={() => setForm({ ...form, targets: [...form.targets, { provider: "", alias: "", path: "", weight: null }] })}
            >
              + Add target
            </Button>
          }
        />
        <div className="px-4 py-4 flex flex-col gap-3">
          {form.targets.length === 0 && (
            <div className="text-[12.5px] text-[color:var(--text-4)]">No targets — a group needs at least one to validate.</div>
          )}
          {form.targets.map((t, i) => (
            <TargetCard
              key={i}
              index={i}
              total={form.targets.length}
              draft={t}
              mode={form.mode}
              onChange={(next) => {
                const copy = form.targets.slice()
                copy[i] = next
                setForm({ ...form, targets: copy })
              }}
              onRemove={() => {
                const copy = form.targets.slice()
                copy.splice(i, 1)
                setForm({ ...form, targets: copy })
              }}
              onMoveUp={() => {
                if (i === 0) return
                const copy = form.targets.slice()
                ;[copy[i - 1], copy[i]] = [copy[i], copy[i - 1]]
                setForm({ ...form, targets: copy })
              }}
              onMoveDown={() => {
                if (i === form.targets.length - 1) return
                const copy = form.targets.slice()
                ;[copy[i], copy[i + 1]] = [copy[i + 1], copy[i]]
                setForm({ ...form, targets: copy })
              }}
            />
          ))}
          {form.mode === "failover" && form.targets.length > 1 && (
            <div className="text-[11px] text-[color:var(--text-4)]">
              Failover order follows the list order — top target is tried first.
            </div>
          )}
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

function TargetCard({
  index,
  total,
  draft,
  mode,
  onChange,
  onRemove,
  onMoveUp,
  onMoveDown,
}: {
  index: number
  total: number
  draft: TargetDraft
  mode: string
  onChange: (next: TargetDraft) => void
  onRemove: () => void
  onMoveUp: () => void
  onMoveDown: () => void
}) {
  const weighted = mode !== "failover"
  return (
    <div className="rounded-[var(--radius)] border border-[color:var(--border)] overflow-hidden">
      <div className="px-3 py-2 border-b border-[color:var(--border)] bg-[color:var(--bg-2)] flex items-center gap-2">
        <span className="text-[11px] text-[color:var(--text-4)] mono w-5 text-right">{index + 1}</span>
        <Tag variant="default"><span className="mono">{draft.provider || "provider"}</span></Tag>
        <div className="ml-auto flex items-center gap-1">
          <Button type="button" size="xs" variant="ghost" onClick={onMoveUp} disabled={index === 0}>↑</Button>
          <Button type="button" size="xs" variant="ghost" onClick={onMoveDown} disabled={index === total - 1}>↓</Button>
          <Button type="button" size="xs" variant="ghost" onClick={onRemove} className="text-[color:var(--text-3)] hover:text-[color:var(--err)]">Remove</Button>
        </div>
      </div>
      <div className="px-3 py-3 grid grid-cols-1 sm:grid-cols-2 gap-3">
        <ProviderSelectField label="Provider" value={draft.provider} onChange={(v) => onChange({ ...draft, provider: v })} />
        <TextField label="Alias (model rewrite)" value={draft.alias} onChange={(v) => onChange({ ...draft, alias: v })} placeholder="gpt-4o" mono hint="Rewrites the request model when this target is picked." />
        <TextField label="Path override" value={draft.path} onChange={(v) => onChange({ ...draft, path: v })} placeholder="" mono />
        {weighted && (
          <NumberField label="Weight" value={draft.weight} onChange={(n) => onChange({ ...draft, weight: n })} placeholder="1" hint="Relative selection weight. Zero = even." />
        )}
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
