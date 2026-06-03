// Shared group (resilience) list view, consumed by both the gateway admin
// console and the control-plane console. The gateway passes rows mapped from
// its live /policies read API (per-target circuit state + pod); the CP maps a
// staged entity body via group-views-model.ts. Page chrome (PageHeader,
// actions, loading/empty states, the gateway's pod footer) stays with the
// caller — these render only the per-group cards.

import { Link } from "react-router"
import { PanelCard, TableScroll } from "@/components/atoms/card"
import { Tag } from "@/components/atoms/tag"
import { Button } from "@/components/ui/button"
import type { GroupListRow, GroupTargetRow } from "./group-views-model"

// GroupListTable renders one card per resilience group — the header chips
// (mode, strict_weights, circuit breaker, retry codes) plus the targets table.
// Each card's Edit button links to the group editor; the href is caller-owned
// because the two consoles route to it differently (gateway /edit suffix vs CP
// bare name).
export function GroupListTable({
  rows,
  editHrefFor = (name) => `/groups/${encodeURIComponent(name)}/edit`,
}: {
  rows: GroupListRow[]
  editHrefFor?: (name: string) => string
}) {
  return (
    <>
      {rows.map((g) => (
        <GroupCard key={g.name} group={g} editHref={editHrefFor(g.name)} />
      ))}
    </>
  )
}

function GroupCard({ group, editHref }: { group: GroupListRow; editHref: string }) {
  return (
    <PanelCard>
      <div className="px-4 py-3 border-b border-[color:var(--border)] flex items-center gap-2">
        <span className="font-semibold text-[13.5px]">{group.name}</span>
        <Tag variant="violet">{group.mode}</Tag>
        {group.strict_weights && <Tag variant="warn">strict_weights</Tag>}
        {group.circuit_breaker_enabled && <Tag variant="violet">cb enabled</Tag>}
        {group.failure_status_codes && group.failure_status_codes.length > 0 && (
          <span className="mono text-[11px] text-[color:var(--text-3)]">
            retry on {group.failure_status_codes.join(", ")}
          </span>
        )}
        <Link to={editHref} className="ml-auto">
          <Button type="button" size="xs" variant="outline">Edit</Button>
        </Link>
      </div>
      <TableScroll>
        <thead>
          <tr className="text-[11px] uppercase tracking-[0.07em] text-[color:var(--text-3)]">
            <th className="text-left font-medium px-4 py-2">Target</th>
            <th className="text-left font-medium px-4 py-2">Backend</th>
            <th className="text-right font-medium px-4 py-2">Order</th>
            <th className="text-right font-medium px-4 py-2">Weight</th>
            <th className="text-left font-medium px-4 py-2">Circuit state</th>
          </tr>
        </thead>
        <tbody>
          {group.targets.map((t) => (
            <tr key={t.name} className="border-t border-[color:var(--border)]">
              <td className="px-4 py-2.5 font-medium">{t.name}</td>
              <td className="mono px-4 py-2.5 text-[color:var(--text-2)]">
                {t.backend ?? <span className="text-[color:var(--text-4)]">—</span>}
              </td>
              <td className="mono tnum text-right px-4 py-2.5">{t.order ?? "—"}</td>
              <td className="mono tnum text-right px-4 py-2.5">{t.weight ?? "—"}</td>
              <td className="px-4 py-2.5">
                {t.circuit_state ? (
                  <CircuitStateBadge state={t.circuit_state} />
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

function CircuitStateBadge({ state }: { state: GroupTargetRow["circuit_state"] }) {
  const variant: "danger" | "warn" | "success" | "ghost" =
    state === "open" ? "danger" : state === "half_open" ? "warn" : state === "closed" ? "success" : "ghost"
  return <Tag variant={variant}>{state}</Tag>
}
