import { useEffect, useState } from "react"
import { Link, useNavigate } from "react-router"
import { Plus } from "lucide-react"
import { Button } from "@/components/ui/button"
import { PageHeader, LoadingPanel, ErrorPanel, EmptyPanel } from "@/components/atoms/page-states"
import { BackendListTable } from "@/components/config-views/backend-views"
import { backendListItemFromContract, type BackendListItem } from "@/components/config-views/backend-views-model"
import { apiFetch, apiErrorText, UnauthorizedError } from "../lib/api"
import { KIND_META } from "../lib/kinds"
import { PublishBar } from "../components/publish-bar"
import type { ConfigEntity } from "../lib/types"

// CpBackendListPage is the control-plane backends list — the shared
// BackendListTable fed from the CP entity store (same view the gateway console
// renders from its live API). Names link to the CP backend detail page.
export function CpBackendListPage() {
  const meta = KIND_META.backend
  const nav = useNavigate()
  const [rows, setRows] = useState<BackendListItem[] | null>(null)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    apiFetch<ConfigEntity[]>("/api/v1/config/entities")
      .then((all) =>
        setRows(
          all
            .filter((e) => e.kind === "backend")
            .map((e) => backendListItemFromContract(e.name, e.body as Record<string, unknown>)),
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
            <Link to="/backends/new">
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
      {!error && rows !== null && rows.length === 0 && <EmptyPanel message="No backends yet." />}
      {!error && rows !== null && rows.length > 0 && <BackendListTable rows={rows} />}
    </div>
  )
}
