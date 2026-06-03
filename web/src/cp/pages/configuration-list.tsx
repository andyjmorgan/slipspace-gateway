import { useEffect, useState } from "react"
import { Link, useNavigate } from "react-router"
import { Plus } from "lucide-react"
import { Button } from "@/components/ui/button"
import { PageHeader, LoadingPanel, ErrorPanel, EmptyPanel } from "@/components/atoms/page-states"
import { ConfigurationListTable } from "@/components/config-views/configuration-views"
import {
  configurationListItemFromContract,
  type ConfigurationListItem,
} from "@/components/config-views/configuration-views-model"
import { apiFetch, apiErrorText, UnauthorizedError } from "../lib/api"
import { KIND_META } from "../lib/kinds"
import { PublishBar } from "../components/publish-bar"
import type { ConfigEntity } from "../lib/types"

// CpConfigurationListPage is the control-plane configurations list — the shared
// ConfigurationListTable fed from the CP entity store (same view the gateway
// console renders from its live API). Names link to the CP configuration detail
// page.
export function CpConfigurationListPage() {
  const meta = KIND_META.configuration
  const nav = useNavigate()
  const [rows, setRows] = useState<ConfigurationListItem[] | null>(null)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    apiFetch<ConfigEntity[]>("/api/v1/config/entities")
      .then((all) =>
        setRows(
          all
            .filter((e) => e.kind === "configuration")
            .map((e) => configurationListItemFromContract(e.name, e.body as Record<string, unknown>)),
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
            <Link to="/configurations/new">
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
      {!error && rows !== null && rows.length === 0 && <EmptyPanel message="No configurations yet." />}
      {!error && rows !== null && rows.length > 0 && <ConfigurationListTable rows={rows} />}
    </div>
  )
}
