import type { DashboardSeries } from "@/lib/dashboard"
import { fmt } from "@/lib/fmt"

type Series = DashboardSeries

const SERIES_COLORS = [
  "var(--accent)",
  "var(--p-anthropic)",
  "var(--p-openai)",
  "var(--p-gemini)",
  "var(--p-qwen-vllm)",
  "var(--p-qwen-ollama)",
]

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

  return (
    <svg
      viewBox={`0 0 ${W} ${H}`}
      width="100%"
      height={height}
      preserveAspectRatio="none"
      style={{ overflow: "visible", display: "block" }}
    >
      {yTicks.map((t, i) => {
        const y = padT + innerH - ((t - lo) / range) * innerH
        return (
          <g key={i}>
            <line
              x1={padL}
              x2={W - padR}
              y1={y}
              y2={y}
              stroke="var(--border)"
              strokeWidth={1}
              strokeDasharray={i === 0 ? "" : "3 3"}
            />
            <text
              x={padL - 6}
              y={y + 3.5}
              fill="var(--text-3)"
              fontSize={10}
              fontFamily="var(--font-mono)"
              textAnchor="end"
            >
              {formatY ? formatY(t) : t.toFixed(1)}
            </text>
          </g>
        )
      })}
      {xIdx.map((i, k) => {
        const x = padL + i * stepX
        const ts = arr[0].points[i].timestamp
        return (
          <text
            key={k}
            x={x}
            y={H - 6}
            fill="var(--text-3)"
            fontSize={10}
            fontFamily="var(--font-mono)"
            textAnchor={k === 0 ? "start" : k === 2 ? "end" : "middle"}
          >
            {formatX ? formatX(ts) : fmt.shortTime(ts)}
          </text>
        )
      })}
      {arr.map((s, si) => {
        const seriesColor =
          (s as Series & { color?: string }).color || color || SERIES_COLORS[si % SERIES_COLORS.length]
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
      {arr.length > 1 && (
        <g>
          {arr.map((s, si) => (
            <g key={s.name} transform={`translate(${padL + si * 110}, ${padT - 4})`}>
              <rect
                width={8}
                height={8}
                fill={
                  (s as Series & { color?: string }).color || SERIES_COLORS[si % SERIES_COLORS.length]
                }
                rx={1}
                y={-7}
              />
              <text x={14} y={0} fill="var(--text-2)" fontSize={10.5} fontFamily="var(--font-mono)">
                {s.name}
              </text>
            </g>
          ))}
        </g>
      )}
    </svg>
  )
}
