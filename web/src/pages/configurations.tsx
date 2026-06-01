import { Link } from "react-router"
import { useConfigurations } from "@/lib/config-api"
import { PanelCard, TableScroll } from "@/components/atoms/card"
import { Tag } from "@/components/atoms/tag"
import {
  PageHeader,
  NewButton,

  LoadingPanel,
  ErrorPanel,
  EmptyPanel,
  useUnauthorizedRedirect,
} from "@/components/atoms/page-states"

export function ConfigurationsPage() {
  const { state } = useConfigurations()
  useUnauthorizedRedirect(state)

  return (
    <div>
      <PageHeader
        title="Configurations"
        sub="Reusable policy bundles — upstream credentials, attached rules, and the API keys that resolve to each. Open one for the breakdown."
        action={
          <NewButton to="/configurations/new" label="New configuration" />
        }
      />
      {state.status === "loading" && <LoadingPanel />}
      {state.status === "error" && <ErrorPanel message={state.message} />}
      {state.status === "ok" && state.data.length === 0 && (
        <EmptyPanel message="No configurations loaded." />
      )}
      {state.status === "ok" && state.data.length > 0 && (
        <PanelCard>
          <TableScroll>
            <thead>
              <tr className="text-[11px] uppercase tracking-[0.07em] text-[color:var(--text-3)]">
                <th className="text-left font-medium px-4 py-2">Name</th>
                <th className="text-left font-medium px-4 py-2">Tags</th>
                <th className="text-right font-medium px-4 py-2">API keys</th>
                <th className="text-right font-medium px-4 py-2">Rules</th>
              </tr>
            </thead>
            <tbody>
              {state.data.map((row) => (
                <tr key={row.name} className="border-t border-[color:var(--border)] hover:bg-[color:var(--hover)]">
                  <td className="px-4 py-2.5">
                    <Link to={`/configurations/${encodeURIComponent(row.name)}`} className="mono font-medium hover:underline">
                      {row.name}
                    </Link>
                  </td>
                  <td className="px-4 py-2.5">
                    <div className="flex gap-1.5 flex-wrap">
                      {Object.entries(row.tags ?? {}).map(([k, v]) => (
                        <Tag key={k} variant="default">
                          <span className="mono">{k}={v}</span>
                        </Tag>
                      ))}
                    </div>
                  </td>
                  <td className="mono tnum px-4 py-2.5 text-right">{row.key_count}</td>
                  <td className="mono tnum px-4 py-2.5 text-right">{row.rule_count}</td>
                </tr>
              ))}
            </tbody>
          </TableScroll>
        </PanelCard>
      )}
    </div>
  )
}
