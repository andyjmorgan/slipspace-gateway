import { useCallback, useEffect, useState } from "react"
import { Link, useNavigate } from "react-router"
import { Pencil, Plus, Trash2 } from "lucide-react"
import { Button } from "@/components/ui/button"
import { PanelCard, TableScroll } from "@/components/atoms/card"
import { PageHeader, LoadingPanel, ErrorPanel, EmptyPanel } from "@/components/atoms/page-states"
import { fmt } from "@/lib/fmt"
import { apiFetch, apiErrorText, UnauthorizedError } from "../lib/api"
import { deleteEntity } from "../lib/config-api"
import { KIND_META } from "../lib/kinds"
import { PublishBar } from "../components/publish-bar"
import type { ConfigEntity, EntityKind } from "../lib/types"

// CpEntityListPage is the per-type config list — same header + table chrome as
// the gateway console (PageHeader + PanelCard), driven by the CP entity store.
// One component covers every entity kind; the structured vs JSON editor is
// resolved by the route the New/edit links point at.
export function CpEntityListPage({ kind }: { kind: EntityKind }) {
  const meta = KIND_META[kind]
  const nav = useNavigate()
  const [rows, setRows] = useState<ConfigEntity[] | null>(null)
  const [error, setError] = useState<string | null>(null)

  const load = useCallback(() => {
    apiFetch<ConfigEntity[]>("/api/v1/config/entities")
      .then((all) => setRows(all.filter((e) => e.kind === kind)))
      .catch((e) => {
        if (e instanceof UnauthorizedError) return nav("/login", { replace: true })
        setError(apiErrorText(e))
      })
  }, [kind, nav])

  useEffect(load, [load])

  const onDelete = async (e: ConfigEntity) => {
    if (!confirm(`Delete ${e.kind}/${e.name}? This edits the working set; publish to apply.`)) return
    try {
      await deleteEntity(e.kind, e.name)
      load()
    } catch (err) {
      setError(apiErrorText(err))
    }
  }

  return (
    <div>
      <PageHeader
        title={meta.title}
        sub={meta.sub}
        action={
          <div className="flex items-start gap-2">
            <Link to={`/${meta.route}/new`}>
              <Button variant="ghost" size="sm">
                <Plus />
                <span className="hidden sm:inline">New</span>
              </Button>
            </Link>
            <PublishBar />
          </div>
        }
      />

      {error && <ErrorPanel message={error} />}
      {!error && rows === null && <LoadingPanel />}
      {!error && rows !== null && rows.length === 0 && <EmptyPanel message={`No ${meta.title.toLowerCase()} yet.`} />}

      {!error && rows !== null && rows.length > 0 && (
        <PanelCard>
          <TableScroll>
            <thead>
              <tr className="text-[11px] uppercase tracking-[0.07em] text-[color:var(--text-3)]">
                <th className="text-left font-medium px-4 py-2">Name</th>
                <th className="text-left font-medium px-4 py-2">Last edited</th>
                <th className="px-4 py-2"></th>
              </tr>
            </thead>
            <tbody>
              {rows.map((e) => (
                <tr key={e.name} className="border-t border-[color:var(--border)] hover:bg-[color:var(--hover)]">
                  <td className="px-4 py-2.5">
                    <Link to={`/${meta.route}/${encodeURIComponent(e.name)}`} className="mono font-medium hover:underline">
                      {e.name}
                    </Link>
                  </td>
                  <td className="px-4 py-2.5 text-[12px] text-[color:var(--text-3)]" title={fmt.fullTime(e.updated_at)}>
                    {e.updated_by} · {fmt.ago(e.updated_at)}
                  </td>
                  <td className="px-4 py-2.5">
                    <div className="flex items-center justify-end gap-1">
                      <Link to={`/${meta.route}/${encodeURIComponent(e.name)}`}>
                        <Button variant="ghost" size="icon" aria-label="Edit"><Pencil /></Button>
                      </Link>
                      <Button variant="ghost" size="icon" aria-label="Delete" onClick={() => onDelete(e)}><Trash2 /></Button>
                    </div>
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
