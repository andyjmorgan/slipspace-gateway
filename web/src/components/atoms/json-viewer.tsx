import { memo, useCallback, useMemo, useState } from "react"
import { Check, ChevronDown, ChevronRight, Copy } from "lucide-react"
import { cn } from "@/lib/utils"

// JsonViewer renders a string of JSON as a collapsible tree. The input is
// parsed once at the top; subtrees render recursively and can be folded
// or copied to the clipboard. When the input does not parse as JSON, the
// component falls back to a plain monospace block so callers can pass
// any captured body without first sniffing the content-type.
//
// Theming is driven entirely by CSS custom properties so the viewer
// inherits the admin console's dark/light surface tokens — no per-mode
// palette is duplicated here.
export const JsonViewer = memo(function JsonViewer({
  text,
  className,
  maxHeightClassName = "max-h-72",
  initialDepth = Number.MAX_SAFE_INTEGER,
}: {
  text: string
  className?: string
  // Tailwind class controlling the scroll container height. Defaults
  // match the existing BodySection panel. When the consumer gives the
  // viewer a definite height via `className` (e.g. `flex-1 min-h-0`),
  // pass `maxHeightClassName=""` so the parent controls the size.
  maxHeightClassName?: string
  // Object/array depths below this collapse on first render. The root
  // is depth 0; default expands everything. The container handles
  // overflow via `overflow-auto`, so a fully expanded tree just gets
  // a scrollbar instead of pushing the modal taller.
  initialDepth?: number
}) {
  const parsed = useMemo(() => tryParse(text), [text])
  if (!parsed.ok) {
    return (
      <pre
        className={cn(
          "mono overflow-auto rounded-[var(--radius)] border border-[color:var(--border)] bg-[color:var(--bg-2)] p-2 text-[11.5px] text-[color:var(--text-2)] whitespace-pre-wrap break-all",
          maxHeightClassName,
          className,
        )}
      >
        {text}
      </pre>
    )
  }
  return (
    <div
      className={cn(
        "mono overflow-auto rounded-[var(--radius)] border border-[color:var(--border)] bg-[color:var(--bg-2)] p-2 text-[11.5px] text-[color:var(--text-2)]",
        maxHeightClassName,
        className,
      )}
    >
      <Node value={parsed.value} depth={0} initialDepth={initialDepth} />
    </div>
  )
})

type Parsed =
  | { ok: true; value: unknown }
  | { ok: false }

function tryParse(text: string): Parsed {
  if (!text) return { ok: false }
  const trimmed = text.trim()
  if (!trimmed.startsWith("{") && !trimmed.startsWith("[")) return { ok: false }
  try {
    return { ok: true, value: JSON.parse(trimmed) }
  } catch {
    return { ok: false }
  }
}

function Node({
  value,
  depth,
  initialDepth,
  keyName,
  isLast = true,
}: {
  value: unknown
  depth: number
  initialDepth: number
  keyName?: string
  isLast?: boolean
}) {
  if (value === null) return <Leaf keyName={keyName} text="null" tone="null" isLast={isLast} />
  if (typeof value === "boolean")
    return <Leaf keyName={keyName} text={String(value)} tone="bool" isLast={isLast} />
  if (typeof value === "number")
    return <Leaf keyName={keyName} text={String(value)} tone="num" isLast={isLast} />
  if (typeof value === "string")
    return <Leaf keyName={keyName} text={JSON.stringify(value)} tone="str" isLast={isLast} />
  if (Array.isArray(value))
    return (
      <Collection
        keyName={keyName}
        kind="array"
        entries={value.map((v, i) => [String(i), v] as const)}
        depth={depth}
        initialDepth={initialDepth}
        isLast={isLast}
      />
    )
  if (typeof value === "object")
    return (
      <Collection
        keyName={keyName}
        kind="object"
        entries={Object.entries(value as Record<string, unknown>)}
        depth={depth}
        initialDepth={initialDepth}
        isLast={isLast}
      />
    )
  // Unreachable for JSON.parse output, but keep the type checker happy.
  return <Leaf keyName={keyName} text={String(value)} tone="str" isLast={isLast} />
}

type Tone = "str" | "num" | "bool" | "null"

const TONE_COLOR: Record<Tone, string> = {
  str: "var(--ok)",
  num: "var(--accent)",
  bool: "var(--warn)",
  null: "var(--text-4)",
}

function Leaf({
  keyName,
  text,
  tone,
  isLast,
}: {
  keyName?: string
  text: string
  tone: Tone
  isLast: boolean
}) {
  return (
    <div className="flex items-baseline whitespace-pre-wrap break-all leading-[1.55]">
      {keyName !== undefined && <KeyLabel name={keyName} />}
      <span style={{ color: TONE_COLOR[tone] }}>{text}</span>
      {!isLast && <span className="text-[color:var(--text-4)]">,</span>}
    </div>
  )
}

function KeyLabel({ name }: { name: string }) {
  return (
    <span className="mr-1 text-[color:var(--text-3)]">
      <span className="text-[color:var(--text-4)]">&quot;</span>
      {name}
      <span className="text-[color:var(--text-4)]">&quot;</span>
      <span className="text-[color:var(--text-4)]">:</span>
    </span>
  )
}

function Collection({
  keyName,
  kind,
  entries,
  depth,
  initialDepth,
  isLast,
}: {
  keyName?: string
  kind: "object" | "array"
  entries: ReadonlyArray<readonly [string, unknown]>
  depth: number
  initialDepth: number
  isLast: boolean
}) {
  const open = kind === "object" ? "{" : "["
  const close = kind === "object" ? "}" : "]"
  const [expanded, setExpanded] = useState(depth < initialDepth)
  const subtree = useMemo(() => collectionToValue(kind, entries), [kind, entries])
  const empty = entries.length === 0

  if (empty) {
    return (
      <div className="flex items-baseline leading-[1.55]">
        {keyName !== undefined && <KeyLabel name={keyName} />}
        <span className="text-[color:var(--text-4)]">{open}</span>
        <span className="text-[color:var(--text-4)]">{close}</span>
        {!isLast && <span className="text-[color:var(--text-4)]">,</span>}
      </div>
    )
  }

  return (
    <div className="leading-[1.55]">
      <div className="group flex items-baseline">
        <button
          type="button"
          aria-label={expanded ? "Collapse" : "Expand"}
          onClick={() => setExpanded((v) => !v)}
          className="mr-1 inline-flex h-3.5 w-3.5 shrink-0 self-center items-center justify-center text-[color:var(--text-4)] hover:text-[color:var(--text-2)]"
        >
          {expanded ? <ChevronDown size={12} /> : <ChevronRight size={12} />}
        </button>
        {keyName !== undefined && <KeyLabel name={keyName} />}
        <span className="text-[color:var(--text-4)]">{open}</span>
        {!expanded && (
          <span className="ml-1 text-[10.5px] text-[color:var(--text-4)]">
            {entries.length} {kind === "object" ? (entries.length === 1 ? "key" : "keys") : (entries.length === 1 ? "item" : "items")}
          </span>
        )}
        {!expanded && <span className="text-[color:var(--text-4)]">{close}</span>}
        {!expanded && !isLast && <span className="text-[color:var(--text-4)]">,</span>}
        <CopyButton value={subtree} />
      </div>
      {expanded && (
        <>
          <div className="border-l border-[color:var(--border)] pl-3 ml-1">
            {entries.map(([k, v], i) => (
              <Node
                key={k}
                value={v}
                depth={depth + 1}
                initialDepth={initialDepth}
                keyName={kind === "object" ? k : undefined}
                isLast={i === entries.length - 1}
              />
            ))}
          </div>
          <div className="flex items-baseline">
            <span className="text-[color:var(--text-4)]">{close}</span>
            {!isLast && <span className="text-[color:var(--text-4)]">,</span>}
          </div>
        </>
      )}
    </div>
  )
}

function collectionToValue(
  kind: "object" | "array",
  entries: ReadonlyArray<readonly [string, unknown]>,
): unknown {
  if (kind === "array") return entries.map(([, v]) => v)
  const out: Record<string, unknown> = {}
  for (const [k, v] of entries) out[k] = v
  return out
}

function CopyButton({ value }: { value: unknown }) {
  const [copied, setCopied] = useState(false)
  const onClick = useCallback(async () => {
    try {
      const text = JSON.stringify(value, null, 2)
      await navigator.clipboard.writeText(text)
      setCopied(true)
      window.setTimeout(() => setCopied(false), 1200)
    } catch {
      // Clipboard API can fail in non-secure contexts or when the
      // permission is denied. Silently no-op; the user can still
      // expand the subtree and select text manually.
    }
  }, [value])
  return (
    <button
      type="button"
      onClick={onClick}
      aria-label={copied ? "Copied" : "Copy subtree"}
      title={copied ? "Copied" : "Copy"}
      className="ml-1.5 inline-flex h-4 w-4 shrink-0 self-center items-center justify-center rounded-[3px] text-[color:var(--text-4)] opacity-0 transition group-hover:opacity-100 hover:bg-[color:var(--hover)] hover:text-[color:var(--text-2)]"
    >
      {copied ? <Check size={11} /> : <Copy size={11} />}
    </button>
  )
}
