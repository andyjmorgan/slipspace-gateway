import { useState } from "react"
import { Link, useNavigate, useParams } from "react-router"
import { useBackend, deleteBackend } from "@/lib/config-api"
import { Tag } from "@/components/atoms/tag"
import { ProviderChip } from "@/components/atoms/provider-chip"
import { Button } from "@/components/ui/button"
import { DeleteDialog } from "@/components/forms/write-atoms"
import { BackendDetailView } from "@/components/config-views/backend-views"
import {
  PageHeader,
  LoadingPanel,
  ErrorPanel,
  NotFoundPanel,
  useUnauthorizedRedirect,
} from "@/components/atoms/page-states"

export function BackendDetailPage() {
  const { name } = useParams<{ name: string }>()
  const nav = useNavigate()
  const { state } = useBackend(name)
  useUnauthorizedRedirect(state)
  const [confirmDelete, setConfirmDelete] = useState(false)

  if (state.status === "loading") return <Wrap name={name}><LoadingPanel /></Wrap>
  if (state.status === "error") return <Wrap name={name}><ErrorPanel message={state.message} /></Wrap>
  if (state.status === "not_found") return <Wrap name={name}><NotFoundPanel kind="backend" name={name} /></Wrap>
  if (state.status !== "ok") return null

  const b = state.data
  return (
    <div className="flex flex-col gap-4">
      <PageHeader
        title={b.name}
        sub={
          <div className="flex items-center gap-2 mt-1.5 flex-wrap">
            <ProviderChip name={b.name} />
            {b.protocols.map((p) => (
              <Tag key={p.name} variant="ghost"><span className="mono">{p.name}</span></Tag>
            ))}
            {b.passthrough && b.passthrough.length > 0 && <Tag variant="violet">passthrough</Tag>}
          </div>
        }
        action={
          <div className="flex items-center gap-2">
            <Link to={`/backends/${encodeURIComponent(b.name)}/edit`}>
              <Button size="sm" variant="outline">Edit</Button>
            </Link>
            <Button size="sm" variant="destructive" onClick={() => setConfirmDelete(true)}>Delete</Button>
            <Link to="/backends" className="text-[12.5px] text-[color:var(--text-3)] hover:underline ml-1">
              ← back
            </Link>
          </div>
        }
      />

      <DeleteDialog
        open={confirmDelete}
        resourceKind="backend"
        resourceName={b.name}
        requireConfirmName
        onConfirm={async () => {
          await deleteBackend(b.name)
          nav("/backends", { replace: true })
        }}
        onClose={() => setConfirmDelete(false)}
      />

      <BackendDetailView backend={b} />
    </div>
  )
}

function Wrap({ name, children }: { name?: string; children: React.ReactNode }) {
  return (
    <div>
      <PageHeader title={name ?? "Backend"} />
      {children}
    </div>
  )
}
