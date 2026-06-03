// Shared observability data shapes — the wire contracts the gateway admin
// API and the control-plane observability API both return (the CP phase-2
// backend re-serves contracts/admin byte-for-byte). The presentational
// dashboard + message-inspector components under
// components/observability/ render these; each console's own lib provides
// the fetch hooks that produce them.
//
// Mirrors contracts/admin/dashboard.go + contracts/admin/messages.go on
// the Go side.

export type DashboardProviderRow = {
  provider: string
  requests: number
  p95_latency_ms: number
  error_rate: number
}

export type DashboardModelRow = {
  model: string
  provider: string
  requests: number
  tokens_in: number
  tokens_out: number
}

export type DashboardEndpointRow = {
  provider: string
  endpoint: string
  requests: number
  p95_latency_ms: number
  error_rate: number
}

export type DashboardConfigurationRow = {
  configuration: string
  requests: number
  p95_latency_ms: number
  error_rate: number
}

export type DashboardRuleFiredRow = {
  rule_name: string
  fire_count: number
  used_by_configurations: string[]
}

export type DashboardTagFiredRow = {
  tag: string
  apply_count: number
  used_by_configurations: string[]
}

export type DashboardProviderHealth = {
  provider: string
  healthy: boolean
  error_rate_5m: number
  requests_5m: number
}

export type DashboardSummary = {
  window: string
  generated_at: string
  gateway_started_at: string
  totals: {
    requests: number
    requests_success: number
    requests_errored: number
    tokens_in: number
    tokens_out: number
    tokens_cached: number
    tokens_cache_creation: number
  }
  rates: {
    requests_per_second: number
    error_rate: number
  }
  latency_ms: { p50: number; p95: number; p99: number }
  by_provider: DashboardProviderRow[]
  by_endpoint: DashboardEndpointRow[]
  by_configuration: DashboardConfigurationRow[]
  by_model: DashboardModelRow[]
  rules_fired: DashboardRuleFiredRow[]
  tags_fired: DashboardTagFiredRow[]
  provider_health: DashboardProviderHealth[]
}

export type DashboardPoint = {
  timestamp: string
  value: number
}

export type DashboardSeries = {
  name: string
  unit?: string
  labels?: Record<string, string>
  points: DashboardPoint[]
}

export type DashboardTimeseries = {
  series: DashboardSeries[]
}

/** The set of windows the dashboard server accepts on ?window=. */
export type DashboardWindow = "1h" | "24h"

export type RuleHit = {
  rule_name: string
  actions_applied?: string[]
  terminated?: boolean
  error_message?: string
}

export type AttemptHit = {
  target: string
  started_at: string
  duration_ms?: number
  status_code?: number
  error?: string
  outcome: string
}

export type MessageEntry = {
  event_id: string
  at: string
  correlation_id?: string
  session_id?: string
  session_id_source?: string
  provider?: string
  endpoint?: string
  model?: string
  method?: string
  configuration?: string
  status_code: number
  duration_ms: number
  streaming?: boolean
  upstream_error?: string
  tokens_in?: number
  tokens_out?: number
  tokens_cached?: number
  tokens_cache_creation?: number
  tags?: string[]
  rules_matched?: RuleHit[]
  policy_ref?: string
  attempts?: AttemptHit[]
}

export type MessagesRecentResponse = {
  capacity: number
  entries: MessageEntry[]
}

// A single part of a GenAI message — text, a model-issued tool call, a
// tool-call result, or a reasoning trace. Mirrors the gateway's emitPart JSON
// (cmd/gateway/reporter.go::jsonMap): tool calls carry name + arguments,
// tool-call responses carry a result, everything else carries content.
export type GenAIMessagePart = {
  type: string
  content?: string
  id?: string
  name?: string
  arguments?: unknown
  result?: unknown
}

// One role-tagged turn: [{role, parts}]. The gateway emits input as the latest
// user turn and output as the assistant turn (incl. tool_call parts).
export type GenAIMessage = {
  role: string
  parts?: GenAIMessagePart[]
}

// One available tool the request advertised to the model. Mirrors the gateway's
// jsonToolDefsString output.
export type GenAIToolDefinition = {
  type?: string
  name?: string
  description?: string
  parameters?: unknown
}

// The telemetry-native GenAI content the control plane captured from the
// request span (SLUICE_OTEL_CAPTURE_CONTENT). Each field is the gateway's
// [{role, parts}] / tool-defs JSON. A {"truncated":...} marker replaces the
// object when the captured content exceeded the CP's content cap.
export type GenAIContent = {
  input_messages?: GenAIMessage[]
  output_messages?: GenAIMessage[]
  tool_definitions?: GenAIToolDefinition[]
  system_instructions?: GenAIMessagePart[]
  truncated?: boolean
  original_bytes?: number
}

export type MessageBodyDetail = {
  event_id: string
  request?: string
  request_total_bytes: number
  request_truncated?: boolean
  response?: string
  response_total_bytes: number
  response_truncated?: boolean
  response_assembled?: string
  assembly_partial?: boolean
  request_headers?: Record<string, string[]>
  response_headers?: Record<string, string[]>
  gen_ai_content?: GenAIContent
}
