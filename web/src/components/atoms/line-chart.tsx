import type { DashboardSeries } from "@/lib/dashboard"
import { fmt } from "@/lib/fmt"
import { providerColor } from "@/lib/provider-color"

type Series = DashboardSeries

// LineChart renders one or more time series. The SVG uses
// preserveAspectRatio="none" so the data paths stretch to fill the
// panel — useful for sparkline-style horizontal scaling regardless of
// panel width. Text inside that SVG would get non-uniformly scaled
// (and squashed unreadable in narrow panels), so the legend and axis
// labels are rendered as HTML positioned over the chart area instead.
export function LineChart({
  series,
  height = 200,
  formatY,
  formatX,
  color,
}: {
  series: Series | (Series & { color?: string })[]
  height?: number
  formatY?: (v: number) => string
  formatX?: (iso: string) => string
  color?: string
}) {
  const arr = Array.isArray(series) ? series : [series]
  // Single-series usage may pass `color`; per-series may carry its own.
  // Multi-series with no explicit color falls back to the deterministic
  // hue derived from the series name, so the same provider always lines
  // up to the same color in the chart and the chip.
  const seriesColorAt = (_si: number, s: Series) =>
    (s as Series & { color?: string }).color || color || providerColor(s.name).fg
  const allValues = arr.flatMap((s) => s.points.map((p) => p.value))
  const lo = Math.min(0, ...allValues)
  const hi = Math.max(...allValues) * 1.1 || 1
  const range = hi - lo || 1
  const padL = 38
  const padR = 12
  const padT = 12
  const padB = 22
  const W = 1000
  const H = height
  const innerW = W - padL - padR
  const innerH = H - padT - padB
  const n = arr[0].points.length
  const stepX = innerW / (n - 1 || 1)

  const ticks = 4
  const yTicks = Array.from({ length: ticks + 1 }, (_, i) => lo + (range * i) / ticks)
  const xIdx = [0, Math.floor(n / 2), n - 1]

  const pctL = (padL / W) * 100
  const pctR = (padR / W) * 100
  const pctT = (padT / H) * 100
  const pctB = (padB / H) * 100

  return (
    <div className="flex flex-col gap-2">
      {arr.length > 1 && (
        <div className="flex items-center gap-3 flex-wrap text-[11.5px] text-[color:var(--text-2)]">
          {arr.map((s, si) => (
            <div key={s.name} className="inline-flex items-center gap-1.5">
              <span
                className="inline-block w-2.5 h-2.5 rounded-[2px]"
                style={{ backgroundColor: seriesColorAt(si, s) }}
              />
              <span className="mono">{s.name}</span>
            </div>
          ))}
        </div>
      )}
      <div className="relative" style={{ height: `${H}px` }}>
        <svg
          viewBox={`0 0 ${W} ${H}`}
          width="100%"
          height={H}
          preserveAspectRatio="none"
          style={{ overflow: "visible", display: "block", position: "absolute", inset: 0 }}
        >
          {yTicks.map((t, i) => {
            const y = padT + innerH - ((t - lo) / range) * innerH
            return (
              <line
                key={i}
                x1={padL}
                x2={W - padR}
                y1={y}
                y2={y}
                stroke="var(--border)"
                strokeWidth={1}
                strokeDasharray={i === 0 ? "" : "3 3"}
                vectorEffect="non-scaling-stroke"
              />
            )
          })}
          {arr.map((s, si) => {
            const seriesColor = seriesColorAt(si, s)
            const path = s.points
              .map((p, i) => {
                const x = padL + i * stepX
                const y = padT + innerH - ((p.value - lo) / range) * innerH
                return `${i === 0 ? "M" : "L"}${x.toFixed(1)} ${y.toFixed(1)}`
              })
              .join(" ")
            return (
              <g key={s.name}>
                {arr.length === 1 && (
                  <path
                    d={`${path} L ${padL + (n - 1) * stepX} ${padT + innerH} L ${padL} ${padT + innerH} Z`}
                    fill={`color-mix(in oklch, ${seriesColor} 15%, transparent)`}
                  />
                )}
                <path
                  d={path}
                  fill="none"
                  stroke={seriesColor}
                  strokeWidth={1.4}
                  vectorEffect="non-scaling-stroke"
                />
              </g>
            )
          })}
        </svg>
        <div className="absolute inset-0 pointer-events-none mono text-[10px] text-[color:var(--text-3)]">
          {yTicks.map((t, i) => {
            const frac = (t - lo) / range
            const topPct = pctT + (1 - frac) * (100 - pctT - pctB)
            return (
              <span
                key={i}
                className="absolute -translate-y-1/2"
                style={{ top: `${topPct}%`, right: `calc(${100 - pctL}% + 6px)` }}
              >
                {formatY ? formatY(t) : t.toFixed(1)}
              </span>
            )
          })}
          {xIdx.map((i, k) => {
            const xFrac = i / (n - 1 || 1)
            const leftPct = pctL + xFrac * (100 - pctL - pctR)
            const ts = arr[0].points[i].timestamp
            const align = k === 0 ? "left" : k === 2 ? "right" : "center"
            const style: React.CSSProperties = { bottom: 2 }
            if (align === "left") style.left = `${leftPct}%`
            else if (align === "right") style.right = `${100 - leftPct}%`
            else {
              style.left = `${leftPct}%`
              style.transform = "translateX(-50%)"
            }
            return (
              <span key={k} className="absolute" style={style}>
                {formatX ? formatX(ts) : fmt.shortTime(ts)}
              </span>
            )
          })}
        </div>
      </div>
    </div>
  )
}
