// Formatters ported from the design prototype.

// tzAbbrev returns the short local time-zone name for the given instant
// (e.g. "BST", "GMT", "UTC", "EST"). Resolved per-instant so a record from
// a different DST period is labelled with its own offset, not the viewer's
// current one. Falls back to "" if Intl can't produce a zone name.
function tzAbbrev(d: Date): string {
  try {
    const parts = new Intl.DateTimeFormat("en-GB", {
      hour: "2-digit",
      hour12: false,
      timeZoneName: "short",
    }).formatToParts(d)
    return parts.find((p) => p.type === "timeZoneName")?.value ?? ""
  } catch {
    return ""
  }
}

export const fmt = {
  num: (n?: number | null) => (n == null ? "—" : n.toLocaleString("en-US")),

  compact: (n?: number | null) => {
    if (n == null) return "—"
    if (n >= 1e9) return (n / 1e9).toFixed(n >= 1e10 ? 0 : 1).replace(/\.0$/, "") + "B"
    if (n >= 1e6) return (n / 1e6).toFixed(n >= 1e7 ? 0 : 1).replace(/\.0$/, "") + "M"
    if (n >= 1e3) return (n / 1e3).toFixed(n >= 1e4 ? 0 : 1).replace(/\.0$/, "") + "k"
    return String(n)
  },

  ms: (n?: number | null) => {
    if (n == null) return "—"
    if (n < 1000) return n + "ms"
    return (n / 1000).toFixed(n < 10000 ? 2 : 1) + "s"
  },

  pct: (n?: number | null, digits = 1) =>
    n == null ? "—" : (n * 100).toFixed(digits) + "%",

  pctRaw: (n?: number | null, digits = 1) =>
    n == null ? "—" : n.toFixed(digits) + "%",

  ago: (iso?: string | null) => {
    if (!iso) return "—"
    const d = new Date(iso).getTime()
    const now = Date.now()
    const secs = Math.max(0, Math.round((now - d) / 1000))
    if (secs < 5) return "just now"
    if (secs < 60) return secs + "s ago"
    if (secs < 3600) return Math.round(secs / 60) + "m ago"
    if (secs < 86400) return Math.round(secs / 3600) + "h ago"
    return Math.round(secs / 86400) + "d ago"
  },

  // Wire timestamps arrive as UTC ISO strings (…Z); render them in the
  // viewer's local zone — operators read incident timelines against a wall
  // clock, and a silent UTC offset caused a real "where did the traffic go?"
  // mix-up. shortTime stays bare (HH:MM:SS) for dense chart axes where a zone
  // label would be noise; fullTime carries an explicit zone label so the
  // offset is never ambiguous.
  shortTime: (iso?: string | null) =>
    !iso ? "—" : new Date(iso).toLocaleTimeString("en-GB", { hour12: false }),

  fullTime: (iso?: string | null) => {
    if (!iso) return "—"
    const d = new Date(iso)
    const date = d.toLocaleDateString("en-CA") // YYYY-MM-DD in the local zone
    const time = d.toLocaleTimeString("en-GB", { hour12: false })
    const zone = tzAbbrev(d)
    return zone ? `${date} ${time} ${zone}` : `${date} ${time}`
  },

  /**
   * Compact human-readable uptime. Returns "12s", "3m 14s", "2h 7m",
   * "1d 4h" — the two largest non-zero units. Negative inputs clamp
   * to zero. Designed for ticking once per second; expensive math
   * stays out of the hot path.
   */
  uptime: (ms: number) => {
    if (!Number.isFinite(ms) || ms < 0) ms = 0
    const totalSecs = Math.floor(ms / 1000)
    const days = Math.floor(totalSecs / 86400)
    const hours = Math.floor((totalSecs % 86400) / 3600)
    const minutes = Math.floor((totalSecs % 3600) / 60)
    const seconds = totalSecs % 60
    if (days > 0) return `${days}d ${hours}h`
    if (hours > 0) return `${hours}h ${minutes}m`
    if (minutes > 0) return `${minutes}m ${seconds}s`
    return `${seconds}s`
  },
}
