import { useEffect, useState } from "react"
import { Link, useNavigate } from "react-router"
import { PanelCard, TableScroll } from "@/components/atoms/card"
import { Tag } from "@/components/atoms/tag"
import { PageHeader, LoadingPanel, ErrorPanel, EmptyPanel } from "@/components/atoms/page-states"
import { apiFetch, apiErrorText, UnauthorizedError } from "../lib/api"
import type { ConfigEntity } from "../lib/types"

interface BindingRow {
  configuration: string
  protocol: string
  models: string[]
  target: string
  alias?: string
}

// BindingsPage renders the fleet's generative routing table — read-only, since
// bindings live inside configuration entities (edit them there). Derived by
// flattening every configuration's bindings array.
export function BindingsPage() {
  const nav = useNavigate()
  const [rows, setRows] = useState<BindingRow[] | null>(null)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    apiFetch<ConfigEntity[]>("/api/v1/config/entities")
      .then((all) => {
        const out: BindingRow[] = []
        for (const e of all) {
          if (e.kind !== "configuration") continue
          const cfg = e.body as { bindings?: Array<Record<string, unknown>> }
          for (const b of cfg.bindings ?? []) {
            out.push({
              configuration: e.name,
              protocol: String(b.protocol ?? ""),
              models: Array.isArray(b.models) ? (b.models as string[]) : [],
              target: b.group ? `group:${b.group}` : `backend:${b.backend ?? ""}`,
              alias: b.alias ? String(b.alias) : undefined,
            })
          }
        }
        setRows(out)
      })
      .catch((e) => {
        if (e instanceof UnauthorizedError) return nav("/login", { replace: true })
        setError(apiErrorText(e))
      })
  }, [nav])

  return (
    <div>
      <PageHeader
        title="Bindings"
        sub="The generative routing table across all configurations — (protocol, model) → backend or group. Edit these inside their configuration."
      />
      {error && <ErrorPanel message={error} />}
      {!error && rows === null && <LoadingPanel />}
      {!error && rows !== null && rows.length === 0 && (
        <EmptyPanel message="No bindings yet — add them to a configuration." />
      )}
      {!error && rows !== null && rows.length > 0 && (
        <PanelCard>
          <TableScroll>
            <thead>
              <tr className="text-[11px] uppercase tracking-[0.07em] text-[color:var(--text-3)]">
                <th className="text-left font-medium px-4 py-2">Configuration</th>
                <th className="text-left font-medium px-4 py-2">Protocol</th>
                <th className="text-left font-medium px-4 py-2">Models</th>
                <th className="text-left font-medium px-4 py-2">Target</th>
              </tr>
            </thead>
            <tbody>
              {rows.map((r, i) => (
                <tr key={i} className="border-t border-[color:var(--border)] hover:bg-[color:var(--hover)]">
                  <td className="px-4 py-2.5">
                    <Link to={`/configurations/${encodeURIComponent(r.configuration)}`} className="mono hover:underline">
                      {r.configuration}
                    </Link>
                  </td>
                  <td className="px-4 py-2.5"><Tag variant="default"><span className="mono">{r.protocol}</span></Tag></td>
                  <td className="px-4 py-2.5">
                    <div className="flex gap-1 flex-wrap">
                      {r.models.length === 0 ? (
                        <span className="text-[color:var(--text-4)]">* (catch-all)</span>
                      ) : (
                        r.models.map((m) => <span key={m} className="mono text-[12px] text-[color:var(--text-2)]">{m}</span>)
                      )}
                    </div>
                  </td>
                  <td className="px-4 py-2.5 mono text-[12px] text-[color:var(--text-2)]">
                    {r.target}
                    {r.alias && <span className="text-[color:var(--text-4)]"> → {r.alias}</span>}
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
