import { cn } from "@/lib/utils"
import { providerColor } from "@/lib/provider-color"

export function ProviderChip({
  name,
  className,
}: {
  name: string
  className?: string
}) {
  const { fg, bg } = providerColor(name)
  return (
    <span
      className={cn(
        // max-w-full + min-w-0 let the chip shrink inside a constrained grid/flex
        // cell; the name truncates rather than bleeding into the adjacent bar.
        "mono inline-flex min-w-0 max-w-full items-center gap-1.5 rounded-[5px] px-1.5 py-0.5 text-[11px] font-medium",
        className,
      )}
      style={{ background: bg, color: fg }}
      title={name}
    >
      <span
        className="inline-block size-1.5 shrink-0 rounded-full"
        style={{ background: "currentColor" }}
      />
      <span className="truncate">{name}</span>
    </span>
  )
}
