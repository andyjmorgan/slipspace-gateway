import { useEffect, useState } from "react"
import { Link, useNavigate } from "react-router"
import { Plus } from "lucide-react"
import { Button } from "@/components/ui/button"
import { PageHeader, LoadingPanel, ErrorPanel, EmptyPanel } from "@/components/atoms/page-states"
import { GroupListTable } from "@/components/config-views/group-views"
import { groupListRowFromContract, type GroupListRow } from "@/components/config-views/group-views-model"
import { apiFetch, apiErrorText, UnauthorizedError } from "../lib/api"
import { KIND_META } from "../lib/kinds"
import { PublishBar } from "../components/publish-bar"
import type { ConfigEntity } from "../lib/types"

// CpGroupListPage is the control-plane groups list — the shared GroupListTable
// fed from the CP entity store (same per-group cards the gateway console renders
// from its live /policies API, minus the live circuit state the CP can't see).
// Each card's Edit links to the CP group editor; groups have no detail page.
export function CpGroupListPage() {
  const meta = KIND_META.group
  const nav = useNavigate()
  const [rows, setRows] = useState<GroupListRow[] | null>(null)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    apiFetch<ConfigEntity[]>("/api/v1/config/entities")
      .then((all) =>
        setRows(
          all
            .filter((e) => e.kind === "group")
            .map((e) => groupListRowFromContract(e.name, e.body as Record<string, unknown>)),
        ),
      )
      .catch((e) => {
        if (e instanceof UnauthorizedError) return nav("/login", { replace: true })
        setError(apiErrorText(e))
      })
  }, [nav])

  return (
    <div>
      <PageHeader
        title={meta.title}
        sub={meta.sub}
        action={
          <div className="flex items-start gap-2">
            <Link to="/groups/new">
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
      {!error && rows !== null && rows.length === 0 && <EmptyPanel message="No groups yet." />}
      {!error && rows !== null && rows.length > 0 && (
        <div className="space-y-3">
          <GroupListTable rows={rows} editHrefFor={(name) => `/groups/${encodeURIComponent(name)}`} />
        </div>
      )}
    </div>
  )
}
