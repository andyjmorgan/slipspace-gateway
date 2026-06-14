import { useEffect, useMemo, useState } from "react"
import { useNavigate } from "react-router"
import { RefreshCw } from "lucide-react"
import { Button } from "@/components/ui/button"
import { PanelCard } from "@/components/atoms/card"
import { Segmented } from "@/components/atoms/segmented"
import { CardCopy } from "../components/span-inspector-panes"
import { apiFetch, UnauthorizedError } from "@/lib/api"
import type { SettingsResponse } from "@/lib/generated/admin"

type ViewFormat = "json" | "yaml"

// SettingsPage is the read-only view of the telemetry service's applied (loaded)
// config — what's actually in effect in the running container, including the
// scanner block. Secrets are already redacted server-side (the password hash,
// gateway HMAC secrets, evidence key, and DSN password come back as "***"), so
// this surface only renders; it never edits and never sees a credential.
export function SettingsPage() {
  const nav = useNavigate()
  const [format, setFormat] = useState<ViewFormat>("yaml")
  const [reloadNonce, setReloadNonce] = useState(0)

  // The fetch result is keyed by the reload nonce, so status derives during
  // render (loading until the current nonce's result lands) and the effect only
  // setStates inside its async callbacks — never synchronously.
  const [fetched, setFetched] = useState<{ key: number; config: Record<string, unknown> | null; err: string | null } | null>(null)
  const current = fetched && fetched.key === reloadNonce ? fetched : null
  const status: "loading" | "ok" | "error" = !current ? "loading" : current.err ? "error" : "ok"
  const config = current?.config ?? null
  const err = current?.err ?? null

  useEffect(() => {
    let cancelled = false
    apiFetch<SettingsResponse>("/api/v1/settings")
      .then((r) => {
        if (!cancelled) setFetched({ key: reloadNonce, config: r.config ?? {}, err: null })
      })
      .catch((e) => {
        if (cancelled) return
        if (e instanceof UnauthorizedError) {
          nav("/login", { replace: true })
          return
        }
        setFetched({ key: reloadNonce, config: null, err: e instanceof Error ? e.message : String(e) })
      })
    return () => {
      cancelled = true
    }
  }, [reloadNonce, nav])

  // The rendered text derives purely from config + format — no setState in an
  // effect, so the toggle is instant and re-render-safe.
  const text = useMemo(() => {
    if (!config) return ""
    return format === "json" ? JSON.stringify(config, null, 2) : toYAML(config)
  }, [config, format])

  return (
    <div className="flex flex-col gap-3.5">
      <div className="flex flex-wrap items-center gap-2 sm:gap-3">
        <div className="flex-1 min-w-0">
          <h1 className="text-[24px] font-semibold tracking-[-0.02em]">Settings</h1>
          <div className="text-[13px] text-[color:var(--text-3)] mt-1">
            Applied configuration of the running telemetry service (read-only, secrets redacted)
          </div>
        </div>
        <Segmented
          value={format}
          onChange={(v) => setFormat(v as ViewFormat)}
          options={[
            { value: "yaml", label: "YAML" },
            { value: "json", label: "JSON" },
          ]}
        />
        <Button variant="ghost" size="sm" onClick={() => setReloadNonce((n) => n + 1)} aria-label="Refresh">
          <RefreshCw /> <span className="hidden sm:inline">Refresh</span>
        </Button>
      </div>

      <PanelCard>
        {status === "error" ? (
          <div
            className="m-3 rounded-[var(--radius-lg)] border p-4 text-[13px]"
            style={{ color: "var(--err)", background: "var(--err-bg)" }}
          >
            Failed to load settings: <span className="mono">{err}</span>
          </div>
        ) : status === "loading" ? (
          <div className="p-6 text-[13px] text-[color:var(--text-4)]">loading applied config…</div>
        ) : (
          <div className="p-3">
            <div className="flex items-center justify-between px-1 pb-2">
              <span className="text-[11px] uppercase tracking-[0.07em] text-[color:var(--text-4)]">
                {format === "yaml" ? "config.yaml" : "config.json"}
              </span>
              <CardCopy text={text} label="applied config" />
            </div>
            <pre className="whitespace-pre rounded-[var(--radius)] border border-[color:var(--border)] bg-[color:var(--bg)] px-3.5 py-3 mono text-[12px] leading-[1.6] text-[color:var(--text-2)] overflow-auto max-h-[calc(100vh-16rem)] m-0">
              {text}
            </pre>
          </div>
        )}
      </PanelCard>
    </div>
  )
}

// toYAML is a tiny, dependency-free YAML stringifier for the applied-config
// tree. It handles the shapes the telemetry Config serializes to — nested
// objects, arrays of objects/scalars, strings, numbers, booleans, null — which
// is all this read-only view needs. It is deliberately not a general YAML
// emitter (no anchors, no multi-line block scalars); scalars that could be
// ambiguous (contain a colon, hash, quote, or leading/trailing space, or look
// like a bool/number/null) are double-quoted so the output stays valid.
function toYAML(value: unknown, indent = 0): string {
  const pad = "  ".repeat(indent)

  if (Array.isArray(value)) {
    if (value.length === 0) return " []"
    return (
      "\n" +
      value
        .map((item) => {
          if (isContainer(item)) {
            // A container element: emit "- " then its body, re-indented so the
            // first key sits on the dash line.
            const body = toYAML(item, indent + 1)
            const trimmed = body.startsWith("\n") ? body.slice(1) : body
            return `${pad}- ${trimmed.replace(new RegExp(`^${"  ".repeat(indent + 1)}`), "")}`
          }
          return `${pad}- ${scalar(item)}`
        })
        .join("\n")
    )
  }

  if (isPlainObject(value)) {
    const keys = Object.keys(value)
    if (keys.length === 0) return " {}"
    return (
      "\n" +
      keys
        .map((k) => {
          const v = (value as Record<string, unknown>)[k]
          if (isContainer(v)) {
            return `${pad}${k}:${toYAML(v, indent + 1)}`
          }
          return `${pad}${k}: ${scalar(v)}`
        })
        .join("\n")
    )
  }

  return ` ${scalar(value)}`
}

function isPlainObject(v: unknown): v is Record<string, unknown> {
  return typeof v === "object" && v !== null && !Array.isArray(v)
}

function isContainer(v: unknown): boolean {
  return Array.isArray(v) ? v.length > 0 : isPlainObject(v) && Object.keys(v).length > 0
}

// scalar renders a leaf value, quoting strings that would otherwise be
// misparsed as YAML (empty, bool/number/null lookalikes, or carrying special
// characters / surrounding whitespace).
function scalar(v: unknown): string {
  if (v === null || v === undefined) return "null"
  if (typeof v === "boolean" || typeof v === "number") return String(v)
  const s = String(v)
  if (s === "" || needsQuote(s)) return JSON.stringify(s)
  return s
}

function needsQuote(s: string): boolean {
  if (s !== s.trim()) return true
  if (/^(true|false|null|~)$/i.test(s)) return true
  if (/^-?\d+(\.\d+)?$/.test(s)) return true
  return /[:#'"[\]{}&*!|>%@`]/.test(s) || s.startsWith("- ")
}
