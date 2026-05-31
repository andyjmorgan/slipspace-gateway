import { Link, useParams } from "react-router"
import { useBackend, type PassthroughFamilyRow } from "@/lib/config-api"
import { PanelCard, PanelHead, TableScroll } from "@/components/atoms/card"
import { Tag } from "@/components/atoms/tag"
import { ProviderChip } from "@/components/atoms/provider-chip"
import {
  PageHeader,
  LoadingPanel,
  ErrorPanel,
  NotFoundPanel,
  useUnauthorizedRedirect,
} from "@/components/atoms/page-states"

export function BackendDetailPage() {
  const { name } = useParams<{ name: string }>()
  const { state } = useBackend(name)
  useUnauthorizedRedirect(state)

  if (state.status === "loading") return <Wrap name={name}><LoadingPanel /></Wrap>
  if (state.status === "error") return <Wrap name={name}><ErrorPanel message={state.message} /></Wrap>
  if (state.status === "not_found") return <Wrap name={name}><NotFoundPanel kind="backend" name={name} /></Wrap>
  if (state.status !== "ok") return null

  const b = state.data
  return (
    <div className="flex flex-col gap-4">
      <PageHeader
        title={b.name}
        sub={
          <div className="flex items-center gap-2 mt-1.5 flex-wrap">
            <ProviderChip name={b.name} />
            {b.protocols.map((p) => (
              <Tag key={p.name} variant="ghost"><span className="mono">{p.name}</span></Tag>
            ))}
            {b.passthrough && b.passthrough.length > 0 && <Tag variant="violet">passthrough</Tag>}
          </div>
        }
        action={
          <Link to="/backends" className="text-[12.5px] text-[color:var(--text-3)] hover:underline">
            ← back to all backends
          </Link>
        }
      />

      <PanelCard>
        <PanelHead title="Base URL" />
        <div className="px-4 py-3">
          <span className="mono text-[12.5px]">{b.base_url}</span>
        </div>
      </PanelCard>

      {b.required_headers && Object.keys(b.required_headers).length > 0 && (
        <KeyValueCard
          title="Required headers"
          sub="added to every request forwarded to this backend"
          keyLabel="Header"
          entries={b.required_headers}
        />
      )}

      {b.query && Object.keys(b.query).length > 0 && (
        <KeyValueCard
          title="Query parameters"
          sub="appended to every request forwarded to this backend"
          keyLabel="Parameter"
          entries={b.query}
        />
      )}

      <PanelCard>
        <PanelHead title="Protocols" sub={`${b.protocols.length} · auth overrides shown inline where a protocol sets one`} />
        <TableScroll>
          <thead>
            <tr className="text-[11px] uppercase tracking-[0.07em] text-[color:var(--text-3)]">
              <th className="text-left font-medium px-4 py-2">Name</th>
              <th className="text-left font-medium px-4 py-2">Path</th>
              <th className="text-left font-medium px-4 py-2">Auth header</th>
              <th className="text-left font-medium px-4 py-2">Auth format</th>
            </tr>
          </thead>
          <tbody>
            {b.protocols.map((p) => (
              <tr key={p.name} className="border-t border-[color:var(--border)]">
                <td className="px-4 py-2.5 mono font-medium">{p.name}</td>
                <td className="mono px-4 py-2.5 text-[color:var(--text-2)]">{p.path}</td>
                <td className="px-4 py-2.5">
                  {p.auth_header ? (
                    <Tag variant="violet"><span className="mono">{p.auth_header}</span></Tag>
                  ) : (
                    <span className="text-[color:var(--text-4)]">—</span>
                  )}
                </td>
                <td className="px-4 py-2.5">
                  {p.auth_format ? (
                    <span className="mono text-[12px] text-[color:var(--text-2)]">{p.auth_format}</span>
                  ) : (
                    <span className="text-[color:var(--text-4)]">—</span>
                  )}
                </td>
              </tr>
            ))}
          </tbody>
        </TableScroll>
      </PanelCard>

      {b.passthrough && b.passthrough.length > 0 && (
        <PanelCard>
          <PanelHead
            title="Passthrough families"
            sub={`${b.passthrough.length} · client-token traffic forwarded verbatim per family`}
          />
          <div className="flex flex-col">
            {b.passthrough.map((f) => (
              <PassthroughFamily key={f.name} family={f} />
            ))}
          </div>
        </PanelCard>
      )}
    </div>
  )
}

function KeyValueCard({
  title,
  sub,
  keyLabel,
  entries,
}: {
  title: string
  sub: string
  keyLabel: string
  entries: Record<string, string>
}) {
  return (
    <PanelCard>
      <PanelHead title={title} sub={sub} />
      <TableScroll>
        <thead>
          <tr className="text-[11px] uppercase tracking-[0.07em] text-[color:var(--text-3)]">
            <th className="text-left font-medium px-4 py-2">{keyLabel}</th>
            <th className="text-left font-medium px-4 py-2">Value</th>
          </tr>
        </thead>
        <tbody>
          {Object.entries(entries).map(([k, v]) => (
            <tr key={k} className="border-t border-[color:var(--border)]">
              <td className="mono px-4 py-2.5">{k}</td>
              <td className="mono px-4 py-2.5 text-[color:var(--text-2)]">{v}</td>
            </tr>
          ))}
        </tbody>
      </TableScroll>
    </PanelCard>
  )
}

function PassthroughFamily({ family }: { family: PassthroughFamilyRow }) {
  return (
    <div className="border-t border-[color:var(--border)] first:border-t-0 px-4 py-3">
      <div className="flex items-center gap-2 flex-wrap mb-2">
        <span className="mono font-medium text-[12.5px]">{family.name}</span>
        {family.auth_header && (
          <Tag variant="violet"><span className="mono">{family.auth_header}</span></Tag>
        )}
      </div>
      <div className="flex flex-col gap-1">
        {family.paths.map((p, i) => (
          <div key={`${p.match}::${i}`} className="flex items-center gap-2 flex-wrap">
            <span className="mono text-[12px] text-[color:var(--text-2)]">{p.match}</span>
            <div className="flex gap-1 flex-wrap">
              {p.methods.map((m) => (
                <Tag key={m} variant="default"><span className="mono">{m}</span></Tag>
              ))}
            </div>
          </div>
        ))}
      </div>
    </div>
  )
}

function Wrap({ name, children }: { name?: string; children: React.ReactNode }) {
  return (
    <div>
      <PageHeader title={name ?? "Backend"} />
      {children}
    </div>
  )
}
