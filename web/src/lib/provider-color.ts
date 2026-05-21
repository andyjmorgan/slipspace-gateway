// HUE_OVERRIDES pins the three OG provider names to their brand hues
// so we don't end up rendering OpenAI as orange or Anthropic as blue.
// Everything else falls through to the hash so any provider name an
// operator adds in providers.yaml tomorrow gets a stable color for
// free — no CSS var, no UI patch, no enumeration.
const HUE_OVERRIDES: Record<string, number> = {
  openai: 158,
  anthropic: 60,
  gemini: 240,
}

// hueFor returns a deterministic hue (0-360) for a provider name.
// FNV-1a 32-bit gives a uniform spread cheaply; the hash is stable
// across renders, machines, and browser sessions so the same name
// always lands on the same color.
export function hueFor(name: string): number {
  if (HUE_OVERRIDES[name] !== undefined) return HUE_OVERRIDES[name]
  let h = 0x811c9dc5
  for (let i = 0; i < name.length; i++) {
    h ^= name.charCodeAt(i)
    h = Math.imul(h, 0x01000193)
  }
  return (h >>> 0) % 360
}

// providerColor returns the foreground and background tints to use for
// a provider chip or chart series. The lightness/chroma constants are
// picked once here so the palette stays coherent — callers should not
// reach for raw oklch() strings.
export function providerColor(name: string): { fg: string; bg: string } {
  const hue = hueFor(name)
  return {
    fg: `oklch(0.78 0.13 ${hue})`,
    bg: `oklch(0.78 0.13 ${hue} / 0.15)`,
  }
}
