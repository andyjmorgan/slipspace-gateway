// Shared rule list + detail views, consumed by both the gateway admin console
// and the control-plane console. The gateway passes its live read-API data
// straight in (RuleSummary / RuleDetail are structurally identical to these
// view shapes); the CP maps a staged entity body via rule-views-model.ts. Page
// chrome (PageHeader, edit/delete actions, loading/empty states) stays with the
// caller — these render only the data.

import { useState } from "react"
import { Link } from "react-router"
import { PanelCard, PanelHead, TableScroll } from "@/components/atoms/card"
import { Tag } from "@/components/atoms/tag"
import { cn } from "@/lib/utils"
import { ConditionView, type RawCondition } from "@/components/atoms/condition-view"
import { ActionView, type RawAction } from "@/components/atoms/action-view"
import type { RuleListItem, RuleDetailData } from "./rule-views-model"

// RuleListTable renders the rules table — name, the one-line condition summary,
// the action-type chips, behavior, and the configurations that attach the rule.
// Each name links to the detail route.
export function RuleListTable({
  rows,
  hrefFor = (name) => `/rules/${encodeURIComponent(name)}`,
}: {
  rows: RuleListItem[]
  hrefFor?: (name: string) => string
}) {
  return (
    <PanelCard>
      <TableScroll>
        <thead>
          <tr className="text-[11px] uppercase tracking-[0.07em] text-[color:var(--text-3)]">
            <th className="text-left font-medium px-4 py-2">Name</th>
            <th className="text-left font-medium px-4 py-2">Condition</th>
            <th className="text-left font-medium px-4 py-2">Actions</th>
            <th className="text-left font-medium px-4 py-2">Behavior</th>
            <th className="text-left font-medium px-4 py-2">Used by</th>
          </tr>
        </thead>
        <tbody>
          {rows.map((r) => (
            <tr key={r.name} className="border-t border-[color:var(--border)] hover:bg-[color:var(--hover)]">
              <td className="px-4 py-2.5">
                <Link to={hrefFor(r.name)} className="mono font-medium hover:underline">{r.name}</Link>
              </td>
              <td className="mono text-[12px] px-4 py-2.5 text-[color:var(--text-2)]">
                {r.condition_summary}
              </td>
              <td className="px-4 py-2.5">
                <div className="flex gap-1.5 flex-wrap">
                  {r.action_types.map((t, i) => (
                    <Tag key={i} variant={t.startsWith("change") ? "violet" : "default"}>
                      <span className="mono">{t}</span>
                    </Tag>
                  ))}
                </div>
              </td>
              <td className="px-4 py-2.5">
                <BehaviorBadge behavior={r.behavior} />
              </td>
              <td className="px-4 py-2.5">
                <UsedBy names={r.used_by} kind="configurations" linkPrefix="/configurations" />
              </td>
            </tr>
          ))}
        </tbody>
      </TableScroll>
    </PanelCard>
  )
}

// RuleDetailView renders the read-only rule detail panels — the body (a
// visual/json toggle over the condition + ordered actions) and the
// configurations that attach the rule. The caller owns the page header +
// edit/delete actions.
export function RuleDetailView({ rule }: { rule: RuleDetailData }) {
  const [tab, setTab] = useState<Tab>("visual")
  const condition = rule.condition as RawCondition
  const actions = (rule.actions as RawAction[] | undefined) ?? []

  return (
    <>
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
          <UsedBy names={rule.used_by} kind="configurations" linkPrefix="/configurations" />
        </div>
      </PanelCard>
    </>
  )
}

// BehaviorBadge renders the rule's post-match behavior (continue / exit / other)
// as a coloured tag. Shared so both the list cell and the detail header agree.
export function BehaviorBadge({ behavior }: { behavior?: string }) {
  if (!behavior || behavior === "continue") return <Tag variant="default">continue</Tag>
  if (behavior === "exit") return <Tag variant="warn">exit</Tag>
  return <Tag variant="ghost">{behavior}</Tag>
}

// UsedBy renders the set of configurations referencing a shared library entry,
// each linking to its detail page; an empty set renders an "unattached" note.
export function UsedBy({
  names,
  kind,
  linkPrefix,
}: {
  names: string[]
  kind: string
  linkPrefix: string
}) {
  if (names.length === 0) {
    return <span className="text-[11px] text-[color:var(--text-4)]">unattached — no {kind} reference this</span>
  }
  return (
    <div className="flex gap-1.5 flex-wrap">
      {names.map((n) => (
        <Link key={n} to={`${linkPrefix}/${encodeURIComponent(n)}`}>
          <Tag variant="ghost"><span className="mono">{n}</span></Tag>
        </Link>
      ))}
    </div>
  )
}

type Tab = "visual" | "json"

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

function JsonBlock({ value }: { value: unknown }) {
  return (
    <pre className="mono text-[11.5px] overflow-x-auto px-3 py-2 bg-[color:var(--bg-0)] rounded-[var(--radius)]">
      {JSON.stringify(value ?? null, null, 2)}
    </pre>
  )
}
