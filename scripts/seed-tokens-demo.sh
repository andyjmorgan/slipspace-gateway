#!/usr/bin/env bash
#
# seed-tokens-demo.sh — fires a deterministic mix of requests through the
# local dev gateway after staging matching canned responses on the
# mockllm. The canned responses carry real-shape upstream `usage` blocks
# so the gateway's token extractors run, the four gateway.tokens.*
# meters tick up, and the admin dashboard tiles + Top Models token
# columns light up with non-mock-numbers (real meter values, mocked
# upstream).
#
# Prereq: `make dev` running (gateway on :8585, mockllm on :5555 via
# docker compose, NATS on :4222). The admin console listens on the
# bind configured in config-dev/admin.yaml — typically :8081.
#
# Usage:
#   ./scripts/seed-tokens-demo.sh         # fires the default mix
#   ITERATIONS=5 ./scripts/seed-tokens-demo.sh   # repeat the mix N times
#
# Cleanup of registered canned responses is best-effort: the script
# clears the mockllm's response registry on exit so subsequent runs
# do not pile up.

set -euo pipefail

GATEWAY="${GATEWAY:-http://127.0.0.1:8585}"
MOCKLLM="${MOCKLLM:-http://127.0.0.1:5555}"
API_KEY="${SLIPSPACE_DEV_API_KEY:-sk_dev_local_development_only_not_for_production}"
ITERATIONS="${ITERATIONS:-1}"

if ! command -v jq >/dev/null 2>&1; then
  echo "jq is required (brew install jq)" >&2
  exit 1
fi

cleanup() {
  curl -sf -X DELETE "${MOCKLLM}/control/responses" >/dev/null || true
}
trap cleanup EXIT

echo "preflight: gateway ${GATEWAY}/healthz"
if ! curl -sf "${GATEWAY}/healthz" >/dev/null; then
  echo "gateway not reachable at ${GATEWAY}; is \`make dev\` running?" >&2
  exit 1
fi

stage_canned() {
  # Clear before each stage — mockllm's match() returns the first
  # registered response, so accumulating canned bodies collapses every
  # subsequent fire to the original stage. Clear keeps each call's
  # canned response authoritative for the single fire that follows.
  curl -sf -X DELETE "${MOCKLLM}/control/responses" >/dev/null
  curl -sf -X POST "${MOCKLLM}/control/responses" \
    -H 'Content-Type: application/json' \
    -d "$1" >/dev/null
}

post_to_gateway() {
  local path="$1"; shift
  local body="$1"; shift
  curl -sf -X POST "${GATEWAY}${path}" \
    -H "Authorization: Bearer ${API_KEY}" \
    -H 'Content-Type: application/json' \
    "$@" \
    -d "${body}" >/dev/null
}

# OpenAI chat completions — non-streaming, realistic usage block.
seed_openai_chat() {
  local prompt_tokens="$1"; local completion_tokens="$2"; local cached="${3:-0}"
  stage_canned "$(jq -n \
    --arg method "POST" \
    --arg path "/v1/chat/completions" \
    --argjson status 200 \
    --argjson body "$(jq -n --argjson pt "${prompt_tokens}" --argjson ct "${completion_tokens}" --argjson cached "${cached}" '
      {
        id: "chatcmpl-seed",
        object: "chat.completion",
        created: 1700000000,
        model: "gpt-4o-mini",
        choices: [{index: 0, message: {role: "assistant", content: "pong"}, finish_reason: "stop"}],
        usage: {
          prompt_tokens: $pt,
          completion_tokens: $ct,
          total_tokens: ($pt + $ct),
          prompt_tokens_details: {cached_tokens: $cached, audio_tokens: 0},
          completion_tokens_details: {reasoning_tokens: 0, audio_tokens: 0}
        }
      } | tojson')" \
    '{method: $method, path: $path, status: $status, body: $body}')"

  post_to_gateway "/openai/v1/chat/completions" \
    '{"model":"gpt-4o-mini","messages":[{"role":"user","content":"."}]}'
}

# Anthropic — non-stream with cache read + cache creation populated, so
# both TokensCached and TokensCacheCreation surface on the dashboard.
seed_anthropic_messages() {
  local input="$1"; local output="$2"; local cache_read="${3:-0}"; local cache_write="${4:-0}"
  stage_canned "$(jq -n \
    --arg method "POST" \
    --arg path "/v1/messages" \
    --argjson status 200 \
    --argjson body "$(jq -n \
      --argjson it "${input}" --argjson ot "${output}" \
      --argjson cr "${cache_read}" --argjson cw "${cache_write}" '
      {
        id: "msg_seed",
        type: "message",
        role: "assistant",
        content: [{type: "text", text: "pong"}],
        model: "claude-haiku-4-5",
        stop_reason: "end_turn",
        usage: {
          input_tokens: $it,
          output_tokens: $ot,
          cache_read_input_tokens: $cr,
          cache_creation_input_tokens: $cw
        }
      } | tojson')" \
    '{method: $method, path: $path, status: $status, body: $body}')"

  post_to_gateway "/anthropic/v1/messages" \
    '{"model":"claude-haiku-4-5","max_tokens":50,"messages":[{"role":"user","content":"."}]}' \
    -H "x-api-key: placeholder" \
    -H "anthropic-version: 2023-06-01"
}

# Gemini — exercises the derived output = total - prompt path with
# thoughts tokens, the same prod-shape we captured during research.
seed_gemini_generate() {
  local prompt="$1"; local total="$2"; local cached="${3:-0}"
  stage_canned "$(jq -n \
    --arg method "POST" \
    --arg path "/v1beta/models/gemini-2.5-flash:generateContent" \
    --argjson status 200 \
    --argjson body "$(jq -n --argjson p "${prompt}" --argjson t "${total}" --argjson c "${cached}" '
      {
        candidates: [{content: {role: "model", parts: [{text: "pong"}]}, finishReason: "STOP", index: 0}],
        usageMetadata: {
          promptTokenCount: $p,
          totalTokenCount: $t,
          cachedContentTokenCount: $c,
          thoughtsTokenCount: ($t - $p)
        },
        modelVersion: "gemini-2.5-flash"
      } | tojson')" \
    '{method: $method, path: $path, status: $status, body: $body}')"

  post_to_gateway "/gemini/v1beta/models/gemini-2.5-flash:generateContent" \
    '{"contents":[{"parts":[{"text":"."}]}]}' \
    -H "x-goog-api-key: placeholder"
}

for ((i=1; i<=ITERATIONS; i++)); do
  echo "iteration ${i}/${ITERATIONS}"

  # Realistic mixed traffic — small prompts, medium prompts, a couple of
  # cache-hit requests on Anthropic, a Gemini thoughts-heavy response.
  seed_openai_chat 320 78
  seed_openai_chat 1100 215 380
  seed_openai_chat 95 14
  seed_openai_chat 4500 612

  seed_anthropic_messages 480 92
  seed_anthropic_messages 75 12 1200 40
  seed_anthropic_messages 220 45 800 0
  seed_anthropic_messages 2100 310

  seed_gemini_generate 180 250
  seed_gemini_generate 1500 1900 600
  seed_gemini_generate 60 95

  echo "  fired 11 requests"
done

echo
echo "done. open the admin console — dashboard tiles + Top Models tokens"
echo "columns should reflect the seeded traffic."
