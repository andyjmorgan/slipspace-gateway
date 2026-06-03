import { useEffect, useState } from "react"
import { Link, useNavigate, useParams } from "react-router"
import { Tag } from "@/components/atoms/tag"
import { ProviderChip } from "@/components/atoms/provider-chip"
import { Button } from "@/components/ui/button"
import { DeleteDialog } from "@/components/forms/write-atoms"
import { BackendDetailView } from "@/components/config-views/backend-views"
import { backendDetailFromContract, type BackendDetailData } from "@/components/config-views/backend-views-model"
import { PageHeader, LoadingPanel, ErrorPanel, NotFoundPanel } from "@/components/atoms/page-states"
import { APIError, UnauthorizedError } from "../lib/api"
import { getEntity, deleteEntity } from "../lib/config-api"

// CpBackendDetailPage is the control-plane backend detail — the shared
// BackendDetailView fed from a staged entity body. Mirrors the gateway detail
// page (edit / delete / back), but delete edits the working set rather than
// rewriting live config, so the confirm copy says so.
export function CpBackendDetailPage() {
  const { name } = useParams<{ name: string }>()
  const nav = useNavigate()
  const [backend, setBackend] = useState<BackendDetailData | null>(null)
  const [status, setStatus] = useState<"loading" | "ok" | "not_found" | "error">("loading")
  const [message, setMessage] = useState("")
  const [confirmDelete, setConfirmDelete] = useState(false)

  useEffect(() => {
    if (!name) return
    getEntity("backend", name)
      .then((e) => {
        setBackend(backendDetailFromContract(e.name, e.body as Record<string, unknown>))
        setStatus("ok")
      })
      .catch((e) => {
        if (e instanceof UnauthorizedError) return nav("/login", { replace: true })
        if (e instanceof APIError && e.status === 404) {
          setStatus("not_found")
          return
        }
        setMessage(e instanceof Error ? e.message : String(e))
        setStatus("error")
      })
  }, [name, nav])

  if (status === "loading") return <Wrap name={name}><LoadingPanel /></Wrap>
  if (status === "error") return <Wrap name={name}><ErrorPanel message={message} /></Wrap>
  if (status === "not_found" || backend === null) return <Wrap name={name}><NotFoundPanel kind="backend" name={name} /></Wrap>

  const b = backend
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
        description={
          <>
            This removes <span className="mono text-[color:var(--text)]">{b.name}</span> from the working set. The change
            is staged — <span className="mono">publish</span> to roll it out to the fleet.
          </>
        }
        onConfirm={async () => {
          await deleteEntity("backend", b.name)
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
