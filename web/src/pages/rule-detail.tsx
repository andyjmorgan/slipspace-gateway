import { useState } from "react"
import { Link, useParams } from "react-router"
import { useRule } from "@/lib/config-api"
import { PanelCard, PanelHead } from "@/components/atoms/card"
import { Tag } from "@/components/atoms/tag"
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
          <Link to="/rules" className="text-[12.5px] text-[color:var(--text-3)] hover:underline">
            ← back to all rules
          </Link>
        }
      />

      <PanelCard>
        <PanelHead
          title="Body"
          sub="when this matches, do these actions"
          action={<Tabs value={tab} onChange={setTab} />}
        />
        {tab === "visual" ? (
          <VisualBody condition={condition} actions={actions} />
        ) : (
          <JsonBody condition={condition} actions={actions} />
        )}
      </PanelCard>

      <PanelCard>
        <PanelHead title="Used by" sub="configurations that reference this rule" />
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
      <Section title="WHEN" sub="predicate that must match for the actions to fire">
        <ConditionView condition={condition} />
      </Section>
      <Section title="THEN" sub={`${actions.length} action${actions.length === 1 ? "" : "s"} · executed in order on match`}>
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
      <Section title="condition" sub="raw condition body">
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
