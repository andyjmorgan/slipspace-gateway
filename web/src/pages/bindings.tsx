import { useMemo, useState } from "react"
import { Link } from "react-router"
import { useBindings, type BindingRow, type PassthroughBindingRow } from "@/lib/config-api"
import { PanelCard, PanelHead, TableScroll } from "@/components/atoms/card"
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

export function BindingsPage() {
  const { state } = useBindings()
  useUnauthorizedRedirect(state)

  const [filter, setFilter] = useState("")

  const generic = useMemo(() => {
    if (state.status !== "ok") return []
    const q = filter.trim().toLowerCase()
    if (!q) return state.data.bindings
    return state.data.bindings.filter((b) =>
      b.protocol.toLowerCase().includes(q) ||
      b.models.some((m) => m.toLowerCase().includes(q)) ||
      (b.provider ?? "").toLowerCase().includes(q) ||
      (b.group ?? "").toLowerCase().includes(q) ||
      (b.alias ?? "").toLowerCase().includes(q) ||
      (b.configuration ?? "").toLowerCase().includes(q),
    )
  }, [state, filter])

  const passthrough = useMemo(() => {
    if (state.status !== "ok") return []
    const q = filter.trim().toLowerCase()
    if (!q) return state.data.passthrough_bindings
    return state.data.passthrough_bindings.filter((b) =>
      b.family.toLowerCase().includes(q) ||
      b.provider.toLowerCase().includes(q) ||
      (b.configuration ?? "").toLowerCase().includes(q),
    )
  }, [state, filter])

  const totalGeneric = state.status === "ok" ? state.data.bindings.length : 0
  const totalPassthrough = state.status === "ok" ? state.data.passthrough_bindings.length : 0
  const empty = totalGeneric === 0 && totalPassthrough === 0

  return (
    <div className="flex flex-col gap-4">
      <PageHeader
        title="Bindings"
        sub="How models map to providers. Each binding routes a (protocol, models) pair to a single provider or a group; passthrough bindings route a client-token family verbatim to one provider."
      />
      {state.status === "loading" && <LoadingPanel />}
      {state.status === "error" && <ErrorPanel message={state.message} />}
      {state.status === "ok" && empty && <EmptyPanel message="No bindings configured." />}
      {state.status === "ok" && !empty && (
        <>
          <PanelCard>
            <PanelHead
              title={`${generic.length} of ${totalGeneric} bindings`}
              sub="filters across protocol, models, provider, group, alias, and configuration"
              action={
                <Input
                  placeholder="filter…"
                  value={filter}
                  onChange={(e) => setFilter(e.target.value)}
                  className="h-7 w-48 text-[12px]"
                />
              }
            />
            <TableScroll>
              <thead>
                <tr className="text-[11px] uppercase tracking-[0.07em] text-[color:var(--text-3)]">
                  <th className="text-left font-medium px-4 py-2">Configuration</th>
                  <th className="text-left font-medium px-4 py-2">Protocol</th>
                  <th className="text-left font-medium px-4 py-2">Models</th>
                  <th className="text-left font-medium px-4 py-2">Provider / Group</th>
                  <th className="text-left font-medium px-4 py-2">Alias</th>
                </tr>
              </thead>
              <tbody>
                {generic.map((b, i) => (
                  <BindingRowView key={`${b.configuration ?? ""}::${b.protocol}::${i}`} b={b} />
                ))}
              </tbody>
            </TableScroll>
          </PanelCard>

          {totalPassthrough > 0 && (
            <PanelCard>
              <PanelHead
                title={`${passthrough.length} of ${totalPassthrough} passthrough bindings`}
                sub="client-token families forwarded verbatim to a provider"
              />
              <TableScroll>
                <thead>
                  <tr className="text-[11px] uppercase tracking-[0.07em] text-[color:var(--text-3)]">
                    <th className="text-left font-medium px-4 py-2">Configuration</th>
                    <th className="text-left font-medium px-4 py-2">Family</th>
                    <th className="text-left font-medium px-4 py-2">Provider</th>
                    <th className="text-left font-medium px-4 py-2">Tags</th>
                  </tr>
                </thead>
                <tbody>
                  {passthrough.map((b, i) => (
                    <PassthroughRowView key={`${b.configuration ?? ""}::${b.family}::${i}`} b={b} />
                  ))}
                </tbody>
              </TableScroll>
            </PanelCard>
          )}
        </>
      )}
    </div>
  )
}

function BindingRowView({ b }: { b: BindingRow }) {
  return (
    <tr className="border-t border-[color:var(--border)] hover:bg-[color:var(--hover)]">
      <td className="px-4 py-2.5">
        {b.configuration ? (
          <Link to={`/configurations/${encodeURIComponent(b.configuration)}`} className="mono hover:underline">
            {b.configuration}
          </Link>
        ) : (
          <span className="text-[color:var(--text-4)]">—</span>
        )}
      </td>
      <td className="mono px-4 py-2.5 text-[color:var(--text-2)]">{b.protocol}</td>
      <td className="px-4 py-2.5">
        <div className="flex gap-1 flex-wrap">
          {b.models.length === 0 ? (
            <Tag variant="ghost">any</Tag>
          ) : (
            b.models.map((m) => (
              <Tag key={m} variant="default"><span className="mono">{m}</span></Tag>
            ))
          )}
        </div>
      </td>
      <td className="px-4 py-2.5">
        {b.provider ? (
          <Link to={`/providers/${encodeURIComponent(b.provider)}`}><ProviderChip name={b.provider} /></Link>
        ) : b.group ? (
          <Tag variant="violet"><span className="mono">group {b.group}</span></Tag>
        ) : (
          <span className="text-[color:var(--text-4)]">—</span>
        )}
      </td>
      <td className="mono px-4 py-2.5 text-[color:var(--text-2)]">
        {b.alias ?? <span className="text-[color:var(--text-4)]">—</span>}
      </td>
    </tr>
  )
}

function PassthroughRowView({ b }: { b: PassthroughBindingRow }) {
  return (
    <tr className="border-t border-[color:var(--border)] hover:bg-[color:var(--hover)]">
      <td className="px-4 py-2.5">
        {b.configuration ? (
          <Link to={`/configurations/${encodeURIComponent(b.configuration)}`} className="mono hover:underline">
            {b.configuration}
          </Link>
        ) : (
          <span className="text-[color:var(--text-4)]">—</span>
        )}
      </td>
      <td className="mono px-4 py-2.5 text-[color:var(--text-2)]">{b.family}</td>
      <td className="px-4 py-2.5">
        <Link to={`/providers/${encodeURIComponent(b.provider)}`}><ProviderChip name={b.provider} /></Link>
      </td>
      <td className="px-4 py-2.5">
        <div className="flex gap-1 flex-wrap">
          {(b.tags ?? []).length === 0 ? (
            <span className="text-[color:var(--text-4)]">—</span>
          ) : (
            (b.tags ?? []).map((t) => (
              <Tag key={t} variant="ghost"><span className="mono">{t}</span></Tag>
            ))
          )}
        </div>
      </td>
    </tr>
  )
}
