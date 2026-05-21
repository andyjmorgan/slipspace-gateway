import { cn } from "@/lib/utils"

const PROVIDER_BG: Record<string, string> = {
  openai: "var(--p-openai-bg)",
  anthropic: "var(--p-anthropic-bg)",
  gemini: "var(--p-gemini-bg)",
  "qwen-vllm": "var(--p-qwen-vllm-bg)",
  "qwen-ollama": "var(--p-qwen-ollama-bg)",
  "gpt-oss": "var(--p-gpt-oss-bg)",
  qwen36: "var(--p-qwen36-bg)",
}
const PROVIDER_FG: Record<string, string> = {
  openai: "var(--p-openai)",
  anthropic: "var(--p-anthropic)",
  gemini: "var(--p-gemini)",
  "qwen-vllm": "var(--p-qwen-vllm)",
  "qwen-ollama": "var(--p-qwen-ollama)",
  "gpt-oss": "var(--p-gpt-oss)",
  qwen36: "var(--p-qwen36)",
}

export function ProviderChip({
  name,
  className,
}: {
  name: string
  className?: string
}) {
  const bg = PROVIDER_BG[name] ?? "var(--bg-2)"
  const fg = PROVIDER_FG[name] ?? "var(--text-2)"
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
        style={{ background: fg }}
      />
      {name}
    </span>
  )
}
