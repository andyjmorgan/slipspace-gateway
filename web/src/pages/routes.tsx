import { useMemo, useState } from "react"
import { Link } from "react-router"
import { useRoutes } from "@/lib/config-api"
import { PanelCard, PanelHead } from "@/components/atoms/card"
import { Tag } from "@/components/atoms/tag"
import { ProviderChip } from "@/components/atoms/provider-chip"
import { Input } from "@/components/ui/input"
import {
  PageHeader,
  LoadingPanel,
  ErrorPanel,
  EmptyPanel,
  useUnauthorizedRedirect,
} from "@/components/atoms/page-states"

export function RoutesPage() {
  const { state } = useRoutes()
  useUnauthorizedRedirect(state)

  const [filter, setFilter] = useState("")
  const filtered = useMemo(() => {
    if (state.status !== "ok") return []
    const q = filter.trim().toLowerCase()
    if (!q) return state.data
    return state.data.filter((r) =>
      r.path.toLowerCase().includes(q) ||
      r.provider.toLowerCase().includes(q) ||
      r.endpoint.toLowerCase().includes(q),
    )
  }, [state, filter])

  return (
    <div>
      <PageHeader
        title="Routes"
        sub="The flattened path table the routing middleware matches on every request — each URL the gateway accepts and the (provider, endpoint) pair that owns it."
      />
      {state.status === "loading" && <LoadingPanel />}
      {state.status === "error" && <ErrorPanel message={state.message} />}
      {state.status === "ok" && state.data.length === 0 && (
        <EmptyPanel message="No routes registered." />
      )}
      {state.status === "ok" && state.data.length > 0 && (
        <PanelCard>
          <PanelHead
            title={`${filtered.length} of ${state.data.length} routes`}
            sub="filters across path, provider, and endpoint"
            action={
              <Input
                placeholder="filter…"
                value={filter}
                onChange={(e) => setFilter(e.target.value)}
                className="h-7 w-48 text-[12px]"
              />
            }
          />
          <table className="w-full text-[12.5px]">
            <thead>
              <tr className="text-[11px] uppercase tracking-[0.07em] text-[color:var(--text-3)]">
                <th className="text-left font-medium px-4 py-2">Path</th>
                <th className="text-left font-medium px-4 py-2">Provider</th>
                <th className="text-left font-medium px-4 py-2">Endpoint</th>
                <th className="text-left font-medium px-4 py-2">Methods</th>
              </tr>
            </thead>
            <tbody>
              {filtered.map((r) => (
                <tr key={`${r.path}::${r.provider}::${r.endpoint}`} className="border-t border-[color:var(--border)] hover:bg-[color:var(--hover)]">
                  <td className="mono px-4 py-2.5 text-[color:var(--text)]">{r.path}</td>
                  <td className="px-4 py-2.5">
                    <Link to={`/providers/${encodeURIComponent(r.provider)}`}>
                      <ProviderChip name={r.provider} />
                    </Link>
                  </td>
                  <td className="mono px-4 py-2.5 text-[color:var(--text-2)]">{r.endpoint}</td>
                  <td className="px-4 py-2.5">
                    <div className="flex gap-1 flex-wrap">
                      {r.methods.map((m) => (
                        <Tag key={m} variant="default"><span className="mono">{m}</span></Tag>
                      ))}
                    </div>
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
