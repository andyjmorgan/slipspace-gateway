// Tiny state-render helpers for the read-only config pages. Loading,
// error, not-found, and unauthorized all flow through the same async
// hook envelope (lib/config-api.ts::useConfigFetch) — pages call into
// these helpers so the shapes stay identical across every page.

import { useEffect } from "react"
import { useLocation, useNavigate } from "react-router"
import type { ConfigFetchState } from "@/lib/config-api"
import { navMetaForPath } from "@/lib/nav-meta"

export function PageHeader({
  title,
  sub,
  action,
}: {
  title: string
  sub?: React.ReactNode
  action?: React.ReactNode
}) {
  return (
    <div className="mb-4 flex flex-col gap-3 sm:flex-row sm:items-start">
      <div className="flex items-start gap-3 flex-1 min-w-0">
        <PageIcon className="mt-0.5" />
        <div className="flex-1 min-w-0">
          <h1 className="text-[22px] font-semibold tracking-[-0.02em]">{title}</h1>
          {sub && (
            <div className="text-[13px] text-[color:var(--text-3)] mt-1">{sub}</div>
          )}
        </div>
      </div>
      {action && <div className="flex items-center gap-2 flex-wrap shrink-0">{action}</div>}
    </div>
  )
}

// PageIcon renders the current route's section icon in its accent colour,
// matching the sidebar (single source of truth in lib/nav-meta). Renders
// nothing for routes no section claims. Used by PageHeader and by the few
// pages (dashboard, live messages) that roll their own header.
export function PageIcon({ size = 22, className }: { size?: number; className?: string }) {
  const meta = navMetaForPath(useLocation().pathname)
  if (!meta) return null
  return (
    <meta.icon
      size={size}
      style={{ color: meta.color }}
      className={["shrink-0", className].filter(Boolean).join(" ")}
    />
  )
}

export function LoadingPanel({ label = "Loading…" }: { label?: string }) {
  return (
    <div className="rounded-[var(--radius-lg)] border border-[color:var(--border)] bg-[color:var(--bg-1)] p-8 text-center text-[13px] text-[color:var(--text-3)]">
      {label}
    </div>
  )
}

export function ErrorPanel({ message }: { message: string }) {
  return (
    <div
      className="rounded-[var(--radius-lg)] border p-5 text-[13px]"
      style={{
        color: "var(--err)",
        background: "var(--err-bg)",
        borderColor: "color-mix(in oklab, var(--err) 30%, var(--border))",
      }}
    >
      Failed to load: <span className="mono">{message}</span>
    </div>
  )
}

export function NotFoundPanel({ kind, name }: { kind: string; name?: string }) {
  return (
    <div className="rounded-[var(--radius-lg)] border border-[color:var(--border)] bg-[color:var(--bg-1)] p-12 text-center">
      <div className="text-[15px] font-medium">No {kind} named <span className="mono">{name}</span></div>
      <div className="text-[12.5px] text-[color:var(--text-3)] mt-1">
        Check the spelling — names are case-sensitive.
      </div>
    </div>
  )
}

export function EmptyPanel({ message }: { message: string }) {
  return (
    <div className="rounded-[var(--radius-lg)] border border-[color:var(--border)] bg-[color:var(--bg-1)] p-12 text-center text-[13px] text-[color:var(--text-3)]">
      {message}
    </div>
  )
}

// useUnauthorizedRedirect bounces to /login when the fetch envelope
// reports an expired session. Centralised here so every page does the
// same thing on 401 — drift between pages would surface as inconsistent
// session-expiry behaviour.
export function useUnauthorizedRedirect(state: ConfigFetchState<unknown>) {
  const nav = useNavigate()
  useEffect(() => {
    if (state.status === "unauthorized") {
      nav("/login", { replace: true })
    }
  }, [state.status, nav])
}
