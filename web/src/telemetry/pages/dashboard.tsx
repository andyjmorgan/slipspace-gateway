import { useEffect, useState } from "react"
import type { DashboardSummary } from "@/lib/dashboard"
import { Card } from "@/components/ui/card"
import { Button } from "@/components/ui/button"
import { apiFetch } from "../lib/api"

const WINDOWS = ["1h", "24h", "7d"] as const
type Win = (typeof WINDOWS)[number]

function Stat({ label, value }: { label: string; value: string }) {
  return (
    <Card className="p-4">
      <div className="text-muted-foreground text-xs uppercase tracking-wide">{label}</div>
      <div className="mt-1 text-2xl font-semibold tabular-nums">{value}</div>
    </Card>
  )
}

export function DashboardPage() {
  const [window, setWindow] = useState<Win>("24h")
  const [data, setData] = useState<DashboardSummary | null>(null)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    let cancelled = false
    setError(null)
    apiFetch<DashboardSummary>(`/api/v1/dashboard/summary?window=${window}`)
      .then((d) => {
        if (!cancelled) setData(d)
      })
      .catch((e: unknown) => {
        if (!cancelled) setError(e instanceof Error ? e.message : String(e))
      })
    return () => {
      cancelled = true
    }
  }, [window])

  return (
    <div className="space-y-6">
      <div className="flex items-center gap-2">
        <h2 className="text-lg font-semibold">Fleet dashboard</h2>
        <div className="ml-auto flex gap-1">
          {WINDOWS.map((w) => (
            <Button key={w} size="sm" variant={w === window ? "default" : "outline"} onClick={() => setWindow(w)}>
              {w}
            </Button>
          ))}
        </div>
      </div>

      {error && <div className="text-destructive text-sm">Failed to load: {error}</div>}
      {!data && !error && <div className="text-muted-foreground text-sm">Loading…</div>}

      {data && (
        <>
          <div className="grid grid-cols-2 gap-3 md:grid-cols-4">
            <Stat label="Requests" value={data.totals.requests.toLocaleString()} />
            <Stat label="Error rate" value={`${(data.rates.error_rate * 100).toFixed(1)}%`} />
            <Stat label="Req/s" value={data.rates.requests_per_second.toFixed(2)} />
            <Stat label="p95 latency" value={`${data.latency_ms.p95} ms`} />
            <Stat label="Tokens in" value={data.totals.tokens_in.toLocaleString()} />
            <Stat label="Tokens out" value={data.totals.tokens_out.toLocaleString()} />
            <Stat label="Cached" value={data.totals.tokens_cached.toLocaleString()} />
            <Stat label="Success" value={data.totals.requests_success.toLocaleString()} />
          </div>

          <div className="grid grid-cols-1 gap-4 lg:grid-cols-2">
            <Breakdown
              title="By provider"
              rows={data.by_provider.map((r) => ({ key: r.provider, requests: r.requests, extra: `p95 ${r.p95_latency_ms}ms` }))}
            />
            <Breakdown
              title="By model"
              rows={data.by_model.map((r) => ({ key: r.model, requests: r.requests, extra: `${r.tokens_in.toLocaleString()} in` }))}
            />
          </div>
        </>
      )}
    </div>
  )
}

function Breakdown({ title, rows }: { title: string; rows: { key: string; requests: number; extra: string }[] }) {
  return (
    <Card className="p-4">
      <div className="mb-2 text-sm font-medium">{title}</div>
      {rows.length === 0 ? (
        <div className="text-muted-foreground text-sm">No data</div>
      ) : (
        <table className="w-full text-sm">
          <tbody>
            {rows.map((r) => (
              <tr key={r.key} className="border-t">
                <td className="py-1.5 pr-2">{r.key || "—"}</td>
                <td className="py-1.5 text-right tabular-nums">{r.requests.toLocaleString()}</td>
                <td className="text-muted-foreground py-1.5 pl-2 text-right">{r.extra}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </Card>
  )
}
