import { useEffect, useState } from "react"
import { useNavigate } from "react-router"
import { Server } from "lucide-react"
import { apiFetch, UnauthorizedError } from "../lib/api"
import type { DriftRow, FleetGateway, FleetStatus, DriftStatus } from "../lib/types"

interface Row extends FleetGateway {
  drift?: DriftStatus
}

const STATUS_COLOR: Record<FleetStatus, string> = {
  online: "var(--ok)",
  stale: "var(--warn)",
  offline: "var(--err)",
}

const DRIFT_COLOR: Record<DriftStatus, string> = {
  current: "var(--ok)",
  drifted: "var(--warn)",
  unknown: "var(--text-4)",
}

export function FleetPage() {
  const nav = useNavigate()
  const [rows, setRows] = useState<Row[] | null>(null)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    let cancelled = false
    Promise.all([
      apiFetch<FleetGateway[]>("/api/v1/fleet"),
      apiFetch<DriftRow[]>("/api/v1/fleet/drift").catch(() => [] as DriftRow[]),
    ])
      .then(([fleet, drift]) => {
        if (cancelled) return
        const driftById = new Map(drift.map((d) => [d.gateway_id, d.status]))
        setRows(fleet.map((g) => ({ ...g, drift: driftById.get(g.id) })))
      })
      .catch((e) => {
        if (cancelled) return
        if (e instanceof UnauthorizedError) {
          nav("/login", { replace: true })
          return
        }
        setError(e instanceof Error ? e.message : "Failed to load fleet")
      })
    return () => {
      cancelled = true
    }
  }, [nav])

  return (
    <div>
      <div className="mb-4 flex items-start gap-3">
        <Server size={22} className="mt-0.5 shrink-0 text-[color:var(--accent)]" />
        <div>
          <h1 className="text-[22px] font-semibold tracking-[-0.02em]">Fleet</h1>
          <div className="text-[13px] text-[color:var(--text-3)] mt-1.5">
            Gateways registered with this control plane, their liveness, and config drift.
          </div>
        </div>
      </div>

      {error && (
        <div
          className="rounded-[var(--radius-lg)] border p-5 text-[13px]"
          style={{
            color: "var(--err)",
            background: "var(--err-bg)",
            borderColor: "color-mix(in oklab, var(--err) 30%, var(--border))",
          }}
        >
          {error}
        </div>
      )}

      {!error && rows === null && (
        <div className="rounded-[var(--radius-lg)] border border-[color:var(--border)] bg-[color:var(--bg-1)] p-8 text-center text-[13px] text-[color:var(--text-3)]">
          Loading…
        </div>
      )}

      {!error && rows !== null && rows.length === 0 && (
        <div className="rounded-[var(--radius-lg)] border border-[color:var(--border)] bg-[color:var(--bg-1)] p-8 text-center text-[13px] text-[color:var(--text-3)]">
          No gateways have registered yet.
        </div>
      )}

      {!error && rows !== null && rows.length > 0 && (
        <div className="rounded-[var(--radius-lg)] border border-[color:var(--border)] bg-[color:var(--bg-1)] overflow-hidden">
          <table className="w-full text-[13px]">
            <thead>
              <tr className="text-left text-[11px] uppercase tracking-[0.07em] text-[color:var(--text-4)] border-b border-[color:var(--border)]">
                <th className="px-4 py-2.5 font-medium">Gateway</th>
                <th className="px-4 py-2.5 font-medium">Status</th>
                <th className="px-4 py-2.5 font-medium">Version</th>
                <th className="px-4 py-2.5 font-medium">Config</th>
                <th className="px-4 py-2.5 font-medium">Last seen</th>
              </tr>
            </thead>
            <tbody>
              {rows.map((g) => (
                <tr key={g.id} className="border-b border-[color:var(--border)] last:border-0">
                  <td className="px-4 py-2.5">
                    <div className="font-medium">{g.id}</div>
                    {g.labels && Object.keys(g.labels).length > 0 && (
                      <div className="mono text-[11px] text-[color:var(--text-4)] mt-0.5">
                        {Object.entries(g.labels)
                          .map(([k, v]) => `${k}=${v}`)
                          .join("  ")}
                      </div>
                    )}
                  </td>
                  <td className="px-4 py-2.5">
                    <Pill color={STATUS_COLOR[g.status]} label={g.status} />
                  </td>
                  <td className="px-4 py-2.5 mono text-[12px] text-[color:var(--text-2)]">{g.version || "—"}</td>
                  <td className="px-4 py-2.5">
                    {g.drift ? (
                      <Pill color={DRIFT_COLOR[g.drift]} label={g.drift} />
                    ) : (
                      <span className="text-[color:var(--text-4)]">—</span>
                    )}
                  </td>
                  <td className="px-4 py-2.5 mono text-[12px] text-[color:var(--text-3)]">
                    {fmtTime(g.last_seen)}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  )
}

function Pill({ color, label }: { color: string; label: string }) {
  return (
    <span
      className="inline-flex items-center gap-1.5 text-[12px] capitalize"
      style={{ color }}
    >
      <span className="inline-block size-1.5 rounded-full" style={{ background: color }} />
      {label}
    </span>
  )
}

function fmtTime(iso: string): string {
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return iso
  return d.toLocaleString()
}
