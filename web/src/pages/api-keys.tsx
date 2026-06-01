// APIKeysPage is the standalone api-key management view — distinct from
// configurations. It lists every gateway-issued key (redacted), mints new
// ones (the plaintext secret is shown exactly once in a modal), toggles
// enable/disable as the primary reversible action, reassigns the bound
// configuration, and deletes behind a type-to-confirm warning dialog.
//
// The secret is never editable: rotation is a deliberate re-mint, not an
// edit. PATCH {enabled} is the everyday off-switch; DELETE is the
// destructive last resort.

import { useState } from "react"
import { Copy, Check, Eye, EyeOff, Plus } from "lucide-react"
import {
  useAPIKeys,
  useConfigurations,
  createAPIKey,
  patchAPIKey,
  replaceAPIKey,
  deleteAPIKey,
  revealAPIKey,
  apiKeyRef,
  type APIKeyListItem,
  type APIKeyReveal,
  type RedactedSecret,
  type ConfigurationSummary,
} from "@/lib/config-api"
import { PanelCard, PanelHead, TableScroll } from "@/components/atoms/card"
import { Tag } from "@/components/atoms/tag"
import { Button } from "@/components/ui/button"
import { SelectField, CheckboxField, TextField, type SelectOption } from "@/components/forms/field-atoms"
import { ErrorBanner, DeleteDialog } from "@/components/forms/write-atoms"
import { classifyWriteError, type EditorError } from "@/lib/write-error"
import {
  PageHeader,
  LoadingPanel,
  ErrorPanel,
  EmptyPanel,
  useUnauthorizedRedirect,
} from "@/components/atoms/page-states"
import { APIError, UnauthorizedError } from "@/lib/api"

export function APIKeysPage() {
  const { state, refetch } = useAPIKeys()
  const { state: cfgState } = useConfigurations()
  useUnauthorizedRedirect(state)

  const [minting, setMinting] = useState(false)
  const [minted, setMinted] = useState<APIKeyReveal | null>(null)
  const [editing, setEditing] = useState<APIKeyListItem | null>(null)
  const [deleting, setDeleting] = useState<APIKeyListItem | null>(null)

  const configurations: ConfigurationSummary[] = cfgState.status === "ok" ? cfgState.data : []

  return (
    <div>
      <PageHeader
        title="API keys"
        sub="Gateway-issued bearer keys. Each resolves to one configuration; enable/disable is the reversible off-switch."
        action={
          <Button variant="ghost" size="sm" onClick={() => setMinting(true)} aria-label="Mint key">
            <Plus /> <span className="hidden sm:inline">Mint key</span>
          </Button>
        }
      />

      {state.status === "loading" && <LoadingPanel />}
      {state.status === "error" && <ErrorPanel message={state.message} />}
      {state.status === "ok" && state.data.length === 0 && (
        <EmptyPanel message="No API keys. Mint one to authenticate a client against a configuration." />
      )}
      {state.status === "ok" && state.data.length > 0 && (
        <PanelCard>
          <PanelHead title="Keys" sub={`${state.data.length} · secret shown only once, at mint`} />
          <TableScroll>
            <thead>
              <tr className="text-[11px] uppercase tracking-[0.07em] text-[color:var(--text-3)]">
                <th className="text-left font-medium px-4 py-2">Name</th>
                <th className="text-left font-medium px-4 py-2">Configuration</th>
                <th className="text-left font-medium px-4 py-2">Secret</th>
                <th className="text-left font-medium px-4 py-2">Enabled</th>
                <th className="text-right font-medium px-4 py-2 w-48">Actions</th>
              </tr>
            </thead>
            <tbody>
              {state.data.map((k) => (
                <APIKeyRow
                  key={apiKeyRef(k)}
                  k={k}
                  onChanged={refetch}
                  onEdit={() => setEditing(k)}
                  onDelete={() => setDeleting(k)}
                />
              ))}
            </tbody>
          </TableScroll>
        </PanelCard>
      )}

      {minting && (
        <MintDialog
          configurations={configurations}
          onMinted={(r) => {
            setMinting(false)
            setMinted(r)
            refetch()
          }}
          onClose={() => setMinting(false)}
        />
      )}

      {minted && <RevealDialog reveal={minted} onClose={() => setMinted(null)} />}

      {editing && (
        <EditDialog
          k={editing}
          configurations={configurations}
          onSaved={() => {
            setEditing(null)
            refetch()
          }}
          onClose={() => setEditing(null)}
        />
      )}

      {deleting && (
        <DeleteDialog
          open
          resourceKind="API key"
          resourceName={deleting.name}
          requireConfirmName
          onConfirm={async () => {
            await deleteAPIKey(apiKeyRef(deleting))
            setDeleting(null)
            refetch()
          }}
          onClose={() => setDeleting(null)}
        />
      )}
    </div>
  )
}

function APIKeyRow({
  k,
  onChanged,
  onEdit,
  onDelete,
}: {
  k: APIKeyListItem
  onChanged: () => void
  onEdit: () => void
  onDelete: () => void
}) {
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState<string | null>(null)
  const [revealed, setRevealed] = useState<string | null>(null)
  const [revealing, setRevealing] = useState(false)
  const [copied, setCopied] = useState(false)
  const ref = apiKeyRef(k)

  // reveal fetches the stored plaintext secret for an existing key via the
  // reveal endpoint (admin-auth gated — the operator already holds the
  // password). Parity with the per-key reveal on the configuration detail page.
  const reveal = async () => {
    setRevealing(true)
    setErr(null)
    try {
      const r = await revealAPIKey(k.configuration, k.name)
      setRevealed(r.secret)
    } catch (e) {
      if (e instanceof UnauthorizedError) setErr("Session expired — log in again.")
      else setErr(e instanceof Error ? e.message : String(e))
    } finally {
      setRevealing(false)
    }
  }

  const copyRevealed = async () => {
    if (revealed == null) return
    try {
      await navigator.clipboard.writeText(revealed)
      setCopied(true)
    } catch {
      // clipboard unavailable — operator can select-copy the shown value
    }
  }

  const toggle = async () => {
    setBusy(true)
    setErr(null)
    try {
      await patchAPIKey(ref, { enabled: !k.enabled })
      onChanged()
    } catch (e) {
      if (e instanceof UnauthorizedError) setErr("Session expired — log in again.")
      else setErr(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }

  return (
    <tr className="border-t border-[color:var(--border)]">
      <td className="px-4 py-2.5 mono">{k.name}</td>
      <td className="px-4 py-2.5 mono text-[color:var(--text-2)]">{k.configuration}</td>
      <td className="px-4 py-2.5">
        {revealed != null ? (
          <span className="inline-flex items-center gap-1.5">
            <span className="mono text-[12px] break-all">{revealed}</span>
            <button
              type="button"
              aria-label="Copy secret"
              onClick={copyRevealed}
              className="inline-flex items-center text-[color:var(--text-3)] hover:text-[color:var(--text)]"
            >
              {copied ? <Check size={11} /> : <Copy size={11} />}
            </button>
            <button
              type="button"
              aria-label="Hide secret"
              onClick={() => {
                setRevealed(null)
                setCopied(false)
              }}
              className="inline-flex items-center text-[color:var(--text-3)] hover:text-[color:var(--text)]"
            >
              <EyeOff size={11} />
            </button>
          </span>
        ) : (
          <span className="inline-flex items-center gap-2">
            <Redacted r={k.secret} />
            <button
              type="button"
              onClick={reveal}
              disabled={revealing}
              className="inline-flex items-center gap-1 text-[11px] text-[color:var(--text-3)] hover:text-[color:var(--text)]"
            >
              <Eye size={11} />
              <span>{revealing ? "…" : "reveal"}</span>
            </button>
          </span>
        )}
      </td>
      <td className="px-4 py-2.5">
        {k.enabled ? <Tag variant="success">enabled</Tag> : <Tag variant="danger">disabled</Tag>}
        {err && <div className="text-[11px] mt-1" style={{ color: "var(--err)" }}>{err}</div>}
      </td>
      <td className="px-4 py-2.5">
        <div className="flex items-center gap-1.5 justify-end">
          <Button type="button" size="xs" variant={k.enabled ? "outline" : "default"} onClick={toggle} disabled={busy}>
            {busy ? "…" : k.enabled ? "Disable" : "Enable"}
          </Button>
          <Button type="button" size="xs" variant="ghost" onClick={onEdit}>Edit</Button>
          <Button
            type="button"
            size="xs"
            variant="ghost"
            className="text-[color:var(--text-3)] hover:text-[color:var(--err)]"
            onClick={onDelete}
          >
            Delete
          </Button>
        </div>
      </td>
    </tr>
  )
}

function MintDialog({
  configurations,
  onMinted,
  onClose,
}: {
  configurations: ConfigurationSummary[]
  onMinted: (r: APIKeyReveal) => void
  onClose: () => void
}) {
  const [name, setName] = useState("")
  const [configuration, setConfiguration] = useState(configurations[0]?.name ?? "")
  const [enabled, setEnabled] = useState(true)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<EditorError | null>(null)

  const options: SelectOption[] = configurations.map((c) => ({ value: c.name }))

  const submit = async () => {
    setBusy(true)
    setError(null)
    try {
      const r = await createAPIKey({ name: name.trim(), configuration, enabled })
      onMinted(r)
    } catch (e) {
      if (e instanceof APIError) setError(classifyWriteError(e))
      else setError({ kind: "generic", message: e instanceof Error ? e.message : String(e) })
      setBusy(false)
    }
  }

  return (
    <Modal title="Mint API key" onClose={busy ? undefined : onClose}>
      <div className="px-5 py-4 flex flex-col gap-3">
        {error && <ErrorBanner error={error} />}
        <TextField label="Name" value={name} onChange={setName} placeholder="svc-ingest" mono hint="Human-readable label. Must be unique." />
        {options.length > 0 ? (
          <SelectField label="Configuration" value={configuration} options={options} onChange={setConfiguration} hint="The policy bundle this key resolves to." />
        ) : (
          <TextField label="Configuration" value={configuration} onChange={setConfiguration} placeholder="production" mono hint="No configurations loaded — type a name." />
        )}
        <CheckboxField label="Enabled" checked={enabled} onChange={setEnabled} />
      </div>
      <div className="px-5 py-3 border-t border-[color:var(--border)] flex items-center justify-end gap-2">
        <Button type="button" variant="ghost" onClick={onClose} disabled={busy}>Cancel</Button>
        <Button type="button" onClick={submit} disabled={busy || name.trim() === "" || configuration.trim() === ""}>
          {busy ? "Minting…" : "Mint key"}
        </Button>
      </div>
    </Modal>
  )
}

function RevealDialog({ reveal, onClose }: { reveal: APIKeyReveal; onClose: () => void }) {
  const [copied, setCopied] = useState(false)
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(reveal.secret)
      setCopied(true)
      setTimeout(() => setCopied(false), 1500)
    } catch {
      // clipboard unavailable — operator can select-copy the text manually
    }
  }
  return (
    <Modal title="Key minted — copy it now" onClose={onClose}>
      <div className="px-5 py-4 flex flex-col gap-3">
        <div
          className="rounded-[var(--radius)] border p-3 text-[12.5px]"
          style={{
            color: "var(--warn)",
            background: "var(--warn-bg)",
            borderColor: "color-mix(in oklab, var(--warn) 30%, var(--border))",
          }}
        >
          This is the only time the plaintext secret for <span className="mono">{reveal.name}</span> is shown. Copy it
          now and store it securely — you will not see it again. To rotate, mint a new key.
        </div>
        <div className="flex items-center gap-2">
          <span className="mono text-[12px] break-all flex-1 px-2 py-2 rounded-[5px] border border-[color:var(--border)] bg-[color:var(--bg-0)]">
            {reveal.secret}
          </span>
          <Button type="button" size="sm" variant="outline" onClick={copy}>
            {copied ? <Check size={13} /> : <Copy size={13} />}
            <span>{copied ? "copied" : "copy"}</span>
          </Button>
        </div>
        <div className="text-[11.5px] text-[color:var(--text-4)] mono">
          configuration: {reveal.configuration} · {reveal.enabled ? "enabled" : "disabled"}
        </div>
      </div>
      <div className="px-5 py-3 border-t border-[color:var(--border)] flex items-center justify-end gap-2">
        <Button type="button" onClick={onClose}>Done</Button>
      </div>
    </Modal>
  )
}

function EditDialog({
  k,
  configurations,
  onSaved,
  onClose,
}: {
  k: APIKeyListItem
  configurations: ConfigurationSummary[]
  onSaved: () => void
  onClose: () => void
}) {
  const [name, setName] = useState(k.name)
  const [configuration, setConfiguration] = useState(k.configuration)
  const [enabled, setEnabled] = useState(k.enabled)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<EditorError | null>(null)
  const ref = apiKeyRef(k)

  const options: SelectOption[] = configurations.map((c) => ({ value: c.name }))

  const submit = async () => {
    setBusy(true)
    setError(null)
    try {
      // Reassign config + enabled via PUT; rename (if changed) via PATCH,
      // which the backend handles as a separate name-uniqueness path.
      await replaceAPIKey(ref, { configuration, enabled })
      if (name.trim() !== k.name) {
        await patchAPIKey(ref, { name: name.trim() })
      }
      onSaved()
    } catch (e) {
      if (e instanceof APIError) setError(classifyWriteError(e))
      else setError({ kind: "generic", message: e instanceof Error ? e.message : String(e) })
      setBusy(false)
    }
  }

  return (
    <Modal title={`Edit key · ${k.name}`} onClose={busy ? undefined : onClose}>
      <div className="px-5 py-4 flex flex-col gap-3">
        {error && <ErrorBanner error={error} />}
        <TextField label="Name" value={name} onChange={setName} mono hint="Rename applies via PATCH; must stay unique." />
        {options.length > 0 ? (
          <SelectField label="Configuration" value={configuration} options={options} onChange={setConfiguration} />
        ) : (
          <TextField label="Configuration" value={configuration} onChange={setConfiguration} mono />
        )}
        <CheckboxField label="Enabled" checked={enabled} onChange={setEnabled} />
        <div className="text-[11.5px] text-[color:var(--text-4)]">
          The secret is immutable — rotation is a deliberate re-mint, not an edit.
        </div>
      </div>
      <div className="px-5 py-3 border-t border-[color:var(--border)] flex items-center justify-end gap-2">
        <Button type="button" variant="ghost" onClick={onClose} disabled={busy}>Cancel</Button>
        <Button type="button" onClick={submit} disabled={busy || name.trim() === "" || configuration.trim() === ""}>
          {busy ? "Saving…" : "Save changes"}
        </Button>
      </div>
    </Modal>
  )
}

function Modal({ title, children, onClose }: { title: string; children: React.ReactNode; onClose?: () => void }) {
  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center p-4 bg-black/50"
      role="dialog"
      aria-modal="true"
      onMouseDown={(e) => {
        if (e.target === e.currentTarget && onClose) onClose()
      }}
    >
      <div className="w-full max-w-md rounded-[var(--radius-lg)] border border-[color:var(--border)] bg-[color:var(--bg-1)] shadow-xl">
        <div className="px-5 py-4 border-b border-[color:var(--border)]">
          <h2 className="text-[15px] font-semibold">{title}</h2>
        </div>
        {children}
      </div>
    </div>
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
