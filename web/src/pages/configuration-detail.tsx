import { useState } from "react"
import { Link, useNavigate, useParams } from "react-router"
import { useConfiguration, deleteConfiguration } from "@/lib/config-api"
import { Tag } from "@/components/atoms/tag"
import { Button } from "@/components/ui/button"
import { DeleteDialog } from "@/components/forms/write-atoms"
import { ConfigurationDetailView } from "@/components/config-views/configuration-views"
import { configurationDetailFromGateway } from "@/components/config-views/configuration-views-model"
import {
  PageHeader,
  LoadingPanel,
  ErrorPanel,
  NotFoundPanel,
  useUnauthorizedRedirect,
} from "@/components/atoms/page-states"

export function ConfigurationDetailPage() {
  const { name } = useParams<{ name: string }>()
  const nav = useNavigate()
  const { state } = useConfiguration(name)
  useUnauthorizedRedirect(state)
  const [confirmDelete, setConfirmDelete] = useState(false)

  if (state.status === "loading") {
    return (
      <div>
        <PageHeader title={name ?? "Configuration"} />
        <LoadingPanel />
      </div>
    )
  }
  if (state.status === "error") {
    return (
      <div>
        <PageHeader title={name ?? "Configuration"} />
        <ErrorPanel message={state.message} />
      </div>
    )
  }
  if (state.status === "not_found") {
    return (
      <div>
        <PageHeader title={name ?? "Configuration"} />
        <NotFoundPanel kind="configuration" name={name} />
      </div>
    )
  }
  if (state.status !== "ok") return null

  const c = state.data
  const view = configurationDetailFromGateway(c)
  return (
    <div className="flex flex-col gap-4">
      <PageHeader
        title={c.name}
        sub={
          <div className="flex items-center gap-2 mt-1.5 flex-wrap">
            <Tag variant="default"><span className="mono">configuration</span></Tag>
            {Object.entries(c.tags ?? {}).map(([k, v]) => (
              <Tag key={k} variant="ghost"><span className="mono">{k}={v}</span></Tag>
            ))}
          </div>
        }
        action={
          <div className="flex items-center gap-2">
            <Link to={`/configurations/${encodeURIComponent(c.name)}/edit`}>
              <Button size="sm" variant="outline">Edit</Button>
            </Link>
            <Button size="sm" variant="destructive" onClick={() => setConfirmDelete(true)}>Delete</Button>
            <Link to="/configurations" className="text-[12.5px] text-[color:var(--text-3)] hover:underline ml-1">
              ← back
            </Link>
          </div>
        }
      />

      <DeleteDialog
        open={confirmDelete}
        resourceKind="configuration"
        resourceName={c.name}
        requireConfirmName
        onConfirm={async () => {
          await deleteConfiguration(c.name)
          nav("/configurations", { replace: true })
        }}
        onClose={() => setConfirmDelete(false)}
      />

      <ConfigurationDetailView config={view} />
    </div>
  )
}
