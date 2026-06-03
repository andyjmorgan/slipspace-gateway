import { useEffect, useState } from "react"
import { Link, useNavigate } from "react-router"
import { Plus } from "lucide-react"
import { Button } from "@/components/ui/button"
import { PageHeader, LoadingPanel, ErrorPanel, EmptyPanel } from "@/components/atoms/page-states"
import { ApiKeyListTable } from "@/components/config-views/api-key-views"
import { apiKeyListItemFromContract, type ApiKeyListItem } from "@/components/config-views/api-key-views-model"
import { apiFetch, apiErrorText, UnauthorizedError } from "../lib/api"
import { KIND_META } from "../lib/kinds"
import { PublishBar } from "../components/publish-bar"
import type { ConfigEntity } from "../lib/types"

// CpApiKeyListPage is the control-plane api-keys list — the shared
// ApiKeyListTable fed from the CP entity store (same view the gateway console
// renders from its live API). Names link to the CP api-key editor; the secret
// stays out of the row entirely (the mapper drops it) so no key value is ever
// rendered in the control plane.
export function CpApiKeyListPage() {
  const meta = KIND_META.api_key
  const nav = useNavigate()
  const [rows, setRows] = useState<ApiKeyListItem[] | null>(null)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    apiFetch<ConfigEntity[]>("/api/v1/config/entities")
      .then((all) =>
        setRows(
          all
            .filter((e) => e.kind === "api_key")
            .map((e) => apiKeyListItemFromContract(e.name, e.body as Record<string, unknown>)),
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
            <Link to="/api-keys/new">
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
      {!error && rows !== null && rows.length === 0 && <EmptyPanel message="No API keys yet." />}
      {!error && rows !== null && rows.length > 0 && (
        <ApiKeyListTable rows={rows} hrefFor={(name) => `/api-keys/${encodeURIComponent(name)}`} />
      )}
    </div>
  )
}
