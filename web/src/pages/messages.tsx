import { useCallback, useEffect, useMemo, useRef, useState } from "react"
import { useNavigate } from "react-router"
import { Layers, Pause, Play, Trash2 } from "lucide-react"
import { Button } from "@/components/ui/button"
import { PanelCard } from "@/components/atoms/card"
import { LoadingPanel, ErrorPanel, PageHeader } from "@/components/atoms/page-states"
import { MessagesTable, MessageInspectorModal } from "@/components/observability/messages-view"
import { fetchMessageBody, fetchRecentMessages, openMessageStream, type MessageEntry } from "@/lib/messages"
import { UnauthorizedError } from "@/lib/api"

type FeedState =
  | { status: "loading" }
  | { status: "disabled" }
  | { status: "error"; message: string }
  | { status: "ok"; capacity: number }

// MAX_ROWS_IN_MEMORY caps the visible table and the paused buffer so a
// long-running pane doesn't OOM the browser. Bodies are fetched on-demand
// into the modal, but row metadata + the SSE event objects still
// accumulate — bound both ends.
const MAX_ROWS_IN_MEMORY = 100

export function MessagesPage() {
  const [feed, setFeed] = useState<FeedState>({ status: "loading" })
  const [entries, setEntries] = useState<MessageEntry[]>([])
  const [paused, setPaused] = useState(false)
  const [dropped, setDropped] = useState(0)
  const [streaming, setStreaming] = useState(false)
  const [selectedId, setSelectedId] = useState<string | null>(null)
  const [grouped, setGrouped] = useState(false)
  const [collapsed, setCollapsed] = useState<Set<string>>(() => new Set())
  const pausedRef = useRef(paused)
  pausedRef.current = paused
  const pendingRef = useRef<MessageEntry[]>([])
  const nav = useNavigate()

  const handleNewEntry = useCallback((e: MessageEntry) => {
    if (pausedRef.current) {
      pendingRef.current.push(e)
      if (pendingRef.current.length > MAX_ROWS_IN_MEMORY) {
        pendingRef.current = pendingRef.current.slice(-MAX_ROWS_IN_MEMORY)
      }
      return
    }
    setEntries((prev) => {
      const next = [...prev, e]
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
  const selected = useMemo(() => entries.find((e) => e.event_id === selectedId) ?? null, [entries, selectedId])
  const toggleBundle = useCallback((key: string) => {
    setCollapsed((prev) => {
      const next = new Set(prev)
      if (next.has(key)) next.delete(key)
      else next.add(key)
      return next
    })
  }, [])
  const onSelect = useCallback((eid: string) => setSelectedId((cur) => (cur === eid ? null : eid)), [])

  if (feed.status === "loading") return <LoadingPanel label="Loading messages…" />
  if (feed.status === "error") return <ErrorPanel message={feed.message} />

  return (
    <div className="flex flex-col gap-3.5">
      <PageHeader
        title="Live messages"
        sub={
          feed.status === "disabled"
            ? "Live tail is off. Set SLUICE_ADMIN_LIVE_FEED_CAPACITY > 0 to enable it."
            : `Live tail of requests as they complete — single process, last ${feed.capacity} held in memory, cleared on restart.`
        }
        action={
          feed.status === "ok" && (
            <>
              <Button
                variant="ghost"
                size="sm"
                onClick={() => setGrouped((g) => !g)}
                aria-label="Group by session"
                aria-pressed={grouped}
              >
                <Layers />
                <span className="hidden sm:inline">{grouped ? "Ungroup" : "Group by session"}</span>
              </Button>
              <Button variant="ghost" size="sm" onClick={() => setPaused((p) => !p)} aria-label={paused ? "Resume" : "Pause"}>
                {paused ? <Play /> : <Pause />}
                <span className="hidden sm:inline">
                  {paused
                    ? pendingRef.current.length > 0
                      ? `Resume (+${pendingRef.current.length})`
                      : "Resume"
                    : "Pause"}
                </span>
              </Button>
              <Button
                variant="ghost"
                size="sm"
                onClick={() => {
                  setEntries([])
                  setDropped(0)
                  pendingRef.current = []
                  setSelectedId(null)
                }}
                aria-label="Clear table"
              >
                <Trash2 /> <span className="hidden sm:inline">Clear</span>
              </Button>
            </>
          )
        }
      />

      {feed.status === "disabled" && (
        <PanelCard className="p-6 text-[13px] text-[color:var(--text-3)]">
          The gateway is running with the live messages ring turned off. The pane stays here so operators can confirm
          the feature gate; nothing else to show until it&apos;s re-enabled.
        </PanelCard>
      )}

      {feed.status === "ok" && (
        <>
          <FeedStatusLine streaming={streaming} paused={paused} dropped={dropped} shown={entries.length} capacity={feed.capacity} />
          <MessagesTable
            ordered={ordered}
            grouped={grouped}
            collapsed={collapsed}
            onToggleBundle={toggleBundle}
            selectedId={selectedId}
            onSelect={onSelect}
            emptyLabel="Waiting for traffic…"
          />
        </>
      )}
      {selected && (
        <MessageInspectorModal
          entries={ordered}
          selectedId={selected.event_id}
          onSelect={setSelectedId}
          onClose={() => setSelectedId(null)}
          fetchBody={fetchMessageBody}
        />
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
        style={{ background: streaming ? (paused ? "var(--warn)" : "var(--ok)") : "var(--err)" }}
      />
      <span>{streaming ? (paused ? "paused" : "streaming") : "disconnected"}</span>
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
