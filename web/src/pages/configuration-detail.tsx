import { useState } from "react"
import { Link, useNavigate, useParams } from "react-router"
import { useConfiguration, deleteConfiguration, type RedactedSecret, type RuleAttachment, type APIKeySummary, type BindingRow, type PassthroughBindingRow } from "@/lib/config-api"
import { Tabs, TabsList, TabsTrigger, TabsContent } from "@/components/ui/tabs"
import { PanelCard, PanelHead, TableScroll } from "@/components/atoms/card"
import { Tag } from "@/components/atoms/tag"
import { ProviderChip } from "@/components/atoms/provider-chip"
import { Button } from "@/components/ui/button"
import { DeleteDialog } from "@/components/forms/write-atoms"
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
  const nav = useNavigate()
  const { state } = useConfiguration(name)
  useUnauthorizedRedirect(state)
  const [confirmDelete, setConfirmDelete] = useState(false)

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
          <div className="flex items-center gap-2">
            <Link to={`/configurations/${encodeURIComponent(c.name)}/edit`}>
              <Button size="sm" variant="outline">Edit</Button>
            </Link>
            <Button size="sm" variant="destructive" onClick={() => setConfirmDelete(true)}>Delete</Button>
            <Link to="/configurations" className="text-[12.5px] text-[color:var(--text-3)] hover:underline ml-1">
              ← back
            </Link>
          </div>
        }
      />

      <DeleteDialog
        open={confirmDelete}
        resourceKind="configuration"
        resourceName={c.name}
        requireConfirmName
        onConfirm={async () => {
          await deleteConfiguration(c.name)
          nav("/configurations", { replace: true })
        }}
        onClose={() => setConfirmDelete(false)}
      />

      <Tabs defaultValue="credentials" className="gap-4">
        <TabsList variant="line" className="flex-wrap">
          <TabsTrigger value="credentials">
            Credentials{Object.keys(c.credentials).length > 0 ? ` · ${Object.keys(c.credentials).length}` : ""}
          </TabsTrigger>
          <TabsTrigger value="bindings">
            Bindings{c.bindings.length + c.passthrough_bindings.length > 0 ? ` · ${c.bindings.length + c.passthrough_bindings.length}` : ""}
          </TabsTrigger>
          <TabsTrigger value="rules">
            Rules{c.rules.length > 0 ? ` · ${c.rules.length}` : ""}
          </TabsTrigger>
          <TabsTrigger value="api-keys">
            API keys{c.api_keys.length > 0 ? ` · ${c.api_keys.length}` : ""}
          </TabsTrigger>
        </TabsList>

        <TabsContent value="credentials">
          <CredentialsCard creds={c.credentials} />
        </TabsContent>

        <TabsContent value="bindings">
          <BindingsCard bindings={c.bindings} passthrough={c.passthrough_bindings} />
        </TabsContent>

        <TabsContent value="rules">
          <AttachedRulesCard rules={c.rules} />
        </TabsContent>

        <TabsContent value="api-keys">
          <APIKeysCard keys={c.api_keys} />
        </TabsContent>
      </Tabs>
    </div>
  )
}

function CredentialsCard({ creds }: { creds: Record<string, RedactedSecret> }) {
  const entries = Object.entries(creds)
  return (
    <PanelCard>
      <PanelHead title="Credentials" sub="redacted · managed mode swaps these onto the upstream request, keyed by provider" />
      {entries.length === 0 && (
        <div className="px-4 py-6 text-[12.5px] text-[color:var(--text-4)]">
          No managed-mode credentials. Passthrough-only configuration.
        </div>
      )}
      {entries.length > 0 && (
        <TableScroll>
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
                <td className="px-4 py-2.5">
                  <Link to={`/providers/${encodeURIComponent(provider)}`}><ProviderChip name={provider} /></Link>
                </td>
                <td className="px-4 py-2.5"><Redacted r={redacted} /></td>
                <td className="mono tnum text-right px-4 py-2.5 text-[color:var(--text-3)]">{redacted.length}</td>
              </tr>
            ))}
          </tbody>
        </TableScroll>
      )}
    </PanelCard>
  )
}

function BindingsCard({
  bindings,
  passthrough,
}: {
  bindings: BindingRow[]
  passthrough: PassthroughBindingRow[]
}) {
  const total = bindings.length + passthrough.length
  return (
    <PanelCard>
      <PanelHead title="Bindings" sub={`how this configuration maps models to providers · ${total}`} />
      {total === 0 && (
        <div className="px-4 py-6 text-[12.5px] text-[color:var(--text-4)]">No bindings configured.</div>
      )}
      {bindings.length > 0 && (
        <TableScroll>
          <thead>
            <tr className="text-[11px] uppercase tracking-[0.07em] text-[color:var(--text-3)]">
              <th className="text-left font-medium px-4 py-2">Protocol</th>
              <th className="text-left font-medium px-4 py-2">Models</th>
              <th className="text-left font-medium px-4 py-2">Target</th>
              <th className="text-left font-medium px-4 py-2">Alias</th>
            </tr>
          </thead>
          <tbody>
            {bindings.map((b, i) => (
              <tr key={`${b.protocol}::${i}`} className="border-t border-[color:var(--border)]">
                <td className="mono px-4 py-2.5 text-[color:var(--text-2)]">{b.protocol}</td>
                <td className="px-4 py-2.5">
                  <div className="flex gap-1 flex-wrap">
                    {b.models.length === 0 ? (
                      <Tag variant="ghost">any</Tag>
                    ) : (
                      b.models.map((m) => (
                        <Tag key={m} variant="default"><span className="mono">{m}</span></Tag>
                      ))
                    )}
                  </div>
                </td>
                <td className="px-4 py-2.5">
                  {b.provider ? (
                    <Link to={`/providers/${encodeURIComponent(b.provider)}`}><ProviderChip name={b.provider} /></Link>
                  ) : b.group ? (
                    <Tag variant="violet"><span className="mono">group {b.group}</span></Tag>
                  ) : (
                    <span className="text-[color:var(--text-4)]">—</span>
                  )}
                </td>
                <td className="mono px-4 py-2.5 text-[color:var(--text-2)]">
                  {b.alias ?? <span className="text-[color:var(--text-4)]">—</span>}
                </td>
              </tr>
            ))}
          </tbody>
        </TableScroll>
      )}
      {passthrough.length > 0 && (
        <TableScroll className="border-t border-[color:var(--border)]">
          <thead>
            <tr className="text-[11px] uppercase tracking-[0.07em] text-[color:var(--text-3)]">
              <th className="text-left font-medium px-4 py-2">Passthrough family</th>
              <th className="text-left font-medium px-4 py-2">Provider</th>
            </tr>
          </thead>
          <tbody>
            {passthrough.map((b, i) => (
              <tr key={`${b.family}::${i}`} className="border-t border-[color:var(--border)]">
                <td className="mono px-4 py-2.5 text-[color:var(--text-2)]">{b.family}</td>
                <td className="px-4 py-2.5">
                  <Link to={`/providers/${encodeURIComponent(b.provider)}`}><ProviderChip name={b.provider} /></Link>
                </td>
              </tr>
            ))}
          </tbody>
        </TableScroll>
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
        <TableScroll>
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
        </TableScroll>
      )}
    </PanelCard>
  )
}


function APIKeysCard({ keys }: { keys: APIKeySummary[] }) {
  return (
    <PanelCard>
      <PanelHead
        title="API keys"
        sub={`${keys.length} key${keys.length === 1 ? "" : "s"} resolve to this configuration`}
        action={
          <Link to="/api-keys" className="text-[12px] text-[color:var(--text-3)] hover:underline">
            manage →
          </Link>
        }
      />
      {keys.length === 0 && (
        <EmptyPanel message="No API keys resolve to this configuration." />
      )}
      {keys.length > 0 && (
        <TableScroll>
          <thead>
            <tr className="text-[11px] uppercase tracking-[0.07em] text-[color:var(--text-3)]">
              <th className="text-left font-medium px-4 py-2">Name</th>
              <th className="text-left font-medium px-4 py-2">Secret</th>
              <th className="text-left font-medium px-4 py-2">Enabled</th>
            </tr>
          </thead>
          <tbody>
            {keys.map((k) => (
              <tr key={k.name} className="border-t border-[color:var(--border)]">
                <td className="px-4 py-2.5 mono">{k.name}</td>
                <td className="px-4 py-2.5"><Redacted r={k.secret} /></td>
                <td className="px-4 py-2.5">
                  {k.enabled ? <Tag variant="success">enabled</Tag> : <Tag variant="danger">disabled</Tag>}
                </td>
              </tr>
            ))}
          </tbody>
        </TableScroll>
      )}
    </PanelCard>
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

function BehaviorBadge({ behavior }: { behavior?: string }) {
  if (!behavior || behavior === "continue") return <Tag variant="default">continue</Tag>
  if (behavior === "exit") return <Tag variant="warn">exit</Tag>
  return <Tag variant="ghost">{behavior}</Tag>
}
