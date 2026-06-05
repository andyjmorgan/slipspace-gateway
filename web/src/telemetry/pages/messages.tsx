import { useCallback, useEffect, useState } from "react"
import { useNavigate } from "react-router"
import { ChevronDown, ChevronUp, RefreshCw, X } from "lucide-react"
import { Button } from "@/components/ui/button"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { StatusPill } from "@/components/atoms/status-pill"
import { ProviderChip } from "@/components/atoms/provider-chip"
import { JsonViewer } from "@/components/atoms/json-viewer"
import { PanelCard, PanelHead, TableScroll } from "@/components/atoms/card"
import { fmt } from "@/lib/fmt"
import { UnauthorizedError } from "@/lib/api"
import {
  fetchMessageBody,
  fetchRecentMessages,
  type MessageBodyDetail,
  type MessageEntry,
} from "@/lib/messages"

const POLL_MS = 5000
const LIMIT = 200

// MessagesPage lists the most recent requests the telemetry store has stitched
// (from the gen_ai OTLP feed + the Record feed) and opens a per-request
// inspector. The telemetry service has no live SSE stream, so this polls
// /messages/recent rather than streaming; the inspector reads the captured
// bodies via /messages/{id}/body.
export function MessagesPage() {
  const nav = useNavigate()
  const [entries, setEntries] = useState<MessageEntry[]>([])
  const [status, setStatus] = useState<"loading" | "ok" | "error">("loading")
  const [err, setErr] = useState("")
  const [selected, setSelected] = useState<number | null>(null)

  const load = useCallback(() => {
    fetchRecentMessages(LIMIT)
      .then((r) => {
        // Newest first for the table.
        setEntries([...r.entries].reverse())
        setStatus("ok")
      })
      .catch((e) => {
        if (e instanceof UnauthorizedError) {
          nav("/login", { replace: true })
          return
        }
        setErr(e instanceof Error ? e.message : String(e))
        setStatus((s) => (s === "loading" ? "error" : s))
      })
  }, [nav])

  useEffect(() => {
    load()
    const id = setInterval(load, POLL_MS)
    return () => clearInterval(id)
  }, [load])

  return (
    <div className="flex flex-col gap-3.5">
      <div className="flex items-center gap-3">
        <div className="flex-1">
          <h1 className="text-[24px] font-semibold tracking-[-0.02em]">Messages</h1>
          <div className="text-[13px] text-[color:var(--text-3)] mt-1">Recent requests · polled every {POLL_MS / 1000}s</div>
        </div>
        <Button variant="ghost" size="sm" onClick={load} aria-label="Refresh">
          <RefreshCw /> <span className="hidden sm:inline">Refresh</span>
        </Button>
      </div>

      {status === "error" && <div className="rounded-[var(--radius-lg)] border p-5 text-[13px]" style={{ color: "var(--err)", background: "var(--err-bg)" }}>Failed to load messages: <span className="mono">{err}</span></div>}

      <PanelCard>
        <PanelHead title="Recent requests" sub={`${entries.length} shown`} />
        <TableScroll>
          <thead>
            <tr className="text-[11px] uppercase tracking-[0.07em] text-[color:var(--text-3)]">
              <th className="text-left font-medium px-4 py-2">Time</th>
              <th className="text-left font-medium px-4 py-2">Status</th>
              <th className="text-left font-medium px-4 py-2">Provider</th>
              <th className="text-left font-medium px-4 py-2">Endpoint</th>
              <th className="text-left font-medium px-4 py-2">Model</th>
              <th className="text-left font-medium px-4 py-2">Configuration</th>
              <th className="text-right font-medium px-4 py-2">Duration</th>
              <th className="text-right font-medium px-4 py-2">Tokens</th>
            </tr>
          </thead>
          <tbody>
            {entries.map((e, i) => (
              <tr
                key={e.event_id}
                onClick={() => setSelected(i)}
                className="border-t border-[color:var(--border)] cursor-pointer hover:bg-[color:var(--hover)]"
              >
                <td className="mono text-[11.5px] px-4 py-2 text-[color:var(--text-3)] whitespace-nowrap">{shortTime(e.at)}</td>
                <td className="px-4 py-2"><StatusPill code={e.status_code} /></td>
                <td className="px-4 py-2">{e.provider ? <ProviderChip name={e.provider} /> : <Dash />}</td>
                <td className="mono text-[12px] px-4 py-2">{e.endpoint || <Dash />}</td>
                <td className="mono text-[12px] px-4 py-2">{e.model || <Dash />}</td>
                <td className="mono text-[12px] px-4 py-2">{e.configuration || <Dash />}</td>
                <td className="mono tnum text-[12px] text-right px-4 py-2">{fmt.ms(e.duration_ms)}</td>
                <td className="mono tnum text-[11.5px] text-right px-4 py-2 text-[color:var(--text-3)]">
                  {(e.tokens_in ?? 0) + (e.tokens_out ?? 0) > 0 ? `${fmt.compact(e.tokens_in ?? 0)}/${fmt.compact(e.tokens_out ?? 0)}` : <Dash />}
                </td>
              </tr>
            ))}
            {status === "ok" && entries.length === 0 && (
              <tr><td colSpan={8} className="px-4 py-10 text-center text-[12px] text-[color:var(--text-4)]">No requests recorded yet.</td></tr>
            )}
          </tbody>
        </TableScroll>
      </PanelCard>

      {selected !== null && entries[selected] && (
        <Inspector
          entry={entries[selected]}
          position={`${selected + 1} / ${entries.length}`}
          onClose={() => setSelected(null)}
          onPrev={selected > 0 ? () => setSelected(selected - 1) : undefined}
          onNext={selected < entries.length - 1 ? () => setSelected(selected + 1) : undefined}
        />
      )}
    </div>
  )
}

function Dash() {
  return <span className="text-[color:var(--text-4)]">—</span>
}

function Inspector({
  entry,
  position,
  onClose,
  onPrev,
  onNext,
}: {
  entry: MessageEntry
  position: string
  onClose: () => void
  onPrev?: () => void
  onNext?: () => void
}) {
  const [body, setBody] = useState<MessageBodyDetail | null | undefined>(undefined)

  useEffect(() => {
    let cancelled = false
    setBody(undefined)
    fetchMessageBody(entry.event_id)
      .then((b) => { if (!cancelled) setBody(b) })
      .catch(() => { if (!cancelled) setBody(null) })
    return () => { cancelled = true }
  }, [entry.event_id])

  // Keyboard nav: Esc close, arrows prev/next.
  useEffect(() => {
    const onKey = (ev: KeyboardEvent) => {
      if (ev.key === "Escape") onClose()
      else if (ev.key === "ArrowUp" && onPrev) { ev.preventDefault(); onPrev() }
      else if (ev.key === "ArrowDown" && onNext) { ev.preventDefault(); onNext() }
    }
    window.addEventListener("keydown", onKey)
    return () => window.removeEventListener("keydown", onKey)
  }, [onClose, onPrev, onNext])

  return (
    <div className="fixed inset-0 z-50 flex justify-end bg-black/40" onClick={onClose}>
      <div
        className="h-full w-full max-w-2xl overflow-auto border-l border-[color:var(--border)] bg-[color:var(--bg-1)] p-5 flex flex-col gap-4"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="flex items-center gap-2">
          <StatusPill code={entry.status_code} />
          <span className="mono text-[12px] text-[color:var(--text-3)] truncate">{entry.correlation_id || entry.event_id}</span>
          <span className="ml-auto mono text-[11px] text-[color:var(--text-4)]">{position}</span>
          <Button variant="ghost" size="icon-xs" onClick={onPrev} disabled={!onPrev} aria-label="Previous"><ChevronUp /></Button>
          <Button variant="ghost" size="icon-xs" onClick={onNext} disabled={!onNext} aria-label="Next"><ChevronDown /></Button>
          <Button variant="ghost" size="icon-xs" onClick={onClose} aria-label="Close"><X /></Button>
        </div>

        <MetaGrid entry={entry} />

        {entry.upstream_error && (
          <div className="rounded-[var(--radius)] border p-3 text-[12px] mono" style={{ color: "var(--err)", background: "var(--err-bg)" }}>{entry.upstream_error}</div>
        )}

        {(entry.tags?.length ?? 0) > 0 && (
          <div className="flex flex-wrap gap-1.5">
            {entry.tags!.map((t) => (
              <span key={t} className="inline-flex items-center px-1.5 py-0.5 rounded-[4px] text-[10.5px] mono uppercase tracking-[0.04em] border border-[color:var(--border)] text-[color:var(--text-2)] bg-[color:var(--bg-2)]">{t}</span>
            ))}
          </div>
        )}

        {(entry.rules_matched?.length ?? 0) > 0 && (
          <Section title="Rules fired">
            <div className="flex flex-col gap-1">
              {entry.rules_matched!.map((r, i) => (
                <div key={i} className="flex items-center gap-2 text-[12px]">
                  <span className="mono">{r.rule_name}</span>
                  {r.actions_applied?.map((a) => <span key={a} className="text-[10.5px] mono text-[color:var(--text-4)]">{a}</span>)}
                  {r.terminated && <span className="text-[10.5px] mono text-[color:var(--warn)]">terminated</span>}
                </div>
              ))}
            </div>
          </Section>
        )}

        {(entry.attempts?.length ?? 0) > 0 && (
          <Section title="Resilience attempts">
            <div className="flex flex-col gap-1">
              {entry.attempts!.map((a, i) => (
                <div key={i} className="grid grid-cols-[auto_1fr_auto_auto] items-center gap-2 text-[12px]">
                  <span className="mono text-[color:var(--text-4)]">#{i + 1}</span>
                  <span className="mono truncate">{a.target}</span>
                  <span className="mono tnum text-[color:var(--text-3)]">{a.status_code || a.error || "—"}</span>
                  <span className="mono text-[10.5px]" style={{ color: a.outcome === "success" ? "var(--ok)" : "var(--err)" }}>{a.outcome}</span>
                </div>
              ))}
            </div>
          </Section>
        )}

        <BodyTabs body={body} streaming={entry.streaming} />
      </div>
    </div>
  )
}

function MetaGrid({ entry }: { entry: MessageEntry }) {
  const items: [string, React.ReactNode][] = [
    ["Provider", entry.provider || "—"],
    ["Endpoint", entry.endpoint || "—"],
    ["Model", entry.model || "—"],
    ["Method", entry.method || "—"],
    ["Configuration", entry.configuration || "—"],
    ["Duration", fmt.ms(entry.duration_ms)],
    ["Streaming", entry.streaming ? "yes" : "no"],
    ["Session", entry.session_id || "—"],
    ["At", new Date(entry.at).toLocaleString()],
  ]
  return (
    <div className="grid grid-cols-2 sm:grid-cols-3 gap-x-4 gap-y-2 rounded-[var(--radius)] border border-[color:var(--border)] bg-[color:var(--bg-2)] p-3">
      {items.map(([k, v]) => (
        <div key={k} className="min-w-0">
          <div className="text-[10.5px] uppercase tracking-[0.06em] text-[color:var(--text-4)]">{k}</div>
          <div className="mono text-[12px] truncate" title={typeof v === "string" ? v : undefined}>{v}</div>
        </div>
      ))}
    </div>
  )
}

function Section({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <div className="flex flex-col gap-1.5">
      <div className="text-[11px] uppercase tracking-[0.06em] text-[color:var(--text-3)]">{title}</div>
      {children}
    </div>
  )
}

function BodyTabs({ body, streaming }: { body: MessageBodyDetail | null | undefined; streaming?: boolean }) {
  if (body === undefined) return <div className="text-[12px] text-[color:var(--text-4)]">Loading bodies…</div>
  if (body === null) return <div className="text-[12px] text-[color:var(--text-4)]">No captured bodies (rolled off or capture disabled).</div>

  const hasGenAI = !!body.gen_ai_content
  return (
    <Tabs defaultValue={hasGenAI ? "genai" : "request"} className="flex flex-col gap-2">
      <TabsList>
        {hasGenAI && <TabsTrigger value="genai">GenAI</TabsTrigger>}
        <TabsTrigger value="request">Request</TabsTrigger>
        <TabsTrigger value="response">Response</TabsTrigger>
        {streaming && <TabsTrigger value="sse">SSE</TabsTrigger>}
        <TabsTrigger value="headers">Headers</TabsTrigger>
      </TabsList>
      {hasGenAI && (
        <TabsContent value="genai">
          <BodyView label="GenAI content" text={body.gen_ai_content} />
        </TabsContent>
      )}
      <TabsContent value="request">
        <BodyView label="Request body" text={body.request} bytes={body.request_total_bytes} truncated={body.request_truncated} />
      </TabsContent>
      <TabsContent value="response">
        <BodyView label="Response body" text={body.response} bytes={body.response_total_bytes} truncated={body.response_truncated} />
      </TabsContent>
      {streaming && (
        <TabsContent value="sse">
          <BodyView label="Assembled stream" text={body.response_assembled} partial={body.assembly_partial} />
        </TabsContent>
      )}
      <TabsContent value="headers">
        <Headers label="Request headers" headers={body.request_headers} />
        <div className="h-2" />
        <Headers label="Response headers" headers={body.response_headers} />
      </TabsContent>
    </Tabs>
  )
}

function BodyView({ label, text, bytes, truncated, partial }: { label: string; text?: string; bytes?: number; truncated?: boolean; partial?: boolean }) {
  if (!text) return <div className="text-[12px] text-[color:var(--text-4)]">No {label.toLowerCase()}.</div>
  return (
    <div className="flex flex-col gap-1">
      <div className="text-[10.5px] text-[color:var(--text-4)]">
        {label}{bytes ? ` · ${fmt.compact(bytes)} bytes` : ""}{truncated ? " · truncated" : ""}{partial ? " · partial" : ""}
      </div>
      <JsonViewer text={text} maxHeightClassName="max-h-[60vh]" />
    </div>
  )
}

function Headers({ label, headers }: { label: string; headers?: Record<string, string[]> }) {
  const keys = headers ? Object.keys(headers).sort() : []
  if (!keys.length) return <div className="text-[12px] text-[color:var(--text-4)]">No {label.toLowerCase()}.</div>
  return (
    <div className="flex flex-col gap-1">
      <div className="text-[10.5px] text-[color:var(--text-4)]">{label}</div>
      <div className="rounded-[var(--radius)] border border-[color:var(--border)] bg-[color:var(--bg-2)] p-2 text-[11.5px] mono flex flex-col gap-0.5">
        {keys.map((k) => (
          <div key={k} className="flex gap-2 break-all">
            <span className="text-[color:var(--text-3)] shrink-0">{k}:</span>
            <span className="text-[color:var(--text-2)]">{headers![k].join(", ")}</span>
          </div>
        ))}
      </div>
    </div>
  )
}

function shortTime(iso: string): string {
  const d = new Date(iso)
  return d.toLocaleTimeString(undefined, { hour12: false }) + "." + String(d.getMilliseconds()).padStart(3, "0")
}
