import { useEffect, useState } from "react"
import { Link, useNavigate } from "react-router"
import { Plus } from "lucide-react"
import { Button } from "@/components/ui/button"
import { PageHeader, LoadingPanel, ErrorPanel, EmptyPanel } from "@/components/atoms/page-states"
import { RuleListTable } from "@/components/config-views/rule-views"
import { ruleListItemFromContract, type RuleListItem } from "@/components/config-views/rule-views-model"
import { apiFetch, apiErrorText, UnauthorizedError } from "../lib/api"
import { KIND_META } from "../lib/kinds"
import { PublishBar } from "../components/publish-bar"
import type { ConfigEntity } from "../lib/types"

// CpRuleListPage is the control-plane rules list — the shared RuleListTable fed
// from the CP entity store (same view the gateway console renders from its live
// API). Names link to the CP rule detail page.
export function CpRuleListPage() {
  const meta = KIND_META.rule
  const nav = useNavigate()
  const [rows, setRows] = useState<RuleListItem[] | null>(null)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    apiFetch<ConfigEntity[]>("/api/v1/config/entities")
      .then((all) =>
        setRows(
          all
            .filter((e) => e.kind === "rule")
            .map((e) => ruleListItemFromContract(e.name, e.body as Record<string, unknown>)),
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
            <Link to="/rules/new">
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
      {!error && rows !== null && rows.length === 0 && <EmptyPanel message="No rules yet." />}
      {!error && rows !== null && rows.length > 0 && <RuleListTable rows={rows} />}
    </div>
  )
}
