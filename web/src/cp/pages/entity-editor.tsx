import { useEffect, useState } from "react"
import { useNavigate, useParams, useSearchParams } from "react-router"
import { ArrowLeft, Save } from "lucide-react"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { apiErrorText, UnauthorizedError } from "../lib/api"
import { getEntity, putEntity } from "../lib/config-api"
import { ENTITY_KINDS, type EntityKind } from "../lib/types"

// Minimal valid skeletons so a freshly-created entity starts from a shape
// the contract validator accepts on publish (not on save — save is permissive).
const SKELETON: Record<EntityKind, string> = {
  backend: `{
  "base_url": "https://api.example.com",
  "protocols": { "chat": { "path": "/v1/chat/completions" } }
}`,
  group: `{
  "mode": "failover",
  "targets": [{ "backend": "" }]
}`,
  configuration: `{
  "credentials": {},
  "bindings": []
}`,
  api_key: `{
  "secret": "",
  "name": "",
  "configuration": "",
  "enabled": true
}`,
  rule: `{
  "name": "",
  "conditions": [],
  "actions": []
}`,
  connector: `{
  "name": "",
  "type": "controlplane",
  "url": "",
  "secret_ref": "env:SLUICE_CP_TOKEN",
  "timeout_ms": 5000
}`,
}

export function EntityEditorPage({ mode }: { mode: "create" | "edit" }) {
  const nav = useNavigate()
  const params = useParams()
  const [searchParams] = useSearchParams()
  const editing = mode === "edit"

  const initialKind = ((params.kind as EntityKind) ??
    (searchParams.get("kind") as EntityKind) ??
    "backend") as EntityKind
  const [kind, setKind] = useState<EntityKind>(initialKind)
  const [name, setName] = useState(params.name ?? "")
  const [body, setBody] = useState<string>(editing ? "" : SKELETON[initialKind])
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const [loaded, setLoaded] = useState(!editing)

  useEffect(() => {
    if (!editing) return
    getEntity(params.kind!, params.name!)
      .then((e) => {
        setBody(JSON.stringify(e.body, null, 2))
        setLoaded(true)
      })
      .catch((e) => {
        if (e instanceof UnauthorizedError) return nav("/login", { replace: true })
        setError(e instanceof Error ? e.message : "Failed to load entity")
      })
  }, [editing, params.kind, params.name, nav])

  const save = async () => {
    setError(null)
    if (!name.trim()) {
      setError("Name is required.")
      return
    }
    let parsed: unknown
    try {
      parsed = JSON.parse(body)
    } catch (e) {
      setError(`Invalid JSON: ${e instanceof Error ? e.message : "parse error"}`)
      return
    }
    setBusy(true)
    try {
      await putEntity(kind, name.trim(), parsed)
      nav("/config")
    } catch (e) {
      if (e instanceof UnauthorizedError) return nav("/login", { replace: true })
      setError(apiErrorText(e))
      setBusy(false)
    }
  }

  if (!loaded) {
    return <div className="text-[13px] text-[color:var(--text-3)]">Loading…</div>
  }

  return (
    <div className="max-w-[820px]">
      <button
        onClick={() => nav("/config")}
        className="flex items-center gap-1.5 text-[13px] text-[color:var(--text-3)] hover:text-[color:var(--text)] mb-3"
      >
        <ArrowLeft size={14} /> Config
      </button>
      <h1 className="text-[22px] font-semibold tracking-[-0.02em] mb-4">
        {editing ? `Edit ${kind}/${name}` : "New entity"}
      </h1>

      <div className="flex flex-col gap-4">
        <div className="flex gap-3">
          <div className="flex flex-col gap-1.5 w-[200px]">
            <Label className="text-[11px] font-medium uppercase tracking-[0.07em] text-[color:var(--text-3)]">Kind</Label>
            <select
              value={kind}
              disabled={editing}
              onChange={(e) => {
                const k = e.target.value as EntityKind
                setKind(k)
                if (!editing) setBody(SKELETON[k])
              }}
              className="h-9 rounded-[var(--radius)] border border-[color:var(--border)] bg-[color:var(--bg-1)] px-2.5 text-[13px] disabled:opacity-60"
            >
              {ENTITY_KINDS.map((k) => (
                <option key={k} value={k}>
                  {k}
                </option>
              ))}
            </select>
          </div>
          <div className="flex flex-col gap-1.5 flex-1">
            <Label className="text-[11px] font-medium uppercase tracking-[0.07em] text-[color:var(--text-3)]">Name</Label>
            <Input value={name} disabled={editing} onChange={(e) => setName(e.target.value)} placeholder="entity-name" />
          </div>
        </div>

        <div className="flex flex-col gap-1.5">
          <Label className="text-[11px] font-medium uppercase tracking-[0.07em] text-[color:var(--text-3)]">Body (JSON)</Label>
          <textarea
            value={body}
            onChange={(e) => setBody(e.target.value)}
            spellCheck={false}
            className="mono text-[12.5px] leading-relaxed min-h-[360px] rounded-[var(--radius)] border border-[color:var(--border)] bg-[color:var(--bg-1)] p-3 resize-y"
          />
          <div className="text-[11.5px] text-[color:var(--text-4)]">
            Saved into the working set as-is. The full config is validated on <strong>Publish</strong>.
          </div>
        </div>

        {error && (
          <div
            className="rounded-[var(--radius)] px-3 py-2 text-[13px] border whitespace-pre-wrap"
            style={{ color: "var(--err)", background: "var(--err-bg)", borderColor: "color-mix(in oklab, var(--err) 30%, var(--border))" }}
          >
            {error}
          </div>
        )}

        <div className="flex items-center gap-2">
          <Button onClick={save} disabled={busy}>
            <Save />
            {busy ? "Saving…" : "Save"}
          </Button>
          <Button variant="ghost" onClick={() => nav("/config")}>
            Cancel
          </Button>
        </div>
      </div>
    </div>
  )
}
