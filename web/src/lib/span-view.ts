// Non-component helpers behind the shared span inspector
// (telemetry/components/span-inspector-panes.tsx): tab-strip meta strings,
// the id abbreviation, and the on-demand message-body fetch hook. They live
// here rather than next to the components so the component module exports
// only components (react-refresh constraint).

import { useEffect, useState } from "react"
import { fmt } from "@/lib/fmt"
import { fetchMessageBody, type BodySide, type MessageBodyDetail } from "@/lib/messages"
import type { SessionSpan } from "@/lib/session-spans"

// id8 abbreviates a tool_call id to its last 8 chars, matching the ledger.
export function id8(id?: string): string {
  return "…" + String(id ?? "").slice(-8)
}

// outputMeta / inputMeta are the inspector tab-strip summary strings.
export function outputMeta(s: SessionSpan): string {
  const toolCalls = (s.output_parts ?? []).filter((p) => p.type === "tool_call").length
  const nReasoning = (s.output_parts ?? []).filter((p) => p.type === "reasoning").length
  const outChars = s.output_text_chars ?? (s.output_text ?? "").length
  return `${fmt.compact(outChars)} chars${nReasoning ? ` · ${nReasoning} reasoning` : ""}${
    toolCalls ? ` · ${toolCalls} tool call${toolCalls === 1 ? "" : "s"}` : ""
  }`
}

export function inputMeta(s: SessionSpan): string {
  const inParts = s.input_parts ?? []
  const resps = inParts.filter((p) => p.type === "tool_call_response").length
  const texts = inParts.filter((p) => p.type === "text").length
  return inParts.length
    ? `${resps} tool response${resps === 1 ? "" : "s"}${texts ? ` · ${texts} text` : ""}`
    : "empty"
}

// ─────────────────────────────────────────────────────────────────────────
// On-demand message-body fetch (the Telemetry/Report bridge feed)
// ─────────────────────────────────────────────────────────────────────────

// bodyCache memoizes /messages/{cid}/body fetches (LRU, keyed side+cid) so
// flipping sub-tabs or stepping back to a span already opened never
// re-fetches. Each parent tab fetches ONLY its side (?include=): pressing
// Telemetry never pulls the record, pressing Report never reads the span.
// null is a valid cached value: "asked, nothing stored" (404/503), so a
// missing side doesn't re-fetch on every tab click.
const BODY_CACHE_MAX = 16
const bodyCache = new Map<string, MessageBodyDetail | null>()

function bodyKey(side: BodySide, cid: string): string {
  return `${side} ${cid}`
}

function cachePut(key: string, v: MessageBodyDetail | null) {
  bodyCache.delete(key)
  bodyCache.set(key, v)
  if (bodyCache.size > BODY_CACHE_MAX) {
    const oldest = bodyCache.keys().next().value
    if (oldest !== undefined) bodyCache.delete(oldest)
  }
}

export type BodyState =
  | { state: "loading" }
  | { state: "error" }
  | { state: "missing" }
  | { state: "ready"; body: MessageBodyDetail }

// fromCache derives the render-time state for a key without mutating the LRU
// order (renders must stay pure; the order refreshes on cachePut).
function fromCache(key: string): BodyState | null {
  const hit = bodyCache.get(key)
  if (hit === undefined) return null
  return hit === null ? { state: "missing" } : { state: "ready", body: hit }
}

/**
 * useMessageBody lazily fetches ONE SIDE of the body detail for a correlation
 * id. Nothing happens until `wanted` first turns true (the operator clicked
 * that side's parent tab) — the on-demand contract the bridge tabs promise.
 * State is keyed by (side, cid) so stepping prev/next derives fresh state
 * during render instead of flashing the previous span's body.
 */
export function useMessageBody(cid: string, wanted: boolean, side: BodySide): BodyState {
  const key = bodyKey(side, cid)
  const [st, setSt] = useState<{ key: string; v: BodyState } | null>(null)
  useEffect(() => {
    if (!wanted || bodyCache.has(key)) return
    let cancelled = false
    fetchMessageBody(cid, side)
      .then((body) => {
        cachePut(key, body)
        if (cancelled) return
        setSt({ key, v: body === null ? { state: "missing" } : { state: "ready", body } })
      })
      .catch(() => {
        if (!cancelled) setSt({ key, v: { state: "error" } })
      })
    return () => {
      cancelled = true
    }
  }, [cid, side, key, wanted])
  const cached = fromCache(key)
  if (cached) return cached
  if (st && st.key === key) return st.v
  return { state: "loading" }
}
