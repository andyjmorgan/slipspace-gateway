import { useState } from "react"
import { Link, useParams } from "react-router"
import { Eye, EyeOff, Copy, Check } from "lucide-react"
import { useConfiguration, revealAPIKey, type RedactedSecret, type RuleAttachment, type APIKeySummary } from "@/lib/config-api"
import { PanelCard, PanelHead } from "@/components/atoms/card"
import { Tag } from "@/components/atoms/tag"
import { ProviderChip } from "@/components/atoms/provider-chip"
import { cn } from "@/lib/utils"
import {
  PageHeader,
  LoadingPanel,
  ErrorPanel,
  NotFoundPanel,
  EmptyPanel,
  useUnauthorizedRedirect,
} from "@/components/atoms/page-states"

export function ConfigurationDetailPage() {
  const { name } = useParams<{ name: string }>()
  const { state } = useConfiguration(name)
  useUnauthorizedRedirect(state)

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
          <Link to="/configurations" className="text-[12.5px] text-[color:var(--text-3)] hover:underline">
            ← back to all configurations
          </Link>
        }
      />

      <UpstreamCredentialsCard creds={c.upstream_credentials} />
      <AttachedRulesCard rules={c.rules} />
      <APIKeysCard configuration={c.name} keys={c.api_keys} />
    </div>
  )
}

function UpstreamCredentialsCard({ creds }: { creds: Record<string, RedactedSecret> }) {
  const entries = Object.entries(creds)
  return (
    <PanelCard>
      <PanelHead title="Upstream credentials" sub="redacted · managed mode swaps these onto the upstream request" />
      {entries.length === 0 && (
        <div className="px-4 py-6 text-[12.5px] text-[color:var(--text-4)]">
          No managed-mode credentials. Passthrough-only configuration.
        </div>
      )}
      {entries.length > 0 && (
        <table className="w-full text-[12.5px]">
          <thead>
            <tr className="text-[11px] uppercase tracking-[0.07em] text-[color:var(--text-3)]">
              <th className="text-left font-medium px-4 py-2">Provider</th>
              <th className="text-left font-medium px-4 py-2">Redacted</th>
              <th className="text-right font-medium px-4 py-2">Length</th>
            </tr>
          </thead>
          <tbody>
            {entries.map(([provider, redacted]) => (
              <tr key={provider} className="border-t border-[color:var(--border)]">
                <td className="px-4 py-2.5"><ProviderChip name={provider} /></td>
                <td className="px-4 py-2.5"><Redacted r={redacted} /></td>
                <td className="mono tnum text-right px-4 py-2.5 text-[color:var(--text-3)]">{redacted.length}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </PanelCard>
  )
}

function AttachedRulesCard({ rules }: { rules: RuleAttachment[] }) {
  return (
    <PanelCard>
      <PanelHead title="Attached rules" sub={`evaluated in order, top to bottom · ${rules.length}`} />
      {rules.length === 0 && (
        <div className="px-4 py-6 text-[12.5px] text-[color:var(--text-4)]">No rules attached.</div>
      )}
      {rules.length > 0 && (
        <table className="w-full text-[12.5px]">
          <thead>
            <tr className="text-[11px] uppercase tracking-[0.07em] text-[color:var(--text-3)]">
              <th className="text-left font-medium px-4 py-2">Name</th>
              <th className="text-left font-medium px-4 py-2">Condition</th>
              <th className="text-left font-medium px-4 py-2">Actions</th>
              <th className="text-left font-medium px-4 py-2">Behavior</th>
            </tr>
          </thead>
          <tbody>
            {rules.map((r) => (
              <tr key={r.name} className="border-t border-[color:var(--border)] hover:bg-[color:var(--hover)]">
                <td className="px-4 py-2.5">
                  <Link to={`/rules/${encodeURIComponent(r.name)}`} className="mono hover:underline">{r.name}</Link>
                </td>
                <td className="mono text-[12px] px-4 py-2.5 text-[color:var(--text-2)]">{r.condition_summary}</td>
                <td className="px-4 py-2.5">
                  <div className="flex gap-1.5 flex-wrap">
                    {r.action_types.map((t, i) => (
                      <Tag key={i} variant={t.startsWith("change") ? "violet" : "default"}>
                        <span className="mono">{t}</span>
                      </Tag>
                    ))}
                  </div>
                </td>
                <td className="px-4 py-2.5">
                  <BehaviorBadge behavior={r.behavior} />
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </PanelCard>
  )
}


function APIKeysCard({ configuration, keys }: { configuration: string; keys: APIKeySummary[] }) {
  return (
    <PanelCard>
      <PanelHead
        title="API keys"
        sub={`${keys.length} key${keys.length === 1 ? "" : "s"} · reveal shows the plaintext`}
      />
      {keys.length === 0 && (
        <EmptyPanel message="No API keys attached to this configuration." />
      )}
      {keys.length > 0 && (
        <table className="w-full text-[12.5px]">
          <thead>
            <tr className="text-[11px] uppercase tracking-[0.07em] text-[color:var(--text-3)]">
              <th className="text-left font-medium px-4 py-2">Name</th>
              <th className="text-left font-medium px-4 py-2">Secret</th>
              <th className="text-right font-medium px-4 py-2">Length</th>
              <th className="text-left font-medium px-4 py-2">Enabled</th>
              <th className="text-right font-medium px-4 py-2 w-32">Actions</th>
            </tr>
          </thead>
          <tbody>
            {keys.map((k) => (
              <APIKeyRow key={k.name} configuration={configuration} k={k} />
            ))}
          </tbody>
        </table>
      )}
    </PanelCard>
  )
}

function APIKeyRow({ configuration, k }: { configuration: string; k: APIKeySummary }) {
  const [revealed, setRevealed] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState<string | null>(null)
  const [copied, setCopied] = useState(false)

  async function toggleReveal() {
    if (revealed != null) {
      setRevealed(null)
      setErr(null)
      return
    }
    setBusy(true)
    setErr(null)
    try {
      const r = await revealAPIKey(configuration, k.name)
      setRevealed(r.secret)
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }

  async function copyPlaintext() {
    if (revealed == null) return
    try {
      await navigator.clipboard.writeText(revealed)
      setCopied(true)
      setTimeout(() => setCopied(false), 1500)
    } catch {
      // clipboard permission denied or unsupported context — silently ignore
    }
  }

  return (
    <tr className="border-t border-[color:var(--border)]">
      <td className="px-4 py-2.5 mono">{k.name}</td>
      <td className="px-4 py-2.5">
        {revealed != null ? (
          <span className="mono text-[12px] break-all">{revealed}</span>
        ) : (
          <Redacted r={k.secret} />
        )}
        {err && (
          <div className="text-[11px] mt-1" style={{ color: "var(--err)" }}>
            {err}
          </div>
        )}
      </td>
      <td className="mono tnum text-right px-4 py-2.5 text-[color:var(--text-3)]">{k.secret.length}</td>
      <td className="px-4 py-2.5">
        {k.enabled ? <Tag variant="success">enabled</Tag> : <Tag variant="danger">disabled</Tag>}
      </td>
      <td className="px-4 py-2.5">
        <div className="flex items-center gap-1.5 justify-end">
          <ChipButton
            onClick={toggleReveal}
            disabled={busy}
            ariaLabel={revealed ? "Hide secret" : "Reveal secret"}
          >
            {revealed ? <EyeOff size={11} /> : <Eye size={11} />}
            <span>{revealed ? "hide" : "reveal"}</span>
          </ChipButton>
          {revealed != null && (
            <ChipButton
              onClick={copyPlaintext}
              ariaLabel="Copy secret to clipboard"
              variant={copied ? "ok" : "default"}
            >
              {copied ? <Check size={11} /> : <Copy size={11} />}
              <span>{copied ? "copied" : "copy"}</span>
            </ChipButton>
          )}
        </div>
      </td>
    </tr>
  )
}

function Redacted({ r }: { r: RedactedSecret }) {
  if (r.length === 0) return <span className="text-[color:var(--text-4)]">—</span>
  return (
    <span className="mono text-[12px]">
      <span aria-hidden="true">••••</span>
      <span>{r.last4}</span>
    </span>
  )
}

// ChipButton is a small, low-contrast button matching the page's Tag
// aesthetic — reveal/hide/copy live next to redacted secrets so a high-
// contrast outline button (the shadcn default) read too loud against the
// dark theme. Same hover affordance as the navbar links.
function ChipButton({
  children,
  onClick,
  disabled,
  ariaLabel,
  variant = "default",
}: {
  children: React.ReactNode
  onClick: () => void
  disabled?: boolean
  ariaLabel: string
  variant?: "default" | "ok"
}) {
  const color =
    variant === "ok"
      ? "bg-[color:var(--ok-bg)] text-[color:var(--ok)]"
      : "bg-[color:var(--bg-2)] text-[color:var(--text-2)] hover:bg-[color:var(--hover)] hover:text-[color:var(--text)]"
  return (
    <button
      type="button"
      onClick={onClick}
      disabled={disabled}
      aria-label={ariaLabel}
      className={cn(
        "mono inline-flex items-center gap-1 rounded-[5px] px-1.5 py-0.5 text-[11px] font-medium transition-colors disabled:opacity-50 disabled:pointer-events-none",
        color,
      )}
    >
      {children}
    </button>
  )
}

function BehaviorBadge({ behavior }: { behavior?: string }) {
  if (!behavior || behavior === "continue") return <Tag variant="default">continue</Tag>
  if (behavior === "exit") return <Tag variant="warn">exit</Tag>
  return <Tag variant="ghost">{behavior}</Tag>
}
