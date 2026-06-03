import { useConnectors } from "@/lib/config-api"
import { ConnectorListTable } from "@/components/config-views/connector-views"
import { connectorListItemFromContract } from "@/components/config-views/connector-views-model"
import {
  PageHeader,
  NewButton,
  LoadingPanel,
  ErrorPanel,
  EmptyPanel,
  useUnauthorizedRedirect,
} from "@/components/atoms/page-states"

export function ConnectorsPage() {
  const { state } = useConnectors()
  useUnauthorizedRedirect(state)

  return (
    <div>
      <PageHeader
        title="Connectors"
        sub="Spool destinations for end-of-pipeline records — S3, Azure Blob, or webhook. A configuration's connector bindings reference these by name."
        action={
          <NewButton to="/connectors/new" label="New connector" />
        }
      />
      {state.status === "loading" && <LoadingPanel />}
      {state.status === "error" && <ErrorPanel message={state.message} />}
      {state.status === "ok" && state.data.length === 0 && (
        <EmptyPanel message="No connectors configured." />
      )}
      {state.status === "ok" && state.data.length > 0 && (
        <ConnectorListTable rows={state.data.map((c) => connectorListItemFromContract(c.name, c))} />
      )}
    </div>
  )
}
