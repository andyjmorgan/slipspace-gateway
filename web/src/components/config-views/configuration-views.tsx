// Shared configuration list + detail views, consumed by both the gateway admin
// console and the control-plane console. The gateway feeds these from its
// redacted read API (configurationDetailFromGateway); the CP maps a staged
// entity body (configurationDetailFromContract). Page chrome (PageHeader,
// actions, loading/empty states) stays with the caller — these render only the
// data.
//
// Credential safety: the view shape (CredentialView) carries only a backend key
// + a non-reversible mask. Neither mapper supplies a plaintext value, so no
// render path here can leak a raw secret.

import { Link } from "react-router"
import { PanelCard, PanelHead, TableScroll } from "@/components/atoms/card"
import { Tag } from "@/components/atoms/tag"
import { ProviderChip } from "@/components/atoms/provider-chip"
import type {
  ConfigurationListItem,
  ConfigurationDetailData,
  CredentialView,
  ConfigBindingView,
  PassthroughBindingView,
  RuleAttachmentView,
  ConnectorBindingView,
  APIKeyView,
} from "./configuration-views-model"

// ConfigurationListTable renders the configurations table — name, tags, and
// rule count, plus an API-keys count column when the caller supplies one (the
// gateway resolves it; the CP entity store has no back-reference). Each name
// links to the detail route.
export function ConfigurationListTable({
  rows,
  hrefFor = (name) => `/configurations/${encodeURIComponent(name)}`,
}: {
  rows: ConfigurationListItem[]
  hrefFor?: (name: string) => string
}) {
  const showKeys = rows.some((r) => r.keyCount !== undefined)
  return (
    <PanelCard>
      <TableScroll>
        <thead>
          <tr className="text-[11px] uppercase tracking-[0.07em] text-[color:var(--text-3)]">
            <th className="text-left font-medium px-4 py-2">Name</th>
            <th className="text-left font-medium px-4 py-2">Tags</th>
            {showKeys && <th className="text-right font-medium px-4 py-2">API keys</th>}
            <th className="text-right font-medium px-4 py-2">Rules</th>
          </tr>
        </thead>
        <tbody>
          {rows.map((row) => (
            <tr key={row.name} className="border-t border-[color:var(--border)] hover:bg-[color:var(--hover)]">
              <td className="px-4 py-2.5">
                <Link to={hrefFor(row.name)} className="mono font-medium hover:underline">
                  {row.name}
                </Link>
              </td>
              <td className="px-4 py-2.5">
                <div className="flex gap-1.5 flex-wrap">
                  {Object.entries(row.tags ?? {}).map(([k, v]) => (
                    <Tag key={k} variant="default">
                      <span className="mono">{k}={v}</span>
                    </Tag>
                  ))}
                </div>
              </td>
              {showKeys && (
                <td className="mono tnum px-4 py-2.5 text-right">{row.keyCount ?? "—"}</td>
              )}
              <td className="mono tnum px-4 py-2.5 text-right">{row.ruleCount}</td>
            </tr>
          ))}
        </tbody>
      </TableScroll>
    </PanelCard>
  )
}

// ConfigurationDetailView renders the read-only configuration panels —
// credentials (keys + mask, never raw), bindings, passthrough bindings,
// attached rules, connector bindings, and the API keys that resolve here (when
// supplied). The caller owns the page header + edit/delete actions.
export function ConfigurationDetailView({ config }: { config: ConfigurationDetailData }) {
  return (
    <>
      <CredentialsCard creds={config.credentials} />
      <BindingsCard bindings={config.bindings} passthrough={config.passthroughBindings} />
      <AttachedRulesCard rules={config.rules} />
      {config.connectorBindings.length > 0 && (
        <ConnectorBindingsCard bindings={config.connectorBindings} />
      )}
      {config.apiKeys && <APIKeysCard keys={config.apiKeys} />}
    </>
  )
}

function CredentialsCard({ creds }: { creds: CredentialView[] }) {
  return (
    <PanelCard>
      <PanelHead title="Credentials" sub="redacted · managed mode swaps these onto the upstream request, keyed by backend" />
      {creds.length === 0 && (
        <div className="px-4 py-6 text-[12.5px] text-[color:var(--text-4)]">
          No managed-mode credentials. Passthrough-only configuration.
        </div>
      )}
      {creds.length > 0 && (
        <TableScroll>
          <thead>
            <tr className="text-[11px] uppercase tracking-[0.07em] text-[color:var(--text-3)]">
              <th className="text-left font-medium px-4 py-2">Backend</th>
              <th className="text-left font-medium px-4 py-2">Redacted</th>
              {creds.some((c) => c.length !== undefined) && (
                <th className="text-right font-medium px-4 py-2">Length</th>
              )}
            </tr>
          </thead>
          <tbody>
            {creds.map((c) => (
              <tr key={c.backend} className="border-t border-[color:var(--border)]">
                <td className="px-4 py-2.5">
                  <Link to={`/backends/${encodeURIComponent(c.backend)}`}><ProviderChip name={c.backend} /></Link>
                </td>
                <td className="px-4 py-2.5"><Mask mask={c.mask} /></td>
                {creds.some((x) => x.length !== undefined) && (
                  <td className="mono tnum text-right px-4 py-2.5 text-[color:var(--text-3)]">
                    {c.length ?? <span className="text-[color:var(--text-4)]">—</span>}
                  </td>
                )}
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
  bindings: ConfigBindingView[]
  passthrough: PassthroughBindingView[]
}) {
  const total = bindings.length + passthrough.length
  const showTags = bindings.some((b) => b.tags.length > 0)
  return (
    <PanelCard>
      <PanelHead title="Bindings" sub={`how this configuration maps models to backends · ${total}`} />
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
              {showTags && <th className="text-left font-medium px-4 py-2">Tags</th>}
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
                  {b.backend ? (
                    <Link to={`/backends/${encodeURIComponent(b.backend)}`}><ProviderChip name={b.backend} /></Link>
                  ) : b.group ? (
                    <Tag variant="violet"><span className="mono">group {b.group}</span></Tag>
                  ) : (
                    <span className="text-[color:var(--text-4)]">—</span>
                  )}
                </td>
                <td className="mono px-4 py-2.5 text-[color:var(--text-2)]">
                  {b.alias ?? <span className="text-[color:var(--text-4)]">—</span>}
                </td>
                {showTags && (
                  <td className="px-4 py-2.5">
                    <div className="flex gap-1 flex-wrap">
                      {b.tags.length === 0 ? (
                        <span className="text-[color:var(--text-4)]">—</span>
                      ) : (
                        b.tags.map((t) => (
                          <Tag key={t} variant="ghost"><span className="mono">{t}</span></Tag>
                        ))
                      )}
                    </div>
                  </td>
                )}
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
              <th className="text-left font-medium px-4 py-2">Backend</th>
            </tr>
          </thead>
          <tbody>
            {passthrough.map((b, i) => (
              <tr key={`${b.family}::${i}`} className="border-t border-[color:var(--border)]">
                <td className="mono px-4 py-2.5 text-[color:var(--text-2)]">{b.family}</td>
                <td className="px-4 py-2.5">
                  <Link to={`/backends/${encodeURIComponent(b.backend)}`}><ProviderChip name={b.backend} /></Link>
                </td>
              </tr>
            ))}
          </tbody>
        </TableScroll>
      )}
    </PanelCard>
  )
}

function AttachedRulesCard({ rules }: { rules: RuleAttachmentView[] }) {
  const enriched = rules.some((r) => r.conditionSummary !== undefined || (r.actionTypes?.length ?? 0) > 0)
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
              {enriched && <th className="text-left font-medium px-4 py-2">Condition</th>}
              {enriched && <th className="text-left font-medium px-4 py-2">Actions</th>}
              {enriched && <th className="text-left font-medium px-4 py-2">Behavior</th>}
            </tr>
          </thead>
          <tbody>
            {rules.map((r) => (
              <tr key={r.name} className="border-t border-[color:var(--border)] hover:bg-[color:var(--hover)]">
                <td className="px-4 py-2.5">
                  <Link to={`/rules/${encodeURIComponent(r.name)}`} className="mono hover:underline">{r.name}</Link>
                </td>
                {enriched && (
                  <td className="mono text-[12px] px-4 py-2.5 text-[color:var(--text-2)]">{r.conditionSummary}</td>
                )}
                {enriched && (
                  <td className="px-4 py-2.5">
                    <div className="flex gap-1.5 flex-wrap">
                      {(r.actionTypes ?? []).map((t, i) => (
                        <Tag key={i} variant={t.startsWith("change") ? "violet" : "default"}>
                          <span className="mono">{t}</span>
                        </Tag>
                      ))}
                    </div>
                  </td>
                )}
                {enriched && (
                  <td className="px-4 py-2.5">
                    <BehaviorBadge behavior={r.behavior} />
                  </td>
                )}
              </tr>
            ))}
          </tbody>
        </TableScroll>
      )}
    </PanelCard>
  )
}

function ConnectorBindingsCard({ bindings }: { bindings: ConnectorBindingView[] }) {
  return (
    <PanelCard>
      <PanelHead
        title="Connector bindings"
        sub={`record destinations the spool ships matching requests to · ${bindings.length}`}
      />
      <TableScroll>
        <thead>
          <tr className="text-[11px] uppercase tracking-[0.07em] text-[color:var(--text-3)]">
            <th className="text-left font-medium px-4 py-2">Connector</th>
            <th className="text-right font-medium px-4 py-2">Sampling</th>
            <th className="text-left font-medium px-4 py-2">Sampling key</th>
            <th className="text-left font-medium px-4 py-2">Oversize</th>
            <th className="text-left font-medium px-4 py-2">Filter</th>
          </tr>
        </thead>
        <tbody>
          {bindings.map((b, i) => (
            <tr key={`${b.connector}::${i}`} className="border-t border-[color:var(--border)]">
              <td className="px-4 py-2.5">
                <Link to={`/connectors/${encodeURIComponent(b.connector)}`} className="mono hover:underline">{b.connector}</Link>
              </td>
              <td className="mono tnum text-right px-4 py-2.5 text-[color:var(--text-2)]">
                {b.sampling === undefined ? <span className="text-[color:var(--text-4)]">—</span> : b.sampling}
              </td>
              <td className="mono px-4 py-2.5 text-[color:var(--text-2)]">
                {b.samplingKey ?? <span className="text-[color:var(--text-4)]">—</span>}
              </td>
              <td className="mono px-4 py-2.5 text-[color:var(--text-2)]">
                {b.oversizeBehaviour ?? <span className="text-[color:var(--text-4)]">—</span>}
              </td>
              <td className="px-4 py-2.5">
                {b.hasFilter ? <Tag variant="default">filtered</Tag> : <span className="text-[color:var(--text-4)]">—</span>}
              </td>
            </tr>
          ))}
        </tbody>
      </TableScroll>
    </PanelCard>
  )
}

function APIKeysCard({ keys }: { keys: APIKeyView[] }) {
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
        <div className="px-4 py-6 text-[12.5px] text-[color:var(--text-4)]">
          No API keys resolve to this configuration.
        </div>
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
                <td className="px-4 py-2.5"><Mask mask={k.mask} /></td>
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

// Mask renders a credential presence indicator — never a raw value. An empty
// mask means no value is stored; "set" is the CP marker; "••••<last4>" is the
// gateway's redaction.
function Mask({ mask }: { mask: string }) {
  if (mask === "") return <span className="text-[color:var(--text-4)]">—</span>
  if (mask === "set") return <Tag variant="default">set</Tag>
  return <span className="mono text-[12px]">{mask}</span>
}

function BehaviorBadge({ behavior }: { behavior?: string }) {
  if (!behavior || behavior === "continue") return <Tag variant="default">continue</Tag>
  if (behavior === "exit") return <Tag variant="warn">exit</Tag>
  return <Tag variant="ghost">{behavior}</Tag>
}
