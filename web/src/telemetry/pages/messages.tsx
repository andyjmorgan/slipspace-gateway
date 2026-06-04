import { useEffect, useState } from "react"
import type { MessageBodyDetail, MessageEntry, MessagesRecentResponse } from "@/lib/messages"
import { Card } from "@/components/ui/card"
import { Badge } from "@/components/ui/badge"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { apiFetch } from "../lib/api"

export function MessagesPage() {
  const [entries, setEntries] = useState<MessageEntry[]>([])
  const [error, setError] = useState<string | null>(null)
  const [selected, setSelected] = useState<MessageEntry | null>(null)

  useEffect(() => {
    let cancelled = false
    apiFetch<MessagesRecentResponse>("/api/v1/messages/recent?limit=200")
      .then((r) => {
        if (!cancelled) setEntries([...r.entries].reverse()) // newest-first for display
      })
      .catch((e: unknown) => {
        if (!cancelled) setError(e instanceof Error ? e.message : String(e))
      })
    return () => {
      cancelled = true
    }
  }, [])

  return (
    <div className="grid grid-cols-1 gap-4 lg:grid-cols-[1fr_1fr]">
      <div className="space-y-2">
        <h2 className="text-lg font-semibold">Recent messages</h2>
        {error && <div className="text-destructive text-sm">Failed to load: {error}</div>}
        <Card className="overflow-hidden p-0">
          <table className="w-full text-sm">
            <thead className="bg-muted/50 text-muted-foreground text-xs">
              <tr>
                <th className="px-3 py-2 text-left">Model</th>
                <th className="px-3 py-2 text-left">Provider</th>
                <th className="px-3 py-2 text-right">Status</th>
                <th className="px-3 py-2 text-right">ms</th>
              </tr>
            </thead>
            <tbody>
              {entries.map((e) => (
                <tr
                  key={e.event_id}
                  onClick={() => setSelected(e)}
                  className={`hover:bg-muted/50 cursor-pointer border-t ${selected?.event_id === e.event_id ? "bg-muted" : ""}`}
                >
                  <td className="px-3 py-1.5">{e.model || "—"}</td>
                  <td className="px-3 py-1.5">{e.provider || "—"}</td>
                  <td className="px-3 py-1.5 text-right">
                    <Badge variant={e.status_code >= 400 ? "destructive" : "secondary"}>{e.status_code}</Badge>
                  </td>
                  <td className="px-3 py-1.5 text-right tabular-nums">{e.duration_ms}</td>
                </tr>
              ))}
              {entries.length === 0 && !error && (
                <tr>
                  <td colSpan={4} className="text-muted-foreground px-3 py-4 text-center">
                    No messages yet
                  </td>
                </tr>
              )}
            </tbody>
          </table>
        </Card>
      </div>

      <div>{selected ? <Inspector entry={selected} /> : <p className="text-muted-foreground text-sm">Select a message to inspect.</p>}</div>
    </div>
  )
}

function Inspector({ entry }: { entry: MessageEntry }) {
  const [body, setBody] = useState<MessageBodyDetail | null>(null)
  const [missing, setMissing] = useState(false)

  useEffect(() => {
    let cancelled = false
    setBody(null)
    setMissing(false)
    apiFetch<MessageBodyDetail>(`/api/v1/messages/${encodeURIComponent(entry.event_id)}/body`)
      .then((b) => {
        if (!cancelled) setBody(b)
      })
      .catch((e: unknown) => {
        const status = (e as { status?: number }).status
        if (!cancelled && status === 404) setMissing(true)
      })
    return () => {
      cancelled = true
    }
  }, [entry.event_id])

  return (
    <Card className="space-y-3 p-4">
      <div className="text-sm font-medium">{entry.correlation_id || entry.event_id}</div>
      {entry.tags && entry.tags.length > 0 && (
        <div className="flex flex-wrap gap-1">
          {entry.tags.map((t) => (
            <Badge key={t} variant="outline">
              {t}
            </Badge>
          ))}
        </div>
      )}
      {missing && <div className="text-muted-foreground text-sm">No captured body for this request.</div>}
      {body && (
        <Tabs defaultValue="request">
          <TabsList>
            <TabsTrigger value="request">Request</TabsTrigger>
            <TabsTrigger value="response">Response</TabsTrigger>
            <TabsTrigger value="assembled">SSE rollup</TabsTrigger>
          </TabsList>
          <TabsContent value="request">
            <Pre text={body.request} />
          </TabsContent>
          <TabsContent value="response">
            <Pre text={body.response} />
          </TabsContent>
          <TabsContent value="assembled">
            <Pre text={body.response_assembled} />
          </TabsContent>
        </Tabs>
      )}
    </Card>
  )
}

function Pre({ text }: { text?: string }) {
  if (!text) {
    return <div className="text-muted-foreground text-sm">empty</div>
  }
  return <pre className="bg-muted max-h-96 overflow-auto rounded p-3 text-xs">{text}</pre>
}
