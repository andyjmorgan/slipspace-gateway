import { useState } from "react"
import { Link, useNavigate, useParams } from "react-router"
import { deleteRule, useRule, type RuleConflict } from "@/lib/config-api"
import { APIError } from "@/lib/api"
import { PanelCard, PanelHead } from "@/components/atoms/card"
import { Tag } from "@/components/atoms/tag"
import { Button } from "@/components/ui/button"
import { ConditionView, type RawCondition } from "@/components/atoms/condition-view"
import { ActionView, type RawAction } from "@/components/atoms/action-view"
import { cn } from "@/lib/utils"
import {
  PageHeader,
  LoadingPanel,
  ErrorPanel,
  NotFoundPanel,
  useUnauthorizedRedirect,
} from "@/components/atoms/page-states"
import { UsedBy } from "@/pages/rules"

type Tab = "visual" | "json"

export function RuleDetailPage() {
  const { name } = useParams<{ name: string }>()
  const { state } = useRule(name)
  useUnauthorizedRedirect(state)
  const [tab, setTab] = useState<Tab>("visual")

  if (state.status === "loading") return <Wrap name={name}><LoadingPanel /></Wrap>
  if (state.status === "error") return <Wrap name={name}><ErrorPanel message={state.message} /></Wrap>
  if (state.status === "not_found") return <Wrap name={name}><NotFoundPanel kind="rule" name={name} /></Wrap>
  if (state.status !== "ok") return null

  const r = state.data
  const condition = r.condition as RawCondition
  const actions = (r.actions as RawAction[] | undefined) ?? []

  return (
    <div className="flex flex-col gap-4">
      <PageHeader
        title={r.name}
        sub={
          <div className="flex items-center gap-2 mt-1.5 flex-wrap">
            <Tag variant="default"><span className="mono">rule</span></Tag>
            <BehaviorBadge behavior={r.behavior} />
          </div>
        }
        action={
          <div className="flex items-center gap-2">
            <Link to={`/rules/${encodeURIComponent(r.name)}/edit`}>
              <Button size="sm" variant="outline">Edit</Button>
            </Link>
            <DeleteRuleButton name={r.name} />
            <Link to="/rules" className="text-[12.5px] text-[color:var(--text-3)] hover:underline ml-1">
              ← back
            </Link>
          </div>
        }
      />

      <PanelCard>
        <PanelHead
          title="Body"
          sub="the condition to match and the actions that run when it does"
          action={<Tabs value={tab} onChange={setTab} />}
        />
        {tab === "visual" ? (
          <VisualBody condition={condition} actions={actions} />
        ) : (
          <JsonBody condition={condition} actions={actions} />
        )}
      </PanelCard>

      <PanelCard>
        <PanelHead title="Used by" sub="configurations that attach this rule" />
        <div className="px-4 py-3">
          <UsedBy names={r.used_by} kind="configurations" linkPrefix="/configurations" />
        </div>
      </PanelCard>
    </div>
  )
}

function VisualBody({ condition, actions }: { condition: RawCondition; actions: RawAction[] }) {
  return (
    <div className="px-4 py-4 flex flex-col gap-4">
      <Section title="WHEN" sub="the condition that must hold for the actions to run">
        <ConditionView condition={condition} />
      </Section>
      <Section title="THEN" sub={`${actions.length} action${actions.length === 1 ? "" : "s"} · run in order when the condition matches`}>
        {actions.length === 0 ? (
          <div className="text-[12.5px] text-[color:var(--text-4)]">No actions.</div>
        ) : (
          <div className="flex flex-col gap-2.5">
            {actions.map((a, i) => (
              <ActionView key={i} action={a} index={i} />
            ))}
          </div>
        )}
      </Section>
    </div>
  )
}

function JsonBody({ condition, actions }: { condition: RawCondition; actions: RawAction[] }) {
  return (
    <div className="px-4 py-4 flex flex-col gap-3">
      <Section title="condition" sub="the raw condition JSON">
        <JsonBlock value={condition} />
      </Section>
      <Section title="actions" sub={`${actions.length} ordered`}>
        <JsonBlock value={actions} />
      </Section>
    </div>
  )
}

function Section({
  title,
  sub,
  children,
}: {
  title: string
  sub?: string
  children: React.ReactNode
}) {
  return (
    <div>
      <div className="flex items-baseline gap-2 mb-2">
        <span className="text-[11px] uppercase tracking-[0.07em] text-[color:var(--text-2)] font-semibold">
          {title}
        </span>
        {sub && <span className="text-[11px] text-[color:var(--text-4)]">{sub}</span>}
      </div>
      {children}
    </div>
  )
}

function Tabs({ value, onChange }: { value: Tab; onChange: (t: Tab) => void }) {
  return (
    <div className="inline-flex items-center rounded-[5px] border border-[color:var(--border)] bg-[color:var(--bg-2)] p-0.5 text-[11.5px]">
      {(["visual", "json"] as Tab[]).map((t) => (
        <button
          key={t}
          type="button"
          onClick={() => onChange(t)}
          className={cn(
            "px-2 py-0.5 rounded-[4px] transition-colors",
            value === t
              ? "bg-[color:var(--bg-3)] text-[color:var(--text)]"
              : "text-[color:var(--text-3)] hover:text-[color:var(--text)]",
          )}
        >
          {t}
        </button>
      ))}
    </div>
  )
}

function Wrap({ name, children }: { name?: string; children: React.ReactNode }) {
  return (
    <div>
      <PageHeader title={name ?? "Rule"} />
      {children}
    </div>
  )
}

function JsonBlock({ value }: { value: unknown }) {
  return (
    <pre className="mono text-[11.5px] overflow-x-auto px-3 py-2 bg-[color:var(--bg-0)] rounded-[var(--radius)]">
      {JSON.stringify(value ?? null, null, 2)}
    </pre>
  )
}

function BehaviorBadge({ behavior }: { behavior?: string }) {
  if (!behavior || behavior === "continue") return <Tag variant="default">continue</Tag>
  if (behavior === "exit") return <Tag variant="warn">exit</Tag>
  return <Tag variant="ghost">{behavior}</Tag>
}

// DeleteRuleButton owns the per-rule delete flow: a confirmation
// affordance (two-click — first click arms, second click commits)
// plus inline rendering of the 409 conflict envelope when the rule
// is still referenced by a configuration.
function DeleteRuleButton({ name }: { name: string }) {
  const nav = useNavigate()
  const [armed, setArmed] = useState(false)
  const [busy, setBusy] = useState(false)
  const [conflict, setConflict] = useState<RuleConflict | null>(null)
  const [error, setError] = useState<string | null>(null)

  const commit = async () => {
    setBusy(true)
    setError(null)
    setConflict(null)
    try {
      await deleteRule(name)
      nav("/rules", { replace: true })
    } catch (e) {
      if (e instanceof APIError && e.status === 409) {
        setConflict(e.body as RuleConflict)
      } else if (e instanceof Error) {
        setError(e.message)
      } else {
        setError(String(e))
      }
      setArmed(false)
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="flex flex-col items-end gap-1">
      <div className="flex items-center gap-2">
        {armed ? (
          <>
            <Button
              type="button"
              size="sm"
              variant="destructive"
              onClick={commit}
              disabled={busy}
            >
              {busy ? "Deleting…" : "Confirm delete"}
            </Button>
            <Button
              type="button"
              size="sm"
              variant="ghost"
              onClick={() => setArmed(false)}
              disabled={busy}
            >
              Cancel
            </Button>
          </>
        ) : (
          <Button
            type="button"
            size="sm"
            variant="destructive"
            onClick={() => {
              setError(null)
              setConflict(null)
              setArmed(true)
            }}
          >
            Delete
          </Button>
        )}
      </div>
      {(conflict || error) && (
        <div
          className="max-w-sm rounded-[var(--radius)] border p-2 text-[11.5px]"
          style={{
            color: "var(--err)",
            background: "var(--err-bg)",
            borderColor: "color-mix(in oklab, var(--err) 30%, var(--border))",
          }}
        >
          {conflict ? (
            <>
              <div className="font-medium">Cannot delete — still referenced</div>
              {conflict.used_by && conflict.used_by.length > 0 && (
                <div className="mt-1">
                  Unbind from: {conflict.used_by.map((c) => <span key={c} className="mono mr-1">{c}</span>)}
                </div>
              )}
            </>
          ) : (
            <div className="mono">{error}</div>
          )}
        </div>
      )}
    </div>
  )
}
