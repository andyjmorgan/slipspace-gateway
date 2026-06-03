import { useCallback, useEffect, useState } from "react"
import { useNavigate } from "react-router"
import { History, RotateCcw } from "lucide-react"
import { Button } from "@/components/ui/button"
import { EmptyPanel, ErrorPanel, LoadingPanel } from "@/components/atoms/page-states"
import { fmt } from "@/lib/fmt"
import { apiErrorText, UnauthorizedError } from "../lib/api"
import { activateVersion, listChanges, listVersions } from "../lib/config-api"
import type { ConfigChange, ConfigVersion } from "../lib/types"

export function VersionsPage() {
  const nav = useNavigate()
  const [versions, setVersions] = useState<ConfigVersion[] | null>(null)
  const [changes, setChanges] = useState<ConfigChange[]>([])
  const [error, setError] = useState<string | null>(null)
  const [banner, setBanner] = useState<string | null>(null)

  const load = useCallback(() => {
    Promise.all([listVersions(), listChanges(30).catch(() => [] as ConfigChange[])])
      .then(([v, c]) => {
        setVersions(v)
        setChanges(c)
      })
      .catch((e) => {
        if (e instanceof UnauthorizedError) return nav("/login", { replace: true })
        setError(e instanceof Error ? e.message : "Failed to load versions")
      })
  }, [nav])

  useEffect(load, [load])

  const onActivate = async (v: ConfigVersion) => {
    if (!confirm(`Roll the fleet back to version ${v.id.slice(0, 8)}? Gateways converge on next fetch.`)) return
    try {
      await activateVersion(v.id)
      setBanner(`Activated version ${v.id.slice(0, 8)}`)
      load()
    } catch (e) {
      if (e instanceof UnauthorizedError) return nav("/login", { replace: true })
      setError(apiErrorText(e))
    }
  }

  return (
    <div>
      <div className="mb-4 flex items-start gap-3">
        <History size={22} className="mt-0.5 shrink-0 text-[color:var(--accent)]" />
        <div>
          <h1 className="text-[22px] font-semibold tracking-[-0.02em]">Versions</h1>
          <div className="text-[13px] text-[color:var(--text-3)] mt-1.5">
            Published config versions are immutable. Rollback re-points the active version; the fleet converges on
            its next fetch.
          </div>
        </div>
      </div>

      {banner && (
        <div
          className="mb-4 rounded-[var(--radius)] px-3 py-2 text-[13px] border"
          style={{ color: "var(--ok)", background: "var(--ok-bg)", borderColor: "color-mix(in oklab, var(--ok) 30%, var(--border))" }}
        >
          {banner}
        </div>
      )}
      {error && <div className="mb-4"><ErrorPanel message={error} /></div>}
      {versions === null && !error && <LoadingPanel />}
      {versions !== null && versions.length === 0 && (
        <EmptyPanel message="Nothing published yet. Edit entities under Config, then Publish." />
      )}

      {versions !== null && versions.length > 0 && (
        <div className="rounded-[var(--radius-lg)] border border-[color:var(--border)] bg-[color:var(--bg-1)] overflow-hidden mb-6">
          <table className="w-full text-[13px]">
            <thead>
              <tr className="text-left text-[11px] uppercase tracking-[0.07em] text-[color:var(--text-4)] border-b border-[color:var(--border)]">
                <th className="px-4 py-2.5 font-medium">Version</th>
                <th className="px-4 py-2.5 font-medium">Hash</th>
                <th className="px-4 py-2.5 font-medium">Published</th>
                <th className="px-4 py-2.5 font-medium"></th>
              </tr>
            </thead>
            <tbody>
              {versions.map((v) => (
                <tr key={v.id} className="border-b border-[color:var(--border)] last:border-0">
                  <td className="px-4 py-2.5 mono text-[12px]">
                    {v.id.slice(0, 8)}
                    {v.active && (
                      <span className="ml-2 text-[11px]" style={{ color: "var(--ok)" }}>
                        ● active
                      </span>
                    )}
                  </td>
                  <td className="px-4 py-2.5 mono text-[12px] text-[color:var(--text-3)]">{v.hash.slice(0, 16)}…</td>
                  <td className="px-4 py-2.5 text-[12px] text-[color:var(--text-3)]">
                    {fmt.fullTime(v.published_at)} · {v.published_by}
                  </td>
                  <td className="px-4 py-2.5 text-right">
                    {!v.active && (
                      <Button variant="ghost" size="sm" onClick={() => onActivate(v)}>
                        <RotateCcw />
                        <span className="hidden sm:inline">Roll back</span>
                      </Button>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      {changes.length > 0 && (
        <section>
          <h2 className="text-[13px] font-semibold uppercase tracking-[0.06em] text-[color:var(--text-3)] mb-2">
            Recent changes
          </h2>
          <div className="rounded-[var(--radius-lg)] border border-[color:var(--border)] bg-[color:var(--bg-1)] overflow-hidden">
            {changes.map((c, i) => (
              <div
                key={`${c.kind}/${c.name}/${c.changed_at}/${i}`}
                className="flex items-center gap-3 px-4 py-2 border-b border-[color:var(--border)] last:border-0 text-[12.5px]"
              >
                <span className="mono uppercase text-[10.5px] w-14 shrink-0" style={{ color: opColor(c.op) }}>
                  {c.op}
                </span>
                <span className="mono text-[color:var(--text-2)] flex-1 min-w-0 truncate">
                  {c.kind}/{c.name}
                </span>
                <span className="text-[color:var(--text-4)] shrink-0">
                  {c.changed_by} · {fmt.fullTime(c.changed_at)}
                </span>
              </div>
            ))}
          </div>
        </section>
      )}
    </div>
  )
}

function opColor(op: string): string {
  switch (op.toLowerCase()) {
    case "delete":
      return "var(--err)"
    case "create":
    case "insert":
      return "var(--ok)"
    default:
      return "var(--text-3)"
  }
}
