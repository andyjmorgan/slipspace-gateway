import { useEffect, useState } from "react"
import { useNavigate } from "react-router"
import { ChevronDown, ChevronRight, RefreshCw } from "lucide-react"
import { Button } from "@/components/ui/button"
import { PanelCard, PanelHead } from "@/components/atoms/card"
import { KPI } from "@/components/atoms/kpi"
import { PageHeader } from "@/components/atoms/page-header"
import { Segmented } from "@/components/atoms/segmented"
import { SkeletonBlock } from "@/components/atoms/skeleton"
import { fmt } from "@/lib/fmt"
import { type DashboardWindow } from "@/lib/dashboard"
import {
  fetchAdviseAuditBefore,
  useAdviseAudit,
  useAdviseSavings,
  type AdviseAuditItem,
  type AdviseSavingsItem,
} from "@/lib/advise"

// verdictBadge maps a judgement outcome to its label + design-system tone.
// Keyed on verdict_switch / error, never on verdict_model being non-empty —
// a declined verdict can still carry a model (structured outputs cannot
// conditionally force it empty) and consumers must ignore it.
function verdictBadge(e: AdviseAuditItem): { label: string; color: string } {
  if (e.error) return { label: "error", color: "var(--err)" }
  if (!e.verdict_switch) return { label: "no switch", color: "var(--text-4)" }
  return { label: e.verdict_model || "switch", color: "var(--ok)" }
}

// AgentRoutingPage is the operator view of the agent-aware routing advisor:
// the judgement audit log (what the judge saw, what it decided, why) and the
// savings attribution (measured pinned spend vs the rate-card counterfactual,
// net of the judge's own inference cost). Read-only over the arbiter's
// advise_audit + request_events tables.
export function AgentRoutingPage() {
  const nav = useNavigate()
  const [window, setWindow] = useState<DashboardWindow>("24h")
  const audit = useAdviseAudit()
  const savings = useAdviseSavings(window)

  // Older judgement pages, appended below the polled head page.
  const [older, setOlder] = useState<AdviseAuditItem[]>([])
  const [loadingOlder, setLoadingOlder] = useState(false)
  const [exhausted, setExhausted] = useState(false)

  useEffect(() => {
    if (audit.state.status === "unauthorized" || savings.state.status === "unauthorized") {
      nav("/login", { replace: true })
    }
  }, [audit.state, savings.state, nav])

  const head: AdviseAuditItem[] = audit.state.status === "ok" ? (audit.state.data.items ?? []) : []
  // Drop any overlap between the polled head and previously-loaded older
  // pages (the head grows as new judgements land).
  const seen = new Set(head.map((e) => `${e.received_at}:${e.conversation_id}`))
  const items = [...head, ...older.filter((e) => !seen.has(`${e.received_at}:${e.conversation_id}`))]

  const loadOlder = () => {
    const last = items[items.length - 1]
    if (!last || loadingOlder) return
    setLoadingOlder(true)
    fetchAdviseAuditBefore(last.received_at)
      .then((page) => {
        const got = page.items ?? []
        if (got.length === 0) setExhausted(true)
        setOlder((prev) => [...prev, ...got])
      })
      .catch(() => {
        // Transient — the button stays visible for a retry.
      })
      .finally(() => setLoadingOlder(false))
  }

  const refreshing = audit.state.status === "loading" || savings.state.status === "loading"

  return (
    <div className="flex flex-col gap-3.5">
      <PageHeader title="Agent routing" sub="Judge verdicts and the spend the down-ranking saved">
        <Segmented
          value={window}
          onChange={setWindow}
          options={[
            { value: "1h", label: "1h" },
            { value: "24h", label: "24h" },
            { value: "7d", label: "7d" },
            { value: "30d", label: "30d" },
          ]}
        />
        <Button
          variant="ghost"
          size="sm"
          onClick={() => {
            audit.refetch()
            savings.refetch()
          }}
          disabled={refreshing}
          aria-label="Refresh"
        >
          <RefreshCw className={refreshing ? "animate-spin" : undefined} /> <span className="hidden sm:inline">Refresh</span>
        </Button>
      </PageHeader>

      {savings.state.status === "error" && (
        <ErrorNote label="savings" message={savings.state.message} />
      )}
      {audit.state.status === "error" && <ErrorNote label="judgement log" message={audit.state.message} />}

      <SavingsPanel window={window} state={savings.state} />

      <PanelCard>
        <PanelHead
          title="Judgement log"
          sub={audit.state.status === "ok" ? `${items.length} loaded · newest first` : undefined}
        />
        {audit.state.status === "loading" && (
          <div className="px-4 py-3">
            <SkeletonBlock height={180} />
          </div>
        )}
        {audit.state.status === "ok" && items.length === 0 && (
          <div className="px-4 py-10 text-center text-[12.5px] text-[color:var(--text-4)]">
            No judgements recorded yet — the advisor writes one row per classified conversation.
          </div>
        )}
        {audit.state.status === "ok" && items.length > 0 && (
          <div className="flex flex-col">
            {items.map((e, i) => (
              <JudgementRow key={`${e.received_at}:${e.conversation_id}:${i}`} entry={e} />
            ))}
            {!exhausted && (
              <div className="px-4 py-2.5 border-t border-[color:var(--border)]">
                <Button variant="ghost" size="sm" onClick={loadOlder} disabled={loadingOlder}>
                  {loadingOlder ? "Loading…" : "Load older"}
                </Button>
              </div>
            )}
          </div>
        )}
      </PanelCard>
    </div>
  )
}

function ErrorNote({ label, message }: { label: string; message: string }) {
  return (
    <div
      className="rounded-[var(--radius-lg)] border border-[color:var(--border)] p-4 text-[13px]"
      style={{ color: "var(--err)", background: "var(--err-bg)" }}
    >
      Failed to load {label}: <span className="mono">{message}</span>
    </div>
  )
}

// SavingsPanel renders the attribution totals as KPI tiles plus the
// per-conversation breakdown. Null counterfactuals (models without a
// configured rate) render as an em-dash — the API never guesses, neither
// does the UI.
function SavingsPanel({
  window,
  state,
}: {
  window: DashboardWindow
  state: ReturnType<typeof useAdviseSavings>["state"]
}) {
  if (state.status === "loading") {
    return <SkeletonBlock height={92} />
  }
  if (state.status !== "ok") return null
  const { totals, items } = state.data
  const rows: AdviseSavingsItem[] = items ?? []
  return (
    <>
      <div className="grid grid-cols-2 lg:grid-cols-4 gap-3">
        <KPI
          label="Net saved"
          value={fmt.usd(totals.net_saved_usd)}
          sub={`over ${window}`}
          accent={totals.net_saved_usd >= 0 ? "ok" : "warn"}
        />
        <KPI label="Saved" value={fmt.usd(totals.saved_usd)} sub="vs counterfactual" />
        <KPI
          label="Pinned spend"
          value={fmt.usd(totals.actual_usd)}
          sub={`would have been ${fmt.usd(totals.counterfactual_usd)}`}
        />
        <KPI label="Judge cost" value={fmt.usd(totals.judge_cost_usd)} sub="charged against saving" />
      </div>

      {rows.length > 0 && (
        <PanelCard>
          <PanelHead title="Down-ranked conversations" sub={`${rows.length} · ${window}`} />
          <div className="flex flex-col">
            {rows.map((r) => (
              <div
                key={r.conversation_id}
                className="grid grid-cols-[1fr_auto_auto_auto] items-center gap-2.5 px-4 py-2.5 border-t border-[color:var(--border)] first:border-t-0 text-[12px]"
              >
                <span className="min-w-0 truncate">
                  <span className="mono">{r.conversation_id}</span>
                  <span className="text-[color:var(--text-4)]">
                    {" "}
                    · {r.requested_model} → {r.pinned_model} · {r.pinned_requests} req
                  </span>
                </span>
                <span className="mono tnum text-[color:var(--text-3)]">{fmt.usd(r.actual_usd)}</span>
                <span className="mono tnum text-[color:var(--text-4)]">
                  {r.counterfactual_usd == null ? "—" : fmt.usd(r.counterfactual_usd)}
                </span>
                <span className="mono tnum" style={{ color: r.saved_usd == null ? "var(--text-4)" : "var(--ok)" }}>
                  {r.saved_usd == null ? "no rate" : `+${fmt.usd(r.saved_usd)}`}
                </span>
              </div>
            ))}
          </div>
        </PanelCard>
      )}
    </>
  )
}

// JudgementRow is one audit entry: a single summary line, expandable to the
// exact excerpts the judge saw (system prefix, task, tools).
function JudgementRow({ entry }: { entry: AdviseAuditItem }) {
  const [open, setOpen] = useState(false)
  const badge = verdictBadge(entry)
  return (
    <div className="border-t border-[color:var(--border)] first:border-t-0">
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        aria-expanded={open}
        className="w-full grid grid-cols-[auto_auto_auto_1fr_auto] items-center gap-2.5 px-4 py-2.5 text-left hover:bg-[color:var(--hover)] transition-colors"
      >
        {open ? (
          <ChevronDown size={14} className="text-[color:var(--text-4)]" />
        ) : (
          <ChevronRight size={14} className="text-[color:var(--text-4)]" />
        )}
        <span className="mono tnum text-[11.5px] text-[color:var(--text-4)] shrink-0 whitespace-nowrap">
          {new Date(entry.received_at).toLocaleString("en-GB", { hour12: false })}
        </span>
        <span
          className="mono text-[10.5px] tracking-[0.02em] font-medium rounded px-1.5 py-0.5 shrink-0"
          style={{ color: badge.color, background: "var(--bg-2)" }}
        >
          {badge.label}
        </span>
        <span className="text-[12px] truncate min-w-0">
          <span className="mono">{entry.requested_model || "—"}</span>
          <span className="text-[color:var(--text-4)]">
            {" "}
            · {entry.agent_family || "unknown"}
            {entry.cache_hit ? " · cached" : entry.judge_latency_ms ? ` · ${entry.judge_latency_ms}ms` : ""}
            {entry.verdict_reason ? ` · ${entry.verdict_reason}` : entry.error ? ` · ${entry.error}` : ""}
          </span>
        </span>
        <span className="mono text-[11px] text-[color:var(--text-4)] text-right shrink-0" title={entry.conversation_id}>
          {entry.conversation_id.slice(0, 10)}
        </span>
      </button>
      {open && (
        <div className="px-11 pb-3 flex flex-col gap-2 text-[12px]">
          <JudgementDetail label="Verdict">
            {entry.error
              ? `judge error: ${entry.error}`
              : entry.verdict_switch
                ? `switch → ${entry.verdict_model} (confidence ${fmt.pct(entry.verdict_confidence)})`
                : `no switch (confidence ${fmt.pct(entry.verdict_confidence)})`}
            {entry.verdict_reason ? ` — ${entry.verdict_reason}` : ""}
          </JudgementDetail>
          {entry.tool_names && entry.tool_names.length > 0 && (
            <JudgementDetail label={`Tools (${entry.tool_names.length})`}>
              <span className="mono">{entry.tool_names.join(", ")}</span>
            </JudgementDetail>
          )}
          {entry.first_user_message && (
            <JudgementDetail label="Task excerpt">
              <pre className="mono whitespace-pre-wrap break-words text-[11.5px] leading-relaxed m-0">
                {entry.first_user_message}
              </pre>
            </JudgementDetail>
          )}
          {entry.system_prefix && (
            <JudgementDetail label="System prefix">
              <pre className="mono whitespace-pre-wrap break-words text-[11.5px] leading-relaxed m-0">
                {entry.system_prefix}
              </pre>
            </JudgementDetail>
          )}
          <div className="text-[11px] text-[color:var(--text-4)]">
            {entry.gateway_id} · {entry.configuration || "—"} · {entry.protocol || "—"} · {entry.provider || "—"}
            {entry.session_id ? ` · session ${entry.session_id}` : ""}
          </div>
        </div>
      )}
    </div>
  )
}

function JudgementDetail({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="flex flex-col gap-1">
      <div className="text-[10.5px] font-medium uppercase tracking-[0.07em] text-[color:var(--text-4)]">{label}</div>
      <div className="rounded-[var(--radius)] bg-[color:var(--bg-2)] px-2.5 py-2">{children}</div>
    </div>
  )
}
