import { Link, useParams } from "react-router"
import { useProvider } from "@/lib/config-api"
import { PanelCard, PanelHead } from "@/components/atoms/card"
import { Tag } from "@/components/atoms/tag"
import { ProviderChip } from "@/components/atoms/provider-chip"
import {
  PageHeader,
  LoadingPanel,
  ErrorPanel,
  NotFoundPanel,
  useUnauthorizedRedirect,
} from "@/components/atoms/page-states"

export function ProviderDetailPage() {
  const { name } = useParams<{ name: string }>()
  const { state } = useProvider(name)
  useUnauthorizedRedirect(state)

  if (state.status === "loading") return <Wrap name={name}><LoadingPanel /></Wrap>
  if (state.status === "error") return <Wrap name={name}><ErrorPanel message={state.message} /></Wrap>
  if (state.status === "not_found") return <Wrap name={name}><NotFoundPanel kind="provider" name={name} /></Wrap>
  if (state.status !== "ok") return null

  const p = state.data
  return (
    <div className="flex flex-col gap-4">
      <PageHeader
        title={p.name}
        sub={
          <div className="flex items-center gap-2 mt-1.5 flex-wrap">
            <ProviderChip name={p.name} />
            {p.prefix && (
              <Tag variant="ghost"><span className="mono">prefix /{p.prefix}</span></Tag>
            )}
            {p.prefix_required && <Tag variant="warn">prefix required</Tag>}
            {p.auth_header && (
              <Tag variant="violet"><span className="mono">auth_header {p.auth_header}</span></Tag>
            )}
          </div>
        }
        action={
          <Link to="/providers" className="text-[12.5px] text-[color:var(--text-3)] hover:underline">
            ← back to all providers
          </Link>
        }
      />

      <PanelCard>
        <PanelHead title="Base URL" />
        <div className="px-4 py-3">
          <span className="mono text-[12.5px]">{p.base_url}</span>
        </div>
      </PanelCard>

      {p.required_headers && Object.keys(p.required_headers).length > 0 && (
        <PanelCard>
          <PanelHead title="Required headers" sub="added to every request forwarded to this provider" />
          <table className="w-full text-[12.5px]">
            <thead>
              <tr className="text-[11px] uppercase tracking-[0.07em] text-[color:var(--text-3)]">
                <th className="text-left font-medium px-4 py-2">Header</th>
                <th className="text-left font-medium px-4 py-2">Value</th>
              </tr>
            </thead>
            <tbody>
              {Object.entries(p.required_headers).map(([k, v]) => (
                <tr key={k} className="border-t border-[color:var(--border)]">
                  <td className="mono px-4 py-2.5">{k}</td>
                  <td className="mono px-4 py-2.5 text-[color:var(--text-2)]">{v}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </PanelCard>
      )}

      <PanelCard>
        <PanelHead title="Endpoints" sub={`${p.endpoints.length} · auth overrides shown inline where an endpoint sets one`} />
        <table className="w-full text-[12.5px]">
          <thead>
            <tr className="text-[11px] uppercase tracking-[0.07em] text-[color:var(--text-3)]">
              <th className="text-left font-medium px-4 py-2">Name</th>
              <th className="text-left font-medium px-4 py-2">Methods</th>
              <th className="text-left font-medium px-4 py-2">Path</th>
              <th className="text-left font-medium px-4 py-2">Accepted paths</th>
              <th className="text-left font-medium px-4 py-2">Auth override</th>
              <th className="text-left font-medium px-4 py-2">Flags</th>
            </tr>
          </thead>
          <tbody>
            {p.endpoints.map((e) => (
              <tr key={e.name} className="border-t border-[color:var(--border)]">
                <td className="px-4 py-2.5 mono font-medium">{e.name}</td>
                <td className="px-4 py-2.5">
                  <div className="flex gap-1 flex-wrap">
                    {e.methods.map((m) => (
                      <Tag key={m} variant="default"><span className="mono">{m}</span></Tag>
                    ))}
                  </div>
                </td>
                <td className="mono px-4 py-2.5 text-[color:var(--text-2)]">{e.path}</td>
                <td className="px-4 py-2.5">
                  <div className="flex gap-1 flex-wrap">
                    {e.accepted_paths.map((path) => (
                      <Tag key={path} variant="ghost"><span className="mono">{path}</span></Tag>
                    ))}
                  </div>
                </td>
                <td className="px-4 py-2.5">
                  {e.auth_header ? (
                    <Tag variant="violet"><span className="mono">{e.auth_header}</span></Tag>
                  ) : (
                    <span className="text-[color:var(--text-4)]">—</span>
                  )}
                </td>
                <td className="px-4 py-2.5">
                  <div className="flex gap-1.5 flex-wrap">
                    {e.accepts_streaming && <Tag variant="success">stream</Tag>}
                    {e.prefix_optional && <Tag variant="violet">prefix-optional</Tag>}
                    {!e.accepts_streaming && !e.prefix_optional && (
                      <span className="text-[color:var(--text-4)]">—</span>
                    )}
                  </div>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </PanelCard>
    </div>
  )
}

function Wrap({ name, children }: { name?: string; children: React.ReactNode }) {
  return (
    <div>
      <PageHeader title={name ?? "Provider"} />
      {children}
    </div>
  )
}
