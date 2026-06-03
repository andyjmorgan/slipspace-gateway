import { useEffect, useState } from "react"
import { useNavigate } from "react-router"
import { PanelCard, TableScroll } from "@/components/atoms/card"
import { StatusPill } from "@/components/atoms/status-pill"
import { PageHeader, LoadingPanel, ErrorPanel, EmptyPanel } from "@/components/atoms/page-states"
import { fmt } from "@/lib/fmt"
import { apiFetch, apiErrorText, UnauthorizedError } from "../lib/api"

interface EventRow {
  correlation_id: string
  gateway_id: string
  configuration: string
  backend: string
  model: string
  protocol: string
  status_code: number
  latency_ms: number
  tokens_in: number
  tokens_out: number
  observed_at: string
}

// ObservabilityPage shows the fleet's recent request events — the slim per-
// request telemetry the gateways push over OTLP. Read-only; per-request body
// drill-down + receipt verification land in a follow-up.
export function ObservabilityPage() {
  const nav = useNavigate()
  const [rows, setRows] = useState<EventRow[] | null>(null)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    apiFetch<EventRow[]>("/api/v1/observability/events?limit=100")
      .then(setRows)
      .catch((e) => {
        if (e instanceof UnauthorizedError) return nav("/login", { replace: true })
        setError(apiErrorText(e))
      })
  }, [nav])

  return (
    <div>
      <PageHeader
        title="Observability"
        sub="Recent request events across the fleet — model, tokens, latency, and status, pushed by gateways over OTLP."
      />
      {error && <ErrorPanel message={error} />}
      {!error && rows === null && <LoadingPanel />}
      {!error && rows !== null && rows.length === 0 && (
        <EmptyPanel message="No request events yet. Gateways push these over OTLP as they serve traffic." />
      )}
      {!error && rows !== null && rows.length > 0 && (
        <PanelCard>
          <TableScroll>
            <thead>
              <tr className="text-[11px] uppercase tracking-[0.07em] text-[color:var(--text-3)]">
                <th className="text-left font-medium px-4 py-2">When</th>
                <th className="text-left font-medium px-4 py-2">Gateway</th>
                <th className="text-left font-medium px-4 py-2">Model</th>
                <th className="text-left font-medium px-4 py-2">Backend</th>
                <th className="text-left font-medium px-4 py-2">Status</th>
                <th className="text-right font-medium px-4 py-2">Tokens</th>
                <th className="text-right font-medium px-4 py-2">Latency</th>
              </tr>
            </thead>
            <tbody>
              {rows.map((e) => (
                <tr
                  key={e.correlation_id}
                  onClick={() => nav(`/observability/${encodeURIComponent(e.correlation_id)}`)}
                  className="border-t border-[color:var(--border)] hover:bg-[color:var(--hover)] cursor-pointer"
                >
                  <td className="px-4 py-2.5 text-[12px] text-[color:var(--text-3)]" title={fmt.fullTime(e.observed_at)}>
                    {fmt.ago(e.observed_at)}
                  </td>
                  <td className="px-4 py-2.5 mono text-[12px]">{e.gateway_id}</td>
                  <td className="px-4 py-2.5 mono text-[12px] text-[color:var(--text-2)]">{e.model || "—"}</td>
                  <td className="px-4 py-2.5 mono text-[12px] text-[color:var(--text-3)]">{e.backend || "—"}</td>
                  <td className="px-4 py-2.5"><StatusPill code={e.status_code} /></td>
                  <td className="px-4 py-2.5 text-right mono text-[12px] text-[color:var(--text-3)]">
                    {fmt.compact(e.tokens_in)} / {fmt.compact(e.tokens_out)}
                  </td>
                  <td className="px-4 py-2.5 text-right mono text-[12px] text-[color:var(--text-3)]">{fmt.ms(e.latency_ms)}</td>
                </tr>
              ))}
            </tbody>
          </TableScroll>
        </PanelCard>
      )}
    </div>
  )
}
