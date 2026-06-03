import { useConfigurations } from "@/lib/config-api"
import { ConfigurationListTable } from "@/components/config-views/configuration-views"
import { configurationListItemFromSummary } from "@/components/config-views/configuration-views-model"
import {
  PageHeader,
  NewButton,
  LoadingPanel,
  ErrorPanel,
  EmptyPanel,
  useUnauthorizedRedirect,
} from "@/components/atoms/page-states"

export function ConfigurationsPage() {
  const { state } = useConfigurations()
  useUnauthorizedRedirect(state)

  return (
    <div>
      <PageHeader
        title="Configurations"
        sub="Reusable policy bundles — upstream credentials, attached rules, and the API keys that resolve to each. Open one for the breakdown."
        action={
          <NewButton to="/configurations/new" label="New configuration" />
        }
      />
      {state.status === "loading" && <LoadingPanel />}
      {state.status === "error" && <ErrorPanel message={state.message} />}
      {state.status === "ok" && state.data.length === 0 && (
        <EmptyPanel message="No configurations loaded." />
      )}
      {state.status === "ok" && state.data.length > 0 && (
        <ConfigurationListTable rows={state.data.map(configurationListItemFromSummary)} />
      )}
    </div>
  )
}
