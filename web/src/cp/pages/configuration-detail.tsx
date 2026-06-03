import { useEffect, useState } from "react"
import { Link, useNavigate, useParams } from "react-router"
import { Tag } from "@/components/atoms/tag"
import { Button } from "@/components/ui/button"
import { DeleteDialog } from "@/components/forms/write-atoms"
import { ConfigurationDetailView } from "@/components/config-views/configuration-views"
import {
  configurationDetailFromContract,
  type ConfigurationDetailData,
} from "@/components/config-views/configuration-views-model"
import { PageHeader, LoadingPanel, ErrorPanel, NotFoundPanel } from "@/components/atoms/page-states"
import { APIError, UnauthorizedError } from "../lib/api"
import { getEntity, deleteEntity } from "../lib/config-api"

// CpConfigurationDetailPage is the control-plane configuration detail — the
// shared ConfigurationDetailView fed from a staged entity body. The CP body
// carries credentials inline, but configurationDetailFromContract discards the
// value and projects only a masked presence marker, so no raw secret renders.
// Mirrors the gateway detail page (edit / delete / back), but delete edits the
// working set rather than rewriting live config, so the confirm copy says so.
export function CpConfigurationDetailPage() {
  const { name } = useParams<{ name: string }>()
  const nav = useNavigate()
  const [config, setConfig] = useState<ConfigurationDetailData | null>(null)
  const [status, setStatus] = useState<"loading" | "ok" | "not_found" | "error">("loading")
  const [message, setMessage] = useState("")
  const [confirmDelete, setConfirmDelete] = useState(false)

  useEffect(() => {
    if (!name) return
    getEntity("configuration", name)
      .then((e) => {
        setConfig(configurationDetailFromContract(e.name, e.body as Record<string, unknown>))
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
  if (status === "not_found" || config === null) return <Wrap name={name}><NotFoundPanel kind="configuration" name={name} /></Wrap>

  const c = config
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
        description={
          <>
            This removes <span className="mono text-[color:var(--text)]">{c.name}</span> from the working set. The change
            is staged — <span className="mono">publish</span> to roll it out to the fleet.
          </>
        }
        onConfirm={async () => {
          await deleteEntity("configuration", c.name)
          nav("/configurations", { replace: true })
        }}
        onClose={() => setConfirmDelete(false)}
      />

      <ConfigurationDetailView config={c} />
    </div>
  )
}

function Wrap({ name, children }: { name?: string; children: React.ReactNode }) {
  return (
    <div>
      <PageHeader title={name ?? "Configuration"} />
      {children}
    </div>
  )
}
