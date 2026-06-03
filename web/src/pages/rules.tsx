import { useRules } from "@/lib/config-api"
import { RuleListTable } from "@/components/config-views/rule-views"
import {
  PageHeader,
  NewButton,
  LoadingPanel,
  ErrorPanel,
  EmptyPanel,
  useUnauthorizedRedirect,
} from "@/components/atoms/page-states"

export function RulesPage() {
  const { state } = useRules()
  useUnauthorizedRedirect(state)

  return (
    <div>
      <PageHeader
        title="Rules"
        sub="Shared library. A rule can attach to many configurations; the 'used by' column shows which ones do."
        action={
          <NewButton to="/rules/new" label="New rule" />
        }
      />
      {state.status === "loading" && <LoadingPanel />}
      {state.status === "error" && <ErrorPanel message={state.message} />}
      {state.status === "ok" && state.data.length === 0 && (
        <EmptyPanel message="No rules loaded." />
      )}
      {state.status === "ok" && state.data.length > 0 && (
        <RuleListTable rows={state.data} />
      )}
    </div>
  )
}

export { BehaviorBadge, UsedBy } from "@/components/config-views/rule-views"
