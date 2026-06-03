import { useBackends } from "@/lib/config-api"
import { BackendListTable } from "@/components/config-views/backend-views"
import {
  PageHeader,
  NewButton,
  LoadingPanel,
  ErrorPanel,
  EmptyPanel,
  useUnauthorizedRedirect,
} from "@/components/atoms/page-states"

export function BackendsPage() {
  const { state } = useBackends()
  useUnauthorizedRedirect(state)

  return (
    <div>
      <PageHeader
        title="Backends"
        sub="Upstream services the gateway forwards to, shared across every configuration — base URLs, the protocols each one speaks, and whether it accepts passthrough traffic."
        action={
          <NewButton to="/backends/new" label="New backend" />
        }
      />
      {state.status === "loading" && <LoadingPanel />}
      {state.status === "error" && <ErrorPanel message={state.message} />}
      {state.status === "ok" && state.data.length === 0 && (
        <EmptyPanel message="No backends configured." />
      )}
      {state.status === "ok" && state.data.length > 0 && (
        <BackendListTable rows={state.data} />
      )}
    </div>
  )
}
