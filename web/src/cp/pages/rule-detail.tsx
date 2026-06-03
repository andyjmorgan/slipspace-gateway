import { useEffect, useState } from "react"
import { Link, useNavigate, useParams } from "react-router"
import { Tag } from "@/components/atoms/tag"
import { Button } from "@/components/ui/button"
import { DeleteDialog } from "@/components/forms/write-atoms"
import { RuleDetailView, BehaviorBadge } from "@/components/config-views/rule-views"
import { ruleDetailFromContract, type RuleDetailData } from "@/components/config-views/rule-views-model"
import { PageHeader, LoadingPanel, ErrorPanel, NotFoundPanel } from "@/components/atoms/page-states"
import { APIError, UnauthorizedError } from "../lib/api"
import { getEntity, deleteEntity } from "../lib/config-api"

// CpRuleDetailPage is the control-plane rule detail — the shared RuleDetailView
// fed from a staged entity body. Mirrors the gateway detail page (edit / delete
// / back), but delete edits the working set rather than rewriting live config,
// so the confirm copy says so.
export function CpRuleDetailPage() {
  const { name } = useParams<{ name: string }>()
  const nav = useNavigate()
  const [rule, setRule] = useState<RuleDetailData | null>(null)
  const [status, setStatus] = useState<"loading" | "ok" | "not_found" | "error">("loading")
  const [message, setMessage] = useState("")
  const [confirmDelete, setConfirmDelete] = useState(false)

  useEffect(() => {
    if (!name) return
    getEntity("rule", name)
      .then((e) => {
        setRule(ruleDetailFromContract(e.name, e.body as Record<string, unknown>))
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
  if (status === "not_found" || rule === null) return <Wrap name={name}><NotFoundPanel kind="rule" name={name} /></Wrap>

  const r = rule
  return (
    <div className="flex flex-col gap-4">
      <PageHeader
        title={r.name}
        sub={
          <div className="flex items-center gap-2 mt-1.5 flex-wrap">
            <Tag variant="default"><span className="mono">rule</span></Tag>
            <BehaviorBadge behavior={r.behavior} />
          </div>
        }
        action={
          <div className="flex items-center gap-2">
            <Link to={`/rules/${encodeURIComponent(r.name)}/edit`}>
              <Button size="sm" variant="outline">Edit</Button>
            </Link>
            <Button size="sm" variant="destructive" onClick={() => setConfirmDelete(true)}>Delete</Button>
            <Link to="/rules" className="text-[12.5px] text-[color:var(--text-3)] hover:underline ml-1">
              ← back
            </Link>
          </div>
        }
      />

      <DeleteDialog
        open={confirmDelete}
        resourceKind="rule"
        resourceName={r.name}
        requireConfirmName
        description={
          <>
            This removes <span className="mono text-[color:var(--text)]">{r.name}</span> from the working set. The change
            is staged — <span className="mono">publish</span> to roll it out to the fleet.
          </>
        }
        onConfirm={async () => {
          await deleteEntity("rule", r.name)
          nav("/rules", { replace: true })
        }}
        onClose={() => setConfirmDelete(false)}
      />

      <RuleDetailView rule={r} />
    </div>
  )
}

function Wrap({ name, children }: { name?: string; children: React.ReactNode }) {
  return (
    <div>
      <PageHeader title={name ?? "Rule"} />
      {children}
    </div>
  )
}
