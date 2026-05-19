// Formatters ported from the design prototype.

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

  shortTime: (iso?: string | null) =>
    !iso ? "—" : new Date(iso).toISOString().slice(11, 19),

  fullTime: (iso?: string | null) =>
    !iso ? "—" : new Date(iso).toISOString().replace("T", " ").slice(0, 19) + "Z",

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
