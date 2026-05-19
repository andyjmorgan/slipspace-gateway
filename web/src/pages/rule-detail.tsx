import { Link, useParams } from "react-router"
import { useRule } from "@/lib/config-api"
import { PanelCard, PanelHead } from "@/components/atoms/card"
import { Tag } from "@/components/atoms/tag"
import {
  PageHeader,
  LoadingPanel,
  ErrorPanel,
  NotFoundPanel,
  useUnauthorizedRedirect,
} from "@/components/atoms/page-states"
import { UsedBy } from "@/pages/rules"

export function RuleDetailPage() {
  const { name } = useParams<{ name: string }>()
  const { state } = useRule(name)
  useUnauthorizedRedirect(state)

  if (state.status === "loading") return <Wrap name={name}><LoadingPanel /></Wrap>
  if (state.status === "error") return <Wrap name={name}><ErrorPanel message={state.message} /></Wrap>
  if (state.status === "not_found") return <Wrap name={name}><NotFoundPanel kind="rule" name={name} /></Wrap>
  if (state.status !== "ok") return null

  const r = state.data
  return (
    <div className="flex flex-col gap-4">
      <PageHeader
        title={r.name}
        sub={
          <div className="flex items-center gap-2 mt-1.5 flex-wrap">
            <Tag variant="default"><span className="mono">rule</span></Tag>
            <Tag variant="ghost"><span className="mono">priority {r.priority}</span></Tag>
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
        <PanelHead title="Condition" sub="predicate that must match for actions to fire" />
        <JsonBlock value={r.condition} />
      </PanelCard>

      <PanelCard>
        <PanelHead title="Actions" sub={`executed in order on match · ${r.actions?.length ?? 0}`} />
        {(!r.actions || r.actions.length === 0) && (
          <div className="px-4 py-6 text-[12.5px] text-[color:var(--text-4)]">No actions.</div>
        )}
        {r.actions && r.actions.length > 0 && (
          <div className="px-4 py-3 flex flex-col gap-3">
            {r.actions.map((a, i) => (
              <div key={i} className="rounded-[var(--radius)] border border-[color:var(--border)] overflow-hidden">
                <div className="px-3 py-1.5 border-b border-[color:var(--border)] bg-[color:var(--bg-2)] flex items-center gap-2">
                  <span className="text-[11px] text-[color:var(--text-4)] mono">#{i + 1}</span>
                  <span className="mono text-[12px] font-semibold">{String(a.type ?? "?")}</span>
                </div>
                <JsonBlock value={a} compact />
              </div>
            ))}
          </div>
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

function Wrap({ name, children }: { name?: string; children: React.ReactNode }) {
  return (
    <div>
      <PageHeader title={name ?? "Rule"} />
      {children}
    </div>
  )
}

function JsonBlock({ value, compact = false }: { value: unknown; compact?: boolean }) {
  return (
    <pre
      className="mono text-[11.5px] overflow-x-auto px-4 py-3 bg-[color:var(--bg-0)]"
      style={{
        margin: 0,
        borderTop: compact ? undefined : "0",
      }}
    >
      {JSON.stringify(value, null, 2)}
    </pre>
  )
}

function BehaviorBadge({ behavior }: { behavior?: string }) {
  if (!behavior || behavior === "continue") return <Tag variant="default">continue</Tag>
  if (behavior === "exit") return <Tag variant="warn">exit</Tag>
  return <Tag variant="ghost">{behavior}</Tag>
}
