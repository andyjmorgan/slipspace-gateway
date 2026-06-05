import type { SelectOption } from "@/components/forms/field-atoms"

// PROTOCOL_OPTIONS is the recognised generative protocol set — the
// `ProtocolX` consts in contracts/config (chat / responses / messages /
// generate_content / embeddings). Shared by the provider editor (a provider's
// protocols map) and the configuration editor (a binding's protocol) so the
// list has one source of truth.
export const PROTOCOL_OPTIONS: SelectOption[] = [
  { value: "chat" },
  { value: "responses" },
  { value: "messages" },
  { value: "generate_content" },
  { value: "embeddings" },
]
