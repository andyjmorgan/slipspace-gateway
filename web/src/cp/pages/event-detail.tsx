import { useEffect, useState } from "react"
import { useNavigate, useParams } from "react-router"
import { ArrowLeft, ShieldCheck, ShieldAlert } from "lucide-react"
import { PanelCard, PanelHead } from "@/components/atoms/card"
import { StatusPill } from "@/components/atoms/status-pill"
import { LoadingPanel, ErrorPanel, NotFoundPanel } from "@/components/atoms/page-states"
import { fmt } from "@/lib/fmt"
import { apiFetch, apiErrorText, APIError, UnauthorizedError } from "../lib/api"

interface EventDetail {
  correlation_id: string
  gateway_id: string
  configuration: string
  backend: string
  model: string
  protocol: string
  status_code: number
  latency_ms: number
  tokens_in: number
  tokens_out: number
  gen_ai_content?: unknown
  observed_at: string
}

interface BodyRow {
  correlation_id: string
  instance_id: string
  seq: number
  ts_ns: number
  body: unknown
  captured_at: string
}

interface ChainVerify {
  gateway_id: string
  count: number
  verified: boolean
  error?: string
}

interface DetailState {
  event: EventDetail | null
  bodies: BodyRow[] | null
  verify: ChainVerify | null
  error: string | null
  notFound: boolean
}

const INITIAL: DetailState = { event: null, bodies: null, verify: null, error: null, notFound: false }

// EventDetailPage is the per-request drill-down off the observability list: the
// slim event (model, tokens, latency), the heavy captured request/response
// bodies the spool shipped (joined by correlation_id), and the gateway's
// receipt-chain verification ("verified ✓"). Bodies and receipt verification
// are best-effort — a request with no connector binding has no captured body,
// and that absence is shown, not treated as an error.
export function EventDetailPage() {
  const { correlation_id } = useParams<{ correlation_id: string }>()
  const nav = useNavigate()
  const [s, setS] = useState<DetailState>(INITIAL)

  useEffect(() => {
    if (!correlation_id) return
    let cancelled = false
    const cid = encodeURIComponent(correlation_id)
    // Reset to loading whenever the viewed request changes.
    // eslint-disable-next-line react-hooks/set-state-in-effect
    setS(INITIAL)

    apiFetch<EventDetail>(`/api/v1/observability/events/${cid}`)
      .then((e) => {
        if (cancelled) return
        setS((p) => ({ ...p, event: e }))

        apiFetch<BodyRow[]>(`/api/v1/observability/bodies/${cid}`)
          .then((b) => !cancelled && setS((p) => ({ ...p, bodies: b })))
          .catch((err) => {
            if (cancelled) return
            if (err instanceof UnauthorizedError) return nav("/login", { replace: true })
            // 404 = nothing captured (no binding / sampled out); any other
            // failure shouldn't blank the page — treat both as "no bodies".
            setS((p) => ({ ...p, bodies: [] }))
          })

        apiFetch<ChainVerify>(`/api/v1/observability/receipts/${encodeURIComponent(e.gateway_id)}/verify`)
          .then((v) => !cancelled && setS((p) => ({ ...p, verify: v })))
          .catch(() => {
            // Verification is best-effort; leave the badge absent on failure.
          })
      })
      .catch((err) => {
        if (cancelled) return
        if (err instanceof UnauthorizedError) return nav("/login", { replace: true })
        if (err instanceof APIError && err.status === 404) {
          setS((p) => ({ ...p, notFound: true }))
          return
        }
        setS((p) => ({ ...p, error: apiErrorText(err) }))
      })

    return () => {
      cancelled = true
    }
  }, [correlation_id, nav])

  const { event, bodies, verify, error, notFound } = s

  return (
    <div className="flex flex-col gap-4 max-w-[920px]">
      <button
        onClick={() => nav("/observability")}
        className="flex items-center gap-1.5 text-[13px] text-[color:var(--text-3)] hover:text-[color:var(--text)] w-fit"
      >
        <ArrowLeft size={14} /> Observability
      </button>

      {error && <ErrorPanel message={error} />}
      {notFound && <NotFoundPanel kind="request event" name={correlation_id} />}
      {!error && !notFound && event === null && <LoadingPanel />}

      {event && (
        <>
          <div className="flex items-start gap-3">
            <div className="flex-1 min-w-0">
              <div className="flex items-center gap-2">
                <h1 className="text-[22px] font-semibold tracking-[-0.02em]">Request</h1>
                <StatusPill code={event.status_code} />
              </div>
              <div className="mono text-[12px] text-[color:var(--text-3)] mt-1 break-all">
                {event.correlation_id}
              </div>
            </div>
            <ReceiptBadge verify={verify} />
          </div>

          <PanelCard>
            <PanelHead title="Event" sub={fmt.fullTime(event.observed_at)} />
            <dl className="grid grid-cols-2 sm:grid-cols-3 gap-x-6 gap-y-3 px-4 py-4">
              <Field label="Gateway" value={event.gateway_id} mono />
              <Field label="Configuration" value={event.configuration} mono />
              <Field label="Protocol" value={event.protocol} mono />
              <Field label="Model" value={event.model || "—"} mono />
              <Field label="Backend" value={event.backend || "—"} mono />
              <Field label="Latency" value={fmt.ms(event.latency_ms)} mono />
              <Field label="Tokens in" value={fmt.num(event.tokens_in)} mono />
              <Field label="Tokens out" value={fmt.num(event.tokens_out)} mono />
              <Field label="Observed" value={fmt.ago(event.observed_at)} />
            </dl>
          </PanelCard>

          {event.gen_ai_content != null && (
            <PanelCard>
              <PanelHead title="GenAI content" sub="bounded gen_ai.* capture" />
              <JsonBlock value={event.gen_ai_content} />
            </PanelCard>
          )}

          <BodiesSection bodies={bodies} />
        </>
      )}
    </div>
  )
}

function ReceiptBadge({ verify }: { verify: ChainVerify | null }) {
  if (verify === null) return null
  if (verify.verified) {
    return (
      <span
        className="inline-flex items-center gap-1.5 rounded-[var(--radius)] px-2.5 py-1.5 text-[12px] font-medium border whitespace-nowrap"
        style={{ color: "var(--ok)", background: "var(--ok-bg)", borderColor: "color-mix(in oklab, var(--ok) 30%, var(--border))" }}
        title={`${verify.count} receipts verified under the CP signing key`}
      >
        <ShieldCheck size={14} /> Verified
      </span>
    )
  }
  return (
    <span
      className="inline-flex items-center gap-1.5 rounded-[var(--radius)] px-2.5 py-1.5 text-[12px] font-medium border whitespace-nowrap"
      style={{ color: "var(--err)", background: "var(--err-bg)", borderColor: "color-mix(in oklab, var(--err) 30%, var(--border))" }}
      title={verify.error || "receipt chain verification failed"}
    >
      <ShieldAlert size={14} /> Chain broken
    </span>
  )
}

function BodiesSection({ bodies }: { bodies: BodyRow[] | null }) {
  if (bodies === null) {
    return (
      <PanelCard>
        <PanelHead title="Captured bodies" />
        <div className="px-4 py-3 text-[12.5px] text-[color:var(--text-3)]">Loading…</div>
      </PanelCard>
    )
  }
  if (bodies.length === 0) {
    return (
      <PanelCard>
        <PanelHead title="Captured bodies" />
        <div className="px-4 py-3 text-[12.5px] text-[color:var(--text-3)]">
          No captured bodies for this request — its configuration has no connector binding, or the request was
          sampled out.
        </div>
      </PanelCard>
    )
  }
  return (
    <>
      {bodies.map((b) => (
        <PanelCard key={`${b.instance_id}:${b.seq}`}>
          <PanelHead
            title="Captured body"
            sub={`${b.instance_id} · seq ${b.seq}`}
            action={
              <span className="text-[11px] text-[color:var(--text-3)]" title={fmt.fullTime(b.captured_at)}>
                {fmt.ago(b.captured_at)}
              </span>
            }
          />
          <JsonBlock value={b.body} />
        </PanelCard>
      ))}
    </>
  )
}

function Field({ label, value, mono }: { label: string; value: React.ReactNode; mono?: boolean }) {
  return (
    <div className="min-w-0">
      <dt className="text-[11px] uppercase tracking-[0.07em] text-[color:var(--text-3)]">{label}</dt>
      <dd className={`mt-0.5 text-[13px] text-[color:var(--text-2)] break-all ${mono ? "mono" : ""}`}>{value}</dd>
    </div>
  )
}

function JsonBlock({ value }: { value: unknown }) {
  const text = typeof value === "string" ? value : JSON.stringify(value, null, 2)
  return (
    <pre className="mono text-[11.5px] leading-[1.5] text-[color:var(--text-2)] overflow-auto max-h-[420px] px-4 py-3 whitespace-pre-wrap break-words">
      {text}
    </pre>
  )
}
