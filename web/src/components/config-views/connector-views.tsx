// Shared connector list view, consumed by both the gateway admin console and the
// control-plane console. The gateway passes its live read-API data straight in
// (Connector is structurally identical to this view shape); the CP maps a staged
// entity body via connector-views-model.ts. Connectors have no detail page — the
// name links straight to the editor. Page chrome (PageHeader, actions,
// loading/empty states) stays with the caller — this renders only the data.

import { Link } from "react-router"
import { PanelCard, TableScroll } from "@/components/atoms/card"
import { Tag } from "@/components/atoms/tag"
import type { ConnectorListItem } from "./connector-views-model"

// ConnectorListTable renders the connectors table — name, type, the resolved
// destination, and the auth mode. Each name links to the editor route.
export function ConnectorListTable({
  rows,
  hrefFor = (name) => `/connectors/${encodeURIComponent(name)}/edit`,
}: {
  rows: ConnectorListItem[]
  hrefFor?: (name: string) => string
}) {
  return (
    <PanelCard>
      <TableScroll>
        <thead>
          <tr className="text-[11px] uppercase tracking-[0.07em] text-[color:var(--text-3)]">
            <th className="text-left font-medium px-4 py-2">Name</th>
            <th className="text-left font-medium px-4 py-2">Type</th>
            <th className="text-left font-medium px-4 py-2">Destination</th>
            <th className="text-left font-medium px-4 py-2">Auth</th>
          </tr>
        </thead>
        <tbody>
          {rows.map((c) => (
            <tr key={c.name} className="border-t border-[color:var(--border)] hover:bg-[color:var(--hover)]">
              <td className="px-4 py-2.5">
                <Link to={hrefFor(c.name)} className="mono font-medium hover:underline">{c.name}</Link>
              </td>
              <td className="px-4 py-2.5"><Tag variant="violet"><span className="mono">{c.type}</span></Tag></td>
              <td className="mono text-[12px] px-4 py-2.5 text-[color:var(--text-2)] truncate max-w-[320px]">{c.destination}</td>
              <td className="px-4 py-2.5">
                {c.auth_mode ? (
                  <Tag variant="default"><span className="mono">{c.auth_mode}</span></Tag>
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
