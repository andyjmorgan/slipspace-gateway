import { useCallback, useEffect, useState } from "react"
import { Link, useNavigate } from "react-router"
import { Boxes, Pencil, Plus, Trash2, UploadCloud } from "lucide-react"
import { Button } from "@/components/ui/button"
import { ErrorPanel, LoadingPanel } from "@/components/atoms/page-states"
import { fmt } from "@/lib/fmt"
import { apiErrorText, apiFetch, UnauthorizedError } from "../lib/api"
import { deleteEntity, publish } from "../lib/config-api"
import { ENTITY_KINDS, type ConfigEntity, type EntityKind } from "../lib/types"

const KIND_LABEL: Record<EntityKind, string> = {
  backend: "Backends",
  group: "Groups",
  configuration: "Configurations",
  api_key: "API keys",
  rule: "Rules",
  connector: "Connectors",
}

type Banner = { kind: "ok" | "err"; text: string } | null

export function ConfigPage() {
  const nav = useNavigate()
  const [entities, setEntities] = useState<ConfigEntity[] | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [banner, setBanner] = useState<Banner>(null)
  const [publishing, setPublishing] = useState(false)

  const load = useCallback(() => {
    apiFetch<ConfigEntity[]>("/api/v1/config/entities")
      .then(setEntities)
      .catch((e) => {
        if (e instanceof UnauthorizedError) return nav("/login", { replace: true })
        setError(e instanceof Error ? e.message : "Failed to load entities")
      })
  }, [nav])

  useEffect(load, [load])

  const onPublish = async () => {
    setPublishing(true)
    setBanner(null)
    try {
      const res = await publish()
      setBanner({ kind: "ok", text: `Published version ${res.version.slice(0, 8)} (hash ${res.hash.slice(0, 12)}…)` })
    } catch (e) {
      setBanner({ kind: "err", text: apiErrorText(e) })
    } finally {
      setPublishing(false)
    }
  }

  const onDelete = async (e: ConfigEntity) => {
    if (!confirm(`Delete ${e.kind}/${e.name}? This edits the working set; publish to apply.`)) return
    try {
      await deleteEntity(e.kind, e.name)
      load()
    } catch (err) {
      setBanner({ kind: "err", text: apiErrorText(err) })
    }
  }

  const byKind = (k: EntityKind) => (entities ?? []).filter((e) => e.kind === k)

  return (
    <div>
      <div className="mb-4 flex items-start gap-3">
        <Boxes size={22} className="mt-0.5 shrink-0 text-[color:var(--accent)]" />
        <div className="flex-1 min-w-0">
          <h1 className="text-[22px] font-semibold tracking-[-0.02em]">Config</h1>
          <div className="text-[13px] text-[color:var(--text-3)] mt-1.5">
            The working set of config entities. Edits are staged; <strong>publish</strong> composes + validates
            the whole config into a new immutable version the fleet converges on.
          </div>
        </div>
        <div className="flex items-center gap-2 shrink-0">
          <Link to="/config/new">
            <Button variant="ghost" size="sm">
              <Plus />
              <span className="hidden sm:inline">New</span>
            </Button>
          </Link>
          <Button size="sm" onClick={onPublish} disabled={publishing}>
            <UploadCloud />
            {publishing ? "Publishing…" : "Publish"}
          </Button>
        </div>
      </div>

      {banner && (
        <div
          className="mb-4 rounded-[var(--radius)] px-3 py-2 text-[13px] border"
          style={
            banner.kind === "ok"
              ? { color: "var(--ok)", background: "var(--ok-bg)", borderColor: "color-mix(in oklab, var(--ok) 30%, var(--border))" }
              : { color: "var(--err)", background: "var(--err-bg)", borderColor: "color-mix(in oklab, var(--err) 30%, var(--border))" }
          }
        >
          {banner.text}
        </div>
      )}

      {error && <ErrorPanel message={error} />}
      {!error && entities === null && <LoadingPanel />}

      {!error &&
        entities !== null &&
        ENTITY_KINDS.map((kind) => {
          const rows = byKind(kind)
          return (
            <section key={kind} className="mb-5">
              <div className="flex items-center gap-2 mb-2">
                <h2 className="text-[13px] font-semibold uppercase tracking-[0.06em] text-[color:var(--text-3)]">
                  {KIND_LABEL[kind]}
                </h2>
                <span className="mono text-[11px] text-[color:var(--text-4)]">{rows.length}</span>
                <Link
                  to={newPath(kind)}
                  className="ml-auto text-[12px] text-[color:var(--text-3)] hover:text-[color:var(--text)]"
                >
                  + New
                </Link>
              </div>
              {rows.length === 0 ? (
                <div className="text-[12.5px] text-[color:var(--text-4)] px-1">None.</div>
              ) : (
                <div className="rounded-[var(--radius-lg)] border border-[color:var(--border)] bg-[color:var(--bg-1)] overflow-hidden">
                  {rows.map((e) => (
                    <div
                      key={e.name}
                      className="flex items-center gap-3 px-4 py-2.5 border-b border-[color:var(--border)] last:border-0"
                    >
                      <div className="flex-1 min-w-0">
                        <div className="font-medium text-[13px] truncate">{e.name}</div>
                        <div className="mono text-[11px] text-[color:var(--text-4)]">
                          {e.updated_by} · {fmt.ago(e.updated_at)}
                        </div>
                      </div>
                      <Link to={`/config/${encodeURIComponent(e.kind)}/${encodeURIComponent(e.name)}`}>
                        <Button variant="ghost" size="icon" aria-label="Edit">
                          <Pencil />
                        </Button>
                      </Link>
                      <Button variant="ghost" size="icon" aria-label="Delete" onClick={() => onDelete(e)}>
                        <Trash2 />
                      </Button>
                    </div>
                  ))}
                </div>
              )}
            </section>
          )
        })}
    </div>
  )
}

// newPath routes "+ New" to the structured editor for kinds that have one, and
// the generic JSON editor (preselected to the kind) for the rest.
function newPath(kind: EntityKind): string {
  if (kind === "backend") return "/config/backend/new"
  return `/config/new?kind=${kind}`
}
