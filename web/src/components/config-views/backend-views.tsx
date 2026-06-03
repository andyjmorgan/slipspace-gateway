// Shared backend list + detail views, consumed by both the gateway admin
// console and the control-plane console. The gateway passes its live read-API
// data straight in (BackendSummary / BackendDetail are structurally identical
// to these view shapes); the CP maps a staged entity body via
// backend-views-model.ts. Page chrome (PageHeader, actions, loading/empty
// states) stays with the caller — these render only the data.

import { Link } from "react-router"
import { PanelCard, PanelHead, TableScroll } from "@/components/atoms/card"
import { Tag } from "@/components/atoms/tag"
import { ProviderChip } from "@/components/atoms/provider-chip"
import type { BackendListItem, BackendDetailData, PassthroughFamilyRow } from "./backend-views-model"

// BackendListTable renders the backends table — name, base URL, the protocols
// each speaks, and a passthrough marker. Each name links to the detail route.
export function BackendListTable({
  rows,
  hrefFor = (name) => `/backends/${encodeURIComponent(name)}`,
}: {
  rows: BackendListItem[]
  hrefFor?: (name: string) => string
}) {
  return (
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
          {rows.map((b) => (
            <tr key={b.name} className="border-t border-[color:var(--border)] hover:bg-[color:var(--hover)]">
              <td className="px-4 py-2.5">
                <Link to={hrefFor(b.name)}><ProviderChip name={b.name} /></Link>
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
  )
}

// BackendDetailView renders the read-only backend detail panels — base URL,
// required headers, default query, the per-protocol path + auth overrides, and
// passthrough families. The caller owns the page header + edit/delete actions.
export function BackendDetailView({ backend }: { backend: BackendDetailData }) {
  return (
    <>
      <PanelCard>
        <PanelHead title="Base URL" />
        <div className="px-4 py-3">
          <span className="mono text-[12.5px]">{backend.base_url}</span>
        </div>
      </PanelCard>

      {backend.required_headers && Object.keys(backend.required_headers).length > 0 && (
        <KeyValueCard
          title="Required headers"
          sub="added to every request forwarded to this backend"
          keyLabel="Header"
          entries={backend.required_headers}
        />
      )}

      {backend.query && Object.keys(backend.query).length > 0 && (
        <KeyValueCard
          title="Query parameters"
          sub="appended to every request forwarded to this backend"
          keyLabel="Parameter"
          entries={backend.query}
        />
      )}

      <PanelCard>
        <PanelHead title="Protocols" sub={`${backend.protocols.length} · auth overrides shown inline where a protocol sets one`} />
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
            {backend.protocols.map((p) => (
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

      {backend.passthrough && backend.passthrough.length > 0 && (
        <PanelCard>
          <PanelHead
            title="Passthrough families"
            sub={`${backend.passthrough.length} · client-token traffic forwarded verbatim per family`}
          />
          <div className="flex flex-col">
            {backend.passthrough.map((f) => (
              <PassthroughFamily key={f.name} family={f} />
            ))}
          </div>
        </PanelCard>
      )}
    </>
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
