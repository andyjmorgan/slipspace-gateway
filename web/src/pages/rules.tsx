import { Link } from "react-router"
import { useRules } from "@/lib/config-api"
import { PanelCard } from "@/components/atoms/card"
import { Tag } from "@/components/atoms/tag"
import {
  PageHeader,
  LoadingPanel,
  ErrorPanel,
  EmptyPanel,
  useUnauthorizedRedirect,
} from "@/components/atoms/page-states"

export function RulesPage() {
  const { state } = useRules()
  useUnauthorizedRedirect(state)

  return (
    <div>
      <PageHeader
        title="Rules"
        sub="Shared library. Each rule may be referenced by many configurations — the 'used by' column shows which ones."
      />
      {state.status === "loading" && <LoadingPanel />}
      {state.status === "error" && <ErrorPanel message={state.message} />}
      {state.status === "ok" && state.data.length === 0 && (
        <EmptyPanel message="No rules loaded." />
      )}
      {state.status === "ok" && state.data.length > 0 && (
        <PanelCard>
          <table className="w-full text-[12.5px]">
            <thead>
              <tr className="text-[11px] uppercase tracking-[0.07em] text-[color:var(--text-3)]">
                <th className="text-right font-medium px-4 py-2 w-16">Priority</th>
                <th className="text-left font-medium px-4 py-2">Name</th>
                <th className="text-left font-medium px-4 py-2">Condition</th>
                <th className="text-left font-medium px-4 py-2">Actions</th>
                <th className="text-left font-medium px-4 py-2">Used by</th>
              </tr>
            </thead>
            <tbody>
              {state.data.map((r) => (
                <tr key={r.name} className="border-t border-[color:var(--border)] hover:bg-[color:var(--hover)]">
                  <td className="mono tnum text-right px-4 py-2.5">{r.priority}</td>
                  <td className="px-4 py-2.5">
                    <Link to={`/rules/${encodeURIComponent(r.name)}`} className="mono font-medium hover:underline">{r.name}</Link>
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
                    <UsedBy names={r.used_by} kind="configurations" linkPrefix="/configurations" />
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </PanelCard>
      )}
    </div>
  )
}

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
