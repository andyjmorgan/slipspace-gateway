import { useCallback, useEffect, useMemo, useRef, useState } from "react"
import { useNavigate } from "react-router"
import { Pause, Play, Trash2 } from "lucide-react"
import { Button } from "@/components/ui/button"
import { PanelCard } from "@/components/atoms/card"
import { StatusPill } from "@/components/atoms/status-pill"
import { ProviderChip } from "@/components/atoms/provider-chip"
import { LoadingPanel, ErrorPanel, PageHeader } from "@/components/atoms/page-states"
import {
  fetchRecentMessages,
  openMessageStream,
  type MessageEntry,
  type RuleHit,
} from "@/lib/messages"
import { UnauthorizedError } from "@/lib/api"
import { cn } from "@/lib/utils"

type FeedState =
  | { status: "loading" }
  | { status: "disabled" }
  | { status: "error"; message: string }
  | { status: "ok"; capacity: number }

const MAX_ROWS_IN_MEMORY = 500

export function MessagesPage() {
  const [feed, setFeed] = useState<FeedState>({ status: "loading" })
  const [entries, setEntries] = useState<MessageEntry[]>([])
  const [paused, setPaused] = useState(false)
  const [dropped, setDropped] = useState(0)
  const [streaming, setStreaming] = useState(false)
  const [selectedId, setSelectedId] = useState<string | null>(null)
  const pausedRef = useRef(paused)
  pausedRef.current = paused
  const pendingRef = useRef<MessageEntry[]>([])
  const nav = useNavigate()

  const handleNewEntry = useCallback((e: MessageEntry) => {
    if (pausedRef.current) {
      pendingRef.current.push(e)
      return
    }
    setEntries((prev) => {
      const next = [...prev, e]
      // Trim from the head if we overshoot the in-memory cap.
      if (next.length > MAX_ROWS_IN_MEMORY) {
        return next.slice(next.length - MAX_ROWS_IN_MEMORY)
      }
      return next
    })
  }, [])

  // Initial fetch + stream subscription.
  useEffect(() => {
    let cancelled = false
    fetchRecentMessages()
      .then((resp) => {
        if (cancelled) return
        setEntries(resp.entries)
        setFeed({ status: "ok", capacity: resp.capacity })
      })
      .catch((err) => {
        if (cancelled) return
        if (err instanceof UnauthorizedError) {
          nav("/login", { replace: true })
          return
        }
        // 503 means the live feed is disabled via SLUICE_ADMIN_LIVE_FEED_CAPACITY=0.
        const msg = String((err as Error).message ?? err)
        if (msg.includes("503")) {
          setFeed({ status: "disabled" })
          return
        }
        setFeed({ status: "error", message: msg })
      })
    return () => {
      cancelled = true
    }
  }, [nav])

  useEffect(() => {
    if (feed.status !== "ok") return
    const close = openMessageStream({
      onMessage: handleNewEntry,
      onDrop: (n) => setDropped((d) => d + n),
      onOpen: () => setStreaming(true),
      onError: () => setStreaming(false),
    })
    return () => {
      close()
      setStreaming(false)
    }
  }, [feed.status, handleNewEntry])

  // Drain buffered entries when the operator unpauses.
  useEffect(() => {
    if (paused) return
    if (pendingRef.current.length === 0) return
    const flush = pendingRef.current
    pendingRef.current = []
    setEntries((prev) => {
      const next = [...prev, ...flush]
      if (next.length > MAX_ROWS_IN_MEMORY) {
        return next.slice(next.length - MAX_ROWS_IN_MEMORY)
      }
      return next
    })
  }, [paused])

  const ordered = useMemo(() => entries.slice().reverse(), [entries])
  const selected = useMemo(
    () => entries.find((e) => e.event_id === selectedId) ?? null,
    [entries, selectedId],
  )

  if (feed.status === "loading") return <LoadingPanel label="Loading messages…" />
  if (feed.status === "error") return <ErrorPanel message={feed.message} />

  return (
    <div className="flex flex-col gap-3.5">
      <PageHeader
        title="Live messages"
        sub={
          feed.status === "disabled"
            ? "Live tail is disabled. Set SLUICE_ADMIN_LIVE_FEED_CAPACITY > 0 to enable it."
            : `Live tail — single process, last ${feed.capacity}, restart clears it.`
        }
        action={
          feed.status === "ok" && (
            <>
              <Button
                variant="outline"
                size="sm"
                onClick={() => setPaused((p) => !p)}
                aria-label={paused ? "Resume" : "Pause"}
              >
                {paused ? <Play /> : <Pause />}
                {paused
                  ? pendingRef.current.length > 0
                    ? `Resume (+${pendingRef.current.length})`
                    : "Resume"
                  : "Pause"}
              </Button>
              <Button
                variant="outline"
                size="sm"
                onClick={() => {
                  setEntries([])
                  setDropped(0)
                  pendingRef.current = []
                  setSelectedId(null)
                }}
                aria-label="Clear table"
              >
                <Trash2 /> Clear
              </Button>
            </>
          )
        }
      />

      {feed.status === "disabled" && (
        <PanelCard className="p-6 text-[13px] text-[color:var(--text-3)]">
          The gateway is running with the live messages ring turned off. The pane
          stays here so operators can confirm the feature gate; nothing else to
          show until it&apos;s re-enabled.
        </PanelCard>
      )}

      {feed.status === "ok" && (
        <>
          <FeedStatusLine
            streaming={streaming}
            paused={paused}
            dropped={dropped}
            shown={entries.length}
            capacity={feed.capacity}
          />
          <PanelCard className="overflow-hidden">
            <table className="w-full text-[12.5px]">
              <thead className="bg-[color:var(--bg-2)] text-[11px] uppercase tracking-[0.05em] text-[color:var(--text-4)]">
                <tr>
                  <th className="px-3 py-2 text-left font-medium">Time</th>
                  <th className="px-3 py-2 text-left font-medium">Status</th>
                  <th className="px-3 py-2 text-left font-medium">Provider · Endpoint</th>
                  <th className="px-3 py-2 text-left font-medium">Model</th>
                  <th className="px-3 py-2 text-left font-medium">Configuration</th>
                  <th className="px-3 py-2 text-right font-medium">Duration</th>
                  <th className="px-3 py-2 text-left font-medium">Rules</th>
                </tr>
              </thead>
              <tbody>
                {ordered.length === 0 && (
                  <tr>
                    <td colSpan={7} className="px-3 py-8 text-center text-[color:var(--text-3)]">
                      Waiting for traffic…
                    </td>
                  </tr>
                )}
                {ordered.map((e) => (
                  <Row
                    key={e.event_id}
                    entry={e}
                    selected={e.event_id === selectedId}
                    onClick={() =>
                      setSelectedId((cur) => (cur === e.event_id ? null : e.event_id))
                    }
                  />
                ))}
              </tbody>
            </table>
          </PanelCard>
          {selected && <Detail entry={selected} onClose={() => setSelectedId(null)} />}
        </>
      )}
    </div>
  )
}

function FeedStatusLine({
  streaming,
  paused,
  dropped,
  shown,
  capacity,
}: {
  streaming: boolean
  paused: boolean
  dropped: number
  shown: number
  capacity: number
}) {
  return (
    <div className="flex items-center gap-3 text-[11.5px] text-[color:var(--text-3)]">
      <span
        className="inline-block size-1.5 rounded-full"
        style={{
          background: streaming
            ? paused
              ? "var(--warn)"
              : "var(--ok)"
            : "var(--err)",
        }}
      />
      <span>
        {streaming ? (paused ? "paused" : "streaming") : "disconnected"}
      </span>
      <span className="text-[color:var(--text-4)]">·</span>
      <span className="mono">
        {shown} / {capacity}
      </span>
      {dropped > 0 && (
        <>
          <span className="text-[color:var(--text-4)]">·</span>
          <span className="text-[color:var(--warn)] mono">{dropped} dropped</span>
        </>
      )}
    </div>
  )
}

function Row({
  entry,
  selected,
  onClick,
}: {
  entry: MessageEntry
  selected: boolean
  onClick: () => void
}) {
  return (
    <tr
      onClick={onClick}
      className={cn(
        "cursor-pointer border-t border-[color:var(--border)] hover:bg-[color:var(--hover)]",
        selected && "bg-[color:var(--hover)]",
      )}
    >
      <td className="mono px-3 py-1.5 whitespace-nowrap text-[color:var(--text-3)]">
        {formatClock(entry.at)}
      </td>
      <td className="px-3 py-1.5">
        <StatusPill code={entry.status_code} />
      </td>
      <td className="px-3 py-1.5">
        <div className="flex items-center gap-1.5">
          {entry.provider && <ProviderChip name={entry.provider} />}
          <span className="mono text-[color:var(--text-3)]">{entry.endpoint ?? "—"}</span>
          {entry.streaming && (
            <span className="mono text-[10px] uppercase text-[color:var(--text-4)]">
              sse
            </span>
          )}
        </div>
      </td>
      <td className="mono px-3 py-1.5 text-[color:var(--text-2)]">
        {entry.model ?? "—"}
      </td>
      <td className="mono px-3 py-1.5 text-[color:var(--text-2)]">
        {entry.configuration ?? "—"}
      </td>
      <td className="mono px-3 py-1.5 text-right tnum text-[color:var(--text-2)]">
        {entry.duration_ms} ms
      </td>
      <td className="px-3 py-1.5 text-[color:var(--text-3)]">
        {entry.rules_matched && entry.rules_matched.length > 0
          ? `${entry.rules_matched.length}`
          : "—"}
      </td>
    </tr>
  )
}

function Detail({ entry, onClose }: { entry: MessageEntry; onClose: () => void }) {
  return (
    <PanelCard className="p-4">
      <div className="mb-3 flex items-start gap-3">
        <div className="flex-1 min-w-0">
          <div className="text-[13px] font-medium">Request detail</div>
          <div className="mono text-[11.5px] text-[color:var(--text-3)]">
            {entry.correlation_id ?? entry.event_id}
          </div>
        </div>
        <Button variant="outline" size="sm" onClick={onClose}>
          Close
        </Button>
      </div>
      <dl className="grid grid-cols-2 gap-x-4 gap-y-1 text-[12.5px] md:grid-cols-3">
        <Field label="Status" value={String(entry.status_code)} />
        <Field label="Duration" value={`${entry.duration_ms} ms`} />
        <Field label="Streaming" value={entry.streaming ? "yes" : "no"} />
        <Field label="Provider" value={entry.provider ?? "—"} />
        <Field label="Endpoint" value={entry.endpoint ?? "—"} />
        <Field label="Model" value={entry.model ?? "—"} />
        <Field label="Configuration" value={entry.configuration ?? "—"} />
        <Field label="At" value={entry.at} />
      </dl>
      {entry.upstream_error && (
        <div className="mt-3 rounded-[var(--radius)] border border-[color:var(--err)] bg-[color:var(--err-bg)] p-2 text-[12.5px]">
          <div className="mono text-[11px] uppercase text-[color:var(--err)]">upstream error</div>
          <div className="mono mt-1 text-[color:var(--text-2)]">{entry.upstream_error}</div>
        </div>
      )}
      {entry.rules_matched && entry.rules_matched.length > 0 && (
        <RulesList rules={entry.rules_matched} />
      )}
    </PanelCard>
  )
}

function Field({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex flex-col">
      <dt className="text-[10.5px] uppercase tracking-[0.06em] text-[color:var(--text-4)]">
        {label}
      </dt>
      <dd className="mono text-[color:var(--text-2)]">{value}</dd>
    </div>
  )
}

function RulesList({ rules }: { rules: RuleHit[] }) {
  return (
    <div className="mt-3">
      <div className="text-[10.5px] uppercase tracking-[0.06em] text-[color:var(--text-4)] mb-1">
        Rules matched
      </div>
      <ul className="flex flex-col gap-1">
        {rules.map((r, i) => (
          <li
            key={`${r.rule_name}-${i}`}
            className="flex flex-wrap items-center gap-2 rounded-[var(--radius)] border border-[color:var(--border)] px-2 py-1.5 text-[12.5px]"
          >
            <span className="mono">{r.rule_name}</span>
            {r.actions_applied && r.actions_applied.length > 0 && (
              <span className="mono text-[11px] text-[color:var(--text-3)]">
                → {r.actions_applied.join(", ")}
              </span>
            )}
            {r.terminated && (
              <span
                className="mono text-[10px] uppercase"
                style={{ color: "var(--violet)" }}
              >
                terminated
              </span>
            )}
            {r.error_message && (
              <span className="mono text-[11px]" style={{ color: "var(--err)" }}>
                {r.error_message}
              </span>
            )}
          </li>
        ))}
      </ul>
    </div>
  )
}

function formatClock(iso: string): string {
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return iso
  return d.toLocaleTimeString([], { hour12: false }) + "." + String(d.getMilliseconds()).padStart(3, "0")
}
