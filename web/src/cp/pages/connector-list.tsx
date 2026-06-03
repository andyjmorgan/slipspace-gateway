import { useEffect, useState } from "react"
import { Link, useNavigate } from "react-router"
import { Plus } from "lucide-react"
import { Button } from "@/components/ui/button"
import { PageHeader, LoadingPanel, ErrorPanel, EmptyPanel } from "@/components/atoms/page-states"
import { ConnectorListTable } from "@/components/config-views/connector-views"
import { connectorListItemFromContract, type ConnectorListItem } from "@/components/config-views/connector-views-model"
import { apiFetch, apiErrorText, UnauthorizedError } from "../lib/api"
import { KIND_META } from "../lib/kinds"
import { PublishBar } from "../components/publish-bar"
import type { ConfigEntity } from "../lib/types"

// CpConnectorListPage is the control-plane connectors list — the shared
// ConnectorListTable fed from the CP entity store (same view the gateway console
// renders from its live API). Connectors have no detail page, so names link
// straight to the CP connector editor.
export function CpConnectorListPage() {
  const meta = KIND_META.connector
  const nav = useNavigate()
  const [rows, setRows] = useState<ConnectorListItem[] | null>(null)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    apiFetch<ConfigEntity[]>("/api/v1/config/entities")
      .then((all) =>
        setRows(
          all
            .filter((e) => e.kind === "connector")
            .map((e) => connectorListItemFromContract(e.name, e.body as Record<string, unknown>)),
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
            <Link to="/connectors/new">
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
      {!error && rows !== null && rows.length === 0 && <EmptyPanel message="No connectors yet." />}
      {!error && rows !== null && rows.length > 0 && (
        <ConnectorListTable rows={rows} hrefFor={(name) => `/connectors/${encodeURIComponent(name)}`} />
      )}
    </div>
  )
}
