// Non-component helpers for the form atoms. Kept out of field-atoms.tsx so that
// module only exports components (react-refresh/only-export-components) — same
// split as write-error.ts.

export type KVPair = {
  key: string
  value: string
}

// pairsFromRecord turns a {key: value} record into the ordered pair list the
// KeyValueEditor edits. Undefined yields an empty list.
export function pairsFromRecord(rec: Record<string, string> | undefined): KVPair[] {
  return Object.entries(rec ?? {}).map(([key, value]) => ({ key, value }))
}

// recordFromPairs collapses the edited pair list back into a record, dropping
// rows with a blank (trimmed) key.
export function recordFromPairs(pairs: KVPair[]): Record<string, string> {
  const out: Record<string, string> = {}
  for (const p of pairs) {
    const k = p.key.trim()
    if (k === "") continue
    out[k] = p.value
  }
  return out
}

// stringsFromList trims and drops empty entries from an edited string list
// (model patterns, tags, methods, failure status codes as text).
export function stringsFromList(list: string[]): string[] {
  return list.map((s) => s.trim()).filter((s) => s !== "")
}
