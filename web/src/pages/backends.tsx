import { Link } from "react-router"
import { useBackends } from "@/lib/config-api"
import { PanelCard, TableScroll } from "@/components/atoms/card"
import { ProviderChip } from "@/components/atoms/provider-chip"
import { Tag } from "@/components/atoms/tag"
import {
  PageHeader,
  LoadingPanel,
  ErrorPanel,
  EmptyPanel,
  useUnauthorizedRedirect,
} from "@/components/atoms/page-states"

export function BackendsPage() {
  const { state } = useBackends()
  useUnauthorizedRedirect(state)

  return (
    <div>
      <PageHeader
        title="Backends"
        sub="Upstream services the gateway forwards to, shared across every configuration — base URLs, the protocols each one speaks, and whether it accepts passthrough traffic."
      />
      {state.status === "loading" && <LoadingPanel />}
      {state.status === "error" && <ErrorPanel message={state.message} />}
      {state.status === "ok" && state.data.length === 0 && (
        <EmptyPanel message="No backends configured." />
      )}
      {state.status === "ok" && state.data.length > 0 && (
        <PanelCard>
          <TableScroll>
            <thead>
              <tr className="text-[11px] uppercase tracking-[0.07em] text-[color:var(--text-3)]">
                <th className="text-left font-medium px-4 py-2">Name</th>
                <th className="text-left font-medium px-4 py-2">Base URL</th>
                <th className="text-left font-medium px-4 py-2">Protocols</th>
                <th className="text-left font-medium px-4 py-2">Passthrough</th>
              </tr>
            </thead>
            <tbody>
              {state.data.map((b) => (
                <tr key={b.name} className="border-t border-[color:var(--border)] hover:bg-[color:var(--hover)]">
                  <td className="px-4 py-2.5">
                    <Link to={`/backends/${encodeURIComponent(b.name)}`}><ProviderChip name={b.name} /></Link>
                  </td>
                  <td className="mono px-4 py-2.5 text-[color:var(--text-2)] truncate max-w-[280px]">{b.base_url}</td>
                  <td className="px-4 py-2.5">
                    <div className="flex gap-1 flex-wrap">
                      {b.protocols.length === 0 ? (
                        <span className="text-[color:var(--text-4)]">—</span>
                      ) : (
                        b.protocols.map((p) => (
                          <Tag key={p} variant="default"><span className="mono">{p}</span></Tag>
                        ))
                      )}
                    </div>
                  </td>
                  <td className="px-4 py-2.5">
                    {b.has_passthrough ? (
                      <Tag variant="violet">passthrough</Tag>
                    ) : (
                      <span className="text-[color:var(--text-4)]">—</span>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </TableScroll>
        </PanelCard>
      )}
    </div>
  )
}
