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
        "mono inline-flex items-center gap-1.5 rounded-[5px] px-1.5 py-0.5 text-[11px] font-medium",
        className,
      )}
      style={{ background: bg, color: fg }}
    >
      <span
        className="inline-block size-1.5 rounded-full"
        style={{ background: "currentColor" }}
      />
      {name}
    </span>
  )
}
