import { Link } from "react-router"
import { useConnectors, type Connector } from "@/lib/config-api"
import { PanelCard, TableScroll } from "@/components/atoms/card"
import { Tag } from "@/components/atoms/tag"
import {
  PageHeader,
  NewButton,

  LoadingPanel,
  ErrorPanel,
  EmptyPanel,
  useUnauthorizedRedirect,
} from "@/components/atoms/page-states"

export function ConnectorsPage() {
  const { state } = useConnectors()
  useUnauthorizedRedirect(state)

  return (
    <div>
      <PageHeader
        title="Connectors"
        sub="Spool destinations for end-of-pipeline records — S3, Azure Blob, or webhook. A configuration's connector bindings reference these by name."
        action={
          <NewButton to="/connectors/new" label="New connector" />
        }
      />
      {state.status === "loading" && <LoadingPanel />}
      {state.status === "error" && <ErrorPanel message={state.message} />}
      {state.status === "ok" && state.data.length === 0 && (
        <EmptyPanel message="No connectors configured." />
      )}
      {state.status === "ok" && state.data.length > 0 && (
        <PanelCard>
          <TableScroll>
            <thead>
              <tr className="text-[11px] uppercase tracking-[0.07em] text-[color:var(--text-3)]">
                <th className="text-left font-medium px-4 py-2">Name</th>
                <th className="text-left font-medium px-4 py-2">Type</th>
                <th className="text-left font-medium px-4 py-2">Destination</th>
                <th className="text-left font-medium px-4 py-2">Auth</th>
              </tr>
            </thead>
            <tbody>
              {state.data.map((c) => (
                <tr key={c.name} className="border-t border-[color:var(--border)] hover:bg-[color:var(--hover)]">
                  <td className="px-4 py-2.5">
                    <Link to={`/connectors/${encodeURIComponent(c.name)}/edit`} className="mono font-medium hover:underline">{c.name}</Link>
                  </td>
                  <td className="px-4 py-2.5"><Tag variant="violet"><span className="mono">{c.type}</span></Tag></td>
                  <td className="mono text-[12px] px-4 py-2.5 text-[color:var(--text-2)] truncate max-w-[320px]">{destination(c)}</td>
                  <td className="px-4 py-2.5">
                    {c.auth?.mode ? (
                      <Tag variant="default"><span className="mono">{c.auth.mode}</span></Tag>
                    ) : (
                      <span className="text-[color:var(--text-4)]">—</span>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </TableScroll>
        </PanelCard>
      )}
    </div>
  )
}

function destination(c: Connector): string {
  if (c.type === "s3") return `${c.bucket ?? "?"}${c.prefix ? "/" + c.prefix : ""} · ${c.region ?? ""}`
  if (c.type === "azure_blob") return `${c.account ?? "?"} / ${c.container ?? "?"}`
  if (c.type === "webhook") return c.url ?? "?"
  return ""
}
