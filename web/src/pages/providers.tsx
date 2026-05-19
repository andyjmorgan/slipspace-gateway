import { Link } from "react-router"
import { useProviders } from "@/lib/config-api"
import { PanelCard } from "@/components/atoms/card"
import { ProviderChip } from "@/components/atoms/provider-chip"
import { Tag } from "@/components/atoms/tag"
import {
  PageHeader,
  LoadingPanel,
  ErrorPanel,
  EmptyPanel,
  useUnauthorizedRedirect,
} from "@/components/atoms/page-states"

export function ProvidersPage() {
  const { state } = useProviders()
  useUnauthorizedRedirect(state)

  return (
    <div>
      <PageHeader
        title="Providers"
        sub="Global infrastructure. Defines upstream services, prefixes, and per-endpoint auth overrides."
      />
      {state.status === "loading" && <LoadingPanel />}
      {state.status === "error" && <ErrorPanel message={state.message} />}
      {state.status === "ok" && state.data.length === 0 && (
        <EmptyPanel message="No providers configured." />
      )}
      {state.status === "ok" && state.data.length > 0 && (
        <PanelCard>
          <table className="w-full text-[12.5px]">
            <thead>
              <tr className="text-[11px] uppercase tracking-[0.07em] text-[color:var(--text-3)]">
                <th className="text-left font-medium px-4 py-2">Name</th>
                <th className="text-left font-medium px-4 py-2">Prefix</th>
                <th className="text-left font-medium px-4 py-2">Base URL</th>
                <th className="text-right font-medium px-4 py-2">Endpoints</th>
              </tr>
            </thead>
            <tbody>
              {state.data.map((p) => (
                <tr key={p.name} className="border-t border-[color:var(--border)] hover:bg-[color:var(--hover)]">
                  <td className="px-4 py-2.5">
                    <Link to={`/providers/${encodeURIComponent(p.name)}`}><ProviderChip name={p.name} /></Link>
                  </td>
                  <td className="mono px-4 py-2.5">
                    {p.prefix ? (
                      <span className="inline-flex items-center gap-1.5">
                        <span>/{p.prefix}</span>
                        {p.prefix_required && <Tag variant="warn">required</Tag>}
                      </span>
                    ) : (
                      <span className="text-[color:var(--text-4)]">—</span>
                    )}
                  </td>
                  <td className="mono px-4 py-2.5 text-[color:var(--text-2)] truncate max-w-[280px]">{p.base_url}</td>
                  <td className="mono tnum text-right px-4 py-2.5">{p.endpoint_count}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </PanelCard>
      )}
    </div>
  )
}
