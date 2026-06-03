import { useState } from "react"
import { Link, useNavigate, useParams } from "react-router"
import { deleteRule, useRule, type RuleConflict } from "@/lib/config-api"
import { APIError } from "@/lib/api"
import { Tag } from "@/components/atoms/tag"
import { Button } from "@/components/ui/button"
import { RuleDetailView, BehaviorBadge } from "@/components/config-views/rule-views"
import {
  PageHeader,
  LoadingPanel,
  ErrorPanel,
  NotFoundPanel,
  useUnauthorizedRedirect,
} from "@/components/atoms/page-states"

export function RuleDetailPage() {
  const { name } = useParams<{ name: string }>()
  const { state } = useRule(name)
  useUnauthorizedRedirect(state)

  if (state.status === "loading") return <Wrap name={name}><LoadingPanel /></Wrap>
  if (state.status === "error") return <Wrap name={name}><ErrorPanel message={state.message} /></Wrap>
  if (state.status === "not_found") return <Wrap name={name}><NotFoundPanel kind="rule" name={name} /></Wrap>
  if (state.status !== "ok") return null

  const r = state.data
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
            <DeleteRuleButton name={r.name} />
            <Link to="/rules" className="text-[12.5px] text-[color:var(--text-3)] hover:underline ml-1">
              ← back
            </Link>
          </div>
        }
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

// DeleteRuleButton owns the per-rule delete flow: a confirmation
// affordance (two-click — first click arms, second click commits)
// plus inline rendering of the 409 conflict envelope when the rule
// is still referenced by a configuration.
function DeleteRuleButton({ name }: { name: string }) {
  const nav = useNavigate()
  const [armed, setArmed] = useState(false)
  const [busy, setBusy] = useState(false)
  const [conflict, setConflict] = useState<RuleConflict | null>(null)
  const [error, setError] = useState<string | null>(null)

  const commit = async () => {
    setBusy(true)
    setError(null)
    setConflict(null)
    try {
      await deleteRule(name)
      nav("/rules", { replace: true })
    } catch (e) {
      if (e instanceof APIError && e.status === 409) {
        setConflict(e.body as RuleConflict)
      } else if (e instanceof Error) {
        setError(e.message)
      } else {
        setError(String(e))
      }
      setArmed(false)
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="flex flex-col items-end gap-1">
      <div className="flex items-center gap-2">
        {armed ? (
          <>
            <Button
              type="button"
              size="sm"
              variant="destructive"
              onClick={commit}
              disabled={busy}
            >
              {busy ? "Deleting…" : "Confirm delete"}
            </Button>
            <Button
              type="button"
              size="sm"
              variant="ghost"
              onClick={() => setArmed(false)}
              disabled={busy}
            >
              Cancel
            </Button>
          </>
        ) : (
          <Button
            type="button"
            size="sm"
            variant="destructive"
            onClick={() => {
              setError(null)
              setConflict(null)
              setArmed(true)
            }}
          >
            Delete
          </Button>
        )}
      </div>
      {(conflict || error) && (
        <div
          className="max-w-sm rounded-[var(--radius)] border p-2 text-[11.5px]"
          style={{
            color: "var(--err)",
            background: "var(--err-bg)",
            borderColor: "color-mix(in oklab, var(--err) 30%, var(--border))",
          }}
        >
          {conflict ? (
            <>
              <div className="font-medium">Cannot delete — still referenced</div>
              {conflict.used_by && conflict.used_by.length > 0 && (
                <div className="mt-1">
                  Unbind from: {conflict.used_by.map((c) => <span key={c} className="mono mr-1">{c}</span>)}
                </div>
              )}
            </>
          ) : (
            <div className="mono">{error}</div>
          )}
        </div>
      )}
    </div>
  )
}
