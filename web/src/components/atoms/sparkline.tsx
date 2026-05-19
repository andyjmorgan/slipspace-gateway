export function Sparkline({
  points,
  color,
  width = 200,
  height = 32,
  fill = true,
}: {
  points: number[] | { value: number }[]
  color?: string
  width?: number
  height?: number
  fill?: boolean
}) {
  if (!points || !points.length) return null
  const values = points.map((p) => (typeof p === "object" ? p.value : p))
  const lo = Math.min(...values)
  const hi = Math.max(...values)
  const range = hi - lo || 1
  const stepX = width / (values.length - 1 || 1)
  const ys = values.map((v) => height - 2 - ((v - lo) / range) * (height - 4))
  const linePath = values
    .map((_, i) => `${i === 0 ? "M" : "L"}${(i * stepX).toFixed(2)} ${ys[i].toFixed(2)}`)
    .join(" ")
  const areaPath = `${linePath} L ${width} ${height} L 0 ${height} Z`
  const stroke = color || "var(--text-3)"
  return (
    <svg
      className="block w-full"
      viewBox={`0 0 ${width} ${height}`}
      preserveAspectRatio="none"
      style={{ height }}
    >
      {fill && (
        <path
          d={areaPath}
          style={{
            fill: `color-mix(in oklch, ${stroke} 18%, transparent)`,
          }}
        />
      )}
      <path d={linePath} fill="none" stroke={stroke} strokeWidth={1.2} />
    </svg>
  )
}
