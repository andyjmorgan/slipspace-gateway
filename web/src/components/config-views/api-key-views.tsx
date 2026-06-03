// Shared api-key list view, consumed by both the gateway admin console and the
// control-plane console. The gateway passes its live read-API data straight in
// (APIKeyListItem is a structural superset of ApiKeyListItem — it adds the
// minted id and the redacted secret); the CP maps a staged entity body via
// api-key-views-model.ts. Page chrome (PageHeader, actions, loading/empty
// states) stays with the caller — this renders only the data.
//
// SECURITY: this table renders name / configuration / enabled only — never a
// secret value. The gateway's per-row secret reveal + lifecycle actions are
// gateway-only behavior injected through the `extraHead` / `renderTrailing`
// slots; the CP omits them entirely, so no secret ever flows through the shared
// component or the control plane.

import { Link } from "react-router"
import { PanelCard, PanelHead, TableScroll } from "@/components/atoms/card"
import { Tag } from "@/components/atoms/tag"
import type { ApiKeyListItem } from "./api-key-views-model"

// ApiKeyListTable renders the api-keys table — name, the configuration each key
// resolves to, and its enabled state. When `hrefFor` is supplied the name links
// there (the CP points it at the key editor); otherwise the name renders plain
// (the gateway list has no detail route). The `extraHead` / `renderTrailing`
// slots let the gateway append its secret + actions columns without the CP
// inheriting any secret-reveal behavior.
export function ApiKeyListTable<Row extends ApiKeyListItem>({
  rows,
  title = "Keys",
  sub,
  hrefFor,
  rowKey = (r) => r.name,
  extraHead,
  renderTrailing,
}: {
  rows: Row[]
  title?: string
  sub?: string
  hrefFor?: (name: string) => string
  rowKey?: (row: Row) => string
  extraHead?: React.ReactNode
  renderTrailing?: (row: Row) => React.ReactNode
}) {
  return (
    <PanelCard>
      <PanelHead title={title} sub={sub} />
      <TableScroll>
        <thead>
          <tr className="text-[11px] uppercase tracking-[0.07em] text-[color:var(--text-3)]">
            <th className="text-left font-medium px-4 py-2">Name</th>
            <th className="text-left font-medium px-4 py-2">Configuration</th>
            <th className="text-left font-medium px-4 py-2">Enabled</th>
            {extraHead}
          </tr>
        </thead>
        <tbody>
          {rows.map((k) => (
            <tr key={rowKey(k)} className="border-t border-[color:var(--border)] hover:bg-[color:var(--hover)]">
              <td className="px-4 py-2.5 mono">
                {hrefFor ? (
                  <Link to={hrefFor(k.name)} className="font-medium hover:underline">{k.name}</Link>
                ) : (
                  k.name
                )}
              </td>
              <td className="px-4 py-2.5 mono text-[color:var(--text-2)]">{k.configuration}</td>
              <td className="px-4 py-2.5">
                {k.enabled ? <Tag variant="success">enabled</Tag> : <Tag variant="danger">disabled</Tag>}
              </td>
              {renderTrailing?.(k)}
            </tr>
          ))}
        </tbody>
      </TableScroll>
    </PanelCard>
  )
}
