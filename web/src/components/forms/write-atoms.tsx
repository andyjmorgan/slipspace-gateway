// Shared write-flow primitives for the read-write config editors
// (providers, groups, configurations, connectors, api_keys). The rules
// editor predates these and rolls its own; everything added in the
// read-write surface funnels through here so the destructive-action
// warning, the dry-run preview banner, and the structured error banner
// look and behave identically across every resource.

import { useEffect, useMemo, useState } from "react"
import { APIError } from "@/lib/api"
import type { PreviewResult } from "@/lib/config-api"
import { classifyWriteError, type EditorError } from "@/lib/write-error"
import { Button } from "@/components/ui/button"

const errPanelStyle = {
  color: "var(--err)",
  background: "var(--err-bg)",
  borderColor: "color-mix(in oklab, var(--err) 30%, var(--border))",
}

export function ErrorBanner({ error }: { error: EditorError }) {
  const heading = useMemo(() => {
    if (error.kind === "conflict") return "Conflict — write rejected"
    if (error.kind === "validation") return "Validation failed"
    return "Save failed"
  }, [error])
  return (
    <div className="rounded-[var(--radius-lg)] border p-4 text-[13px]" style={errPanelStyle}>
      <div className="font-semibold mb-1">{heading}</div>
      {error.kind === "conflict" && (
        <>
          <div>{error.message}</div>
          {error.usedBy && error.usedBy.length > 0 && (
            <div className="mt-2 text-[12px]">
              Referenced by: {error.usedBy.map((n) => <span key={n} className="mono mr-2">{n}</span>)}
            </div>
          )}
        </>
      )}
      {error.kind === "validation" && (
        <div className="mono text-[12px] whitespace-pre-wrap">{error.detail}</div>
      )}
      {error.kind === "generic" && <div className="mono text-[12px]">{error.message}</div>}
    </div>
  )
}

// PreviewBanner renders the dry-run validation result above the save row.
// valid -> a low-key "would apply cleanly" note; invalid -> the error.
export function PreviewBanner({ result, onDismiss }: { result: PreviewResult; onDismiss: () => void }) {
  if (result.valid) {
    return (
      <div
        className="rounded-[var(--radius-lg)] border p-3 text-[12.5px] flex items-start gap-2"
        style={{
          color: "var(--ok)",
          background: "var(--ok-bg)",
          borderColor: "color-mix(in oklab, var(--ok) 30%, var(--border))",
        }}
      >
        <div className="flex-1">
          <span className="font-semibold">Preview: valid.</span> This change would apply cleanly. Click Save to commit.
        </div>
        <DismissX onClick={onDismiss} />
      </div>
    )
  }
  return (
    <div className="rounded-[var(--radius-lg)] border p-3 text-[12.5px] flex items-start gap-2" style={errPanelStyle}>
      <div className="flex-1">
        <span className="font-semibold">Preview: would be rejected.</span>
        <div className="mono text-[12px] whitespace-pre-wrap mt-1">{result.error}</div>
      </div>
      <DismissX onClick={onDismiss} />
    </div>
  )
}

function DismissX({ onClick }: { onClick: () => void }) {
  return (
    <button
      type="button"
      onClick={onClick}
      aria-label="Dismiss preview"
      className="text-[12px] opacity-70 hover:opacity-100 leading-none"
    >
      ✕
    </button>
  )
}

// DeleteDialog is the destructive-action warning. requireConfirmName turns
// on type-to-confirm (high-blast-radius resources: providers, configurations,
// api_keys); the operator must type the resource name to enable Delete.
// A 409 ConflictError surfaces the used_by list inline so the operator knows
// what references the resource before they can remove it.
export function DeleteDialog({
  open,
  resourceKind,
  resourceName,
  requireConfirmName,
  onConfirm,
  onClose,
}: {
  open: boolean
  resourceKind: string
  resourceName: string
  requireConfirmName?: boolean
  onConfirm: () => Promise<void>
  onClose: () => void
}) {
  const [typed, setTyped] = useState("")
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<EditorError | null>(null)

  useEffect(() => {
    if (open) {
      // eslint-disable-next-line react-hooks/set-state-in-effect
      setTyped("")
      setBusy(false)
      setError(null)
    }
  }, [open])

  if (!open) return null

  const confirmed = !requireConfirmName || typed === resourceName

  const commit = async () => {
    setBusy(true)
    setError(null)
    try {
      await onConfirm()
    } catch (e) {
      if (e instanceof APIError) {
        setError(classifyWriteError(e))
      } else {
        setError({ kind: "generic", message: e instanceof Error ? e.message : String(e) })
      }
      setBusy(false)
    }
  }

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center p-4 bg-black/50"
      role="dialog"
      aria-modal="true"
      onMouseDown={(e) => {
        if (e.target === e.currentTarget && !busy) onClose()
      }}
    >
      <div className="w-full max-w-md rounded-[var(--radius-lg)] border border-[color:var(--border)] bg-[color:var(--bg-1)] shadow-xl">
        <div className="px-5 py-4 border-b border-[color:var(--border)]">
          <h2 className="text-[15px] font-semibold" style={{ color: "var(--err)" }}>
            Delete {resourceKind}
          </h2>
          <p className="text-[12.5px] text-[color:var(--text-3)] mt-1">
            This permanently removes <span className="mono text-[color:var(--text)]">{resourceName}</span> and rewrites
            <span className="mono"> policy.yaml</span>. In-flight requests finish on the pre-delete config; new requests
            see the change immediately. This cannot be undone.
          </p>
        </div>

        <div className="px-5 py-4 flex flex-col gap-3">
          {requireConfirmName && (
            <div className="flex flex-col gap-1.5">
              <label className="text-[11px] text-[color:var(--text-3)]">
                Type <span className="mono text-[color:var(--text)]">{resourceName}</span> to confirm:
              </label>
              <input
                type="text"
                value={typed}
                onChange={(e) => setTyped(e.target.value)}
                autoFocus
                className="mono rounded-[5px] border border-[color:var(--border)] bg-[color:var(--bg-2)] px-2 py-1 text-[12.5px] text-[color:var(--text)] outline-none focus:border-[color:var(--text-3)]"
              />
            </div>
          )}
          {error && <ErrorBanner error={error} />}
        </div>

        <div className="px-5 py-3 border-t border-[color:var(--border)] flex items-center justify-end gap-2">
          <Button type="button" variant="ghost" onClick={onClose} disabled={busy}>
            Cancel
          </Button>
          <Button type="button" variant="destructive" onClick={commit} disabled={busy || !confirmed}>
            {busy ? "Deleting…" : `Delete ${resourceKind}`}
          </Button>
        </div>
      </div>
    </div>
  )
}
