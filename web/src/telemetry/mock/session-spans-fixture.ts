// Bundled SessionSpansDTO fixture for the session lifecycle page. Served by
// fetchSessionSpans when GET /api/v1/sessions/{id}/spans 404s (the telemetry
// service predates the spans projection), so the page stays demonstrable
// against an older backend. Entirely synthetic — invented session, agents,
// tools, and content; nothing here is captured traffic.
//
// The shape mirrors span-dto.schema.json (SessionSpansDTO v1) and deliberately
// exercises every renderer-contract path:
//   - 4 conversations (main + three sub-agents), overlapping for concurrency
//   - client tool calls joined to a later span's tool_call_response (rule 4)
//   - one server-executed tool: call + response in the same span (rule 8)
//   - one server-executed tool flagged executor:"server" with NO same-span
//     response (OpenAI web_search folds its result into the model output) —
//     the flag alone must land it in the server tool table
//   - one unjoined tool_call_response (capture-gap honesty path)
//   - one error tool result, one truncated-args call, one raw non-JSON args

import type { SessionSpan } from "@/lib/session-spans"

// FIXTURE_SESSION_ID is the synthetic session the fixture describes; the page
// shows a "fixture data" notice when rendering it in place of live spans.
export const FIXTURE_SESSION_ID = "fixture-session-0001"

const MAIN = FIXTURE_SESSION_ID
const ALPHA = "fixture-agent-alpha"
const BETA = "fixture-agent-beta"
const GAMMA = "fixture-agent-gamma"

export const SESSION_SPANS_FIXTURE: SessionSpan[] = [
  // ── main loop ──────────────────────────────────────────────────────────
  {
    cid: "fx-m1",
    at: "2026-06-01T09:00:00.000Z",
    latency_ms: 5200,
    ttfc_ms: 850,
    status: 200,
    model: "demo-model-large",
    finish_reason: "tool_use",
    session_id: MAIN,
    conversation_id: MAIN,
    parent_conversation_id: null,
    usage: { input: 1200, output: 180, cache_read: 0, cache_creation: 900 },
    output_parts: [
      { type: "text", chars: 210 },
      {
        type: "tool_call",
        id: "fx-tc-001",
        name: "fetch_metrics",
        args: '{"source":"warehouse","period":"2026-Q1","metrics":["revenue","churn","activation"]}',
        args_chars: 84,
      },
    ],
    input_parts: [{ type: "text", chars: 52 }],
    input_text: "Summarize the Q1 metrics and refresh the dashboard.",
    input_text_chars: 52,
    output_text: "Pulling the Q1 metrics from the warehouse first, then I will refresh the dashboard and validate the totals.",
    output_text_chars: 210,
  },
  {
    cid: "fx-m2",
    at: "2026-06-01T09:00:08.400Z",
    latency_ms: 6100,
    ttfc_ms: 700,
    status: 200,
    model: "demo-model-large",
    finish_reason: "tool_use",
    session_id: MAIN,
    conversation_id: MAIN,
    parent_conversation_id: null,
    usage: { input: 2600, output: 240, cache_read: 1100, cache_creation: 400 },
    output_parts: [
      { type: "text", chars: 140 },
      {
        type: "tool_call",
        id: "fx-tc-002",
        name: "spawn_worker",
        args: '{"task":"Render the dashboard charts from the Q1 metrics file"}',
        args_chars: 63,
      },
      {
        type: "tool_call",
        id: "fx-tc-010",
        name: "spawn_worker",
        args: '{"task":"Validate metric totals against the ledger"}',
        args_chars: 52,
      },
    ],
    input_parts: [
      {
        type: "tool_call_response",
        id: "fx-tc-001",
        chars: 1420,
        text: '{"revenue":"4.2M","churn":"2.1%","activation":"38%","rows":412}',
      },
    ],
    input_text: null,
    input_text_chars: null,
    output_text: "Metrics fetched. Spawning one worker to render the charts and another to validate the totals.",
    output_text_chars: 140,
  },
  {
    cid: "fx-m3",
    at: "2026-06-01T09:00:30.000Z",
    latency_ms: 9800,
    ttfc_ms: 1500,
    status: 200,
    model: "demo-model-large",
    finish_reason: "stop",
    session_id: MAIN,
    conversation_id: MAIN,
    parent_conversation_id: null,
    usage: {
      input: 3400,
      output: 520,
      cache_read: 2900,
      cache_creation: 200,
      server_tool_use: { web_search_requests: 1 },
    },
    output_parts: [
      { type: "text", chars: 120 },
      {
        type: "tool_call",
        id: "fx-tc-003",
        name: "knowledge_search",
        args: '{"query":"benchmark dashboards quarterly metrics"}',
        args_chars: 50,
        executor: "server",
      },
      // Same-span response → server-executed tool (renderer-contract rule 8).
      { type: "tool_call_response", id: "fx-tc-003", chars: 2100, executor: "server" },
      // Provider-hosted web_search (OpenAI Responses): flagged executor:server
      // but NO same-span response — its result folds into the model output, so
      // the flag is the only server signal. Must still land in the server tool
      // table, not client.
      {
        type: "tool_call",
        id: "fx-tc-004",
        name: "web_search",
        args: '{"query":"published Q1 SaaS benchmark medians"}',
        args_chars: 47,
        executor: "server",
      },
      { type: "text", chars: 260 },
    ],
    input_parts: [],
    input_text: null,
    input_text_chars: null,
    output_text: "Checking how the numbers compare against published benchmarks while the workers run. The Q1 figures sit comfortably above the median for this segment.",
    output_text_chars: 380,
  },
  {
    cid: "fx-m4",
    at: "2026-06-01T09:00:44.000Z",
    latency_ms: 7100,
    ttfc_ms: 950,
    status: 200,
    model: "demo-model-large",
    finish_reason: "stop",
    session_id: MAIN,
    conversation_id: MAIN,
    parent_conversation_id: null,
    usage: { input: 5200, output: 880, cache_read: 4600, cache_creation: 150 },
    output_parts: [{ type: "text", chars: 2600 }],
    input_parts: [
      {
        type: "tool_call_response",
        id: "fx-tc-002",
        chars: 380,
        text: "Worker finished: dashboard rendered with 4 charts at /out/dashboard.html.",
      },
      {
        type: "tool_call_response",
        id: "fx-tc-010",
        chars: 310,
        text: "Worker finished: ledger validation failed on the revenue query; totals unverified.",
      },
      // No matching tool_call anywhere in the captured spans — exercises the
      // honest "possible capture gap" path in the inspector.
      {
        type: "tool_call_response",
        id: "fx-tc-999",
        chars: 150,
        text: "Stale result from a call captured outside this window.",
      },
    ],
    input_text: null,
    input_text_chars: null,
    output_text: "Summary: Q1 revenue landed at 4.2M with churn at 2.1%. The dashboard has been refreshed with four charts. Note: ledger validation could not be completed, so treat the revenue total as provisional…",
    output_text_chars: 2600,
  },
  {
    cid: "fx-m5",
    at: "2026-06-01T09:01:05.000Z",
    latency_ms: 4100,
    ttfc_ms: 600,
    status: 200,
    model: "demo-model-small",
    finish_reason: "stop",
    session_id: MAIN,
    conversation_id: MAIN,
    parent_conversation_id: null,
    usage: { input: 6100, output: 160, cache_read: 5800, cache_creation: 80 },
    output_parts: [{ type: "text", chars: 300 }],
    input_parts: [{ type: "text", chars: 34 }],
    input_text: "Thanks — anything left to follow up?",
    input_text_chars: 34,
    output_text: "Nothing blocking. The only open item is re-running the ledger validation once the revenue query is fixed.",
    output_text_chars: 300,
  },

  // ── sub-agent alpha: renders the charts ────────────────────────────────
  {
    cid: "fx-a1",
    at: "2026-06-01T09:00:16.000Z",
    latency_ms: 4800,
    ttfc_ms: 620,
    status: 200,
    model: "demo-model-small",
    finish_reason: "tool_use",
    session_id: MAIN,
    conversation_id: ALPHA,
    parent_conversation_id: MAIN,
    usage: { input: 800, output: 90, cache_read: 0, cache_creation: 600 },
    output_parts: [
      {
        type: "tool_call",
        id: "fx-tc-101",
        name: "read_file",
        args: '{"path":"/data/q1-metrics.json"}',
        args_chars: 32,
      },
    ],
    input_parts: [{ type: "text", chars: 61 }],
    input_text: "Render the dashboard charts from the Q1 metrics file",
    input_text_chars: 61,
    output_text: null,
    output_text_chars: null,
  },
  {
    cid: "fx-a2",
    at: "2026-06-01T09:00:24.000Z",
    latency_ms: 7200,
    ttfc_ms: 1100,
    status: 200,
    model: "demo-model-small",
    finish_reason: "tool_use",
    session_id: MAIN,
    conversation_id: ALPHA,
    parent_conversation_id: MAIN,
    usage: { input: 1700, output: 1450, cache_read: 750, cache_creation: 120 },
    output_parts: [
      { type: "reasoning", chars: 340 },
      {
        type: "tool_call",
        id: "fx-tc-102",
        name: "write_file",
        // Server-capped arguments: args_chars carries the uncapped total, so
        // the inspector shows a "showing first N of M" truncation notice.
        args: '{"path":"/out/dashboard.html","contents":"<!doctype html><html><head><title>Q1 Dashboard</title>',
        args_chars: 4800,
      },
    ],
    input_parts: [
      {
        type: "tool_call_response",
        id: "fx-tc-101",
        chars: 900,
        text: '{"revenue":[1.1,1.0,2.1],"churn":[2.4,2.2,2.1],"activation":[31,35,38]}',
      },
    ],
    input_text: null,
    input_text_chars: null,
    output_text: null,
    output_text_chars: null,
  },
  {
    cid: "fx-a3",
    at: "2026-06-01T09:00:36.000Z",
    latency_ms: 3900,
    ttfc_ms: 480,
    status: 200,
    model: "demo-model-small",
    finish_reason: "stop",
    session_id: MAIN,
    conversation_id: ALPHA,
    parent_conversation_id: MAIN,
    usage: { input: 2400, output: 70, cache_read: 2100, cache_creation: 60 },
    output_parts: [{ type: "text", chars: 33 }],
    input_parts: [
      { type: "tool_call_response", id: "fx-tc-102", chars: 60, text: "wrote /out/dashboard.html (4.7kB)" },
    ],
    input_text: null,
    input_text_chars: null,
    output_text: "Dashboard rendered with 4 charts.",
    output_text_chars: 33,
  },

  // ── sub-agent beta: validates the totals (and fails) ───────────────────
  {
    cid: "fx-b1",
    at: "2026-06-01T09:00:17.500Z",
    latency_ms: 5600,
    ttfc_ms: 900,
    status: 200,
    model: "demo-model-small",
    finish_reason: "tool_use",
    session_id: MAIN,
    conversation_id: BETA,
    parent_conversation_id: MAIN,
    usage: { input: 760, output: 110, cache_read: 0, cache_creation: 580 },
    output_parts: [
      {
        type: "tool_call",
        id: "fx-tc-201",
        name: "run_query",
        // Capped mid-string → not valid JSON: exercises the visible raw
        // fallback in the inspector instead of the parsed key/value grid.
        args: '{"sql":"SELECT SUM(amount) FROM ledger WHERE quarter=',
        args_chars: 88,
      },
    ],
    input_parts: [{ type: "text", chars: 41 }],
    input_text: "Validate metric totals against the ledger",
    input_text_chars: 41,
    output_text: null,
    output_text_chars: null,
  },
  {
    cid: "fx-b2",
    at: "2026-06-01T09:00:27.000Z",
    latency_ms: 4400,
    ttfc_ms: 520,
    status: 200,
    model: "demo-model-small",
    finish_reason: "stop",
    session_id: MAIN,
    conversation_id: BETA,
    parent_conversation_id: MAIN,
    usage: { input: 1500, output: 95, cache_read: 700, cache_creation: 50 },
    output_parts: [{ type: "text", chars: 78 }],
    input_parts: [
      {
        type: "tool_call_response",
        id: "fx-tc-201",
        chars: 240,
        text: "error: column \"quarter\" does not exist in relation ledger",
        is_error: true,
      },
    ],
    input_text: null,
    input_text_chars: null,
    output_text: "The validation query failed (schema mismatch), so the totals were not verified.",
    output_text_chars: 78,
  },

  // ── sub-agent gamma: deployment status check ───────────────────────────
  {
    cid: "fx-c1",
    at: "2026-06-01T09:00:50.000Z",
    latency_ms: 3200,
    ttfc_ms: 400,
    status: 200,
    model: "demo-model-small",
    finish_reason: "tool_use",
    session_id: MAIN,
    conversation_id: GAMMA,
    parent_conversation_id: MAIN,
    usage: { input: 700, output: 60, cache_read: 0, cache_creation: 520 },
    output_parts: [
      {
        type: "tool_call",
        id: "fx-tc-301",
        name: "http_get",
        args: '{"url":"https://status.example.test/api/v2/summary","timeout_s":10}',
        args_chars: 67,
      },
    ],
    input_parts: [{ type: "text", chars: 27 }],
    input_text: "Collect deployment status",
    input_text_chars: 27,
    output_text: null,
    output_text_chars: null,
  },
  {
    cid: "fx-c2",
    at: "2026-06-01T09:00:56.000Z",
    latency_ms: 2800,
    ttfc_ms: 350,
    status: 200,
    model: "demo-model-small",
    finish_reason: "stop",
    session_id: MAIN,
    conversation_id: GAMMA,
    parent_conversation_id: MAIN,
    usage: { input: 1300, output: 55, cache_read: 640, cache_creation: 40 },
    output_parts: [{ type: "text", chars: 46 }],
    input_parts: [
      {
        type: "tool_call_response",
        id: "fx-tc-301",
        chars: 540,
        text: '{"environments":{"staging":"green","prod":"green"},"pending_rollouts":0}',
      },
    ],
    input_text: null,
    input_text_chars: null,
    output_text: "All environments green; no pending rollouts.",
    output_text_chars: 46,
  },
]
